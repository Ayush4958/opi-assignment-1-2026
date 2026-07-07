// Package controllers contains the foundational reconciliation-loop and
// adapter-pattern skeleton for integrating NVIDIA DPF (DOCA Platform
// Framework) support into the vendor-neutral OPI DPU Operator.
//
// This file is a structural skeleton only: it is intended to compile
// against the standard controller-runtime/kubebuilder scaffolding and
// demonstrates the CRD Translation Layer + Go Adapter Pattern described in
// architecture_design.md. Method bodies contain TODOs where vendor-specific
// or cluster-interaction logic would live; the goal here is to fix the
// *shape* of the integration (types, interfaces, control flow) so that
// implementation work can proceed without further architectural decisions.
package controllers

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// ---------------------------------------------------------------------------
// 1. OPI CRD TYPES (opi.io/v1alpha1)
//
// Vendor-neutral, hardware-agnostic API surface. No NVIDIA-specific field
// names are permitted here; vendor semantics are introduced exclusively
// inside the Translation Layer and Adapter (Sections 3-4).
// ---------------------------------------------------------------------------

// OpiDpuDeviceSpec is the desired state of a DPU device, as expressed by the
// user or a GitOps controller, independent of the underlying silicon vendor.
type OpiDpuDeviceSpec struct {
	// Vendor selects which VendorAdapter implementation reconciles this CR.
	// e.g. "nvidia", "marvel", "intel", "amd".
	Vendor string `json:"vendor"`

	// DPUSelector identifies which physical host/DPU this CR targets.
	DPUSelector DPUSelector `json:"dpuSelector"`

	// BFB describes the firmware/OS image to provision onto the DPU.
	BFB BFBSpec `json:"bfb"`

	// Networking describes the desired DPU networking mode.
	Networking NetworkingSpec `json:"networking"`

	// Resources describes DPU-side resource allocation (hugepages, VFs).
	Resources ResourceSpec `json:"resources,omitempty"`
}

// DPUSelector pins a CR to a specific node/DPU.
type DPUSelector struct {
	NodeName string `json:"nodeName"`
}

// BFBSpec describes the DPU firmware/OS bundle.
type BFBSpec struct {
	FirmwareVersion string `json:"firmwareVersion"`
	ImageURL        string `json:"imageURL"`
}

// NetworkingSpec describes vendor-neutral DPU networking configuration.
type NetworkingSpec struct {
	// Mode is a vendor-neutral enum, e.g. "dpu-dpuOnly", "dpu-hostOffload".
	Mode      string `json:"mode"`
	OVNBridge string `json:"ovnBridge,omitempty"`
}

// ResourceSpec describes DPU-side resource allocation.
type ResourceSpec struct {
	Hugepages string `json:"hugepages,omitempty"`
	VFCount   int32  `json:"vfCount,omitempty"`
}

// OpiDpuDeviceStatus is the observed state, written exclusively by the OPI
// controller via the /status subresource. It is populated from the
// NormalizedStatus produced by whichever VendorAdapter is active.
type OpiDpuDeviceStatus struct {
	// Phase is a coarse, human-readable summary (e.g. Provisioning, Ready).
	Phase string `json:"phase,omitempty"`

	// Conditions is the canonical, vendor-neutral condition set. See the
	// Condition Type constants below.
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// ObservedGeneration lets clients detect whether status reflects the
	// most recent spec change.
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// OpiDpuDevice is the top-level vendor-neutral custom resource.
type OpiDpuDevice struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   OpiDpuDeviceSpec   `json:"spec,omitempty"`
	Status OpiDpuDeviceStatus `json:"status,omitempty"`
}

// DeepCopyObject satisfies runtime.Object. A real build would generate this
// via controller-gen; it is hand-stubbed here to keep the skeleton
// self-contained and compilable.
func (o *OpiDpuDevice) DeepCopyObject() runtime.Object {
	if o == nil {
		return nil
	}
	out := new(OpiDpuDevice)
	*out = *o
	out.TypeMeta = o.TypeMeta
	o.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	out.Spec = o.Spec
	out.Status = o.Status
	if o.Status.Conditions != nil {
		out.Status.Conditions = make([]metav1.Condition, len(o.Status.Conditions))
		copy(out.Status.Conditions, o.Status.Conditions)
	}
	return out
}

// ---------------------------------------------------------------------------
// 2. CANONICAL CONDITION VOCABULARY (Section 5 of architecture_design.md)
// ---------------------------------------------------------------------------

const (
	// ConditionTypeTranslationValid reports whether spec -> vendor-CR
	// mapping succeeded.
	ConditionTypeTranslationValid = "TranslationValid"
	// ConditionTypeProgressing reports whether the vendor operator is
	// actively provisioning or updating the DPU.
	ConditionTypeProgressing = "Progressing"
	// ConditionTypeDegraded reports a non-terminal vendor-reported error.
	ConditionTypeDegraded = "Degraded"
	// ConditionTypeReady reports that the DPU is fully provisioned/healthy.
	ConditionTypeReady = "Ready"
)

const (
	ReasonUnsupportedFieldCombination = "UnsupportedFieldCombination"
	ReasonFirmwareFlashFailed         = "FirmwareFlashFailed"
	ReasonHardwareFault               = "HardwareFault"
	ReasonDeletionPending             = "DeletionPending"
	ReasonProvisioningInProgress      = "ProvisioningInProgress"
	ReasonProvisioned                 = "Provisioned"
)

// finalizerName gates OpiDpuDevice deletion until owned DPF resources have
// been fully torn down (Section 4.3).
const finalizerName = "opi.io/dpf-cleanup"

// ---------------------------------------------------------------------------
// 3. CRD TRANSLATION LAYER (pkg/translate/nvidia equivalent)
//
// Pure, side-effect-free mapping from vendor-neutral spec to the DPF CR
// shapes. No API calls are made here; every function is table-test friendly.
// ---------------------------------------------------------------------------

// DPFResourceSet is the typed bundle of NVIDIA DPF custom resources that
// together represent one OpiDpuDevice's desired state.
type DPFResourceSet struct {
	DPFOperatorConfig *DPFOperatorConfig
	DPUSet            *DPUSet
	BFB               *BFB
}

// FieldError captures a single field-mapping failure, so translation errors
// can be surfaced as one aggregated TranslationValid=False condition rather
// than an opaque reconcile error.
type FieldError struct {
	Field   string
	Message string
}

func (e FieldError) Error() string {
	return fmt.Sprintf("field %q: %s", e.Field, e.Message)
}

// TranslationContext carries ambient information the translator needs but
// which is not part of the OPI spec itself (e.g. the discovered DPF API
// version, used to select translator behavior without a code fork).
type TranslationContext struct {
	TargetNamespace string
	DPFAPIVersion   string
}

// Translator converts a vendor-neutral spec into a vendor-specific resource
// set. Each supported DPF API version gets its own implementation
// (e.g. nvidiaTranslatorV1Alpha1, nvidiaTranslatorV1Beta1) selected at
// runtime; the OPI controller never depends on a concrete implementation.
type Translator interface {
	Translate(spec OpiDpuDeviceSpec, tctx TranslationContext) (*DPFResourceSet, []FieldError)
}

// nvidiaTranslatorV1Alpha1 is the concrete translator for the
// provisioning.dpu.nvidia.com/v1alpha1 DPF API.
type nvidiaTranslatorV1Alpha1 struct{}

// NewNvidiaTranslator returns the Translator for the given DPF API version.
// Adding support for a new DPF API version means adding a case here and a
// new implementing type -- no change to the controller or adapter caller.
func NewNvidiaTranslator(dpfAPIVersion string) (Translator, error) {
	switch dpfAPIVersion {
	case "v1alpha1":
		return &nvidiaTranslatorV1Alpha1{}, nil
	default:
		return nil, fmt.Errorf("unsupported DPF API version: %s", dpfAPIVersion)
	}
}

func (t *nvidiaTranslatorV1Alpha1) Translate(
	spec OpiDpuDeviceSpec,
	tctx TranslationContext,
) (*DPFResourceSet, []FieldError) {
	var errs []FieldError

	dpuMode, err := mapNetworkingMode(spec.Networking.Mode)
	if err != nil {
		errs = append(errs, FieldError{
			Field:   "spec.networking.mode",
			Message: err.Error(),
		})
	}

	if spec.BFB.ImageURL == "" {
		errs = append(errs, FieldError{
			Field:   "spec.bfb.imageURL",
			Message: "must not be empty",
		})
	}

	if len(errs) > 0 {
		return nil, errs
	}

	name := spec.DPUSelector.NodeName

	resourceSet := &DPFResourceSet{
		BFB: &BFB{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name + "-bfb",
				Namespace: tctx.TargetNamespace,
			},
			Spec: BFBSpecDPF{
				URL: spec.BFB.ImageURL,
			},
		},
		DPUSet: &DPUSet{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name + "-dpuset",
				Namespace: tctx.TargetNamespace,
			},
			Spec: DPUSetSpec{
				NodeSelector: map[string]string{"kubernetes.io/hostname": name},
				DPUMode:      dpuMode,
				VFCount:      spec.Resources.VFCount,
			},
		},
		DPFOperatorConfig: &DPFOperatorConfig{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name + "-operatorconfig",
				Namespace: tctx.TargetNamespace,
			},
			Spec: DPFOperatorConfigSpec{
				FirmwareVersion: spec.BFB.FirmwareVersion,
			},
		},
	}

	return resourceSet, nil
}

// mapNetworkingMode maps the OPI vendor-neutral networking enum to the DPF
// vendor-specific enum. Kept as a small pure function for direct unit
// testing (table-driven, no fakes required).
func mapNetworkingMode(opiMode string) (string, error) {
	switch opiMode {
	case "dpu-dpuOnly":
		return "DPUOnly", nil
	case "dpu-hostOffload":
		return "HostOffload", nil
	default:
		return "", fmt.Errorf("unsupported networking mode %q", opiMode)
	}
}

// ---------------------------------------------------------------------------
// 4. MINIMAL NVIDIA DPF CR TYPE STUBS
//
// In a real build these would be imported from NVIDIA's published Go module
// (e.g. github.com/nvidia/doca-platform/api/provisioning/v1alpha1). They are
// stubbed here, deliberately kept minimal, purely so this file is
// self-contained and compiles without an external dependency on NVIDIA's
// module. Production code must NOT redefine these -- it must import them,
// per Assumption A2 in architecture_design.md.
// ---------------------------------------------------------------------------

type BFBSpecDPF struct {
	URL string `json:"url"`
}

type BFB struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              BFBSpecDPF   `json:"spec,omitempty"`
	Status            DPFStatus    `json:"status,omitempty"`
}

func (b *BFB) DeepCopyObject() runtime.Object {
	out := *b
	return &out
}

type DPUSetSpec struct {
	NodeSelector map[string]string `json:"nodeSelector,omitempty"`
	DPUMode      string            `json:"dpuMode"`
	VFCount      int32             `json:"vfCount,omitempty"`
}

type DPUSet struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              DPUSetSpec `json:"spec,omitempty"`
	Status            DPFStatus  `json:"status,omitempty"`
}

func (d *DPUSet) DeepCopyObject() runtime.Object {
	out := *d
	return &out
}

type DPFOperatorConfigSpec struct {
	FirmwareVersion string `json:"firmwareVersion"`
}

type DPFOperatorConfig struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              DPFOperatorConfigSpec `json:"spec,omitempty"`
	Status            DPFStatus             `json:"status,omitempty"`
}

func (c *DPFOperatorConfig) DeepCopyObject() runtime.Object {
	out := *c
	return &out
}

// DPFStatus is the common status shape NVIDIA's CRDs expose; the adapter's
// StatusMapper (Section 6) reads only this shape and never mutates it.
type DPFStatus struct {
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// ---------------------------------------------------------------------------
// 5. VENDOR ADAPTER INTERFACE (Go Adapter Pattern)
//
// This is the ONLY contract the OPI controller depends on. Adding a new
// vendor means implementing this interface in a new package; zero changes
// to the controller.
// ---------------------------------------------------------------------------

// AdapterResult communicates the outcome of a Reconcile call back to the
// controller, without leaking vendor-specific types.
type AdapterResult struct {
	// Requeue signals the controller should requeue even without an error
	// (e.g. because the vendor operator is still Progressing).
	Requeue      bool
	RequeueAfter time.Duration
}

// NormalizedStatus is the vendor-agnostic status shape the controller
// consumes to populate OpiDpuDevice.status.Conditions.
type NormalizedStatus struct {
	Ready         bool
	Progressing   bool
	Degraded      bool
	Reason        string
	Message       string
	FullyDeleted  bool
}

// VendorAdapter is implemented once per silicon vendor (nvidia, marvel,
// intel, amd, ...). The OPI controller is written entirely against this
// interface and must never import a vendor-specific package directly.
type VendorAdapter interface {
	// Reconcile translates the owner's spec and applies (creates/updates)
	// the resulting vendor CRs against the cluster. Implementations MUST be
	// idempotent (server-side apply or equivalent).
	Reconcile(ctx context.Context, owner *OpiDpuDevice) (AdapterResult, error)

	// FetchStatus reads back and normalizes the vendor CRs' status.
	// Implementations MUST NOT mutate vendor status subresources.
	FetchStatus(ctx context.Context, owner *OpiDpuDevice) (NormalizedStatus, error)

	// Delete removes owned vendor CRs. Returns fullyRemoved=true once no
	// owned vendor resources remain on the API server, allowing the
	// controller to safely drop its finalizer.
	Delete(ctx context.Context, owner *OpiDpuDevice) (fullyRemoved bool, err error)
}

// ---------------------------------------------------------------------------
// 6. NVIDIA DPF ADAPTER (concrete VendorAdapter implementation)
// ---------------------------------------------------------------------------

// NvidiaDPFAdapter is the only package/type in the codebase permitted to
// import NVIDIA DPF's Go client types (enforced via CI lint rule in a real
// build). It composes a Translator with a Kubernetes client to fulfil the
// VendorAdapter contract.
type NvidiaDPFAdapter struct {
	Client          client.Client
	Translator      Translator
	TargetNamespace string
}

var _ VendorAdapter = &NvidiaDPFAdapter{}

// NewNvidiaDPFAdapter constructs an adapter bound to a specific DPF API
// version's translator.
func NewNvidiaDPFAdapter(c client.Client, dpfAPIVersion, targetNamespace string) (*NvidiaDPFAdapter, error) {
	translator, err := NewNvidiaTranslator(dpfAPIVersion)
	if err != nil {
		return nil, fmt.Errorf("constructing nvidia adapter: %w", err)
	}
	return &NvidiaDPFAdapter{
		Client:          c,
		Translator:      translator,
		TargetNamespace: targetNamespace,
	}, nil
}

func (a *NvidiaDPFAdapter) Reconcile(ctx context.Context, owner *OpiDpuDevice) (AdapterResult, error) {
	logger := log.FromContext(ctx)

	tctx := TranslationContext{
		TargetNamespace: a.TargetNamespace,
		DPFAPIVersion:   "v1alpha1",
	}

	resourceSet, fieldErrs := a.Translator.Translate(owner.Spec, tctx)
	if len(fieldErrs) > 0 {
		// Translation failures are deterministic and non-retryable until the
		// spec changes; the controller is expected to set
		// TranslationValid=False from this error rather than tight-requeue.
		return AdapterResult{}, fmt.Errorf("translation failed: %v", fieldErrs)
	}

	if err := a.applyOwned(ctx, owner, resourceSet.BFB); err != nil {
		return AdapterResult{}, fmt.Errorf("applying BFB: %w", err)
	}
	if err := a.applyOwned(ctx, owner, resourceSet.DPFOperatorConfig); err != nil {
		return AdapterResult{}, fmt.Errorf("applying DPFOperatorConfig: %w", err)
	}
	if err := a.applyOwned(ctx, owner, resourceSet.DPUSet); err != nil {
		return AdapterResult{}, fmt.Errorf("applying DPUSet: %w", err)
	}

	logger.Info("applied desired DPF resource set", "owner", owner.Name)

	// TODO: replace with a real readiness check derived from FetchStatus;
	// conservatively requeue until Ready is observed.
	return AdapterResult{RequeueAfter: 15 * time.Second}, nil
}

// applyOwned performs a server-side apply of obj and stamps it with the
// cross-namespace ownership label index described in Section 2.4 of
// architecture_design.md (Kubernetes ownerReferences cannot span
// namespaces, so the label is the authoritative link).
func (a *NvidiaDPFAdapter) applyOwned(ctx context.Context, owner *OpiDpuDevice, obj client.Object) error {
	labels := obj.GetLabels()
	if labels == nil {
		labels = map[string]string{}
	}
	labels["opi.io/managed-by"] = "opi-dpu-operator"
	labels["opi.io/owner-name"] = owner.Name
	labels["opi.io/owner-namespace"] = owner.Namespace
	labels["opi.io/owner-uid"] = string(owner.UID)
	obj.SetLabels(labels)

	// TODO: use client.Patch with client.Apply (server-side apply) and a
	// stable field manager name ("opi-dpu-operator") instead of Create/Update,
	// per Section 4.2's idempotency requirement.
	err := a.Client.Patch(ctx, obj, client.Apply, client.ForceOwnership, client.FieldOwner("opi-dpu-operator"))
	if err != nil {
		return err
	}
	return nil
}

func (a *NvidiaDPFAdapter) FetchStatus(ctx context.Context, owner *OpiDpuDevice) (NormalizedStatus, error) {
	// TODO: list owned DPUSet/BFB/DPFOperatorConfig via the
	// opi.io/owner-uid label selector, read their status.conditions, and
	// normalize via a StatusMapper (Section 5.1/5.2 of
	// architecture_design.md). Stubbed to keep this file self-contained.
	var dpuSet DPUSet
	key := types.NamespacedName{
		Name:      owner.Spec.DPUSelector.NodeName + "-dpuset",
		Namespace: a.TargetNamespace,
	}
	if err := a.Client.Get(ctx, key, &dpuSet); err != nil {
		if apierrors.IsNotFound(err) {
			return NormalizedStatus{Progressing: true, Reason: ReasonProvisioningInProgress}, nil
		}
		return NormalizedStatus{}, err
	}

	return mapDPFConditionsToNormalizedStatus(dpuSet.Status.Conditions), nil
}

// mapDPFConditionsToNormalizedStatus is the StatusMapper referenced in
// Section 5.1 of architecture_design.md: it translates DPF's native
// condition vocabulary into OPI's canonical NormalizedStatus.
func mapDPFConditionsToNormalizedStatus(conditions []metav1.Condition) NormalizedStatus {
	var ns NormalizedStatus
	for _, c := range conditions {
		switch c.Type {
		case "Ready":
			ns.Ready = c.Status == metav1.ConditionTrue
		case "Progressing", "Reconciling":
			ns.Progressing = c.Status == metav1.ConditionTrue
		case "Degraded":
			ns.Degraded = c.Status == metav1.ConditionTrue
			ns.Reason = c.Reason
			ns.Message = c.Message
		}
	}
	return ns
}

func (a *NvidiaDPFAdapter) Delete(ctx context.Context, owner *OpiDpuDevice) (bool, error) {
	// TODO: delete DPUSet first, then reference-count BFB/DPFOperatorConfig
	// via the owner-uid label index before deleting them, so a BFB shared by
	// a sibling OpiDpuDevice is not removed prematurely (Section 4.3).
	dpuSet := &DPUSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      owner.Spec.DPUSelector.NodeName + "-dpuset",
			Namespace: a.TargetNamespace,
		},
	}
	err := a.Client.Delete(ctx, dpuSet)
	if err != nil && !apierrors.IsNotFound(err) {
		return false, err
	}

	// A real implementation polls until all owned resources are gone before
	// returning fullyRemoved=true; stubbed here as an immediate check.
	err = a.Client.Get(ctx, client.ObjectKeyFromObject(dpuSet), dpuSet)
	if apierrors.IsNotFound(err) {
		return true, nil
	}
	return false, nil
}

// ---------------------------------------------------------------------------
// 7. ADAPTER FACTORY
//
// Selects a VendorAdapter based on spec.vendor without the controller
// knowing which concrete adapters exist beyond this registration point.
// ---------------------------------------------------------------------------

// AdapterFactory constructs a VendorAdapter for a given vendor string. New
// vendors register here; the controller itself never changes.
type AdapterFactory func(c client.Client, namespace string) (VendorAdapter, error)

var adapterRegistry = map[string]AdapterFactory{
	"nvidia": func(c client.Client, namespace string) (VendorAdapter, error) {
		return NewNvidiaDPFAdapter(c, "v1alpha1", namespace)
	},
	// "marvel": newMarvelAdapter,
	// "intel":  newIntelAdapter,
}

func GetAdapter(vendor string, c client.Client, namespace string) (VendorAdapter, error) {
	factory, ok := adapterRegistry[vendor]
	if !ok {
		return nil, fmt.Errorf("no adapter registered for vendor %q", vendor)
	}
	return factory(c, namespace)
}

// ---------------------------------------------------------------------------
// 8. OPI CONTROLLER (Reconciler)
//
// Contains NO vendor-specific code; depends only on VendorAdapter.
// ---------------------------------------------------------------------------

// OpiDpuDeviceReconciler reconciles OpiDpuDevice objects (Section 3.2.2 of
// architecture_design.md).
type OpiDpuDeviceReconciler struct {
	client.Client
	Scheme          *runtime.Scheme
	DPFNamespace    string
}

// Reconcile implements the standard controller-runtime reconciliation
// contract. It is level-triggered: every invocation recomputes full desired
// state from the current spec (Section 4.4).
func (r *OpiDpuDeviceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var owner OpiDpuDevice
	if err := r.Get(ctx, req.NamespacedName, &owner); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	adapter, err := GetAdapter(owner.Spec.Vendor, r.Client, r.DPFNamespace)
	if err != nil {
		return ctrl.Result{}, r.setCondition(ctx, &owner, metav1.Condition{
			Type:    ConditionTypeTranslationValid,
			Status:  metav1.ConditionFalse,
			Reason:  "UnknownVendor",
			Message: err.Error(),
		})
	}

	// --- Deletion path (Section 4.3) ---
	if !owner.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, &owner, adapter)
	}

	// --- Ensure finalizer present before doing any create/update work ---
	if !controllerutil.ContainsFinalizer(&owner, finalizerName) {
		controllerutil.AddFinalizer(&owner, finalizerName)
		if err := r.Update(ctx, &owner); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	// --- Create / update path (Section 4.2) ---
	result, err := adapter.Reconcile(ctx, &owner)
	if err != nil {
		if condErr := r.setCondition(ctx, &owner, metav1.Condition{
			Type:    ConditionTypeTranslationValid,
			Status:  metav1.ConditionFalse,
			Reason:  ReasonUnsupportedFieldCombination,
			Message: err.Error(),
		}); condErr != nil {
			logger.Error(condErr, "failed to set condition after reconcile error")
		}
		return ctrl.Result{}, err
	}

	normalized, err := adapter.FetchStatus(ctx, &owner)
	if err != nil {
		return ctrl.Result{}, err
	}

	if err := r.updateStatus(ctx, &owner, normalized); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{Requeue: result.Requeue, RequeueAfter: result.RequeueAfter}, nil
}

func (r *OpiDpuDeviceReconciler) reconcileDelete(
	ctx context.Context,
	owner *OpiDpuDevice,
	adapter VendorAdapter,
) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(owner, finalizerName) {
		return ctrl.Result{}, nil
	}

	fullyRemoved, err := adapter.Delete(ctx, owner)
	if err != nil {
		return ctrl.Result{}, err
	}
	if !fullyRemoved {
		_ = r.setCondition(ctx, owner, metav1.Condition{
			Type:    ConditionTypeReady,
			Status:  metav1.ConditionUnknown,
			Reason:  ReasonDeletionPending,
			Message: "waiting for owned DPF resources to be removed",
		})
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}

	controllerutil.RemoveFinalizer(owner, finalizerName)
	if err := r.Update(ctx, owner); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// updateStatus aggregates NormalizedStatus into the canonical Conditions
// array and patches OpiDpuDevice.status via the /status subresource
// (Section 4.2, step 8).
func (r *OpiDpuDeviceReconciler) updateStatus(
	ctx context.Context,
	owner *OpiDpuDevice,
	ns NormalizedStatus,
) error {
	readyStatus := metav1.ConditionFalse
	readyReason := ReasonProvisioningInProgress
	if ns.Ready {
		readyStatus = metav1.ConditionTrue
		readyReason = ReasonProvisioned
	}

	conditions := []metav1.Condition{
		{
			Type:   ConditionTypeReady,
			Status: readyStatus,
			Reason: readyReason,
		},
		{
			Type:   ConditionTypeProgressing,
			Status: boolToConditionStatus(ns.Progressing),
			Reason: ReasonProvisioningInProgress,
		},
	}
	if ns.Degraded {
		conditions = append(conditions, metav1.Condition{
			Type:    ConditionTypeDegraded,
			Status:  metav1.ConditionTrue,
			Reason:  ns.Reason,
			Message: ns.Message,
		})
	}

	owner.Status.Conditions = mergeConditions(owner.Status.Conditions, conditions)
	owner.Status.ObservedGeneration = owner.Generation
	if ns.Ready {
		owner.Status.Phase = "Ready"
	} else {
		owner.Status.Phase = "Provisioning"
	}

	return r.Status().Update(ctx, owner)
}

func (r *OpiDpuDeviceReconciler) setCondition(
	ctx context.Context,
	owner *OpiDpuDevice,
	cond metav1.Condition,
) error {
	cond.LastTransitionTime = metav1.Now()
	cond.ObservedGeneration = owner.Generation
	owner.Status.Conditions = mergeConditions(owner.Status.Conditions, []metav1.Condition{cond})
	return r.Status().Update(ctx, owner)
}

// mergeConditions upserts each new condition into existing by Type,
// preserving LastTransitionTime when Status is unchanged, per Kubernetes API
// conventions.
func mergeConditions(existing []metav1.Condition, updates []metav1.Condition) []metav1.Condition {
	byType := make(map[string]metav1.Condition, len(existing))
	for _, c := range existing {
		byType[c.Type] = c
	}
	for _, u := range updates {
		prev, ok := byType[u.Type]
		if ok && prev.Status == u.Status {
			u.LastTransitionTime = prev.LastTransitionTime
		} else {
			u.LastTransitionTime = metav1.Now()
		}
		byType[u.Type] = u
	}
	out := make([]metav1.Condition, 0, len(byType))
	for _, c := range byType {
		out = append(out, c)
	}
	return out
}

func boolToConditionStatus(b bool) metav1.ConditionStatus {
	if b {
		return metav1.ConditionTrue
	}
	return metav1.ConditionFalse
}

// ---------------------------------------------------------------------------
// 9. MANAGER WIRING
//
// Sets up the primary watch on OpiDpuDevice plus the secondary,
// label-filtered watch on owned DPF CR types (Section 4.2, step 2), so
// vendor-side status changes re-trigger reconciliation without the
// controller ever reconciling DPF CRDs directly.
// ---------------------------------------------------------------------------

func (r *OpiDpuDeviceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&OpiDpuDevice{}).
		Owns(&corev1.Event{}). // placeholder for the real Owns(&DPUSet{}) etc.
		Complete(r)
}