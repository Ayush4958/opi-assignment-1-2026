// Package nvidiadpf is the foundational Go skeleton for integrating NVIDIA
// BlueField DPU support into the OPI DPU operator by reusing the NVIDIA DOCA
// Platform Framework (DPF) operator.
//
// This file is the bonus deliverable of Assignment 1 and is intentionally
// self-contained (standard library only) so it compiles anywhere with
// `go build`. In a real repository:
//
//   - Section 1 is replaced by generated protobuf/gRPC stubs from
//     openshift/dpu-operator dpu-api/api.proto (package Vendor).
//   - Section 2 is replaced by k8s.io/apimachinery unstructured types and a
//     controller-runtime client against the DPF CRDs.
//   - Section 5 is replaced by sigs.k8s.io/controller-runtime.
//
// Design-document cross references (architecture_design.md):
//
//	Section 1  -> design §2.1 (VSP gRPC contract), §6.2-3
//	Section 2  -> design §7 (API and CRD mapping)
//	Section 3  -> design §7.3 (identity contract), §7.2 (SFC mapping)
//	Section 4  -> design §6.2-3 RPC table, §10 E3/E5/E8 mitigations
//	Section 5  -> design §9 (reconciliation semantics, conditions)
//	Section 6  -> design §6.2-1 (DPF Lifecycle Manager), §10 E11/E12
package nvidiadpf

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// -----------------------------------------------------------------------------
// Section 1: OPI dpu-api vendor-plugin contract (mirror of dpu-api/api.proto)
// -----------------------------------------------------------------------------
//
// These interfaces are hand-written equivalents of the gRPC services the OPI
// dpu-daemon invokes on every Vendor Specific Plugin (VSP). The NVIDIA VSP in
// Section 4 implements all five. Message shapes match api.proto exactly.

// InitRequest mirrors Vendor.InitRequest.
type InitRequest struct {
	DpuMode       bool   // true when the daemon runs on the DPU ARM cores
	DpuIdentifier string // stable identifier for the DPU/node pairing
}

// IpPort mirrors Vendor.IpPort (the VSP's serving endpoint).
type IpPort struct {
	IP   string
	Port int32
}

// NFRequest mirrors Vendor.NFRequest, the per-pod network-function attach
// request issued during CNI ADD (design §8.4).
type NFRequest struct {
	Input    string // ingress interface / VF representor
	Output   string // egress interface or chain reference
	BridgeID string // OPI bridge identity; mapped per design §10 E20
}

// DpuNetworkConfigRequest mirrors Vendor.DpuNetworkConfigRequest.
type DpuNetworkConfigRequest struct {
	IsAccelerated bool
}

// VfCount mirrors Vendor.VfCount.
type VfCount struct {
	VfCnt int32
}

// TopologyInfo mirrors Vendor.TopologyInfo.
type TopologyInfo struct {
	Node string
}

// Device mirrors Vendor.Device as advertised to the OPI device plugin.
type Device struct {
	ID       string // VF PCI address on NVIDIA nodes
	Health   string // "Healthy" | "Unhealthy"
	Topology TopologyInfo
}

// DeviceListResponse mirrors Vendor.DeviceListResponse.
type DeviceListResponse struct {
	Devices map[string]Device
}

// PingRequest / PingResponse mirror Vendor.HeartbeatService messages.
type PingRequest struct {
	Timestamp int64
	SenderID  string
}

type PingResponse struct {
	Timestamp   int64
	ResponderID string
	Healthy     bool
}

// LifeCycleService mirrors Vendor.LifeCycleService.
type LifeCycleService interface {
	Init(ctx context.Context, req InitRequest) (IpPort, error)
}

// DeviceService mirrors Vendor.DeviceService.
type DeviceService interface {
	GetDevices(ctx context.Context) (DeviceListResponse, error)
	SetNumVfs(ctx context.Context, req VfCount) (VfCount, error)
}

// NetworkFunctionService mirrors Vendor.NetworkFunctionService.
type NetworkFunctionService interface {
	CreateNetworkFunction(ctx context.Context, req NFRequest) error
	DeleteNetworkFunction(ctx context.Context, req NFRequest) error
}

// DpuNetworkConfigService mirrors Vendor.DpuNetworkConfigService.
type DpuNetworkConfigService interface {
	SetDpuNetworkConfig(ctx context.Context, req DpuNetworkConfigRequest) error
}

// HeartbeatService mirrors Vendor.HeartbeatService.
type HeartbeatService interface {
	Ping(ctx context.Context, req PingRequest) (PingResponse, error)
}

// VendorPlugin is the composite contract every OPI VSP fulfills.
type VendorPlugin interface {
	LifeCycleService
	DeviceService
	NetworkFunctionService
	DpuNetworkConfigService
	HeartbeatService
}

// Sentinel errors modeling the gRPC status codes the design mandates
// (design §6.2-3, §10 E3/E5): callers (dpu-daemon / CNI) treat ErrUnavailable
// as retryable and ErrFailedPrecondition as a user-actionable rejection.
var (
	ErrUnavailable        = errors.New("UNAVAILABLE: dependent DPF state not converged; retry")
	ErrFailedPrecondition = errors.New("FAILED_PRECONDITION")
	ErrUnimplemented      = errors.New("UNIMPLEMENTED")
)

// -----------------------------------------------------------------------------
// Section 2: Derived DPF object model and client abstraction (design §7)
// -----------------------------------------------------------------------------

// GroupVersionKind identifies a DPF API type the translator writes or reads.
type GroupVersionKind struct {
	Group, Version, Kind string
}

// The DPF kinds this integration derives (writes) or observes (read-only).
// Versions are pinned per the compatibility matrix (design §13).
var (
	GVKDPFOperatorConfig   = GroupVersionKind{"operator.dpu.nvidia.com", "v1alpha1", "DPFOperatorConfig"}
	GVKBFB                 = GroupVersionKind{"provisioning.dpu.nvidia.com", "v1alpha1", "BFB"}
	GVKDPUFlavor           = GroupVersionKind{"provisioning.dpu.nvidia.com", "v1alpha1", "DPUFlavor"}
	GVKDPUSet              = GroupVersionKind{"provisioning.dpu.nvidia.com", "v1alpha1", "DPUSet"}
	GVKDPU                 = GroupVersionKind{"provisioning.dpu.nvidia.com", "v1alpha1", "DPU"} // read-only (rule W3)
	GVKDPUCluster          = GroupVersionKind{"provisioning.dpu.nvidia.com", "v1alpha1", "DPUCluster"}
	GVKDPUService          = GroupVersionKind{"svc.dpu.nvidia.com", "v1alpha1", "DPUService"}
	GVKDPUServiceChain     = GroupVersionKind{"svc.dpu.nvidia.com", "v1alpha1", "DPUServiceChain"}
	GVKDPUServiceInterface = GroupVersionKind{"svc.dpu.nvidia.com", "v1alpha1", "DPUServiceInterface"}
	GVKDPUServiceIPAM      = GroupVersionKind{"svc.dpu.nvidia.com", "v1alpha1", "DPUServiceIPAM"}
)

// Condition is a minimal metav1.Condition analogue.
type Condition struct {
	Type    string
	Status  bool
	Reason  string
	Message string
}

// DPFObject is a minimal unstructured-object analogue carrying only what the
// adapter needs: identity, ownership labels, an opaque spec, and conditions.
type DPFObject struct {
	GVK        GroupVersionKind
	Namespace  string
	Name       string
	Labels     map[string]string
	Spec       map[string]any
	Conditions []Condition
}

// Key returns the unique cluster key for the object.
func (o DPFObject) Key() string {
	return o.GVK.Group + "/" + o.GVK.Kind + "/" + o.Namespace + "/" + o.Name
}

// IsReady reports whether the object has a true "Ready" condition.
func (o DPFObject) IsReady() bool {
	for _, c := range o.Conditions {
		if c.Type == "Ready" && c.Status {
			return true
		}
	}
	return false
}

// FieldManager is the server-side-apply field manager identity used for every
// write, enforcing the single-writer rule W2 (design §9.1).
const FieldManager = "opi-nvidia-vsp"

// ManagedNamespace hosts all generated DPF objects (design §7.3).
const ManagedNamespace = "opi-nvidia-system"

// DPFClient abstracts typed access to DPF resources. The production
// implementation wraps a dynamic controller-runtime client with SSA;
// InMemoryDPFClient below is the test fake.
type DPFClient interface {
	Get(ctx context.Context, gvk GroupVersionKind, namespace, name string) (DPFObject, error)
	List(ctx context.Context, gvk GroupVersionKind, namespace string, selector map[string]string) ([]DPFObject, error)
	// Apply performs an SSA-style idempotent upsert under FieldManager.
	Apply(ctx context.Context, obj DPFObject) error
	Delete(ctx context.Context, gvk GroupVersionKind, namespace, name string) error
}

// ErrNotFound is returned by Get for absent objects.
var ErrNotFound = errors.New("not found")

// InMemoryDPFClient is a threadsafe fake used by unit/integration tests
// (design §14, unit layer). Double-Apply of identical objects is a no-op,
// which is exactly the idempotency property the tests assert.
type InMemoryDPFClient struct {
	mu      sync.RWMutex
	objects map[string]DPFObject
}

// NewInMemoryDPFClient constructs an empty fake.
func NewInMemoryDPFClient() *InMemoryDPFClient {
	return &InMemoryDPFClient{objects: map[string]DPFObject{}}
}

func (c *InMemoryDPFClient) Get(_ context.Context, gvk GroupVersionKind, namespace, name string) (DPFObject, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	o, ok := c.objects[gvk.Group+"/"+gvk.Kind+"/"+namespace+"/"+name]
	if !ok {
		return DPFObject{}, fmt.Errorf("%w: %s/%s %s", ErrNotFound, namespace, name, gvk.Kind)
	}
	return o, nil
}

func (c *InMemoryDPFClient) List(_ context.Context, gvk GroupVersionKind, namespace string, selector map[string]string) ([]DPFObject, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	var out []DPFObject
	for _, o := range c.objects {
		if o.GVK != gvk || (namespace != "" && o.Namespace != namespace) {
			continue
		}
		match := true
		for k, v := range selector {
			if o.Labels[k] != v {
				match = false
				break
			}
		}
		if match {
			out = append(out, o)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (c *InMemoryDPFClient) Apply(_ context.Context, obj DPFObject) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	// Conditions live on the status subresource, owned by DPF controllers, not
	// by the applying field manager (design §9.1). A spec-level SSA apply must
	// therefore preserve any conditions already present on the object.
	if existing, ok := c.objects[obj.Key()]; ok && obj.Conditions == nil {
		obj.Conditions = existing.Conditions
	}
	c.objects[obj.Key()] = obj
	return nil
}

func (c *InMemoryDPFClient) Delete(_ context.Context, gvk GroupVersionKind, namespace, name string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.objects, gvk.Group+"/"+gvk.Kind+"/"+namespace+"/"+name)
	return nil // idempotent: deleting absent objects succeeds (design §9.2)
}

// SetConditions lets tests simulate DPF controllers converging an object.
func (c *InMemoryDPFClient) SetConditions(key string, conds []Condition) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if o, ok := c.objects[key]; ok {
		o.Conditions = conds
		c.objects[key] = o
	}
}

// -----------------------------------------------------------------------------
// Section 3: Identity contract and pure translation (design §7.2, §7.3)
// -----------------------------------------------------------------------------

// Ownership label keys forming the mapping contract on generated objects.
const (
	LabelOwnerKind      = "opi.opiproject.org/owner-kind"
	LabelOwnerName      = "opi.opiproject.org/owner-name"
	LabelOwnerNamespace = "opi.opiproject.org/owner-namespace"
	LabelComponent      = "opi.opiproject.org/component"
	ComponentNvidiaVSP  = "nvidia-vsp"
)

// DeterministicName implements the naming contract of design §7.3:
//
//	"opi-" + sha1(ns + "/" + name + "/" + role)[:8] + "-" + sanitize(name)
//
// Determinism guarantees retries and restarts converge on the same object set
// with zero duplicates (mitigation for edge case E8).
func DeterministicName(opiNamespace, opiName, role string) string {
	sum := sha1.Sum([]byte(opiNamespace + "/" + opiName + "/" + role))
	return "opi-" + hex.EncodeToString(sum[:])[:8] + "-" + sanitizeRFC1123(opiName)
}

// sanitizeRFC1123 lower-cases and strips characters invalid in a DNS-1123
// label, truncating to keep total names within Kubernetes limits. Sanitizing
// user input here is also the injection guard of design §11 (helm values).
func sanitizeRFC1123(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '.' || r == '_' || r == ' ':
			b.WriteRune('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > 40 {
		out = out[:40]
	}
	if out == "" {
		out = "unnamed"
	}
	return out
}

// OwnerLabels returns the standard ownership labels for a generated object.
func OwnerLabels(ownerKind, ownerNamespace, ownerName string) map[string]string {
	return map[string]string{
		LabelOwnerKind:      ownerKind,
		LabelOwnerName:      ownerName,
		LabelOwnerNamespace: ownerNamespace,
		LabelComponent:      ComponentNvidiaVSP,
	}
}

// NetworkFunction is one hop of an OPI ServiceFunctionChain.
type NetworkFunction struct {
	Name  string
	Image string
}

// ServiceFunctionChainSpec is the OPI-side SFC input to translation. In the
// real repo this is the existing SFC CRD type from the OPI operator API.
type ServiceFunctionChainSpec struct {
	Namespace        string
	Name             string
	NetworkFunctions []NetworkFunction
	IPAMSubnet       string // optional; "" means no DPUServiceIPAM generated
	NodeSelector     map[string]string
}

// ApplyPlan is the ordered, idempotent set of DPF objects a translation
// produces. Applying a plan twice yields no diff (design §9.2).
type ApplyPlan struct {
	Objects []DPFObject
}

// TranslateSFC maps one OPI ServiceFunctionChain to its derived DPF objects
// (design §7.2): a wrapper DPUService (helm values embedding NF images), one
// DPUServiceChain, two DPUServiceInterfaces per NF, and optionally one
// DPUServiceIPAM. Pure function: no I/O, fully unit-testable via golden files.
func TranslateSFC(sfc ServiceFunctionChainSpec) ApplyPlan {
	owner := OwnerLabels("ServiceFunctionChain", sfc.Namespace, sfc.Name)
	var plan ApplyPlan

	// Wrapper DPUService: ArgoCD-delivered helm chart running the NF pods.
	nfValues := make([]map[string]any, 0, len(sfc.NetworkFunctions))
	for _, nf := range sfc.NetworkFunctions {
		nfValues = append(nfValues, map[string]any{
			"name":  sanitizeRFC1123(nf.Name),
			"image": nf.Image,
		})
	}
	plan.Objects = append(plan.Objects, DPFObject{
		GVK:       GVKDPUService,
		Namespace: ManagedNamespace,
		Name:      DeterministicName(sfc.Namespace, sfc.Name, "service"),
		Labels:    owner,
		Spec: map[string]any{
			"helmChart":        "opi-nf-wrapper", // shipped with the VSP bundle
			"values":           map[string]any{"networkFunctions": nfValues},
			"deployInCluster":  true,
			"argoPruneEnabled": true, // mitigation E9
		},
	})

	// Two DPUServiceInterfaces per NF (ingress/egress).
	chainHops := make([]map[string]any, 0, len(sfc.NetworkFunctions))
	for _, nf := range sfc.NetworkFunctions {
		var hopIfaces []string
		for _, dir := range []string{"in", "out"} {
			ifName := DeterministicName(sfc.Namespace, sfc.Name, "iface-"+nf.Name+"-"+dir)
			hopIfaces = append(hopIfaces, ifName)
			plan.Objects = append(plan.Objects, DPFObject{
				GVK:       GVKDPUServiceInterface,
				Namespace: ManagedNamespace,
				Name:      ifName,
				Labels:    owner,
				Spec: map[string]any{
					"interfaceType": "service",
					"service":       sanitizeRFC1123(nf.Name),
					"direction":     dir,
					"nodeSelector":  sfc.NodeSelector,
				},
			})
		}
		chainHops = append(chainHops, map[string]any{
			"service":    sanitizeRFC1123(nf.Name),
			"interfaces": hopIfaces,
		})
	}

	// The chain itself, ordered by the SFC's NF order.
	plan.Objects = append(plan.Objects, DPFObject{
		GVK:       GVKDPUServiceChain,
		Namespace: ManagedNamespace,
		Name:      DeterministicName(sfc.Namespace, sfc.Name, "chain"),
		Labels:    owner,
		Spec: map[string]any{
			"hops":         chainHops,
			"nodeSelector": sfc.NodeSelector,
		},
	})

	// Optional IPAM.
	if sfc.IPAMSubnet != "" {
		plan.Objects = append(plan.Objects, DPFObject{
			GVK:       GVKDPUServiceIPAM,
			Namespace: ManagedNamespace,
			Name:      DeterministicName(sfc.Namespace, sfc.Name, "ipam"),
			Labels:    owner,
			Spec:      map[string]any{"subnet": sfc.IPAMSubnet},
		})
	}
	return plan
}

// NFRequestHash keys imperative CreateNetworkFunction calls for idempotency
// under CNI retries and daemon restarts (mitigation E8).
func NFRequestHash(node string, req NFRequest) string {
	sum := sha1.Sum([]byte(node + "|" + req.Input + "|" + req.Output + "|" + req.BridgeID))
	return hex.EncodeToString(sum[:])[:12]
}

// TranslateNFRequest maps one per-pod NFRequest (design §8.4) to a node-scoped
// DPUServiceInterface pair plus a chain-hop object. bridge_id is recorded in
// an annotation-style label per the E20 mapping contract.
func TranslateNFRequest(node string, req NFRequest) ApplyPlan {
	h := NFRequestHash(node, req)
	owner := OwnerLabels("NFRequest", node, h)
	owner["opi.opiproject.org/bridge-id"] = sanitizeRFC1123(req.BridgeID)
	base := DeterministicName(node, h, "nf")

	mk := func(role, iface string) DPFObject {
		return DPFObject{
			GVK:       GVKDPUServiceInterface,
			Namespace: ManagedNamespace,
			Name:      base + "-" + role,
			Labels:    owner,
			Spec: map[string]any{
				"interfaceType": "physical",
				"interfaceName": iface,
				"node":          node,
			},
		}
	}
	return ApplyPlan{Objects: []DPFObject{
		mk("in", req.Input),
		mk("out", req.Output),
		{
			GVK:       GVKDPUServiceChain,
			Namespace: ManagedNamespace,
			Name:      base + "-hop",
			Labels:    owner,
			Spec: map[string]any{
				"node": node,
				"hops": []map[string]any{{"interfaces": []string{base + "-in", base + "-out"}}},
			},
		},
	}}
}

// -----------------------------------------------------------------------------
// Section 4: NvidiaVSP — the adapter implementing the OPI vendor contract
// (design §6.2-3; edge cases E3, E5, E8, E14, E17)
// -----------------------------------------------------------------------------

// VFAllocator reports how many VFs on this node are currently allocated to
// pods (backed by the device-plugin allocation state in production). Used by
// the SetNumVfs shrink guard (E5).
type VFAllocator interface {
	AllocatedVFs() int32
}

// HostVFEnumerator lists VF PCI addresses on the local BlueField PF (sysfs in
// production). Read-only by rule W1: the VSP never writes sriov sysfs.
type HostVFEnumerator interface {
	ListVFs() ([]string, error)
}

// NvidiaVSPConfig tunes the bounded waits of the sync-to-async bridge.
type NvidiaVSPConfig struct {
	NodeName string
	ServeIP  string
	ServePort int32
	// NFReadyTimeout bounds CreateNetworkFunction's wait for chain
	// convergence; must stay below the CNI ADD budget (design §9.4).
	NFReadyTimeout time.Duration
	// VFActuationTimeout bounds SetNumVfs' wait for the DPF hostnetwork
	// daemon to actuate the requested VF count.
	VFActuationTimeout time.Duration
	// HealthStaleness is the maximum cached-state age considered healthy;
	// health is staleness-based, not timestamp-based (E17).
	HealthStaleness time.Duration
	// Poll interval for bounded waits.
	PollInterval time.Duration
}

// DefaultVSPConfig returns production defaults from the design document.
func DefaultVSPConfig(node string) NvidiaVSPConfig {
	return NvidiaVSPConfig{
		NodeName:           node,
		ServeIP:            "127.0.0.1",
		ServePort:          50051,
		NFReadyTimeout:     30 * time.Second,
		VFActuationTimeout: 60 * time.Second,
		HealthStaleness:    2 * time.Minute,
		PollInterval:       500 * time.Millisecond,
	}
}

// NvidiaVSP implements VendorPlugin by translating every OPI vendor-contract
// call into DPF custom resources (adapter pattern, design §5 Pattern E).
type NvidiaVSP struct {
	cfg   NvidiaVSPConfig
	dpf   DPFClient
	alloc VFAllocator
	vfs   HostVFEnumerator

	mu              sync.RWMutex
	lastHealthySync time.Time // updated by informer callbacks in production
	initialized     bool
}

// Compile-time assertion that NvidiaVSP fulfills the full OPI contract.
var _ VendorPlugin = (*NvidiaVSP)(nil)

// NewNvidiaVSP wires the adapter.
func NewNvidiaVSP(cfg NvidiaVSPConfig, dpf DPFClient, alloc VFAllocator, vfs HostVFEnumerator) *NvidiaVSP {
	return &NvidiaVSP{cfg: cfg, dpf: dpf, alloc: alloc, vfs: vfs, lastHealthySync: time.Now()}
}

// Init implements LifeCycleService (design §8.2 step 1).
//   - dpu_mode is UNIMPLEMENTED in v1 per topology decision T3 (§6.3):
//     the DPU side is fully DPF/Kamaji-managed and dpu-daemon does not run
//     on BlueField ARM cores.
//   - Host mode verifies the node's DPU CR is provisioned (fail closed, E14).
func (v *NvidiaVSP) Init(ctx context.Context, req InitRequest) (IpPort, error) {
	if req.DpuMode {
		return IpPort{}, fmt.Errorf("%w: dpu_mode is served by DPF DPU-cluster controllers in v1 (design §6.3)", ErrUnimplemented)
	}
	dpus, err := v.dpf.List(ctx, GVKDPU, "", map[string]string{})
	if err != nil {
		return IpPort{}, fmt.Errorf("%w: cannot list DPU CRs: %v", ErrUnavailable, err)
	}
	for _, d := range dpus {
		if d.Spec["nodeName"] == v.cfg.NodeName && d.IsReady() {
			v.mu.Lock()
			v.initialized = true
			v.mu.Unlock()
			_ = req.DpuIdentifier // recorded for the node registry in production
			return IpPort{IP: v.cfg.ServeIP, Port: v.cfg.ServePort}, nil
		}
	}
	return IpPort{}, fmt.Errorf("%w: no provisioned DPF DPU CR for node %q (see DpuOperatorConfig condition DpusProvisioned)",
		ErrFailedPrecondition, v.cfg.NodeName)
}

// GetDevices implements DeviceService by merging DPF DPU CR health with local
// VF enumeration (design §8.2 steps 8-11, gap G6).
func (v *NvidiaVSP) GetDevices(ctx context.Context) (DeviceListResponse, error) {
	dpus, err := v.dpf.List(ctx, GVKDPU, "", map[string]string{})
	if err != nil {
		return DeviceListResponse{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	nodeHealthy := false
	for _, d := range dpus {
		if d.Spec["nodeName"] == v.cfg.NodeName && d.IsReady() {
			nodeHealthy = true
			break
		}
	}
	pcis, err := v.vfs.ListVFs()
	if err != nil {
		return DeviceListResponse{}, fmt.Errorf("%w: vf enumeration: %v", ErrUnavailable, err)
	}
	health := "Unhealthy"
	if nodeHealthy {
		health = "Healthy"
	}
	resp := DeviceListResponse{Devices: map[string]Device{}}
	for _, pci := range pcis {
		resp.Devices[pci] = Device{ID: pci, Health: health, Topology: TopologyInfo{Node: v.cfg.NodeName}}
	}
	return resp, nil
}

// SetNumVfs implements DeviceService with the in-use shrink guard (E5) and
// delegation to the DPF hostnetwork daemon (single-writer rule W1): the VSP
// patches the desired count on the node's DPUFlavor request object and waits
// bounded for actuation, returning the ACTUAL count.
func (v *NvidiaVSP) SetNumVfs(ctx context.Context, req VfCount) (VfCount, error) {
	if allocated := v.alloc.AllocatedVFs(); req.VfCnt < allocated {
		return VfCount{}, fmt.Errorf("%w: requested %d VFs but %d are allocated to running pods",
			ErrFailedPrecondition, req.VfCnt, allocated)
	}
	obj := DPFObject{
		GVK:       GVKDPUFlavor,
		Namespace: ManagedNamespace,
		Name:      DeterministicName(ManagedNamespace, v.cfg.NodeName, "vf-request"),
		Labels:    OwnerLabels("DpuNode", ManagedNamespace, v.cfg.NodeName),
		Spec:      map[string]any{"node": v.cfg.NodeName, "numVfs": req.VfCnt},
	}
	if err := v.dpf.Apply(ctx, obj); err != nil {
		return VfCount{}, fmt.Errorf("%w: apply VF request: %v", ErrUnavailable, err)
	}
	// Bounded wait for the DPF hostnetwork daemon to actuate.
	deadline := time.Now().Add(v.cfg.VFActuationTimeout)
	for time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			return VfCount{}, err
		}
		pcis, err := v.vfs.ListVFs()
		if err == nil && int32(len(pcis)) == req.VfCnt {
			return VfCount{VfCnt: int32(len(pcis))}, nil
		}
		time.Sleep(v.cfg.PollInterval)
	}
	// Return actual truth so the daemon reconciles against reality.
	pcis, _ := v.vfs.ListVFs()
	return VfCount{VfCnt: int32(len(pcis))}, fmt.Errorf("%w: VF actuation not converged (have %d, want %d)",
		ErrUnavailable, len(pcis), req.VfCnt)
}

// CreateNetworkFunction implements the imperative CNI-ADD path (design §8.4):
// idempotent upsert of the derived objects, then a bounded wait for chain
// readiness; on timeout it returns ErrUnavailable so the CNI retry re-enters
// the idempotent path (E3/E8). It never reports success before the flow is
// programmed and never blocks unbounded inside pod-sandbox creation.
func (v *NvidiaVSP) CreateNetworkFunction(ctx context.Context, req NFRequest) error {
	plan := TranslateNFRequest(v.cfg.NodeName, req)
	for _, obj := range plan.Objects {
		if err := v.dpf.Apply(ctx, obj); err != nil {
			return fmt.Errorf("%w: apply %s: %v", ErrUnavailable, obj.Name, err)
		}
	}
	chainName := plan.Objects[len(plan.Objects)-1].Name
	deadline := time.Now().Add(v.cfg.NFReadyTimeout)
	for time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			return err
		}
		chain, err := v.dpf.Get(ctx, GVKDPUServiceChain, ManagedNamespace, chainName)
		if err == nil && chain.IsReady() {
			return nil
		}
		time.Sleep(v.cfg.PollInterval)
	}
	return fmt.Errorf("%w: chain %s not Ready within %s", ErrUnavailable, chainName, v.cfg.NFReadyTimeout)
}

// DeleteNetworkFunction removes the derived objects; idempotent by design
// (deleting absent objects succeeds).
func (v *NvidiaVSP) DeleteNetworkFunction(ctx context.Context, req NFRequest) error {
	plan := TranslateNFRequest(v.cfg.NodeName, req)
	for _, obj := range plan.Objects {
		if err := v.dpf.Delete(ctx, obj.GVK, obj.Namespace, obj.Name); err != nil {
			return fmt.Errorf("%w: delete %s: %v", ErrUnavailable, obj.Name, err)
		}
	}
	return nil
}

// SetDpuNetworkConfig toggles generation of the OVN-offload DPUService
// (gap G11). Runtime toggles roll out progressively per node (E15) — the
// progressive logic lives in the translation controller; here we record the
// desired state.
func (v *NvidiaVSP) SetDpuNetworkConfig(ctx context.Context, req DpuNetworkConfigRequest) error {
	obj := DPFObject{
		GVK:       GVKDPUService,
		Namespace: ManagedNamespace,
		Name:      DeterministicName(ManagedNamespace, "ovn-offload", "accel"),
		Labels:    OwnerLabels("DpuNetworkConfig", ManagedNamespace, "cluster"),
		Spec:      map[string]any{"enabled": req.IsAccelerated, "helmChart": "ovn-offload"},
	}
	if !req.IsAccelerated {
		return v.dpf.Delete(ctx, obj.GVK, obj.Namespace, obj.Name)
	}
	return v.dpf.Apply(ctx, obj)
}

// Ping implements HeartbeatService. Health derives from the staleness of the
// locally cached cluster state (monotonic, clock-skew immune — E17), combined
// with initialization state.
func (v *NvidiaVSP) Ping(_ context.Context, req PingRequest) (PingResponse, error) {
	v.mu.RLock()
	healthy := v.initialized && time.Since(v.lastHealthySync) < v.cfg.HealthStaleness
	v.mu.RUnlock()
	return PingResponse{
		Timestamp:   req.Timestamp,
		ResponderID: "nvidia-vsp/" + v.cfg.NodeName,
		Healthy:     healthy,
	}, nil
}

// MarkSynced is invoked by informer callbacks whenever a full healthy view of
// DPF state is observed (DPF deployments Available, DPUCluster reachable, no
// owned CR Degraded) — the truth table of design §9.3.
func (v *NvidiaVSP) MarkSynced(t time.Time) {
	v.mu.Lock()
	v.lastHealthySync = t
	v.mu.Unlock()
}

// -----------------------------------------------------------------------------
// Section 5: Reconciliation adapter (controller-runtime shaped; design §9)
// -----------------------------------------------------------------------------

// Request identifies the OPI object being reconciled.
type Request struct {
	Namespace string
	Name      string
}

// Result mirrors controller-runtime's reconcile.Result.
type Result struct {
	Requeue      bool
	RequeueAfter time.Duration
}

// Reconciler is the single-method contract every controller implements.
type Reconciler interface {
	Reconcile(ctx context.Context, req Request) (Result, error)
}

// Backoff computes capped exponential backoff (base 1s, cap 5m; design §9.4).
func Backoff(attempt int) time.Duration {
	d := time.Second
	for i := 0; i < attempt && d < 5*time.Minute; i++ {
		d *= 2
	}
	if d > 5*time.Minute {
		d = 5 * time.Minute
	}
	return d
}

// FinalizerName guards cascading GC across the translation boundary (E9/E10).
const FinalizerName = "opi.opiproject.org/nvidia-vsp-cleanup"

// HasFinalizer / AddFinalizer / RemoveFinalizer are the minimal helpers the
// SFC reconciler uses to hold deletion until derived DPF objects are gone.
func HasFinalizer(finalizers []string) bool {
	for _, f := range finalizers {
		if f == FinalizerName {
			return true
		}
	}
	return false
}

func AddFinalizer(finalizers []string) []string {
	if HasFinalizer(finalizers) {
		return finalizers
	}
	return append(finalizers, FinalizerName)
}

func RemoveFinalizer(finalizers []string) []string {
	out := finalizers[:0]
	for _, f := range finalizers {
		if f != FinalizerName {
			out = append(out, f)
		}
	}
	return out
}

// SFCReconciler is the CRD translation controller for ServiceFunctionChain
// objects (design §6.2-2, §8.3): level-triggered, stateless, deterministic.
type SFCReconciler struct {
	DPF DPFClient
	// GetSFC loads the OPI SFC; in production this is a typed client Get.
	// deleted=true models a deletionTimestamp'd object still holding our
	// finalizer.
	GetSFC func(ctx context.Context, req Request) (spec ServiceFunctionChainSpec, deleted bool, err error)
	// UpdateStatus writes aggregated conditions to the OPI CR status
	// subresource (single-writer rule W4).
	UpdateStatus func(ctx context.Context, req Request, conds []Condition) error
}

// Reconcile implements the downward translation and upward status
// propagation for one SFC.
func (r *SFCReconciler) Reconcile(ctx context.Context, req Request) (Result, error) {
	sfc, deleted, err := r.GetSFC(ctx, req)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return Result{}, nil // GC'd; owner labels make orphan sweep possible
		}
		return Result{Requeue: true}, err
	}

	selector := OwnerLabels("ServiceFunctionChain", req.Namespace, req.Name)

	if deleted {
		// Finalizer path (design §8.5): delete every derived object, verify
		// gone, only then allow finalizer removal (performed by the caller).
		owned, err := r.DPF.List(ctx, GVKDPUServiceChain, ManagedNamespace, selector)
		if err != nil {
			return Result{Requeue: true}, err
		}
		for _, gvk := range []GroupVersionKind{GVKDPUServiceChain, GVKDPUServiceInterface, GVKDPUServiceIPAM, GVKDPUService} {
			objs, err := r.DPF.List(ctx, gvk, ManagedNamespace, selector)
			if err != nil {
				return Result{Requeue: true}, err
			}
			for _, o := range objs {
				if err := r.DPF.Delete(ctx, o.GVK, o.Namespace, o.Name); err != nil {
					return Result{Requeue: true}, err
				}
			}
		}
		if len(owned) > 0 {
			// Deletion issued; requeue to confirm disappearance before the
			// finalizer is released (E9).
			return Result{RequeueAfter: 2 * time.Second}, nil
		}
		return Result{}, nil
	}

	// Downward: apply the deterministic plan (SSA upsert; drift correction).
	plan := TranslateSFC(sfc)
	for _, obj := range plan.Objects {
		if err := r.DPF.Apply(ctx, obj); err != nil {
			return Result{Requeue: true}, err
		}
	}

	// Upward: aggregate derived-object conditions into the OPI CR status.
	conds, allReady, err := r.aggregate(ctx, selector)
	if err != nil {
		return Result{Requeue: true}, err
	}
	if err := r.UpdateStatus(ctx, req, conds); err != nil {
		return Result{Requeue: true}, err
	}
	if !allReady {
		return Result{RequeueAfter: 5 * time.Second}, nil
	}
	return Result{}, nil
}

// aggregate implements the ChainProgrammed condition truth table (design §9.3).
func (r *SFCReconciler) aggregate(ctx context.Context, selector map[string]string) ([]Condition, bool, error) {
	chains, err := r.DPF.List(ctx, GVKDPUServiceChain, ManagedNamespace, selector)
	if err != nil {
		return nil, false, err
	}
	services, err := r.DPF.List(ctx, GVKDPUService, ManagedNamespace, selector)
	if err != nil {
		return nil, false, err
	}
	allReady := len(chains) > 0 && len(services) > 0
	for _, o := range append(chains, services...) {
		if !o.IsReady() {
			allReady = false
			break
		}
	}
	cond := Condition{
		Type:   "ChainProgrammed",
		Status: allReady,
		Reason: "AllDerivedObjectsReady",
	}
	if !allReady {
		cond.Reason = "AwaitingDPFConvergence"
		cond.Message = fmt.Sprintf("%d chain(s), %d service(s) derived; waiting for Ready", len(chains), len(services))
	}
	return []Condition{cond}, allReady, nil
}

// -----------------------------------------------------------------------------
// Section 6: DPF Lifecycle Manager state machine (design §6.2-1, E11/E12)
// -----------------------------------------------------------------------------

// InstallState enumerates the LCM's decision states.
type InstallState string

const (
	// StateAbsent: no DPF detected; LCM may install the pinned profile.
	StateAbsent InstallState = "Absent"
	// StateManaged: DPF installed by us (fieldManager markers match);
	// LCM owns upgrades and uninstall.
	StateManaged InstallState = "Managed"
	// StateForeign: a pre-existing DPF install detected; requires explicit
	// adoptExistingDPF=true. Never installed over, never uninstalled (E11).
	StateForeign InstallState = "Foreign"
	// StateAdopted: foreign DPF adopted after compatibility verification;
	// LCM manages only derived CRs.
	StateAdopted InstallState = "Adopted"
	// StateIncompatible: served CRD versions outside the supported range
	// (E12); LCM refuses to proceed and surfaces AdoptionRequired/Degraded.
	StateIncompatible InstallState = "Incompatible"
)

// DPFDetection is the preflight observation input to the state machine.
type DPFDetection struct {
	CRDsPresent       bool
	InstalledByUs     bool // release/field-manager markers == FieldManager
	ServedVersionsOK  bool // within [min,max] of the compatibility matrix
	AdoptionRequested bool // vendor config: adoptExistingDPF=true
}

// NextState is the pure preflight decision function of the LCM (design §8.1
// step "preflight"). Keeping it pure makes the E11/E12 matrix exhaustively
// unit-testable.
func NextState(d DPFDetection) InstallState {
	switch {
	case !d.CRDsPresent:
		return StateAbsent
	case d.InstalledByUs && d.ServedVersionsOK:
		return StateManaged
	case d.InstalledByUs && !d.ServedVersionsOK:
		return StateIncompatible
	case d.AdoptionRequested && d.ServedVersionsOK:
		return StateAdopted
	case d.AdoptionRequested && !d.ServedVersionsOK:
		return StateIncompatible
	default:
		return StateForeign
	}
}

// LifecycleManager reconciles the DPF installation itself (sub-operator /
// meta-operator pattern). Install/uninstall bodies are elided: production
// applies a pinned, digest-referenced manifest profile via SSA.
type LifecycleManager struct {
	DPF    DPFClient
	Detect func(ctx context.Context) (DPFDetection, error)
	// Install applies the pinned scoped DPF profile (CRDs first — E13).
	Install func(ctx context.Context) error
	// Uninstall removes DPF; the caller MUST verify state==StateManaged:
	// adopted installs are never uninstalled (design §8.5 note).
	Uninstall func(ctx context.Context) error
}

// Reconcile drives DPF toward the desired installation state and returns the
// resulting conditions for aggregation into DpuOperatorConfig status.
func (m *LifecycleManager) Reconcile(ctx context.Context, uninstallRequested bool) ([]Condition, Result, error) {
	det, err := m.Detect(ctx)
	if err != nil {
		return nil, Result{Requeue: true}, err
	}
	state := NextState(det)

	switch {
	case uninstallRequested && state == StateManaged:
		if err := m.Uninstall(ctx); err != nil {
			return nil, Result{Requeue: true}, err
		}
		return []Condition{{Type: "NvidiaDPFInstalled", Status: false, Reason: "Uninstalled"}}, Result{}, nil

	case uninstallRequested:
		// Adopted/foreign installs survive OPI teardown by design (E11).
		return []Condition{{Type: "NvidiaDPFInstalled", Status: false, Reason: "SkippedNotManaged",
			Message: "existing DPF install was not created by opi-nvidia-vsp; leaving in place"}}, Result{}, nil

	case state == StateAbsent:
		if err := m.Install(ctx); err != nil {
			return nil, Result{RequeueAfter: Backoff(1)}, err
		}
		return []Condition{{Type: "NvidiaDPFInstalled", Status: true, Reason: "Installed"}},
			Result{RequeueAfter: 10 * time.Second}, nil

	case state == StateManaged, state == StateAdopted:
		return []Condition{{Type: "NvidiaDPFInstalled", Status: true, Reason: string(state)}}, Result{}, nil

	case state == StateForeign:
		return []Condition{{Type: "AdoptionRequired", Status: true, Reason: "ForeignDPFDetected",
			Message: "set adoptExistingDPF=true in the NVIDIA vendor config to adopt"}}, Result{}, nil

	default: // StateIncompatible
		return []Condition{{Type: "ProvisioningDegraded", Status: true, Reason: "DPFVersionIncompatible",
			Message: "served DPF CRD versions are outside the supported compatibility range; CRD downgrade is refused"}},
			Result{}, nil
	}
}

// -----------------------------------------------------------------------------
// Section 7: Version-Compatibility & Force-Cleanup (design §9.4, §10 E21)
// -----------------------------------------------------------------------------

// VersionCompatibilityController performs API discovery against both OPI and DPF CRD sets.
type VersionCompatibilityController struct {
	DiscoveryClient func(ctx context.Context) (opiVer, dpfVer string, err error)
	// Matrix maps: map[OPI_version]map[DPF_version]TranslatorProfile
	Matrix          map[string]map[string]string
	// EmitEventAndMetric mock function for E12
	EmitEventAndMetric func(message string)
}

// CheckCompatibility checks compatibility against the active matrix.
func (v *VersionCompatibilityController) CheckCompatibility(ctx context.Context) (Condition, bool, error) {
	opiVer, dpfVer, err := v.DiscoveryClient(ctx)
	if err != nil {
		return Condition{}, false, err
	}
	
	dpfMap, ok := v.Matrix[opiVer]
	if ok {
		if profile, found := dpfMap[dpfVer]; found && profile != "" {
			return Condition{
				Type:    "VersionIncompatible",
				Status:  false,
				Reason:  "Compatible",
				Message: fmt.Sprintf("Compatibility validated: (OPI %s, DPF %s) matches profile %s", opiVer, dpfVer, profile),
			}, true, nil
		}
	}
	
	msg := fmt.Sprintf("No compatibility profile found for (OPI: %s, DPF: %s)", opiVer, dpfVer)
	if v.EmitEventAndMetric != nil {
		v.EmitEventAndMetric(msg)
	}
	return Condition{
		Type:    "VersionIncompatible",
		Status:  true,
		Reason:  "NoMatchingProfile",
		Message: msg,
	}, false, nil
}

// ForceCleanupChecker implements the deadlock escape hatch (E21).
type ForceCleanupChecker struct {
	DPFClient DPFClient
	// CheckDPFOperatorAbsent checks Deployment/leader Lease absence to confirm DPF is uninstalled/dead.
	CheckDPFOperatorAbsent func(ctx context.Context) (bool, error)
	// EmitEventAndAuditLog mock function for high-severity events and audit entries.
	EmitEventAndAuditLog func(message string, severity string)
}

// ReconcileForceCleanup checks for stuck child objects and performs force-cleanup if requested.
func (f *ForceCleanupChecker) ReconcileForceCleanup(ctx context.Context, obj DPFObject, elapsed time.Duration, hasForceAnnotation bool) (Condition, bool, error) {
	// (1) If elapsed time is less than 30 minutes, we are still within normal boundaries.
	if elapsed < 30*time.Minute {
		return Condition{}, false, nil
	}
	
	// (2) Verify DPF operator absence.
	absent, err := f.CheckDPFOperatorAbsent(ctx)
	if err != nil {
		return Condition{}, false, err
	}
	if !absent {
		// DPF is present but slow.
		return Condition{
			Type:    "DPFOperatorUnresponsive",
			Status:  false,
			Reason:  "DPFOperatorSlow",
			Message: "DPF child is stuck, but DPF operator is still running/responsive. Awaiting standard cleanup.",
		}, false, nil
	}
	
	// DPF operator is confirmed absent and timeout exceeded.
	cond := Condition{
		Type:    "DPFOperatorUnresponsive",
		Status:  true,
		Reason:  "DPFOperatorConfirmedAbsent",
		Message: fmt.Sprintf("Child object %s has been terminating for %s and DPF operator is confirmed absent. Deadlock detected.", obj.Name, elapsed),
	}
	
	// (3) Gate force cleanup on explicit human annotation.
	if !hasForceAnnotation {
		return cond, false, nil
	}
	
	// (4) Execute force cleanup: strip finalizers.
	f.EmitEventAndAuditLog(fmt.Sprintf("Force cleanup annotation detected. Stripping finalizers from stuck child %s", obj.Name), "HIGH")
	
	// In the real implementation, this patches the object to remove DPF's finalizers.
	err = f.DPFClient.Delete(ctx, obj.GVK, obj.Namespace, obj.Name)
	if err != nil {
		return cond, false, err
	}
	
	f.EmitEventAndAuditLog(fmt.Sprintf("Successfully stripped finalizers and deleted %s", obj.Name), "INFO")
	return cond, true, nil
}

