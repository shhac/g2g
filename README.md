# gt2gh

`gt2gh` is a lightweight Go CLI intended to bridge a Graphite-managed linear
branch stack into GitHub's native stack feature. Graphite remains the source of
truth; the eventual workflow will discover a single ordered Graphite stack and
pass its branches to `gh stack link` from bottom to top.

The stable v1 command shape is:

```sh
gt2gh link
```

`gt2gh link` is a safe preview by default. It resolves the checked-out Git
branch as its target, reads the Graphite path from its declared trunk to that
target, inspects matching GitHub pull requests, and prints the exact proposed
bottom-to-top command. Nothing changes unless `--apply` is present.

```sh
# Preview the path ending at the current branch.
gt2gh link

# Preview a Graphite-tracked local branch without checking it out.
gt2gh link --branch feature/top

# Revalidate, then allow gh to create/update the native GitHub stack.
gt2gh link --branch feature/top --apply

# Preview Graphite-authoritative reconciliation for existing GitHub PRs.
gt2gh sync --branch feature/top
```

`--help`, `--version`, and `completion bash|zsh|fish` are available; bare
`gt2gh` shows help. The command requires Graphite CLI 1.8.6 exactly for its
supported display grammar and a compatible `gh` with `stack link`. Its tests
use fake executables on `PATH`, so they need neither authentication nor a
network connection.

`gt2gh sync` is also preview-first. It compares the selected Graphite path to
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
