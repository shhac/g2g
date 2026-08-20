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

**remote history reverter.** The trunk is rewritten upstream. Neither side is an
ancestor of the other, so there is nothing to fast-forward. `sync` refuses and
touches nothing. *We would rather it offered a resolution: take the remote
trunk, keep the local stack. It does not.*

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

**both moved.** You have work the published version does not, and it has work
you do not. `sync` refuses: choosing between two versions of your own branch is
not something to do behind your back.

## Merges that land out of order

**middle merges first.** `main ← A ← B ← C` and B lands, carrying A with it.
C reaches the trunk with only its own work; A and B have nothing left and
`prune` offers to forget them. *They read as ordinary branches rather than as
finished ones — the graph only says "landed" for a branch that has drifted, and
a branch that collapsed onto the trunk sits exactly on it. Distinguishing that
from a branch nobody has committed to yet needs something the current state does
not carry.*

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
