# Undo

**Status:** implemented.

## Problem

A restack that stops part-way can be put back: `g2g restack --abort` restores
every branch and the recorded structure from the journal. A restack that
finishes cannot. The journal is deleted on success, and changing your mind
means finding each branch's old tip in Git's reflog, moving it back by hand,
and then repairing a record whose fork points were rewritten to match commits
the branches no longer have. Nothing says which branches moved.

The same is true of everything else that edits the record. `prune` forgets a
branch, `untrack` strands what sat on it, `fold` and `delete` rewrite and
remove: the graph file is overwritten in place and no earlier version is kept.

## What undo is

`g2g undo` puts back what the last local operation made from this worktree
changed:
the branches it moved, created or deleted, what the graph records, g2g's own
refs, and the checkouts it brought along. It is a preview until `--apply`,
like everything else.

```text
g2g undo            # what the last operation changed, and what undoing it does
g2g undo --apply
g2g undo --list     # recent operations, newest first
```

It is local. A push, a merged pull request or a retargeted base is not undone
and never will be by this command: reversing something other people have seen
is a different decision, made outward. An operation that published says so in
the log, and undo names what it leaves.

## What is recorded

One entry per completed mutation, appended to `g2g/operations.json` under the
Git common directory, for the commands that change only this repository:
`adopt`, `create`, `delete`, `fold`, `prune`, `pull`, `rename`, `restack`,
`track` and `untrack`. An entry holds:

- the command as it was run, when, and from which worktree;
- every ref it changed, with the commit before and after: local branches, and
  g2g's own refs under `refs/g2g/` other than the fetched copies of a remote
  (`refs/g2g/remotes/`), which are a record of the remote and not of anything
  done here. Sync points are among them, and have to be: a pull undone while
  its sync point still said this checkout agreed with the remote's newer tip
  would read the commits it put back as dropped here, and the next push would
  take them off the remote;
- the edges of the graph it changed, before and after, per branch, with the
  trunks and declarations when those changed;
- the tip of each branch whose record it changed, moved or not, since a
  record is only put back for the branch it was about;
- where the checkout stood before and after.

The entry is the difference between two readings, one before the mutation and
one after, which is what makes it the same code for every command and keeps a
command from forgetting to say something it did. The price is attribution: a
ref another session moved during those seconds is in the difference too. So
the difference is cut to what the operation says it touches: the footprint a
command gives the restack guard (`applyFlow.touches`), or for the reshaping
commands, which give none, the branches they name for the purpose
(`applyFlow.changes`). A branch that was created or deleted between the two
readings is always the operation's own. Within that, a branch that existed
before and after is recorded as moved only when the operation may move it: a
restack reads the trunk it replays onto, and a pull in another worktree that
advanced the trunk in those seconds is not the restack's to put back.

The log is shared by every worktree, and each may be somebody else's session,
so an undo offers only the operations made from the worktree it is run in.
`--list` shows the rest, marked.

Two things are deliberately outside it. A command that finds a branch level
with its remote records that agreement while it plans, before the first
reading; it stays true whatever is undone, because it names the commit the
branch goes back to. And an operation that stopped part-way is not recorded:
it has a journal, and `--abort` is its undo. A restack finished later with
`--continue` leaves no entry, so the branches it moved refuse the undo of
anything older, which is the safe answer and not yet the helpful one.

### Publications

`push`, `submit` and `land` are recorded too, as a marker and nothing else:
which branches were about to be sent, written before the publication so that
one that stops part-way is still marked. A marker is never offered to be
undone. It is there so that an undo of something earlier knows the remote has
seen those branches since, and refuses: putting them back here would leave the
remote holding what this checkout no longer had. A plain `git push` leaves no
marker, which cannot be helped from here.

## What undo checks

Undo is itself a mutation computed from a record, so it rests on something,
exactly as a snapshot does (`design-docs/snapshots.md`). All of this is asked
before anything moves:

- every ref the operation changed is where the operation left it, or already
  where it is going. A branch committed to since, or moved by another
  session, is not put back over that work, and the refusal names it;
- every branch whose record it changed is at the tip it had, every edge as
  it left it, and the branches recorded on each are the ones that were:
  something that leaves no entry here may have recorded a child since, which
  putting back the record would strand;
- none of those branches has been published since;
- no other worktree has one of them checked out, since putting it back would
  leave that worktree's files describing a commit its branch no longer has.
  Bringing a clean worktree along, as a restack does, is not done yet: switch
  it to another branch and undo again;
- a branch the operation created holds no commit that is on nothing else.
  `create -m` commits what was staged, and removing the branch would leave
  that work on no branch while reporting everything as it was. Keeping the
  commit under another name is the way through. A rename is not that: the
  same undo restores the old name at that commit;
- every commit a branch is to be put back at still exists;
- the working tree here is clean, when the undo moves the branch that is
  checked out or returns the checkout to where it stood;
- no restack is unfinished here, and none elsewhere overlaps.

Then the checkout returns to where it stood, the refs move, the record is put
back in the graph as it is now, and the entry is marked undone. The refs move
one at a time, each only from the commit the operation left it at; there is
no way to hand Git all of them at once through the process seam as it stands.
An undo that stops part-way says what it put back and exits `3`, and running
it again finishes it, because a ref already where it is going is left alone.

## What it does not do

- **Reach the remote**, as above.
- **Redo.** An undone entry is marked, and the next `g2g undo` offers the
  operation before it. Putting an undo back is done by running the command
  again.
- **Go back past what it cannot vouch for.** Entries are offered newest
  first. One whose branches have changed since refuses, and nothing older is
  offered until it is resolved or a newer operation is recorded.
- **Put back upstream configuration.** A branch restored after a delete, fold
  or rename comes back without the upstream it tracked, and the preview says
  so.
- **Bring back commits Git has collected.** A branch deleted by `prune
  --delete-branches` comes back while its commits exist, which by default is
  weeks, not forever. Entries older than two weeks are not offered, and at
  most fifty are kept.
