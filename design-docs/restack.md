# Restack

**Status:** designed, not implemented. Everything below was verified against
real Git on local repository pairs; the observed behaviour is noted where it
decided the design.

## Problem

When the bottom of a stack is squash-merged, GitHub retargets the children's
pull requests but their commits are untouched. A child still carries the
parent's pre-squash commits, so its merge base is unchanged and its pull
request shows the parent's changes a second time. Merging it then tries to
reapply work the trunk already has.

gt2gh currently repairs structure and not contents, so the stack rots on the
first merge. Restack is the operation that fixes the contents.

## What Git can and cannot tell us

**Git alone cannot detect a squash merge.** After squashing a two-commit branch,
`git cherry` marks both commits as unmerged, because the squash's patch id
matches neither. The trap is that it *does* detect the single-commit case, so a
Git-only heuristic passes every simple test and fails in production.

The merged state therefore comes from the pull request, not from Git. That is
not a convenience, it is the only reliable signal.

## The rebase range

The naive `git rebase <trunk> <child>` conflicts once the parent's commits
overlap the trunk's, because it tries to replay commits whose content is
already upstream in a different shape. It must be:

```sh
git rebase --onto <new parent tip> <fork point> <branch>
```

so only the branch's own commits are replayed. The fork point is the parent's
tip at the time the edge was recorded — see the storage note in
[g2g-owned-graphs.md](g2g-owned-graphs.md). This is why the fork point is
stored rather than derived: after a merged parent is deleted there is nothing
left to derive it from.

**Children reparent onto the merged branch's recorded parent**, never onto "the
trunk". Those coincide in a simple linear stack and diverge everywhere else —
a branch rooted on `release-2x` must go back to `release-2x`, and a stack with
an unmerged branch below the merged one reparents onto that branch. The rule
needs no notion of a trunk at all.

## Two engines

| | `git replay` | `git rebase --update-refs` |
|---|---|---|
| Touches worktree/HEAD | never | yes |
| Whole stack in one call | yes, atomically | yes, per linear path |
| Conflict | exit 1, **no ref moves**, no state to clean | stops, resumable |
| Resolvable | no `--continue` | `--continue` / `--abort` |
| Preview without mutating | `--ref-action=print` | — |

`git replay` is EXPERIMENTAL (Git 2.44+), so it needs the same
compatibility gate the Graphite CLI has.

**Preview** uses `git replay --ref-action=print`, which yields the exact
resulting object ids and mutates nothing. Its exit status also predicts a
conflict *before* anything is touched, so the preview can say which branch will
conflict and that applying will take over the working tree. That is informed
consent, and it is only possible because replay has no side effects.

**Apply, no conflict predicted** uses `git replay`. Nothing is checked out,
HEAD never moves, and the existing safety story is fully preserved. If HEAD is
on a rewritten branch and the new tree differs, the index is left stale — git
does not resync it — so a `git reset --keep` follows.

**Apply, conflict predicted** uses `git rebase --update-refs` **in the user's
own worktree**. This is a deliberate renegotiation of the no-checkout
invariant: resolving a conflict requires a working tree the user can edit with
their own tools, and that is their checkout. The alternative was rejected on
evidence, below.

## Why not a dedicated worktree

Running the rebase in a private worktree keeps the user's checkout usable, and
it does work — a separate process can resolve and `git rebase --continue`
there. It was rejected for two reasons.

The user is normally standing on a branch in the stack, and Git refuses to
check out a branch that is checked out elsewhere. The obvious `--detach`
workaround **silently corrupts the stack**: `--update-refs` moves the
intermediate branches but leaves the detached tip behind, producing two
divergent copies of the same commit, with exit code 0 and no warning.

Even setting that aside, conflict markers in `.git/g2g/restack/` put resolution
in a directory the user did not choose, without their build tooling.

## Resumable state

`git rebase` already journals one invocation. `.git/rebase-merge/` holds the
todo list, `onto`, `orig-head`, `head-name`, and an `update-refs` file of
`(ref, old, new)` triples — and **`--abort` restores every branch that
invocation touched**, verified. Another process sees the interrupted state
through `git status`.

So gt2gh's journal only needs what spans *several* invocations, which is a tree
(one rebase per root-to-leaf path). At `$GIT_COMMON_DIR/g2g/restack.json`:

- the remaining queue of branches
- the `--onto` for the in-flight rebase
- the branch the user was on, to restore afterwards
- **every branch's tip at operation start**, so `--abort` can roll back paths
  that already completed — which git cannot do, because it only knows about the
  current invocation

Graphite solves the same problem the same way: `.gtcontinue` holds the queue,
the in-flight base and the branch to return to, and a start-of-operation
snapshot holds every branch's revision.

## Command shape

Git-native, because the operation underneath genuinely is a rebase:

```text
g2g restack [--branch <b>] [--scope path|subtree|graph] [--onto <ref>]
g2g restack --continue | --abort | --skip
```

`--onto` reparents and rewrites together. They cannot be separated: retargeting
the edge first discards the fork point the rewrite needs. Plain `restack` means
"the edge is right, the parent moved".

A later `--interactive` is a natural extension rather than a bolt-on, because
`git rebase -i --update-refs` already writes branch updates into the todo list
as `update-ref refs/heads/<branch>` lines. Reordering commits and moving a
branch boundary become the same gesture in one buffer. It does break the
non-interactive rule, so it must be an explicit opt-in; `submit --edit` is the
precedent.

## Consequences for the rest of the tool

Restack is gt2gh's **first resumable operation**. Everything else is one-shot.

- Every other command must refuse while a restack is in progress. Mid-restack a
  branch's ref may have moved while the graph still records its old parent, so
  `g2g graph` would render nonsense and `g2g push` would publish a half-rebased
  stack.
- The clean-worktree precondition changes meaning: still required before apply,
  but the tool may now leave the tree dirty on purpose.
- The graph edge update is applied only when the whole operation completes, and
  is rolled back by `--abort`.

## Remote interaction

Reading remote state must not disturb the user's. This is implemented; see
`RemoteTips` and `FetchIsolated` in `internal/git`.

Detection is free: `git ls-remote` writes nothing, and the pull request state
supplies the merged flag Git cannot. Objects come from a fetch into
`refs/g2g/remotes/`, which requires both `--refmap=` — a private destination
refspec alone does not stop git opportunistically updating the matching
remote-tracking ref — and `--no-write-fetch-head`.

The reason is not cosmetic. A bare `--force-with-lease` uses the
remote-tracking ref as its baseline, so refreshing it behind the user's back
disarms the check that stops a force push destroying work that landed in
between. Leases are now pinned to the tips the plan observed.

## Out of scope

Conflict resolution assistance beyond handing the user the conflict.
Reordering (until `--interactive`). Any rewrite of a branch gt2gh does not
record. Restacking onto a ref the user has not named, inferred or otherwise.
