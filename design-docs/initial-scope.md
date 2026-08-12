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
invocation mutate state. Bare `gt2gh` shows help. Standard `--help` and
`--version` behavior is kept in the small standard-library parser.

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
