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
target, inspects matching GitHub pull requests, and prints the exact proposed
bottom-to-top command. Preview clearly states that no changes were made;
nothing changes unless `--apply` is present.

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

# Preview Graphite-authoritative reconciliation for existing GitHub PRs.
g2g sync --branch feature/top
```

`--help`, `--version`, and `completion bash|zsh|fish` are available; bare
`g2g` shows help when installed through Homebrew. The command requires Graphite CLI 1.8.6 exactly for its
supported display grammar and a compatible `gh` with `stack link`. Its tests
use fake executables on `PATH`, so they need neither authentication nor a
network connection.

`gt2gh` never guesses a trunk from its name. It infers the only
Graphite-declared trunk on the selected ancestry and shows it prominently. If
that ancestry has multiple declared trunks, it fails closed and requires
`--trunk <branch>`; an override must be both declared by Graphite and an
ancestor of the selected branch.

Preview renders the selected stack graph once and always shows the exact
`gh stack link` command it validated. `--apply` re-discovers and revalidates
before it invokes that command. Manually copying the displayed command is a
separate, deliberate snapshot action and does not cause `gt2gh` to re-resolve
anything.

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

`g2g sync` is also preview-first. It compares the selected Graphite path to
existing open GitHub PR bases, identifies aligned and divergent relationships,
and can reconcile the native stack only with `--apply`. It deliberately refuses
to create a PR for a Graphite-only branch or repair a closed/non-open PR.

## Structure

- `cmd/gt2gh`: executable entry point.
- `internal/cli`: Cobra command parsing, preview output, and completion.
- `internal/graphite`: strict, version-pinned read-only Graphite display parser.
- `internal/git`, `internal/githubstack`: read-only repository and PR seams.
- `internal/link`: Graphite-authoritative plan/apply orchestration.
- `internal/subprocess`: boundary for `git`, `gt`, and `gh` invocations.
- `internal/testutil`: fake executables installed on `PATH` during tests.
- `design-docs`: concise scope and safety notes.

Run the test suite with `go test ./...`.
