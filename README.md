# g2g

`g2g` is a local stacked-branch structure tool. It records a branch forest in
the repository's Git common directory, then uses that structure to inspect,
restack, update, and publish a stack. Recording and maintaining structure need
only Git: neither Graphite nor GitHub is required.

Graphite is an optional source and alignment target. g2g can read a
Graphite-described stack where one exists, import it into the local forest, or
mirror local structure back to Graphite. GitHub is a publishing and projection
integration: g2g can create pull requests, inspect their bases, and project a
selected **linear** path onto GitHub native stacks. It never flattens a fork
merely to publish it.

## Install

```sh
brew install shhac/tap/g2g
```

The formula was once named `gt2gh`. The tap maps the old name to the new one,
so an existing install migrates on `brew update` rather than quietly stopping
at the last version published under it. Release archives from tags before the
rename keep the old asset name, which is why a download from one of those
unpacks a binary called `gt2gh`.

`--help`, `--version`, and `completion bash|zsh|fish` are available; bare
`g2g` shows help. Completion for `--branch` and `--trunk` draws on whichever
sources describe the repository, so it works with no Graphite installed. It is
read-only and checkout-free, and it never runs Graphite in a repository that
does not already use it.

## A day with g2g

Every command that changes anything previews first and acts only with
`--apply`; run each one bare to read the plan before adding the flag. Moving
the checkout is the one exception. The branch names are placeholders.

```sh
# Record the stack you are on, once. The order comes from commit ancestry; the
# trunk has to be named the first time, because nothing recorded implies it.
g2g track --stack --trunk main --apply
g2g graph

# Or pick up a stack a colleague published: with its branches fetched and
# checked out here, adopt the structure their pull requests declare.
g2g import --from pull-request --apply

# Add a branch on this one, committing what is staged, and move around.
g2g create synthetic-three -m "Add the third change" --apply
g2g down 2

# Change a branch in the middle, then replay what sits on it.
git commit --amend
g2g restack --apply
g2g top

# Reshape: merge a branch into the one below, drop one, or give one a new name.
g2g fold --apply
g2g delete --branch synthetic-abandoned --apply
g2g restack --apply
g2g rename synthetic-better-name --apply

# The trunk moved: fetch, fast-forward it, replay; then forget what landed.
g2g sync --apply
g2g prune --apply

# Publish the branches, open missing pull requests as drafts and link them,
# fix bases a restack left stale, keep a stack map on each, and check it all.
g2g push --apply
g2g submit --edit --apply    # also keeps the stack comments; --no-comment skips
g2g retarget --apply
g2g comment --apply          # or keep them by hand, any time
g2g status

# Finished: from the top, merge the stack down onto the trunk, bottom first.
g2g land
g2g land --apply
```

## Concepts

### The forest

`g2g graph`, `g2g track`, and `g2g untrack` maintain a branch forest g2g owns
itself. They read Git and nothing else: no Graphite, no GitHub, no network.
This is the structure that exists for branches you have not pushed yet, and it
is the only place a fork can live — GitHub native stacks are linear, and a pull
request base cannot describe a branch that has no pull request.

Every branch has at most one parent, a parent may have many children, and a
repository may have several roots. `track` never chooses a parent when the
answer is ambiguous. See
[design-docs/g2g-owned-graphs.md](design-docs/g2g-owned-graphs.md) for the
model, the storage decisions, and what is deliberately left out, and
[Compatibility and storage](#compatibility-and-storage) for where it lives.

### Where structure comes from

Every command that selects a stack asks one question first: which source
describes this branch?

```
adopted into g2g's store  →  g2g's own graph
tracked by Graphite       →  Graphite
neither                   →  refused, with the remedy
```

Adoption wins because recording an edge is you saying you want g2g to own the
branch; Graphite is the fallback for branches g2g has not adopted. The answer
is worked out per branch, every time, and never stored — so moving a branch
between sources is just `g2g track` or `g2g untrack`, in either direction, and
there is no ownership record to go stale.

`link`, `push`, and `submit` therefore work on a stack g2g owns, with no
Graphite installed. And **g2g will not run Graphite in a repository that does
not already use it**: Graphite's discovery creates state, so being asked
whether it applies must not be what enrols you. In such a repository g2g stays
local.

`restack` is the exception, deliberately: it needs a fork point, which only
g2g's own store records, so it refuses a Graphite-owned branch and says to
`g2g track` it first. Authority governs what may be changed, not what may be
read, and it is **not** exclusive: a branch can sit in g2g's graph, be tracked
by Graphite, and appear in a GitHub stack all at once, and nothing here removes
it from any of them.

`--from` pins the source for one command:

```sh
g2g status --from graphite    # what does Graphite think this stack is?
g2g push --from g2g
g2g graph --from graphite     # Graphite's record, drawn in g2g's format
```

Once a branch is adopted there is otherwise no way to ask Graphite what it
thinks of it, and comparing the two views is what you want before reconciling
them. Nothing is recorded. `graph --from` offers only the offline records.

**Pull request bases** are a third source, read only when named:

```sh
g2g status --from pull-request --scope stack
```

It shows a repository's published branches as a tree with nothing recorded
locally, and says what GitHub will merge rather than what you intended. It is
never consulted by precedence because reading a base invokes `gh`, and `push`
must never do that. Asking for it makes its two limits an informed reading
rather than a silent one: it describes **published branches only** — no pull
request, no edge — and GitHub retargets a child when its base branch is deleted
on merge, so right after a parent lands its children point at the trunk. It is
never wrong about what a merge will do, and no longer a record of what the
stack was. `g2g import --from pull-request` records what it describes; see
[Adopting a published stack](#adopting-a-published-stack).

**Trunks are never guessed from a name.** On a Graphite-described stack g2g
infers the only Graphite-declared trunk on the selected ancestry and shows it
prominently. If that ancestry has multiple declared trunks, it fails closed and
requires `--trunk <branch>`; an override must be both declared by Graphite and
an ancestor of the selected branch. A g2g-owned path has exactly one root, so
`--trunk` can only confirm it — naming any other branch is refused rather than
ignored.

### Scope: how much of the stack

One flag says how much of the structure a command means, and it means the same
thing whichever record describes the branch. The values form a lattice rather
than a list — two halves, toward the trunk and toward the tips, taken separately
or together:

| `--scope` | selects |
|---|---|
| `branch` | just this branch |
| `path` | the trunk down to this branch |
| `subtree` | this branch and everything above it |
| `stack` | this whole stack, trunk to tips |
| `trunk` | every stack on this trunk |
| `all` | every stack in the repository (`graph` and `prune` only) |

Selected from a trunk, `stack` is the whole tree under it.

Defaults differ because the commands differ. `status` and `graph` default to
`stack` — reading is free, so show where you are, ancestors and descendants
both. `restack` defaults to `subtree`, because rewriting is not free: a
conflict below you may be one you are deliberately deferring, and replaying it
uninvited is how restacking from the middle walks into it every time. `land`
defaults to `path`, because standing in the middle of a stack and typing `land`
means "as far as here". `prune` defaults to `stack` and offers `all`, since it
edits only the record and forgets only what has landed. `sync` offers only
`stack` and `trunk`, and `untrack` only `branch` (its default) and `subtree`.

```sh
g2g status                   # where am I: the trunk, me, and everything above
g2g status --scope path      # just the trunk down to me
g2g restack --apply          # me and what depends on me
g2g graph --scope all        # every stack in the repository
```

A GitHub native stack is linear, so `link`, `unlink`, `submit`, `push`,
`retarget` and `land` take `stack` or `path` only, and refuse a selection that
forks — naming the remedy rather than choosing a line. Selecting a leaf is that
remedy and needs no flag: a leaf has no descendants, so `stack` collapses to an
ordered path by itself. [`comment`](#comment) has no `--scope`.

### Preview, `--apply`, and revalidation

All mutating commands preview first and require `--apply`. With `--apply` they
re-discover and revalidate against the preview before mutating, render and
flush the final plan first, and refuse ambiguous or unsafe work instead of
guessing. On success a command prints a concise confirmation; on failure it
never claims that changes were made.

The one exception is moving the checkout (`up`, `down`, `top`, `bottom`),
which changes nothing else — see [Move and add](#move-and-add).

Interactive confirmation or a cancellation/cooldown period before mutation is
intentionally deferred; it needs a separate safety design and is not implied by
the current `--apply` flow.

### Exit status

| Status | Meaning |
|---|---|
| `0` | it did what was asked, or there was nothing to do |
| `2` | it failed, and achieved nothing |
| `3` | it did part of what was asked and stopped somewhere you have to act |

`3` is `sync` stopping on a conflict mid-replay, `land` stopping part-way down
a stack after something merged, `comment` stopping after writing some of its
comments, `create -m` whose commit failed after the branch was recorded, and a
`delete`, `fold` or `rename` that could not put back what it had done. A
descent that stopped before changing anything is an ordinary failure. Those are
not failures to retry — what replayed stays replayed and what merged stays
merged — and not successes either. Both print what happened and what to do
next; the status lets something reading only the status tell the difference,
the way `git rebase` and `git merge` exit non-zero when they stop needing you.

## Commands by task

### Record

```sh
# Record the whole stack you are on, in one step. This is where to start.
g2g track --stack --trunk main --apply

# Preview the candidate parents of one branch. It refuses to choose.
g2g track

# Record one parent. Preview first; --apply writes.
g2g track --branch feature/login --parent feature/auth
g2g track --branch feature/login --parent feature/auth --apply

# Inspect the graph. Scope widens from one branch to every stack.
g2g graph                                        # this whole stack
g2g graph --scope subtree                        # the branch and its descendants
g2g graph --branch feature/login --scope trunk   # every stack on that trunk
g2g graph --scope all                            # every stack in the repository

# Remove edges. --scope subtree removes descendants too.
g2g untrack --branch feature/auth --apply
g2g untrack --branch feature/auth --scope subtree --apply
```

`--stack` records a whole existing stack at once, which is almost always what a
repository that predates g2g needs. You assert one thing — the trunk, and even
that is inferred when exactly one recorded root is an ancestor — and the shape
follows from commit ancestry. It records a **forest, not a chain**: branches
hanging off the stack join it, and branches hanging off those join in turn,
while a branch that merely shares the trunk is left alone, being a separate
stack rather than part of this one. Where ancestry cannot order two branches it
refuses and names them, exactly as `track` does.

Parents are inferred from commit ancestry: the candidate parents of a branch
are the local branches its commits sit on top of, ordered nearest first.
`track` shows that list and blocks. It never picks for you — the nearest
ancestor is usually right, and "usually" is not a basis for writing down
structure every later command trusts.

A parent you name that is *not* an ancestor is recorded on request rather than
refused, since that is how a stack looks before a restack, but `track` says so
first, because it explains why the branch will then read as needing one.

A trunk that has moved on is no longer an ancestor of the branches built from
it, so recorded roots are always offered as candidates. Adopting the very first
branch into an empty graph has neither, so it falls back to measuring from the
fork point: one Git call per local branch, only when the cheap paths found
nothing.

`graph` renders a fork with connectors and a chain as the same flat column
every other command uses, because a chain has no structure that indentation
would add. It reports what it finds and repairs none of it:

- **needs restack** — the recorded parent moved underneath the branch.
- **moved off parent** — the branch is no longer built on its recorded parent,
  which is what a manual rebase looks like.
- **parent missing** — the recorded parent is no longer a local branch, which
  is what a squash-merged and deleted parent looks like.
- **landed** — the branch's own work is already in the trunk, by content.
- **no commits of its own**, **fork point unresolvable**, and **branch
  missing** (the recorded branch was deleted or renamed with plain Git).

Untracking a branch in the middle leaves its children pointing at it and says
so. Reparenting them onto the grandparent would invent an edge you never asked
for.

`g2g import` records what Graphite declares, described with
[Graphite alignment](#graphite-alignment), or with `--from pull-request` what a
published stack's pull requests declare, described in
[Adopting a published stack](#adopting-a-published-stack).

### Move and add

```sh
# Start a branch on top of this one, switch to it, and record it. Preview first.
g2g create feature/login
g2g create feature/login --apply

# The same, committing what is staged onto the new branch.
g2g create feature/login -m "Add the login form" --apply

# Start it on another recorded branch instead of the one you are on.
g2g create feature/session --parent feature/auth --apply

# Delete a branch, recording what sat on it on what it sat on. Then replay
# those onto their new parent, which drops the deleted branch's commits.
g2g delete --branch feature/abandoned --apply
g2g restack --branch feature/child --apply

# Fold a branch into its parent: the parent fast-forwards to it, it goes.
g2g fold --branch feature/login --apply

# Rename a branch and every record of it.
g2g rename --branch feature/login feature/sign-in --apply

# Move the checkout. No --apply: these change nothing but where you stand.
g2g up            # the branch above
g2g down 2        # two below; from the bottom of a stack, down is the trunk
g2g top           # follow the branch above until there is none
g2g bottom        # the first branch above the trunk
g2g up --dry-run  # say where, and the git switch that gets there, without moving
```

`create` replaces `git switch -c`, a commit, and a `track --parent` retyping the
branch you were just on. The parent is the branch you stand on or the one
`--parent` names, so it is stated rather than inferred and there is no candidate
list. It must already be in the g2g graph, or be the repository's default branch
(what `refs/remotes/origin/HEAD` names): recording a child under a branch the
graph does not know would quietly make that branch a trunk, so `create` refuses
and names `track --stack` instead. In a repository with no default branch
recorded, start the first branch on the trunk by hand and record it with
`track --parent`; `create` works from there on.

It switches, records, then commits. A recording that fails is undone — you are
put back where you were and the new branch is deleted — because nothing is on
it yet. A commit that fails after the record (a hook refusing it, say) leaves
the branch created, checked out and recorded with the changes still staged, and
exits `3`.

`delete`, `fold` and `rename` change which branches a stack is made of, and act
only on branches the g2g graph records — anything else is refused, naming
`g2g track`. None of them replays a commit; that stays `restack`'s job.

- `delete` removes the local branch and records each branch that sat on it on
  the branch it sat on. Unlike `untrack`, which never reparents, this is what
  asking for the branch to go means. The children keep their fork points, so
  the next `restack` replays only their own commits onto the new parent and the
  deleted branch's commits leave them. The preview names every one of those
  commits that exists nowhere else — not in the parent by content, and on no
  remote-tracking ref — and suggests the restack. Deleting the branch you stand
  on switches to its parent first, which `git switch` refuses rather than
  overwrite a local change. The remote branch and any pull request are left
  alone. A trunk, and a branch checked out in another worktree, are refused.
- `fold` fast-forwards the parent to the branch, so its commits become the
  parent's, then removes it as `delete` does; what sat on it already sits on the
  parent's new tip. Only a parent the branch sits directly on can be
  fast-forwarded, so one that has moved on is refused, naming `g2g restack`.
  Folding into a trunk is refused, naming `g2g land`: a branch joins its trunk
  through its pull request. The parent's other children then need a restack,
  and the preview says which. If the parent is checked out, the working tree
  moves with it; a local change in the way stops the fold and puts the parent
  back.
- `rename` runs `git branch -m` and rewrites the record: the branch's own edge,
  the branches recorded on it, its place among the trunks, and its fork-point
  ref. The name is checked with `git check-ref-format --branch` and must be
  free. Git moves another worktree along with the branch, so that is allowed. A
  branch already published stays published under the old name, and so does its
  pull request; the preview says so when a remote-tracking ref carries the old
  name, and `push` then publishes the new name as a new branch.

Each orders its steps so that everything but the last can be put back, and does
put it back if a later step fails. There is no `split`: dividing a branch's
commits means choosing which goes where, which `git rebase -i` and
`g2g create`/`g2g track` do with a person choosing.

`up`, `down`, `top` and `bottom` resolve the stack the way every stack command
does, including `--from`, and never choose: at a fork they refuse and name the
branches above, each as the `git switch` that would go there. They are the one
exception to preview-first, deliberately: moving the checkout changes no ref,
record or remote, and `git switch` already refuses to overwrite a local change,
so a preview would only be a second command to type. The switch is
`git switch --no-guess`, so a branch a record names but this checkout lacks is
refused rather than recreated from a remote. They refuse mid-restack, because
switching away strands the rebase in progress. With `--json` or `--porcelain`
the destination is `target`, how it was reached `targetSource`, the branches
walked are listed, and the switch is `command` — the one that ran, or with
`--dry-run` the one that would; `command` is absent when you are already there.

### Keep current

#### Restack

`g2g restack` replays a stack's commits so its contents match that structure.
This is what a squash merge upstream breaks: the child keeps its parent's
pre-squash commits, so its pull request shows the parent's changes a second
time and merging it reapplies work the trunk already has.

```sh
# Preview. Says exactly what will be replayed and whether it will conflict.
g2g restack --branch main --scope stack

# Replay.
g2g restack --branch main --scope stack --apply

# Move a fragment onto a different base instead of its recorded parent.
g2g restack --branch feature/login --scope subtree --onto main --apply

# Resume verbs, with the same meanings they have in git rebase.
g2g restack --continue
g2g restack --abort
g2g restack --skip
```

**A clean replay never touches your working tree or checked-out branch.** The
preview knows in advance whether the rewrite applies, and says so:

```
Replays feature/auth and feature/login onto main.
Applies without touching your working tree or checked-out branch.
```

When it cannot apply cleanly the preview says `This will not apply cleanly`
*before* you apply, because rebasing then happens in your own working tree —
resolving a conflict needs a tree you can edit with your own tools — and stops
on the conflict for you. Resolve the conflict, `git add` the files, and run `g2g restack --continue`.
Using `git rebase --continue` or `git rebase --abort` yourself is fine too:
`--continue` re-derives what is left from the refs rather than replaying a
stored queue, so your own git commands simply change what remains to do.
`g2g restack --abort` restores every branch to where it started, including
ones an earlier step already moved.

This is g2g's only resumable operation, so **every other command that
changes anything refuses while a restack is unfinished** — mid-restack a
branch may already have moved while the graph still records where it used to
be.

Two things a restack reports rather than doing quietly:

- **A branch it empties.** If everything a branch carried is already upstream
  it collapses onto its base and its pull request would show no changes.
- **Commits the parent dropped.** They are dropped from the child too by
  default. Where every one of them was genuinely removed rather than
  rewritten, `--absorb` keeps them as the child's own instead — which rewrites
  nothing and only re-records where the branch forks.

A branch you rebased by hand is refused rather than replayed: its recorded
fork point is no longer in its history, so the replay range would silently
widen to include the base's own commits. Re-record it with `g2g track` first.
A rewrite also refuses to move a branch another worktree has checked out,
because that worktree would be left describing a commit its branch no longer
points at.

#### Sync

```sh
# Fetch, fast-forward the base, replay the stack.
g2g sync
g2g sync --apply
```

This is `git switch main && git pull && git switch back && restack` in one
command, and it needs no Graphite. It works on the stack as g2g's graph
records it.

It does not forget anything. Pruning is `g2g prune`, a separate command,
because it answers a different question on the same boundary and edits the
recorded graph rather than moving branches.

The fetch writes only into `refs/g2g/remotes/`, so your own remote-tracking
refs, `FETCH_HEAD`, and ahead/behind counts are untouched. The base is
**fast-forwarded or not at all**: a base that has diverged is reported, never
merged or reset, because "you are behind" and "you have diverged" want
different responses and only you can give the second.

When a branch and its published version have each moved, `sync` refuses rather
than choosing. `--take published` is the way through, and it is the one path
where `sync` loses work that exists nowhere else — so the preview names every
commit it would discard.

```sh
g2g sync --take published                          # the whole stack
g2g sync --take published --through synthetic-fix  # and no further
```

It only ever changes the outcome for a branch that has *genuinely diverged*.
A branch that is merely ahead of its published version is push's business and
is left alone; one that is behind, or whose published version supersedes it,
is brought down either way.

That makes `--take published` all or nothing, and `--through` narrows it. With
two diverged branches the unbounded form takes both — discarding local work on
the upper one alongside the lower one you meant. `--through` stops at the
branch you name and **refuses the rest**, because a boundary says where you
have decided, not that you have decided everywhere. Above the boundary your
commits are kept, and replayed onto what was taken below — which is what `sync`
does anyway.

The boundary is the branch you name and what it is stacked on, because a
branch's published version is built on its parent's, so taking one and not the
other describes a stack that never existed. A sibling on another fork is
outside it; the trunk is inside any boundary, and naming the trunk takes it and
nothing else.

`published` means the version on the remote you named with `--remote`, not
`origin` in particular.

#### Prune

```sh
g2g prune
g2g prune --apply
```

Pruning forgets a landed branch in the recorded graph, asking Git by content —
a squash merge included — whether its work is already in the trunk. It never
deletes a branch: that is a separate, deliberate act, not the tail of another
command. It refuses to strand a branch still recorded under a landed one rather
than reparenting around it.

### Publish

A branch is identified by its single open pull request. Closed and merged pull
requests left on a reused branch name are treated as history: they never block
`link`, `retarget`, or `status`, and `submit` creates a replacement rather than
skipping the branch. Two or more open pull requests for one branch is the only
ambiguity, and it fails closed.

#### push

```sh
g2g push --branch feature/top             # preview; full-stack expansion is the default
g2g push --branch feature/top --apply     # every selected ref advances, or none do
g2g push --remote staging --apply         # a configured remote other than origin
```

`g2g push` is deliberately narrow: it publishes the selected linear path in one
`git push --atomic` with a `--force-with-lease` per branch, and never invokes
GitHub, submits, or restacks. Each lease is pinned to the remote tip the
preview observed, because a bare lease takes its baseline from the
remote-tracking ref and any fetch in between would disarm it. The path may come
from the local forest or Graphite. `--remote` defaults to `origin` and must name
a configured remote. Every selected non-trunk branch is pushed bottom-to-top.

A branch replayed since it was published — the ordinary state after a restack —
is shown as rewritten and pushed: the remote's version is compared by content,
so a commit that is here under a new id is not mistaken for somebody else's.
One the remote has that this checkout does not, by content, is refused rather
than dropped. Unsupported atomic pushes and rejected leases fail without a
non-atomic or unsafe-force fallback.

#### submit

`g2g submit` is a preview-first publication path for a resolved linear stack.
With `--apply`, it validates the complete spec, revalidates immediately before
mutation, performs one atomic lease-protected push, creates only missing PRs
bottom-to-top as drafts, preserves existing PRs, then links the complete stack
and keeps the stack comment on each pull request (`--no-comment` skips that).
It never invokes `gt submit`, restacks Graphite, or retargets an existing PR.

Generate a reusable spec outside the repository, fill in each title, validate,
then apply it:

```sh
spec_dir="$(mktemp -d)"
g2g submit --write-spec "$spec_dir"
g2g submit --spec "$spec_dir/submission.json"
g2g submit --spec "$spec_dir/submission.json" --apply
```

The spec is one JSON document with ordered branch/title/body/reviewer entries;
complex Markdown bodies are preserved exactly. If apply fails, the spec remains
in place and the error gives exact repair, validation, and retry commands.

Missing PRs are opened as drafts. There is no `--draft` flag, because a draft
is the default and can be marked ready at any time; `--ready` is how you ask
for the thing that cannot be undone, since opening ready for review notifies
reviewers immediately. `--write-spec` records the choice in the document, so an
`--apply` that reads it back does not silently drop it, and `--no-ready`
overrules a spec that asks for ready. The preview names what it is about to
open and echoes `--ready` into the command it suggests, so what you read is
what runs.

`g2g submit --edit` creates one temporary `submission.json` document and opens
`$EDITOR`; it never opens a buffer per PR. Add `--apply` to continue after
editing. The temporary spec is deleted only after successful `--edit --apply`;
use `--keep-spec` to retain it. Validation, editor, interruption, and GitHub
failures always retain it.

Repository PR templates are detected from GitHub's conventional locations. One
template pre-fills generated bodies. Multiple templates require an explicit
`--template <name>` or `--no-template`; g2g never guesses. Explicit bodies in
the spec win over templates.

#### retarget

After a restack the local stack is correct and GitHub may still record where
each pull request used to sit. A base is what a merge follows, so leaving it
stale means merging into the wrong branch.

```sh
g2g retarget            # which bases would move, and where from
g2g retarget --apply
```

It is separate from `submit` deliberately. Creating a pull request and changing
what an existing one will merge into are different classes of act, and the
second wants its own preview — every line names the pull request, the base it
has, and the base it would get. It writes through `gh pr edit <number> --base
<branch>`.

It touches only the pull requests whose base disagrees with the resolved stack,
leaves branches with no pull request to `submit`, ignores merged and closed
ones, and refuses outright when a branch has more than one open pull request,
because nothing here can tell which one you meant.

#### link and unlink

`link` projects a resolved linear path onto GitHub's native stack feature. It
works with a g2g-owned or Graphite-described path; Graphite is not a
prerequisite. When at least two PR-backed branches need linking, it prints the
exact bottom-to-top `gh stack link` command. A one-PR path is a successful
no-op: it prints `Nothing to link` and never constructs an invalid command.
Creating the relationship and repairing it are the same act, so there is no
separate reconcile command.

```sh
g2g link                                      # the path ending at the current branch
g2g link --branch feature/top                 # another local branch, without checking it out
g2g link --branch feature/top --from graphite # pin a source
g2g link --branch feature/top --trunk main    # pin a Graphite multi-trunk ancestry's trunk
g2g link --branch feature/middle --scope path # stop at the selected branch
g2g link --branch feature/top --apply         # revalidate, then let gh create or update it
```

The preview renders the selected stack once, as a fixed-indent column because
the path is linear: the trunk marked, the branches bottom-to-top, and pull
request numbers and state in their own column. It always shows the exact
`gh stack link` command it validated, even when apply is blocked, because the
command is the plan's destination and running it by hand is a legitimate way to
get gh's own, often more specific, error. A blocked preview states the reason
above the command and heads it `Command to run once unblocked`. `--apply`
re-discovers and revalidates, prints one `Ready to apply` graph and command,
flushes that output, and invokes it. Copying the displayed command by hand is a
separate, deliberate snapshot and does not make `g2g` re-resolve anything.

`g2g unlink` previews removal of a GitHub-native stack relationship. It
discovers the stack number from the selected path, the same batched read
`status` uses, so the number does not have to be copied by hand. Discovery
refuses rather than guesses: a path that is not linked, or that spans more than
one stack, is an error naming `--stack-number`, which remains available to
choose deliberately and always wins. `--apply` invokes the supported
`gh stack unstack <number>` after the selected structure and PR path are
revalidated. It never changes Graphite, branches, pull-request metadata, review
state, or PR lifecycle.

#### status

`g2g status` is the read-only first step for triage, and never changes GitHub
or Graphite. It renders the selected stack from the resolved structure — a
chain as a flat column, a fork as a tree — with its open PR mappings and
blocked relationships highlighted, reports each branch against **its own
parent** rather than whichever sibling sorts first, and says which record
described it. A branch no source describes is
rendered as such rather than refused: "nothing is stacked here" answers what
was asked.

The same bounded GitHub PR read reports native stack number, size, and position
for each selected PR, without a checkout or a second graph, and marks the
members of the native stack running through the tree. A healthy path ends with one compact `GitHub stack #… · selected
path … · aligned` line; only missing or conflicting membership is annotated on
individual nodes.

Each branch is annotated one axis at a time, so a mark means one thing and
carries its own colour. `base✓` says the pull request is based where the
resolved structure puts it, and `base✗` says it is not; `head✗` says the pull
request is not on the commit the branch is, and only ever appears when that is
true, so a current one stays unannotated and the stale ones stand out. `pr✗`
is a pull request that is missing, closed, or ambiguous — not a statement about
a base, because a branch with no pull request has no base to be wrong about.
`pr✓` is a merged one, in the ordinary colour: it did what it was for. Read the
column for `✗`.

A branch whose work is already in the branch below it reads as `landed in …`,
in the ordinary colour, and is offered forgetting rather than submitting. That
is a question Git answers and GitHub cannot: a squash merge lands the work
under a pull request whose head the branch never had, and a series somebody
cherry-picked has no pull request at all — so the branch looks like one merely
missing a pull request, and the advice for that is to open one for a change
already in the trunk.

#### comment

GitHub shows a pull request in isolation. `comment` keeps one comment on each
pull request in the stack that lists the rest of it, with that pull request in
bold, so a reviewer can move through the stack without reading bases.

```sh
g2g comment            # what each comment would say, and which would change
g2g comment --apply
```

```
**Stack**

Merged into `synthetic-main`: #10

- `synthetic-main`
- #11 `synthetic-one`
- **#12 `synthetic-two`** 👈 this pull request
- #13 `synthetic-three`
```

Rerunning edits the comment it finds rather than adding another, found by an
HTML marker in its first line. Pull requests that have merged out of the stack
stay listed: each comment records every pull request the stack has listed, so
the history survives the branch being pruned and deleted. A merged pull request
is never given a new comment, a comment you cannot edit is left alone, and a
branch with two open pull requests refuses the run.

`submit` and `land` keep the comments too, as their last act, because they
change which pull requests the stack is made of: `submit` once the pull
requests are opened and linked, `land` on what remains above the branches it
landed. Both say so in their preview, and `--no-comment` skips it. If keeping
the comments fails there, the command's own work stands and it exits `3`,
naming `g2g comment --apply`. `push`, `retarget` and `link` never touch them.

It keeps the whole stack the branch belongs to, whichever branch you run it
from, and so has no `--scope`: each comment lists its own pull request's
ancestors and descendants — a fork appears in some comments and not others,
and keeping only part of a stack would leave the rest describing a different
one. Run from a trunk, it keeps every stack on it. See
[design-docs/stack-comment.md](design-docs/stack-comment.md).

### Land

When a stack is finished, `land` takes it down onto its trunk, bottom branch
first, without waiting for CI. It lands the path from the trunk to the branch
you stand on (or `--branch`), so from the top that is the whole stack.

```sh
g2g land                 # the descent, as the commands it would run
g2g land --apply
g2g land --apply --admin # merge without waiting for restarted checks
```

Each branch in turn is published, merged, and then forgotten and deleted, and
the branches above it are replayed onto the advanced trunk before the next one
goes. Only the branch about to merge is pushed: republishing the whole stack
after every merge restarts the checks on every branch above it, which is the
cost this exists to avoid.

The preview is the recipe. Every line is a command you could run yourself, in
order, so driving it by hand is a first-class option rather than a fallback:

```
Commands this would run, in order
   1  gh pr merge 41 --squash  · land synthetic-one
   2  g2g sync --apply  · advance the trunk and replay what is left onto it
   3  g2g prune --branch synthetic-one --scope branch --apply  · forget it, once what sat on it has been reparented
   4  git push origin --delete synthetic-one  · remove the published branch, if the merge has not already
   5  git branch -D synthetic-one  · remove it here
   6  g2g push --branch synthetic-two --scope path --apply  · publish it as it is here
   7  gh pr edit 42 --base synthetic-main  · merge into synthetic-main rather than synthetic-one
   8  gh pr merge 42 --squash --admin  · land synthetic-two
   …
```

**It needs the stack in g2g's own graph.** Landing reads pull requests from
whichever source describes the stack, and then replays, reparents and forgets in
g2g's graph — and those are not the same record. Pointed at a Graphite-described
stack, it would merge every pull request and then find nothing to replay and
nothing to forget, so it refuses and names `g2g track --stack`.

`land` owns no rules of its own. Publishing goes through `push`, which refuses
a branch the remote has moved on; advancing and replaying go through `sync`,
which refuses a trunk that has diverged; and "has this landed" is asked of Git
by content, through the same check `prune` uses, because a squash merge is
invisible to a pull request's head. It aims every pull request at the trunk
rather than at the branch below, because by the time a branch's turn comes the
branch below has merged and gone.

It refuses the whole descent before merging anything. Discovering the fourth
branch is a draft after the first three have merged is not a refusal, it is a
half-landed stack.

**On a protected repository you will need `--admin`.** Every branch above the
first is force-pushed by its own replay, which restarts the required checks
that were green a moment ago, so GitHub reports it blocked. That is inherent
rather than incidental, and the preview says so before the first merge instead
of letting the run discover it at the second branch. `--admin` also bypasses
approvals, so a pull request nobody has approved is refused under its own name
rather than folded into the protection refusal.

Each deletion is on by default and can be turned off on its own:
`--no-delete-remote`, `--no-delete-local`. Forgetting the landed branch in
g2g's graph cannot be: the branches above it are reparented onto the trunk as it
goes, and leaving it recorded would put them under a branch that no longer
exists. None of the cleanups can stop a descent — the work is merged, and a ref
that would not delete is untidiness, not a failed land. A branch the remote
deleted on merge is already in the state it was asked for.

Once the descent is done, `land` keeps the stack comments on what remains above
the branches it landed, so those pull requests list what merged as history.
It is the last line of the recipe, and `--no-comment` skips it.

`--method squash|merge|rebase` defaults to squash, and is refused up front if
the repository does not allow it. Squash is the case a stack needs help with:
the other two leave each parent's commits in its child under the same identity,
so nothing needs replaying between merges.

See [design-docs/land.md](design-docs/land.md) for why each of those is the way
it is, including the three publishing decisions that were wrong first.

### Graphite alignment

Once g2g adopts a branch it stops asking Graphite about it, so without these
`gt log` would keep showing a structure that is quietly wrong.

```sh
g2g mirror              # what would it take for Graphite to agree?
g2g mirror --apply
g2g mirror --prune --apply   # also untrack, in Graphite, what g2g does not record

g2g import              # adopt what Graphite declares into g2g's graph
g2g import --apply
```

**Neither command ever removes a branch from g2g's graph.** This keeps the two
records in step; it does not hand ownership over.

`mirror` writes only Graphite, and is the only command that does. Its `--prune`
is opt-in, because "this branch's work has landed" is certain and "Graphite
knows a branch we do not" is not — it is just as likely to be one you tracked
in `gt` on purpose. A prune also refuses a branch whose child g2g *does* know,
because `gt untrack` takes the whole subtree with it.

`import` writes only g2g's graph, and it is additive: it refuses a branch
g2g already records under a different parent rather than silently reverting a
deliberate change. Adoption is the authority claim, so afterwards g2g answers
for everything it adopted — and `--from graphite` is how you see Graphite's view
of them again.

Both refuse outright in a repository that does not already use Graphite. Reading
Graphite's forest is what enrols you, so even a preview has to stop first.

### Adopting a published stack

A stack someone else published has its structure in one place: the bases of
its pull requests. Rather than switching to each branch and tracking it by
hand, import it from there.

```sh
git fetch
git switch synthetic-their-lower     # every branch of the stack, here
git switch synthetic-their-top
g2g import --from pull-request       # preview the stack of the branch you are on
g2g import --from pull-request --apply
```

It reads the stack exactly as `g2g status --from pull-request` does —
`--branch` picks another branch's, `--scope stack` (the default) or `trunk`
says how much — and records it, so the branches can be restacked. It is the
only import that needs the network, because reading a base invokes `gh`. Its
rules are the Graphite import's: it writes only g2g's graph, adds what is
missing, and refuses a branch g2g already records under a different parent.
Nothing is written to GitHub.

Three things it will not do:

- **Create a branch.** The graph records local branches, so a branch the pull
  requests place that is only on the remote refuses the import by name, with
  `git fetch && git switch <branch>` or `git branch <branch> origin/<branch>`
  as the way out.
- **Make a trunk of a feature branch.** The stack must start from the
  repository's default branch (what `refs/remotes/origin/HEAD` names) or from a
  branch g2g already records; otherwise it names the `g2g track` that
  establishes one.
- **Take a base's tip as the fork point.** The base may have moved since the
  pull request was opened, so each fork point is where the branch and its base
  last agreed. A base that is not an ancestor of its branch is still recorded,
  and the preview says the branch will read as needing a restack.

## When a command refuses

A blocked preview names the command that repairs the state rather than leaving
the reader to work it out. `status` gives the same advice, phrased as a next
step, and `--json` carries it as `repair`.

| State | Way out |
|---|---|
| A pull request has merged | `g2g sync` for a stack g2g records, `gt sync` for one Graphite declares; none for one read from pull request bases, which nothing here records |
| A branch's work has already landed | `g2g prune`, or `gt sync` for a Graphite-declared stack |
| A branch has no pull request, or one closed without merging | `g2g submit` |
| A pull request is open on the wrong base | `g2g retarget` |
| Two open pull requests for one branch | none — close all but one; a person has to choose, and the preview says so |
| The remote has moved on a branch `push` would publish | fetch and reconcile first, or `git push --force-with-lease <remote> <branch>` to replace what is published |
| A branch and its published version have both moved | `g2g sync --take published`, bounded with `--through`, or reconcile it yourself |
| A tracked branch was deleted with plain Git | `g2g untrack --branch <branch>` |
| A branch was rebased by hand (moved off its parent) | re-record it with `g2g track` |
| A branch that has to move is checked out in another worktree | switch that worktree away or close it, or select less with `--branch` or `--scope` |
| `fold` into a parent that has moved on since the branch was stacked on it | `g2g restack --branch <branch>`, then fold |
| `fold` into a trunk | `g2g land --branch <branch>`: a branch joins its trunk through its pull request |
| `delete`, `fold` or `rename` of a branch the g2g graph does not record | `g2g track --branch <branch>`, or plain `git branch`, which is all it would do |

## Output

### Colour and hyperlinks

Colour is enabled only for an interactive terminal. It is disabled for
redirected output, CI, `NO_COLOR`, and `TERM=dumb`, so the plain graph is
deterministic for scripts. In colour output, headers, trunks, branches, PR
numbers, unresolved state, and success use distinct restrained roles; the
renderer keeps plan data separate from ANSI decoration.

Pull request numbers are hyperlinks where the terminal supports them. The text
is unchanged — `#42` reads as `#42` either way — so a terminal without OSC 8
support loses nothing. Links follow the same interactive-terminal rule as colour
but deliberately ignore `NO_COLOR`, which asks for output without colour and a
hyperlink is not colour. `--no-links` turns them off; `--json` and `--porcelain`
never emit them.

A number points at GitHub when GitHub reported an address for it — that came
back from the API rather than being assembled, so it cannot be wrong about the
repository — and otherwise at Graphite's view of the same pull request
(`https://app.graphite.com/github/pr/<owner>/<name>/<number>`), which a
repository that does not use Graphite never produces.

### Copyable commands

Nothing but whitespace ever shares the line holding a copyable command: no
prompt character, border, or annotation, so a loose, wrapped, or whole-line
selection can only pick up spaces, which a shell ignores. In colour output the
highlight is padded a few columns past the command to widen the click target,
and every highlighted command, on its own line or inside a sentence, carries a
column of background on each side. Those columns are painted rather than
written: without colour the text reads exactly as it was typed.

A hint that names a command mid-sentence draws it with the same highlight, so
the reader can see where it starts and ends without reading the prose around
it. Which words are a command is recorded when the sentence is written rather
than found by a pattern afterwards, so what is highlighted is exactly what can
be copied and run. Plain output, `--json`, and `--porcelain` carry the
sentences unchanged.

### Machine-readable output

Every command renders one semantic view, and `--json` and `--porcelain` are
alternative renderers over exactly the facts the graph shows, so nothing has to
parse decorated terminal text. Both suppress colour and every human-facing
line, emitting only the document. They are mutually exclusive; the default
stays the human-readable preview.

```sh
# One JSON object with a schemaVersion, the trunk, each branch's pull request
# and state, and the validated command when one applies.
g2g status --json

# Stable tab-separated records, each led by its type:
#   target  <branch> <source>
#   trunk   <branch>
#   branch  <name> <pr> <state> <severity> <url> <target?> <parent?>
#   blocked <reason>
#   repair  <reason>
#   way     <command> <effect>
#   command <argv>...
#   step    <n> <command> <effect>
#   note    <severity> <text>
#   comment <pr> <branch> <action> <reason>
g2g link --porcelain
```

`parent` is populated by `g2g graph`, where order alone cannot express
structure once a graph forks; the linear commands leave it empty and their
order still holds. In porcelain it is appended after the fields that shipped
before it, so an existing reader keeps working. `blocked` is reported alongside `command`, not instead of it, so a consumer can
see the destination and decide for itself; check `blocked` before acting on
`command`. `schemaVersion` is bumped when a field changes meaning or
disappears; adding a field is not a breaking change.

`repair` is what to do about `blocked`, with each way out carried as a command
and what running it achieves, so a consumer no longer has to find the command
inside the sentence. A way out with no `command` is a whole answer that is not
a thing to run — "fetch and reconcile first" — rather than a step with a field
missing, and a refusal that nothing here fixes carries no `repair` at all. It
is also reported where nothing is blocked and there is still something to do: a
branch no source describes is a state, not a refusal.

`sequence` is the ordered recipe for a command whose work is several steps
(`land`, `create`), and `comments` carries each pull request comment a
`comment` run writes, with its body in `--json`; porcelain says what happens to
each comment but not its text, since a body is many lines.

Schema 2 narrowed `blocked` to the reason alone. It used to carry the label a
person is shown in front of it, which differed between commands; that label is
the renderer's now.

### Diagnostics

```sh
g2g --debug link --branch feature/top
```

`--debug` is a root flag and may appear before or after any command. Its output
goes only to stderr, so stdout keeps the normal preview. It does not change
discovery, timeouts, checkout behaviour, or mutations. Its records summarize
supported Graphite discovery, the selected path, batched GitHub PR facts for
`link`, including native stack number and position, or the selected remote and
atomic leased Git argv for `push`, plus plan/revalidation decisions and bounded
subprocess status. It never logs environment values, credentials, auth headers,
cookies, or GraphQL query payloads.

### Timeouts

```sh
g2g --timeout 3m submit --spec "$spec_dir/submission.json" --apply
```

Discovery and mutation are bounded separately. Discovery and revalidation get
45 seconds; the mutation phase gets its own budget of 60 seconds plus 30 per
selected branch (180 per branch for `land`, which waits on GitHub between its
calls), taken fresh rather than from whatever discovery left over, so a slow
read can never cancel a push or pull-request creation halfway. The root
`--timeout` flag replaces both ceilings, for a slow network or a deep stack. A
mutation that does expire says so explicitly and states what may have already
happened, because an interrupted `submit` can leave refs pushed and some pull
requests created; re-running it with the same spec is safe and creates only
what is missing.

## Compatibility and storage

When Graphite is selected as a source, g2g uses Graphite CLI 1.8.6 as its
tested compact-display baseline; compatible patch/minor versions continue with
a stderr warning, while an unsupported major version or changed display grammar
fails safely. GitHub projection requires a compatible `gh` with the relevant
stack command.

The graph is stored at `$(git rev-parse --path-format=absolute
--git-common-dir)/g2g/graph.json`. Linked worktrees share it, it never appears
in a diff or dirties a checkout, and it is neither pushed nor shared between
clones — a fresh clone starts empty, which matches the unpublished branches
those edges describe. Writes are a temporary file plus a rename, so a
concurrent reader sees either the old graph or the new one. Its
`storeSchemaVersion` is separate from the `--json` output's `schemaVersion`;
an unrecognised store version fails closed rather than being rewritten.

## Development

Start with [AGENTS.md](AGENTS.md), which holds the process knowledge, and the
design doc for the area. Tests use fake executables on `PATH`, or throwaway
local repositories where the question is what Git itself does, so they need
neither authentication nor a network connection. Run them with `go test ./...`.

| Path | Holds |
|---|---|
| `cmd/g2g` | executable entry point |
| `internal/cli` | Cobra command parsing, preview output, and completion |
| `internal/graph` | the forest g2g owns — model, ancestry discovery, and the store; depends on Git alone |
| `internal/shape` | the scope vocabulary and forest traversal every record shares; depends on nothing |
| `internal/stack` | source resolution — which record describes a branch — and selection within it |
| `internal/graphite` | strict, compatibility-gated read-only Graphite display parser |
| `internal/git`, `internal/githubstack` | narrow repository, publication, and PR seams |
| `internal/create`, `internal/navigate` | starting a branch on a recorded one, and moving the checkout |
| `internal/reshape` | deleting, folding and renaming a recorded branch; moves refs, never replays |
| `internal/restack` | the only history-rewriting service, with the journal that makes it resumable |
| `internal/sync`, `internal/prune` | fetch, fast-forward and replay; forgetting landed branches |
| `internal/landed` | the one by-content "has this landed" check every caller shares |
| `internal/push`, `internal/submit` | atomic stack-ref publication; spec-driven pull request creation |
| `internal/retarget` | moving pull request bases to match the resolved stack |
| `internal/link` | GitHub native-stack projection, and the read `status` renders |
| `internal/comment` | the stack comment kept on each pull request |
| `internal/land` | taking a stack down onto its trunk by composing push, sync and prune |
| `internal/align` | `mirror` and `import`, keeping g2g's graph and Graphite's in step, and adopting a stack from its pull requests |
| `internal/repair` | a refusal's reason and its ways out, shared by every package that refuses |
| `internal/parallel` | bounded concurrency for independent per-branch reads |
| `internal/diagnostic` | opt-in, stderr-only `--debug` events and bounded, redacted output |
| `internal/subprocess` | the boundary for `git`, `gt`, and `gh` invocations |
| `internal/testutil` | fake executables installed on `PATH` during tests |
| `design-docs` | concise scope and safety notes |
