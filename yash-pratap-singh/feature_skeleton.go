// Package dpfadapter implements Pattern 1 — the Host-Cluster Symmetric Shim.
//
// The SfcTranslationReconciler watches an OPI ServiceFunctionChain in the host
// cluster and translates it into NVIDIA DPF host-cluster template objects
// (DPUService, DPUServiceChain), then mirrors DPF's status back onto the OPI CR.
// It operates on *unstructured* objects on purpose: this decouples OPI from
// DPF's compiled Go types, so a wrong DPF field name is a one-line change here
// rather than a recompile against a different DPF module version.
//
// NOTE ON VERIFICATION:
//   - This file targets sigs.k8s.io/controller-runtime v0.20.x (the version
//     openshift/dpu-operator pins at HEAD 3092bcbe).
//   - It was NOT compiled in the authoring sandbox (no Go toolchain there); it
//     is written to compile in the OPI repo, whose go.mod already vendors these
//     deps. Compile with `go build ./...` in that module.
//   - Every DPF GroupVersionKind and spec field path marked "VALIDATION
//     BOUNDARY" is asserted from the DPF analysis document, not confirmed
//     against DPF source. Confirm before relying on them.
//
// The reconcile *decision logic* (translation + both gates) is deliberately
// factored into pure functions with no client dependency, so it is unit-testable
// with `go test` alone — no apiserver, no envtest binaries, no hardware. See
// feature_skeleton_test.go.
package dpfadapter

import (
	"context"
	"fmt"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// Field-manager, association, and finalizer identifiers.
const (
	// FieldManager scopes the adapter's Server-Side Apply ownership. DPF owns
	// status; the adapter owns only the spec fields it applies under this name.
	FieldManager = "opi-dpf-translator-shim"

	// AnnotationOwnerUID records the owning OPI CR's UID on each DPF object.
	AnnotationOwnerUID = "opi.openshift.io/uid"
	// AnnotationOwnerName / AnnotationOwnerNamespace let a DPF-child watch map
	// back to the owning OPI CR's request key without a full cache scan.
	AnnotationOwnerName      = "opi.openshift.io/owning-sfc-name"
	AnnotationOwnerNamespace = "opi.openshift.io/owning-sfc-namespace"
	// LabelOwningSFCUID indexes DPF children for list-by-owner.
	LabelOwningSFCUID = "opi.openshift.io/owning-sfc-uid"

	// FinalizerNVIDIA gates ordered, DPF-finalizer-respecting teardown.
	FinalizerNVIDIA = "dpu.openshift.io/nvidia-cleanup"

	condReady        = "Ready"
	reasonWaitingDPF = "WaitingOnDPF"
	reasonReady      = "ComponentsReady"
	reasonProgress   = "Progressing"
)

const (
	requeueSteadyState = 2 * time.Minute
	requeueProgressing = 15 * time.Second
)

// GroupVersionKinds. The OPI SFC GVK is verified against the repo; the DPF GVKs
// are VALIDATION BOUNDARIES (confirm group/version/kind against DPF source).
var (
	sfcGVK = schema.GroupVersionKind{
		Group: "config.openshift.io", Version: "v1", Kind: "ServiceFunctionChain",
	}
	dpuServiceGVK = schema.GroupVersionKind{ // VALIDATION BOUNDARY
		Group: "svc.dpu.nvidia.com", Version: "v1alpha1", Kind: "DPUService",
	}
	dpuServiceChainGVK = schema.GroupVersionKind{ // VALIDATION BOUNDARY
		Group: "svc.dpu.nvidia.com", Version: "v1alpha1", Kind: "DPUServiceChain",
	}
)

// SfcTranslationReconciler is the Pattern 1 adapter.
type SfcTranslationReconciler struct {
	Client client.Client
	// DPFNamespace is the host-cluster namespace in which DPF template objects
	// are created.
	DPFNamespace string
}

// Reconcile implements the full loop: finalizer management, SSA translation,
// two-hop status aggregation gated by the ObservedGeneration and Equality gates,
// and finalizer-ordered teardown.
func (r *SfcTranslationReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	sfc := &unstructured.Unstructured{}
	sfc.SetGroupVersionKind(sfcGVK)
	if err := r.Client.Get(ctx, req.NamespacedName, sfc); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !sfc.GetDeletionTimestamp().IsZero() {
		return r.reconcileDelete(ctx, sfc)
	}

	if !controllerutil.ContainsFinalizer(sfc, FinalizerNVIDIA) {
		controllerutil.AddFinalizer(sfc, FinalizerNVIDIA)
		if err := r.Client.Update(ctx, sfc); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	// Desired state: translate and Server-Side Apply the DPF host templates.
	children, err := translateServiceFunctionChain(sfc, r.DPFNamespace)
	if err != nil {
		return ctrl.Result{}, err
	}
	for _, child := range children {
		if err := r.Client.Patch(ctx, child, client.Apply,
			client.FieldOwner(FieldManager), client.ForceOwnership); err != nil {
			return ctrl.Result{}, fmt.Errorf("SSA apply %s/%s: %w",
				child.GetKind(), child.GetName(), err)
		}
	}

	// Status: observe DPF children (the host side of the two-hop loop).
	statuses, err := r.observeChildren(ctx, sfc)
	if err != nil {
		return ctrl.Result{}, err
	}

	// The ObservedGeneration Gate lives inside buildTargetConditions: it returns
	// a WaitingOnDPF/Progressing condition unless every child has caught up to
	// its own spec (observedGeneration >= generation).
	target := buildTargetConditions(statuses, sfc.GetGeneration())
	current := extractConditions(sfc)

	// The Equality Gate: skip the /status write entirely when nothing changed.
	// This is what bounds writes to distinct transitions and halts self-trigger
	// loops (a /status write re-enqueues the object, but the recomputed target
	// then equals current, so no further write happens).
	if conditionsSemanticallyEqual(current, target) {
		return ctrl.Result{RequeueAfter: requeueSteadyState}, nil
	}

	setConditions(sfc, target)
	if err := r.Client.Status().Update(ctx, sfc); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: requeueProgressing}, nil
}

// reconcileDelete issues deletes for DPF children and defers removal of the OPI
// finalizer until DPF's own finalizers have run and the children are gone —
// deterministic teardown that respects DPF's lifecycle rather than racing GC.
func (r *SfcTranslationReconciler) reconcileDelete(ctx context.Context, sfc *unstructured.Unstructured) (ctrl.Result, error) {
	remaining := 0
	for _, gvk := range []schema.GroupVersionKind{dpuServiceGVK, dpuServiceChainGVK} {
		list := newList(gvk)
		if err := r.Client.List(ctx, list,
			client.InNamespace(r.DPFNamespace),
			client.MatchingLabels{LabelOwningSFCUID: string(sfc.GetUID())}); err != nil {
			return ctrl.Result{}, err
		}
		for i := range list.Items {
			remaining++
			if err := r.Client.Delete(ctx, &list.Items[i]); err != nil && !apierrors.IsNotFound(err) {
				return ctrl.Result{}, err
			}
		}
	}
	if remaining > 0 {
		return ctrl.Result{RequeueAfter: requeueProgressing}, nil
	}
	controllerutil.RemoveFinalizer(sfc, FinalizerNVIDIA)
	if err := r.Client.Update(ctx, sfc); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// observeChildren lists the DPF children owned by this SFC and projects each to
// the minimal status the decision logic needs.
func (r *SfcTranslationReconciler) observeChildren(ctx context.Context, sfc *unstructured.Unstructured) ([]dpfChildStatus, error) {
	var out []dpfChildStatus
	for _, gvk := range []schema.GroupVersionKind{dpuServiceGVK, dpuServiceChainGVK} {
		list := newList(gvk)
		if err := r.Client.List(ctx, list,
			client.InNamespace(r.DPFNamespace),
			client.MatchingLabels{LabelOwningSFCUID: string(sfc.GetUID())}); err != nil {
			return nil, err
		}
		for i := range list.Items {
			out = append(out, childStatusOf(&list.Items[i]))
		}
	}
	return out, nil
}

// SetupWithManager wires the strict spec predicate and the DPF-child back-mapping.
func (r *SfcTranslationReconciler) SetupWithManager(mgr ctrl.Manager) error {
	sfc := &unstructured.Unstructured{}
	sfc.SetGroupVersionKind(sfcGVK)

	dpuSvc := &unstructured.Unstructured{}
	dpuSvc.SetGroupVersionKind(dpuServiceGVK)

	dpuChain := &unstructured.Unstructured{}
	dpuChain.SetGroupVersionKind(dpuServiceChainGVK)

	return ctrl.NewControllerManagedBy(mgr).
		// Spec watch fires only on generation change, so status-only writes to
		// the SFC never trigger a spec reconcile cascade.
		For(sfc, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		// DPF-child watches map back to the owning SFC and fire only on
		// status-relevant deltas.
		Watches(dpuSvc, handler.EnqueueRequestsFromMapFunc(r.mapDPFChildToSFC),
			builder.WithPredicates(dpfStatusChanged())).
		Watches(dpuChain, handler.EnqueueRequestsFromMapFunc(r.mapDPFChildToSFC),
			builder.WithPredicates(dpfStatusChanged())).
		Complete(r)
}

func (r *SfcTranslationReconciler) mapDPFChildToSFC(_ context.Context, obj client.Object) []reconcile.Request {
	ann := obj.GetAnnotations()
	name, ns := ann[AnnotationOwnerName], ann[AnnotationOwnerNamespace]
	if name == "" || ns == "" {
		return nil
	}
	return []reconcile.Request{{NamespacedName: types.NamespacedName{Name: name, Namespace: ns}}}
}

// dpfStatusChanged fires on create/delete and on updates where observedGeneration
// or the Ready condition changed — not on every cache resync.
func dpfStatusChanged() predicate.Predicate {
	return predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldU, ok1 := e.ObjectOld.(*unstructured.Unstructured)
			newU, ok2 := e.ObjectNew.(*unstructured.Unstructured)
			if !ok1 || !ok2 {
				return true
			}
			return childObservedGeneration(oldU) != childObservedGeneration(newU) ||
				childReadyStatus(oldU) != childReadyStatus(newU)
		},
	}
}

// ---------------------------------------------------------------------------
// Pure decision logic (no client dependency) — unit-testable with `go test`.
// ---------------------------------------------------------------------------

// dpfChildStatus is the minimal projection of a DPF child's status the adapter
// needs to decide the OPI CR's status.
type dpfChildStatus struct {
	Name               string
	Generation         int64
	ObservedGeneration int64
	ReadyStatus        string // "True" / "False" / "" (absent)
}

// dpfConverged is the ObservedGeneration Gate for a single child.
func dpfConverged(c dpfChildStatus) bool {
	return c.Generation > 0 && c.ObservedGeneration >= c.Generation
}

// buildTargetConditions is the projection F: it maps observed DPF child status
// to the target OPI condition set. It is pure and deterministic — identical
// input yields identical output — which is what makes the Equality Gate able to
// suppress redundant writes.
func buildTargetConditions(children []dpfChildStatus, opiGeneration int64) []metav1.Condition {
	if len(children) == 0 {
		return []metav1.Condition{{
			Type: condReady, Status: metav1.ConditionFalse,
			Reason: reasonProgress, Message: "no DPF children observed yet",
			ObservedGeneration: opiGeneration,
		}}
	}
	for _, c := range children {
		if !dpfConverged(c) {
			return []metav1.Condition{{
				Type: condReady, Status: metav1.ConditionFalse,
				Reason: reasonWaitingDPF,
				Message: fmt.Sprintf("DPF child %q not reconciled (observedGeneration %d < generation %d)",
					c.Name, c.ObservedGeneration, c.Generation),
				ObservedGeneration: opiGeneration,
			}}
		}
	}
	for _, c := range children {
		if c.ReadyStatus != string(metav1.ConditionTrue) {
			return []metav1.Condition{{
				Type: condReady, Status: metav1.ConditionFalse,
				Reason: reasonProgress, Message: "DPF children converged but not all Ready",
				ObservedGeneration: opiGeneration,
			}}
		}
	}
	return []metav1.Condition{{
		Type: condReady, Status: metav1.ConditionTrue,
		Reason: reasonReady, Message: "all DPF children ready",
		ObservedGeneration: opiGeneration,
	}}
}

// conditionsSemanticallyEqual is the Equality Gate. It compares the meaningful
// fields only, deliberately ignoring LastTransitionTime (a raw reflect.DeepEqual
// would be defeated by the timestamp and would never suppress a write).
func conditionsSemanticallyEqual(a, b []metav1.Condition) bool {
	if len(a) != len(b) {
		return false
	}
	idx := make(map[string]metav1.Condition, len(a))
	for _, c := range a {
		idx[c.Type] = c
	}
	for _, c := range b {
		p, ok := idx[c.Type]
		if !ok {
			return false
		}
		if p.Status != c.Status || p.Reason != c.Reason ||
			p.Message != c.Message || p.ObservedGeneration != c.ObservedGeneration {
			return false
		}
	}
	return true
}

// translateServiceFunctionChain maps one OPI ServiceFunctionChain to its DPF
// host templates. Spec field paths marked below are VALIDATION BOUNDARIES.
func translateServiceFunctionChain(sfc *unstructured.Unstructured, dpfNamespace string) ([]*unstructured.Unstructured, error) {
	uid := string(sfc.GetUID())
	name := sfc.GetName()
	image := firstNetworkFunctionImage(sfc)

	dpuService := newDPFChild(dpuServiceGVK, name+"-svc", dpfNamespace, sfc)
	// VALIDATION BOUNDARY: real DPUService carries spec.helmChart{source,values};
	// this placeholder holds the workload image at a stand-in path.
	if err := unstructured.SetNestedField(dpuService.Object, image, "spec", "image"); err != nil {
		return nil, err
	}

	dpuServiceChain := newDPFChild(dpuServiceChainGVK, name+"-chain", dpfNamespace, sfc)
	// VALIDATION BOUNDARY: DPUServiceChain fans out via a DPUClusterSelector.
	if err := unstructured.SetNestedStringMap(dpuServiceChain.Object,
		map[string]string{LabelOwningSFCUID: uid}, "spec", "clusterSelector", "matchLabels"); err != nil {
		return nil, err
	}

	return []*unstructured.Unstructured{dpuService, dpuServiceChain}, nil
}

func newDPFChild(gvk schema.GroupVersionKind, name, namespace string, sfc *unstructured.Unstructured) *unstructured.Unstructured {
	c := &unstructured.Unstructured{}
	c.SetGroupVersionKind(gvk)
	c.SetName(name)
	c.SetNamespace(namespace)
	c.SetAnnotations(map[string]string{
		AnnotationOwnerUID:       string(sfc.GetUID()),
		AnnotationOwnerName:      sfc.GetName(),
		AnnotationOwnerNamespace: sfc.GetNamespace(),
	})
	c.SetLabels(map[string]string{LabelOwningSFCUID: string(sfc.GetUID())})
	return c
}

func firstNetworkFunctionImage(sfc *unstructured.Unstructured) string {
	nfs, found, err := unstructured.NestedSlice(sfc.Object, "spec", "networkFunctions")
	if err != nil || !found || len(nfs) == 0 {
		return ""
	}
	m, ok := nfs[0].(map[string]interface{})
	if !ok {
		return ""
	}
	img, _, _ := unstructured.NestedString(m, "image")
	return img
}

// ---------------------------------------------------------------------------
// unstructured helpers
// ---------------------------------------------------------------------------

func newList(gvk schema.GroupVersionKind) *unstructured.UnstructuredList {
	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(schema.GroupVersionKind{
		Group: gvk.Group, Version: gvk.Version, Kind: gvk.Kind + "List",
	})
	return list
}

func childStatusOf(u *unstructured.Unstructured) dpfChildStatus {
	return dpfChildStatus{
		Name:               u.GetName(),
		Generation:         u.GetGeneration(),
		ObservedGeneration: childObservedGeneration(u),
		ReadyStatus:        childReadyStatus(u),
	}
}

func childObservedGeneration(u *unstructured.Unstructured) int64 {
	og, found, err := unstructured.NestedInt64(u.Object, "status", "observedGeneration")
	if err != nil || !found {
		return 0
	}
	return og
}

func childReadyStatus(u *unstructured.Unstructured) string {
	conds, found, err := unstructured.NestedSlice(u.Object, "status", "conditions")
	if err != nil || !found {
		return ""
	}
	for _, raw := range conds {
		m, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		if t, _, _ := unstructured.NestedString(m, "type"); t == condReady {
			s, _, _ := unstructured.NestedString(m, "status")
			return s
		}
	}
	return ""
}

func extractConditions(u *unstructured.Unstructured) []metav1.Condition {
	raw, found, err := unstructured.NestedSlice(u.Object, "status", "conditions")
	if err != nil || !found {
		return nil
	}
	out := make([]metav1.Condition, 0, len(raw))
	for _, r := range raw {
		m, ok := r.(map[string]interface{})
		if !ok {
			continue
		}
		t, _, _ := unstructured.NestedString(m, "type")
		s, _, _ := unstructured.NestedString(m, "status")
		reason, _, _ := unstructured.NestedString(m, "reason")
		msg, _, _ := unstructured.NestedString(m, "message")
		og, _, _ := unstructured.NestedInt64(m, "observedGeneration")
		out = append(out, metav1.Condition{
			Type: t, Status: metav1.ConditionStatus(s),
			Reason: reason, Message: msg, ObservedGeneration: og,
		})
	}
	return out
}

func setConditions(u *unstructured.Unstructured, conds []metav1.Condition) {
	raw := make([]interface{}, 0, len(conds))
	for _, c := range conds {
		raw = append(raw, map[string]interface{}{
			"type":               c.Type,
			"status":             string(c.Status),
			"reason":             c.Reason,
			"message":            c.Message,
			"observedGeneration": c.ObservedGeneration,
			"lastTransitionTime": time.Now().UTC().Format(time.RFC3339),
		})
	}
	_ = unstructured.SetNestedSlice(u.Object, raw, "status", "conditions")
}
