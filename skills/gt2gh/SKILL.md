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
- Treat Graphite as authoritative. Preserve the selected declared-trunk-to-leaf
  path in bottom-to-top order; do not manage, reorder, rebase, submit, or merge
  Graphite branches. A selected branch may sit in a forked tree, but siblings
  and descendants are not part of a v0.1 link.
- `gt2gh link` previews by default. Its optional `--branch` target must work
  without checkout; `--apply` is the only path that may invoke `gh stack link`.
  Bare `gt2gh` prints help. `sync` remains design-only until v0.2.0 work begins.
- Read `design-docs/graphite-cli-contract.md` before changing discovery. Do not
  read Graphite internal metadata/configuration or use `gt --debug`: supported
  production discovery is strict, version-pinned noninteractive CLI parsing.

## Develop and test

- Keep external process calls behind `internal/subprocess.Runner`. Tests must
  use fake `gt` and `gh` executables on `PATH`, including captured supported
  Graphite text fixtures; never require credentials, network access, or real
  CLI installations.
- Preserve the `completion bash|zsh|fish` interface. Dynamic `--branch`
  completion must remain deterministic, read-only, and checkout-free.
- Run `gofmt -w` on changed Go files and `go test ./...`. Use `go vet ./...`
  when changing Go code or preparing a release.
- Use `git hunk` for any staging. Do not commit, tag, push, or invoke real
  `gt`/`gh` mutations unless the user explicitly asks.

## Use safely

- Use `gt2gh --help` or `gt2gh link --help` to inspect the current interface.
- Treat the future `sync` direction as design-only until an explicit command and
  apply protocol exist. Require read-only discovery and dry-run output before
  any reconciliation design can change GitHub state.
