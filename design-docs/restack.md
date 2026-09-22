# Restack

**Status:** implemented in v0.10. Everything below was verified against real
Git on local repository pairs; the observed behaviour is noted where it decided
the design.

## Problem

When the bottom of a stack is squash-merged, GitHub retargets the children's
pull requests but their commits are untouched. A child still carries the
parent's pre-squash commits, so its merge base is unchanged and its pull
request shows the parent's changes a second time. Merging it then tries to
reapply work the trunk already has.

g2g currently repairs structure and not contents, so the stack rots on the
first merge. Restack is the operation that fixes the contents.

## What Git can and cannot tell us

**Git alone cannot detect a squash merge.** After squashing a two-commit branch,
`git cherry` marks both commits as unmerged, because the squash's patch id
matches neither. The trap is that it *does* detect the single-commit case, so a
Git-only heuristic passes every simple test and fails in production.

The merged state therefore comes from the pull request, not from Git. That is
not a convenience, it is the only reliable signal.

## The rebase range

The naive `git rebase <trunk> <child>` conflicts once the parent's commits
overlap the trunk's, because it tries to replay commits whose content is
already upstream in a different shape. It must be:

```sh
git rebase --onto <new parent tip> <fork point> <branch>
```

so only the branch's own commits are replayed. The fork point is the parent's
tip at the time the edge was recorded — see the storage note in
[g2g-owned-graphs.md](g2g-owned-graphs.md). This is why the fork point is
stored rather than derived: after a merged parent is deleted there is nothing
left to derive it from.

**Children reparent onto the merged branch's recorded parent**, never onto "the
trunk". Those coincide in a simple linear stack and diverge everywhere else —
a branch rooted on `release-2x` must go back to `release-2x`, and a stack with
an unmerged branch below the merged one reparents onto that branch. The rule
needs no notion of a trunk at all.

## Classifying a branch takes two checks, not one

The fork point detects *"my parent moved"*. It cannot detect *"I moved"*, and
relying on it alone reports a manually restacked branch as **aligned** — the
fork point still equals the parent's tip because the parent never moved.

Both probes are needed:

| fork point == parent tip | parent is ancestor of branch | state |
|---|---|---|
| yes | yes | aligned |
| no | yes | needs restack (the ordinary case) |
| yes | no | moved off its recorded parent |
| no | no | both moved |

### The guard

Before any rebase:

> the fork point must be an ancestor of the branch, or refuse.

This is a precondition, not a warning. When a branch has been manually
restacked the recorded fork point is no longer an ancestor, and the range
`forkPoint..branch` then silently widens to include the *new base's own
commits* — observed replaying a trunk commit back onto the trunk. The failure
is silent duplication, so it must be impossible rather than unlikely.

The remedy for every "our record is behind" state is the same single command:
`g2g track --branch <b> --parent <p>`, which re-records the parent and the fork
point.

### Keep the fork point reachable

The fork point is stored as a ref under `refs/g2g/forkpoints/`, not only as a
string in the graph file. Once a merged parent's branch is deleted its old tip
is unreachable and eligible for garbage collection — precisely the moment the
fork point becomes the only way to compute the range.

## Commits the parent dropped

When a parent is rewritten, the child still carries whatever the parent
dropped. The **orphan set** is `newParentTip..forkPoint`, and `git cherry`
separates two very different situations within it:

- `+` — genuinely dropped: no equivalent content in the parent
- `-` — rewritten: the same content exists in the parent under a new object id

| Where the commit was removed | Orphan set | Ambiguous |
|---|---|---|
| tip of the parent | just the dropped commit, all `+` | yes |
| tail or middle of the parent | the whole rewritten chain: one `+`, several `-` | no |

**Default is to drop.** Removing a commit is far more commonly what was meant
than moving it across a branch boundary, and dropping is the only correct
answer whenever any orphan is `-`: absorbing then hands the child stale copies
alongside the parent's rewritten ones. The `-` orphans need no special handling
in the drop path, since the rebase already skips them by patch id.

`--absorb` is offered **only when every orphan is `+`**, which in practice means
the commit was removed from the parent's tip. It is a metadata-only operation:
the parent's new tip is already an ancestor of the child, so nothing is
rewritten and only the fork point is re-recorded. The costly option is the
default one.

A preview must always name dropped orphans. Silently changing what a branch
contains is the one thing this command must never do.

## Restack can empty a branch

Rebase and replay both drop commits whose patch content is already upstream.
That is exactly what makes squash-merge cleanup work — a fully merged branch
correctly collapses onto its new base — but it means a restack can leave a
branch identical to its parent, whose pull request would then show no changes.
That is reported, not silent, and the suggested remedy is `g2g untrack`.

The same mechanism can drop a commit whose content coincidentally matches
something upstream. The preview counts what will be replayed and what will be
skipped, which is possible because `git replay --ref-action=print` produces the
result without mutating anything.

## Two engines

| | `git replay` | `git rebase --update-refs` |
|---|---|---|
| Touches worktree/HEAD | never | yes |
| Whole stack in one call | yes, atomically | yes, per linear path |
| Conflict | exit 1, **no ref moves**, no state to clean | stops, resumable |
| Resolvable | no `--continue` | `--continue` / `--abort` |
| Preview without mutating | `--ref-action=print` | — |

`git replay` is EXPERIMENTAL (Git 2.44+), so it is gated the same way the
Graphite CLI is.

Where it is unavailable there is no prediction at all, and that is reported as
such: "we could not look" and "we looked and it will conflict" lead a reader to
different actions, and only one of them would be true. The resumable engine is
correct on every version, so the cost is the conflict warning and the untouched
checkout, not correctness.

**A rewrite commits, so it needs an identity.** Both engines create commits,
and some platforms guess a committer from the system while others refuse. A
test fixture that sets one only in the environment its own helpers use does not
give it to the code under test, which spawns its own Git — so a rewrite
succeeds on a developer's machine and stops half-way on a runner, having
applied the patch and failed to record it. The identity belongs in the
repository's own configuration, where every invocation sees it.

That failure is worth knowing by shape: patch applied, changes staged, nothing
unmerged, operation stopped. It reads exactly like a conflict and is not one,
which is why an interrupted rewrite reports what it actually found rather than
assuming.

**The engines are driven differently because they model the work
differently.** Replay takes a root and everything above it at once and needs one
shared origin: the root's fork point. Rebase moves a single line of descent, so
each branch is rebased on its own, bottom-up, onto the parent it now has.
Handing rebase the whole chain and asking `--update-refs` to carry the
intermediate branches works on some versions and not others, and buys nothing
sequencing does not.

**A selection can have several roots**, and each is its own replay. The stacks
of a `--scope trunk` fork from the trunk at different points, and a subtree
whose root collapsed leaves each child as a root of its own. Given the first
root's origin and base, a second root's range widened to take in the trunk's
own commits and its child landed on the first root carrying a stale copy of its
parent. `--onto` over several roots is refused, one command per root offered in
its place; a caller's location moves every root.

**A caller may move branches before the rewrite runs**, and says where through
`Pending`: sync collects published versions of your branches, then replays. A
branch is measured where it will be, not where it is. Its recorded fork point
describes the version being replaced, and after a colleague restacked the stack
and published it, that point is still in the new version but below the trunk
commits it was replayed onto — so the range took them in and a stack that was
already right was rewritten again. A version already on its parent's new tip
begins there; one that still contains its recorded fork point, as a reviewer's
commit on top does, begins there; one that contains neither is refused rather
than given a range that holds somebody else's commits. The preview names such a
branch by the object it is about to be.

**A branch behind its parent** — the parent is being replayed too, and the
branch does not contain the version of it being replayed — gets a replay of its
own onto the parent's result. The engine keeps each commit on the replayed copy
of its own parent, so sharing the parent's replay put the branch back on the
commit it forked from. That happens when the parent gained commits after the
branch forked, and when a caller brings in a version of the parent the branch
never had. The first is predicted exactly, from the object the parent's preview
prints for its ref; a parent named by object prints nothing, so the second
cannot be predicted and says so, and takes the resumable engine, which rebases
each branch onto its parent as it then is.

Several replays are several invocations, and the engine's atomicity covers one.
An in-place rewrite therefore notes every tip it may move first and, on any
failure before the checkout is touched, puts them all back and says so; it is
journaled while it runs, so a process that dies part-way, or a restore that
fails, still leaves something `--abort` can undo. "Not applied" is never said
over refs that moved.

Two rebase flags exist only to stop behaviour depending on which Git is
installed. `--no-reapply-cherry-picks` drops a commit whose content is already
in the new base; older Git reapplies it. `--empty=drop` decides what happens
when a commit becomes empty as a result; older Git stops and waits, which
reads as a conflict but leaves no conflicted file and nothing to resolve — the
commit simply has nothing left to say.

**Neither engine is trusted with an already-upstream commit.** Whether a
rewrite drops one or reapplies it varies by version — `git replay` changed
between 2.54 and 2.55, and `git rebase` differs by backend — and it is exactly
the case a restack exists for, so relying on it would make correctness a
property of the user's Git.

Instead the plan asks, per branch, whether every commit it owns is already in
its new base (`git cherry <base> <branch> <forkPoint>`). A branch where nothing
is left **collapses**: its ref moves to the base and it is never handed to an
engine at all. Its children are then measured against where it landed. What
reaches an engine is only ever commits that genuinely have to be replayed, so
both produce the same result everywhere.

This also makes "which branches does this empty" exact and available from the
plan, rather than inferred from a preview that some versions cannot produce.

`--no-reapply-cherry-picks` is still passed to the rebase engine, as a second
line rather than the first.

**Preview** uses `git replay --ref-action=print`, which yields the exact
resulting object ids and mutates nothing. Its exit status also predicts a
conflict *before* anything is touched, so the preview can say which branch will
conflict and that applying will take over the working tree. That is informed
consent, and it is only possible because replay has no side effects.

**Apply, no conflict predicted** uses `git replay`. Nothing is checked out,
HEAD never moves, and the existing safety story is fully preserved. If HEAD is
on a rewritten branch and the new tree differs, the index is left stale — git
does not resync it — so a `git reset --keep` follows.

**Apply, conflict predicted** uses `git rebase --update-refs` **in the user's
own worktree**. This is a deliberate renegotiation of the no-checkout
invariant: resolving a conflict requires a working tree the user can edit with
their own tools, and that is their checkout. The alternative was rejected on
evidence, below.

Collapses run before either engine, and a collapse is a bare ref move. When it
moves the checked-out branch, the checkout is reconciled before the rebase
starts, on the first pass and on every resumed one: git reads an index that
still describes the old commit as uncommitted changes and refuses to begin.

## Why not a dedicated worktree

Running the rebase in a private worktree keeps the user's checkout usable, and
it does work — a separate process can resolve and `git rebase --continue`
there. It was rejected for two reasons.

The user is normally standing on a branch in the stack, and Git refuses to
check out a branch that is checked out elsewhere. The obvious `--detach`
workaround **silently corrupts the stack**: `--update-refs` moves the
intermediate branches but leaves the detached tip behind, producing two
divergent copies of the same commit, with exit code 0 and no warning.

Even setting that aside, conflict markers in `.git/g2g/restack/` put resolution
in a directory the user did not choose, without their build tooling.

## Resumable state

`git rebase` already journals one invocation. `.git/rebase-merge/` holds the
todo list, `onto`, `orig-head`, `head-name`, and an `update-refs` file of
`(ref, old, new)` triples — and **`--abort` restores every branch that
invocation touched**, verified. Another process sees the interrupted state
through `git status`.

So g2g's journal only needs what spans *several* invocations, which is a tree
(one rebase per root-to-leaf path). At `$GIT_COMMON_DIR/g2g/restack.json`:

- the `--onto` for the in-flight rebase
- the branch the user was on, to restore afterwards
- **every branch's tip at operation start**, so `--abort` can roll back paths
  that already completed — which git cannot do, because it only knows about the
  current invocation
- **the reparenting the operation intends**, because once the rewrite has moved
  a branch a fresh plan can no longer tell where it was headed: it reports the
  branch as moved off its parent, which is true and useless
- **every selected branch's recorded edge at operation start**, because a
  resumed pass records fork points and reparenting as it goes (planning against
  the old ones would read its own progress as drift), and an `--abort` that put
  back only the tips left each branch recorded as forking at a parent tip it no
  longer contained

Because the plan is recomputed, a resume can find it refused — a branch still
to be rewritten opened in another worktree meanwhile. That is not completion:
the journal stays and the refusal is reported, so the half-rewritten stack can
still be continued once the cause is gone, or aborted.

Restoring a tip is a bare ref move, so `--abort` brings the checkout along with
the branch it is standing on. A user who finished git's own rebase by hand is
left on a rewritten branch with no rebase in progress, and moving that branch's
ref back without its working tree reported the whole rewrite as staged changes.

The remaining queue is deliberately absent. It is re-derived from the refs on
every invocation, which is what makes a user's own `git rebase --continue` or
`--abort` change what work remains rather than something to detect.

Graphite solves the same problem the same way: `.gtcontinue` holds the queue,
the in-flight base and the branch to return to, and a start-of-operation
snapshot holds every branch's revision.

## Command shape

Git-native, because the operation underneath genuinely is a rebase:

```text
g2g restack [--branch <b>] [--scope path|subtree|graph] [--onto <ref>]
g2g restack --continue | --abort | --skip
```

`--onto` reparents and rewrites together. They cannot be separated: retargeting
the edge first discards the fork point the rewrite needs. Plain `restack` means
"the edge is right, the parent moved".

A later `--interactive` is a natural extension rather than a bolt-on, because
`git rebase -i --update-refs` already writes branch updates into the todo list
as `update-ref refs/heads/<branch>` lines. Reordering commits and moving a
branch boundary become the same gesture in one buffer. It does break the
non-interactive rule, so it must be an explicit opt-in; `submit --edit` is the
precedent.

## Consequences for the rest of the tool

Restack is g2g's **first resumable operation**. Everything else is one-shot.

- Every other command must refuse while a restack is in progress. Mid-restack a
  branch's ref may have moved while the graph still records its old parent, so
  `g2g graph` would render nonsense and `g2g push` would publish a half-rebased
  stack.
- The clean-worktree precondition changes meaning: still required before apply,
  but the tool may now leave the tree dirty on purpose.
- The graph edge update is applied only when the whole operation completes, and
  is rolled back by `--abort`.

## Remote interaction

Reading remote state must not disturb the user's. This is implemented; see
`RemoteTips` and `FetchIsolated` in `internal/git`.

Detection is free: `git ls-remote` writes nothing, and the pull request state
supplies the merged flag Git cannot. Objects come from a fetch into
`refs/g2g/remotes/`, which requires both `--refmap=` — a private destination
refspec alone does not stop git opportunistically updating the matching
remote-tracking ref — and `--no-write-fetch-head`.

The reason is not cosmetic. A bare `--force-with-lease` uses the
remote-tracking ref as its baseline, so refreshing it behind the user's back
disarms the check that stops a force push destroying work that landed in
between. Leases are now pinned to the tips the plan observed.

## Out of scope

Conflict resolution assistance beyond handing the user the conflict.
Reordering (until `--interactive`). Any rewrite of a branch g2g does not
record. Restacking onto a ref the user has not named, inferred or otherwise.
