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
locally. It is already published, so the remote is ahead of you. Your own push
published it, so the sync point says you had it: that is your drop, and `push`
publishes it, naming the commit. Without a sync point — a branch published with
plain git — nothing says whose the remote's extra commit is, so `push` refuses,
because the alternative is rewinding published history on a guess, and names
`git push --force-with-lease`, which is what you meant.

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

**undoing-child.** `main ← A ← B`, where A adds a file and B's only commit
deletes it again. A is squash-merged. Merged into the trunk as a whole branch,
B changes nothing: over the distance from where the stack left the trunk the
addition and the deletion cancel, so B differs from the fork by less than A
does, and the squashed-parent check above answers that B has contributed
nothing. It has not landed. Its one commit is in nobody's trunk.

Read as landed, `pull` collapsed B onto the trunk with nothing on it, `land`
published it that way and GitHub closed the pull request it had just emptied,
`status` called it landed, `prune` offered to forget and delete it, and `push`
declined to publish it. A yes from the whole-branch merge is now believed only
if B's own commits, replayed from the parent they sit on, would change nothing
either; they would delete the file, so B replays onto the trunk carrying its
deletion. Every caller hands that question the boundaries it knows
(`landed.Whole`), so the answer survives A having been amended after B
forked, and a follow-up commit made after a pull request merged is measured
from the head that merged. Not yet covered: a branch recorded directly on the
trunk whose follow-up reverts its own merged work still reads as landed to
`prune` and to a replay. The journey is in `internal/cli/undoing_child_journey_test.go`,
against a real repository, because the question is what Git's merge makes of
it.

**borrower.** Someone cherry-picked your commits into their branch and it landed
first. Your commits are in the trunk under different object ids. Replaying drops
them by content rather than applying them twice.

**copied stack.** `main ← A1 ← B1` with more branches above, and the same
changes are opened again as `main ← A2 ← B2` under new commits — a second
author, or a stack recreated rather than restacked. A2 lands, then B2. Nothing
in the first stack names the second, and none of its commits are in the trunk.

Each restack of the first stack asks by content, so A1 collapses onto the trunk
once A2 has landed and B1 once B2 has; whatever sat on them replays with only
its own work, and restacking only once, after both, ends in the same place.
The preview names `g2g prune` for the collapsed branches, which forgets them
and records what sat on them on the trunk. It used to suggest `untrack`, which
would have stranded those branches. This depends on the copy being exact: a
review fix made only on A2 leaves A1 with work of its own, and replaying that
conflicts with A2's version of it.

The pull requests for A1 and B1 are still open on GitHub, and nothing here
closes them. Where A2 and B2 are recorded here too, `pull --prune` forgets them.

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

## Two people on one stack

**shared stack.** Alice publishes `main ← A ← B ← C`. Bob makes the branches
local and adopts the chain (`adopt --scope path`, or `github adopt --scope
path` from the pull requests), commits to B and restacks; Alice commits to A
and restacks. Whoever pushes second is refused, because their stack lacks the
other's commit, and `pull` then has to bring them back into step — in either
order — before a plain `push` succeeds. The journeys are in
`internal/cli/shared_stack_journey_test.go`.

Both orders used to stop there. A branch's published version was measured
against the parent *here*, so the commit Alice added to A and published read as
B's own — Bob's B was "both moved" and the only offer discarded his commit —
and when Bob published first, his B sat on the A Alice had since moved past,
so the replay could not tell which of its commits were B's. `pull` now
measures a published branch against its parent *as the remote holds it*, when
it is built on that, and replays from there. The parent's commits stay the
parent's in both directions.

A conflict between the two (both touch the same lines) stops the pull's replay
like any other, and `restack --continue` finishes it. The resolution rewrites
the other person's commit, which this clone pulled, so the sync point says it
was agreed on: `push` publishes the resolution as a rewrite, naming the commit
it replaces. A commit added or amended on the remote since still refuses.

**dropped upstream.** Bob drops a commit `x` from B and publishes that; Alice
still has it. Without a record of where Alice and the remote last agreed, her
`pull` reads `x` as her own unpublished work and her next `push` puts it back.
With one (`design-docs/sync-points.md`) `x` is a commit she saw on the remote
that the remote no longer has: `pull` drops it here too, names it, and records
it in the sync point's history, where it stays recoverable. `--keep x` keeps it
as hers instead. Alice's own commits on top of B refuse instead of choosing,
naming both ways.

**dropped here.** Alice drops `x` herself. The remote still has it, and `pull`
must not fast-forward it back: a branch reset back to drop a commit is an
ancestor of the published one, which is exactly what an ordinary fast-forward
looks for. `pull` leaves it, and says the drop is `push`'s to publish.

**moved across a boundary.** Bob moves A back one commit, so the commit at its
tip becomes B's, and publishes both. Alice's `pull` takes A without it and
leaves it in B, saying it moved rather than that it was dropped, and records
B's fork point where A's published tip is, so the next restack does not read
it as A's dropped work.

**reset to a stale tracking ref.** `pull` never moves `origin/B`, so after Alice
pulls Bob's commits it still names B without them, and
`git reset --hard origin/B` leaves out exactly what the pull brought. That
looks like a deliberate drop and is not safe to publish as one, so it is
counted as theirs: `pull` puts the commits back and says why.

## Merges that land out of order

**middle merges first.** `main ← A ← B ← C` and B lands, carrying A with it.
C reaches the trunk with only its own work; A and B have nothing left, read as
"no commits of its own", and `prune` offers to forget them.

They are not called landed, deliberately. A branch that collapsed onto the trunk
and one nobody has committed to yet are byte-identical from the recorded state —
same tip as the parent, same fork point — so saying which it is would be a guess,
and the wrong guess invites someone to prune work they are about to start.
Saying what is true of both is not a guess.

That holds only while the empty branch's tip is inside its parent. See *an
empty branch left behind*, below, for the one that is not.

**landed parent, not yet pruned.** `main ← A ← B`; A is squash-merged and you
`pull`, which collapses A and replays B, and you `submit` before pruning. A is
still recorded and its pull request is merged. `submit` leaves A out, `left
out · merged · landed in main`, asks for no spec entry for it, and names
`g2g prune`; B's open pull request keeps its base, and the preview names the
`g2g github retarget` that moves it once A is forgotten. It used to read A's
merged pull request as history to replace, say `create draft` about work it
had just confirmed in the trunk, and block B on a title for it. Opening a new
pull request for a branch that sits on A is refused before any push, naming
`g2g prune --branch A`: choosing another base there would be recording
structure. That is a prune of A's stack, because one of A alone forgets
nothing when the branch under it has landed too and is still recorded.

**your branch was deleted after it merged.** You still have it locally with no
work of its own. `push` says "already in the trunk · nothing to publish" rather
than offering to recreate it, and `status` reports it as landed with `prune` as
the remedy. Absent from the remote has two meanings and they want opposite
answers.

## Your own stack

**self-conflict.** You fix a branch low in the stack and it collides with the
branches above. The rewrite stops on the conflict and waits, and
`restack --continue` goes on to whatever is left -- in a stack that forks,
the other lines too. A forked selection used to be refused whole, naming
`--scope path` per line, and `pull` and `land` inherited the refusal: `land`
would not merge an approved pull request whose replay above it conflicted.

**standing on the branch being rewritten.** The ordinary case, and the one that
broke three times: a ref moves and the index and working tree have to move with
it, or `git status` reports changes nobody made and the next `git switch`
refuses.

**a branch open in a second worktree.** A rewrite moves a ref without checking
anything out, so it would strand that worktree. A clean one is brought along
with its branch. One with changes keeps its branch where it is, the rest of
the stack moves, and the report names the branch it left and the command that
finishes it there. `--worktrees refuse` refuses instead, naming the branch and
the worktree. `land` treats a branch above the descent the same way: an
agent's dirty branch above the one being landed is left unreplayed, recorded
on the trunk, and finished by a `pull` in its worktree, where it used to
refuse the descent under every policy. See `design-docs/worktrees.md`.

**somebody else's restack is stopped.** A session in another worktree has a
restack stopped on a conflict, on a stack with nothing in common with yours.
With a worktree per session that is nearly always true of some worktree.
`restack`, `pull`, `prune`, `push`, `submit`, `create`, `track`, `untrack`,
`adopt`, `github retarget` and `github comment` go ahead, and are refused only
when the stopped restack moves a branch they read or they move one it reads. A
`pull` that fast-forwards the trunk both stacks sit on goes ahead too: the
other session's `--continue` carries on to the new tip. `land` and the
reshaping commands still wait, and say it is because they cannot name what
they would touch, not because that restack holds their branches.

**an empty branch left behind.** `main ← A ← B`, with B just created, nothing
committed to it, and open in another worktree with changes. You restack A; B's
worktree is not clean, so B is left on the tip A used to have. B has no
commits of its own, which is also how a branch that collapsed onto its parent
reads, and `prune` forgot it as landed. That record was the only statement of
where B's work begins: the first commit made on B sat on A's old commits, and
recording B again could only measure from where it meets the rewritten A, so
the next restack offered A's old commits as B's own. `prune` now forgets an
empty branch only where its tip is inside its parent. B stays recorded and
reads as needing a restack, which moves it. The same holds when A was
squash-landed instead of rewritten: asked over the range above the trunk B
has commits, all of them A's and all in the squash, so it read as landed with
A, where the same branch with one commit was kept. Prune now asks B's own
record first and refuses as stranding B, and `pull --prune` exits `3`. Once
B's work is committed, its own restack lands it on the trunk with that commit
alone, and prune goes through. Recording any branch for the first
time under a parent it is not built on now names the range it writes down,
for the same reason.

**a merge inside your branch.** You merged another stack's branch, or your own
parent, into a branch with plain Git. Its own commits now include a merge
commit, and a replay would flatten it into the commits it merged. `restack`
refuses when planning, before anything moves, and names the way that keeps the
merge: `git merge <parent>` on the branch, then `g2g track --branch <branch>
--parent <parent>`. A branch that holds its parent's tip has nothing to
replay, so that track only records where it now forks, and a later restack
says it "already holds" its parent and rewrites nothing. It used to replay
such a branch onto the commit it already sat on, giving it and everything
above it new commit ids, and to stop on the merge.

**the rebase never started.** The replay conflicts, so it has to rebase in
your working tree, and Git cannot create its state directory: a read-only or
sandboxed Git directory. Git exits zero having moved nothing. The restack says
what Git printed and that nothing was replayed, and leaves no restack in
progress. It used to say "stopped part-way, with nothing left unmerged" after
two passes and leave a journal that refused every other command, under advice
to `--continue` a replay that would fail the same way.

**the trunk open in the main checkout.** The ordinary layout with agent
worktrees: the main checkout sits on the trunk and the work is elsewhere.
`pull` and `land` advance the trunk there too when it is clean, and leave it
when it is not, in which case `pull` takes nothing from the remote and only
restacks against the trunk as it is.

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
pulls, restacks and lands onto `staging` without seeing `main`. Starting the
first stack there takes one declaration: `g2g track --branch staging
--as-trunk --apply`, after which `create` accepts `staging` as a parent like any
branch the graph records. See [declared trunks](declared-trunks.md).

**landing-branch.** `main ← feature ← a1 ← a2 ← a3`. The three small branches
are reviewed and squash-merged into `feature` one at a time, and `feature`
reaches `main` later, as a whole, by a merge that keeps those three commits.
Recorded as an ordinary branch on `main`, `feature` would make `main` the
stack's base, and `land` from `a3` would merge `feature` into `main` first — the
one thing this shape exists to avoid. So `feature` is declared a trunk that
still records where it goes (`track --as-trunk --into main --by merge`): it
bounds the stack above it, is fast-forwarded and merged into but never
replayed, and lands onto `main` with a merge method that is not a squash. See
[declared trunks](declared-trunks.md).

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
your two invocations. Revalidation refuses. For a restack or a pull it names
the first fact that differs: which branch moved and between which commits,
which record another session edited, a worktree that took a branch. Sessions
sharing one repository refuse each other often, and without that the reader
went to a reflog to learn whether a colleague's fetch or a defect was the
cause. *There is no way to say "I know, go anyway", which may be worth
adding.*

**you changed your mind after it finished.** A restack applied cleanly, or a
prune forgot a branch, and that was not what you wanted. `g2g undo` puts back
what that operation changed here, and refuses if any of it has changed again
since, has been published, or is checked out in another worktree. *A restack
that stopped and was finished with `--continue` is not recorded, so it cannot
be undone this way and blocks the undo of anything before it.*

**the repository is bigger than the budget.** Working out what to do takes
longer than a planning pass is allowed. Every command, the read-only ones
included, says it timed out before anything was changed, names the ceiling in
force and how to raise it, and points at `--debug` for the call it was waiting
on. It used to surface as whatever the cut-off call was read as, such as a
branch "not a commit in this repository", a different branch each run.

**one rejected branch stops the whole push.** `push --atomic` advances every
selected ref or none. One branch's lease failing leaves the others exactly where
they were.
