# What a command costs

g2g reads three graphs at once: the forest it records, git's commit graph
underneath it, and the graph pull requests make on GitHub, each base pointing
at the branch below. Every command is a walk over one of them that asks
questions of another, and the cost that matters is almost never the walk. It is
what each step asks: a git process, a GitHub round trip, and — the one that
grows without anyone noticing — a git command whose own cost is the size of a
history rather than of a branch.

This records where that cost comes from, the patterns that keep it flat, and
what is still known to be expensive.

## The sizes

| Letter | What | Grows with |
|---|---|---|
| B | branches a command selects | the stack, or the repository for `--scope all` and `doctor` |
| L | local branches | how long the repository has been worked in |
| K | a branch's own commits | the branch |
| C | commits the trunk gained since a branch left it | time, and how busy the trunk is |
| P | pull requests read | the stack, and whatever their bases lead to |

A command should cost a handful of processes per selected branch, each bounded
by K. C and L are the ones that turn a fast command into one that times out,
because they are large in exactly the repositories people use most.

## The patterns

**Bound every content comparison by the branch's own commits.** A comparison
by content — `git cherry`, patch ids — costs every commit on both sides. A
branch's own side is bounded by its parent or recorded fork point, which is
also what makes the answer right: counted from the trunk's start, every commit
the trunk gained reads as the branch's own work. `link`'s currency learned this
first; `pull`'s comparison with a published version had not, and its refusal
counted the trunk's commits as the branch's own. It still asks the unbounded
question once, deliberately: whether the published version holds everything
here, base and all, is what tells a reworded branch from a replayed one.

**Ask the cheap question first, and let it only say no.** Whether a branch has
landed needs content, but "no" usually does not: if nothing the trunk gained
touches a path the branch touches, no commit can be equivalent and no squash
can be absorbed. `git.Client.Untouched` answers that from paths in
milliseconds, and `landed.Into` asks it first. It never answers "landed", and it
must never be used where an undercount is dangerous — `landed.Missing` counts
work a push would drop, and path-limited counting could miss some.

**Leave out what cannot be the answer.** Branches already merged into the trunk
are ancestors of every branch on it, so a whole-stack adoption measured each
against each: quadratic in the repository's history of merged branches, and on
a few hundred of them a preview that timed out. None can be a parent or a child
of anything in the stack, so one `for-each-ref --merged` names them and they are
skipped. `internal/graph/cost_test.go` pins the growth of both kinds of
sediment — squashed branches the trunk does not contain, and merged ones it
does.

**Do not ask for what will be thrown away.** Selecting a stack from the g2g
record needs its shape, not how each branch's contents stand; `Structure` is
the selection without the assessment `Discover` adds. A named parent is one
ancestry question, not the whole candidate list. A branch only behind its
remote has every remote commit missing by definition, so it is counted rather
than compared.

**Batch, then parallelise what is genuinely per branch.** One `for-each-ref`,
one `rev-parse` of every revision, one aliased GraphQL query per round. What is
per pair — divergence, ancestry, cherry — runs through `parallel.Each` into a
slice sized first, so nothing needs a lock.

## Still expensive

Known, measured, and left for a change of its own:

- **A land of N branches replays quadratically, and deliberately so.** After
  every merge, `land` advances and replays the whole remaining stack, so the
  branch at the top is replayed N times. That cost is local — processes and
  commits rewritten on this machine — and it buys a stack that is whole and on
  the current trunk after every step, which is what makes a descent that stops
  part-way safe to leave. What reaches the remote stays linear: each merge
  pushes exactly the next branch (`publish` refuses a plan holding any other),
  retargets its one pull request, and reads the remote in one `ls-remote` and
  one fetch; the stack comments are kept once, at the end. Keep it that way — a
  change that pushed the replayed branches above as it went would make the
  remote quadratic too. The one waste worth removing is the trunk fetch just
  after a merge settles, which the advance repeats.
- **restack resolves and re-records serially.** Each step resolves its tips one
  at a time, and fork points are re-recorded for every branch after a rewrite,
  including ones that did not move. One batched resolve and one `update-ref
  --stdin` transaction would do.
- **A squash check that conflicts** walks back through the base looking for
  where the branch was taken in, up to 32 merges, a process each. `merge-tree
  --stdin` would take them all in one.
- **`ResolveAll` falls back to a process per revision** when one of them is
  missing, which a pull request head nobody fetched makes ordinary. `cat-file
  --batch-check` answers missing per line.
- **The pull request graph's first round is every local branch in one query.**
  With hundreds of stale branches that is one large request; chunking it would
  keep one slow answer from failing the command.
