# Structural review

This pass separates decisions from effects, removes duplicate decisions and
lookups, and gives cohesive implementation groups their own files. It preserves
command behavior, package boundaries, and exported interfaces.

The baseline is `11f87c6705ed5d6c5c3362e8bbe9142cc1ecca38`. The nineteen
refactoring commits end at `db833f8`; each was verified independently before
committing. A separately audited cleanup follows in `50d2ae5`.

## Changes

| Area | Commit | Result |
| --- | --- | --- |
| Submission | `3c70a59` | Validation prepares ordered creation requests once, before publishing; execution consumes those requests. |
| Navigation | `9dcd1d6` | Uses the existing forest for sorted children instead of maintaining a second membership representation. |
| Landing | `73385fe` | Compares queue policy fields directly; adds policy-change coverage before enrollment and while waiting. |
| Landing | `0a9db5e` | Flattens survivor PR filtering while retaining response order and deduplication by PR number. |
| Landing | `b7bef3d` | Separates preparation, one irreversible request plus diagnosis, and observation; adds uncertain-outcome and queue-entry checks. |
| Landing | `a7a817a` | Separates PR resolution from readiness refusal policy, retaining their priority. |
| Landing | `db833f8` | Gives step classification and forecasting their own planning file. |
| Landing | `50d2ae5` | Removes an independently audited, unused merge-settling wrapper. |
| Pull | `45ffdb8` | Names the pure published-parent boundary selection separately from fetching and reconciliation. |
| Pull | `233afd1` | Isolates worktree decisions and the second collection with left branches fixed. |
| Pull | `2d36947` | Reduces concurrent branch answers in selection order through a pure function. |
| Creation | `15591b1` | Gives pinned-start resolution and protection of the original detached HEAD one phase. |
| GitHub adapter | `b9948c8` | Separates required-check decoding and classification; covers cancellation/failure/pending priority. |
| GitHub adapter | `126be50` | Flattens successful mergeability reads while retaining nonfatal check-detail enrichment. |
| Git adapter | `ec08a82` | Reuses ordered positional ref validation at twenty sites, preserving the first error and optional-value rules. |
| Git adapter | `8fb1c7b` | Separates sync-point reads and identity evidence from recording, rename, and deletion. |
| Plan comparison | `603ec33` | Uses standard slice equality in adoption, mirroring, and retargeting plans. |
| CLI lifecycle | `baaff44` | Isolates mutation failure diagnosis and reporting; render/flush/execute remains visible in the mutation flow. |
| Graph adoption | `0a7119b` | Separates candidate warming and branch attachment from planning and recording. |
| Comments | `41ce000` | Separates execution and partial-success reporting from comment planning. |

The four new implementation files are
[`adopt_branches.go`](../internal/graph/adopt_branches.go),
[`execute.go`](../internal/comment/execute.go),
[`syncpoint_read.go`](../internal/git/syncpoint_read.go), and
[`plan_steps.go`](../internal/land/plan_steps.go). Their moved implementations
were compared byte for byte with their previous locations.

## Verification

Every logical change passed formatting and whitespace checks, `go build ./...`,
`go vet ./...`, the full offline suite, and the scoped race suite for link,
graph, parallel, and land. The full suite includes 536 top-level CLI tests,
compiled once and sharded into sixteen separate processes by the repository's
test runner. Local runs use a longer outer test timeout; test selection and
assertions are unchanged.

An AST comparison found all 927 exported Go function, method, type, and value
declarations unchanged from the baseline. No existing golden files changed.

New coverage exercises queue policy changes, ambiguous request outcomes with a
failed diagnostic read, cancellation-independent diagnostic budgets, queue-entry
head mismatches, disappearing queue support, and required-check precedence.
Tests use injected clients and synthetic data; no hosted GitHub queue was used.

After the audited removal, the final full-project run also passed with
`GOFLAGS='-race -buildvcs=false'`: race-enabled build and vet, every package,
and all 536 CLI tests in sixteen processes. The test suite completed in 263
seconds with no race reports. The final scoped race checks passed as well.

A fresh binary passed version and help checks for the root command, `land`,
`pull`, and `create`. The final exported-declaration comparison still matched
all 927 baseline declarations. No changes were pushed.

## Dead code cleanup report

The scan covered the 35 Phase 2 changed files and their pre/post-refactor import
neighbors, expanded to Go package units: 465 Go files across 32 packages. A
broad scanner checked all six dead-code categories; a separate auditor checked
the sole candidate for callers, interface obligations, dynamic references,
entry points, documentation references, and meaningful test support.

### Removed (1)

- `internal/land/settle.go` — unused private symbol: `Service.settleMerge`
  forwarded to `settleMergeObserved` and had no current-tree callers. Both
  direct and queued landing already call the observed variant. Its ancestry
  explanation remains above the active implementation.

### Rejected by deep audit (0)

None.

### Reverted by failing verification (0)

None.

### Uncertain (0)

None.

### Coverage follow-ups (0)

None. The retained implementation is exercised through `Service.Apply`,
including `TestQueueWaitCanConfirmAndDescend`,
`TestDirectMergeNotReportedUntilGitHubConfirms`, and
`TestObservedMergeStillReportedWhenAncestryPropagationFails`. Tests do not call
the private helper directly; those behavior checks remain intact.

## Deferred

The broader CLI refusal-view representation needs a separate output-model
migration plan because several legitimate layouts share its state. Additional
test-fixture file splits and small live wrappers were lower priorities than the
production decision paths addressed here. All nineteen selected changes were
completed.
