# Reviewer Guide (≈3 minutes)

**Candidate:** Aditya Sarna · **Assignment:** LFX OPI Hands-On #1 · **Doc version:** v1.9

## Decision in one line

**Pattern E (Hybrid):** NVIDIA VSP gRPC adapter + managed DPF sub-operator (LCM) + CRD translation controller — OPI owns intent, DPF owns hardware truth, VSP owns translation.

**Interactive simulator (v1.5-aligned):** [dpu-architect.lovable.app](https://dpu-architect.lovable.app)

## Assignment rubric scorecard (self-audit)

| Rubric lane | Evidence | Verified by |
|---|---|---|
| LLM-assisted design process | `llm_transcript.json` (~256 turns, `section_ref` per turn) + `TRANSCRIPT_INDEX.md` | Structured JSON; indexed to §sections |
| Architecture depth + trade-offs | `architecture_design.md` — **16 ADRs**, Pattern A–E scoring, §10 failures | Concern matrix **12/12 closed** |
| OPI/DPF alignment | Real OPI types (`ServiceFunctionChain`, `DpuOperatorConfig`); not invented CRDs | §7, `repo_analysis.md`, §7.6 receipts |
| NVIDIA DPF reuse | LCM + translator; DPF owns lifecycle; ADR-011 bundle | `config/nvidia/dpf-bundle.yaml` |
| Bonus skeleton | Behavioral tests + **gRPC daemon** (protobuf stubs) | `./scripts/demo-grpc.sh`; **82%** pkg coverage in `coverage_summary.txt` |
| Validation discipline | Recorded + CI-captured | `validation_output.txt`, `validation_ci_github.log` |
| Honest scope | Claim boundaries below | No production/GA overclaim |

**Peer gap this submission closes that adapter-only PRs typically do not:** SFC/NF path, Kamaji topology, **live gRPC VSP daemon**, version matrix, simulator↔golden contract, envtest+Kind CI proof.

## Claim boundaries (what this submission is / is not)

| Claim | In scope (A1) | Out of scope (later phases) |
|---|---|---|
| Architecture proposal | ✓ `architecture_design.md` | — |
| Compilable skeleton + tests | ✓ 22+ default tests; integration/Kind lanes | Full upstream OPI merge |
| DPF actuation | ✓ Declarative CR translation (fake/envtest/Kind) | Live BlueField lab (`BF3_LAB=1`) |
| gRPC VSP | ✓ Protobuf services + `cmd/vspdaemon` + contract tests | Upstream proto merge in OPI repo |
| LCM install | ✓ State machine + bundle pins | OLM/Helm install in cluster |
| CI proof | ✓ [Captured Actions run](validation_ci_github_summary.md) | Mentor must use log if repo private |

## 30-second demos (zero Docker)

```bash
chmod +x scripts/demo.sh scripts/demo-grpc.sh
./scripts/demo.sh       # golden SFC + simulator contract
./scripts/demo-grpc.sh  # gRPC Init / GetDevices / Ping / CreateNF
```

Live daemon (optional, two terminals):

```bash
go run ./cmd/vspdaemon -addr :50051 -seed-nf
go run ./cmd/vspclient -addr localhost:50051 -nf
```

Automated two-process demo: `VSP_LIVE_DEMO=1 ./scripts/demo-grpc.sh`

## Read in this order

| Step | File / section | Time | Why |
|---|---|---|---|
| 0 | `./scripts/demo.sh` | 20s | Golden SFC + simulator contract |
| 0b | `./scripts/demo-grpc.sh` | 30s | **gRPC stubs** → NvidiaVSP (Init/Ping/NF) |
| 1 | Concern Closure Matrix below | 30s | **12/12 closed** — audit proof columns |
| 2 | `repo_analysis.md` | 60s | Pinned SHAs + source receipts |
| 3 | `TRANSCRIPT_INDEX.md` + `llm_transcript.json` | 30s | LLM-assisted process evidence |
| 4 | `architecture_design.md` §5.2 (BP1) | 45s | Evidence-closed actuation path |
| 5 | `architecture_design.md` §6.3 | 60s | Topology modes — hardest mismatch |
| 6 | `architecture_design.md` §10 E1, E7, E21 | 45s | Critical failure mitigations |
| 7 | `./scripts/verify-all.sh` | 60s | All lanes → `validation_output.txt` |
| 8 | `validation_ci_reference.md` | 30s | CI / Kind mentor proof path |
| 9 | `validation_hardware_e2e.txt` | 15s | BF-3 lane contract record |
| 10 | [dpu-architect.lovable.app](https://dpu-architect.lovable.app) (optional) | 60s | Interactive simulator walkthrough |

Full depth: `architecture_design.md`. Fast path avoids reading it linearly.

**Submission bundle:** exclude `.tools/` (local Go toolchain only).

## Concern Closure Matrix

| Concern | Status | Proof |
|---|---|---|
| BP1: BlueField-local imperative SFC API? | **Closed** | §5.2, ADR-009 |
| Topology (Kamaji vs OPI two-cluster) | **Closed** | §6.3, ADR-002 |
| `CreateNetworkFunction` caller (Mode A) | **Closed** | §7.6, ADR-008 |
| Host VF dual-writer (E7) | **Closed** | §9.1, §10 E7 |
| NF image → DPUService (Q-D) | **Closed** | §7.5, ADR-010, `charts/opi-nf-wrapper/` |
| DPF packaging baked-in (Q-A) | **Closed** | ADR-011, `config/nvidia/dpf-bundle.yaml` |
| Version drift (Q-E) | **Closed** | ADR-012, `config/nvidia/compatibility.yaml`, `TestVersionCompatibilityController` |
| VSP framework extraction (Q-B) | **Closed** (defer v2) | ADR-013, §16.2 |
| Zero-Trust (Q-C) | **Closed** (Host-Trusted v1) | ADR-014, §11 |
| Multi-DPU N:1 per host | **Closed** (1:1 v1) | ADR-015, §16.3 A8 |
| Force-cleanup escape (E21) | **Closed** | §8.5 |
| Brownfield DPF adoption (E11) | **Closed** | ADR-003 |

**Open count: 0 / 12** — all concerns closed with explicit v1 boundaries or evidence.

## Pattern selection (§5.1 rubric)

| Pattern | Weighted score | Gate result |
|---|---|---|
| A Native | 42/70 | Rejected (NFR1) |
| B Pure adapter | 48/70 | Insufficient (FR1) |
| C Sub-operator | 53/70 | Necessary not sufficient |
| D CRD bridge only | 46/70 | Rejected (breaks VSP model) |
| **E Hybrid** | **67/70** | **Adopted** — no criterion below 4 |

## Operational safety (why this beats alternatives)

Kamaji CP loss during active SFC → **dataplane keeps forwarding** (§8.7); new attaches fail `UNAVAILABLE`; recovery is CP restore + level-triggered re-sync — no manual OVS surgery, no dual writer on host VFs. Pattern A would make OPI maintainers own every DOCA/firmware failure mode.

## Deliverables map

| Artifact | Purpose |
|---|---|
| `architecture_design.md` | Full proposal + 16 ADRs + diagrams |
| `repo_analysis.md` | Upstream grounding + pinned SHAs |
| `REVIEWER.md` | This guide |
| `TRANSCRIPT_INDEX.md` | LLM transcript → design section map |
| `llm_transcript.json` | LLM-assisted design transcript (`section_ref` on each turn) |
| `coverage.out` + `coverage_summary.txt` | Recorded test coverage (`go test -coverprofile`) |
| `feature_skeleton.go` + `_test.go` | Structural core + 18 behavioral unit/contract tests |
| `bf3_lane_test.go` + `e2e_bf3_test.go` | BF-3 hardware lane spec + optional lab gate |
| `integration_test.go` | envtest reconcile loop (SSA, drift, teardown) |
| `e2e_kind_test.go` + `scripts/e2e-kind.sh` | Kind cluster or envtest fallback golden-object lane |
| `simulator_contract_test.go` | Simulator ↔ golden YAML contract gate |
| `testdata/hardware/bf3-lane.yaml` | Phase 6 BF-3 e2e lane specification |
| `scripts/fetch-envtest.sh` | Auto-provision envtest binaries |
| `scripts/verify-all.sh` | Full multi-lane gate → validation artifacts |
| `scripts/e2e-bf3-hardware.sh` | BF-3 contract + optional lab runner |
| `.github/workflows/verify.yml` | CI: unit, integration, Kind, BF-3 contract |
| `validation_ci_reference.md` | Mentor CI / local Docker instructions |
| `validation_ci_github.log` | Captured GitHub Actions log (Kind + integration green) |
| `validation_ci_github_summary.md` | CI run URL + job summary |
| `validation_hardware_e2e.txt` | Recorded BF-3 lane output |
| `testdata/simulator/sfc-web-chain-contract.json` | Machine-readable simulator object graph |
| `scripts/resolve-bundle-digests.sh` | Verifies NGC/quay digest pins resolve |
| `cmd/vspdaemon/` + `cmd/vspclient/` + `vspgrpc/` + `api/vsp/` | OPI Vendor gRPC daemon + live demo client |
| `scripts/demo-grpc.sh` | gRPC contract demo (bufconn; no hardware) |
| `scripts/demo.sh` | 30-second golden-contract demo (no Docker) |
| `scripts/verify.sh` | One-command verification gate |
| `validation_output.txt` | Recorded verify output |
| `config/nvidia/compatibility.yaml` | Version matrix SSOT (ADR-012) |
| `config/nvidia/dpf-bundle.yaml` | Pinned DPF bundle (ADR-011) |
| `charts/opi-nf-wrapper/` | ADR-010 wrapper Helm chart |
| `testdata/golden/sfc-web-chain.yaml` | Golden OPI→DPF translation |
| [dpu-architect.lovable.app](https://dpu-architect.lovable.app) | Interactive architecture simulator (v1.5) |

## Quick validation

```bash
chmod +x scripts/*.sh
./scripts/verify-all.sh                 # all lanes → validation_output.txt
./scripts/e2e-bf3-hardware.sh           # BF-3 contract → validation_hardware_e2e.txt
# Full Kind (needs Docker): ./scripts/e2e-kind.sh
# CI proof: see validation_ci_reference.md
```

## Honest limitations

- BF-3 **hardware lab** e2e (`BF3_LAB=1`) is documented for Phase 6 GA; Assignment 1 ships **lane contract gate** (`TestBF3LaneSpec_Complete`) + Kind/envtest proof.
- Kind cluster e2e requires Docker; local record uses envtest fallback when Docker is stopped (CI `kind-e2e` job runs real Kind on Linux).
- Exact `DPUService` Helm field paths: reconfirm at implementation start (§7.6).
- Mode B (`OPIConverged`) adds certification burden vs Mode A default.
- N:1 multi-DPU-per-host explicitly out of v1 scope (ADR-015).
