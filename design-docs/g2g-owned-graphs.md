# g2g-owned graphs

**Status:** implemented in v0.9. Restack is explicitly out of scope; see
[Known limits](#known-limits).

## Problem

Everything gt2gh does today starts by asking Graphite for a linear path. That
makes Graphite a hard runtime dependency for commands that otherwise only need
Git and GitHub, and it means the tool has nothing to say in a repository where
Graphite was never installed.

The structure being asked for is small: which branch is the parent of which.
Graphite holds it, GitHub holds a partial copy of it in pull request bases, and
neither is available in every situation a user is actually in.

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

## Authority

Authority is recorded **per branch**, not per graph:

| Authority | Meaning |
|---|---|
| `graphite` | Graphite declares this branch's parent. gt2gh reads, never writes. |
| `g2g` | The user adopted this edge into the local store. |

Per-branch authority is what makes the rule locally checkable. A whole-graph
rule cannot survive `gt track` on a branch that bridges two previously separate
components, because that merges them through an action gt2gh never observed.
The per-branch rule instead says: **an edge whose endpoints disagree about
authority is a conflict**, which localises the report to the offending edge and
never invalidates a component.

GitHub pull request bases are **observed**, not an authority. The distinction
that matters is adopted versus observed rather than authority versus import: an
observed base edge is renderable and comparable immediately, it just is not
something the user has committed to.

Precedence when several sources describe one branch:

1. Graphite, when installed and tracking the branch
2. the g2g store, when it holds an adopted edge
3. the branch's single open pull request base
4. unknown

A lower source disagreeing with a higher one is reported, never silently
merged. gt2gh does not write Graphite's metadata under any circumstances.

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

Commit SHAs are deliberately **not** stored. Commits and force-pushes are
normal content movement, not structural drift. Structure is validated by
branch existence and ancestry at read time instead.

The store carries its own schema version, independent of the `--json` output
schema. They evolve separately and must never be reasoned about as one number.
An unrecognised future store version fails closed.

## Commands

```text
g2g graph   [--branch <branch>] [--scope branch|path|subtree|graph]
g2g track   [--branch <branch>] [--parent <branch>] [--apply]
g2g untrack [--branch <branch>] [--scope branch|subtree] [--apply]
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

The existing `--no-stack` flag on the Graphite-backed commands is the same axis
as `--scope branch`. Unifying them is a user-visible change to six shipped
commands and is deliberately left to its own change; `--scope` is introduced
only on the new commands.

## Known limits

**No restack.** gt2gh does not rebase, and does not check out branches. It can
repair a tree's parent edges but cannot repair a branch's contents.

This matters most under squash merges. When the bottom branch of a stack is
squash-merged, GitHub retargets the child's pull request onto the new base, but
the child still carries the original pre-squash commits. Its merge base is
unchanged, so its pull request then shows the parent's changes a second time,
and merging it will try to reapply changes the trunk already has.

The consequence is stated rather than discovered: **until gt2gh owns restack, a
g2g-owned graph needs something else to repair branch contents after a merge.**
The graph is safe to build and safe to inspect; it goes stale in content, not
in structure, and `g2g graph` reports the branches affected.

When restack is implemented, the intended route is a detached temporary
worktree — rebase there, update the branch ref, remove the worktree — so that
HEAD, the index, and the user's working tree are never touched and the
no-checkout property survives intact. Conflicts abort and report rather than
leaving a half-finished state.

**No Graphite writes.** Graphite has no supported mutation contract that gt2gh
can rely on, so adoption is one-way: gt2gh can read Graphite structure but
never writes it back.

**No automatic adoption.** Nothing is written to the store without an explicit
`--apply`. Observing a pull request base or inferring an ancestry edge produces
a preview, never a record.

## Non-goals

Reordering, creating, merging, or rebasing branches. Replacing Graphite for
users who have it and are happy with it. Sharing the graph between clones or
machines — if that is wanted later it should be an explicit export/import or a
dedicated ref, never a silent addition to a normal branch push.
