# Worktrees

A rewrite moves a ref without checking anything out. When another worktree has
that branch checked out, Git updates the ref and leaves that worktree's index
and files describing the commit before, so its next `git status` reports
staged changes nobody made. For a long time the only answer g2g had was to
refuse.

Refusing was safe. It was also wrong for the commonest layout: a main checkout
sitting clean on the trunk, with the work in linked worktrees, which is how
agent worktrees are laid out. Every `pull` with anything to bring down
refused, and so did every `land` of more than one branch. The way out was to go
to the main checkout and switch it off the trunk by hand.

## The choice

`restack`, `pull` (including `--trunk-only`) and `land` take
`--worktrees follow|skip|refuse`:

| Value | Clean worktree | Worktree with changes |
|---|---|---|
| `follow` (default) | moved, and its files brought along | left where it is |
| `skip` | left where it is | left where it is |
| `refuse` | refused | refused |

**Clean** means `git status --porcelain` there is empty, untracked files
included. A worktree whose state cannot be read counts as having changes.
Moving a clean worktree to the new commit loses nothing, and it is exactly
what `git pull --ff-only` run there would do. A worktree with changes is
somebody's work in progress, and it is never touched.

**Left** is not a failure. The preview names every branch it will leave, why,
and the command that finishes it later: `g2g restack` run in that worktree, or
`g2g pull --trunk-only` there for a trunk. The run exits `0`, because it did
what the preview said it would. Someone who wanted all or nothing reads the
preview and doesn't apply it. A worktree that picks up changes between the
preview and the apply turns a follow into a leave, so the plan is no longer
the one previewed and the apply's revalidation refuses it.

**Following an idle agent is an accepted risk.** A worktree can be clean
between an agent's edits while the agent still holds what it read. Moving its
branch means the next edit lands on different content. The preview names the
worktree, and the person running g2g chose to move that branch.

## How a worktree is brought along

`git -C <path> read-tree -m -u <from> <to>` is the two-tree merge `git switch`
uses: it keeps anything changed since and refuses to overwrite it.
`SwitchTreeIn` refreshes that worktree's stat data first, so stale stat data
can't read as a change. Before each switch g2g checks that the worktree still
has that branch checked out. If it has switched since, bringing it along would
lay this branch's change over whatever it is on now.

- **`pull`'s own moves** (advancing the trunk, taking a branch's published
  version) move the tree first and then the ref, as `moveBranch` does for the
  checkout here. The tree is what can refuse, and refusing first leaves
  nothing having happened. A ref that then won't move puts the tree back.
- **A restack** has to move refs first, because the replay engine moves them.
  Each followed worktree is written into the restack journal, with the commit
  its index describes, before anything moves, and updated as each one is
  brought. A restack that dies in between is finished by `--continue`, and
  `--abort` brings each one back. A failure to bring one is reported as done,
  with the `git -C … read-tree` command that finishes it, and exits `3`.
- **A rewrite that has to rebase** (one whose preview conflicts, or which
  cannot be previewed) checks each branch out here, and Git won't check out a
  branch another worktree has. Following can't help, so it refuses and offers
  to run it from that worktree or with `--worktrees skip`.

## How a branch is left

A branch that is left gets no step. What sits on it is then measured against
where it actually is, not where it would have gone: in
`main < A < B (held) < C`, `main` advances, A replays, B stays, and C is
restacked onto B's current tip only if it is behind it. A later `g2g restack`
from B's worktree moves B and C together. Leaving a branch can only shrink the
set that moves, so one replan is enough.

Each command decides only the branches it moves itself. `pull` decides its own
moves first -- the trunk it advances, the branches it takes -- and the replay
after it takes those decisions as given rather than asking again, bringing a
followed worktree along a second time if it replays that branch too. Under
`refuse`, `pull` refuses its own moves with the replay's wording. It then
collects again with the left branches fixed. That way nothing above a left branch is planned as if the branch had
been taken. Three more rules follow from that:

- **A trunk left behind leaves every branch on it uncollected.** Each one's
  published version is built on the remote's trunk, and taking it onto the
  trunk here would drag that trunk's commits in as the branch's own. The pull
  then only restacks what is stale against the local trunk.
- **A left branch records no sync point.** It was not reconciled with the
  remote. Recording the remote's tip as agreed would make a reviewer's commit
  read as dropped here next time, and `push` would then erase it.
- **A commit that moved between a left branch and a taken one is refused.**
  Taking one side of the move alone would lose the commit or duplicate it. So
  is a `--take` that would replace a left branch: it was asked for by name,
  and quietly not doing it would read as done.

## `land`

A descent cannot leave part of itself behind. Every branch above the next merge
has to end up on the trunk that merge produced. So for `land`:

- A clean worktree under `follow` is no obstacle. Each step's pull brings it
  along. If the trunk is followed and the branch being deleted is the one
  checked out here, this checkout is detached at the trunk, because the trunk
  can't be checked out here while another worktree has it.
- A branch the descent deletes is always refused while another worktree has it
  checked out, because Git won't delete it.
- Anything the policy would leave refuses the descent, except one case: a
  single branch with nothing above it can land while the trunk stays behind.
  The merge is checked against the fetched trunk, as before. `refuse` refuses
  that case too.
- A worktree that picks up changes partway down stops the descent at that
  step, with what has merged standing, and the command exits `3`.

## Busy worktrees

A worktree partway through a rebase or a bisect is detached, and
`git worktree list` says so, but Git still treats the branch as in use and
refuses to move or delete it. g2g used to read only the branch lines, so a
rewrite could move a branch that somebody's rebase was about to write back.
g2g now reads the branch from the files Git keeps for that operation
(`rebase-merge/head-name`, `rebase-apply/head-name`, `BISECT_START`). Such a
branch is held and busy, and busy is never clean.

## Not covered

- `delete`, `fold` and `prune --delete-branches` still refuse a branch another
  worktree has. Git won't delete a checked-out branch, and fold's refusal
  stands. `rename` already works, because `git branch -m` moves the other
  worktree's HEAD.
- Two applies running at once in two worktrees still race on the graph file,
  and the last one to save wins. The atomic save keeps the file whole, and
  the preview and revalidation keep each command honest about what it saw.
