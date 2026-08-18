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
- there may be several roots, because a repository may have several trunks;
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
3. a pull-request base only when explicitly selected with `--from pull-request`;
4. unknown.

GitHub bases are observed merge behaviour, not local intent. A disagreement is
reported rather than silently merged. Graphite can remain alongside the local
record: `import` adopts Graphite edges into g2g, and `mirror` makes Graphite
agree with the g2g forest. See [source alignment](source-alignment.md).

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
nothing behind means it already contains `C` and is therefore a descendant and
never a parent. The commits behind are the ordering, and the nearest is the
parent.

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
g2g graph   [--branch <branch>] [--scope branch|path|subtree|stack|trunk|all]
g2g track   [--branch <branch>] [--parent <branch> | --stack] [--apply]
g2g untrack [--branch <branch>] [--scope branch|subtree] [--apply]
g2g restack [--branch <branch>] [--scope branch|path|subtree|stack] [--apply]
```

`graph` is read-only. `track` and `untrack` follow the same
preview → revalidate → render → flush → mutate sequence as every other mutating
command; the only difference is that the mutation writes a local file instead
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
narrowed to a path before `link`, `submit`, `push`, or `retarget` can project
it. `mirror` is the sole Graphite-writing command and only makes a configured
Graphite repository agree with the local forest; all other Graphite use is
read-only.

**No automatic adoption.** Nothing is written to the store without an explicit
`--apply`. Observing a pull request base or inferring an ancestry edge produces
a preview, never a record.

## Non-goals

Creating or merging branches, and silently sharing the graph between clones or
machines. If shared structure is wanted later it should be an explicit
export/import or dedicated ref, never an accidental addition to a normal push.
