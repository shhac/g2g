# Land

**Status:** implemented in v0.30.0. The publishing decisions below were each
wrong first and were corrected by running the thing against real Git; where an
observation decided the design it is noted.

## Problem

A finished stack has to come down one branch at a time, and every step of doing
it by hand is a chance to do it wrong.

The constraint that shapes everything is the squash merge. When the bottom
branch merges, the trunk gains one commit equivalent to none of that branch's
commits, and every branch above still carries the originals. Git reconciles
that silently about half the time and conflicts the rest, so the person
merging learns nothing about which they are in until it happens.

Doing it correctly means: replay the survivors onto the advanced trunk between
merges, move each pull request's base before its turn, and forget each branch
as it goes. Doing it *quickly* means not waiting for CI to re-confirm what it
confirmed a minute ago, and not restarting it on branches whose turn has not
come.

`land` is that sequence, previewed before it runs.

## It owns no rules

Every refusal `land` needs already exists in a service that previews and is
already covered against real Git. So it decides the order and nothing else:

| Step | Service | What it brings that land must not reimplement |
|---|---|---|
| publish one branch | `push` | refuses a remote that has moved |
| advance and replay | `pull` | refuses a diverged trunk; the replay itself |
| has this landed | `prune` | `Cherry` then `Absorbed`, by content |
| the merge | `githubstack` | the only new external call |

This is `pull`'s own architecture, for the same reason it gives: *"It is an
orchestrator and owns no rules of its own."*

The first draft did reach past them — a direct `PushAtomic`, its own refusal
table, `restack --onto` for the reparenting. Each of those dropped a property
the service it bypassed already had. The rule is worth stating plainly: if
`land` is about to answer a question another command answers, it is wrong.

## The cycle

Bottom-to-top, per branch:

```
publish it                          ← push, which refuses a moved remote
wait: GitHub has seen the push
move the pull request's base        ← only if its turn has changed it
merge
wait: the merge has reached the base
advance the trunk, replay the rest  ← pull
reparent what sat on it, forget it  ← in that order
delete it, here and on the remote
```

**Only the branch about to merge is published.** Pushing the whole stack after
every merge restarts the checks on every branch above it, and that cost is the
whole reason to have this rather than `gt merge`. The consequence is worth
saying out loud: between cycles the branches above are ahead of their pull
requests, and `g2g github status` reports `head✗` for them. That is the intended
state, not drift.

## Every branch is aimed at the trunk

Not at the branch below it. `githubstack.Along` answers the stacked question —
where should this pull request sit in a stack — which is right for
`github status` and wrong here, because by the time a branch's turn comes the branch below has
merged and been deleted. A pull request still pointing at it merges into
nothing and `gh` reports success.

So the base is checked immediately before every merge, and moved if it is
stale. GitHub does retarget a child when its base branch is deleted, but
asynchronously, and `--no-delete-remote` stops it happening at all. Relying on
it is relying on a race.

This is the one place `land` changes what a merge will do, which is otherwise
`github retarget`'s alone. It uses the same client method rather than growing its own,
and every base it would move is named in the preview.

## Reparent before forgetting

`prune` refuses to forget a branch something is still recorded under, which in a
merge-down is every branch but the last. Reparenting first leaves the landed
branch with no children, so the refusal never applies. If prune refuses anyway,
or does not find the branch's work in the trunk by content, the descent stops
there, before the branch's refs are deleted: carrying on once left the graph
recording a branch that no longer existed.

The child keeps **its own fork point** — the landed branch's old tip — which is
what keeps its replay range to its own commits. See
[restack.md](restack.md): children reparent onto the merged branch's recorded
parent, never onto "the trunk".

`restack --onto` looks like the tool for this and is not. It was a silent no-op
in exactly this shape, and flattened a subtree in the other; both were fixed
before `land` shipped, and `land` still does not use it. One `Graph.Adopt` per
landed branch is the whole structural act.

## The two waits

Neither waits for CI. Both are bounded by the mutation budget and nothing else,
and both exist because acting on a stale answer merges the wrong thing.

**After a push**, three facts and not one: `headRefOid` says GitHub has this
commit, `baseRefName` says the merge will go where it is meant to, and
`mergeable` says GitHub has finished deciding whether it can merge at all —
which it reports as `UNKNOWN` while it thinks. The first draft conflated the
first two, which read plausibly and answered a different question.

**After a merge**, `state == MERGED` and then ancestry: fetch the base and ask
whether the merge commit is in it. Never "the tip changed" — a colleague's
unrelated push changes it too, and acting on that fetches a base that does not
contain this merge and replays the branch above onto it. The merge commit comes
back from the same query that reports the merge, so this costs nothing extra.

This is the first polling anything in g2g does. It asks before it waits,
because the ordinary case is that GitHub already agrees.

## `--admin` is not an edge case

On any repository with required status checks, **`land` without `--admin` will
refuse at the second branch, every time.** Each branch above the first is
force-pushed by its own replay, which restarts the checks that were green when
the descent was planned, so GitHub reports it blocked.

That is inherent, not incidental. The preview says so before the first merge
rather than letting the run discover it after something has already landed.

For the same reason, whether a merge is sent with `--admin` is decided again when
the branch's turn comes, not taken from the plan. The first version took it from
the plan, where every branch above the first was still clean, and so merged the
blocked ones without the flag it had been given for exactly them. The plan's
forecast still marks those steps, so the recipe says `--admin` where it will be
needed. Since it is readiness, revalidation leaves it out: a pull request whose
checks pass between the preview and the apply is the same descent.

`--admin` also bypasses approvals, so an unapproved pull request is refused
under its own name rather than folded into the protection refusal. Someone
reaching for the flag to get past restarted checks should not silently also get
past a review nobody gave.

## Telling a replay from a reviewer

After a replay the remote holds commits the branch no longer has. That is
exactly the shape of a reviewer pushing a fix onto your branch — the
`friendly-fixer` scenario — and `push` refuses both, correctly, because from
the tips alone they are the same thing.

What separates them is whether the remote still holds **what the plan saw**.
`land` moves these refs itself and knows what it left there, so the tip is
recorded per branch at plan time and compared before each push. A remote that
has moved since is carrying work this descent has not seen, and it refuses.

That is a sharper question than the one `push` was answering, which is why its
own refusal is not consulted inside the cycle — it is consulted once, before
the descent begins, when nothing has been rewritten and its answer is exactly
right. The lease still guards the push itself.

## It is not resumable, and must not become so

`restack` stays the only resumable operation. `land` is re-entrant by
recomputation instead: a merged branch is detected by content and skipped, a
forgotten branch is gone from the graph, so rerunning continues from wherever
it stopped. A branch somebody else merged in the browser is detected the same
way: once any pull request in the stack reads merged, the trunk is fetched and
a branch whose work is in either version of it has landed.

A descent that stops part-way reports what landed rather than "not applied" —
those merges are done and staying done — and it reports it from what it
recorded as it went, making no call of its own, because the context it is
handed is the mutation budget and the usual reason for stopping is that budget
expiring.

A merge counts from the moment GitHub accepts it, not once its cycle finishes:
the wait after it or the replay above it can still fail, and neither undoes it.
A descent that stopped before merging or tidying anything has not stopped
part-way; it is "not applied", with the failure status, because exit `3` says
something was achieved.

## Cleanup cannot fail a descent

Landing is the act; tidying after it is not. A ref that will not delete is
untidiness, and turning it into a failure would misreport what happened and
strand every branch above. Each cleanup is reported and skipped.

A branch the remote deleted on merge has reached the state it was asked for, so
that is a success rather than a special case.

`gh pr merge --delete-branch` is never passed: it deletes the local branch too,
which would make publishing cleanup quietly remove the user's own work outside
this tool's guarded ref handling. The two deletions are separate acts and stay
separate, `--no-delete-remote` and `--no-delete-local`.

`--no-forget` is refused outright. A branch left recorded under one that has
merged and been deleted makes every later `status` and every later replay
measure against a structure that is not there.

Keeping the stack comments is the last act, once, on what remains above the
landed branches (`Plan.Above`) — not after every merge, which would edit every
comment once per branch for a map that is only true at the end. It is the last
line of the recipe, `--no-comment` skips it, and unlike the cleanups a failure
there does stop the run with `3`: the descent stands, and the comments still
need `g2g github comment --apply`. See [stack-comment.md](stack-comment.md).

## The preview is the recipe

A descent is a sequence, so the preview is the ordered list of commands a
person would run by hand to reach the same place — numbered, and every line
genuinely runnable. Driving it by hand is a first-class answer to "I do not
trust this yet", not a fallback.

The recipe and the work are built from the same steps, so they cannot come to
describe different things — the drift `push.pushArgs` exists to prevent, one
level up. A **refused** descent shows no recipe at all: there is no ordered set
of commands that reaches the end, and offering the ones decided before the
refusal invites running half of it.

## What only a real repository could show

`internal/cli/land_journey_test.go` runs the whole descent against a real bare
remote, with a `gh` that performs the squash merge itself rather than exiting
zero. An inert fake leaves the trunk unchanged, so the merge wait never
settles, the replay has nothing to replay onto, and the test passes having
proved nothing but argv construction.

It found four bugs that injected fakes structurally could not reach: `push`
cannot be asked for a single branch; `Step.Push` was decided before the replay
that invalidates it; a replayed branch is indistinguishable from a reviewed one
by tips alone; and `FetchIsolated` could not follow a rewritten branch at all,
which was a bug in `sync` that had nothing to do with landing.

## Out of scope

Waiting for CI — that is what `--admin` is instead of. Merge queues, which
`gh pr merge` turns into an auto-merge rather than a merge, so neither wait
would ever settle; they are refused. Landing a forked selection. Undoing a
descent: a merge cannot be taken back, and pretending otherwise would be the
one dishonest thing in here.
