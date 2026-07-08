//go:build e2e

// Kind e2e lane: runs against a live cluster (started by scripts/e2e-kind.sh).
// The script sets KUBECONFIG and applies testdata/crds before invoking:
//
//	go test -tags e2e -v ./...
//
// When Docker/Kind is unavailable, scripts/e2e-kind.sh sets USE_ENVTEST_E2E=1
// and drives the same golden-object tests against envtest.
package nvidiadpf

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

var e2eRestConfig *rest.Config

func TestMain(m *testing.M) {
	if os.Getenv("USE_ENVTEST_E2E") == "1" {
		env := &envtest.Environment{
			CRDDirectoryPaths:     []string{filepath.Join("testdata", "crds")},
			ErrorIfCRDPathMissing: true,
		}
		cfg, err := env.Start()
		if err != nil {
			fmt.Fprintf(os.Stderr, "envtest e2e fallback failed: %v\n", err)
			os.Exit(1)
		}
		e2eRestConfig = cfg
		code := m.Run()
		_ = env.Stop()
		os.Exit(code)
	}
	os.Exit(m.Run())
}

func e2eClient(t *testing.T) client.Client {
	t.Helper()
	var cfg *rest.Config
	var err error
	if e2eRestConfig != nil {
		cfg = e2eRestConfig
	} else {
		kubeconfig := os.Getenv("KUBECONFIG")
		if kubeconfig == "" {
			t.Skip("KUBECONFIG not set; run ./scripts/e2e-kind.sh")
		}
		cfg, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
		if err != nil {
			t.Skipf("Kind e2e skipped: invalid KUBECONFIG: %v", err)
		}
	}
	cl, err := client.New(cfg, client.Options{})
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	return cl
}

func TestE2EKindSFCGoldenApply(t *testing.T) {
	ctx := context.Background()
	cl := e2eClient(t)
	if err := EnsureNamespace(ctx, cl, ManagedNamespace); err != nil {
		t.Fatalf("namespace: %v", err)
	}

	spec := sampleSFC()
	var deleted bool
	r, req := &SFCReconciler{
		DPF: &EnvtestDPFClient{Client: cl},
		GetSFC: func(_ context.Context, _ Request) (ServiceFunctionChainSpec, bool, error) {
			return spec, deleted, nil
		},
		UpdateStatus: func(_ context.Context, _ Request, _ []Condition) error { return nil },
	}, Request{Namespace: spec.Namespace, Name: spec.Name}

	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	want := goldenObjectNames()
	for kind, names := range want {
		for _, name := range names {
			if _, err := r.DPF.Get(ctx, gvkForKindE2E(kind), ManagedNamespace, name); err != nil {
				t.Fatalf("Kind e2e missing %s/%s: %v", kind, name, err)
			}
		}
	}
	t.Logf("Kind e2e: golden contract %d objects present in cluster", len(TranslateSFC(spec).Objects))
}

func TestE2EKindTopologyBootstrap(t *testing.T) {
	ctx := context.Background()
	cl := e2eClient(t)
	for _, ns := range []string{ManagedNamespace, "tenant-a"} {
		if err := EnsureNamespace(ctx, cl, ns); err != nil {
			t.Fatalf("namespace %s: %v", ns, err)
		}
	}
	// Smoke: LCM absent-state decision is pure; e2e proves namespaces + API reachability.
	state := NextState(DPFDetection{CRDsPresent: false})
	if state != StateAbsent {
		t.Fatalf("expected Absent pre-install, got %s", state)
	}
	t.Log("Kind e2e bootstrap: API reachable; LCM preflight Absent as expected")
}

func gvkForKindE2E(kind string) GroupVersionKind {
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
		panic(fmt.Sprintf("unknown kind: %s", kind))
	}
}
