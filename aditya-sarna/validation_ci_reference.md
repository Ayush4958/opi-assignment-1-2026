# CI validation reference (mentor / reviewer)

**Captured run:** [workflow run 28895446525](https://github.com/Aditya-Sarna/opi-assignment1-ci-proof/actions/runs/28895446525) on test repo [Aditya-Sarna/opi-assignment1-ci-proof](https://github.com/Aditya-Sarna/opi-assignment1-ci-proof) — see `validation_ci_github.log` and `validation_ci_github_summary.md`. **5 jobs green** (includes `grpc-vsp`).

When local Docker is unavailable, `./scripts/verify-all.sh` records **green integration + e2e (envtest fallback) + BF-3 contract** in `validation_output.txt`. Full **Kind cluster** proof runs on GitHub Actions Linux runners.

## Workflow

File: `.github/workflows/verify.yml` (relative to this `aditya-sarna/` directory — all submission files live here)

For GitHub Actions on a standalone test repo, check out this folder as the repo root (see [opi-assignment1-ci-proof run 28895446525](https://github.com/Aditya-Sarna/opi-assignment1-ci-proof/actions/runs/28895446525)).

| Job | Command | Proves |
|---|---|---|
| `unit-contract` | `./scripts/verify.sh` (unit slice) | 22 tests + digest gate |
| `integration` | `fetch-envtest.sh` + `go test -tags integration` | SSA, drift, teardown on apiserver |
| `kind-e2e` | `./scripts/e2e-kind.sh` | Real Kind cluster + CRD install |
| `bf3-lane-contract` | `./scripts/e2e-bf3-hardware.sh` | Phase 6 hardware lane spec gate |
| `grpc-vsp` | `./scripts/demo-grpc.sh` | OPI Vendor gRPC daemon contract (bufconn + TCP smoke) |

## Trigger (after pushing to GitHub)

```bash
# Push branch, then in repo UI: Actions → verify → Run workflow
# Or with gh CLI:
gh workflow run verify.yml
gh run list --workflow verify.yml --limit 1
gh run view --log
```

## Local full stack (preferred when Docker Desktop is running)

```bash
chmod +x scripts/*.sh
./scripts/verify-all.sh          # writes validation_output.txt
./scripts/e2e-bf3-hardware.sh    # writes validation_hardware_e2e.txt
```

With Docker stopped, e2e uses **envtest fallback** (`USE_ENVTEST_E2E=1`) — same golden-object assertions, labeled honestly in the log.

## BF-3 hardware lab (Phase 6)

Contract gate (Assignment 1 bundle): `TestBF3LaneSpec_Complete` + `testdata/hardware/bf3-lane.yaml`

Full lab (requires BlueField-3 + DOCA): `BF3_LAB=1 KUBECONFIG=<lab> ./scripts/e2e-bf3-hardware.sh`
