# Repository Grounding

Read this **before** `architecture_design.md`. Every finding below was verified against upstream source — not inferred from docs alone.

**Repos:** [openshift/dpu-operator](https://github.com/openshift/dpu-operator) (OPI) · [NVIDIA/doca-platform](https://github.com/NVIDIA/doca-platform) (DPF)

**Pinned verification commits (2026-07-07):**

| Repo | Branch / ref | Commit SHA |
|---|---|---|
| openshift/dpu-operator | `main` | `846e1124583ccb197bc59f77203d707f96a01e4d` |
| NVIDIA/doca-platform | `public-main` | `3e0f454ceb038a1a5504b77c0c72b176f5afb8ec` |

Line references in §7.6 are stable across these commits for the cited call paths. Re-verify on pin bump.

---

## OPI DPU Operator

| # | Finding | Source pointer | Design impact |
|---|---|---|---|
| O-1 | Vendor seam is the `dpu-api` gRPC contract (`LifeCycleService`, `DeviceService`, `NetworkFunctionService`, …) | `dpu-api/api.proto` | VSP must implement all five services (§6.2-3) |
| O-2 | `CreateNetworkFunction` is invoked from the **DPU-side** daemon manager only | `internal/daemon/dpusidemanager.go:156`, `:173` | Mode A has no imperative per-pod attach caller (ADR-008) |
| O-3 | Host-side daemon manages bridge ports; **no** `CreateNetworkFunction` trigger | `internal/daemon/hostsidemanager.go:186-223` | SFC attach in Mode A is declarative via translation (§8.3) |
| O-4 | Host vs DPU side is selected structurally by `Spec.IsDpuSide` | `internal/daemon/daemon.go:320-333` (`createSideManager`) | Topology modes are not a config typo — they change valid code paths |
| O-5 | User-facing CRDs: `DpuOperatorConfig`, `ServiceFunctionChain`, `DpuConfig`, `DpuNetwork` | `api/v1alpha1/` types | Translation boundary; no new OPI CRDs in v1 (NFR2) |
| O-6 | VSP deployed as DaemonSet per vendor; `dpu-daemon` discovers and calls it | `config/manager/` manifests, daemon init flow | Hybrid pattern must package as one NVIDIA VSP artifact (Pattern E) |

---

## NVIDIA DPF (DOCA Platform Framework)

| # | Finding | Source pointer | Design impact |
|---|---|---|---|
| D-1 | DPF is dual-cluster: host CP provisions DPUs; DPU CP runs service/chain controllers | `docs/public/developer-guides/architecture/component-description.md` | Kamaji topology adapter required (§6.3, G3) |
| D-2 | **DMS** is invoked by DPU controller for BFB flash/reboot — provisioning scope only | Same doc, Provisioning Components + sequence diagrams | BP1: VSP must not bypass DPF to call DMS for SFC (§5.2, ADR-009) |
| D-3 | OVS ports/flows created by **ServiceInterface/ServiceChain controllers** reconciling K8s objects on DPU cluster | `system-overview.md` steps 1–4 | Actuation is CR-driven; no third-party local SFC API (§5.2) |
| D-4 | `DPUService` is Helm-chart-based; image lives inside chart values | `api/dpuservice/v1alpha1` types, `dpuservice.md` | Q-D closed: wrapper chart `opi-nf-wrapper` (ADR-010, `charts/`) |
| D-5 | `DPUServiceChain` lifecycle is reconcile-driven; conflicts surface on `ServiceChain` conditions | `docs/public/developer-guides/api/dpuservicechain.md` | Translator uses deterministic names + SSA; errors propagate via conditions (§9.3) |
| D-6 | Provisioning CRs: `BFB`, `DPUFlavor`, `DPUSet`, `DPU`, `DPUCluster`, `DPFOperatorConfig` | `api/provisioning/v1alpha1`, `api/operator/v1alpha1` | LCM + translation mapping (§7.1) |
| D-7 | Legacy `doca_grpc` deprecated since DOCA ≥2.9.0; not in production BFBs | [NVIDIA Developer Forums](https://forums.developer.nvidia.com/t/running-doca-grpc-server-with-nvidia-bluefield-3/352096) | Rejects direct BlueField socket integration path (ADR-009) |

---

## Cross-repo integration conclusions

1. **OPI integration surface = VSP gRPC + existing CRDs.** A controller-only bridge without VSP breaks the plugin model (Pattern D rejected, §5).
2. **DPF integration surface = host-cluster Kubernetes API (DPF CRs).** Even Mode B “imperative” `CreateNetworkFunction` resolves to CR upserts, not DOCA SDK calls (§5.2 E-BP1-6).
3. **Hardest mismatch = topology (G3).** Resolved with `DPFNative` / `OPIConverged` modes (§6.3), not deferred.
4. **Hardest ongoing risk = bidirectional version drift (Q-E).** **Closed for v1** via ADR-012: `config/nvidia/compatibility.yaml` + `VersionIncompatible` gate + CI contract gate (§13.1).

---

## Verification method

- OPI call paths: direct file reads on `internal/daemon/*` at commit `846e112`.
- DPF architecture: NVIDIA public docs on `public-main` at commit `3e0f454`.
- BP1 DMS scope: [NVIDIA DMS Guide](https://docs.nvidia.com/doca/sdk/DOCA-Management-Service-Guide/index.html) + DPF component description cross-check.
- Reproducibility: `./scripts/verify.sh` → `validation_output.txt`.
