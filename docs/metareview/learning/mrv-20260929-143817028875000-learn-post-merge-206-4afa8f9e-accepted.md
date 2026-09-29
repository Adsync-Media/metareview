# metareview Accepted Learning

Run ID: `mrv-20260929-143817028875000-learn-post-merge-206-4afa8f9e`

Post-merge PR: `206`

## Source Status

- Git base: `48d0db59d591e61b5715ee271493597096d88f7b`
- Git head: `4dbcbdee40b540ee78fda15704d11080f91b7210`
- GitHub: available
- Session history: available

## Git Diff Summary

- `AGENTS.md`
- `CLAUDE.md`
- `USAGE.md`
- `cmd/metareview/main.go`
- `cmd/metareview/main_test.go`
- `docs/ARCHITECTURE.md`
- `internal/findings/findings.go`
- `internal/findings/override.go`
- `internal/findings/override_test.go`
- `internal/status/fsmruns.go`
- `internal/status/runclose_test.go`
- `internal/status/status.go`


## GitHub Context

- PR: https://github.com/dsifry/metareview/pull/206
- Title: override: close an abandoned FSM run through request/grant (#179)
- Review decision: APPROVED
- Body excerpt: ## Summary

Fixes #179. `metareview override request|grant <run-id>` now closes an abandoned FSM run through the existing override flow. This is the ledger design the maintainer chose over a new FSM audit event.

**Behaviour**
- For any run in the store left in a non-terminal state (not a mock), on any branch, the override commands file a **closure row**: `findings.AbandonedRunRecord`, fingerprint `fsm:abandoned-run:<id>`, carrying the run's own branch and init head.
  - The row is advisory book...

Comments:
- cursor https://github.com/dsifry/metareview/pull/206#issuecomment-5892315364: <h3>Bugbot couldn't run - usage limit reached</h3>

Bugbot is counted against Cursor usage for this user or team, and this run hit a usage or spend limit.

A user or team admin can review and increase usage limits in the [Cursor dashboard](https://www.cursor.com/dashboard/spending).

(requestId: serverGenReqId_18086e3f-4f56-419b-816f-aa9fab0143cc)
- coderabbitai https://github.com/dsifry/metareview/pull/206#issuecomment-5892318438: <!-- This is an auto-generated comment: summarize by coderabbit.ai -->
<!-- review_stack_entry_start -->

<a href="https://app.coderabbit.ai/change-stack/dsifry/metareview/pull/206?cs_source=review_comment"><img src="https://storage.googleapis.com/coderabbit_public_assets/review-stack-in-coderabbit-ui-dark.svg?v=2" alt="Review in Change Stack →" width="220" height="32"></a>

Navigate logical layers of code changes, visualize relationships, and explore their blast radius.

<!-- review_stack_entry...

Reviews:
- APPROVED by coderabbitai

## Accepted Learning

No accepted learning candidates.

## Calibration Candidates

No reviewer calibration candidates.

## Trajectory Flags

No trajectory flags.
