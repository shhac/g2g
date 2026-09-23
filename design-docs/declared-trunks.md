# Declared trunks

**Status:** implemented.

## Problem

Two shapes people work in have no honest representation.

**two-trunks.** `main` and `staging` each carry their own stacks. The forest
already allows several roots, but a trunk only comes into being when something
is recorded on it, and `create` refuses to be the first thing: a parent that is
neither recorded nor the default branch would silently become a trunk. So the
first stack on `staging` has to be built by hand.

**landing-branch.** `main ← feature ← a1 ← a2 ← a3`. The small branches are
reviewed and squash-merged into `feature` one at a time; `feature` reaches
`main` later, whole, by a merge that keeps those commits. Recorded as an
ordinary stack, the base is `main`, and `land` from `a3` merges `feature` into
`main` first — the one thing the shape exists to avoid.

Both want the same primitive: saying out loud that a branch is a trunk.

## What a trunk is

A trunk has always been three things at once, and the second shape needs them
apart:

1. **a root** — nothing is recorded beneath it, so every walk toward the base
   stops there;
2. **never rewritten** — it is fast-forwarded from its remote and merged into,
   and nothing here replays it;
3. **where things land** — `land` aims every pull request at it and `pull`
   advances it.

`feature` needs all three toward the stacks above it. It also needs one more
fact of its own: it goes somewhere when it is finished.

## Decisions

### Where it goes is not a stack edge

`feature` lands into `main`. That is recorded beside the trunk set, as
`declared: {feature: {into: main, by: rebase}}`, and **not** as an edge in the
forest.

That one choice answers most of the design:

- **Every walk is unchanged.** `feature` has no edge, so to `status`,
  `restack`, `pull`, `push` and `land` run from above it is a root exactly as
  `main` is. Nothing has to learn to stop at it, and the parity table has
  nothing new to compare, because Graphite describes the stacks above it the
  same way.
- **It cannot be replayed.** An edge carries a fork point, and a fork point is a
  replay range. With no edge there is none, so nothing can compute one.
- **A rewrite elsewhere cannot make it look wrong.** A colleague rebasing
  `feature` onto a newer `main` would leave an edge's fork point outside the
  branch, which reads as "moved off parent" and sends `doctor` to `track`. There
  is no fork point to leave behind.

The alternative — a trunk flag on an ordinary edge, with every walk taught to
stop at a flagged branch — puts the same boundary rule in every traversal in
`internal/shape`, and makes the one rule that must never be missed (do not
replay a trunk) depend on each caller remembering it.

### Declared, not promoted

The trunk set already exists, and g2g adds to it on its own: a parent nothing
records becomes a trunk the moment something is recorded on it. A declaration
is different in kind — somebody said so — and the bulk paths must not undo it.
`adopt`, `graphite adopt` and `github adopt` would otherwise read `feature`
sitting under `main` in Graphite, in a pull request's base or in ancestry, and
record the edge: `Track` drops a branch from the trunk set when it gains a
parent, so the declaration would vanish without a word and `land` from `a3`
would merge `feature` into `main` first.

So a declaration is recorded explicitly, in its own set, even when it lands
nowhere (`declared: {staging: {}}`), and the graph itself refuses to give a
declared branch a parent: `Track` fails, so no path that records edges can end
one by forgetting to check. The bulk paths ask `Judge` what an edge would mean
and name a declared trunk as a conflict. Only `track --parent`, naming the
branch, clears it — it undeclares first, explicitly.

The same reason puts every declared trunk in g2g's hands at selection: another
source that still tracks `staging` under `main` must not get to answer for it.

### The branch something lands into is not stacked

`into` has no edge of its own: it is a trunk, or at least a branch nothing
records a parent for. `Validate` and `Track` both hold this, so it surfaces as
a refusal rather than a failed save. It does two jobs. It keeps the branch
everything is aimed at from being replayed, and it makes following `into` from
branch to branch a complete cycle check, because any cycle that mixed stack
edges with landings would need an `into` that has one.

### Never rewritten

`restack` replays only branches that have an edge (`forkPoint..branch`), and
`pull` replays through it. A declared trunk has none, so neither can replay it,
and nothing new is needed to make that so — only a test that pins it.
`restack --branch feature` restacks what sits on `feature`, exactly as
`restack --branch main` does. `feature` is shared — other people land into
it — and replaying it would do to them what replaying `main` would.

Keeping `feature` current with `main` is a merge of `main` into it, which is
not something g2g does. To replay it anyway — it is yours alone — give it a
parent with `track --parent main`: it becomes an ordinary branch in the stack it
came from, and replays like one.

### Following somebody else's rewrite

Never rewriting is not the same as refusing to follow. A declared trunk that
somebody else rewrote upstream is `remote history reverter`, and it gets the
same answer `main` does: `pull` takes the published version when everything
local is in it by content and replays the stacks above onto it, and refuses
otherwise until `pull --take published`, which names every commit it drops.
Only each branch's own commits (`forkPoint..branch`) are replayed, so a commit
removed upstream does not come back with the stack.

### The merge method is part of the declaration

Where a trunk goes and how it gets there are both properties of the branch, not
of one invocation. `land --onto`-style flags were rejected for the same reason:
`pull`, `status` and `land` would each need telling every time, and forgetting
once replays the stack onto the wrong base. `land --method` still overrides the
recorded method for one descent, and the preview says it is overriding.

`squash` is allowed and is not the default: the shape usually exists to keep
the small merges as separate commits. The declaration names the method
explicitly; there is no default to guess from.

### The command surface

Trunk-ness is part of "which branch sits under which", so it lives on
`track`/`untrack` rather than a new verb:

```text
g2g track --branch staging --as-trunk                          # a trunk
g2g track --branch feature --as-trunk --into main --by rebase  # one that lands into main
g2g untrack --branch feature                                   # not a trunk any more
```

`--trunk` was not used: on `adopt`, `status` and the rest it is a string naming
the base, and a boolean of the same name would read as that.

Declaring a branch that has an edge removes the edge. Its old parent does not
become `--into` by default: inferring it would be choosing where a shared
branch lands. The preview names the edge it removes and the parent it had, so
keeping it is one flag away.

`track --parent` on a declared trunk is the way back: it records the edge, and
the branch stops being a trunk and forgets where it lands, as any trunk does
when it is given a parent — `Graph.Track` already drops it from the set. The
preview says the declaration goes with it.

`untrack` on a declared trunk removes the declaration and the trunk entry. What
sits on it is reported as stranded, not reparented, as for any other untrack;
a declaration that lands into it is named in the preview and left as it is.
`untrack` on a trunk nobody declared is unchanged — still nothing to do —
because widening it would change what `untrack --branch main` means.

### Store

`declared` is an additive field. The store's rule is that adding a field does
not bump `storeSchemaVersion`, and fork points were added the same way. An
older g2g that loads the file ignores the field and drops it on its next save,
which leaves the branch a plain trunk: `land --branch feature` then refuses as
it does for any trunk. That degrades toward refusing, which is the direction
the rule is safe in.

Validation: every declared branch is in the trunk set and has no edge; its
`into` is not itself and has no edge; following `into` never cycles. The method
is a plain string to the graph, which depends on Git alone; `track` checks it,
and `land` refuses one it cannot use, naming `track --as-trunk --by`.

## Commands

| Command | On a declared trunk |
|---|---|
| `create` | a declared trunk is recorded, so `create` from it is allowed — **two-trunks** needs nothing else |
| `status` | drawn as `trunk`; one that lands somewhere adds `lands into main by rebase` |
| `restack`, `pull` from above | base, never replayed; `pull` fast-forwards it or takes a rewrite as for `main` |
| `restack --branch feature` | restacks what sits on it, never `feature` — as for `main` |
| `adopt`, `graphite adopt`, `github adopt` | a declared trunk is a conflict, never re-tracked |
| `land` from above | lands into `feature`, unchanged: it is the base |
| `land --branch feature` | a one-branch descent into `main`, by the recorded method |
| `push`, `github link`, `submit` | one branch on `main`, so its pull request targets `main` |
| `doctor` | nothing about its own edge, because it has none |

### Landing the trunk itself

`land --branch feature` resolves to base `main`, branches `[feature]`, and runs
the ordinary cycle: publish, wait, merge by the recorded method, wait for the
merge to reach `main`, advance `main`. Forgetting it removes the declaration
rather than an edge. Deleting it is the ordinary cleanup.

After the merge only the base moves: `pull`, asked for `main` alone, fast-forwards
it and replays nothing. Widening that to `main`'s stack would replay every stack
on `main` in the middle of somebody else's descent.

It **refuses while anything is still recorded on `feature`**, naming each
branch, and while another declaration lands into `feature`, because the
cleanup deletes it. Carrying unlanded children across would mean replaying them from a
shared branch's commits onto `main` in the middle of a descent — possible, but
the ordinary flow is to land them into `feature` first, and a refusal that
names them is honest where a half-designed replay would not be. This is the
first thing to relax if it proves common.

Landed detection needs no change: `Cherry` sees a merge or a rebase-merge by
content, `Absorbed` sees a squash.

## Out of scope

- Merging `main` into a declared trunk. That is a merge, and g2g does not make
  merges.
- Graphite. It has trunks but not trunks that land somewhere; `graphite mirror`
  mirrors the stacks above a declared trunk and says nothing about where it
  lands. A declared trunk Graphite has no trunk for blocks the mirror the way
  any unknown root does today.
- Landing a declared trunk with children still recorded on it (above).
