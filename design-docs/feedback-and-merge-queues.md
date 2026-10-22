# Feedback fixes and merge queue support

Status: reviewed by Claude Opus 5.5, validated by the coordinator, implemented and verified locally.

## Outcome

Address the actionable feedback from October 2026 and make GitHub merge queues
an explicit, supported landing path. A preview must describe work that apply
can perform, refusals must name the actual obstacle, and a queued merge must
never be reported as landed.

The feedback files are private evidence. This plan, documentation, fixtures and
tests use only synthetic examples. Work stays local: no commits, pushes,
releases, real GitHub mutations or Graphite service operations are required.
The checkout started clean on main; preserve this repository's local workflow.

## Constraints

- Keep preview, rediscovery, revalidation and render/flush before mutation.
- Keep GitHub and Graphite out of graph and offline commands. New GraphQL
  selections must pass the published-schema tests.
- Keep all history rewriting in restack; reuse push, pull, pruning, worktree
  policy, repair notes, subprocess cancellation and diagnostics.
- Keep leases pinned to observed tips and leave user remote-tracking refs alone.
- Land remains re-entrant by recomputation, with no new operation journal.
- Test offline with synthetic PATH fakes and real disposable Git repositories
  where ancestry, reflogs, rewriting or worktree state decide the answer.

## 1. Make landing aware of GitHub merge queues

Read the eventual trunk's queue with repository.mergeQueue(branch: $trunk) in
the batched readiness query, since upper PRs currently target feature branches.
Read each PR's queue entry, entry head and auto-merge request as volatile
readiness, outside generic PR observations. Confirm queue policy again after a
retarget. Distinguish merge requested, queued, waiting for requirements,
confirmed merged and removed/refused.

For a queue-required base, read repository autoMergeAllowed in the same
readiness query. Request enablePullRequestAutoMerge when allowed, or
enqueuePullRequest directly when auto-merge is disabled, through the existing
mutation seam with pullRequestId and expectedHeadOid. GitHub CLI issue #13398
confirms that ruleset queues can have auto-merge disabled; its auto-merge-only
path fails there. Both inputs support head pinning in the published schema.
Unmet prerequisites in direct-enqueue mode refuse accurately rather than
promising deferred auto-merge. The queue owns the merge method. Skip the direct-merge method gate and refuse explicit
--method, a conflicting declared --by, and --admin with clear advice to use the
queue. Never derive queue bypass from BLOCKED. Observe after an ambiguous
request error before deciding whether another request is necessary; never fall
back to direct merge. Add the mutation to schema validation.

Default land enqueues the next eligible bottom PR, confirms whether it already
merged, and otherwise stops with an explicit pending result and a scoped rerun
command. A pending PR is never added to the landed list. With --wait, wait for
confirmed merge of an accepted request under the landing budget and continue
the descent; initial approvals/conflicts/failing prerequisites still refuse. Queue
removal, closure, a changed head/base, failed checks, cancellation or timeout
must leave an accurate report and recoverable state. Do not repeatedly enqueue
an existing queue entry or mutate a queued PR's head/base. Observe existing
requests before publishing or retargeting. Refuse an existing request with a
different local head/base, and protect queued/auto-merge descendants that a
landing replay could move, including survivors above the selected target.

Once GitHub confirms MERGED and the merge commit is in the fetched trunk,
advance/replay/cleanup with the existing services. A rerun after an external
merge uses those same paths. Never replay survivors or delete refs because
enqueue returned success. Preserve direct-merge behavior for non-queue repos.

Every pending end exits 3, including observation of an existing request, with
a typed pending outcome separate from landed and changed. Confirmed MERGED is
reported as merged even if ancestry propagation subsequently fails. Skip
republishing and comments on a pending stop. Queue preview recipes stop at the
request/observation by default; --wait may describe cleanup after confirmation.
Machine previews expose direct/queue and volatile pending state additively.
Rerun advice preserves the selected branch, scope, remote and relevant flags.

Coverage: enqueue, existing entry, auto-merge waiting, quick confirmed merge,
two-branch squash descent across reruns, auto-merge-disabled direct enqueue,
unmet direct-enqueue prerequisites, --wait, timeout, queue removal, changed
head/base, explicit bypass refusal, CLI argv and GraphQL schema. A real-Git journey
must actually advance the remote when the fake GitHub confirms a merge.

## 2. Diagnose readiness and worktree blockers accurately

Read required check contexts for blocked/refused steps, including isRequired,
name, status/conclusion, inspection URL and pagination completeness. Distinguish
pending, failed/cancelled and unknown protection blocks; optional failures must
not be called required blockers. Name failed checks and provide an inspection
route;
suggest rerunning a cancelled run only when its identity is established. Do
not assume every cancelled run is a harmless duplicate or default to bypass.

When rediscovery becomes blocked, report that specific blocker before generic
plan inequality. Rerun the guard before revalidation. Restack-in-progress advice
must identify its owning checkout
or branch where the journal establishes it; map its private Git directory to a
checkout when possible, otherwise label it accurately. Direct recovery to that
owner and
do not invite an unrelated session to abort it.

Coverage: completed failure with no pending checks, cancelled run, pending
checks, review refusal, other protection, and a second worktree acquiring a
restack journal between discovery and revalidation.

## 3. Give planning and revalidation appropriate time budgets

The current apply path spends one 45-second deadline on both planning passes;
preview performs only the first. Give discovery and revalidation independent
budgets while preserving an independently bounded mutation. Keep --timeout as
the highest-precedence explicit override. Add G2G_TIMEOUT as a persistent
minimum budget: max(environment duration, phase default), validated centrally
before execution as a positive duration. It must never shorten a long stack
landing. An explicit --timeout wins and retains exact-override semantics.
Default budgets remain bounded; do not exclude network operations from all
deadlines or trust a previous invocation's plan without revalidation.

Coverage: a first pass consuming most of its budget does not starve the second;
both phases still cancel; flag beats environment; invalid values fail before
mutation. Update help and timeout documentation.

## 4. Require submit metadata only for PRs being created

Generate specs for missing/replacement PRs only. Accept a spec omitting existing
PRs; continue accepting complete older specs without updating existing PR
titles, bodies or reviewers. Reject duplicates, unknown branches and missing
creation entries, while resolving entries by branch rather than incidental
position. Revalidation must reject changed creation requirements before push.

Without metadata for a missing PR, preview must say apply requires a spec and
show the exact next command. Existing-PR-only submission remains spec-free.
Do not add overlapping one-branch title/body flags in this change.

Coverage: mixed existing/missing PRs, generated minimal spec, old complete spec,
preserved metadata, duplicates/unknowns, missing title and PR lifecycle races.
Use one branch-keyed validator for CLI and service apply. Derive PR bases from
snapshot order, never spec order; old existing-PR entries may have blank titles.
Generated --edit specs are minimal too.

## 5. Align pull and push on emptied branches

Preserve the conservative refusal to automatically publish an emptied/recreated
branch. Use the same sync-point evidence to keep pull from promising push will
publish a replacement it will refuse. Name the replacement and the existing
pinned-lease repair when that is the intended action. Partial intentional drops
and conflict-resolved replay replacements must still publish normally.

Coverage: all own commits removed, entirely new replacement series, partial
drop, replay plus drop, stale tracking-ref reset and a colleague's unseen commit.
Use real Git/reflogs for the distinguishing evidence, not just decision fakes.

## 6. Support explicit creation at a newer revision

Add create --at <revision> while keeping --parent as recorded structure.
Resolve revision^{commit}, reject option-like values and pin the start, parent
and original detached HEAD separately. Initially support the parent's own tip,
or a newer evidenced trunk revision contained in its known local upstream;
refuse arbitrary feature-parent descendants and unknown upstreams. Require
parent ancestry. This solves stale local trunks without classifying unrelated
trunk commits as the new branch's own work. A detached HEAD must be contained
in the start revision, so switching abandons no detached work.

Create at the pinned start OID, let PlanTrack remain authoritative for the
fork and verify it agrees with the start; recheck the parent tip and original
HEAD before mutation. Rollback returns to the original detached HEAD, not the
new start. Without --at retain existing behavior and improve stale-parent
advice. Do not move the trunk held elsewhere.

Coverage: detached at a newer local upstream ref, stale trunk in another
worktree, equal start, annotated tag, invalid/unrelated/option-like revision,
unknown upstream, feature-parent descendant, changed ref, dirty files and
rollback to the original detached HEAD.

## 7. Follow a published parent without rebasing it onto trunk

Add pull --above <branch>, an explicit replay boundary independent of
--take published. The named recorded feature branch must be the target or an
ancestor within the selected stack and have a published tip. Preserve the
original target's selected stack: reconcile the boundary and selected branches
above it, leave trunk/ancestors and unselected siblings alone. Naming the
boundary itself as --branch deliberately includes its whole subtree. Mutually
exclusive with --trunk-only, --scope and --prune in this first interface.

Reconcile the named boundary through normal collection, sync-point, drop,
--keep and divergence rules, keeping its published base rather than replaying
it onto trunk. Replay only the selected descendants through the existing
restacker and Pending.Tips, with a durable boundary/fixed input. If worktree
policy leaves the boundary behind, measure children against its actual local
tip and record no remote agreement for the untouched boundary. Fetch the
boundary's parent only when needed as comparison evidence, never advance it.

If collecting the published boundary removes its recorded fork from history,
plan a verified replacement fork before mutation. Reuse existing trunk tracking
evidence and record/pin the boundary's new fork after collection, before child
replay. Abort reverses the child replay, not the already-collected parent or its
corrected fork. Refuse ambiguous feature-parent reforks before moving refs.

Persist the replay boundary/fixed set in the existing restack journal. Boundary
records need schema version 2 so an older binary refuses rather than ignoring
the constraint; accept existing version-1 records. Ordinary records can remain
version 1. Continue, skip and abort must preserve the selected branch set,
protected parent and original structure. This extends the one existing
restack journal; it does not add a pull journal or store graph ownership marks.

Coverage: published parent based on an older trunk, new remote parent commit,
local-only child, several selected descendants, divergence refusal/take,
inherited drops, worktree follow/skip/dirty, missing published parent, --through
outside the selection, flag exclusions, and unselected sibling isolation.
Real-Git conflict continue/abort cases assert the protected parent's exact tip,
trunk/sibling refs, clean worktrees and each child's own replay range. A later
ordinary pull still means advancing/replaying the full stack; --above is an
invocation choice, not persistent branch ownership.

## Work sequence and ownership

1. Write this plan; obtain a read-only Claude Opus 5.5 medium-effort audit.
   Ask for proposed replacement text and decisions. Incorporate justified edits
   after checking their code evidence; retain a decision record below.
2. Parallel implementation after review, with three workers and disjoint files:
   A: GitHub adapter, landing and landing-specific CLI/tests (sections 1 and
   check readiness in 2).
   B: submission and shared CLI lifecycle/timeouts, idle-journal advice (sections
   3/4 and worktree blocker in 2).
   C: sync/push/create services, restack boundary/journal/resume, and their command
   files/tests (sections 5/6/7). B owns only restack idle/recovery advice;
   coordinate any restack service edits with C. B owns shared cli.go wiring.
   Shared README, skill and design changes are coordinated by the parent.
3. Parent reviews interfaces, integrates shared wiring/docs, and assigns an
   independent cross-workstream review and integration verification.
4. Run gofmt on changed Go files, go test ./..., go vet ./..., and the required
   go test -race ./internal/link ./internal/cli. Validate any edited skill.
   Resolve failures and review findings before delivery.

## Exclusions

No hook-installation warning: a hook file does not prove intended checks ran.
No cached-plan apply, automatic cancelled-run reruns, new land journal, new
Graphite enrollment, release or remote publication.

## Decision record

- Initial coordinator recommendation: conservative emptied-branch refusal;
  independent planning budgets; minimal submission specs; explicit create and
  published-parent controls; queue handoff by default with optional waiting.
- Claude Opus 5.5, medium effort, session 1156afc2-575b-408b-a72a-b0e21682a42d:
  audited read-only and supplied proposed plan edits. Confirmed queue accounting,
  future-base detection, shorter-spec base ordering, journal recovery, pinned
  creation and environment timeout risks.
- Coordinator accepted explicit queue mutation after verifying it against the
  published schema and GitHub CLI source; queue flags refuse bypass/method
  overrides. Accepted environment budgets as a floor, minimal spec validation,
  conservative emptied-branch rules and a versioned durable replay boundary.
- Coordinator narrowed --at to evidenced trunk advancement, avoiding a broader
  arbitrary-revision fork-planning API. Kept --above within the original target's
  selection rather than Claude's proposed all-descendants widening, because
  unrelated siblings must remain untouched. Three native read-only investigations
  independently checked queue, workflow and collaboration boundaries.
- Independent implementation review found a further compatibility gap in the
  audited plan: ruleset queues can have autoMergeAllowed=false. Coordinator
  verified [GitHub CLI #13398](https://github.com/cli/cli/issues/13398) and the
  published head-pinned enqueuePullRequest input. Added explicit transport
  selection and disabled-auto-merge tests; never fall back to direct merge.
- Final independent real-Git audit found a published-parent refork gap: a
  parent rebuilt on an older trunk could lose its recorded fork while retaining
  its own patches. Added verified boundary refork planning/recording before
  descendant replay and a real regression; ambiguous feature-parent cases
  refuse rather than record a guessed boundary.
- A separate, pre-existing offline status limitation remains: when divergent
  local remote records disagree, KnownTips intentionally prefers the tracking
  ref, even over a freshly fetched isolated tip. A synthetic reproduction is
  recorded in ignored FEEDBACK/2026-10-22-feedback-queue-build.md. This work
  preserves that documented preference and all pinned push leases.
- Final submit audit added an apply-only guard before its initial metadata
  discovery, so a detached active restack cannot hide its owner behind a generic
  discovery error. Preview reads remain available.
- Verification passed: go test ./... -timeout 45m; go test -race
  ./internal/link ./internal/cli -timeout 45m; final-source go vet ./...;
  gofmt and git diff --check; skill validation; and a rebuilt binary's version
  and command-help smoke checks. The broad runs preceded the final boundary
  refork, submit guard and output/help corrections; focused follow-up tests
  cover those changes, including race tests for pull --above. Full-suite and
  race logs are /tmp/g2g-feedback-full-tests.log and
  /tmp/g2g-feedback-race-tests.log; the local build is /tmp/g2g-feedback-build.
  Queue API contracts are schema-validated and exercised offline; no hosted
  queue was mutated. When auto-merge is disabled, initial prerequisites must
  already pass: --wait observes an accepted request, not prerequisite completion.

## Post-implementation repository audit

The initial implementation was committed on main in four commits, ending at
0a0204e. At the user's request, Claude Opus 5.5 then reviewed the repository and
implemented the confirmed findings in separate passes; the coordinator reviewed
and committed each validated batch. The CLI reported the actual model as
claude-opus-5-5, with no model fallback.

- d526811 preserves collected reviewer commits when a pull's replay is aborted.
  The journal now captures post-collection heads and the graph structure at
  replay start, including fork points the collection updated. Standalone
  restack rollback and the protected --above boundary retain their contracts.
- 9bca8fa preserves branch, scope, remote and --above in partial-pull advice,
  reports the branches actually collected, quotes repair arguments, and targets
  declaration undo advice at the branch that was declared.
- 0497723 replaces landing's private short-ID formatter with localgit.Short
  and quotes its remaining repair target.
- 7bfd9c9 leases remote cleanup on the cycle's merged head or the exact tip
  assessed by push's preflight. Later remote reads never refresh this baseline.
  The coordinator caught and reproduced a flaw in an intermediate version:
  a post-merge reviewer revert can pass whole-branch absorption without having
  landed. The final implementation reuses push's per-commit assessment instead.
  Printed recipes capture each merge head, pin the merge and deletion to it,
  and refuse an unset or empty capture before invoking a tool.
- 84b6688 isolates adapter subprocesses and cloned test repositories from
  personal Git configuration. The first final full/race run exposed that
  fixture setup alone ignored the global configuration: commits and rebases
  started by the adapters still inherited signing settings. The regression
  supplies a synthetic unavailable signer, proves commits succeed in both the
  fixture and a clone, and checks environment restoration without editing the
  caller's configuration.

Real-Git regressions reproduce the abort and unleased-deletion bugs against the
old implementation. Coverage includes repeated pulls, reforked incoming history,
post-merge pushes and reverts, a push between the deletion read and write, a
changed remote between preflight and planning, direct and queued descent, and
interrupted-descent cleanup. Shell tests check argument quoting and empty-head
refusal. A final read-only Claude source review found no blockers; the coordinator
also reviewed the new tests and reproduced the failures independently.

Final verification passed:

- go test ./... -timeout 45m: all 32 package results clean; CLI 1138.598s.
- go test -race ./internal/link ./internal/cli ./internal/land
  ./internal/restack -timeout 45m: all four packages clean; CLI 1261.910s.
- go vet ./..., gofmt and git diff --check.
- Rebuilt /tmp/g2g-feedback-build and checked its version and root/create/pull/
  land help, including help with an invalid G2G_TIMEOUT.

All 501 source, configuration and fixture hashes match the manifest recorded
when the corrected full/race runs started. Logs are
/tmp/g2g-claude-followup-full-tests-isolated.log and
/tmp/g2g-claude-followup-race-tests-isolated.log. The first failed runs are
retained separately under the same names without -isolated.

The existing conservative refusal for an interrupted descent of a several-commit
squash without a sync point remains: push cannot establish the remote work's
identity in that case. This audit does not loosen that publication policy or the
offline status preference recorded above. No hosted queue was exercised, and
all Git remote writes stayed in disposable local test repositories.
