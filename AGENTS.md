# gt2gh agent notes

Start with the repository skill at `skills/gt2gh/SKILL.md`, then use the
README and `design-docs/initial-scope.md` for product behavior. This file keeps
only process knowledge that is easy to miss.

## Discovery and external CLIs

- Graphite parsing is a narrow compatibility boundary. Before changing it, read
  `design-docs/graphite-cli-contract.md` and the parser/fixture tests in
  `internal/graphite`, capture only synthetic regression coverage, and do not
  "improve" discovery by reading Graphite metadata or enabling Graphite debug
  output.
- Treat `internal/subprocess` as the sole process seam. Extend adapters through
  it, retain context cancellation, and route opt-in diagnostics through
  `internal/diagnostic`; diagnostic tests deliberately exercise redaction and
  bounded output.
- Tests must stay offline. Use `internal/testutil.FakeCLIs` (declarative
  routes plus an invocation recorder) or the lower-level
  `WithFakeExecutables`, both PATH-backed `git`/`gt`/`gh` scripts. Prefer
  injected fakes for decision matrices, where spawning a process per case buys
  nothing, and PATH fakes for at least one end-to-end path per command, which
  is the only thing that covers argv construction, response parsing, and exit
  handling. A PATH fake answers from its routes whatever it is asked, so assert
  the recorded request as well as the result — `Recorder.Find` exists for that. Any Graphite display or error regression
  fixture must be fully synthetic—never copy a real checkout's names, graph, or
  CLI output into the repository.

## g2g-owned graphs

- Read `design-docs/g2g-owned-graphs.md` before touching `internal/graph`. The
  forest model, per-branch authority, derived (never stored) graph identity,
  and the deliberate absence of restack are decisions, not accidents.
- `internal/graph` must depend on Git alone. Importing Graphite or GitHub into
  it, or making any of `graph`/`track`/`untrack` need a network, removes the
  only reason the package exists.
- `track` previews candidates and blocks rather than choosing; `untrack`
  reports the children it strands rather than reparenting them. Both are the
  same fail-closed rule the Graphite commands follow, and both have tests that
  fail if the guess is reintroduced.
- Ancestry is the one seam where a PATH fake proves nothing, because the fake
  answers whatever it is asked and the question is what Git considers
  reachable. Those cases build a throwaway local repository — synthetic branch
  names, no remote, nothing that leaves the machine.

## Fixtures and data hygiene

- Put reusable Graphite display fixtures in `internal/graphite/testdata/`; keep
  parser grammar cases beside them in `internal/graphite/parser_test.go`.
  Adapter integration tests should load those fixtures through a temporary file
  and a PATH fake, following `internal/link/integration_test.go`, rather than
  depending on a locally installed CLI.
- Use plainly synthetic values everywhere that lands in the repository:
  `synthetic-*` branches, `example.test` URLs, fictional PR text, and invented
  diagnostics. This applies equally to fixtures, test names, comments, README/
  design docs, and commit messages. Real output may be inspected only as
  transient evidence and must be abstracted before writing a regression case.
- Treat error and debug-output tests as data-leak tests too: exercise the
  bounded/redacted path in `internal/diagnostic`, never a real token, header,
  repository identifier, or credential-bearing command argument.

## Change and verification workflow

- Preview/apply sequencing is a safety contract, not just presentation. When a
  command can mutate, preserve its re-discovery/revalidation and final
  render/write/flush-before-mutation tests; command-family coverage lives under
  `internal/cli/*_test.go` and adapter-level integration cases sit with their
  packages.
- Use `git hunk` for staging. Run `gofmt -w` on changed Go files and
  `go test ./...`; add `go vet ./...` for Go changes and release work. Validate
  a changed repository skill with the active `skill-creator` quick validator
  (using its environment-provided path; install PyYAML transiently if needed).

## Release and distribution

- Follow `.agents/commands/release.md` literally: a version tag is the release
  trigger. Do not hand-build a release or edit the generated Homebrew formula.
  Verify both the `Release` and `Publish skill` tag workflows afterwards.
- The shared release generator lives in `shhac/homebrew-tap`; this repository's
  durable distribution knobs are `.github/workflows/release.yml` (not a formula
  edit). `installed_binary_name: g2g` is deliberate: the project, asset, and
  formula remain `gt2gh`, while Homebrew installs `g2g`. Check the generated
  formula's alias and completion/test lines after a release.
- `CLAUDE.md` is a symlink to this file, so keep instructions harness-neutral
  and edit `AGENTS.md` only.
