# g2g-owned graphs

**Status:** implemented. This is g2g's primary structure model; see the
[README](../README.md) for product orientation and
[source resolution](source-resolution.md) for how it composes with Graphite
and pull-request bases.

## Problem

Stack structure is useful before branches are pushed, when Graphite is absent,
and when a repository has more than one line of work. Git itself supplies
ancestry, but it does not retain the intended parent edge after a branch moves
or a parent is squash-merged. GitHub pull-request bases describe only published
work and describe merge behaviour rather than local intent.

g2g therefore records the small, local fact every later operation needs: which
branch is the intended parent of which. The record is independent of Graphite
and GitHub, and remains a forest even though GitHub projection is linear.

## Model

A g2g-owned graph is a **forest of trees over branch names**:

- every branch has at most one parent;
- a parent may have many children;
- there may be several roots, because a repository may have several trunks —
  some recorded because something was stacked on them, some declared, which
  is how a trunk can also say where it lands (see
  [declared trunks](declared-trunks.md));
- the model is never constrained by what `gh stack link` can currently express.

```text
main
├─ synthetic-auth
│  ├─ synthetic-login
│  └─ synthetic-session
└─ synthetic-billing
   └─ synthetic-invoice
```

The tree shape is the reason this state has to exist at all. GitHub native
stacks are linear, so a tree cannot live there. Pull request bases can express
a tree but only for branches that have an open pull request, so they cannot
describe local work. Graphite can express a tree but may be absent.

**Trees are the model even though today's projection is linear.** When a tree
is projected onto a GitHub native stack, one root-to-leaf path is selected and
projected; the tree is neither flattened nor rejected for containing a fork.
That is a property of the projection, not of the graph, and it is expected to
change if `gh stack` gains tree support.

## Authority and sources

An edge in the local store is an adoption claim, not a mutable owner field.
Source resolution is **per branch**, not per graph: a path may legitimately
cross from a g2g-recorded edge to a Graphite-described one. That prevents a
whole-tree rule from becoming stale when sources change independently.

Precedence when several sources describe a branch is:

1. the g2g store, when it holds an adopted edge;
2. Graphite, when it tracks a branch g2g has not adopted;
3. a pull-request base only when explicitly selected with `--from github`;
4. unknown.

GitHub bases are observed merge behaviour, not local intent. A disagreement is
reported rather than silently merged. Graphite can remain alongside the local
record: `graphite adopt` adopts Graphite edges into g2g, and `graphite mirror`
makes Graphite agree with the g2g forest. See [source alignment](source-alignment.md).

## Deriving edges from Git

Parents are inferred from commit ancestry, which needs no network and works for
branches that have never been pushed.

For a target branch `C`, the preferred candidates are the branches whose tip is
an ancestor of `C` — what `git for-each-ref --merged C` returns — plus the
roots the graph already records, which are offered regardless of ancestry
because a trunk stops being an ancestor the moment it moves ahead. A recorded
root that no longer exists locally is dropped rather than offered as a parent
that could never be validated.

Each candidate is then measured with one
`git rev-list --left-right --count <candidate>...C`. That single invocation
answers both directions: nothing ahead means the candidate is a true ancestor,
nothing behind and something ahead means it already contains `C` and more, and
is therefore a descendant and never a parent. The commits behind are the
ordering, and the nearest is the parent.

Nothing either way means the candidate is at `C`'s own commit. Each is then an
ancestor of the other and ancestry cannot say which sits on which — and it is
the state of every branch the moment it is created, so it is the ordinary case.
Such a candidate is offered, marked as the same commit, for the user to answer.
`adopt` refuses it by name rather than ordering it, with one exception:
the trunk, whose place the user asserted, so a branch just created from it
sits on it. A branch as near the trunk as it is to the selection sits directly
on the trunk and stays out, like any other stack there.

Measuring from the merge base rather than requiring ancestry is what makes the
first adoption possible. An empty graph records no roots, and the trunk a
branch forked from has almost always moved on since, so ancestry alone returns
nothing at all. When the preferred set comes back empty, every local branch is
measured instead: the fork point is still there. That fallback costs one Git
call per branch and runs once per repository, not once per command.

The method degrades in the right direction elsewhere too. When a parent is
squash-merged and deleted, it simply drops out of the candidate set, the child
falls through to the trunk question, and the trunk is the correct answer at
that point.

One primitive answers three questions, which is why it is worth having:

| Question | Check |
|---|---|
| What is my parent? | nearest branch the target is ahead of |
| Has my recorded parent drifted? | is the recorded parent's tip still an ancestor |
| Does this branch need a restack? | it stopped being one |

A recorded parent whose tip is no longer an ancestor of its child is reported
as `needs restack`, not silently reparented. That distinction matters: a
missing candidate means "the parent moved", not "there is no parent", and
treating the two the same would quietly reparent a stale child onto the trunk.

## Storage

Adopted edges live under the Git common directory:

```text
$(git rev-parse --path-format=absolute --git-common-dir)/g2g/graph.json
```

`--path-format=absolute` is required. The bare `--git-common-dir` is resolved
relative to the current working directory, so it returns `.git` from the
repository root and `../../.git` from a subdirectory.

The common directory is shared by linked worktrees, is never part of a diff,
does not dirty a checkout, and does not conflict with the clean-worktree
precondition that every mutation depends on. It is deliberately not shared
between clones and is deliberately not pushed: a fresh clone starts with no
adopted edges, which is consistent, because the unpublished branches those
edges describe do not survive a clone either.

Writes go to a temporary file in the same directory and are renamed into place,
so a concurrent reader sees either the old file or the new one and never a
partial write. Concurrent writers are last-writer-wins; the store is small,
written only by an explicit `--apply`, and locking it would buy nothing.

The file is a flat `branch -> {parent, authority, origin}` map plus the trunk
set. `origin` records whether Git already agreed with the edge when it was
written: `git-ancestry` when the parent's tip was reachable from the branch,
`user` when it was not. The second is legitimate — it is how a stack looks
before a restack — but `track` says so before writing it, because that fact is
the one that explains why the branch will subsequently read as needing a
restack. **Graph identity is derived, not stored.** A graph is a connected
component of the edge relation, which is a computation rather than a record, so
there is no identifier to generate, no branch-to-graph index to maintain, and
no merge or split event when two components join. Branch rename becomes a key
rewrite rather than a graph migration.

A branch's own tip is deliberately **not** stored. It moves with every
ordinary commit, so recording it would make routine work look like the graph
had changed.

The **fork point** is stored, and the distinction matters. An edge records the
parent's tip at the moment the edge was written:

```json
"synthetic-login": { "parent": "synthetic-auth", "forkPoint": "1005ca4…" }
```

That is not drift state, it is structural state — it answers *which commits
are mine*, namely `forkPoint..branch`. Without it a restack cannot compute what
to replay, because the range must exclude everything that was already in the
parent. It changes only on structural events (adopting an edge, restacking),
never on a commit or a force push.

It is also what lets a restack survive its parent's deletion. Once a merged
parent's branch is gone, `merge-base(trunk, child)` points at the fork with the
*old* trunk and replaying from there would reapply the parent's work; the
recorded fork point still says exactly where the child's own commits begin.

This is the one place the model follows Graphite's rather than diverging from
it: Graphite stores `parentBranchRevision` per edge for the same reason.

The store carries its own schema version, independent of the `--json` output
schema. They evolve separately and must never be reasoned about as one number.
An unrecognised future store version fails closed.

## Commands

```text
g2g status  [--branch <branch>] [--scope branch|path|subtree|stack|trunk|all]
            [--from g2g|graphite] [--remote <remote>]
g2g doctor  [--remote <remote>]
g2g adopt   [--branch <branch>] [--trunk <branch>] [--apply]
g2g track   [--branch <branch>] [--parent <branch>] [--apply]
g2g untrack [--branch <branch>] [--scope branch|subtree] [--apply]
g2g restack [--branch <branch>] [--scope branch|path|subtree|stack] [--apply]
g2g create  <branch> [--parent <branch>] [-m <message>] [--apply]
g2g delete  [--branch <branch>] [--apply]
g2g fold    [--branch <branch>] [--apply]
g2g rename  [--branch <branch>] <new-name> [--apply]
g2g up [n] | down [n] | top | bottom   [--dry-run]
```

`status` and `doctor` are read-only. `adopt`, `track` and `untrack` follow the
same preview → revalidate → render → flush → mutate sequence as every other
mutating command; the only difference is that the mutation writes a local file instead
of calling an external CLI.

`track` with no `--parent` previews the ordered candidate list and blocks,
because choosing a parent for the user is exactly the guess this tool does not
make. With `--parent` it validates that the parent exists locally and that the
edge would not close a cycle, and it reports whether Git already agrees with
the edge. A parent that is not an ancestor is recorded on request rather than
refused — that is how a stack looks before a restack — but never silently.

Recording a branch under a parent that is not itself tracked also records that
parent as a root. Without it the next branch up the stack could not find the
trunk as a candidate once the trunk had moved past being an ancestor.

### Reading where a branch stands

`status` draws the recorded forest with each branch's state — needs restack,
moved off parent, parent missing, landed — and, beside it, how the branch
stands against its remote. That second half needs no network either: it reads
what the remote last held from local refs, the remote-tracking ref a push or a
fetch moves and the ref under `refs/g2g/remotes/` that `pull` fetches into,
taking whichever descends from the other and the remote-tracking ref when they
are not in order. Reading only the remote-tracking ref would have the trunk
claim, after every pull, to be ahead by everything just pulled. Commits are
counted by content, as `push` counts them, so a branch replayed since it was
pushed reads as replayed rather than diverged. The comparison is wired in the
command, not in `internal/graph`, which still depends on Git alone.

`doctor` reads every recorded stack the same way and reports only what is
wrong, each finding with the one command that repairs it, and exits `1` when it
finds anything. The two are split on purpose: `status` is the overview of the
stack you are on, `doctor` the list of the unexpected across all of them, most
of which broke outside g2g.

### Creating a branch

`create` was a non-goal, and became a command once the daily loop showed what
it cost to leave out: `git switch -c`, a commit, then `track --branch X
--parent Y --apply`, retyping a parent the user had been standing on a moment
before. It does not weaken the rule `track` follows. The parent is the branch
you stand on or the one `--parent` names — stated, never inferred — which is
the same reason `track --parent` is not a guess, and why `create` shows no
candidate list.

It adds one refusal `track` has no reason to make. Recording a child under a
branch the graph does not know makes that branch a root, so a feature branch
nobody recorded would silently become a trunk. `create` therefore requires the
parent to be recorded — tracked, or a trunk something is recorded under — or to
be the repository's default branch, which is a trunk by the only evidence the
repository gives (`refs/remotes/<remote>/HEAD`). Anything else is refused,
naming `adopt` and `track --parent` as the ways out.

The edge is written through `PlanTrack` and `ApplyTrack`, so a created branch
is recorded exactly as `track` records one, fork point included. The order is
switch, record, commit: a recording that fails is rolled back completely —
back to where you were, the new branch deleted — because the branch has no
commits of its own yet, while a commit that fails after the record stays
recorded and reports exit status `3`, since the branch and its edge are what
was asked for and the staged changes are still staged.

### Moving between branches

`up`, `down`, `top` and `bottom` move the checkout around a stack resolved the
way every stack command resolves one. They are the one deliberate exception to
preview-first: moving the checkout changes no ref, no record and no remote, and
`git switch` already refuses to overwrite a local change, so a preview would
only be a second command to type. `--dry-run` answers where without moving.
What they keep is the refusal to choose — a fork names the branches above it
and stops — and the guard every mutating command has, because switching away
mid-restack strands the rebase in progress. The switch is `git switch
--no-guess`, so a destination a stale record names is refused rather than
recreated from a remote-tracking branch.

A trunk is a branch nothing sits under, so no source describes it as part of a
stack, and it is exactly where someone types `up`. When resolution finds
nothing, the g2g graph is asked what it records directly on the branch; one
child is the answer and the rest of the walk resolves from there, more than one
is a fork.

### Reshaping a stack

`delete`, `fold` and `rename` change which branches a stack is made of. They
act only on branches the g2g graph records and refuse anything else naming
`track`, because the record is what says where a removed branch's children
belong. None of them replays a commit — deleting removes a ref, folding
fast-forwards one, renaming moves one — so `restack` stays the only thing that
rewrites history, and each says when a restack is the next step rather than
running one.

**Delete reparents; untrack does not.** `untrack` must never reparent the
children it strands, because nobody said where they belong and choosing is the
guess this tool does not make. `delete` records each child on the deleted
branch's parent, and that is not the same guess: the user asked for the branch
to go, and what it sat on is the only place its children can mean. The rule
lives on the type, as `Graph.Remove`, beside the `Untrack` that keeps its own.

The children keep their fork points. A fork point is where the parent's work
ended when the edge was written, so after a delete it is where the deleted
branch's work ended, and the next restack replays `forkPoint..child` onto the
new parent: only the child's own commits, with the deleted branch's left
behind. That changes what the child contains, so the preview says it plainly,
and names every commit of the deleted branch that exists nowhere else — none
of its content in the parent (`landed.Into`, then `Cherry` bounded by the fork
point, so a squash-merged branch loses nothing) and no remote-tracking ref
reaching it. That is a local read: a commit a remote holds that this clone has
not fetched reads as unpublished, which errs toward warning. The remote branch
is untouched.

**Fold is a fast-forward, not a merge.** The parent moves to the branch's tip
only when it is an ancestor of it, under a lease on its old tip; a parent that
has moved on needs `restack` first. A trunk is never folded into: moving a
trunk puts work on it that no review saw, one push from publishing it, and a
branch joins its trunk through its pull request, which is `land`. The folded
branch's children already sit on the parent's new tip and keep their fork
points, which now match it; the parent's other children do not, and the preview
names them.

**Rename is a key rewrite.** Graph identity is derived, so renaming touches the
branch's own edge, the edges recorded under it, the trunk list, and the
fork-point pin, and nothing else. `git branch -m` moves HEAD in any worktree
that has the branch, so a rename there is allowed where a delete or fold is
refused. The published branch and its pull request stay under the old name;
nothing here renames a remote.

Every apply orders its steps so everything but the last can be put back: moves
and switches first, where Git refuses outright rather than losing anything;
then the record, which restores exactly; the branch ref last. A failure puts
back what was done. A rollback that cannot finish, or a finished removal that
could not release its fork-point pin, exits `3`.

`split` is deliberately not offered: dividing a branch's commits means choosing
which commit goes where, which is interactive by nature and at odds with never
guessing — `git rebase -i` plus `g2g create`/`g2g track` does it with the person
choosing.

### Why `--scope` and not `--tree`

A boolean frames tree operation as the exception and cannot express "this
branch and everything under it", which is the scope a user actually wants when
working on one sub-stack of a larger tree.

`--scope` is a *graph selection* concept and is separate from projection
policy. Selecting a subtree for display does not imply that a subtree can be
projected onto a GitHub native stack.

`--scope` is shared by every source that can describe a stack. The allowed
values and defaults differ by operation; [stack scope](stack-scope.md) records
the common vocabulary.

## Limits and safety boundaries

`restack` maintains branch contents after the recorded parent moves, including
the common squash-merge case. It previews exact replay ranges before changing
refs. A clean replay leaves the working tree alone; a conflict falls back to
the user's working tree so it can be resolved, then resumed or aborted through
g2g's journal. [restack](restack.md) describes those boundaries.

GitHub native stacks remain linear. A fork is valid local structure but must be
narrowed to a path before `github link`, `submit`, `push`, or
`github retarget` can project it. `graphite mirror` is the sole
Graphite-writing command and only makes a configured Graphite repository agree
with the local forest; all other Graphite use is read-only.

**No automatic adoption.** Nothing is written to the store without an explicit
`--apply`. Observing a pull request base or inferring an ancestry edge produces
a preview, never a record.

## Non-goals

Merging branches locally — nothing here runs `git merge`; `fold` only
fast-forwards a parent the branch already contains — and silently sharing the
graph between clones or machines. Creating branches was listed
here and is now `create`, for the reason given under
[Creating a branch](#creating-a-branch). `land` merges pull
requests, which is GitHub's merge rather than this tool's: asked for
explicitly, previewed as the commands it would run, and refused whole if any
branch in the stack cannot take it.

If shared structure is wanted later it should be an explicit export/import or a
dedicated ref, never an accidental addition to a normal push.
