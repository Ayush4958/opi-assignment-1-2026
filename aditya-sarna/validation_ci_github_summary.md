# GitHub Actions CI proof (captured)

| Field | Value |
|---|---|
| **Repo** | https://github.com/Aditya-Sarna/opi-assignment1-ci-proof (private test repo) |
| **Workflow run** | https://github.com/Aditya-Sarna/opi-assignment1-ci-proof/actions/runs/28895446525 |
| **Run ID** | 28895446525 |
| **Commit** | de0bbd6 — Add gRPC VSP daemon demo and refresh validation artifacts for v1.9 |
| **Date (UTC)** | 2026-07-07T20:11–20:14Z |

## Job results (all green — 5 jobs)

| Job | Result | Key proof |
|---|---|---|
| `unit-contract` | ✓ | 22 unit/contract tests + digest gate |
| `integration` | ✓ | `TestIntegrationTranslateApplyAndReady`, drift SSA, finalizer teardown |
| `kind-e2e` | ✓ | **Real Kind cluster** — `TestE2EKindSFCGoldenApply` PASS, `KIND E2E PASSED` |
| `bf3-lane-contract` | ✓ | `TestBF3LaneSpec_Complete` PASS |
| `grpc-vsp` | ✓ | bufconn + TCP smoke — `GRPC DEMO PASSED` |

Full log: `validation_ci_github.log` (1560+ lines from `gh run view --log`).

## Mentor quick check

Open the workflow run URL above, or grep the log:

```bash
grep -E 'PASS: TestIntegration|PASS: TestE2EKind|KIND E2E PASSED|INTEGRATION PASSED|GRPC DEMO PASSED|TestLiveSmoke' validation_ci_github.log
```

## Previous run (pre-gRPC, 4 jobs)

Run [28885043356](https://github.com/Aditya-Sarna/opi-assignment1-ci-proof/actions/runs/28885043356) — integration + Kind green before v1.9 gRPC lane.
