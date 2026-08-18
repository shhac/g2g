# Source resolution

**Status:** implemented. `retarget` is the explicit pull-request-base mutation;
it is intentionally separate from publishing and restacking.

## Problem

g2g has several useful descriptions of branch structure, but they answer
different questions. Its own local forest records intent without requiring a
service. Graphite can describe structure a repository already maintains there.
Pull-request bases show what GitHub will merge, but only for published work.

Selection must compose those sources without making Graphite a prerequisite or
running it in a repository that has never used it. Graphite discovery creates
local state, so asking whether it applies must itself remain side-effect free.

## The question

Given a branch, which source describes it, and what does that permit?

```text
resolve(branch) → (parent, source)
  a g2g edge exists      → g2g            adoption is the claim
  Graphite tracks it     → graphite       when gt is installed
  exactly one open PR    → pull-request   observed, asked for
  otherwise              → unknown
```

Per branch, never per tree, and never stored.

### Why not stored

A stored owner goes stale through actions g2g never sees. `gt track` on a
branch that bridges two trees merges them, and a whole-tree rule then has to
invalidate both. Deriving the answer each time removes the entire class:
nothing to migrate, nothing to reconcile, and no way for the record to be
wrong.

The presence of an edge in the g2g store *is* the claim, so no separate field
is needed. The previous `Authority` field was written once with a single value
and never read for a decision.

### Why not per tree

A tree can legitimately span sources: `main ← A ← B` where A is Graphite's and
B was adopted into g2g. Per-tree forces a wrong answer. Per-branch resolves
each naturally and makes the edge between them a **boundary** — locally
checkable, reportable, and not something a distant change can invalidate.

### What the sources are actually good for

| Source | Expresses | Unpublished branches | Trees | Shared |
|---|---|---|---|---|
| Graphite | intent | yes | yes | no |
| g2g store | intent | yes | yes | no |
| pull request bases | **effect** | no | yes | yes |

Graphite and the g2g store say what was meant. Pull request bases say what
GitHub will do on merge. That is not a weaker authority, it is authority over
a different question, and both answers are worth having:

- *What is the structure?* — intent, because it covers work that was never
  pushed.
- *What will GitHub do?* — bases, always, because that is what a merge follows.

Drift is where those disagree, and reporting it is the most useful thing a
read-only command can do.

### The pull request source answers only when asked

It is not consulted by precedence, and the reason is a constraint rather than a
preference: reading a base means invoking `gh`, and `push` must never do that.
A source that would be asked merely to resolve a branch would drag GitHub into
a command whose whole contract is that it does not go there. So it sits in the
resolver's on-request tier and answers `--from pull-request`.

That also suits what it is. It describes only published branches, and GitHub
retargets a child when its base branch is deleted on merge — so right after a
parent lands, its children point at the trunk. The structure is never wrong
about what GitHub will do; it stops being a record of what the stack was.
Asking for it makes that an informed reading rather than a silent one.

### Each source builds its own forest, and only its own

Every source has an adapter package that produces the shape, and `stack`
selects within it. Keeping that arrow one-directional is what stops source
resolution from accumulating one traversal per record:

| source | builds the forest | where |
|---|---|---|
| Graphite | `graphite.ReadForest` | `internal/graphite` |
| g2g store | `graph.Graph.Shape` | `internal/graph` |
| pull request bases | `githubstack.BuildForest` | `internal/githubstack` |

`internal/stack` converts each to `shape.Forest`, applies the scope, and builds
the snapshot. It is the only one of the three that has to reach the network, so
it is also the only one with a bound.

### Following a base that is not checked out here

A pull request base need not be a local branch. It happens whenever part of a
stack was published from another machine, and the branch in the middle joins
two subtrees that are both here. Reading only local branches drops that edge, so
the lower subtree reads as a root of its own — and reads that way silently,
which is what misleads rather than what is missing.

So the walk follows bases it does not recognise, in rounds:

- **A round is one invocation.** `Inspect` resolves every branch handed to it in
  a single aliased GraphQL query, so a round costs the same whether it is asking
  about one unknown or forty. The bound is therefore on the *depth* of a
  remote-only chain, never on its width.
- **The bound is `FollowRounds`.** Four consecutive branches none of which are
  on this machine is somebody else's stack, and the honest thing at that point
  is to say where the following stopped rather than keep going.
- **Where it stopped is reported**, because a tree that silently ends early is
  indistinguishable from one that is finished.
- **The structure is read once per invocation.** Resolution asks `Describes` and
  then `Select`, and each needs all of it; building it per question doubles
  every round.

**Structure is not permission.** A branch that is not here has no ref to push,
rewrite, or link, so it is carried on the snapshot as `Absent`, rendered as
`on remote only`, and every command that mutates refuses it by name. That
refusal lives on the snapshot rather than in each command, because "which
commands remembered to check" is not a property worth depending on.

**GitHub's native stack is not a source at all.** Nothing defines a stack by
editing it; branches and bases are edited and the native stack is written from
them. It is a projection artifact, and its only role here is drift detection —
which is already all `status` uses it for.

## Authority governs mutation, not description

Reading composes across sources: a selection returns one coherent ordered path
whichever source supplied each edge. Writing stops at the boundary.

- `restack` needs a fork point, which only the g2g store records, so it refuses
  a Graphite-owned branch and says to `track` it.
- `track` and `untrack` write the g2g store, which is how a branch changes
  hands in either direction. That is the single remedy for every "our record
  disagrees" state.
- `link` and `push` only need an ordered path, so they work with any source.

A Graphite-backed path must **refuse when the repository is not already
Graphite-tracked** rather than invoking `gt` and enrolling it.

### Completion is a question, and questions cost nothing

Shell completion draws on the same sources, in the same order, so a flag never
offers a branch the command would refuse and never reaches a source the command
would not have reached either. It differs from selection in three ways, each
following from the fact that a keystroke is not a request:

- **Every source is merged rather than the first that answers.** Which source
  owns a branch is decided per branch, so narrowing completion to one of them
  would hide branches the command would accept.
- **A source that cannot answer is skipped, not fatal.** One unusable source —
  Graphite installed but broken — costs its own candidates and nothing else.
  The user learns what is wrong from the command they are completing, which can
  say it properly; a shell has nowhere good to put an error.
- **The enrolment gate applies here too, and this is where it bites hardest.**
  Completion used to run Graphite's discovery command unconditionally, so
  pressing tab in a repository that had never used Graphite created Graphite
  state in it — and then failed anyway, because there was nothing to report. A
  side effect nobody asked for, from a keystroke nobody thinks of as a command.

Only the g2g store answers in a repository with no Graphite, and it answers
from one file read with no subprocess at all.

### `--trunk` against a recorded path

A Graphite ancestry can carry several declared trunks, which is what `--trunk`
disambiguates. A recorded path has exactly one root, so the flag can only ever
confirm the base g2g already derived. It is accepted when it names that root
and **refused when it names anything else**, rather than ignored: silently using
a different base than the one asked for is how a stack gets pushed at the wrong
thing. Completion offers that single value, so the two agree.

## One operation, one name

`link` and `sync` were the same operation. Both guarded the same way, both
no-opped on a path shorter than two branches, and both ended in one
`gh stack link` with the same arguments. They differed only in the vocabulary
their previews used for pull request state: `unresolved/missing/closed/merged`
against `aligned/divergent/missing/unsafe`.

`link` absorbs it. One command, one mutation, one vocabulary. Creating the
relationship and repairing it are the same act, and the preview says which one
is happening.

That frees `sync` for the meaning `gt sync` already has, which is what a
stacking user expects:

```text
g2g sync = fetch + advance the base + restack
```

- **fetch** into `refs/g2g/remotes/`, leaving the user's refs alone
- **fast-forward the trunk**, refusing when it is not strictly behind — never
  merging, never rewriting
- **restack** the selection

Forgetting what has landed was originally the fourth step here. It became its
own command: it answers a different question on the same boundary, and being a
tail cost it both a scope and a test. See [stack scope](stack-scope.md).

## Scope

How much of a stack a command means is its own question, answered the same way
whichever record describes the branch. It is written up separately in
[stack scope](stack-scope.md), which supersedes the `--no-stack` boolean this
document originally described.

## After a merge, a disagreeing base is not drift

Because GitHub retargets a child when its base branch is deleted on merge, a
pull request base that no longer matches the recorded parent is the *expected*
result of a merge, not evidence of a problem. The same disagreement without a
merge means someone retargeted and the native stack needs relinking.

Pull request state is what tells those apart, which is the same reason Git
alone cannot detect a squash merge.

## Completed boundary: retargeting pull-request bases

`g2g retarget` uses `gh pr edit <number> --base <branch>` to make open pull
requests match the resolved linear path. It previews the affected pull requests
and their old and new bases, revalidates before `--apply`, changes only bases
that disagree, and refuses a branch with more than one open pull request.

It remains separate from `submit` and `restack`: changing what a merge will do
is a different mutation from creating a pull request or replaying local refs.

## Deferred

**Whether one pull request may belong to two native stacks.** Unanswered, and
it decides what `link` does on a fork. It needs an experiment against real
GitHub, not a decision.
