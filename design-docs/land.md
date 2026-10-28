# Land

**Status:** direct descent introduced in v0.30.0; merge queue support added by
[the feedback plan](feedback-and-merge-queues.md). The publishing decisions were each
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

The shared GitHub client remembers PR observations for offline status. A
successful merge request is recorded as pending confirmation; the existing
merge-state reads record `MERGED` only when GitHub reports it. This adds no
network calls and survives graph pruning. See [PR observations](pr-observations.md).

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
delete it, here and on the remote   ← the remote leased on the head that landed
```

**Only the branch about to merge is published.** Pushing the whole stack after
every merge restarts the checks on every branch above it, and that cost is the
whole reason to have this rather than `gt merge`. The consequence is worth
saying out loud: between cycles the branches above are ahead of their pull
requests, and `g2g github status` reports `head✗` for them. That is the intended
state, not drift.

**What is left above is published once, when the descent is over**
(`Plan.Republish`): every branch above the last one landed that was already on
the remote, parents first, and before the stack comments are kept. Leaving it
was once deliberate — its checks are not this descent's to restart — and was
wrong. GitHub moves that pull request onto the trunk when the branch below is
deleted, so what it goes on showing is a version built on a branch that has
merged and gone, carrying commits the trunk now holds as a squash: checks
against code that will never merge, and a diff full of work already landed.
Each is pinned to the tip the plan saw, exactly as a step's own publish is, so
a reviewer's push above stops the run part-way rather than being overwritten. A
branch never published stays that way.

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

And a race `land` can lose for good: it deletes a landed branch with a push,
and GitHub closes a pull request whose base was deleted that way. A closed one
cannot be retargeted, nor reopened once its base is gone, so the only repair is
a new pull request. Waiting for a branch's own turn to move it is too late for
the next step up, whose base is deleted in the cleanup below it, and a branch
above a `--scope path` descent never gets a turn at all. So every pull request
on a branch about to be deleted is moved first, in that branch's cleanup,
before the deletion (`Plan.moves`, `rehome.go`): a later step's, which its own
merge then has nothing left to move, and a survivor outside the selection
(`Plan.Survivors`, read in the request that already protects queued
survivors). It runs before the branch is forgotten: a move GitHub refuses
keeps the branch, on the remote and in the graph, and stops part-way naming
the `gh pr edit` for each, so landing again finds the branch landed and
finishes its cleanup. Forgotten first, a rerun had nothing left to tidy. Only pull requests on recorded branches are found; one opened on a
landed branch the graph does not record is still at risk.

This is the one place `land` changes what a merge will do, which is otherwise
`github retarget`'s alone. It uses the same client method rather than growing its own,
and every base it would move is named in the preview. That method sends
`updatePullRequest` addressed by the node id the pull request read returned;
`github retarget` batches its changes into one request, and `land` sends a
batch of one, because each base is only right once the merge below it has
happened. The recipe still says `gh pr edit`, which is what a person would run.

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
before `land` shipped, and `land` still does not use it. One `Graph.Lift` per
landed branch is the whole structural act: the first half of `Graph.Remove`,
which is where the rule for a branch that is going lives, keeping the landed
branch's edge for `prune` to forget.

## The two waits

For direct merges neither propagation wait waits for CI. Both are bounded by
the mutation budget and exist because acting on a stale answer merges the wrong
thing. Queue landing has a distinct pending outcome and optional end-to-end
wait, described below.

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

GitHub's explicit "Base branch was modified" refusal is retried twice at most.
Each retry waits, asks GitHub to finish recomputing mergeability, and rechecks
the pull request's identity, head, base, approval and protection. All merges
pass `--match-head-commit` for the head published by this descent. A changed
head cannot slip into the irreversible step after the check. Other failures
are not retried, since a transport failure may already have applied the merge.
Exhaustion preserves the external diagnostic and names rerunning `g2g land`.

## `--admin` is not an edge case

For direct merges with required status checks, **`land` without `--admin` will
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
recorded as it went. The one thing it asks is whether the replay above a merge
was left journaled, and on which files, so it can say how to finish it; that
is asked on a short context of its own that `applyFlow` gives every stop
report, because the usual reason for stopping is the mutation budget expiring.
How to finish a replay is said once, for restack, land and pull alike.

A merge counts once GitHub reports MERGED, even if ancestry propagation or
the replay above it subsequently fails. Accepting a request alone is recorded
as a request, never as merged. A live queue request ends pending with exit `3`,
including an invocation that only observed an existing request. Other stops
exit `3` when they published, retargeted, requested a merge, merged or tidied
anything; an ordinary refusal that achieved nothing exits `2`.

## Uncommitted work

Most of a descent never touches the checkout: the merge happens on GitHub, a
replay that applies cleanly moves refs with `git replay`, and deleting a branch
that is not checked out needs nothing from the working tree. So uncommitted
work is refused only where it would be touched, and the refusal says which:

- **Up front**, when the branch checked out here is one the descent moves — the
  trunk it advances, a branch it replays, or the branch it deletes, which it
  would first have to switch away from.
- **Before each replay**, when the replay's own preview found a conflict (or
  could not be previewed). That is resolved by a rebase in this working tree,
  which would mix the conflict into somebody's changes. The descent stops
  part-way with what merged standing and no rebase started, naming the way
  through: commit, `g2g pull --apply`, rerun `g2g land`, which skips what has
  already landed.

It used to refuse any dirty tree, which stopped someone landing a lone branch
while another held work in progress — and stashing is no answer when the stash
is shared with other worktrees.

## A trunk held in another worktree

A clean worktree holding the trunk, or a branch the descent replays, is no
obstacle: each step's pull brings it along (`design-docs/worktrees.md`), and
this checkout is detached at the trunk before a branch it stands on is
deleted, since the trunk cannot be checked out here. What follows is the case
where it cannot be moved: the worktree has changes, or `--worktrees skip`.

A single-branch descent with no recorded children can leave a trunk held in
another worktree behind. It fetches the merge into g2g's private refs, checks
the branch's own content there through `landed.Into`, and forgets it through
the graph's untrack service. The checked-out trunk's ref, index and tree never
move. Deleting the current branch detaches this checkout onto the fetched
merge, which the preview says explicitly. Keeping the local branch leaves
the checkout on it. Any descent with survivors still refuses the held trunk:
those branches need the ordinary advance and replay, and skipping it would
leave the stack carrying its parents' landed work.

## Cleanup cannot fail a descent

Landing is the act; tidying after it is not. A ref that will not delete is
untidiness, and turning it into a failure would misreport what happened and
strand every branch above. Each cleanup is reported and skipped.

A branch the remote deleted on merge has reached the state it was asked for, so
that is a success rather than a special case.

The remote deletion is leased, as every other remote write is, and the lease
names **the head that landed**, never the remote's own tip. A reviewer can push
to a branch after its merge -- while the descent waits, replays and cleans up,
or before a rerun that only cleans up -- and that commit exists on the remote
alone. The deletion was once a bare `git push --delete`, which took it with the
branch. Now it is `git push --force-with-lease=refs/heads/<b>:<landed>
<remote> --delete refs/heads/<b>`, where the landed head is:

- for a merge this run made, directly or through a queue, the head the merge
  was pinned to (`--match-head-commit`, or the queue's head check). That is the
  cycle's own publish, not the plan's head: every branch above the first is
  replayed before its turn.
- for a branch already landed (`Step.LandedHead`), the remote tip push's
  preflight read and found to hold nothing of the remote's own -- the branch
  exactly, an ancestor, a replayed version, or only commits this clone dropped
  since the two last agreed -- and otherwise the branch here, whose work was
  found in the trunk. The first is what an interrupted descent's rerun needs:
  its replay collapsed the branch here onto the trunk while the remote kept the
  merged head. A later read of the remote never replaces the tip the preflight
  checked. Whether Git can merge the remote tip into the trunk without change
  never decides: a reviewer's revert of squashed work passes that and exists on
  the remote alone (see `landed.Missing`). The head a merged pull request
  reports is not used either, since nothing here establishes that GitHub stops
  moving it.

A remote branch holding anything else -- moved on, or deleted and recreated --
is left where it is and reported like any other cleanup that could not finish.
So is one with no such evidence: untidy, never lossy. On the ordinary path the
post-merge commit rarely gets that far, because the advance takes it into the branch here and
prune then refuses to forget a branch carrying work the trunk lacks; the lease
is what holds where nothing pulls the branch, such as a single branch landed
past a trunk held in another worktree.

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

## Landing a declared trunk

A trunk declared to land into another branch is landed the same way, as a
stack of one on that branch, by the method it was declared with. Two things
differ and both are the record's, not rules of land's: only the base is
advanced afterwards, and forgetting it ends a declaration rather than dropping
an edge. It refuses while anything is still recorded on it. See
[declared-trunks.md](declared-trunks.md).

## A stack linked on GitHub

GitHub refuses `gh pr merge` on a pull request in one of its native stacks and
asks for its asynchronous merge endpoint instead, which merges everything below
the pull request in the stack at once. That is not a descent — nothing above
would be replayed between merges — so a linked stack is refused in the preview,
naming `g2g github unlink --branch <target> --stack-number <n>` for each stack
it meets. Unlinking leaves the pull requests and their bases as they are.

It used to be discovered at the first merge, and `submit` linked every stack it
opened, so no stack `submit` had published could be landed. `submit` now links
only with `--link`. The refusal is a gate rather than a rule: if GitHub comes
to accept the ordinary merge for a stacked pull request, removing it is the
whole change.

## GitHub merge queues

A successful request is not a confirmed merge. The former implementation
called a step landed after `gh pr merge` returned success, even if it had only
queued the PR and the propagation wait subsequently expired. Its claim that
queues were refused was not backed by queue detection.

Read `repository.mergeQueue(branch: $trunk)` alongside readiness: upper PRs
currently target feature branches, so their own `isMergeQueueEnabled` cannot
establish the eventual trunk's policy. Queue entry and auto-merge request state
are volatile readiness, separate from generic PR observations. After retargeting,
confirm the PR's base and queue policy before requesting a merge.

Read repository `autoMergeAllowed` alongside readiness. Use
`enablePullRequestAutoMerge` when enabled and `enqueuePullRequest` when disabled,
through the existing mutation adapter and pinned with `expectedHeadOid` in both
cases. Ruleset queues can exist without repository auto-merge; GitHub CLI
[issue #13398](https://github.com/cli/cli/issues/13398) confirms its catch-all
auto-merge request fails there. Unmet prerequisites in direct-enqueue mode are
reported as refusals rather than a promise to enqueue later. The queue controls its method, so direct-merge method permissions do
not gate this path. Explicit `--method`, `--admin`, and conflicting declared
methods are refused with queue-specific advice; this path never escalates a
BLOCKED state into a protection bypass.

Default `land` requests the next bottom PR and reports queued or waiting with
exit `3` if still pending; an immediately confirmed merge can continue. An existing request with the same head and base is observed without
another request. Observe before publishing or retargeting, refuse mismatches,
and refuse queued/auto-merge descendants that replay would move, including
survivors above the selected target. The default recipe ends at handoff; it
must not present unconditional deletion commands following enqueue.

`--wait` observes accepted requests under the landing budget; initial missing
approvals, conflicts and failing prerequisites still refuse. It continues only when GitHub
confirms MERGED and the merge commit is in the fetched trunk. Confirmed MERGED
is reported even if ancestry propagation later fails. A pending timeout stays
pending, never landed. Queue removal, closure and changed head/base stop with
the observed reason. Pending stops skip republishing and comments and carry a
scoped rerun command; land adds no journal. A rerun after a browser merge uses
the existing landed-content and cleanup paths.

Required-check diagnostics distinguish required pending contexts, required
failed/cancelled contexts and unidentified protection. Optional failures and
incomplete context pages cannot justify a stronger claim. Cancelled does not
establish a duplicate run or authorize automatically rerunning it.

## Out of scope

Waiting for CI on the direct-merge path. Third-party merge queues. Landing a
forked selection. Undoing a descent: a merge cannot be taken back, and
pretending otherwise would be dishonest.

Landing without replaying what sits above, so that a pull request keeps the
head its reviewers saw until a later merge. It was considered and turned down:
the branch above would merge still carrying the landed branch's original
commits, which a rebase merge cannot apply, and its pull request would show
work already in the trunk. A branch that does not depend on the one below it
is not stacked on it: start it from the trunk, or move it there with
`g2g restack --branch <b> --onto <trunk>`, one replay before review rather than
after it. Landing the other then leaves it alone.
