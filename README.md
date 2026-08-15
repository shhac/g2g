# gt2gh

`gt2gh` is a lightweight Go CLI intended to bridge a Graphite-managed linear
branch stack into GitHub's native stack feature. Graphite remains the source of
truth; `link` discovers a single ordered Graphite stack and, only with an
explicit `--apply`, passes its branches to `gh stack link` from bottom to top.

`gt2gh` can also keep a branch graph of its own, which needs neither Graphite
nor GitHub. See [g2g-owned graphs](#g2g-owned-graphs).

## Command names

`gt2gh` remains the project, repository, release-asset, and Homebrew formula
name. Homebrew installs its executable as `g2g`; the examples below therefore
use `g2g`. A source build or unrenamed release archive uses `gt2gh` instead.

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
g2g link --branch feature/middle --no-stack

# Preview Graphite-authoritative reconciliation for existing GitHub PRs.
g2g sync --branch feature/top

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

`--debug` is a root flag and may appear before or after `link`, `sync`, `push`,
or `submit`. It
does not change discovery, timeouts, checkout behavior, or mutations. Its
stderr-only records summarize supported Graphite discovery, the selected path,
batched GitHub PR facts for `link`/`sync`, including native stack number and
position, or the selected remote and atomic leased Git argv for `push`, plus
plan/revalidation decisions and bounded
subprocess status. It never logs environment values, credentials, auth headers,
cookies, or GraphQL query payloads.

A branch is identified by its single open pull request. Closed and merged pull
requests left on a reused branch name are treated as history: they never block
`link`, `sync`, or `status`, and `submit` creates a replacement rather than
skipping the branch. Two or more open pull requests for one branch is the only
ambiguity, and it fails closed.

`gt2gh` never guesses a trunk from its name. It infers the only
Graphite-declared trunk on the selected ancestry and shows it prominently. If
that ancestry has multiple declared trunks, it fails closed and requires
`--trunk <branch>`; an override must be both declared by Graphite and an
ancestor of the selected branch.

Preview renders the selected stack graph once and always shows the exact
`gh stack link` command it validated, including when apply is blocked: the
command is the plan's destination, and running it by hand is a legitimate way
to get gh's own, often more specific, error while triaging. A blocked preview
states the reason above the command and heads it `Command to run once
unblocked` rather than presenting it as the next step. The only command never
shown is one that cannot be constructed — `gh stack link` needs at least two
branches, so a single-branch path prints `Nothing to link` instead.
Every stack gt2gh projects onto GitHub is linear, so this graph is
a fixed-indent column rather than an escalating tree: the trunk is marked, the
branches stacked on it follow bottom-to-top, and pull-request numbers and state
line up in their own column. Blank lines bound the graph and each block below
it. `--apply` re-discovers and revalidates before it prints one `Ready to
apply` graph and command, flushes that output, and invokes the command. On success it prints a concise confirmation; on
failure it never claims that changes were made. Manually copying the displayed
command is a separate, deliberate snapshot action and does not cause `gt2gh`
to re-resolve anything.

A blocked preview names the command that repairs the state rather than leaving
the reader to work it out. A branch whose pull request has merged points at
`gt sync`, because the stack itself is stale and only Graphite can restack
around it; a branch with no pull request, or one closed without merging, points
at `g2g submit`; a pull request open on the wrong branch points at `g2g sync`.
Two open pull requests for one branch is the only state with no command to
offer, and it says so. `status` gives the same advice, phrased as a next step.

Color is enabled only for an interactive terminal. It is disabled for redirected
output, CI, `NO_COLOR`, and `TERM=dumb`, so the plain graph is deterministic
for scripts. In color output, headers, trunks, branches, PR numbers, unresolved
state, and success use distinct restrained roles; the renderer keeps plan data
separate from ANSI decoration.

Nothing but whitespace ever shares the line holding a copyable command: no
prompt character, border, or annotation, so a loose, wrapped, or whole-line
selection can only pick up spaces, which a shell ignores. In color output the
highlight is padded a few columns past the command purely to widen the click
target.

## g2g-owned graphs

`g2g graph`, `g2g track`, and `g2g untrack` maintain a branch forest gt2gh
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
# Preview the candidate parents of the current branch. It refuses to choose.
g2g track

# Record a parent. Preview first; --apply writes.
g2g track --branch feature/login --parent feature/auth
g2g track --branch feature/login --parent feature/auth --apply

# Inspect the graph. Scope widens from one branch to the whole tree.
g2g graph                                    # root to the selected branch
g2g graph --scope subtree                    # the branch and its descendants
g2g graph --branch feature/login --scope graph   # the whole tree it belongs to

# Remove edges. --scope subtree removes descendants too.
g2g untrack --branch feature/auth --apply
g2g untrack --branch feature/auth --scope subtree --apply
```

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

**gt2gh does not rebase.** It records structure and never repairs contents.
Under squash merges this matters: when the bottom branch of a stack merges,
GitHub retargets the child's pull request but the child still carries the
original pre-squash commits, so its diff shows the parent's changes a second
time. `g2g graph` reports the branches affected; something else has to fix
them. See the design doc for the intended route when this changes.

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
complex Markdown bodies are preserved exactly. Missing PRs default to drafts;
use `--ready` only deliberately. If apply fails, the spec remains in place and
the error gives exact repair, validation, and retry commands.

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

Homebrew keeps the formula name as `gt2gh` but installs the executable as
`g2g`:

```sh
brew install shhac/tap/gt2gh
g2g link
```

A source build or unrenamed release archive keeps the release-asset name:

```sh
gt2gh link
```

`g2g sync` is also preview-first. Its one graph labels each selected branch's
PR and aligned, divergent, missing, or unsafe state, then shows an exact command
only when applicable. It can reconcile the native stack only with `--apply` and
deliberately refuses to create a PR for a Graphite-only branch or repair a
closed/non-open PR.

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

All three commands resolve the full declared linear stack by default: they treat
the selected branch as a pivot and extend through a unique downward child chain
to its tip. They do not checkout a branch, include no siblings, and fail rather
than guessing when a descendant fork makes the extension ambiguous. `--no-stack`
is the explicit safe opt-out: it stops at the selected branch and uses only its
declared trunk-to-selected path.

## Structure

- `cmd/gt2gh`: executable entry point.
- `internal/cli`: Cobra command parsing, preview output, and completion.
- `internal/graphite`: strict, compatibility-gated read-only Graphite display parser.
- `internal/git`, `internal/githubstack`: narrow repository, publication, and
  PR seams.
- `internal/graph`: the branch forest gt2gh owns itself — model, ancestry
  discovery, and the store under the Git common directory.
- `internal/link`: Graphite-authoritative plan/apply orchestration.
- `internal/push`: Git-only atomic stack-ref publication planning.
- `internal/subprocess`: boundary for `git`, `gt`, and `gh` invocations.
- `internal/testutil`: fake executables installed on `PATH` during tests.
- `design-docs`: concise scope and safety notes.

Run the test suite with `go test ./...`.
