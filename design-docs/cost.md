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
counted the trunk's commits as the branch's own. What tells a reworded branch
from a replayed one is not an unbounded comparison either — a squash of a
one-commit branch is the same patch as that commit, which fooled it — but
whether the published version sits on the parent as it is here.

**Ask the cheap question first, and let it only say no.** Whether a branch has
landed needs content, but "no" usually does not: if nothing the trunk gained
touches a path the branch touches, no commit can be equivalent and no squash
can be absorbed. `git.Client.Untouched` answers that from paths in
milliseconds, and `landed.Into` asks it first. It never answers "landed", and it
must never be used where an undercount is dangerous — `landed.Missing` counts
work a push would drop, and path-limited counting could miss some.
`syncpoint.Assess` follows the same rule before classifying a branch: a drop
needs a commit the sync point holds with no equivalent on the other side, and
whether there is one is asked by id and by a content comparison bounded on
both sides (`git.Client.Inequivalent`). Only a "maybe" pays for `Classify`, whose
`git cherry` reads every commit the trunk gained since the remote's version —
which, on a stack restacked and not yet pushed, doubled every command that
compares with the remote until it was asked first.

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

Writes batch too. The stack comments and `github retarget`'s bases are one
aliased mutation document of up to twenty writes (`githubstack.Client.mutate`),
where each was a request per pull request: a comment run on fifteen pull
requests was fifteen requests and is one, and a retarget of fifteen was fifteen
`gh pr edit`s — each with look-ups of its own before the edit — and is one. A
document's answer is read per alias, because GitHub applies the rest after
refusing one. `gh pr create` and `gh pr merge` stay one call each: which merge
queue a merge goes through, and what `--admin` bypasses, are gh's to decide.

**Ask GitHub less as a wait drags on.** Every GitHub request draws on one
allowance per account, shared with everything else that account runs. A wait
that has gone on for a minute -- a merge queue, slow checks -- is not about to
answer in seconds, so `land`'s settle loops widen from eight seconds to thirty
once they have waited that long, counted in time waited so a test sees the
same sequence. A request refused for coming too often is waited out at that
slow pace rather than stopping a descent part-way, and named if the budget
runs out first. A state a wait has just read is handed on rather than read
again: `recheck` decides from the merge state `settlePush` settled on. The
loop is `internal/settle`'s, so `submit`'s wait after its push paces itself
the same way.

**A wait asks about the whole selection at once.** `submit` waits, up to a
minute, until each existing open pull request it pushed to reports the pushed
head: one `ResolveAll` for the pushed tips before it starts, then one aliased
`Inspect` per poll for every branch it waits on, read through
`githubstack.ResolveHeads` so a branch's pull request is the one every other
command would name. The ordinary case is one extra query, because the wait
asks before it sleeps and GitHub usually already agrees. A pull request this
run opened, or one whose head GitHub never reported, is not asked about.

**What a submission leaves out is asked from the top of each arm down.**
`submit` asks one `merge-base --is-ancestor` of a branch above the target
only once everything above that branch has been left out (`leftOut`), so the
ordinary stack, whose tops have work, costs one question per leaf and no
more. On a line that is the single question it always was; a fork adds one
per extra arm, never one per branch.

**A restack guard reads every worktree's journal at once.** Each guard
check resolves the common and private Git directories, which costs two
`rev-parse` calls. It then reads the shared journal file and lists the linked
worktrees' files with `os.ReadDir`, never a glob built from the repository's
path. That is one pass whatever the number of worktrees. It
never asks Git per worktree: a linked worktree's checkout comes from the
`gitdir` backlink in its private directory.

**Merged PR history needs local evidence.** A GitHub `MERGED` lifecycle is
not proof of landing in the trunk. Live
`github status` checks merged branches through `landed.Into`; submit checks
only merged predecessors and qualifies their history in its preview. Both
compare against the local trunk, bounded above that trunk rather than a
feature parent that may already contain the head. Cherry and whole-branch
absorption remain the shared decision, with advisory present/absent/unknown
outcomes in `landed.Assess`. A PR merged into the resolved landing trunk whose
content is absent or unconfirmed locally needs `g2g pull` to refresh that trunk
and reassess, never replacement advice. Only feature-base merges with absent
content say “merged into <base>, not landed” and offer `g2g submit`; unavailable
feature content stays “landing unconfirmed”. Cached lifecycle observations
remain dated history and never evidence for deletion. No GitHub call is added.

A merged predecessor whose content that reading confirms in the landing trunk
is left out of the submission: no creation, no spec entry, no push. This is
the landed parent still recorded after a squash merge and a pull, until
`prune` forgets it. It costs nothing more, because it reuses the
merged-history answer the plan already holds (`Plan.landed` over
`MergedLanded`). A landed branch with no pull request at all is still offered
as a creation; see below.

A merged PR into the branch’s recorded feature parent has a separate outcome:
`landed.AssessParent` checks that parent by content and, when necessary, its
locally known published copy from `KnownTips`. Candidate parents are read in
one batch through `landed.KnownParents`, before concurrent content checks. A
confirmed child is “merged into synthetic-parent; absorbed”, excluded from
submission metadata and publication. It is not asserted landed in the trunk.
`github status` offers the source’s cleanup command. For g2g, `prune` already
checks parent absorption; when only the known published parent contains the
work, collect it first with `pull --above synthetic-parent` on the selected
remote. Restack surviving descendants before pruning. Graphite names `gt sync`;
PR-derived structure supplies no cleanup command.
Submit keeps selected topology and filters publication eligibility separately.
It refuses creation or linking across an omitted absorbed base before pushing.
Its absorption evidence must describe the resulting parent: the proposed local
tip for a parent in the final push leases, otherwise the observed published tip
or locally known copy for an unobserved parent. Observed absence overrides stale
knowledge. Bound a published-parent assessment at that version, never the local
parent, which may already contain the child's commit ids. Push observations and
comparison evidence are reused when excluding absorbed actions, without a
second push comparison. Advisory status may still report either parent's copy.

A merge into another feature base, or absent/unreadable parent content, grants
no exclusion. Followup child work still needs a replacement. These advisory
reads neither fetch nor authorize deletion from cached PR lifecycle state.

**Preserved PR bases use projected publication.** Only mismatched open PRs
whose heads are in `Push.Leases()` need ancestry checks. Bases in those leases
use their local proposed tips. Untouched bases reuse push's observed remote
tips, including observed absence; bases push did not observe cost at most one
additional batched `RemoteTips` read per submit plan. One `ResolveAll` resolves
heads and effective bases before the per-pair ancestry questions. Missing refs
or unfetched remote objects remain unassessable; no fetch or GitHub read is added.

## Still expensive

Known, measured, and left for a change of its own:

- **A land of N branches replays quadratically, and deliberately so.** After
  every merge, `land` advances and replays the whole remaining stack, so the
  branch at the top is replayed N times. That cost is local — processes and
  commits rewritten on this machine — and it buys a stack that is whole and on
  the current trunk after every step, which is what makes a descent that stops
  part-way safe to leave. What reaches the remote stays linear: each merge
  pushes exactly the next branch (`publish` refuses a plan holding any other),
  retargets its one pull request in one request, and reads the remote a fixed handful of
  times — land's own tip check, push's, and the pull's `ls-remote` and fetch; the stack comments are kept once, at the end. Keep it that way — a
  change that pushed the replayed branches above as it went would make the
  remote quadratic too. A pull fetches only refs that have moved since g2g last
  fetched them, so the trunk fetched to see a merge arrive is not fetched again
  by the advance after it.
- **restack resolves and re-records serially.** Each step resolves its tips one
  at a time, and fork points are re-recorded for every branch after a rewrite,
  including ones that did not move, except branches explicitly left by worktree
  policy. One batched resolve and one `update-ref
  --stdin` transaction would do.
- **A child of a rewritten parent is compared with everything the trunk
  gained.** `classifyOrphans` runs `git cherry` and `rev-list --left-right`
  between the parent's new tip and the child's fork point with no bound on the
  parent's side, so on a trunk that has moved on both read every commit it
  gained — 0.25–0.36s a call at 500 trunk commits, the slowest commands in the
  preview. `landed.Into`'s Untouched check cannot shortcut there either, since
  `base...branch` includes the parent's old commits. Bounding the parent's side
  at both versions of the grandparent, as `Inequivalent` does, would make it
  cost the branch's own commits.
- **A drifted branch on a trunk asks one more ancestry question.** Telling a
  trunk that lags its upstream from one that moved on
  (`graph.AheadOnUpstream`, in `classify` and in restack's steps) costs one
  `merge-base --is-ancestor` of the trunk's tip against the fork point, per
  drifted branch whose parent is a trunk: one per stack after the trunk
  advances. Only the lagging case, which is rare, also reads the upstream
  (`for-each-ref`) and asks a second. Answering every stack root on a trunk
  from one `merge-base --independent`-style call would make it one process.
- **A squash check that conflicts** walks back through the base looking for
  where the branch was taken in: the newest 32 trunk commits since the fork
  that touch the branch's files, one `git log` to find them with their trees
  and a merge each. The bound is the newest 32, not "give up past 32": it is
  how deep the squash is buried that it limits, so a branch whose files the
  trunk edited often while it was in review is still found landed. A branch
  that never landed paid for all 32, and so did one that landed and was edited
  over since: six such branches on one status measured 396 merges. `heldAdded` now asks first, in one `git log --find-object`,
  whether the trunk ever held the files the branch adds exactly as the branch
  has them: a new file that never arrived rules every candidate out with no
  merge at all, and one the trunk later edited starts the search at the commit
  before that edit. A status over about 110 branches, most of them finished
  and not yet pruned, went from 1,982 trial merges to 1,381. What still pays
  the whole bound is a branch that adds no file, or deletes one, and one whose
  added files the trunk still holds unedited while something else conflicts.
  `merge-tree --stdin` would take the remaining merges in one process, but
  needs input on standard input, which `subprocess.Runner` does not carry.
  What the search finds is remembered between runs (`g2g/landed.json`), by
  commit: a branch found landed costs the merge at the tip from then on, and
  one that was not is asked only about the commits the base gained since it
  was last asked. The first run in a repository pays in full: the status above made 1,186
  merges on its first run with the file empty and 285 on the next, in 39
  seconds where it had taken over four minutes. What is left is the merge at
  each branch's tip and the `Carries` check after a yes, neither of which is
  remembered, because both depend on where the base is now.
- **A landed answer is checked against the branch's own commits.** A yes from
  the whole-branch merge is followed by `Carries`: one ancestry check and one
  `merge-tree`, for each boundary the caller hands `landed.Whole` (a recorded
  fork point, the branch below, a merged head). It is asked only
  after a yes, so a branch that has not landed pays nothing.
- **A branch on the trunk that reverts its own merged work reads as landed**
  to prune and to a replay's collapse. They know no boundary inside the
  branch to measure the follow-up commit from; submit and `github status` do,
  through the merged pull request's head. Left for a change of its own.
- **A replayed branch is asked for merge commits.** restack's plan runs one
  `rev-list --merges` over `forkPoint..head` for each branch it would replay,
  bounded by that branch's own commits. A collapsing branch and a seated one
  are not asked. One call naming every range would make it a process per plan.
- **A landed branch with no pull request is still offered as a creation by
  `submit`.** Leaving out a landed parent reuses the answer already read for
  merged predecessors. Confirming a branch that never had a pull request would
  cost a landed check per creation candidate, on every submit, for a case the
  next `prune` removes. Deferred deliberately.
- **`land` still waits for every worktree's restack.** It cannot name the
  branches a descent touches before it runs, so it has no footprint and one
  restack stopped anywhere refuses it. That costs waiting rather than
  processes, and is listed here because it is the same shape: a whole
  repository's state charged to one stack.
- **What g2g keeps is read whole each time it is asked.** The remembered
  landed answers are one small file, read once per search and written once
  per answer, so a status over a hundred branches reads it a hundred times.
  An operation is recorded from two readings of every local branch and the
  graph, one before the mutation and one after, and a snapshot's basis from
  two more around planning: a `for-each-ref` and a file each, whatever the
  stack's size. Undo asks one question per branch it removes and per commit
  it restores, serially.
- **Left for a change of their own, from the pass over these features:** the
  repository is read "as it is" by two types that overlap (`snapshot.World`,
  `oplog.Reading`), and "is what this rests on still so" is asked by two
  functions with different vocabularies (`Basis.Changed`, undo's `since`);
  `undo.Service.Apply` and `Plan` each do several jobs in one body;
  `runSnapshot` spells the flow's gates a second time; and nothing tests that
  the three destructive callers ask with `landed.Fresh`.
- **`status` asks a drifted branch whether it landed twice**: once for its
  state (`graph` `drifted`, through `landedInATrunk`) and once for its
  publication (`push` `Compare`'s landed standing), with the same trunk and
  branch. Each is a full `landed.Into`, so the conflicting case above costs
  double. Remembering the answer for a command needs it keyed by resolved
  commits rather than names, because a command that moves a ref and asks
  again must not get the answer from before.
- **`ResolveAll` falls back to a process per revision** when one of them is
  missing, which a pull request head nobody fetched makes ordinary. `cat-file
  --batch-check` answers missing per line.
- **A branch under a landed one is asked whether it landed serially.**
  `graph.belowLanded` runs after the concurrent classification, in render
  order, so a branch found landed lets the one under it be asked in the same
  pass. It asks only branches directly under a landed one — the part of a stack
  about to be pruned — through the same check, Untouched shortcut first, that a
  drifted branch gets. Asking each level concurrently would keep the order and
  lose the serial wait on a deep landed chain.
- **A sync point is read for the whole selection, and two things are still
  per branch.** Every branch's sync-point history comes back from one
  `for-each-ref` and one `git log -g` over all of them
  (`git.Client.SyncHistories`), whether each was recorded for the branch that
  has its name now from one walk of their reflogs (`DescribesAll`), and a
  command records every agreement it makes after one read of what is there
  (`syncpoint.Record`). What stays per branch is the drop pre-check — the
  sync point's own commits, and one bounded comparison of both sides — and,
  only when something was dropped, the full classification. Status reads the
  histories twice, once to compare and once for its recent-drops note.
- **A sync point's history is carried across a rename one entry at a time.**
  A ref can be updated only once in a transaction, so `MoveSync` is one
  `update-ref` per agreement it carries. Renames are rare and the history is
  bounded by reflog expiry.
- **The pull request graph's first round is every local branch in one query.**
  With hundreds of stale branches that is one large request; chunking it would
  keep one slow answer from failing the command.
