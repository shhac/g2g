# Sync points

A sync point is where this clone and a remote last agreed on a branch. `pull`
and `push` read it to tell a commit somebody **dropped** from one nobody **had
yet**, which content alone cannot do: both are a commit on one side and not the
other.

## Why

Two people share a stack. Bob drops a commit `x` from B and publishes that.
Alice still has `x`. Without a sync point her `pull` sees "a commit here the
remote does not have" — the same thing it sees for any commit she makes — and
keeps it, and her next `push` puts `x` back. The drop survives only until
anybody who still has the commit pushes.

`git pull --rebase` has the same problem and solves it the same way: its
fork-point mode remembers what the remote-tracking ref used to hold. g2g cannot
use that ref, because `pull` deliberately never moves it, so it keeps its own.

## The record

`refs/g2g/synced/<remote>/<branch>` points at the remote's tip for the branch
at the last agreement. It is written with a reflog, and each entry's message is
`g2g <command> local=<sha>[ dropped=<sha>,…]`: the branch's own tip at that
moment, and anything the command dropped from it. Like `refs/g2g/remotes/`,
these are g2g's refs, never the user's.

The reflog is the recovery story. Every earlier agreement — and so every commit
a later pull or push dropped — stays reachable until git expires the entry.
An entry no longer reachable from the current tip expires on
`gc.reflogExpireUnreachable`, 30 days by default, not 90.

## When one is recorded

| Command | Records |
|---|---|
| `push` (and `submit`, `land`) | each branch it pushed, at the pushed tip, after the atomic push succeeds |
| `push` preview | each branch the remote already holds exactly |
| `pull` (and `land`, `pull --prune`) | each selected branch the remote holds, at the tip the plan saw, after the apply finishes |
| `pull` preview | each branch level with or ahead of the remote — it holds everything the remote has, so the agreement is a fact, like the fetch the preview already makes |
| `adopt`, `github adopt` | each adopted branch exactly level with the remote as last fetched |

Recording the sha the plan saw, never one read again at write time, is
deliberate: another worktree's pull may have fetched past it in between, and a
sync point holding commits this clone never had would call them dropped here.

A recording that fails after the command's own work is a diagnostic, not a
failure: the push or pull has happened, and until the next agreement records it
the cautious reading applies.

Deleting a branch through g2g forgets its sync points; renaming one carries
them. That lives in the Git client's own delete and rename, so no command that
deletes a branch can forget to.

A trunk never has one. What `pull` does to a trunk is decided by the rules for a
rewritten trunk, and a drop there is nobody's to publish.

## Is it this branch's?

A sync point is believed only when the local tip it recorded is in the branch's
history or its reflog. A branch deleted outside g2g and made again under the same
name has neither, so its old sync point says nothing about it. Not believing one
is always safe: with no sync point every difference reads as new work on its own
side, which is exactly how `pull` and `push` behaved before there were any.

## Classifying a branch

`syncpoint.Classify` takes the branch here, on the remote, and at the sync
point:

- **Which commits differ** is asked by content (`git cherry`), each side bounded
  at its own parent: the parent here for this clone's side, the parent as the
  remote holds it for the remote's. The parent's commits stay the parent's.
- **Why each differs** is asked of the sync point **by commit id**:

| Here | Sync point | Remote | Meaning |
|---|---|---|---|
| ✓ | ✗ | ✗ | mine — never published |
| ✗ | ✗ | ✓ | new — somebody else's |
| ✓ | ✓ | ✗ | dropped upstream |
| ✗ | ✓ | ✓ | dropped here |

By id, never by content, because content is the dangerous direction. A change,
its revert, and the same change made again have the content of the first; asked
by content, the re-made one would read as dropped, and `push` would delete it
from the remote. By id a rewritten commit reads as new, which keeps it or
refuses — never deletes it.

### A reset to a stale tracking ref

`pull` fetches into `refs/g2g/remotes/` and leaves `refs/remotes/<remote>/B`
where it was, so after a pull that brought commits down the tracking ref is
older than the sync point. `git reset --hard origin/B` then leaves out exactly
what the pull brought, which looks like a deliberate drop. The test is what the
branch added since that ref: a drop made on purpose (`git rebase -i`) keeps the
rest of what the sync point had beyond the ref, under new ids; a reset to the
ref keeps none of it. So when the branch sits on the tracking ref, every commit
that would read as dropped here is beyond that ref in the sync point, and the
branch has added nothing of the sync point's content since the ref, they are
counted as new instead and the classification says so. It is asked by content,
because somebody may have rewritten the branch before the pull, which leaves the
tracking ref no ancestor of the sync point at all — the first version of this
rule asked for that ancestry and so missed exactly that case. A deliberate drop
and that reset look the same, and only one of them is safe to publish.

### A branch emptied

A branch none of whose own commits are left on one side has not had a commit
dropped from it: it was emptied, or made again under the name. `pull` refuses
the remote's emptied version rather than taking every commit off the branch
here, and `push` refuses to publish this clone's, falling back to the
comparison it had before sync points, which names the replacement for whoever
means it.

## What pull and push do with it

See `README.md`'s `pull` and `push` sections for the behaviour and
`design-docs/scenarios.md` (**dropped upstream**, **dropped here**, **moved
across a boundary**) for the journeys. In one line each: `pull` drops what was
dropped upstream, listing every commit; `push` publishes what was dropped here,
listing every commit; `--keep <commit>` on `pull` keeps either kind; `--strict`
refuses any drop at all.
