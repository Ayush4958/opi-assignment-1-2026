# LLM Transcript → Design Index

Maps `llm_transcript.json` (~256 turns, ~1280 lines) to `architecture_design.md` sections.

Each turn includes a **`section_ref`** field (e.g. `"§5.2"`, `"§6.3"`, `"REVIEWER.md"`) for machine navigation — regenerate via `python3 scripts/annotate_transcript_sections.py` after transcript edits.

## Decision flow

| Theme | User focus | Design output | Doc location |
|---|---|---|---|
| Scope confirmation | Assignment deliverables | Architecture + skeleton + transcript | REVIEWER.md |
| VSP definition | Vendor seam model | gRPC DaemonSet | §2.1, §6 |
| Call chain explain | kubelet → VSP path | End-to-end pod create | §6.2, §8 |
| I6 mixed vendor | LCM scope on non-NVIDIA nodes | PCI scheduling, trimmed DPF profile | §6, E14 |
| Grounding | OPI vs DPF facts | 6 mismatches + I1–I8 | §3 |
| Dual writer explain | Host VF scenario | W1 assignment | §9.1, §10 E7 |
| Level-triggered explain | I4 rationale | SSA + reconcile | §9.2 |
| Kamaji explain | Tenant CP objects | DPUCluster | §6.3 |
| UNAVAILABLE vs DEADLINE | CNI retry semantics | ADR-004 | §9.4 |
| Pattern analysis | A–D + hybrid | Pattern E 67/70 | §5, §5.1 |
| Pattern C explain | OLM operand terms | LCM in bundle | §6.2 |
| Pattern E vs B reuse | Rubric question | E closes I5/I8 | §5.1 |
| Hybrid E roles explain | LCM / translator / gRPC | Control vs data plane | §6 |
| Topology T1–T4 | Dual-writer rules | DPFNative default | §6.3, §9.1 |
| Mode A attach explain | Without host CreateNF | SFC translator path | §8.3, ADR-008 |
| Mode B VAP question | Uplink isolation | Bridge prefix contract | §6.3, ADR-002 |
| SSA W2 explain | Field manager conflicts | opi-nvidia-vsp | §9.1 |
| Helm / Argo explain | Why not bare Deployment | DPF delivery model | §7.5 |
| Naming collision question | SFC rename | Finalizer + label GC | §7.3 |
| CreateNF caller trace | dpusidemanager.go | ADR-008 | §7.6, repo_analysis |
| Upstream evolution question | Host-side caller future | E22 + matrix bump | §10 E22 |
| bridge_id grammar explain | kind:id examples | ADR-005 | §7.4 |
| Red team E1–E22 | Failure scenarios | Mitigations | §10 |
| Finalizer order explain | Teardown sequence | §8.5 | §8.5 |
| Scale question | Set-level writes | E19 | §10 E19 |
| Kamaji CP / dataplane | Flow survival | §8.6–8.7 | §8.7 |
| Adoption explain | Admin workflow | ADR-003 | §6.2, E11 |
| Heartbeat explain | Ping vs K8s probes | E17 | §6.2, §10 |
| Version matrix explain | Two-dimensional SSOT | ADR-012 | §13.1, compatibility.yaml |
| Baked-in bundle question | vs pull-at-reconcile | ADR-011 | dpf-bundle.yaml |
| BP1 explain | Evidence summary | ADR-009 | §5.2 |
| repo_analysis O-2/O-3 | Joint attach proof | Mode A declarative | repo_analysis.md |
| Skeleton + tests | stdlib fake rationale | 17 tests + golden | §17, validation_output.txt |
| UNAVAILABLE test explain | Goroutine Ready simulate | ADR-004 proof | feature_skeleton_test.go |
| Hardening | Modes, proto, CI | 16 ADRs, 12/12 matrix | §18 |
| Integration lane | envtest vs unit tests | integration_test.go, CRDs | §14 |
| Kind e2e | Proof without hardware | e2e_kind_test.go, e2e-kind.sh | §13.1 |
| Simulator contract | UI vs golden alignment | sfc-web-chain-contract.json | testdata/simulator |
| Bundle digests | REPLACE placeholders | dpf-bundle.yaml pins | ADR-011 |
| Transcript tone | Questioning process evidence | am I right / not sure | llm_transcript.json |
| v1.8 seal | Scorecard, demo, DPUDeployment | REVIEWER.md, ADR-016 | scripts/demo.sh |
| v1.7 lanes | Integration + Kind + BF-3 | verify-all, CI workflow | validation_output.txt |
| Submission | Bundle list (no .generated) | Deliverables | REVIEWER.md |
