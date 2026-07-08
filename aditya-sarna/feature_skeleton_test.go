// Test suite for feature_skeleton.go. This proves the skeleton is not just
// compilable but behaviorally correct for the design's core guarantees.
//
// Run: go test ./...
//
// Coverage maps to architecture_design.md edge cases:
//   TestDeterministicName_Stable        -> §7.3 idempotency foundation (E8)
//   TestTranslateSFC_GoldenShape         -> §7.2 SFC mapping
//   TestTranslateSFC_MatchesGolden       -> testdata/golden/sfc-web-chain.yaml contract
//   TestApplyPlan_Idempotent             -> §9.2 double-apply = no diff (E8)
//   TestNextState_TruthTable             -> §6.2-1 adoption/skew (E11/E12)
//   TestSetNumVfs_ShrinkGuard            -> §10 E5 in-use VF guard
//   TestSetNumVfs_Converges              -> §8.2 bounded-wait success
//   TestInit_Contract                    -> §8.2 / §6.3 dpu_mode + provisioned gate
//   TestCreateNF_UnavailableThenSuccess  -> §10 E3/E8 sync-to-async bridge
//   TestSFCReconciler_ApplyThenReady     -> §9.3 condition aggregation
//   TestSFCReconciler_FinalizerGC        -> §8.5 / E9 cascading delete
//   TestPing_HealthStaleness             -> §10 E17 staleness-based health
//   TestBackoffAndFinalizers             -> §9.4 helpers

package nvidiadpf

import (
	"context"
	"errors"
	"testing"
	"time"
)

// --- test doubles ------------------------------------------------------------

type fakeAlloc struct{ n int32 }

func (f fakeAlloc) AllocatedVFs() int32 { return f.n }

type fakeVFs struct{ ids []string }

func (f *fakeVFs) ListVFs() ([]string, error) { return append([]string(nil), f.ids...), nil }

func makeVFs(n int) *fakeVFs {
	ids := make([]string, n)
	for i := range ids {
		ids[i] = "0000:03:00." + string(rune('0'+i))
	}
	return &fakeVFs{ids: ids}
}

// --- Section 3: naming + translation -----------------------------------------

func TestDeterministicName_Stable(t *testing.T) {
	a := DeterministicName("ns", "chain-a", "service")
	b := DeterministicName("ns", "chain-a", "service")
	if a != b {
		t.Fatalf("names must be deterministic: %q != %q", a, b)
	}
	if c := DeterministicName("ns", "chain-a", "chain"); c == a {
		t.Fatalf("different roles must yield different names, both %q", c)
	}
	// Sanitization: uppercase/space become RFC1123-safe, non-empty.
	if got := sanitizeRFC1123("My Chain!"); got != "my-chain" {
		t.Fatalf("sanitize = %q, want %q", got, "my-chain")
	}
}

func sampleSFC() ServiceFunctionChainSpec {
	return ServiceFunctionChainSpec{
		Namespace: "tenant-a",
		Name:      "web-chain",
		NetworkFunctions: []NetworkFunction{
			{Name: "firewall", Image: "example.com/fw:1"},
			{Name: "router", Image: "example.com/rtr:1"},
		},
		IPAMSubnet:   "10.0.0.0/24",
		NodeSelector: map[string]string{"dpu": "true"},
	}
}

func TestTranslateSFC_GoldenShape(t *testing.T) {
	plan := TranslateSFC(sampleSFC())

	counts := map[string]int{}
	for _, o := range plan.Objects {
		counts[o.GVK.Kind]++
		if o.Namespace != ManagedNamespace {
			t.Fatalf("object %s not in managed namespace: %s", o.Name, o.Namespace)
		}
		if o.Labels[LabelComponent] != ComponentNvidiaVSP {
			t.Fatalf("object %s missing ownership labels", o.Name)
		}
	}
	// 1 DPUService + 1 DPUServiceChain + 2 interfaces/NF (×2 NFs) + 1 IPAM.
	if counts["DPUService"] != 1 || counts["DPUServiceChain"] != 1 ||
		counts["DPUServiceInterface"] != 4 || counts["DPUServiceIPAM"] != 1 {
		t.Fatalf("unexpected plan shape: %+v", counts)
	}
}

// goldenObjectNames is the normative name set for sampleSFC() — must match
// testdata/golden/sfc-web-chain.yaml and ADR-010 contract gate.
func goldenObjectNames() map[string][]string {
	return map[string][]string{
		"DPUService":          {"opi-f1cf853c-web-chain"},
		"DPUServiceChain":     {"opi-ae0ff21b-web-chain"},
		"DPUServiceIPAM":      {"opi-a18a1405-web-chain"},
		"DPUServiceInterface": {
			"opi-8bd6b935-web-chain", "opi-615109e6-web-chain",
			"opi-3c256f51-web-chain", "opi-0afdb2fe-web-chain",
		},
	}
}

func TestTranslateSFC_MatchesGolden(t *testing.T) {
	plan := TranslateSFC(sampleSFC())
	want := goldenObjectNames()

	got := map[string][]string{}
	for _, o := range plan.Objects {
		got[o.GVK.Kind] = append(got[o.GVK.Kind], o.Name)
	}
	for kind, names := range want {
		if len(got[kind]) != len(names) {
			t.Fatalf("%s: got %d objects, want %d", kind, len(got[kind]), len(names))
		}
		for i, n := range names {
			if got[kind][i] != n {
				t.Fatalf("%s[%d]: got %q, want %q", kind, i, got[kind][i], n)
			}
		}
	}
	// Key spec fields tied to golden YAML.
	for _, o := range plan.Objects {
		switch o.GVK.Kind {
		case "DPUService":
			if o.Spec["helmChart"] != "opi-nf-wrapper" {
				t.Fatalf("DPUService helmChart = %v", o.Spec["helmChart"])
			}
		case "DPUServiceIPAM":
			if o.Spec["subnet"] != "10.0.0.0/24" {
				t.Fatalf("IPAM subnet = %v", o.Spec["subnet"])
			}
		}
	}
}

func TestTranslateSFC_NoIPAMWhenUnset(t *testing.T) {
	s := sampleSFC()
	s.IPAMSubnet = ""
	for _, o := range TranslateSFC(s).Objects {
		if o.GVK.Kind == "DPUServiceIPAM" {
			t.Fatalf("no IPAM object should be generated when subnet is empty")
		}
	}
}

func TestApplyPlan_Idempotent(t *testing.T) {
	ctx := context.Background()
	c := NewInMemoryDPFClient()
	plan := TranslateSFC(sampleSFC())

	apply := func() int {
		for _, o := range plan.Objects {
			if err := c.Apply(ctx, o); err != nil {
				t.Fatalf("apply: %v", err)
			}
		}
		objs, _ := c.List(ctx, GVKDPUServiceInterface, ManagedNamespace, nil)
		return len(objs)
	}
	first := apply()
	second := apply() // re-applying identical plan must not create duplicates
	if first != second {
		t.Fatalf("double-apply not idempotent: %d then %d interfaces", first, second)
	}
}

// --- Section 6: lifecycle state machine (E11/E12) ----------------------------

func TestNextState_TruthTable(t *testing.T) {
	cases := []struct {
		name string
		d    DPFDetection
		want InstallState
	}{
		{"absent", DPFDetection{CRDsPresent: false}, StateAbsent},
		{"managed-ok", DPFDetection{CRDsPresent: true, InstalledByUs: true, ServedVersionsOK: true}, StateManaged},
		{"managed-skew", DPFDetection{CRDsPresent: true, InstalledByUs: true, ServedVersionsOK: false}, StateIncompatible},
		{"adopt-ok", DPFDetection{CRDsPresent: true, AdoptionRequested: true, ServedVersionsOK: true}, StateAdopted},
		{"adopt-skew", DPFDetection{CRDsPresent: true, AdoptionRequested: true, ServedVersionsOK: false}, StateIncompatible},
		{"foreign", DPFDetection{CRDsPresent: true}, StateForeign},
	}
	for _, tc := range cases {
		if got := NextState(tc.d); got != tc.want {
			t.Errorf("%s: NextState = %s, want %s", tc.name, got, tc.want)
		}
	}
}

// --- Section 4: NvidiaVSP behavior -------------------------------------------

func newVSP(dpf DPFClient, allocated int32, vfN int) *NvidiaVSP {
	cfg := DefaultVSPConfig("node-1")
	cfg.NFReadyTimeout = 2 * time.Second
	cfg.VFActuationTimeout = time.Second
	cfg.PollInterval = 5 * time.Millisecond
	return NewNvidiaVSP(cfg, dpf, fakeAlloc{n: allocated}, makeVFs(vfN))
}

func provisionedDPU(c *InMemoryDPFClient, node string) {
	_ = c.Apply(context.Background(), DPFObject{
		GVK:        GVKDPU,
		Namespace:  ManagedNamespace,
		Name:       "dpu-" + node,
		Spec:       map[string]any{"nodeName": node},
		Conditions: []Condition{{Type: "Ready", Status: true}},
	})
}

func TestInit_Contract(t *testing.T) {
	ctx := context.Background()
	c := NewInMemoryDPFClient()
	vsp := newVSP(c, 0, 4)

	// dpu_mode=true is UNIMPLEMENTED in v1 (topology decision, §6.3).
	if _, err := vsp.Init(ctx, InitRequest{DpuMode: true}); !errors.Is(err, ErrUnimplemented) {
		t.Fatalf("dpu_mode should be UNIMPLEMENTED, got %v", err)
	}
	// Host mode without a provisioned DPU CR fails closed (§8.2).
	if _, err := vsp.Init(ctx, InitRequest{DpuIdentifier: "x"}); !errors.Is(err, ErrFailedPrecondition) {
		t.Fatalf("expected FAILED_PRECONDITION without provisioned DPU, got %v", err)
	}
	// With a provisioned DPU, Init returns the serving endpoint.
	provisionedDPU(c, "node-1")
	ep, err := vsp.Init(ctx, InitRequest{DpuIdentifier: "x"})
	if err != nil || ep.Port != 50051 {
		t.Fatalf("Init after provisioning = %+v, %v", ep, err)
	}
}

func TestSetNumVfs_ShrinkGuard(t *testing.T) {
	ctx := context.Background()
	vsp := newVSP(NewInMemoryDPFClient(), 4 /*allocated*/, 4)
	// Requesting fewer VFs than are allocated to pods must be rejected (E5).
	if _, err := vsp.SetNumVfs(ctx, VfCount{VfCnt: 2}); !errors.Is(err, ErrFailedPrecondition) {
		t.Fatalf("shrink below allocated must fail, got %v", err)
	}
}

func TestSetNumVfs_Converges(t *testing.T) {
	ctx := context.Background()
	// Enumerator already reports 8 VFs, so actuation converges immediately.
	vsp := newVSP(NewInMemoryDPFClient(), 0, 8)
	got, err := vsp.SetNumVfs(ctx, VfCount{VfCnt: 8})
	if err != nil || got.VfCnt != 8 {
		t.Fatalf("SetNumVfs converge = %+v, %v", got, err)
	}
}

func TestGetDevices_HealthFromDPU(t *testing.T) {
	ctx := context.Background()
	c := NewInMemoryDPFClient()
	provisionedDPU(c, "node-1")
	vsp := newVSP(c, 0, 3)
	resp, err := vsp.GetDevices(ctx)
	if err != nil {
		t.Fatalf("GetDevices: %v", err)
	}
	if len(resp.Devices) != 3 {
		t.Fatalf("want 3 devices, got %d", len(resp.Devices))
	}
	for _, d := range resp.Devices {
		if d.Health != "Healthy" {
			t.Fatalf("device %s health = %s, want Healthy", d.ID, d.Health)
		}
	}
}

func chainKeyFor(node string, req NFRequest) string {
	base := DeterministicName(node, NFRequestHash(node, req), "nf")
	name := base + "-hop"
	return GVKDPUServiceChain.Group + "/" + GVKDPUServiceChain.Kind + "/" + ManagedNamespace + "/" + name
}

func TestCreateNF_UnavailableThenSuccess(t *testing.T) {
	ctx := context.Background()
	c := NewInMemoryDPFClient()
	req := NFRequest{Input: "vf:0000:03:00.1", Output: "chainref:web", BridgeID: "domain-a"}

	// (1) Chain never becomes Ready within the bounded wait -> UNAVAILABLE,
	//     which the CNI treats as retryable (E3).
	fast := newVSP(c, 0, 0)
	fast.cfg.NFReadyTimeout = 30 * time.Millisecond
	if err := fast.CreateNetworkFunction(ctx, req); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("cold chain should return UNAVAILABLE, got %v", err)
	}

	// (2) Retry path: while the call waits, DPF converges the chain to Ready.
	vsp := newVSP(c, 0, 0)
	res := make(chan error, 1)
	go func() { res <- vsp.CreateNetworkFunction(ctx, req) }()
	time.Sleep(30 * time.Millisecond) // let at least one apply+poll happen
	c.SetConditions(chainKeyFor("node-1", req), []Condition{{Type: "Ready", Status: true}})
	if err := <-res; err != nil {
		t.Fatalf("CreateNetworkFunction after convergence = %v", err)
	}

	// (3) Delete is idempotent even when objects are already gone (E8).
	if err := vsp.DeleteNetworkFunction(ctx, req); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := vsp.DeleteNetworkFunction(ctx, req); err != nil {
		t.Fatalf("second delete must be idempotent: %v", err)
	}
}

func TestPing_HealthStaleness(t *testing.T) {
	ctx := context.Background()
	c := NewInMemoryDPFClient()
	provisionedDPU(c, "node-1")
	vsp := newVSP(c, 0, 1)

	// Not healthy before Init (not initialized).
	if r, _ := vsp.Ping(ctx, PingRequest{}); r.Healthy {
		t.Fatalf("should be unhealthy before Init")
	}
	if _, err := vsp.Init(ctx, InitRequest{DpuIdentifier: "x"}); err != nil {
		t.Fatalf("init: %v", err)
	}
	vsp.MarkSynced(time.Now())
	if r, _ := vsp.Ping(ctx, PingRequest{}); !r.Healthy {
		t.Fatalf("should be healthy after Init + fresh sync")
	}
	// Stale cache -> unhealthy (E17: staleness-based, not timestamp-based).
	vsp.MarkSynced(time.Now().Add(-10 * time.Minute))
	if r, _ := vsp.Ping(ctx, PingRequest{}); r.Healthy {
		t.Fatalf("should be unhealthy when cached state is stale")
	}
}

// --- Section 5: reconciler (aggregation + finalizer GC) ----------------------

func TestSFCReconciler_ApplyThenReady(t *testing.T) {
	ctx := context.Background()
	c := NewInMemoryDPFClient()
	sfc := sampleSFC()
	var lastConds []Condition

	r := &SFCReconciler{
		DPF: c,
		GetSFC: func(_ context.Context, _ Request) (ServiceFunctionChainSpec, bool, error) {
			return sfc, false, nil
		},
		UpdateStatus: func(_ context.Context, _ Request, conds []Condition) error {
			lastConds = conds
			return nil
		},
	}
	req := Request{Namespace: sfc.Namespace, Name: sfc.Name}

	// First pass: objects applied, nothing Ready yet -> requeue, not programmed.
	res, err := r.Reconcile(ctx, req)
	if err != nil || res.RequeueAfter == 0 {
		t.Fatalf("first reconcile = %+v, %v (expected requeue)", res, err)
	}
	if lastConds[0].Type != "ChainProgrammed" || lastConds[0].Status {
		t.Fatalf("expected ChainProgrammed=false, got %+v", lastConds)
	}

	// Simulate DPF converging the chain and the wrapper service.
	mark := func(gvk GroupVersionKind, role string) {
		key := gvk.Group + "/" + gvk.Kind + "/" + ManagedNamespace + "/" +
			DeterministicName(sfc.Namespace, sfc.Name, role)
		c.SetConditions(key, []Condition{{Type: "Ready", Status: true}})
	}
	mark(GVKDPUServiceChain, "chain")
	mark(GVKDPUService, "service")

	res, err = r.Reconcile(ctx, req)
	if err != nil || res.RequeueAfter != 0 {
		t.Fatalf("second reconcile = %+v, %v (expected no requeue)", res, err)
	}
	if !lastConds[0].Status {
		t.Fatalf("expected ChainProgrammed=true after convergence, got %+v", lastConds)
	}
}

func TestSFCReconciler_FinalizerGC(t *testing.T) {
	ctx := context.Background()
	c := NewInMemoryDPFClient()
	sfc := sampleSFC()
	for _, o := range TranslateSFC(sfc).Objects { // pre-seed derived objects
		_ = c.Apply(ctx, o)
	}
	deleted := true
	r := &SFCReconciler{
		DPF: c,
		GetSFC: func(_ context.Context, _ Request) (ServiceFunctionChainSpec, bool, error) {
			return sfc, deleted, nil
		},
		UpdateStatus: func(_ context.Context, _ Request, _ []Condition) error { return nil },
	}
	req := Request{Namespace: sfc.Namespace, Name: sfc.Name}

	// First deletion pass removes derived objects and asks for a confirm requeue.
	res, err := r.Reconcile(ctx, req)
	if err != nil || res.RequeueAfter == 0 {
		t.Fatalf("delete pass 1 = %+v, %v (expected confirm requeue)", res, err)
	}
	// Second pass sees nothing owned -> safe to release finalizer (no requeue).
	res, err = r.Reconcile(ctx, req)
	if err != nil || res.RequeueAfter != 0 {
		t.Fatalf("delete pass 2 = %+v, %v (expected done)", res, err)
	}
	sel := OwnerLabels("ServiceFunctionChain", sfc.Namespace, sfc.Name)
	for _, gvk := range []GroupVersionKind{GVKDPUServiceChain, GVKDPUServiceInterface, GVKDPUService, GVKDPUServiceIPAM} {
		objs, _ := c.List(ctx, gvk, ManagedNamespace, sel)
		if len(objs) != 0 {
			t.Fatalf("orphaned %s objects after GC: %d", gvk.Kind, len(objs))
		}
	}
}

func TestBackoffAndFinalizers(t *testing.T) {
	if Backoff(0) != time.Second {
		t.Fatalf("Backoff(0) = %v, want 1s", Backoff(0))
	}
	if Backoff(100) > 5*time.Minute {
		t.Fatalf("Backoff must cap at 5m, got %v", Backoff(100))
	}
	f := AddFinalizer(nil)
	if !HasFinalizer(f) {
		t.Fatalf("AddFinalizer/HasFinalizer mismatch")
	}
	if HasFinalizer(RemoveFinalizer(f)) {
		t.Fatalf("RemoveFinalizer failed")
	}
	// AddFinalizer is idempotent.
	if len(AddFinalizer(f)) != 1 {
		t.Fatalf("AddFinalizer must not duplicate")
	}
}

func TestVersionCompatibilityController(t *testing.T) {
	ctx := context.Background()
	matrix := map[string]map[string]string{
		"v1alpha1": {
			"v1alpha1": "TranslatorV1Alpha1",
			"v1beta1":  "TranslatorV1Alpha1",
		},
		"v1beta1": {
			"v1beta1": "TranslatorV1Beta1",
		},
		"v1": {
			"v1": "TranslatorV1",
		},
	}

	var emittedMessage string
	vcc := &VersionCompatibilityController{
		Matrix: matrix,
		EmitEventAndMetric: func(msg string) {
			emittedMessage = msg
		},
	}

	// 1. Compatible match
	vcc.DiscoveryClient = func(ctx context.Context) (string, string, error) {
		return "v1", "v1", nil
	}
	cond, ok, err := vcc.CheckCompatibility(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatalf("expected compatible match to be ok")
	}
	if cond.Type != "VersionIncompatible" || cond.Status {
		t.Fatalf("expected VersionIncompatible=false on match, got %+v", cond)
	}

	// 2. Incompatible match (missing pair)
	vcc.DiscoveryClient = func(ctx context.Context) (string, string, error) {
		return "v1beta1", "v1alpha1", nil
	}
	cond, ok, err = vcc.CheckCompatibility(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatalf("expected incompatible match to not be ok")
	}
	if cond.Type != "VersionIncompatible" || !cond.Status {
		t.Fatalf("expected VersionIncompatible=true on mismatch, got %+v", cond)
	}
	if emittedMessage == "" {
		t.Fatalf("expected event/metric callback to be invoked on incompatibility")
	}
}

func TestForceCleanupChecker(t *testing.T) {
	ctx := context.Background()
	c := NewInMemoryDPFClient()
	obj := DPFObject{
		GVK:       GVKDPUServiceChain,
		Namespace: ManagedNamespace,
		Name:      "stuck-chain",
	}
	_ = c.Apply(ctx, obj)

	dpfAbsent := false
	var loggedMsg string
	fcc := &ForceCleanupChecker{
		DPFClient: c,
		CheckDPFOperatorAbsent: func(ctx context.Context) (bool, error) {
			return dpfAbsent, nil
		},
		EmitEventAndAuditLog: func(msg, sev string) {
			loggedMsg = msg
		},
	}

	// 1. Short elapsed time -> no action, not unresponsive
	cond, cleaned, err := fcc.ReconcileForceCleanup(ctx, obj, 10*time.Minute, false)
	if err != nil || cleaned || cond.Type != "" {
		t.Fatalf("expected no action for short elapsed, got: cond=%+v, cleaned=%v, err=%v", cond, cleaned, err)
	}

	// 2. Timeout exceeded, but DPF operator is still present (not absent) -> unresponsive=false (slow)
	cond, cleaned, err = fcc.ReconcileForceCleanup(ctx, obj, 35*time.Minute, false)
	if err != nil || cleaned {
		t.Fatalf("expected no force cleanup when operator present, got error: %v", err)
	}
	if cond.Type != "DPFOperatorUnresponsive" || cond.Status {
		t.Fatalf("expected DPFOperatorUnresponsive=false (slow), got %+v", cond)
	}

	// 3. Timeout exceeded, DPF operator absent, NO force annotation -> unresponsive=true, but not cleaned/deleted
	dpfAbsent = true
	cond, cleaned, err = fcc.ReconcileForceCleanup(ctx, obj, 35*time.Minute, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cleaned {
		t.Fatalf("should not clean up without force annotation")
	}
	if cond.Type != "DPFOperatorUnresponsive" || !cond.Status {
		t.Fatalf("expected DPFOperatorUnresponsive=true, got %+v", cond)
	}

	// Double check the object still exists
	_, err = c.Get(ctx, obj.GVK, obj.Namespace, obj.Name)
	if err != nil {
		t.Fatalf("stuck object should not be deleted yet: %v", err)
	}

	// 4. Timeout exceeded, DPF operator absent, WITH force annotation -> unresponsive=true, and cleaned/deleted
	cond, cleaned, err = fcc.ReconcileForceCleanup(ctx, obj, 35*time.Minute, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cleaned {
		t.Fatalf("expected object to be cleaned up")
	}
	if cond.Type != "DPFOperatorUnresponsive" || !cond.Status {
		t.Fatalf("expected DPFOperatorUnresponsive=true, got %+v", cond)
	}
	if loggedMsg == "" {
		t.Fatalf("expected event and audit log to be emitted")
	}

	// Verify object has been deleted from client
	_, err = c.Get(ctx, obj.GVK, obj.Namespace, obj.Name)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected object to be deleted, got: %v", err)
	}
}

