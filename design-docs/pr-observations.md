# PR observations and local cleanup

Offline status needs to distinguish a missing local branch with a previously
observed PR from one with unknown PR history. Neither proves what happened on
GitHub since the last observation. A UI merge, `gh pr close`, or branch reuse
can change that answer without g2g running.

## Remembered knowledge

The shared `githubstack.Client` records successful PR reads and PR creation in
`g2g/pull-requests.json` under the Git common directory. Each branch observation
has a PR identity and URL, head and base where known, lifecycle state, open PR
count, and UTC observation time. Existing head resolution chooses one open PR
before historical ones; multiple open PRs remain visibly ambiguous. A new PR
on a reused branch replaces the previous observation.

`submit` reads the URL returned by `gh pr create`, without another query.
`land` records a successful merge request separately: acceptance can mean
queueing, so it does not imply `MERGED`. Existing merge-state reads confirm the
lifecycle and clear the pending request. Successful GitHub reads, including
`github status`, refresh the same file. An empty query preserves older dated
knowledge rather than inventing a lifecycle transition.

The cache is shared by linked worktrees and retained when graph edges are
pruned. Writes use private files, temporary-file replacement, and a mutex for
concurrent clients in one process. Across processes, writers are last-writer-wins;
losing an observation loses context, never user work. Unknown schemas and corrupt
files are not overwritten. Write failures warn without failing an external action
that already succeeded. Offline status can report a cache-read failure and still
draw the graph.

## Reading it

`status` displays a linked PR with **last seen open/closed/merged** and its time.
A missing branch without an observation says **PR history unknown**, never
"never submitted". A last-seen open PR on a missing branch is a warning; a
last-seen merged PR is neutral history. A pending merge request remains distinct
from confirmation. None of these annotations claims to be current.

`g2g github status --branch synthetic-work` asks GitHub for the actual state,
even if that g2g-recorded local branch is missing. Its explicit read-only
selection retains absent refs and skips their local content and currency checks.
Mutating commands still refuse those refs. The PR observation is supplemental:
it never defines a parent, selects a source, proves work landed, or authorizes
deletion. `internal/graph` remains dependent on Git alone.

## Explicit cleanup

`prune` edits graph records by default. `--delete-branches` also removes
assessed local branch refs; `--forget-missing` also removes selected records
whose refs are gone, without classifying them as landed. Both remain preview-first
and compose through `pull --prune`, after the pull completes. Repository-wide
cleanup is `g2g prune --scope all --delete-branches --forget-missing`; it reaches
recorded branches only and never removes remote refs.

Deletion asks Git by content, using both per-commit equivalence and whole-branch
absorption for squash merges. It captures heads and bases before assessment,
rechecks them before mutation, refuses any checked-out candidate, and deletes
each ref under an expected-tip lease. A recorded fork point must still be an
ancestor of the assessed head, so a manual reset cannot exclude unlanded work
from the deletion check. Its inherited work must also remain in the live base,
by ancestry or whole-branch absorption, so a parent rewind cannot leave that
work solely on a branch about to be deleted. A followup commit after a merge remains.
When a parent is missing, the comparison includes its inherited work from the
first fork above a surviving recorded ancestor. Counting only the child's own
commits could delete the last ref carrying a parent's unlanded work.
Surviving selected children are recorded on the branch below only when ancestry
proves they already sit there. Other children still refuse cleanup.

Local deletion precedes saving graph removal. If deletion stops, the graph still
describes what remains, including now-missing branches; a scoped retry with
`--forget-missing` can reconcile it. Failures after irreversible work carry a
`prune.Stopped`, render the completed actions, and exit `3`, including failures
releasing fork-point pins after graph removal. This is recomputation, not a
second resumable journal.

Status cleanup hints preserve their target and scope. A fully missing selected
subtree gets one `untrack --scope subtree` hint; live or unselected descendants
prevent grouping. Landed hints offer the graph-only prune and the explicit
combined cleanup preview separately.
