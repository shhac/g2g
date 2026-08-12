# gt2gh

`gt2gh` is a lightweight Go CLI intended to bridge a Graphite-managed linear
branch stack into GitHub's native stack feature. Graphite remains the source of
truth; the eventual workflow will discover a single ordered Graphite stack and
pass its branches to `gh stack link` from bottom to top.

The stable v1 command shape is:

```sh
gt2gh link
```

This initial release is only a safe project skeleton. `gt2gh link` reports
that linking is not implemented and runs no external commands. `--help` and
`--version` are available now; bare `gt2gh` shows the help text.

## Structure

- `cmd/gt2gh`: executable entry point.
- `internal/cli`: standard-library command parsing and safe placeholder.
- `internal/subprocess`: future boundary for `gt` and `gh` invocations.
- `internal/testutil`: fake executables installed on `PATH` during tests.
- `design-docs`: concise scope and safety notes.

Run the test suite with `go test ./...`. The tests do not require Graphite,
GitHub CLI, authentication, or network access.
