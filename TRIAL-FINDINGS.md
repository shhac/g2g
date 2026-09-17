# g2g land: first real trial

First use of `g2g land` against a live stack, 17 September 2026, g2g 0.30.0.
It merged the first branch correctly and then stopped part-way, waiting for
GitHub to observe a push that never took effect. Nothing was lost and nothing
needed manual repair, but the descent did not complete and the reason is not
visible from the output.

## What was being landed

Nine PRs in the Athletics Quebec project: an eight-branch stack on `main` plus
one standalone branch. The trial landed **Track A only** — the bottom five of
the eight — using `--scope path` with `--branch` naming Track A's top branch.

```
g2g land --branch paul/ex-1178-checkout-affiliation-registry \
         --scope path --admin --from g2g --timeout 600s --apply
```

The repository is a long-lived checkout with six worktrees and several hundred
branches. Every branch was published, current with its remote, and had a PR.

## What happened

`gh pr merge 23150 --squash` landed the bottom branch. The trunk advanced, the
stack above replayed, the g2g graph reparented `paul/ex-1173-affiliation-rules-package`
onto `main`, and both copies of the landed branch were deleted. That half worked
exactly as previewed.

The descent then stopped:

```
Stopped part-way at paul/ex-1173-affiliation-rules-package: gave up waiting for
GitHub to see paul/ex-1173-affiliation-rules-package at 2f373bf after 66 attempts
Merged paul/ex-1172-athletics-quebec-enum, and they stay merged.
Rerun g2g land to see what is left.
```

State afterwards, confirmed against a snapshot taken before the run:

| Fact                         | Value                                                          |
| ---------------------------- | -------------------------------------------------------------- |
| PR 23150                     | merged, base `main`, remote branch gone                        |
| PR 23151                     | open, base already retargeted to `main`, head still `c52d29af` |
| `paul/ex-1173-...` local     | `2f373bf3356` (replayed)                                       |
| `paul/ex-1173-...` on origin | `c52d29afa94` (unchanged, pre-replay)                          |
| g2g graph                    | 8 branches, `paul/ex-1173-...` reparented under `main`         |
| Working tree                 | clean                                                          |

The exit code was `0`.

## Findings

### 1. The wait was for a push that had not taken effect

g2g waited 66 times for GitHub to report `paul/ex-1173-affiliation-rules-package`
at `2f373bf`. The remote is still at `c52d29afa94`, which is where it was before
the replay. So this was not GitHub lagging behind a completed push; the new tip
never reached the remote at all.

That makes the message misleading in a way that matters during an incident. It
reads as a GitHub propagation problem, and the operator's instinct is to wait
longer or raise the timeout. The actual question is why the push did not land,
and the output gives nothing to answer it with — no push step is echoed, no
error from `git push`, no indication of whether the push was attempted, refused
by its lease, or skipped.

**Suggestion.** Before waiting, confirm the remote actually holds the tip being
waited for, and say which of the two failed. If the push did not take, the
refusal should name that rather than reporting a wait timeout. A `--force-with-lease`
rejection in particular is a different problem with a different fix, and it is
invisible here.

### 2. Exit code 0 for an incomplete descent

The command stopped with four of five branches unlanded and exited `0`. The
human-readable output does say "Stopped part-way", so a person is informed, but
a script wrapping this cannot tell a complete descent from a partial one without
parsing prose.

This may be deliberate — the merges that happened are real and permanent, so the
run is not a failure in the sense of needing rollback. But "some of what you
asked for happened" is worth distinguishing from "all of it did", and an exit
code is the usual place.

### 3. Cleanup and reparenting behaved exactly as documented

Worth recording as a positive, because it is the part that would have been
expensive to get wrong. After the merge, the graph reparented the branch above
onto `main` rather than stranding it, both copies of the landed branch were
removed, and nothing above the merge point was disturbed. Re-running `g2g graph`
afterwards shows a coherent eight-branch stack. No manual repair was needed.

### 4. The worktree refusal is the best part of the command

The first preview refused:

> Apply blocked: checked out in another worktree: main (/Users/paul/projects/backend)
> · rewriting it there would leave that worktree describing a commit it no longer
> has · close it or narrow the selection

This is exactly right, it fired at preview time before anything was touched, and
the sentence explains both the mechanism and the way out. Moving the other
worktree off `main` cleared it and the second preview was complete.

### 5. `repair` is null on that refusal

The same blocked preview under `--json` gives a populated `blocked` string and
`"repair": null`. The project's own guidance is that a machine should read
`repair` rather than parse the sentence, and that a refusal "says why and what to
do as a `repair.Note`". Here the ways out exist only inside the prose.

An agent following the documented contract gets nothing actionable.

### 6. Default discovery timeout is too short for a large checkout

Both `g2g track --stack` and `g2g land` timed out during discovery at the 45s
default:

```
Apply blocked: git rev-list --left-right --count paul/sidequests...paul/crm-previews-with-sle-id
failed: context deadline exceeded
```

Note the branches named are unrelated to the selection — discovery walks
ancestry across branches the command was never asked about. `--timeout 300s`
resolved it every time.

Two thoughts. The refusal is honest and does not guess, which is right. But the
cost appears to scale with the repository rather than the selection, and the
branch pair in the message gives no hint that raising the timeout is the fix.

### 7. `land` previews are genuinely readable

The 28-step plan is the strongest argument for the command. Every `gh`, `git`
and nested `g2g` invocation is listed in execution order with a one-line reason,
including the retargets that only become necessary mid-descent. It was possible
to reason about what would happen to the _unselected_ Track B branches purely
from the preview plus `g2g sync --help` — the nested `sync` takes `stack` scope,
so the branches above the selection get replayed rather than stranded. That is a
real question a person needs answered before running this, and the preview
answered it.

## What was not tested

The descent stopped at the second branch, so the following are unexercised:

- More than one merge in a single run
- The mid-descent `gh pr edit --base main` retarget actually taking effect
  (23151 was retargeted, but its merge never ran)
- The final branch's cleanup path, which has no `sync` between merge and prune
- `prune` refusing to strand a branch recorded under a landed one — the case
  that would arise at step 26, where Track B still sits above the last Track A
  branch

## Environment

- g2g 0.30.0, installed as `g2g`
- Repository has six worktrees; `main` had to be moved off the root checkout
- Structure recorded natively via `g2g track --stack --trunk main` immediately
  before the trial, confirmed with `g2g status --from g2g`
- Graphite was also tracking these branches; g2g's own graph took precedence,
  as designed
