# LFX OPI Hands-On Assignment 1 — Aditya Sarna

**Architecture proposal for adding NVIDIA BlueField DPU support to the OPI DPU Operator via DOCA Platform Framework (DPF) integration.**

---

## Interactive Architecture Simulator

Explore the proposed architecture visually — bootstrap flow, SFC topology modes, and component graph:

**[https://dpu-architect.lovable.app](https://dpu-architect.lovable.app)**

---

## Primary Deliverables

| File | Description |
|---|---|
| [`architecture_design.md`](architecture_design.md) | Full architecture proposal, 16 ADRs, sequence diagrams (Mermaid.js), trade-off analysis |
| [`llm_transcript.json`](llm_transcript.json) | Exact LLM prompts and responses used during design (`[{"role":...}]` format) |
| [`feature_skeleton.go`](feature_skeleton.go) | Compilable Go skeleton — VSP adapter, CRD translation, reconciler, state machine |

---

## Quick Start

```bash
chmod +x scripts/*.sh
./scripts/verify.sh          # unit + contract + integration (envtest)
./scripts/verify-all.sh      # all lanes → validation_output.txt
```

---

## Reviewer Guide

Start with [`REVIEWER.md`](REVIEWER.md) for a 3-minute audit path covering:
- Pattern selection (Pattern E Hybrid: 67/70)
- Concern Closure Matrix (12/12 closed)
- Validation lanes (unit → integration → Kind → BF-3 contract)

---

## Architecture Decision Summary

**Pattern E (Hybrid):** NVIDIA VSP gRPC adapter + managed DPF sub-operator (LCM) + CRD translation controller

> OPI owns intent · DPF owns hardware truth · VSP owns translation

Supporting evidence: [`repo_analysis.md`](repo_analysis.md) · [`REVIEWER.md`](REVIEWER.md)

---

## Validation Evidence

| Artifact | Contents |
|---|---|
| [`validation_output.txt`](validation_output.txt) | All-lanes local run (unit + integration + envtest e2e + BF-3 contract) |
| [`validation_ci_reference.md`](validation_ci_reference.md) | CI workflow description and mentor instructions |
| [`validation_ci_github_summary.md`](validation_ci_github_summary.md) | GitHub Actions run URL + all-green job summary |
| [`validation_hardware_e2e.txt`](validation_hardware_e2e.txt) | BF-3 lane contract record |
