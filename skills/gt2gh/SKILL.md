---
name: gt2gh
description: |
  Develop, test, or safely use the gt2gh Go CLI that bridges Graphite-managed
  branch stacks to GitHub native stacks. Use when working on gt2gh commands,
  Graphite/GitHub stack discovery or linking, future reconciliation design, CLI
  tests, or release readiness. Triggers: "gt2gh", "Graphite GitHub stack",
  "gh stack link", "Graphite stack linking", "gt2gh link", "gt2gh sync".
---

# gt2gh

## Work in this repository

- Read `README.md` and `design-docs/initial-scope.md` before changing behavior.
- Treat Graphite as authoritative. Preserve a discovered linear stack's
  bottom-to-top order; do not manage, reorder, rebase, submit, or merge it.
- Keep forked or tree-shaped stacks unsupported until their behavior has been
  designed explicitly. Decline ambiguous stack shapes rather than guessing.
- Do not claim that the current CLI links or synchronizes anything. Today,
  bare `gt2gh` prints help and `gt2gh link` is a safe no-op placeholder.

## Develop and test

- Keep external process calls behind `internal/subprocess.Runner`. Tests must
  use fake `gt` and `gh` executables on `PATH`; never require credentials,
  network access, or real CLI installations.
- Add behavior only behind explicit subcommands and safety gates. Show the
  resolved branch order before any future GitHub mutation.
- Run `gofmt -w` on changed Go files and `go test ./...`. Use `go vet ./...`
  when changing Go code or preparing a release.
- Use `git hunk` for any staging. Do not commit, tag, push, or invoke real
  `gt`/`gh` mutations unless the user explicitly asks.

## Use safely

- Use `gt2gh --help` or `gt2gh link --help` to inspect the current interface.
- Treat the future `sync` direction as design-only until an explicit command and
  apply protocol exist. Require read-only discovery and dry-run output before
  any reconciliation design can change GitHub state.
