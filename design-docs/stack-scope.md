# Stack scope

**Status:** in progress. This is the third question in the family that
[source resolution](source-resolution.md) and [source alignment](source-alignment.md)
answer. Those decide *which record describes this branch* and *how the records
stay in step*. This one decides *how much of the stack a command is talking
about*.

## Problem

Selecting a stack asked two questions through one boolean and one crowded flag,
and they disagreed with each other.

`--no-stack` meant "just this branch"; its absence meant "extend down a unique
child chain to the tip". A branch with two children has no unique chain, so the
question had no answer and the command refused — including on a trunk, which is
the worst place for it, because a trunk is exactly where several stacks meet:

```text
error: selected Graphite branch "main" has multiple descendants
(fix/…, paul/…, …); full-stack resolution requires one linear path
(rerun with --no-stack to stop at the selected branch)
```

The suggested remedy is honest and useless: it stops at `main` and shows one
node.

Three more faults sat underneath that one.

**The same scope meant different things depending on which record answered.**
Graphite selection went through `DiscoverStack(target, includeTip bool)`, and a
bool cannot carry five scopes. `ScopeBranch` mapped to `includeTip=false`, which
suppresses descendants and returns the *whole ancestry* — not the branch alone.
So `branch` and `path` both meant something other than their own documentation
when Graphite answered.

**The g2g store ignored scope entirely.** The g2g selector hardcoded
`ScopePath`, never filled `Snapshot.Scope`, and never filled the shape. Since
that record is consulted first, this was the common path: `--scope subtree`
returned the linear path, exit 0, and the footer rendered `scope  `.

**The vocabulary had no word for the thing the default did.** Graphite's default
— ancestry plus the unique descendant chain — was not `path`, `subtree`, or
`graph`. `--no-stack` was mapped onto `branch` because nothing better existed.

## The axis

How much of the tree around me, as a lattice rather than a list:

```text
              all          every trunk's stacks   (read-only commands)
               │
             trunk         my trunk and everything under it, cousins included
               │
             stack         trunk → me → my descendants
             ╱   ╲
        path       subtree
    (trunk → me)  (me → my descendants)
             ╲   ╱
            branch         just me
```

Two independent halves — toward the trunk and toward the tips — taken
separately, together, or neither. `branch ⊂ {path, subtree} ⊂ stack ⊂ trunk ⊂ all`.

The values are worth having because each answers a sentence someone says:

| value | the sentence |
|---|---|
| `branch` | "just rebase me onto my parent, I'll deal with the rest later" |
| `path` | "what does GitHub need to see below me" |
| `subtree` | "my parent's conflict can wait; get everything above me sitting cleanly" |
| `stack` | "show me where I am" |
| `trunk` | "the trunk moved, bring everything on it up to date" |
| `all` | "show me every stack in this repository" |

`stack` is new. It is what a person means by "my stack": the path down to the
trunk and everything above, excluding the cousins that merely share a trunk.

## Defaults differ by command, and that is the point

- **Read commands** (`status`, `graph`) default to **`stack`**. Reading is free,
  so show where I am — ancestors, descendants, and my position among them.
- **`restack`** defaults to **`subtree`**. Rewriting is not free.

That difference is deliberate and is the whole argument for the axis. If
`restack` defaulted to `stack`, then restacking from the middle would walk into
an ancestor's conflict every time, when the intent was to get the branches above
sitting cleanly on the ancestor that already exists. Defaulting to descendants
keeps a deferred conflict deferred.

The previous default was the exact inverse — ancestors and not descendants — and
it reliably produced a state the tool immediately called broken:

```text
$ g2g restack --apply        # from feature-b
$ g2g graph
  ● feature-c  needs restack
Parent moved under feature-c · run g2g restack.
```

A default whose ordinary outcome is "now run me again" is the wrong default.

A wider default is not a surprise here, because restack previews by default: the
replay list is rendered and has to be approved with `--apply`. Widening the
default widens what you read before agreeing to it.

## Projection is linear, and that is a capability, not a scope

A GitHub native stack is linear. `link`, `submit`, `push` and `retarget`
therefore cannot project a fork, and offer only `stack | path`, refusing a
forked `stack` and naming the remedy: select a leaf.

Selecting a leaf is the remedy because it needs no machinery. A leaf has no
descendants, so `stack` collapses to the ancestry on its own:

```text
g2g status                     g2g status --branch feature-c
  ○ main                         ○ main
  ├─● feature-a                  ● feature-a
  │ └─● feature-b  ← target      ● feature-b
  │    └─● feature-c             ● feature-c   ← target
  └─● other-stack
```

## One vocabulary, both records

A scope asked of the g2g store must mean what it means asked of Graphite. Two
things enforce it:

- The traversal lives once, in `shape.Forest`, and both records answer from it.
  `graph.Graph` exposes its edges as a forest rather than walking them itself.
  `shape` depends on nothing, which is what lets `internal/graph` keep
  depending on Git alone: defining these in `stack` gave it Graphite and
  GitHub transitively, through an import line that named neither.
- Graphite selection is resolved against the forest its display already
  describes, not through a boolean. The parser always built the whole forest;
  only a linear walk over it was ever exposed.

The test that keeps this true is a parity table: for each scope, both records
must return the same branch set for the same shape. Nothing else would have
caught the original divergence, because each record was self-consistent.

## `--no-stack` is gone

It was the binary form of this axis and it is now `--scope branch`. Keeping both
meant two flags that overlapped semantically, and the overlap was where the
meanings drifted apart. Breaking a published flag is the price of having one way
to say a thing.

## Prune leaves sync

`sync` meant fetch, advance the base, replay, and forget what has landed. The
last of those is a different operation on a different boundary: it edits the
graph rather than the branches, and it was the only part reading a hardcoded
whole-tree selection.

`prune` becomes its own preview-first command with its own `--scope`, and `sync`
becomes fetch, advance, replay. This removes a dimension from sync's flags, plan
and preview, and gives the fork-point unpin path — which no test ever executed,
because `sync`'s tests built a graph service with no `Refs` — a command of its
own to be tested through.

## Rendering a tree, and the linear path inside it

`status` renders whatever shape it selected. A chain still renders flat: a
selection where no branch has two selected children reads better as the list
every other command shows than as a staircase that gains no information.

Where a GitHub stack overlaps a tree, both are shown — the tree is the
structure, and the native stack's members are marked within it, so the linear
path is visible as part of the whole rather than instead of it.

## Deferred

**`trunk` and `all` on `restack`.** "Restack everything on this trunk" is a
reasonable thing to want and is the same as running restack from the trunk. It
waits for deliberate worktree handling: a wide rewrite is far more likely to
reach a branch checked out in another worktree, and Git refuses to check out a
branch that is already checked out elsewhere. `restack` therefore offers
`branch | subtree | stack`.

**`all` on anything that mutates.** It exists so a repository with several
trunks can be seen whole, which is a reading problem.
