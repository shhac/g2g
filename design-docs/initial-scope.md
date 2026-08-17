# Initial scope

**Status:** implemented through v0.7; later changes remain focused extensions.
The graph/track/untrack/restack commands are a separate project with its own
document, [`g2g-owned-graphs.md`](g2g-owned-graphs.md), and which source
describes a stack is settled in
[`source-resolution.md`](source-resolution.md) — where `sync` also takes its
current meaning, and the separate reconcile command it describes below was
folded into `link`. Everything else here concerns the Graphite-backed
behaviour, which is unchanged.
**Graphite display contract:** baseline-tested with Graphite CLI 1.8.6; see
[`graphite-cli-contract.md`](graphite-cli-contract.md).

## Problem

Teams using Graphite keep a linear branch stack there, while GitHub's native
stack feature needs that same ordering linked explicitly. `g2g` will bridge
those systems without becoming a second stack manager.

## Initial scope

The first workflow is `g2g link`. It discovers the declared-trunk-to-selected
branch Graphite path, preserves its bottom-to-top order, and calls `gh stack
link` only with explicit `--apply`. A bare `link` is a read-only preview. It
defaults to the checked-out Git branch and prominently prints that selection;
`--branch <branch>` optionally selects another local Graphite branch without a
checkout. A selected leaf may be in a forked tree: only its ancestry path is
linked, never its siblings or descendants.

All commands resolve the full linear stack by default. They treat the
selected/current branch as a pivot, then extend only through one unique
Graphite-declared child chain to its tip. An ancestor fork outside that lineage
is harmless; a fork in the downward extension fails closed rather than choosing
a child. `--no-stack` is an explicit opt-out that stops safely at the selected
branch and uses only its trunk-to-selected path. This preserves no-checkout
selection and never joins siblings.

Graphite may configure multiple trunks. `link` derives trunk candidates only
from the selected Graphite ancestry, never from branch-name heuristics. One
candidate is inferred and shown in preview. Multiple candidates fail closed
until `--trunk <branch>` selects a declared, local, ancestral trunk; a valid
override is also permitted when resolution was already unambiguous. Separate
Graphite trunk components are never joined into an invented edge.

## Non-goals

`g2g` will not create, reorder, rebase, submit, merge, or otherwise manage
Graphite stacks. It will not replace Graphite as the source of truth, infer
repository policy, or require a hosted service. It does not link an entire
forked/tree stack: v0.1 selects one declared trunk-to-leaf path. Tree-wide
linking remains out of scope.

`push` is intentionally not a Graphite replacement: it does not invoke
Graphite submit/restack or GitHub. It only publishes already selected local
refs as one Git operation, leaving all Graphite tracking and branch-management
decisions to Graphite.

## Safety

Before mutation, the command validates Graphite version syntax/major and display grammar,
the local branch set, and existing PR states/base relationships; it shows the resolved
bottom-to-top order and fails before calling `gh` when discovery is incomplete,
ambiguous, unsupported, or stale. `--apply` requires a clean worktree and
repeats discovery immediately before the sole mutation. After that final
revalidation it renders and flushes one neutral `Ready to apply` graph and
copyable command before invoking `gh`; success output is emitted only after
`gh` succeeds. Per-operation timeouts bound CLI calls. No branch is checked
out. Graphite is read only: production code must not use Graphite's internal
metadata/configuration or `--debug`.

The root `--debug` flag is an opt-in local diagnostic mode, not a behavioral
mode. It preserves normal stdout and writes stable, bounded events only to
stderr: operation/target selection, supported Graphite discovery and
path/trunk facts, batched GitHub PR facts for `link`/`sync`, or the selected
remote and computed atomic leased Git argv for `push`, native stack
number/position facts returned with each selected PR, plan/revalidation decisions, and
subprocess status.
It does not change timeouts, checkout behavior, Graphite modes, external argv,
or mutation decisions. Diagnostic output never includes environment values,
authentication headers, tokens, cookies, credential-bearing arguments, or
GraphQL query payloads.

`gh stack link` can push branches and create/update pull requests, so it is the
only mutation performed by `link` and `sync`. Existing matching PRs are displayed and their bases
must already match the declared Graphite path (trunk for the bottom PR, then
each preceding branch); this is the read-only native-stack relationship check
available without checkout. Every non-trunk selected-path branch must have
exactly one open PR so the preview graph is fully labeled; absent, duplicate,
non-open, or divergent mappings render as actionable unresolved nodes and block
apply. A fully mapped path with fewer than two branches above its trunk is a
successful no-op because `gh stack link` requires at least two stack arguments:
preview says `Nothing to link`, and `--apply` revalidates then reports that no
changes were needed or made without invoking `gh`. Tests use fake `gt` and `gh`
executables on `PATH`, so they never need credentials, network access, or real
CLI installations.

`push` has a separate, narrow mutation boundary. It does not require a clean
worktree because uncommitted files do not change refs. Preview validates the
selected local Graphite path and configured remote, then shows one exact
`git push --atomic --force-with-lease <remote> <branches>` invocation. Apply
re-discovers and compares the complete plan before it invokes that one Git
command. All selected refs advance together or none do; lack of atomic support
or a rejected lease is an error with no weaker fallback.

## CLI shape

`g2g link` is an explicit subcommand rather than a default action. This
leaves room for future, separately designed workflows without making a bare
invocation mutate state. Bare `g2g` shows help. The first implemented command
surface will use Cobra for parsing and native shell-completion support.

The implemented surface is:

```text
g2g [--debug] link [--branch <local-graphite-branch>] [--trunk <graphite-trunk>] [--no-stack] [--apply]
g2g [--debug] sync [--branch <local-branch>] [--remote <name>] [--prune] [--apply]
g2g [--debug] push [--branch <local-graphite-branch>] [--trunk <graphite-trunk>] [--no-stack] [--remote <name>] [--apply]
g2g [--debug] submit [--branch <local-graphite-branch>] [--trunk <graphite-trunk>] [--no-stack] [--remote <name>] [--spec <submission.json> | --write-spec <private-temp-dir>] [--template <name> | --no-template] [--draft | --ready] [--apply]
g2g [--debug] status [--branch <local-graphite-branch>] [--trunk <graphite-trunk>] [--no-stack]
g2g [--debug] unlink [--stack-number <github-stack-number>] [--branch <local-graphite-branch>] [--trunk <graphite-trunk>] [--no-stack] [--apply]
g2g [--debug] graph [--branch <local-branch>] [--scope branch|path|subtree|graph]
g2g [--debug] track [--branch <local-branch>] [--parent <local-branch>] [--apply]
g2g [--debug] untrack [--branch <local-branch>] [--scope branch|subtree] [--apply]
g2g [--debug] restack [--branch <local-branch>] [--scope branch|path|subtree|graph] [--onto <ref>] [--absorb] [--apply]
g2g [--debug] restack --continue | --abort | --skip
g2g completion bash|zsh|fish
```

Every command also accepts the root `--timeout <duration>` phase ceiling and
the mutually exclusive `--json` / `--porcelain` output selectors.

`--debug` is persistent and may also appear after `link`, `sync`, `push`, or
`submit`.

Completion is static for commands and flags and dynamic for `--branch` and
`--trunk`. Candidates are deterministic, local Graphite branches/trunks; they
do not trigger a checkout or mutation. Preview output may use color on a
terminal, but remains plain and legible for redirected output, `NO_COLOR`, or
`TERM=dumb`.

The exact command line is bare for reliable copy/drag selection: in plain
output it follows a `Command to run` heading, while color-capable output uses a
subtle background and bold text without adding prompt or border characters to
the command itself. A future interactive confirmation/cancellation cooldown is
explicitly deferred pending its own safety design; the current `--apply` has no
delay or hidden confirmation step.

## Future direction: `sync`

`sync` is one-way reconciliation: it discovers the Graphite structure and
GitHub's native-stack relationship encoded by open PR bases, compares branches
tracked by both systems, identifies divergence, and makes GitHub match
Graphite only through explicit `--apply`. Graphite remains authoritative.

Discovery is read-only and produces a dry-run report. `g2g sync` shares the
optional no-checkout `--branch` selector with `link`; absent it prominently
reports the current Git branch target. It classifies each selected-path branch
as aligned, divergent, missing (no PR), or unsafe (non-open PR). Apply requires
a clean worktree, revalidates the exact preview, and invokes `gh stack link`
only if every selected-path branch has exactly one open PR. That allows the
supported GitHub command to reconcile eligible native-stack relationships while
avoiding unsafe automatic repair.

It does not mutate Graphite, infer a relationship for a branch known to only
one system, create PR mappings for Graphite-only branches, repair a closed or
non-open PR, or turn local working-tree state into a repair decision.

Open questions include which GitHub native-stack repair operations beyond
`gh stack link` are available in supported `gh` versions, how branch and remote
identities map between the tools, and how membership divergence should be
represented. Initial `sync` scope is a selected linear path only. Tree-wide
forked-stack support needs a separate design with an explicit safety model.

## Preview and GitHub-read behavior

`link` renders a synthetic unresolved node (for example, `feature-b
(unresolved: no open pull request)`) when a selected path lacks an unambiguous
open PR mapping. It gives an actionable reason and blocks `--apply`; it never
turns unresolved state into an inferred repair.

One plan performs two bounded read-only GitHub CLI calls: `gh repo view` obtains
the repository identity, then one aliased `gh api graphql` request fetches PR
head/base/state/number/URL only for every selected branch. `--apply` repeats the
same reads during revalidation before the sole mutation. The batch boundary
remains `internal/githubstack.Inspect`, with PATH-backed fixtures and no secret
handling in application code.

Each alias is a `pullRequests(headRefName:)` connection on the repository, not
a `search()` query. The filter is applied server-side, so neither the age of a
stack nor the repository's pull-request volume changes what is returned, and
the result does not depend on GitHub's search index — which lags behind newly
created pull requests, matches head refs loosely, and draws on a much tighter
rate limit. A head that does not match its alias is skipped rather than
failing the command.

A branch's identity is its one open pull request. Long-lived stacks accumulate
closed and merged pull requests for reused branch names; that history never
blocks, so a branch whose earlier pull request was closed resolves cleanly and
`submit` creates a replacement instead of skipping it. Only two or more
simultaneously open pull requests for one head are ambiguous, and that remains
a fail-closed refusal.

`sync` uses the same restrained graph-first presentation as `link`: one
blank-bounded trunk-to-target graph labels PR-backed branches and their
aligned/divergent/missing/unsafe status, followed by the exact copyable command
when applicable. A fully mapped one-PR path is a successful no-op because the
GitHub command requires two stack branches. Preview states that no changes were made. Apply revalidates,
renders and flushes `Ready to apply` before invoking `gh`, then reports either
success or one bounded failure diagnostic; it never claims success early.

## Release roadmap and required quality gates

The following sequence is mandatory. A release must not skip its final
structure review or its release smoke checks, even when the feature work is
small.

### v0.1.0: linear linking

1. Implement actual `g2g link` with Cobra, a read-only preview by default,
   and an explicit apply opt-in for mutation. `--branch <branch>` is optional:
   absent it resolves the current branch and prints that target prominently;
   present it selects a Graphite-tracked branch without checking it out. The
   command must discover and validate one deterministic bottom-to-top Graphite
   path, inspect the corresponding GitHub PR/native-stack state as supported
   without checkout, and keep Graphite authoritative. A selected leaf under a
   fork is supported only by selecting its own trunk-to-leaf path.
2. Add static and dynamic Cobra shell completion. `g2g completion
   bash|zsh|fish` must emit release-compatible scripts. Completion for the
   optional target-branch flag must list deterministically discoverable,
   Graphite-tracked local branches without checkout or mutation.
3. Add the required safety behavior, faked external-CLI tests, and release
   readiness checks. In particular, preview must not mutate; apply must
   revalidate before invoking GitHub; unsupported, ambiguous, or divergent
   state must fail closed.
4. Immediately before creating the v0.1.0 tag, run a fresh
   `improve-code-structure` assessment requesting around sixteen concrete
   recommendations. Independently judge every recommendation and implement
   only those that are valid and worthwhile; do not treat the count as a quota.
5. Run release smoke checks after the selected improvements, then cut v0.1.0.

### v0.2.0: Graphite-authoritative reconciliation

1. Implement `sync` as the design described above: one-way reconciliation from
   Graphite to GitHub, with read-only discovery/dry-run first and explicit apply
   for any GitHub change. This milestone is now in progress after v0.1.0.
2. Immediately before creating the v0.2.0 tag, run the same fresh
   `improve-code-structure` assessment requesting around sixteen concrete
   recommendations. Independently judge and implement only the valid,
   worthwhile recommendations.
3. Run release smoke checks after the selected improvements, then cut v0.2.0.
