# Assignment 1 — DPF-Adapter: verification & reproducibility

Control-plane verification for the Pattern 1 **DPF-Adapter** (Host-Cluster
Symmetric Shim). The adapter translates OPI CRs into NVIDIA DPF host CRs and
mirrors DPF status back. These tests prove that reconcile behaviour against a
real API server — **no BlueField hardware, no OVS, no DPF operator required.**

## Deliverables (what each file is)

| File | Contents |
|---|---|
| `architecture_design.md` | The full Pattern 1 architecture: OPI/DPF responsibilities, the integration boundary, why this pattern, translation mapping, the two-gate status loop, failure handling, gaps/risk registry, Mermaid diagrams, and the convergence proof (Appendix A). |
| `llm_transcript.json` | The LLM-assisted design dialogue (prompts + responses) that produced this design, as a `[{role, content}]` array. |
| `feature_skeleton.go` | The `SfcTranslationReconciler` on unstructured objects: SSA translation (field manager `opi-dpf-translator-shim`), the ObservedGeneration + Equality gates, finalizer teardown, `SetupWithManager`. Decision logic is factored into pure functions so it is unit-testable without a cluster. |
| `feature_skeleton_test.go` | Unit tests over the pure logic (translation + both gates). Run with plain `go test`. |
| `integration_test.go` | envtest suite (build tag `integration`): drives the real `Reconcile` against a live API server. |
| `testdata/crds/*.yaml` | Minimal stand-in CRDs (ServiceFunctionChain, DPUService, DPUServiceChain) that envtest installs so the API server accepts the objects. |
| `go.mod` / `go.sum` | Module + pinned dependencies (controller-runtime, apimachinery). |
| `validation_output.txt` | Captured `go test` output (evidence). |

## How to run

```bash
# 1. Unit tests — pure logic, no infra, runs anywhere Go is installed
go test -v ./...

# 2. Integration tests — real API server via envtest (Linux/WSL only)
go install sigs.k8s.io/controller-runtime/tools/setup-envtest@latest
export KUBEBUILDER_ASSETS=$(setup-envtest use -p path)
go test -tags integration -v ./...
```

envtest is Linux/WSL only (no Windows apiserver binary). The integration suite
loads the CRDs from `testdata/crds/`.

## Test analysis

**Unit layer (`go test -v ./...`) — all pass:**

| Test | What it proves |
|---|---|
| `TestSfcTranslateProducesOwnedDPFChildren` | One `ServiceFunctionChain` → the correct DPF children, stamped with the owner annotation + label; workload image carried across. |
| `TestSfcObservedGenerationGateHoldsBack` | No false `Ready` while a DPF child's `observedGeneration < generation`. |
| `TestSfcAllConvergedAndReadyYieldsReady` | Once all children are converged + Ready, the mirror reports `Ready`. |
| `TestSfcEqualityGateSuppressesRedundantWrite` | Identical input → identical target → no write (the write-amplification bound). |
| `TestSfcEqualityGateDetectsRealTransition` | A genuine status change is still detected (the gate isn't always-silent). |

```
ok  	dpfadapter	3.955s   (5/5 PASS)
```

This is control-plane verification, not a hardware test — it does not, and cannot without a BlueField DPU + a running DPF operator, prove the actual offload or DPF's provisioning. What it does do is take the design's core claims out of "argued on paper" and run them as real, passing code against a real API server. That distinction is the point: most integration designs are pure prose, and this one carries executable evidence for the part that is genuinely the author's to build.

**Integration layer (`go test -tags integration -v ./...`) — real API server:**

- SSA-creates `DPUService` + `DPUServiceChain` in the API server, owned by the SFC.
- ObservedGeneration gate holds `Ready` back until DPF status catches up.
- Equality gate: a no-op reconcile leaves the SFC `resourceVersion` **unchanged** (zero redundant writes, proven at the API level).
- Drift correction: an out-of-band edit to a DPF child is reverted via SSA/ForceOwnership.
- Finalizer teardown: deleting the SFC deletes the children first, then removes the finalizer.

## Scope (stated plainly)

Verifies the adapter's **control-plane** behaviour only. It does **not** prove
hardware offload, real OVS programming, or DPF's provisioning/fanout — those
sit on DPF's side of the boundary and need a BlueField device. The `testdata`
CRDs are minimal stand-ins, not DPF's real schemas, so the tests assert
ownership + status behaviour, never DPF-specific field shapes.
