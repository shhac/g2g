# What the first real descents taught

`land` was first used on a real stack — eight branches, a repository with a few
hundred branches and six worktrees — and stopped part-way. The rest of that
stack was then landed by hand, with `git push`, `gh pr merge` and `gt sync`,
following the preview step for step. This records what those two runs showed,
in synthetic terms. The originals named real branches and pull requests and are
not kept; what matters is the lesson, and every lesson below is either fixed or
a rule the code now follows.

The stack here is `synthetic-1` … `synthetic-8` on `synthetic-main`.

## The preview was the plan

The manual finish was the preview, run by hand. Nothing in it turned out to be
wrong or missing: aiming each pull request at the trunk before its merge, going
bottom first with a replay between merges, and `--admin` on a protected
repository were all right. The preview also answered a question a person has to
ask before running it: with `--scope path` over the bottom five, the nested
`sync` takes `stack` scope, so the three branches above the selection are
replayed rather than stranded.

That is why "the preview is the recipe" is a design rule rather than a nicety.

## Two waits that look the same and are not

A descent waits on GitHub twice: for it to see a pushed tip, and for it to see a
merge. The first run gave up waiting for GitHub to see `synthetic-2` at its
replayed tip. The remote had never received that tip at all — the push had not
happened — so waiting longer could never have helped, and "gave up waiting for
GitHub" sent the reader to the wrong problem.

- Before blaming GitHub, check the remote holds the tip being waited for. "The
  push never landed" and "GitHub is lagging" are indistinguishable from the
  outside and want opposite responses.
- Immediately after a force-push, GitHub can report mergeability of the **old**
  head: `UNKNOWN`, or — the dangerous one — `CONFLICTING`, which looks like an
  actionable state and whose obvious response, rebasing again, is wrong. Both
  settled on the next poll. Mergeability means nothing until `headRefOid`
  equals the pushed tip; read the head first.

## An incomplete descent is not a success

The first run stopped with four of five branches unlanded and exited `0`. What
merged stays merged, so it is not a failure to roll back either. That is exit
status `3`: it did part of what was asked and stopped where a person has to act.

## Refusals must carry their way out

The first preview refused because the trunk was checked out in another worktree
— the ordinary layout of a checkout with several — and the sentence named the
mechanism and the way out. Under `--json`, though, `repair` was `null`, so a
consumer following the documented contract had nothing to act on. Every refusal
with a way out now carries it as structure.

The same refusal fired for a branch the rewrite would never move: restack does
not move the trunk, and only a caller that advances it (sync, land) should count
it.

## Discovery cost has to scale with the selection

Both `track --stack` and `land` ran out of the 45-second discovery budget, and
the error named two branches the command had never been asked about — whichever
ancestry question was in flight when time ran out. That was true and useless: it
read as a fault in two branches rather than as a ceiling. The walk was made
proportional to the selection, and a timeout now names the phase and the
ceiling that was in force.

## What Graphite does around a descent

- `gt sync` restacks and reports on every branch in the repository, so in a
  large checkout the stack being landed is buried under warnings about others.
  `g2g sync` takes the stack, which is why `SyncScopes` stops at `stack` and
  `trunk`.
- `gt sync` reports taking a branch "from remote" with no way to tell a safe
  sync from one that discards local work. Checking survival by content, not by
  commit id, was the only reliable check — which is how `sync` decides a
  divergence, and why `--take published` names every commit it would drop.
- After a partial descent, re-tracking the stack in Graphite (`g2g mirror`)
  restores the parent edges but not Graphite's record that each branch was
  submitted. Graphite then offers, defaulting to yes, to overwrite each local
  branch with the remote version — which after a replay is the pre-replay
  commit, and would orphan everything above it. Answer no; see
  [source-alignment.md](source-alignment.md).
