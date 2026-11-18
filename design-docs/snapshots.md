# Snapshots

**Status:** implemented for `prune` and `restack`.

## Problem

A mutating command is run twice: once to look, once with `--apply`. Nothing
connects the two. The apply plans from scratch, plans a second time and refuses
unless the two answers agree, and then mutates. That comparison closes the few
seconds between planning and writing. It says nothing about the preview that
was read a minute earlier: if the repository changed in between, the apply
plans against the new state and carries on, and the only check is a person
reading both plans.

It also costs three planning passes for one mutation. On a repository of a
hundred stacks a prune's planning pass takes minutes, so the two an apply makes
to guard a window of seconds are most of what the command costs.

## What a snapshot is

`--snapshot <path>` on a preview writes the plan to a file, with what the plan
rests on. `--snapshot <path> --apply` reads the file, checks that what the plan
rests on is still so, and carries out that plan. It does not plan.

```text
g2g prune --snapshot synthetic.json            # plans once, writes the plan
g2g prune --snapshot synthetic.json --apply    # checks, then applies that plan
```

The plan applied is the plan that was read, which is the property the two
separate runs never had. The check is a handful of local reads whatever the
planning cost.

## What a plan rests on

The check is not "nothing in the repository changed". A repository several
sessions share is never still: a branch gains a commit in another worktree, a
fetch moves a remote's copy, somebody records a branch in another stack. None of
that bears on a prune of a stack nobody is touching, and a check that refused
over it would refuse almost every time on exactly the repositories where
planning is slow enough to want this.

So each command names what its plan rests on (`snapshot.Rests`), and the basis
is read from that:

- **Branches.** Each one's tip, the edge the graph records for it, every edge
  recorded *on* it, and the worktree that has it checked out. All compared
  exactly: a branch that moved, was recorded differently, gained a recorded
  child, or was checked out somewhere is not the branch the plan was made
  about.
- **Advancing branches.** A branch the plan needs only to still contain what
  it contained. Its recorded tip must be an ancestor of where it is now. A
  trunk a prune found branches landed in is one: what landed in it is still in
  it after it moves on, and refusing over a trunk that advanced would refuse
  over every colleague's merge.
- **Here.** Where this checkout stands, for a command whose plan depends on
  it: a restack, and a prune given no `--branch`, whose selection started from
  wherever it stood.

For a prune the exact branches are the ones it forgets, the children it
records again, and every branch recorded under those down to the trunk. Every
ancestor and not only the recorded parent, because a branch is judged against
the first ancestor that is still there: with the branch below it deleted, that
is one further down. When branches are deleted, the tips that justified it are
leased exactly, the trunk's among them, so they are exact here too. For a
restack they are every branch it records or replays, what each sits on, the
branch an `--onto` names and the branches it would strand; its trunk is exact,
because a replay is computed onto one commit.

Two things an apply needs are in neither the basis nor the plan's branches, and
a command asks for them itself (`still`): whether a worktree a restack brings
along is still clean, and whether a remote's copy named by `--onto` is still
where it was, since that is a commit in the plan and no branch.

The restack journals are not part of it. The guard every apply runs already
refuses a plan that another worktree's unfinished restack overlaps, and a
digest of every journal refused over a restack starting or finishing on a
stack that had nothing to do with this one.

Nothing else is read. A branch outside that set can change freely, and the
picture the preview drew of it goes stale, as any preview's does; what the
apply does is unaffected.

The basis is the repository **as the plan saw it**, not as it is when the file
is written. Planning takes minutes, and a branch that gains a commit after it
was judged landed must not be recorded as judged at its new tip. So the refs
and the record are read before planning, the basis is cut from that reading
once the plan says what it rests on, and the same things are read again
afterwards: if they differ the repository moved under the plan, and no
snapshot is written. An advancing branch may move between the two readings
without that being a difference, and the plan may have judged against any
commit up to the later one, so it is the later tip that is written down.

## What an apply checks

In order, and refusing at the first that fails:

1. The file was written by this version of g2g, for this command, and for the
   same request: the flags that shape a plan (`--branch`, `--scope`, the
   command's own options) must be the ones the snapshot was made with. A plan
   made for one selection is not applied to another because the path was
   reused.
2. The guard, as any apply: another worktree's unfinished restack that
   overlaps what this plan touches.
3. The basis, read again. A difference is named: which branch moved and
   between which commits, which record changed, which worktree took a branch.
4. The command's precheck, and whatever only an apply reads (`settle`).
5. Whether the plan may be applied at all (`blocked`).

Then the plan is rendered as ready to apply and carried out, as after any
revalidation. A snapshot is used once: applying it changes what it rests on, so
a second apply is refused by the basis.

## What is not snapshotted

- **Anything that reads GitHub.** `submit`, `land`, `github retarget` and the
  rest plan from pull request state, and no reading of local refs says that
  has not changed. They keep both reads.
- **`pull`.** Its plan rests on what the remote holds, read over the network
  while planning. It could be snapshotted against the refs that fetch wrote,
  and that is a change of its own.
- **A plan that is blocked or has nothing to do.** There is nothing to apply,
  so nothing is written, and the preview says so.

## The file

JSON, written atomically, readable only by its owner. It holds the format
version, the g2g version, the command, the request, the basis and the plan.
The plan is the service's own plan type encoded as it is, which is only sound
while every field of it is exported plain data: an unexported field would be
dropped in the file and come back zero, silently. A test walks each
snapshotted plan type and fails on a field that would not survive, and another
encodes and decodes representative plans and compares them with `Equal`.

A path is checked before anything is planned: its directory has to exist,
and a file already there has to be a snapshot. Naming a file that is not one is
far more likely a slip than a wish to lose it. A restack also refuses a path
inside the working tree that Git does not ignore, since the file would be the
change that refuses its own apply. A preview with nothing to apply removes a
snapshot left at the path, so the next apply does not read the one before.

It is not a security boundary. The file is the reader's own, and a plan edited
by hand is a plan they chose to apply. The services' own apply-time checks
(prune's leases on the tips it deletes, restack's journal) still run.

## What changed to make it sound

`prune.Apply` refused when the stored graph differed in any way from the one
the plan was made from. That was already a source of spurious refusals with
several sessions writing the record, and a snapshot made minutes earlier would
never pass it. It now compares what the prune rests on -- the edges of the
branches it forgets and records, and the children recorded under them -- and
applies its edits to the record as it is now.
