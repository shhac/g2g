# Initial scope

**Status:** initial skeleton, August 2026.
**External surface version:** nothing pinned yet; `gt` and `gh` are not invoked.

## Problem

Teams using Graphite keep a linear branch stack there, while GitHub's native
stack feature needs that same ordering linked explicitly. `gt2gh` will bridge
those systems without becoming a second stack manager.

## Initial scope

The first implemented workflow will be `gt2gh link`. It will discover one
linear Graphite stack, preserve its ordering, and call `gh stack link` with
branches in bottom-to-top order. The present command is intentionally a no-op
placeholder: it invokes neither CLI and changes nothing.

## Non-goals

`gt2gh` will not create, reorder, rebase, submit, merge, or otherwise manage
Graphite stacks. It will not replace Graphite as the source of truth, infer
repository policy, or require a hosted service. Forked or branched stacks are
out of scope initially; the first workflow handles only one unambiguous linear
stack and should decline unsupported shapes clearly.

## Safety

Before any future mutation, the command should validate that it found a single
linear stack, show the resolved bottom-to-top order, and fail before calling
`gh` when discovery is ambiguous or incomplete. Tests use fake `gt` and `gh`
executables on `PATH`, so they never need credentials, network access, or real
CLI installations. Graphite data should be read-only; GitHub linking is the
only intended future side effect.

## CLI shape

`gt2gh link` is an explicit subcommand rather than a default action. This
leaves room for future, separately designed workflows without making a bare
invocation mutate state. Bare `gt2gh` shows help. The first implemented command
surface will use Cobra for parsing and native shell-completion support.

## Future direction: `sync`

`sync` is not implemented and its command-line interface is intentionally not
committed yet. Its direction is one-way reconciliation: discover the Graphite
structure and GitHub's native stack relationships, compare branches tracked by
both systems, identify divergence, and make GitHub match Graphite. Graphite
remains authoritative throughout.

Discovery must be read-only and produce a dry-run report before there is any
apply path. A future explicit apply mode should show the proposed GitHub
changes, require an unambiguous linear stack, and refuse to repair missing,
ambiguous, or partially mapped branches automatically. It should not mutate
Graphite, infer a relationship for a branch known to only one system, or turn a
local working-tree state into a repair decision.

Open questions include which GitHub native-stack data and repair operations are
available in the supported `gh` versions, how branch and remote identities map
between the tools, and how parent, order, and membership divergence should be
represented. Initial `sync` scope, if adopted, is linear stacks only. Tree or
forked-stack support needs a separate design with an explicit safety model.

## Release roadmap and required quality gates

The following sequence is mandatory. A release must not skip its final
structure review or its release smoke checks, even when the feature work is
small.

### v0.1.0: linear linking

1. Implement actual `gt2gh link` with Cobra, a read-only preview by default,
   and an explicit apply opt-in for mutation. `--branch <branch>` is optional:
   absent it resolves the current branch and prints that target prominently;
   present it selects a Graphite-tracked branch without checking it out. The
   command must discover and validate one unambiguous bottom-to-top linear
   Graphite path, inspect the corresponding GitHub PR and native-stack state,
   and keep Graphite authoritative.
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
   for any GitHub change. `sync` remains design-only and post-v0.1.0 until this
   milestone begins.
2. Immediately before creating the v0.2.0 tag, run the same fresh
   `improve-code-structure` assessment requesting around sixteen concrete
   recommendations. Independently judge and implement only the valid,
   worthwhile recommendations.
3. Run release smoke checks after the selected improvements, then cut v0.2.0.
