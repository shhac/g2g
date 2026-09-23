# Scenarios

Names for the situations a person actually ends up in, so a bug report, a test,
and a design discussion can all say the same word and mean the same thing.

Each one is a journey rather than a command: it starts from what somebody did
and ends where they are either finished or told what to do next. They live as
tests in `internal/cli/journey_test.go`, and the landing ones in
`internal/cli/land_journey_test.go`, against a real bare remote and a real
second clone standing in for a colleague. Everything there is real except
GitHub, which has no local stand-in.

The landing fake goes one step further and performs the squash merge itself, in
the real remote. It has to: a `gh` that exits zero without merging leaves the
trunk unchanged, so the wait after a merge never settles and the replay above it
has nothing to replay onto — and the test passes having proved nothing but argv
construction.

Where the recorded answer is not the answer we want, the entry says so.

## The cast

- **you** — the working clone. Commands run here.
- **the colleague** — a second clone of the same remote. How a branch moves
  without you doing anything.
- **the remote** — a bare repository. Never GitHub: pull requests are faked,
  refs are not.

## Solo

**simple.** You are the only person. The remote is behind you; you publish and
it matches. `push`.

**history reverter.** You decide your last commit was wrong and drop it
locally. It is already published, so the remote is ahead of you. `push`
refuses, because the alternative is rewinding published history on a guess —
and names `git push --force-with-lease`, which is what you meant. No g2g
command does that, so the preview has to say the one that does.

**where do I stand.** Before deciding whether to push or pull you want to know
which branches differ from the remote, without asking it. `status` answers from
the refs a fetch or a push last left: not on the remote, level, ahead, and
after a pull, replayed since pushed. A pull fetches into g2g's own refs and
leaves `origin/main` where it was, so read from `origin/main` alone the trunk
would claim to be ahead by everything just pulled; `status` takes whichever of
the two refs descends from the other, and the trunk reads level. The journey is
in `internal/cli/status_journey_test.go`.

**indecisive user.** Something conflicts mid-restack and you abandon it.
`restack --abort` puts every branch back exactly where it was, as though the
restack had never started.

## Somebody else moved the trunk

**multi-user.** Other branches land on the trunk while your stack is in flight,
by merge commit or by squash. `pull` fast-forwards the trunk and replays your
stack onto it, and `pull --prune` then forgets what landed.

**multi-user-conflict.** The same, but your work collides with what landed. The
trunk still advances, because it was going to either way; the replay stops and
says where. Half-applied is the honest state and the message says so rather
than "nothing happened".

**remote history reverter.** The trunk is rewritten upstream — a rebase or a
squash cleanup, force-pushed. Neither side is an ancestor of the other, so there
is nothing to fast-forward.

If everything the local trunk has is in the published one by content, nothing is
lost by taking theirs: the trunk is replaced and the stack is replayed onto it.
That is the same supersede rule the branch case uses, and it is the only place
`pull` discards commits, so the preview says so plainly.

If the published trunk does *not* have what this one has, it refuses. Choosing
which commits die is not a side effect.

**squashed parent.** Your parent was squash-merged, and it had more than one
commit. This is the commonest way a branch lands and the one `git cherry` cannot
see: a squash combines the commits into one, so that commit is content-equivalent
to *none* of them, every one reads as new, and each is offered to the rewrite
engine individually — where it conflicts with the squashed version of itself.

`git merge-tree --write-tree` answers of the whole branch what cherry answers per
commit: merge it into the trunk and get the trunk's own tree back, and it has
contributed nothing however it arrived. The parent then collapses instead of
replaying, so the child's range starts above it.

Found by landing this repository's own stack. Its two-commit branch conflicted;
its one-commit branch did not, because a squash of one commit *is* equivalent to
that commit.

Afterwards the parent has nothing of its own and `prune` offers to forget it.
The child still recorded under it already sits on the trunk, because the pull
replayed it there, and Git shows that: the trunk is an ancestor of the child. So
prune records the child on the trunk — the same check and fork point `track`
would use — rather than refusing, and `pull --prune` does the whole thing in
one command. Where Git does not show it, as when the trunk was advanced by hand
and the stack not replayed, prune still refuses rather than reparenting around
the gap, and names `g2g pull --prune` first and then
`g2g track --branch <child> --parent <trunk>` for each child. It used to offer
widening the selection, which brings the child in, finds its own work, and
refuses again.

**borrower.** Someone cherry-picked your commits into their branch and it landed
first. Your commits are in the trunk under different object ids. Replaying drops
them by content rather than applying them twice.

## Somebody else moved *your* branch

**friendly-fixer.** A reviewer pushes a fix straight onto a branch you own.
`pull` brings it down: it fetches the selection, not only the base, and
fast-forwards a branch whose published version is ahead.

**extra-friendly-fixer.** The same, except they rebased your branch too, so the
published version shares no commit ids with yours. Nothing of yours is missing
from it by content, which is what makes it still yours, so theirs supersedes.
That is a reset rather than a fast-forward, and the plan names the two
differently because one replaces what you have and the other adds to it.

**both moved.** Each side holds commits the other does not. The message says
exactly that, with the count on both sides, because "you have work the remote
does not" is true of every ordinary commit and a reader who has just made one
cannot otherwise tell whether that is what it means. An ordinary commit leaves
the published tip an ancestor of yours, which `pull` ignores: publishing is
`push`'s business.

`pull` refuses by default: choosing between two versions of your own
branch is not something to do behind your back. The refusal names the way
through rather than being a dead end, and names it for the same selection —
`--branch`, `--scope` and a widened `--through` carried over — because the bare
command selects something else.

`pull --take published` is that way through. It is the one path where `pull`
loses work that exists nowhere else, so the preview lists every commit it would
discard by name — a count would not be enough to decide on.

`--through <branch>` bounds it: `--take` only ever changes the outcome for a
genuinely diverged branch, so unbounded it takes every one of them, including
branches you were not thinking about. The boundary is the named branch and what
it is stacked on — ancestry, not position — so where the stack forks, a sibling
of the boundary is not below it and a divergence there is still refused, as is
one above it.

There is deliberately no `--take mine`. `pull` only ever moves toward this
checkout and `push` only ever moves toward the remote, so which side wins is
normally answered by which command you run; `push` already prints the
`git push --force-with-lease` line for the other direction. `--take` is an enum
rather than a boolean because the question has more answers than the one
implemented.

**replayed, not yet published.** A pull replayed your stack onto a trunk that
moved, and you have not pushed. Every branch is now ahead of its published
version by content and beside it by commit id, and counted by id that reads as
both moved: the trunk's new commits are "here and not published", and once a
parent has been squashed its original commits are "published and not here".
`pull` asks whether everything the published version has is here — by commit,
then as a whole branch, which is what sees through a squash — and when it is,
this is unpublished work like any other and it leaves it to `push`. The second
sync of the day, and every `land` of three branches over a bottom branch with
more than one commit, refused until it did.

## Merges that land out of order

**middle merges first.** `main ← A ← B ← C` and B lands, carrying A with it.
C reaches the trunk with only its own work; A and B have nothing left, read as
"no commits of its own", and `prune` offers to forget them.

They are not called landed, deliberately. A branch that collapsed onto the trunk
and one nobody has committed to yet are byte-identical from the recorded state —
same tip as the parent, same fork point — so saying which it is would be a guess,
and the wrong guess invites someone to prune work they are about to start.
Saying what is true of both is not a guess.

**your branch was deleted after it merged.** You still have it locally with no
work of its own. `push` says "already in the trunk · nothing to publish" rather
than offering to recreate it, and `status` reports it as landed with `prune` as
the remedy. Absent from the remote has two meanings and they want opposite
answers.

## Your own stack

**self-conflict.** You fix a branch low in the stack and it collides with the
branches above. A forked selection is refused whole, naming `--scope path`; a
straight line stops on the conflict and waits.

**standing on the branch being rewritten.** The ordinary case, and the one that
broke three times: a ref moves and the index and working tree have to move with
it, or `git status` reports changes nobody made and the next `git switch`
refuses.

**a branch open in a second worktree.** A rewrite moves a ref without checking
anything out, so it would strand that worktree. It is refused, naming the branch
and the worktree.

## Taking a stack down

**merge-down.** The stack is finished and every pull request is green. `land`
takes it down bottom first: publish, merge, replay what is left onto the
advanced trunk, forget and delete, next. Only the branch about to merge is
published, so the checks on the branches above are restarted once each, when
their own turn comes, rather than after every merge.

**the base goes stale mid-descent.** B's pull request correctly sits on A. A
merges and is deleted, and until GitHub retargets B — asynchronously, and not
at all if the branch was kept — merging B puts its work into a branch that no
longer exists, and reports success. `land` checks the base immediately before
every merge and moves it, naming every move in the preview.

**your own replay looks like somebody else's commit.** After a branch is
replayed, the remote holds commits it no longer has. From the tips alone that
is `friendly-fixer` exactly, and `push` refuses both. `land` separates them by
remembering what the remote held when the descent was planned: it moved these
refs itself and knows what it left there.

**the checks restart under you.** Every branch above the first is force-pushed
by its own replay, so on a protected repository it reads blocked by the time
its turn comes. This is every run, not an edge case, and `--admin` is the
answer — said in the preview before the first merge rather than discovered
after one has landed.

**a descent stops part-way.** The branches below where it stopped are merged
and staying merged. It says which, and rerunning continues from there: a merged
branch is detected by content and skipped. Nothing is journaled, and `restack`
stays the only resumable operation.

**a branch landed while you were not looking.** Its work is already in the
trunk by content, so `land` skips the merge and does the cleanup only. The
question is asked of Git, never of the pull request: a squash merge lands the
work under a head the branch never had, so merged, closed and missing can all
describe a branch that is plainly finished.

## More than one trunk

**two-trunks.** A repository has `main` and `staging`, and work is stacked on
each independently. The forest already allows several roots, and every command
acts relative to the base of the stack it selected, so a stack on `staging`
pulls, restacks and lands onto `staging` without seeing `main`. *Starting the
first stack there is the gap: `create` refuses a parent that is neither a
recorded trunk nor the default branch, so the first branch has to be made with
`git switch -c` and recorded with `track --parent staging`. Declaring a trunk
is the missing primitive.*

**landing-branch.** `main ← feature ← a1 ← a2 ← a3`. The three small branches
are reviewed and squash-merged into `feature` one at a time, and `feature`
reaches `main` later, as a whole, by a merge that keeps those three commits.
Today `feature` is an ordinary branch on `main`, so the stack's base is `main`
and `land` from `a3` would merge `feature` into `main` first — the one thing
this shape exists to avoid. *The wanted answer is a trunk that still records
where it goes: `feature` is declared a trunk, so it bounds the stack above it,
is fast-forwarded and merged into but never replayed, and lands onto `main`
with a merge method that is not a squash. See
[declared trunks](declared-trunks.md).*

**stranded stack.** A middle branch is untracked, so what sits on it has a
recorded parent that nothing records. `doctor` names `track` for the stranded
branch, and naming the parent it already has roots the stack there: that parent
becomes a trunk. `adopt --trunk` with the same branch does the same. Both used
to find the edge already written and report nothing to do, so no command led
back out.

**someone rewrote the trunk to remove something.** A colleague drops a commit
from the published trunk — a secret, say — and force-pushes. The local trunk
has content the published one does not, so `pull` refuses, and
`pull --take published` replaces it and names what it drops. Only each
branch's own commits are replayed, so the removed commit does not come back
with the stack. Afterwards g2g's own refs hold it only where the remote still
does: a stack branch published before the removal carries it until the replayed
stack is pushed. Your `origin/<trunk>` still reaches it, because g2g never moves
a remote-tracking ref; `git fetch` does.

## Timing

**the world moves between preview and apply.** A colleague publishes between
your two invocations. Revalidation refuses. *There is no way to say "I know, go
anyway", which may be worth adding.*

**one rejected branch stops the whole push.** `push --atomic` advances every
selected ref or none. One branch's lease failing leaves the others exactly where
they were.
