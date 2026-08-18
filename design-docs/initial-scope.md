# Original linking scope

**Status:** historical design record. The current product orientation is in the
[README](../README.md); the current source and scope contracts are in
[source resolution](source-resolution.md) and [stack scope](stack-scope.md).

## What this document records

g2g began with one narrow use case: a team maintained a linear stack in
Graphite and wanted to project that order into GitHub native stacks. `g2g link`
read the selected Graphite path, inspected its pull requests, and, only with
`--apply`, delegated the native-stack change to `gh stack link`.

That use case remains supported. It is no longer the definition of the product:
g2g now owns a local branch forest, can work entirely from Git, and treats
Graphite as an optional source and alignment target. GitHub is a projection and
publishing integration for selected linear paths. The current local model lives
in [g2g-owned graphs](g2g-owned-graphs.md), and Graphite alignment lives in
[source alignment](source-alignment.md).

## Enduring contracts from the first workflow

The original workflow established guarantees that remain product-wide:

- Mutating commands are previews by default and require explicit `--apply`.
- Apply re-discovers and revalidates the plan immediately before mutation.
- The final plan is rendered and flushed before a mutation starts; success is
  reported only after it completes.
- Ambiguous branch, trunk, or pull-request state fails closed instead of
  choosing a likely answer.
- Commands avoid checkout changes unless conflict resolution specifically needs
  the user's working tree.
- Graphite is read through its supported, compatibility-gated CLI output. g2g
  does not inspect Graphite metadata or enable Graphite debug output, and it
  never invokes Graphite merely to discover whether a repository uses it.
- External calls have separate discovery/revalidation and mutation time bounds;
  diagnostic output is bounded, redacted, and opt-in.

`link` itself still projects only an ordered path. A fork is valid g2g
structure, but it must be narrowed to a linear path before GitHub native-stack
operations can act on it. A path with fewer than two pull-request-backed
branches is a successful no-op because `gh stack link` cannot accept it.

## Current entry points

For new local structure, begin with `g2g track --stack --trunk <root> --apply`
when g2g cannot infer the root, then inspect it with `g2g graph`. Use
`restack`, `sync`, and `prune` to maintain the recorded forest. Use `push`,
`submit`, `link`, `retarget`, and `unlink` when projecting or publishing an
eligible linear path to GitHub. Use `import`, `mirror`, or `--from graphite`
only when Graphite is part of the repository's existing workflow.

The Graphite display grammar remains a compatibility boundary; see
[graphite CLI contract](graphite-cli-contract.md). This document intentionally
does not try to describe the evolving command surface or release history.
