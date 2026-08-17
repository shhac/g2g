# g2g

`g2g` is a lightweight Go CLI intended to bridge a Graphite-managed linear
branch stack into GitHub's native stack feature. Graphite remains the source of
truth; `link` discovers a single ordered Graphite stack and, only with an
explicit `--apply`, passes its branches to `gh stack link` from bottom to top.

`g2g` can also keep a branch graph of its own, which needs neither Graphite
nor GitHub. See [g2g-owned graphs](#g2g-owned-graphs).

## Command names

The command is `g2g`, and so is the project. Two names still lag behind and are
being retired in order: the GitHub repository, and the Homebrew formula, which
cannot be renamed until the tap carries a rename mapping or every existing
install silently stops updating. Release assets from tags before the rename keep
the old name, as they should.

The stable v1 command shape is:

```sh
g2g link
```

The `link` command is a safe preview by default. It resolves the checked-out Git
branch as its pivot, reads the Graphite-declared trunk-to-tip linear path, and
inspects matching GitHub pull requests. Its concise output shows a target, one
self-describing graph, and a command only when valid. When at least two
PR-backed branches need linking, it prints the exact proposed bottom-to-top
command. A one-PR path is a successful no-op: it prints `Nothing to link` and
never constructs an invalid `gh stack link` command, whether or not that path
is also blocked. Preview clearly states
that no changes were made; nothing changes unless `--apply` is present.

```sh
# Preview the path ending at the current branch.
g2g link

# Preview a Graphite-tracked local branch without checking it out.
g2g link --branch feature/top

# Use a particular Graphite-declared trunk when the selected ancestry is
# genuinely multi-trunk or when intentionally choosing another valid ancestor.
g2g link --branch feature/top --trunk main

# Revalidate, then allow gh to create/update the native GitHub stack.
g2g link --branch feature/top --apply

# Stop at the selected branch instead of resolving its full linear stack.
g2g link --branch feature/middle --scope path

# Preview an atomic, lease-protected publication of Graphite-selected local
# refs. It reads Graphite but never submits/restacks or invokes GitHub.
# Full-stack expansion is default.
g2g push --branch feature/top

# Revalidate, then advance every selected ref together or none of them.
g2g push --branch feature/top --apply

# Use a configured remote other than origin.
g2g push --remote staging --apply

# Opt-in local diagnostics go only to stderr; stdout keeps the normal preview.
g2g --debug link --branch feature/top

# Raise the per-phase ceiling for a slow network or a deep stack.
g2g --timeout 3m submit --spec "$spec_dir/submission.json" --apply
```

`--help`, `--version`, and `completion bash|zsh|fish` are available; bare
`g2g` shows help when installed through Homebrew. The command uses Graphite CLI
1.8.6 as its tested compact-display baseline and requires a compatible `gh`
with `stack link`.
Compatible Graphite patch/minor versions continue with a stderr warning; an
unsupported major version or changed display grammar fails safely. Its tests
use fake executables on `PATH`, so they need neither authentication nor a
network connection.

Discovery and mutation are bounded separately. Discovery and revalidation get
45 seconds; the mutation phase gets its own budget of 60 seconds plus 30 per
selected branch, taken fresh rather than from whatever discovery left over, so
a slow read can never cancel a push or pull-request creation halfway. The root
`--timeout` flag replaces both ceilings. A mutation that does expire says so
explicitly and states what may have already happened, because an interrupted
`submit` can leave refs pushed and some pull requests created; re-running it
with the same spec is safe and creates only what is missing.

`--debug` is a root flag and may appear before or after any command. It
does not change discovery, timeouts, checkout behavior, or mutations. Its
stderr-only records summarize supported Graphite discovery, the selected path,
batched GitHub PR facts for `link`, including native stack number and
position, or the selected remote and atomic leased Git argv for `push`, plus
plan/revalidation decisions and bounded
subprocess status. It never logs environment values, credentials, auth headers,
cookies, or GraphQL query payloads.

A branch is identified by its single open pull request. Closed and merged pull
requests left on a reused branch name are treated as history: they never block
`link`, `sync`, or `status`, and `submit` creates a replacement rather than
skipping the branch. Two or more open pull requests for one branch is the only
ambiguity, and it fails closed.

`g2g` never guesses a trunk from its name. On a Graphite-described stack it
infers the only Graphite-declared trunk on the selected ancestry and shows it
prominently. If that ancestry has multiple declared trunks, it fails closed and
requires `--trunk <branch>`; an override must be both declared by Graphite and
an ancestor of the selected branch. A g2g-owned path has exactly one root, so
`--trunk` can only confirm it — naming any other branch is refused rather than
ignored.

Shell completion for `--branch` and `--trunk` draws on whichever sources
describe the repository, so it works with no Graphite installed. It is
read-only and checkout-free, and it never runs Graphite in a repository that
does not already use it.

Preview renders the selected stack graph once and always shows the exact
`gh stack link` command it validated, including when apply is blocked: the
command is the plan's destination, and running it by hand is a legitimate way
to get gh's own, often more specific, error while triaging. A blocked preview
states the reason above the command and heads it `Command to run once
unblocked` rather than presenting it as the next step. The only command never
shown is one that cannot be constructed — `gh stack link` needs at least two
branches, so a single-branch path prints `Nothing to link` instead.
Every stack g2g projects onto GitHub is linear, so this graph is
a fixed-indent column rather than an escalating tree: the trunk is marked, the
branches stacked on it follow bottom-to-top, and pull-request numbers and state
line up in their own column. Blank lines bound the graph and each block below
it. `--apply` re-discovers and revalidates before it prints one `Ready to
apply` graph and command, flushes that output, and invokes the command. On success it prints a concise confirmation; on
failure it never claims that changes were made. Manually copying the displayed
command is a separate, deliberate snapshot action and does not cause `g2g`
to re-resolve anything.

A blocked preview names the command that repairs the state rather than leaving
the reader to work it out. A branch whose pull request has merged points at
`gt sync`, because the stack itself is stale and only Graphite can restack
around it; a branch with no pull request, or one closed without merging, points
at `g2g submit`; a pull request open on the wrong branch points at `g2g link`.
Two open pull requests for one branch is the only state with no command to
offer, and it says so. `status` gives the same advice, phrased as a next step.

Color is enabled only for an interactive terminal. It is disabled for redirected
output, CI, `NO_COLOR`, and `TERM=dumb`, so the plain graph is deterministic
for scripts. In color output, headers, trunks, branches, PR numbers, unresolved
state, and success use distinct restrained roles; the renderer keeps plan data
separate from ANSI decoration.

Pull request numbers are hyperlinks where the terminal supports them. The text
is unchanged — `#42` reads as `#42` either way — so a terminal without OSC 8
support loses nothing. Links follow the same interactive-terminal rule as color
but deliberately ignore `NO_COLOR`, which asks for output without color and a
hyperlink is not color. `--no-links` turns them off; `--json` and `--porcelain`
never emit them.

A number points at GitHub when GitHub reported an address for it, and at
Graphite's view of the same pull request
(`https://app.graphite.com/github/pr/<owner>/<name>/<number>`) only when it did
not. GitHub wins because its address came back from the API rather than being
assembled, so it cannot be wrong about the repository. A repository that does
not use Graphite simply never produces the second kind.

Nothing but whitespace ever shares the line holding a copyable command: no
prompt character, border, or annotation, so a loose, wrapped, or whole-line
selection can only pick up spaces, which a shell ignores. In color output the
highlight is padded a few columns past the command purely to widen the click
target.

## g2g-owned graphs

`g2g graph`, `g2g track`, and `g2g untrack` maintain a branch forest g2g
owns itself. They read Git and nothing else: no Graphite, no GitHub, no
network. This is the structure that exists for branches you have not pushed
yet, and it is the only place a fork can live — GitHub native stacks are
linear, and a pull request base cannot describe a branch that has no pull
request.

Every branch has at most one parent, a parent may have many children, and a
repository may have several roots. See
[design-docs/g2g-owned-graphs.md](design-docs/g2g-owned-graphs.md) for the
model, the storage decisions, and what is deliberately left out.

```sh
# Record the whole stack you are on, in one step. This is where to start.
g2g track --stack
g2g track --stack --trunk main --apply

# Preview the candidate parents of one branch. It refuses to choose.
g2g track

# Record one parent. Preview first; --apply writes.
g2g track --branch feature/login --parent feature/auth
g2g track --branch feature/login --parent feature/auth --apply

# Inspect the graph. Scope widens from one branch to every stack.
g2g graph                                    # root to the selected branch
g2g graph --scope subtree                    # the branch and its descendants
g2g graph --branch feature/login --scope trunk   # every stack on that trunk
g2g graph --scope all                        # every stack in the repository

# Remove edges. --scope subtree removes descendants too.
g2g untrack --branch feature/auth --apply
g2g untrack --branch feature/auth --scope subtree --apply

# Replay commits so branch contents match that structure.
g2g restack --branch main --scope stack --apply
```

`--stack` records a whole existing stack at once, which is almost always what a
repository that predates g2g needs. You assert one thing — the trunk, and even
that is inferred when only one recorded root is an ancestor — and the shape
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
first — it is the fact that explains why the branch will then read as needing
one.

A trunk that has moved on is no longer an ancestor of the branches built from
it, so recorded roots are always offered as candidates. Adopting the very first
branch into an empty graph has neither, so it falls back to measuring from the
fork point instead; that costs one Git call per local branch and runs only when
the cheap paths found nothing.

`graph` renders a fork with connectors and a chain as the same flat column
every other command uses, because a chain has no structure that indentation
would add. It reports two kinds of staleness and repairs neither:

- **needs restack** — the recorded parent moved underneath the branch.
- **parent missing** — the recorded parent is no longer a local branch, which
  is what a squash-merged and deleted parent looks like.

Untracking a branch in the middle leaves its children pointing at it and says
so. Reparenting them onto the grandparent would invent an edge you never asked
for.

The graph is stored at `$(git rev-parse --path-format=absolute
--git-common-dir)/g2g/graph.json`. Linked worktrees share it, it never appears
in a diff or dirties a checkout, and it is neither pushed nor shared between
clones — a fresh clone starts empty, which matches the unpublished branches
those edges describe. Writes are a temporary file plus a rename, so a
concurrent reader sees either the old graph or the new one. Its
`storeSchemaVersion` is separate from the `--json` output's `schemaVersion`;
an unrecognised store version fails closed rather than being rewritten.

### Restacking

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
preview knows in advance whether the rewrite applies, so it can tell you
before you commit to it:

```
Replays feature/auth and feature/login onto main.
Applies without touching your working tree or checked-out branch.
```

When it cannot apply cleanly it says so *before* you apply, because rebasing
then happens in your own working tree — resolving a conflict needs a tree you
can edit with your own tools:

```
This will not apply cleanly. Applying rebases in your working tree and stops
on the conflict for you to resolve, then g2g restack --continue.
```

Resolve the conflict, `git add` the files, and run `g2g restack --continue`.
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

## Where structure comes from

Every command that selects a stack asks one question first: which source
describes this branch?

```
adopted into g2g's store  →  g2g's own graph
tracked by Graphite         →  Graphite
neither                     →  refused, with the remedy
```

Adoption wins because recording an edge is you saying you want g2g to own
the branch. The answer is worked out per branch, every time, and never stored —
so moving a branch between sources is just `g2g track` or `g2g untrack`, in
either direction, and there is no record to go stale.

`link`, `push`, and `submit` therefore work on a stack g2g owns, with no
Graphite installed. And **g2g will not run Graphite in a repository that does
not already use it** — Graphite's discovery creates state, so being asked
whether it applies must not be what enrols you.

`restack` is the exception, and deliberately: it needs a fork point, which only
g2g's own store records. It refuses a Graphite-owned branch and says to
`g2g track` it first. Authority governs what may be changed, not what may be
read.

Authority is about which source answers, **not** about exclusivity. A branch can
sit in g2g's graph, be tracked by Graphite, and appear in a GitHub stack all at
once, and nothing here removes it from any of them.

### Seeing the other source

```sh
g2g status --from graphite    # what does Graphite think this stack is?
g2g push --from g2g
```

`--from` pins the source for one command. Once a branch is adopted there is
otherwise no way to ask Graphite what it thinks of it, and comparing the two
views is exactly what you want before reconciling them. Nothing is recorded, so
there is still nothing that can go stale.

## Keeping Graphite in step

Adopting a branch used to strand Graphite: g2g stopped asking it, and nothing
put it back in step, so `gt log` kept showing a structure that was quietly
wrong.

```sh
g2g mirror              # what would it take for Graphite to agree?
g2g mirror --apply
g2g mirror --prune --apply   # also untrack, in Graphite, what g2g does not record

g2g import              # adopt what Graphite declares into g2g's graph
g2g import --apply
```

**Neither command ever removes a branch from g2g's graph.** This keeps the two
records in step; it does not hand ownership over.

`mirror` writes only Graphite. Its `--prune` is opt-in,
because "this branch's work has landed" is certain and "Graphite knows a branch
we do not" is not — it is just as likely to be one you tracked in `gt` on
purpose. A prune also refuses a branch whose child g2g *does* know, because
`gt untrack` takes the whole subtree with it.

`import` writes only g2g's graph, and it is additive: it refuses a branch
g2g already records under a different parent rather than silently reverting a
deliberate change. Adoption is the authority claim, so afterwards g2g answers
for everything it adopted — and `--from graphite` is how you see Graphite's view
of them again.

Both refuse outright in a repository that does not already use Graphite. Reading
Graphite's forest is what enrols you, so even a preview has to stop first.

## Staying up to date

```sh
# Fetch, fast-forward the base, replay the stack, forget what has landed.
g2g sync

g2g sync --apply
g2g sync --apply --prune=false   # keep landed branches in the graph
```

This is `git switch main && git pull && git switch back && restack` in one
command, and it needs no Graphite.

The fetch writes only into `refs/g2g/remotes/`, so your own remote-tracking
refs, `FETCH_HEAD`, and ahead/behind counts are untouched. The base is
**fast-forwarded or not at all**: a base that has diverged is reported, never
merged or reset, because "you are behind" and "you have diverged" want
different responses and only you can give the second.

Pruning forgets a landed branch in the recorded graph. It never deletes a
branch — that is a separate, deliberate act, not the tail of another command.

## Retargeting pull request bases

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
has, and the base it would get.

It touches only the pull requests whose base disagrees with the resolved stack,
leaves branches with no pull request to `submit`, ignores merged and closed
ones, and refuses outright when a branch has more than one open pull request,
because nothing here can tell which one you meant.

## Machine-readable output

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
#   target <branch> <source>
#   trunk  <branch>
#   branch <name> <pr> <state> <severity> <url> <target?> <parent?>
#   command <argv>...
#   note   <severity> <text>
g2g link --porcelain
```

`parent` is populated by `g2g graph`, where order alone cannot express
structure once a graph forks; the linear commands leave it empty and their
order still holds. In porcelain it is appended after the fields that shipped
before it, so an existing reader keeps working.

`blocked` is reported alongside `command`, not instead of it, so a consumer can
see the destination and decide for itself; check `blocked` before acting on
`command`. `schemaVersion` is bumped when a field changes meaning or
disappears; adding a field is not a breaking change.

Interactive confirmation or a cancellation/cooldown period before mutation is
intentionally deferred; it needs a separate safety design and is not implied by
the current `--apply` flow.

## Status and recovery

`g2g status` is the read-only first step for triage. It renders one selected
Graphite path with its open PR mappings and highlights blocked relationships.
The same bounded GitHub PR read reports native stack number, size, and position
for each selected PR, without checkout or a second graph. A healthy selected
path ends with one compact `GitHub stack #… · selected path … · aligned` line;
only missing or conflicting membership is annotated on individual nodes. It
never changes GitHub or Graphite.

`g2g unlink` previews removal of a GitHub-native stack relationship. It
discovers the stack number from the selected path, the same batched read
`status` uses, so the number no longer has to be copied by hand. Discovery
refuses rather than guesses: a path that is not linked, or that spans more than
one stack, is an error naming `--stack-number`, which remains available to
choose deliberately and always wins. `--apply` invokes the supported
`gh stack unstack <number>` after the selected Graphite/PR path is revalidated.
It never changes Graphite, branches, pull-request metadata, review state, or PR
lifecycle.

## Submitting pull requests

`g2g submit` is a preview-first recovery path when Graphite owns a local stack
but its own submit flow cannot publish it. With `--apply`, it validates the
complete spec, revalidates immediately before mutation, performs one atomic
lease-protected push, creates only missing PRs bottom-to-top as drafts,
preserves existing PRs, then links the complete stack. It never invokes
`gt submit`, restacks Graphite, or retargets an existing PR.

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

## Homebrew

```sh
brew install shhac/tap/g2g
g2g link
```

The formula was once named `gt2gh`. The tap maps the old name to the new one,
so an existing install migrates on `brew update` rather than quietly stopping
at the last version published under it. Release archives from tags before the
rename keep the old asset name, which is why a download from one of those
unpacks a binary called `gt2gh`.

`g2g push` is a deliberately narrow publication escape hatch for a
Graphite-managed linear path. It reads Graphite to discover that path, but never
submits, restacks, or otherwise changes Graphite and never invokes `gh`. It
previews `git push --atomic --force-with-lease origin <branches>` by default and
requires `--apply` to run that exact one invocation. `--remote` defaults to
`origin` and must name a configured remote. Every selected non-trunk branch is
pushed bottom-to-top; atomic push means they all advance together or none do.
Unsupported atomic pushes and rejected leases fail without a non-atomic or
unsafe-force fallback. This does not replace Graphite's ownership of tracking,
restacking, or submission.

Use `push` only as a publication-only recovery path when Graphite remains the
owner of a stack but its normal submission flow cannot publish already-prepared
refs because GitHub native-stack restrictions intervene. After a successful
atomic push, return to Graphite for stack management and submission; `g2g`
does not retarget pull requests or repair Graphite state.

## Scope: how much of the stack

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
| `all` | every stack in the repository (`graph` only) |

Defaults differ because the commands differ. `status` and `graph` default to
`stack` — reading is free, so show where you are, ancestors and descendants
both. `restack` defaults to `subtree`, because rewriting is not free: a conflict
below you may be one you are deliberately deferring, and replaying it uninvited
is how restacking from the middle walks into it every time.

```sh
g2g status                   # where am I: the trunk, me, and everything above
g2g status --scope path      # just the trunk down to me
g2g restack --apply          # me and what depends on me
g2g graph --scope all        # every stack in the repository
```

A GitHub native stack is linear, so `link`, `submit`, `push` and `retarget`
take `stack` or `path` only, and refuse a selection that forks — naming the
remedy rather than choosing a line. Selecting a leaf is that remedy and needs no
flag: a leaf has no descendants, so `stack` collapses to an ordered path by
itself.

`status` reports each branch against **its own parent** rather than against
whichever sibling happens to sort first, marks which branches belong to the
GitHub native stack running through the tree, and says which record described
the structure — that is resolved per branch and per invocation rather than
stored.

## Structure

- `cmd/g2g`: executable entry point.
- `internal/cli`: Cobra command parsing, preview output, and completion.
- `internal/graphite`: strict, compatibility-gated read-only Graphite display parser.
- `internal/git`, `internal/githubstack`: narrow repository, publication, and
  PR seams.
- `internal/graph`: the branch forest g2g owns itself — model, ancestry
  discovery, and the store under the Git common directory.
- `internal/restack`: the only history-rewriting service, with the journal that
  makes an interrupted rewrite resumable.
- `internal/link`: Graphite-authoritative plan/apply orchestration.
- `internal/push`: Git-only atomic stack-ref publication planning.
- `internal/subprocess`: boundary for `git`, `gt`, and `gh` invocations.
- `internal/testutil`: fake executables installed on `PATH` during tests.
- `design-docs`: concise scope and safety notes.

Run the test suite with `go test ./...`.
