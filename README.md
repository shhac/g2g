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
branch as its target, reads the Graphite path from its declared trunk to that
target, and inspects matching GitHub pull requests. Its concise output shows a
target, one self-describing graph, and a command only when valid. When at least two
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

# Treat the selected branch as a pivot and extend it through its one
# unambiguous Graphite descendant chain.
g2g link --branch feature/middle --stack

# Preview Graphite-authoritative reconciliation for existing GitHub PRs.
g2g sync --branch feature/top

# Preview an atomic, lease-protected publication of Graphite-selected local
# refs. This never invokes Graphite or GitHub.
g2g push --branch feature/top --stack

# Revalidate, then advance every selected ref together or none of them.
g2g push --branch feature/top --stack --apply

# Opt-in local diagnostics go only to stderr; stdout keeps the normal preview.
g2g --debug link --branch feature/top
```

`--help`, `--version`, and `completion bash|zsh|fish` are available; bare
`g2g` shows help when installed through Homebrew. The command requires Graphite CLI 1.8.6 exactly for its
supported display grammar and a compatible `gh` with `stack link`. Its tests
use fake executables on `PATH`, so they need neither authentication nor a
network connection.

`--debug` is a root flag and may appear before or after `link`, `sync`, or
`push`. It
does not change discovery, timeouts, checkout behavior, or mutations. Its
stderr-only records summarize supported Graphite discovery, the selected path,
batched GitHub PR facts, plan/revalidation decisions, and bounded subprocess
status. It never logs environment values, credentials, auth headers, cookies,
or GraphQL query payloads.

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

`g2g push` is a deliberately narrow, Git-only publication escape hatch for a
Graphite-managed linear path. It never calls `gt`, `gh`, submit, or restack.
It previews `git push --atomic --force-with-lease origin <branches>` by default
and requires `--apply` to run that exact one invocation. `--remote` defaults to
`origin` and must name a configured remote. Every selected non-trunk branch is
pushed bottom-to-top; atomic push means they all advance together or none do.
There is no non-atomic or unsafe-force fallback. This does not replace
Graphite's ownership of tracking, restacking, or submission.

All three commands normally use the declared-trunk-to-selected-branch path.
`--stack` instead treats the selected branch as a pivot and extends through a
unique downward child chain to its tip. It does not checkout a branch, includes
no siblings, and fails rather than guessing when a descendant fork makes the
extension ambiguous.

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
