# Source alignment

Source resolution decided *which* source answers for a branch. It left the
sources free to disagree, and gave nobody anything to do about it. This is the
other half: keeping them in step, in both directions, without either one taking
the other's work away.

## Problem

g2g can read Graphite and can never write it. That was the right call while
Graphite was the only source, and it became a gap the moment g2g grew its own
graph. Two symptoms:

- **Adoption silently strands Graphite.** Once a branch is in the g2g store,
  g2g stops asking Graphite about it and nothing puts Graphite back in step.
  `gt log` keeps showing a structure that is quietly wrong, and the user is the
  one who finds out.
- **There is no way in.** Someone already using `gt` can be read by `link`,
  `push`, `status`, and `submit` — but not by `restack`, which needs a fork
  point only the g2g store records. The only route is `track`, one branch at a
  time, and `track` refuses to guess.

Both are alignment problems, and they are not the same problem reversed.

## Authority is precedence, not exclusivity

A branch may sit in the g2g store, be tracked in Graphite, and appear in a
GitHub stack, all at once. Authority means only this: **when a g2g command needs
a branch's structure, which source does it read?**

- Resolution is per invocation and never stored, so there is no owner record to
  go stale.
- It resolves for the **target**, and that source then vouches for the whole
  path — never a stack half-described by each.
- `stack.G2GSelector.Describes` is per branch (does the store hold this edge?);
  `GraphiteSelector.Describes` is per repository (is Graphite configured?). So
  Graphite is the fallback for whatever g2g has not adopted, not a per-branch
  rival claim.
- It also gates mutation: `restack` refuses a Graphite-owned branch because it
  has no fork point to replay from.

Nothing about adoption removes anything from anywhere. **That is precisely what
makes alignment a coherent idea** — if adoption were exclusive there would be
nothing to align.

## The two directions are not inverses

|                   | Graphite → g2g (`import`)         | g2g → Graphite (`mirror`)    |
| ----------------- | --------------------------------- | ---------------------------- |
| Repeat semantics  | additive; re-running must not undo | idempotent by design         |
| Authority         | **transfers** — g2g starts winning | unchanged                    |
| Information       | **gains** a fork point            | **discards** the fork point  |
| Failure posture   | refuse on conflict                | overwrite — that is the job  |
| Writes            | the g2g store only                | Graphite only                |

The fork point is the proof. `Edge.ForkPoint` is the parent's tip when the edge
was written, and it **cannot be derived after the fact** — once a merged
parent's branch is deleted, the merge base points before the parent's work and
replaying from there reapplies it. Graphite has no equivalent field.

So importing does not copy a value Graphite hands over; it *manufactures* the
one thing that makes `restack` possible, using the same fallible bootstrap
derivation `track` already uses. Mirroring must throw that value away, because
Graphite has nowhere to put it.

Import then mirror does not return you to where you started. That is why these
are not `import`/`export`: a symmetric pair of names would promise a round trip
that does not exist.

## `mirror`

    g2g mirror --to graphite

Reconciles Graphite so it agrees with g2g **about the branches g2g knows**, and
leaves everything else in Graphite alone. Three operations, from a diff of the
two graphs:

| Diff                                    | Action                              |
| --------------------------------------- | ----------------------------------- |
| g2g has an edge Graphite lacks           | `gt track --parent <p>`             |
| the two disagree about a parent          | `gt track --parent <p>` (reparent)  |
| Graphite has an edge g2g lacks           | nothing, unless `--prune`           |

Scope is the **forest**, and the unit is the **branch**. Neither requires a
pull request, a push, or a network.

### Three properties Graphite's CLI decides, not preference

Each was read from `gt track --help` / `gt untrack --help` rather than assumed,
and each shows up as an ordering rule or a refusal:

- **`--parent` must already be tracked.** Writes go parents before children, and
  a g2g root Graphite has never heard of **blocks** rather than being invented:
  only `gt init` establishes a trunk, and enrolling a repository is not this
  command's business.
- **`gt untrack` cascades to the subtree.** A prune refuses a stranger with a
  surviving child — untracking it would take a branch the mirror had just
  aligned — and orders the rest deepest first.
- **Both commands take an explicit branch argument.** That is what keeps
  mirroring checkout-free. A mirror that checked out every branch in a forest
  would be a worse thing than the drift it fixes.

### Removal is not default-on

`sync --prune` defaults to true, and is right to, because its trigger is
unambiguous: the work has demonstrably landed. "Graphite knows a branch we do
not" carries no such certainty — it is just as likely to be a branch someone
deliberately tracked in `gt`. Destroying their work to satisfy alignment is the
wrong trade, so removal is opt-in and everything else is not.

### One word for removal

`sync` already spells this `--prune`, and `mirror` should spell it the same
way. That leaves one repository-wide vocabulary for a single idea — *drop what
the authority no longer has* — rather than a different affordance per command.

`link`/`unlink` remain a pair, and the asymmetry is deliberate rather than
historical: `unlink` removes a whole projection from a **published, shared**
artifact, which is worth typing on purpose and worth being able to do without
first computing a diff. `--prune` is a modifier on an alignment pass; `unlink`
is an operation in its own right. If `link` ever grows incremental removal, it
should be `--prune` too.

### Why not `link --to graphite`

`link` is the same idea — project the resolved structure into another system —
and differs in every particular that matters:

|              | `link` → GitHub          | `mirror` → Graphite |
| ------------ | ------------------------ | ------------------- |
| Unit         | pull requests            | branches            |
| Shape        | one linear path          | the whole forest    |
| Precondition | every branch needs an open PR | none           |
| Reach        | published; teammates see it | local sidecar DB |
| Removal      | separate `unlink`        | same command, gated |

A branch with no pull request is an ordinary thing to mirror and a blocking
`IssueMissing` for `link`. One flag cannot carry two contracts that disagree
about what the input even is.

The reverse is also closed: **GitHub's native stack cannot hold a tree**, so
`mirror --to github` could never mirror the graph — only one path of it, and
only where pull requests already exist. `--to` earns its place the moment a
second forest-shaped, branch-unit destination appears. GitHub is not one.

## `import`

    g2g import --from graphite

Adopts Graphite's declared edges into the g2g store in bulk, deriving a fork
point per edge. Additive and fail-closed: it adds edges g2g lacks and **refuses
where g2g already records a different parent**, naming them. That makes it
safely re-runnable when someone tracks a new branch in `gt`, without silently
reverting a deliberate g2g change.

It is not guessing. Graphite declares each parent, so the rule that `track`
blocks rather than choosing is not being relaxed — the declaration is the
answer.

Two details the implementation settled:

- **`Origin` is assessed against Git, not set to a Graphite value.** The field
  records how far Git agrees with an edge, not which tool supplied it, so an
  imported edge is judged exactly as a tracked one is. Graphite declaring a
  relationship does not make the commits line up, and adding an `OriginGraphite`
  would have replaced a fact about the repository with a fact about provenance.
- **Branches Graphite names that the checkout lacks are skipped.** Recording an
  edge for one would put a branch in the graph that no command could act on.

### Import takes authority, and must say so

Because precedence is fixed and holding an edge *is* the claim:

> `import` takes authority over every branch it touches.

That is not a data copy. It is a permanent-until-untracked authority shift
across the whole import set, and afterwards `--from graphite` is the only way
to see Graphite's view again. The preview must state it in those terms, not
merely list branches.

`mirror` has the opposite property and is safe by construction: it never writes
the g2g store, so it shifts nothing.

## `--from` and `--to`

`--to` names the write destination. `--from` pins the read side for a single
invocation, filtering the resolver to the named source:

    g2g status --from graphite
    g2g graph  --from graphite

`--from` is what makes the pair usable. Today, once g2g holds an edge there is
no way to ask Graphite's opinion at all — so there is no way to see the two
views before reconciling them. Nothing is recorded and nothing can go stale,
which keeps it consistent with resolution being derived rather than stored.

Values reuse the `Source` vocabulary already printed in `--debug`
(`source.resolved`) and in the JSON `source` field: `g2g`, `graphite`, and
`github`. One vocabulary for both directions.

Two obvious names are unavailable, both by collision:

- **`--target`** — `Target` already means the *selected branch* throughout
  `Snapshot`, the `discovery.target` diagnostic, and the JSON output.
- **`--onto`** — `restack` uses `onto` for the rebase base, a commit-ish.

## Two tools, one word

`g2g untrack` removes a branch from the g2g-owned graph. `gt untrack` removes
it from Graphite. A preview line reading `untrack synthetic-x` is ambiguous
about which store is losing the edge.

**Every preview line and every document names the store, never the bare verb.**

## Invariants

- **Neither command ever removes a branch from the g2g graph.** Alignment is
  not ownership transfer.
- `mirror` writes Graphite only. `import` writes the g2g store only.
- Both are previewable, and follow the existing
  preview → revalidate → render → flush → mutate sequence.
- `--prune` is the only path by which anything is removed from Graphite.

## What this costs elsewhere

**The read-only invariant changes.** g2g's entire Graphite surface today is
two read commands, `gt --version` and `gt log short --all --reverse
--no-interactive`. `mirror` adds `gt track` and, under `--prune`, `gt untrack`.
The claim that g2g "reads Graphite and never writes it back" becomes false and
must be rewritten here and in the repository skill, and the tests asserting no
`gt` invocation need re-scoping to the commands that legitimately make none.

**The enrolment gate needs no exception after all.** The proposal expected one:
`mirror` is explicit consent to write Graphite, so the gate would not apply.
Building it showed the exception is unnecessary and worse. A mirror has to read
Graphite's forest to compute its diff, and reading runs the discovery command —
so a *preview* would have been what enrolled the repository, which is the same
bug as the one shell completion had. A repository with no Graphite also has no
trunk, so a mirror into it is blocked for want of a root in any case. Refusing
first reaches the identical answer with no side effect.

**"No g2g command enrols a repository" therefore holds without exception**,
including for the commands that write Graphite. That is a stronger invariant
than the one this document originally proposed, and it is worth keeping.

**`gt track`'s contract is now pinned** in
[`graphite-cli-contract.md`](graphite-cli-contract.md), alongside `gt log`'s
grammar and under the same version gate.

## Rejected

Every rejection below is on semantic grounds, not cost. None of them would
change if the existing code were free to rewrite: they are cases where the
merged thing would have to mean two incompatible things at once, or where the
stored answer would be one that cannot be kept true.


**Stored per-stack authority.** Rejected in `source-resolution.md` and not
reopened by anything here. A stored owner goes stale through actions g2g never
sees, and the presence of an edge in the g2g store already *is* the claim — the
former `Authority` field was written once and never read for a decision. Mirror
does not undermine this, because mirror never writes the g2g store.

**Per-stack authority in any form.** A tree can legitimately span sources —
`main ← A ← B` where A is Graphite's and B was adopted — so a per-stack rule
forces a wrong answer. Per-branch resolution makes the crossing point a
boundary that is locally checkable. "Per stack" is "per tree" renamed and
inherits its defect.

**`export`.** The right word for a handoff where Graphite takes ownership and
g2g stops managing the branches. That is explicitly not wanted, so the word
stays unspent rather than being spent on the mirror and needing renaming later.

**Mirroring as a side effect of `track`/`untrack`/`restack`.** Keeping Graphite
in step on every mutation is the obvious convenience and the wrong shape: it
makes an outward write the untyped tail of another command, with no preview of
its own. An explicit command that is cheap to re-run is better than an implicit
one that is hard to see.

**A mirror watermark.** Recording what was last mirrored would let `mirror`
distinguish "an edge we wrote and g2g has since changed" from "an edge the user
just added in `gt`". It would also be the first stored cross-tool state in a
design that has deliberately avoided any. g2g wins, the preview names every
edge it will overwrite, and that is enough.

## Milestones

All four are built, in this order and for these reasons:

1. **`--from`.** Smallest, independently useful, and a prerequisite for
   trusting either of the others: it is the only way to see both views. Pure
   read path, no new external command.
2. **The gated Graphite write surface**, then **`mirror` without `--prune`**.
3. **`import`.** Needs fork-point derivation and the conflict refusal, and is
   the one that shifts authority, so it went after a `--from` view existed to
   judge what it is about to claim.
4. **`mirror --prune`.** Separable, and the only destructive piece.

## Deferred

**Handing a stack back to Graphite.** Today that is `untrack` per branch across
the path, which drops the fork points and so ends `restack` for those branches.
The mechanism is right and the affordance is missing; if it is worth one
command it is a path-scoped `untrack`, not a stored authority field.

**Mirroring to GitHub.** Closed while native stacks are linear, not deferred
pending effort.

**Retargeting pull request bases.** Built, as `g2g retarget`. It is its own
command rather than a step inside `submit` or the tail of `restack`, because
changing what a merge will do is a different class of act from creating a pull
request and wants its own preview. It moves only the bases that disagree,
refuses a branch with more than one open pull request rather than choosing, and
is a no-op when GitHub already agrees — which is what makes it safe to run after
every restack.
