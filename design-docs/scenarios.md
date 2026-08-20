# Scenarios

Names for the situations a person actually ends up in, so a bug report, a test,
and a design discussion can all say the same word and mean the same thing.

Each one is a journey rather than a command: it starts from what somebody did
and ends where they are either finished or told what to do next. They live as
tests in `internal/cli/journey_test.go`, against a real bare remote and a real
second clone standing in for a colleague. Everything there is real except
GitHub, which has no local stand-in.

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

**indecisive user.** Something conflicts mid-restack and you abandon it.
`restack --abort` puts every branch back exactly where it was, as though the
restack had never started.

## Somebody else moved the trunk

**multi-user.** Other branches land on the trunk while your stack is in flight,
by merge commit or by squash. `sync` fast-forwards the trunk and replays your
stack onto it.

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
`sync` discards commits, so the preview says so plainly.

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

**borrower.** Someone cherry-picked your commits into their branch and it landed
first. Your commits are in the trunk under different object ids. Replaying drops
them by content rather than applying them twice.

## Somebody else moved *your* branch

**friendly-fixer.** A reviewer pushes a fix straight onto a branch you own.
`sync` brings it down: it fetches the selection, not only the base, and
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
the published tip an ancestor of yours, which `sync` ignores: publishing is
`push`'s business.

`sync` refuses by default: choosing between two versions of your own
branch is not something to do behind your back. The refusal names the way
through rather than being a dead end.

`sync --take published` is that way through. It is the one path where `sync`
loses work that exists nowhere else, so the preview lists every commit it would
discard by name — a count would not be enough to decide on.

There is deliberately no `--take mine`. `sync` only ever moves toward this
checkout and `push` only ever moves toward the remote, so which side wins is
normally answered by which command you run; `push` already prints the
`git push --force-with-lease` line for the other direction. `--take` is an enum
rather than a boolean because the question has more answers than the one
implemented.

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
than offering to recreate it, and `graph` reports it as landed with `prune` as
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

## Timing

**the world moves between preview and apply.** A colleague publishes between
your two invocations. Revalidation refuses. *There is no way to say "I know, go
anyway", which may be worth adding.*

**one rejected branch stops the whole push.** `push --atomic` advances every
selected ref or none. One branch's lease failing leaves the others exactly where
they were.
