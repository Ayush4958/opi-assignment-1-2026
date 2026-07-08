// feature_skeleton.go
//
// adapter controller for the NVIDIA integration described in
// architecture_design.md. Translates OPI's Dpu resource into DPF's own
// CRDs (BFB, DPUSet) and reads status back. Doesn't import DPF's go module
// at all, everything goes through unstructured.Unstructured + GVK so I'm
// not tied to their release cycle.
//
// kept it all in one file/package for now since the real Dpu/DpuSpec/etc
// types would normally live in api/v1alpha1 in opi-operator, but that
// package doesn't exist in my fork yet so I just inlined a minimal version
// here instead of pointing at something that isn't there.
//
// no go.mod'd this against a real cluster yet, just wanted it structurally
// right so I have something to build off of. couple DPF field names
// (bfbVersion, docaProfile, status.ready) are TODO - need to check them
// against the actual pinned DPF CRD before this touches hardware.
//
// deps this needs if someone wants to actually build it:
//   k8s.io/apimachinery v0.31.2
//   sigs.k8s.io/controller-runtime v0.19.1
package nvidiaadapter

import (
	"context"
	"fmt"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

// stand-ins for the real OPI API types (normally api/v1alpha1). inlined
// here so this file is self contained instead of importing a package that
// doesn't exist in this form yet.

// Vendor identifies which offload stack a Dpu resource targets.
type Vendor string

const (
	VendorIntel   Vendor = "intel"
	VendorMarvell Vendor = "marvell"
	VendorNVIDIA  Vendor = "nvidia"
)

// NvidiaConfig is the only vendor-specific bit the core OPI reconciler
// needs to pass through - it doesn't need to understand these fields.
type NvidiaConfig struct {
	BFBVersion     string
	DOCAProfile    string
	DPUSetSelector map[string]string
}

// DpuSpec is the desired state of a Dpu resource.
type DpuSpec struct {
	Vendor Vendor
	Nvidia NvidiaConfig
}

// DpuStatus is the observed state, aggregated (for NVIDIA) from DPF's own
// DPUSet status by the adapter controller below.
type DpuStatus struct {
	Phase string
}

// Dpu stands in for the real CRD-backed type, good enough to act like a
// client.Object for this skeleton.
type Dpu struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   DpuSpec
	Status DpuStatus
}

// DeepCopyObject satisfies runtime.Object - normally controller-gen would
// write this, hand-rolling it here. (make sure to actually deep copy the
// map, learned that one the hard way before)
func (d *Dpu) DeepCopyObject() runtime.Object {
	if d == nil {
		return nil
	}
	out := *d
	out.ObjectMeta = *d.ObjectMeta.DeepCopy()
	if d.Spec.Nvidia.DPUSetSelector != nil {
		out.Spec.Nvidia.DPUSetSelector = make(map[string]string, len(d.Spec.Nvidia.DPUSetSelector))
		for k, v := range d.Spec.Nvidia.DPUSetSelector {
			out.Spec.Nvidia.DPUSetSelector[k] = v
		}
	}
	return &out
}

// DPF GVKs - only thing this controller needs to know about DPF, no
// import of their actual API package.
var dpfGroupVersion = schema.GroupVersion{Group: "dpu.nvidia.com", Version: "v1alpha1"}

var (
	gvkBFB    = dpfGroupVersion.WithKind("BFB")
	gvkDPUSet = dpfGroupVersion.WithKind("DPUSet")
)

// DpuPhase is the simplified status the adapter surfaces on the Dpu resource,
// aggregated from whatever DPF actually reports on DPUSet.status.
type DpuPhase string

const (
	PhasePending      DpuPhase = "Pending"
	PhaseProvisioning DpuPhase = "Provisioning"
	PhaseReady        DpuPhase = "Ready"
	PhaseFailed       DpuPhase = "Failed"
)

const (
	ownerFinalizer         = "nvidia-adapter.opiproject.org/cleanup"
	defaultRequeueInterval = 10 * time.Second
)

// NvidiaAdapterReconciler is the new piece from this assignment - it bridges
// OPI's Dpu resource and NVIDIA's unmodified DPF operator.
type NvidiaAdapterReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// Reconcile implements the controller-runtime loop.
func (r *NvidiaAdapterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)

	var dpu Dpu
	if err := r.Get(ctx, req.NamespacedName, &dpu); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("fetching Dpu %s: %w", req.NamespacedName, err)
	}

	if dpu.Spec.Vendor != VendorNVIDIA {
		// Not mine - Intel/Marvell controllers own this one.
		return ctrl.Result{}, nil
	}

	if !dpu.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, &dpu)
	}

	if !containsString(dpu.Finalizers, ownerFinalizer) {
		dpu.Finalizers = append(dpu.Finalizers, ownerFinalizer)
		if err := r.Update(ctx, &dpu); err != nil {
			return ctrl.Result{}, fmt.Errorf("adding finalizer: %w", err)
		}
	}

	bfb := r.buildBFB(&dpu)
	dpuSet := r.buildDPUSet(&dpu)

	if err := r.applyOwned(ctx, &dpu, bfb); err != nil {
		return ctrl.Result{}, fmt.Errorf("applying BFB: %w", err)
	}
	if err := r.applyOwned(ctx, &dpu, dpuSet); err != nil {
		return ctrl.Result{}, fmt.Errorf("applying DPUSet: %w", err)
	}

	phase, err := r.aggregateStatus(ctx, &dpu)
	if err != nil {
		log.Error(err, "failed to read DPF status back")
		phase = PhaseFailed
	}

	dpu.Status.Phase = string(phase)
	if err := r.Status().Update(ctx, &dpu); err != nil {
		return ctrl.Result{}, fmt.Errorf("updating Dpu status: %w", err)
	}

	if phase == PhaseReady || phase == PhaseFailed {
		return ctrl.Result{}, nil
	}
	// still provisioning, poll again shortly
	return ctrl.Result{RequeueAfter: defaultRequeueInterval}, nil
}

// buildBFB turns the OPI-side spec into a DPF BFB object.
// TODO: confirm bfbVersion / docaProfile field names against the pinned DPF CRD version.
func (r *NvidiaAdapterReconciler) buildBFB(dpu *Dpu) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(gvkBFB)
	obj.SetName(fmt.Sprintf("%s-bfb", dpu.Name))
	obj.SetNamespace(dpu.Namespace)

	_ = unstructured.SetNestedField(obj.Object, dpu.Spec.Nvidia.BFBVersion, "spec", "bfbVersion")
	_ = unstructured.SetNestedField(obj.Object, dpu.Spec.Nvidia.DOCAProfile, "spec", "docaProfile")

	return obj
}

// buildDPUSet turns the OPI-side spec into a DPF DPUSet object.
func (r *NvidiaAdapterReconciler) buildDPUSet(dpu *Dpu) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(gvkDPUSet)
	obj.SetName(fmt.Sprintf("%s-dpuset", dpu.Name))
	obj.SetNamespace(dpu.Namespace)

	if len(dpu.Spec.Nvidia.DPUSetSelector) > 0 {
		_ = unstructured.SetNestedStringMap(obj.Object, dpu.Spec.Nvidia.DPUSetSelector, "spec", "nodeSelector")
	}
	_ = unstructured.SetNestedField(obj.Object, fmt.Sprintf("%s-bfb", dpu.Name), "spec", "bfbRef", "name")

	return obj
}

// applyOwned sets the owner ref back to the Dpu and does create-or-update.
func (r *NvidiaAdapterReconciler) applyOwned(ctx context.Context, dpu *Dpu, obj *unstructured.Unstructured) error {
	if err := controllerutil.SetControllerReference(dpu, obj, r.Scheme); err != nil {
		return fmt.Errorf("setting owner reference: %w", err)
	}

	existing := &unstructured.Unstructured{}
	existing.SetGroupVersionKind(obj.GroupVersionKind())
	err := r.Get(ctx, client.ObjectKeyFromObject(obj), existing)
	switch {
	case apierrors.IsNotFound(err):
		return r.Create(ctx, obj)
	case err != nil:
		return err
	default:
		obj.SetResourceVersion(existing.GetResourceVersion())
		return r.Update(ctx, obj)
	}
}

// aggregateStatus reads DPUSet.status (written by DPF, unmodified) and
// collapses it into Pending/Provisioning/Ready/Failed for the OPI side.
// TODO: confirm status.ready is actually the right field once I have a real DPF CRD to check.
func (r *NvidiaAdapterReconciler) aggregateStatus(ctx context.Context, dpu *Dpu) (DpuPhase, error) {
	dpuSet := &unstructured.Unstructured{}
	dpuSet.SetGroupVersionKind(gvkDPUSet)
	key := client.ObjectKey{Namespace: dpu.Namespace, Name: fmt.Sprintf("%s-dpuset", dpu.Name)}

	if err := r.Get(ctx, key, dpuSet); err != nil {
		if apierrors.IsNotFound(err) {
			return PhasePending, nil
		}
		return PhaseFailed, err
	}

	ready, found, err := unstructured.NestedBool(dpuSet.Object, "status", "ready")
	if err != nil {
		return PhaseFailed, err
	}
	if !found {
		return PhaseProvisioning, nil
	}
	if ready {
		return PhaseReady, nil
	}
	return PhaseProvisioning, nil
}

// reconcileDelete deletes the DPF-side objects and waits for DPF's own
// finalizer to finish hardware teardown before releasing the OPI finalizer.
func (r *NvidiaAdapterReconciler) reconcileDelete(ctx context.Context, dpu *Dpu) (ctrl.Result, error) {
	if !containsString(dpu.Finalizers, ownerFinalizer) {
		return ctrl.Result{}, nil
	}

	dpuSet := &unstructured.Unstructured{}
	dpuSet.SetGroupVersionKind(gvkDPUSet)
	key := client.ObjectKey{Namespace: dpu.Namespace, Name: fmt.Sprintf("%s-dpuset", dpu.Name)}

	err := r.Get(ctx, key, dpuSet)
	switch {
	case apierrors.IsNotFound(err):
		// DPF has finished tearing down on the hardware side - safe to let go.
		dpu.Finalizers = removeString(dpu.Finalizers, ownerFinalizer)
		if err := r.Update(ctx, dpu); err != nil {
			return ctrl.Result{}, fmt.Errorf("removing finalizer: %w", err)
		}
		return ctrl.Result{}, nil
	case err != nil:
		return ctrl.Result{}, err
	default:
		if dpuSet.GetDeletionTimestamp().IsZero() {
			if err := r.Delete(ctx, dpuSet); err != nil && !apierrors.IsNotFound(err) {
				return ctrl.Result{}, err
			}
		}
		// DPF's own finalizer is still running hardware deprovisioning -
		// come back and check again shortly instead of blocking here.
		return ctrl.Result{RequeueAfter: defaultRequeueInterval}, nil
	}
}

// SetupWithManager wires this reconciler into the manager, watching both the
// Dpu type and the owned DPUSet kind.
func (r *NvidiaAdapterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	dpuSetWatch := &unstructured.Unstructured{}
	dpuSetWatch.SetGroupVersionKind(gvkDPUSet)

	return ctrl.NewControllerManagedBy(mgr).
		For(&Dpu{}).
		Owns(dpuSetWatch).
		Complete(r)
}

// small helpers
func containsString(list []string, s string) bool {
	for _, item := range list {
		if item == s {
			return true
		}
	}
	return false
}

func removeString(list []string, s string) []string {
	out := make([]string, 0, len(list))
	for _, item := range list {
		if item != s {
			out = append(out, item)
		}
	}
	return out
}