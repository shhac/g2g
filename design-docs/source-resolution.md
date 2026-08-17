# Source resolution

**Status:** implemented in v0.11. Pull request retargeting is deliberately out
of scope; see [Deferred](#deferred).

## Problem

Every command that selects a stack asks Graphite, and only Graphite.
`stack.Resolve` requires a Graphite client, so `push`, `link`, and `submit`
cannot serve a branch graph g2g owns — the thing the previous release built.

It fails badly rather than cleanly. Running `g2g push` on a g2g-owned stack in
a repository that has never used Graphite invokes `gt`, fails with a Graphite
error, and leaves `.graphite_metadata.db` and `.graphite_pr_info` behind in
`.git`. A repository that deliberately has no Graphite is enrolled into it by
running a g2g command.

## The question

Given a branch, which source describes it, and what does that permit?

```text
resolve(branch) → (parent, source)
  a g2g edge exists      → g2g            adoption is the claim
  Graphite tracks it     → graphite       when gt is installed
  exactly one open PR    → pull-request   observed, not adopted
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
g2g sync = fetch + restack + prune
```

- **fetch** into `refs/g2g/remotes/`, leaving the user's refs alone
- **fast-forward the trunk**, refusing when it is not strictly behind — never
  merging, never rewriting
- **restack** the selection
- **prune** the graph: untrack branches whose work is entirely upstream, which
  is exactly what a restack has already collapsed

Prune is graph-level by default. Deleting a local branch is a separate,
explicit opt-in: it is the one step here that destroys something, and this tool
does not do that implicitly.

## After a merge, a disagreeing base is not drift

Because GitHub retargets a child when its base branch is deleted on merge, a
pull request base that no longer matches the recorded parent is the *expected*
result of a merge, not evidence of a problem. The same disagreement without a
merge means someone retargeted and the native stack needs relinking.

Pull request state is what tells those apart, which is the same reason Git
alone cannot detect a squash merge.

## Deferred

**Retargeting a pull request base** (`gh pr edit --base`). After a restack the
local stack is correct and the remote bases may not be. Nothing does this yet
and `submit` explicitly refuses to, so it stays refused rather than becoming a
side effect of another command. It is a new class of GitHub mutation and wants
its own preview and apply scrutiny.

**Whether one pull request may belong to two native stacks.** Unanswered, and
it decides what `link` does on a fork. It needs an experiment against real
GitHub, not a decision.
