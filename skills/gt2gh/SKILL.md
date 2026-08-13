---
name: gt2gh
description: |
  Develop, test, or safely use the gt2gh Go CLI that bridges Graphite-managed
  branch stacks to GitHub native stacks. Use when working on gt2gh commands,
  Graphite/GitHub stack discovery or linking, reconciliation, CLI tests, or
  release readiness. Triggers: "gt2gh", "Graphite GitHub stack", "gh stack
  link", "Graphite stack linking", "gt2gh link", "gt2gh sync", "g2g link",
  "g2g sync", "g2g push", "g2g submit", "Graphite atomic stack push".
---

# gt2gh

## Command identity and discovery

- Keep `gt2gh` as the project, repository, skill, release-asset, and Homebrew
  formula name. Homebrew installs the executable as `g2g`; source builds and
  unrenamed release archives use `gt2gh`.
- At the start of a task, reuse a usable command already resolved in the task
  context. Otherwise, discover it once locally: try `g2g --version` first, then
  `gt2gh --version`. Select the first command that succeeds. If neither works,
  state that the user must install `brew install shhac/tap/gt2gh` or provide a
  source/release-archive `gt2gh` binary; do not assume either command exists.
- Record the selected command as the task's `GT2GH_CMD` and use it for every
  later invocation. Do not re-detect it unless it fails or the environment
  changes. Use `g2g` in Homebrew examples and `gt2gh` in source/archive examples.

## Work in this repository

- Read `README.md` and `design-docs/initial-scope.md` before changing behavior.
- Treat Graphite as authoritative. Preserve the selected declared-trunk-to-leaf
  path in bottom-to-top order; do not manage, reorder, rebase, submit, or merge
  Graphite branches. A selected branch may sit in a forked tree, but siblings
  and descendants are not part of a v0.1 link.
- `link` previews by default. Its optional `--branch` target must work without
  checkout; `--apply` is the only path that may invoke `gh stack link`. Bare
  invocation prints help. `sync` is preview-first and only applies when every
  selected-path branch already has an open GitHub PR; it must not create
  Graphite-only mappings or repair closed/non-open PRs.
- `push` is a preview-first publication escape hatch. It may use Graphite's
  supported read-only discovery to select a path, but must never submit,
  restack, or otherwise mutate Graphite, and must never call `gh`; only
  `--apply` may run exactly one
  `git push --atomic --force-with-lease <remote> <branches>` call. Keep the
  remote default explicit (`origin`), validate it, and never fall back to a
  weaker push mode. Graphite remains responsible for tracking, restacking, and
  submission.
- By default, commands treat the selected branch as a pivot and extend through
  a unique downward Graphite child chain. This remains no-checkout and excludes
  siblings; reject a descendant fork rather than guessing. `--no-stack` is the
  explicit opt-out for only the declared trunk-to-selected path.
- `--debug` is a persistent, stderr-only diagnostic flag for `link`, `sync`,
  and `push`.
  It is safe to use for local investigation but must not alter command behavior
  or cause agents to enable Graphite's own `gt --debug`.
- Never guess a Graphite trunk from its name. The selected ancestry determines
  the inferred trunk; multiple valid declared trunks require `--trunk`, whose
  value must itself be declared and ancestral.
- Read `design-docs/graphite-cli-contract.md` before changing discovery. Do not
  read Graphite internal metadata/configuration or use `gt --debug`: supported
  production discovery is strict, version-pinned noninteractive CLI parsing.

## Develop and test

- Keep external process calls behind `internal/subprocess.Runner`. Tests must
  use fake `gt` and `gh` executables on `PATH`, including captured supported
  Graphite text fixtures; never require credentials, network access, or real
  CLI installations.
- Preserve the `completion bash|zsh|fish` interface. Dynamic `--branch` and
  `--trunk` completion must remain deterministic, read-only, and checkout-free.
- Run `gofmt -w` on changed Go files and `go test ./...`. Use `go vet ./...`
  when changing Go code or preparing a release.
- Use `git hunk` for any staging. Do not commit, tag, push, or invoke real
  `gt`/`gh` mutations unless the user explicitly asks.

## Use safely

- For a person who wants an editor workflow, `g2g submit --edit` opens one
  temporary JSON document, not a buffer per PR. It retains the document on all
  failures and after preview; successful `--edit --apply` cleans it up unless
  `--keep-spec` is present.

## Submitting pull requests

- `submit` is a preview-first PR creation recovery path. It must never invoke
  `gt submit`, restack Graphite, or retarget an existing PR. Its `--apply`
  boundary validates/revalidates first, atomically pushes refs, creates only
  missing draft PRs, then links the eligible stack.
- For non-interactive use, create a private temporary directory with
  `g2g submit --write-spec <dir>`, complete `submission.json`, validate with
  `g2g submit --spec <dir>/submission.json`, then add `--apply`. Keep the spec
  on failure and state exact repair/validation/retry commands. Multiple PR
  templates require `--template <name>` or `--no-template`; never guess.

- After command discovery, use the resolved command's `--help` or `link --help`
  to inspect the current interface (for example, `g2g link --help` after a
  Homebrew install).
- Require read-only discovery and dry-run output before `sync --apply` can
  change GitHub state.
