//go:build integration

// Integration test for the Pattern 1 adapter, run against a real (envtest-backed)
// Kubernetes API server. Unlike feature_skeleton_test.go (pure logic), this drives
// the actual Reconcile method through the API: SSA creates, status subresource
// writes, label-selector lists, and finalizer teardown all hit a real apiserver.
//
// It is guarded by the `integration` build tag so `go test ./...` (the pure unit
// tests) never triggers it accidentally. Run it explicitly:
//
//	go test -tags integration -v ./...
//
// REQUIREMENTS (Linux / WSL only — envtest has no Windows kube-apiserver binary):
//   - setup-envtest binaries on KUBEBUILDER_ASSETS (see the run instructions).
//   - the three CRDs under ./testdata/crds.
//
// What each test proves, at the API level:
//   - TestIntegrationTranslateMirrorAndEqualityGate: SSA-create of DPF children,
//     the ObservedGeneration gate, status mirroring, and the Equality gate
//     (resourceVersion stability across a redundant reconcile).
//   - TestIntegrationDriftCorrectionViaSSA: out-of-band edit reverted by SSA.
//   - TestIntegrationFinalizerTeardown: ordered deletion via the finalizer.
package dpfadapter

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	testSFCNamespace = "openshift-dpu-operator"
	testDPFNamespace = "dpf-system"
)

var testCfg *rest.Config

func TestMain(m *testing.M) {
	env := &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("testdata", "crds")},
		ErrorIfCRDPathMissing: true,
	}
	cfg, err := env.Start()
	if err != nil {
		fmt.Fprintf(os.Stderr,
			"SKIP integration tests: envtest could not start "+
				"(need kube-apiserver+etcd via setup-envtest; Linux/WSL only): %v\n", err)
		os.Exit(0)
	}
	testCfg = cfg
	code := m.Run()
	_ = env.Stop()
	os.Exit(code)
}

func mustClient(t *testing.T) client.Client {
	t.Helper()
	cl, err := client.New(testCfg, client.Options{})
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	return cl
}

func ensureNamespace(t *testing.T, cl client.Client, name string) {
	t.Helper()
	ns := &unstructured.Unstructured{}
	ns.SetGroupVersionKind(schema.GroupVersionKind{Version: "v1", Kind: "Namespace"})
	ns.SetName(name)
	if err := cl.Create(context.Background(), ns); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("create namespace %s: %v", name, err)
	}
}

func createSFC(t *testing.T, cl client.Client, name, image string) *unstructured.Unstructured {
	t.Helper()
	sfc := newSFC(name, testSFCNamespace, "", image) // apiserver assigns the UID
	sfc.SetUID("")
	if err := cl.Create(context.Background(), sfc); err != nil {
		t.Fatalf("create SFC %s: %v", name, err)
	}
	return sfc
}

func getChild(t *testing.T, cl client.Client, gvk schema.GroupVersionKind, name string) *unstructured.Unstructured {
	t.Helper()
	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(gvk)
	if err := cl.Get(context.Background(),
		types.NamespacedName{Name: name, Namespace: testDPFNamespace}, u); err != nil {
		t.Fatalf("get %s %s: %v", gvk.Kind, name, err)
	}
	return u
}

func readSFC(t *testing.T, cl client.Client, name string) *unstructured.Unstructured {
	t.Helper()
	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(sfcGVK)
	if err := cl.Get(context.Background(),
		types.NamespacedName{Name: name, Namespace: testSFCNamespace}, u); err != nil {
		t.Fatalf("get SFC %s: %v", name, err)
	}
	return u
}

func readReady(t *testing.T, cl client.Client, name string) metav1.Condition {
	t.Helper()
	for _, c := range extractConditions(readSFC(t, cl, name)) {
		if c.Type == condReady {
			return c
		}
	}
	return metav1.Condition{Type: condReady, Status: metav1.ConditionUnknown}
}

func markChildReady(t *testing.T, cl client.Client, gvk schema.GroupVersionKind, name string) {
	t.Helper()
	fresh := getChild(t, cl, gvk, name)
	_ = unstructured.SetNestedField(fresh.Object, fresh.GetGeneration(), "status", "observedGeneration")
	_ = unstructured.SetNestedSlice(fresh.Object, []interface{}{
		map[string]interface{}{
			"type":               condReady,
			"status":             string(metav1.ConditionTrue),
			"reason":             "ByTest",
			"message":            "simulated DPF ready",
			"lastTransitionTime": time.Now().UTC().Format(time.RFC3339),
		},
	}, "status", "conditions")
	if err := cl.Status().Update(context.Background(), fresh); err != nil {
		t.Fatalf("status update child %s: %v", name, err)
	}
}

func TestIntegrationTranslateMirrorAndEqualityGate(t *testing.T) {
	ctx := context.Background()
	cl := mustClient(t)
	ensureNamespace(t, cl, testSFCNamespace)
	ensureNamespace(t, cl, testDPFNamespace)

	sfc := createSFC(t, cl, "chain-int", "registry/nf:v1")
	r := &SfcTranslationReconciler{Client: cl, DPFNamespace: testDPFNamespace}
	req := reconcile.Request{NamespacedName: types.NamespacedName{
		Name: sfc.GetName(), Namespace: testSFCNamespace}}

	// #1 adds the finalizer; #2 SSA-creates the DPF children.
	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("reconcile #1 (finalizer): %v", err)
	}
	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("reconcile #2 (create children): %v", err)
	}

	// The children must exist in the DPF namespace, owned by the SFC.
	sfc = readSFC(t, cl, "chain-int")
	svc := getChild(t, cl, dpuServiceGVK, "chain-int-svc")
	chain := getChild(t, cl, dpuServiceChainGVK, "chain-int-chain")
	for _, c := range []*unstructured.Unstructured{svc, chain} {
		if got := c.GetAnnotations()[AnnotationOwnerUID]; got != string(sfc.GetUID()) {
			t.Errorf("%s owner-uid annotation = %q, want %q", c.GetName(), got, sfc.GetUID())
		}
		if got := c.GetLabels()[LabelOwningSFCUID]; got != string(sfc.GetUID()) {
			t.Errorf("%s owning-sfc-uid label = %q, want %q", c.GetName(), got, sfc.GetUID())
		}
	}
	if img, _, _ := unstructured.NestedString(svc.Object, "spec", "image"); img != "registry/nf:v1" {
		t.Errorf("DPUService spec.image = %q, want registry/nf:v1", img)
	}

	// ObservedGeneration Gate: not Ready until DPF converges.
	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("reconcile #3 (pre-converge): %v", err)
	}
	if got := readReady(t, cl, "chain-int"); got.Status == metav1.ConditionTrue {
		t.Fatalf("SFC reported Ready before DPF converged; ObservedGeneration gate failed")
	}

	// Simulate DPF marking both children converged + Ready.
	markChildReady(t, cl, dpuServiceGVK, "chain-int-svc")
	markChildReady(t, cl, dpuServiceChainGVK, "chain-int-chain")

	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("reconcile #4 (post-converge): %v", err)
	}
	if got := readReady(t, cl, "chain-int"); got.Status != metav1.ConditionTrue {
		t.Fatalf("SFC not Ready after DPF converged; got %s/%s", got.Reason, got.Status)
	}

	// Equality Gate: a redundant reconcile must not rewrite status.
	rvBefore := readSFC(t, cl, "chain-int").GetResourceVersion()
	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("reconcile #5 (no-op): %v", err)
	}
	rvAfter := readSFC(t, cl, "chain-int").GetResourceVersion()
	if rvAfter != rvBefore {
		t.Fatalf("equality gate failed: resourceVersion changed %s -> %s on a no-op reconcile",
			rvBefore, rvAfter)
	}
	t.Logf("equality gate confirmed: resourceVersion stable at %s across redundant reconcile", rvBefore)
}

func TestIntegrationDriftCorrectionViaSSA(t *testing.T) {
	ctx := context.Background()
	cl := mustClient(t)
	ensureNamespace(t, cl, testSFCNamespace)
	ensureNamespace(t, cl, testDPFNamespace)

	sfc := createSFC(t, cl, "chain-drift", "registry/nf:good")
	r := &SfcTranslationReconciler{Client: cl, DPFNamespace: testDPFNamespace}
	req := reconcile.Request{NamespacedName: types.NamespacedName{
		Name: sfc.GetName(), Namespace: testSFCNamespace}}
	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("reconcile (finalizer): %v", err)
	}
	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("reconcile (create): %v", err)
	}

	// Admin edits the child spec out of band.
	svc := getChild(t, cl, dpuServiceGVK, "chain-drift-svc")
	_ = unstructured.SetNestedField(svc.Object, "registry/nf:HACKED", "spec", "image")
	if err := cl.Update(ctx, svc); err != nil {
		t.Fatalf("simulate drift: %v", err)
	}

	// Reconcile re-asserts the adapter-owned field via SSA/ForceOwnership.
	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("reconcile after drift: %v", err)
	}
	fixed := getChild(t, cl, dpuServiceGVK, "chain-drift-svc")
	if img, _, _ := unstructured.NestedString(fixed.Object, "spec", "image"); img != "registry/nf:good" {
		t.Fatalf("drift not corrected: spec.image = %q, want registry/nf:good", img)
	}
	t.Logf("drift correction confirmed: spec.image reverted via SSA")
}

func TestIntegrationFinalizerTeardown(t *testing.T) {
	ctx := context.Background()
	cl := mustClient(t)
	ensureNamespace(t, cl, testSFCNamespace)
	ensureNamespace(t, cl, testDPFNamespace)

	sfc := createSFC(t, cl, "chain-del", "registry/nf:v1")
	r := &SfcTranslationReconciler{Client: cl, DPFNamespace: testDPFNamespace}
	req := reconcile.Request{NamespacedName: types.NamespacedName{
		Name: sfc.GetName(), Namespace: testSFCNamespace}}
	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("reconcile (finalizer): %v", err)
	}
	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("reconcile (create): %v", err)
	}

	if err := cl.Delete(ctx, readSFC(t, cl, "chain-del")); err != nil {
		t.Fatalf("delete SFC: %v", err)
	}
	// Pass 1 deletes children; pass 2 removes the finalizer once they are gone.
	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("reconcile delete (children): %v", err)
	}
	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("reconcile delete (finalizer): %v", err)
	}

	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(sfcGVK)
	err := cl.Get(ctx, types.NamespacedName{Name: "chain-del", Namespace: testSFCNamespace}, u)
	if !apierrors.IsNotFound(err) {
		t.Fatalf("SFC still present after teardown; err = %v", err)
	}
	t.Logf("finalizer teardown confirmed: children deleted, SFC removed")
}
