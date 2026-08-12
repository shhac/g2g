# Initial scope

**Status:** v0.2 implementation in progress; v0.1.0 released, August 2026.
**Graphite display contract:** pinned to Graphite CLI 1.8.6; see
[`graphite-cli-contract.md`](graphite-cli-contract.md).

## Problem

Teams using Graphite keep a linear branch stack there, while GitHub's native
stack feature needs that same ordering linked explicitly. `gt2gh` will bridge
those systems without becoming a second stack manager.

## Initial scope

The first workflow is `gt2gh link`. It discovers the declared-trunk-to-selected
branch Graphite path, preserves its bottom-to-top order, and calls `gh stack
link` only with explicit `--apply`. A bare `link` is a read-only preview. It
defaults to the checked-out Git branch and prominently prints that selection;
`--branch <branch>` optionally selects another local Graphite branch without a
checkout. A selected leaf may be in a forked tree: only its ancestry path is
linked, never its siblings or descendants.

Graphite may configure multiple trunks. `link` derives trunk candidates only
from the selected Graphite ancestry, never from branch-name heuristics. One
candidate is inferred and shown in preview. Multiple candidates fail closed
until `--trunk <branch>` selects a declared, local, ancestral trunk; a valid
override is also permitted when resolution was already unambiguous. Separate
Graphite trunk components are never joined into an invented edge.

## Non-goals

`gt2gh` will not create, reorder, rebase, submit, merge, or otherwise manage
Graphite stacks. It will not replace Graphite as the source of truth, infer
repository policy, or require a hosted service. It does not link an entire
forked/tree stack: v0.1 selects one declared trunk-to-leaf path. Tree-wide
linking remains out of scope.

## Safety

Before mutation, the command validates Graphite version and display grammar,
the local branch set, and existing PR states/base relationships; it shows the resolved
bottom-to-top order and fails before calling `gh` when discovery is incomplete,
ambiguous, unsupported, or stale. `--apply` requires a clean worktree and
repeats discovery immediately before the sole mutation. After that final
revalidation it renders and flushes one neutral `Ready to apply` graph and
copyable command before invoking `gh`; success output is emitted only after
`gh` succeeds. Per-operation timeouts bound CLI calls. No branch is checked
out. Graphite is read only: production code must not use Graphite's internal
metadata/configuration or `--debug`.

`gh stack link` can push branches and create/update pull requests, so it is the
only intended side effect. Existing matching PRs are displayed and their bases
must already match the declared Graphite path (trunk for the bottom PR, then
each preceding branch); this is the read-only native-stack relationship check
available without checkout. Every non-trunk selected-path branch must have
exactly one open PR so the preview graph is fully labeled; absent, duplicate,
non-open, or divergent mappings render as actionable unresolved nodes and block
apply. Tests use fake `gt` and `gh` executables on `PATH`, so they never need
credentials, network access, or real CLI installations.

## CLI shape

`gt2gh link` is an explicit subcommand rather than a default action. This
leaves room for future, separately designed workflows without making a bare
invocation mutate state. Bare `gt2gh` shows help. The first implemented command
surface will use Cobra for parsing and native shell-completion support.

The implemented surface is:

```text
gt2gh link [--branch <local-graphite-branch>] [--trunk <graphite-trunk>] [--apply]
gt2gh sync [--branch <local-graphite-branch>] [--trunk <graphite-trunk>] [--apply]
gt2gh completion bash|zsh|fish
```

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

Discovery is read-only and produces a dry-run report. `gt2gh sync` shares the
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

## Release roadmap and required quality gates

The following sequence is mandatory. A release must not skip its final
structure review or its release smoke checks, even when the feature work is
small.

### v0.1.0: linear linking

1. Implement actual `gt2gh link` with Cobra, a read-only preview by default,
   and an explicit apply opt-in for mutation. `--branch <branch>` is optional:
   absent it resolves the current branch and prints that target prominently;
   present it selects a Graphite-tracked branch without checking it out. The
   command must discover and validate one deterministic bottom-to-top Graphite
   path, inspect the corresponding GitHub PR/native-stack state as supported
   without checkout, and keep Graphite authoritative. A selected leaf under a
   fork is supported only by selecting its own trunk-to-leaf path.
2. Add static and dynamic Cobra shell completion. `gt2gh completion
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
