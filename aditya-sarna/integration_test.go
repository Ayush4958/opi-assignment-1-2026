//go:build integration

// Integration tests drive SFCReconciler against a real envtest apiserver with
// fake DPF CRDs installed. Run explicitly:
//
//	go test -tags integration -v ./...
//
// Requires setup-envtest binaries on KUBEBUILDER_ASSETS (Linux/macOS).
package nvidiadpf

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

var integrationCfg *rest.Config

func TestMain(m *testing.M) {
	env := &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("testdata", "crds")},
		ErrorIfCRDPathMissing: true,
	}
	cfg, err := env.Start()
	if err != nil {
		fmt.Fprintf(os.Stderr, "integration envtest start failed: %v\n", err)
		os.Exit(1)
	}
	integrationCfg = cfg
	code := m.Run()
	_ = env.Stop()
	os.Exit(code)
}

func integrationClient(t *testing.T) client.Client {
	t.Helper()
	cl, err := client.New(integrationCfg, client.Options{})
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	return cl
}

func integrationSFCReconciler(t *testing.T, cl client.Client, spec ServiceFunctionChainSpec, deleted *bool) (*SFCReconciler, Request) {
	t.Helper()
	dpf := &EnvtestDPFClient{Client: cl}
	r := &SFCReconciler{
		DPF: dpf,
		GetSFC: func(_ context.Context, req Request) (ServiceFunctionChainSpec, bool, error) {
			if deleted != nil && *deleted {
				return spec, true, nil
			}
			return spec, false, nil
		},
		UpdateStatus: func(_ context.Context, _ Request, conds []Condition) error {
			return nil
		},
	}
	return r, Request{Namespace: spec.Namespace, Name: spec.Name}
}

func TestIntegrationTranslateApplyAndReady(t *testing.T) {
	ctx := context.Background()
	cl := integrationClient(t)
	if err := EnsureNamespace(ctx, cl, ManagedNamespace); err != nil {
		t.Fatalf("namespace: %v", err)
	}

	spec := sampleSFC()
	var deleted bool
	r, req := integrationSFCReconciler(t, cl, spec, &deleted)

	res, err := r.Reconcile(ctx, req)
	if err != nil || res.RequeueAfter == 0 {
		t.Fatalf("first reconcile = %+v, %v (expected requeue while awaiting DPF)", res, err)
	}

	want := goldenObjectNames()
	for kind, names := range want {
		for _, name := range names {
			gvk := gvkForKind(kind)
			obj, err := r.DPF.Get(ctx, gvk, ManagedNamespace, name)
			if err != nil {
				t.Fatalf("missing %s/%s: %v", kind, name, err)
			}
			if obj.Labels[LabelOwnerNamespace] != spec.Namespace || obj.Labels[LabelOwnerName] != spec.Name {
				t.Fatalf("%s ownership labels wrong: %+v", name, obj.Labels)
			}
		}
	}

	chainName := DeterministicName(spec.Namespace, spec.Name, "chain")
	svcName := DeterministicName(spec.Namespace, spec.Name, "service")
	dpf := r.DPF.(*EnvtestDPFClient)
	if err := dpf.SetReady(ctx, GVKDPUServiceChain, ManagedNamespace, chainName); err != nil {
		t.Fatalf("set chain ready: %v", err)
	}
	if err := dpf.SetReady(ctx, GVKDPUService, ManagedNamespace, svcName); err != nil {
		t.Fatalf("set service ready: %v", err)
	}

	res, err = r.Reconcile(ctx, req)
	if err != nil || res.RequeueAfter != 0 {
		t.Fatalf("second reconcile = %+v, %v (expected complete)", res, err)
	}
	t.Logf("integration: %d derived objects applied; ChainProgrammed after DPF Ready", len(TranslateSFC(spec).Objects))
}

func TestIntegrationDriftCorrectionViaSSA(t *testing.T) {
	ctx := context.Background()
	cl := integrationClient(t)
	if err := EnsureNamespace(ctx, cl, ManagedNamespace); err != nil {
		t.Fatalf("namespace: %v", err)
	}

	spec := sampleSFC()
	var deleted bool
	r, req := integrationSFCReconciler(t, cl, spec, &deleted)
	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	svcName := DeterministicName(spec.Namespace, spec.Name, "service")
	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(gvkToSchema(GVKDPUService))
	if err := cl.Get(ctx, client.ObjectKey{Namespace: ManagedNamespace, Name: svcName}, u); err != nil {
		t.Fatalf("get service: %v", err)
	}
	if err := unstructured.SetNestedField(u.Object, "HACKED-CHART", "spec", "helmChart"); err != nil {
		t.Fatalf("drift inject: %v", err)
	}
	if err := cl.Update(ctx, u); err != nil {
		t.Fatalf("drift update: %v", err)
	}

	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("reconcile after drift: %v", err)
	}
	fixed, err := r.DPF.Get(ctx, GVKDPUService, ManagedNamespace, svcName)
	if err != nil {
		t.Fatalf("get fixed: %v", err)
	}
	if fixed.Spec["helmChart"] != "opi-nf-wrapper" {
		t.Fatalf("drift not corrected: helmChart = %v", fixed.Spec["helmChart"])
	}
	t.Logf("drift correction confirmed via SSA field manager %q", FieldManager)
}

func TestIntegrationFinalizerTeardown(t *testing.T) {
	ctx := context.Background()
	cl := integrationClient(t)
	if err := EnsureNamespace(ctx, cl, ManagedNamespace); err != nil {
		t.Fatalf("namespace: %v", err)
	}

	spec := sampleSFC()
	deleted := false
	r, req := integrationSFCReconciler(t, cl, spec, &deleted)
	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("create reconcile: %v", err)
	}

	deleted = true
	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("delete reconcile #1: %v", err)
	}
	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("delete reconcile #2: %v", err)
	}

	objs, _ := r.DPF.List(ctx, GVKDPUService, ManagedNamespace, OwnerLabels("ServiceFunctionChain", spec.Namespace, spec.Name))
	if len(objs) != 0 {
		t.Fatalf("expected zero derived objects after teardown, got %d", len(objs))
	}
	t.Logf("finalizer teardown confirmed: derived objects removed")
}

func gvkForKind(kind string) GroupVersionKind {
	switch kind {
	case "DPUService":
		return GVKDPUService
	case "DPUServiceChain":
		return GVKDPUServiceChain
	case "DPUServiceInterface":
		return GVKDPUServiceInterface
	case "DPUServiceIPAM":
		return GVKDPUServiceIPAM
	default:
		panic("unknown kind: " + kind)
	}
}
