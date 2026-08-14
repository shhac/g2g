# gt2gh

`gt2gh` is a lightweight Go CLI intended to bridge a Graphite-managed linear
branch stack into GitHub's native stack feature. Graphite remains the source of
truth; `link` discovers a single ordered Graphite stack and, only with an
explicit `--apply`, passes its branches to `gh stack link` from bottom to top.

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
never constructs an invalid `gh stack link` command. Preview clearly states
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
```

`--help`, `--version`, and `completion bash|zsh|fish` are available; bare
`g2g` shows help when installed through Homebrew. The command requires Graphite CLI 1.8.6 exactly for its
supported display grammar and a compatible `gh` with `stack link`. Its tests
use fake executables on `PATH`, so they need neither authentication nor a
network connection.

`--debug` is a root flag and may appear before or after `link`, `sync`, `push`,
or `submit`. It
does not change discovery, timeouts, checkout behavior, or mutations. Its
stderr-only records summarize supported Graphite discovery, the selected path,
batched GitHub PR facts for `link`/`sync`, including native stack number and
position, or the selected remote and atomic leased Git argv for `push`, plus
plan/revalidation decisions and bounded
subprocess status. It never logs environment values, credentials, auth headers,
cookies, or GraphQL query payloads.

`gt2gh` never guesses a trunk from its name. It infers the only
Graphite-declared trunk on the selected ancestry and shows it prominently. If
that ancestry has multiple declared trunks, it fails closed and requires
`--trunk <branch>`; an override must be both declared by Graphite and an
ancestor of the selected branch.

Preview renders the selected stack graph once and always shows the exact
`gh stack link` command it validated. `--apply` re-discovers and revalidates
before it prints one `Ready to apply` graph and command, flushes that output,
and invokes the command. On success it prints a concise confirmation; on
failure it never claims that changes were made. Manually copying the displayed
command is a separate, deliberate snapshot action and does not cause `gt2gh`
to re-resolve anything.

Color is enabled only for an interactive terminal. It is disabled for redirected
output, CI, `NO_COLOR`, and `TERM=dumb`, so the plain graph is deterministic
for scripts. In color output, headers, trunks, branches, PR numbers, unresolved
state, and success use distinct restrained roles; the renderer keeps plan data
separate from ANSI decoration, leaving room for a future structured format
without scraping terminal text.

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

`g2g unlink --stack-number <number>` previews removal of a GitHub-native stack
relationship. `--apply` invokes the supported `gh stack unstack <number>` after
the selected Graphite/PR path is revalidated. It never changes Graphite,
branches, pull-request metadata, review state, or PR lifecycle. The stack
number remains explicit until status can safely discover native membership.

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
- `internal/graphite`: strict, version-pinned read-only Graphite display parser.
- `internal/git`, `internal/githubstack`: narrow repository, publication, and
  PR seams.
- `internal/link`: Graphite-authoritative plan/apply orchestration.
- `internal/push`: Git-only atomic stack-ref publication planning.
- `internal/subprocess`: boundary for `git`, `gt`, and `gh` invocations.
- `internal/testutil`: fake executables installed on `PATH` during tests.
- `design-docs`: concise scope and safety notes.

Run the test suite with `go test ./...`.
