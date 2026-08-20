# g2g agent notes

Start with the repository skill at `skills/g2g/SKILL.md`, then use the
README and `design-docs/initial-scope.md` for product behavior. This file keeps
only process knowledge that is easy to miss.

## Running the real `gt`

Running Graphite by hand is allowed, in any directory, with one boundary:
**nothing that writes to a remote.** `gt submit` and anything else that pushes
or talks to Graphite's API is out, because that is what would enrol a
repository with the service. Local reads and local structure commands —
`gt --version`, `gt init`, `gt track`, `gt untrack`, `gt log` — are fine, and
are often the only way to check a change against the tool this one has to stay
compatible with.

Two different things get called enrolment, and conflating them is what makes
people refuse `gt` outright. Writing files under `.git/` is local and
disposable. Registering the repository with Graphite's service is neither. Only
the second is off limits.

- **Pass `--no-interactive`.** Graphite prompts by default, and a prompt in a
  non-interactive session hangs rather than fails.
- **Expect local state.** `gt log` creates `.graphite_metadata.db`,
  `.graphite_repo_config` and `.graphite_pr_info` under `.git/` in a repository
  that has never used Graphite. That is precisely why `Describes` answers from
  the repository instead of running `gt`, and why completion is gated. It is
  local-only and harmless in a throwaway repository; do not let it happen in
  this working tree, and never commit it.
- **Verify in a throwaway repository, not a real one.** Build a repository with
  `synthetic-*` branch names, no remote, and nothing that leaves the machine.
  That keeps a real checkout's names and graph out of anything captured.
- **Real output is evidence, never a fixture.** Anything that lands in the
  repository stays fully synthetic; abstract what a real run showed before
  writing a regression case.

This is what makes an end-to-end claim about Graphite-backed behaviour
checkable. A PATH fake answers whatever it is asked, so it can confirm argv and
parsing and can never confirm that the grammar is still the one Graphite emits.

## Discovery and external CLIs

- Graphite parsing is a narrow compatibility boundary. Before changing it, read
  `design-docs/graphite-cli-contract.md` and the parser/fixture tests in
  `internal/graphite`, capture only synthetic regression coverage, and do not
  "improve" discovery by reading Graphite metadata or enabling Graphite debug
  output.
- Each source's adapter package builds its own forest and `internal/stack`
  selects within it: `graphite.ReadForest`, `graph.Graph.Shape`,
  `githubstack.BuildForest`. Do not assemble a structure inside
  `internal/stack` — the pull request source used to, and it is why the walk
  that follows non-local bases had nowhere to live. `githubstack.BuildForest`
  is bounded by rounds, not by branches, because `Inspect` answers a whole
  round in one query; a branch it places that is not local is carried as
  `Snapshot.Absent` and refused by `RequireActionable` before any mutation.
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

- Read `design-docs/g2g-owned-graphs.md` before touching `internal/graph`, and
  `design-docs/restack.md` before anything that rewrites history or reads the
  remote.
- Never move the user's remote-tracking refs. `RemoteTips` reads through
  `ls-remote` and writes nothing; `FetchIsolated` writes only under
  `refs/g2g/remotes/` and needs both `--refmap=` and `--no-write-fetch-head`.
  A bare `--force-with-lease` takes its baseline from the remote-tracking ref,
  so refreshing it silently disarms the check; leases are pinned to the tips
  the plan observed. The
  forest model, per-branch authority, derived (never stored) graph identity,
  and the deliberate absence of restack are decisions, not accidents.
- `graph --from` reads another record and draws it in g2g's own format, which
  is how a divergence between the two becomes visible on a real repository
  rather than only in `internal/stack/parity_test.go`'s fixtures. It offers
  `stack.OfflineSources` and refuses `pull-request`, because reading a base
  invokes `gh` and answering without a network is why this command exists apart
  from `status`. The flag is on the command, in `internal/cli`; it does not
  breach the rule below, which is about the package.
- `internal/graph` must depend on Git alone. Importing Graphite or GitHub into
  it, or making any of `graph`/`track`/`untrack` need a network, removes the
  only reason the package exists. The scope vocabulary and the forest traversal
  therefore live in `internal/shape`, which depends on nothing: taking them
  from `internal/stack` pulled Graphite and GitHub in transitively, through an
  import line that named neither. `internal/graph/boundary_test.go` checks the
  whole transitive set, because no single import line looked wrong.
- A trunk is evidenced, never guessed. `git.Client.DefaultBranch` reads
  `refs/remotes/<remote>/HEAD`, which clone writes, so the ordinary case is
  answered locally with no network and no config. It is wired as an optional
  `TrunkEvidence` on `graph.Service` and `stack.Resolver`, and it may only
  choose how advice is phrased — never what a command selects. An unset ref is
  an empty answer rather than an error, because a repository nobody has told is
  ordinary. The g2g graph's own trunks cannot fill this role on their own: they
  are branches nothing sits under, so an empty store has none at all, which is
  exactly the repository where someone standing on `main` was told to give it a
  parent.
- "Aligned" is a statement about a pull request's *base*, never its contents,
  so `status` reports currency separately from `githubstack.PullRequest.HeadOID`
  — a field on a query already being made. `push` says the same thing from the
  other side, out of the `RemoteTips` it already reads for its leases. Both
  compare with one local read per branch and nothing extra over the network,
  both are optional capabilities (`link.Service.Tips`, `push.Git`), and in both
  the zero value must not read as the reassuring answer: an uncompared branch
  says nothing rather than "up to date".
- `status` renders a branch nothing describes instead of refusing it, through
  the typed `stack.Undescribed`. "Nothing is stacked here" is an answer to what
  a read-only triage command was asked; only `status` renders it, and every
  command that mutates still refuses because it still has nothing to act on.
- `track` previews candidates and blocks rather than choosing; `untrack`
  reports the children it strands rather than reparenting them. Both are the
  same fail-closed rule the Graphite commands follow, and both have tests that
  fail if the guess is reintroduced.
- Ancestry and rewriting are the seams where a PATH fake proves nothing,
  because the fake answers whatever it is asked and the question is what Git
  considers reachable or actually produces. Those cases build a throwaway local
  repository — synthetic branch names, no remote, nothing that leaves the
  machine.
- `internal/restack` is the only package allowed to rewrite history. The replay
  range is `forkPoint..branch`; the fork point must be an ancestor of the
  branch before any rewrite, or the range silently widens to include the base's
  own commits. Every range handed to an engine starts at the topmost step's
  fork point, and a branch whose parent is being rewritten is rewritten too.
- A rewrite that moves the branch you are standing on must reconcile the
  checkout: the replay engine and a collapse both move refs without one, so the
  index and working tree are left describing the old commit, which git reports
  as changes nobody made and which blocks the next `git switch`. `git reset
  --keep HEAD` cannot fix this and was the previous answer — `--keep` updates
  what differs between the target and HEAD, and by then they are the same
  commit. `Service.standingOn` records both ends before anything moves and
  `resettle` hands them to `git.Client.SwitchTree` (`read-tree -m -u`), which is
  the plumbing `git switch` itself uses.
- A rewrite refuses a branch another worktree has checked out. It does not need
  to check a branch out to move it, so nothing stopped it: Git updated the ref,
  that worktree's index still described the old commit, and its next
  `git status` reported staged changes nobody made — while the preview said it
  had touched no checked-out branch. `git.Client.CheckedOutElsewhere` reads
  `worktree list --porcelain` and excludes the current worktree; it is an
  optional `restack.WorktreeReader`, so a Git that cannot answer leaves the
  rewrite exactly as safe as it was before the check existed.
- Where a rewrite lands and what the graph records are two questions, and
  `restack.Onto` keeps them apart. `ToBranch` is a user's `--onto`: they asked
  for the branch to move, so it is both. `ToLocation` is sync's: it replays onto
  a ref it fetched under `refs/g2g/` because that is where the trunk is about to
  be, and that ref is a place, not a parent. Deriving the recorded parent from
  the replay target instead put `refs/g2g/remotes/origin/main` in the store on
  the ordinary sync path, so every synced stack reported "parent missing"
  immediately after a sync that said it succeeded.
- restack is the only resumable operation, so every other mutating command
  refuses while its journal exists. `--continue` recomputes from the refs
  rather than resuming a stored queue, which is what makes the user's own
  `git rebase --continue`/`--abort` harmless.

## Source resolution

- Read `design-docs/source-resolution.md` before changing how a command selects
  a stack, and `design-docs/stack-scope.md` before changing how much of one it
  selects. Precedence is declared once, in the root command's wiring; the scope
  vocabulary and its traversal live once, in `internal/shape`, which depends on
  nothing so that `internal/graph` can use them without reaching Graphite or
  GitHub.
- A scope means the same thing whichever record answered. The parity table in
  `internal/stack/parity_test.go` is what keeps that true — it asks both records
  the same question and compares, which is the only shape that finds a
  divergence where each side is internally consistent.
- `Describes` must be free of side effects. Graphite's discovery creates state
  in a repository that has never used it, so asking whether Graphite applies is
  answered from the repository rather than by running `gt`. A test fixture that
  omits the marker is asserting that Graphite should not be consulted.
- Converting a command to a different source must not change what a
  Graphite-backed selection produces. The golden files are the check: a diff
  there during selection work is a bug, not an update.

## Journeys

`internal/cli/journey_test.go` drives a person through a stack while the remote
moves under them, against a real bare remote and a real second clone standing in
for a colleague. Everything is real except GitHub, which has no local stand-in.

This exists because a PATH fake answers whatever it is asked, and the failures
that keep recurring are about what Git actually does: a ref moves and the
working tree, the index, or another worktree does not follow. Three shipped
releases had that bug in three different places.

- **Assert a clean tree after every mutation.** `world.assertClean` is the check
  that catches the recurring class. Changes nobody made are the symptom every
  time.
- **Assert against the remote, not the command's own output.** A push that
  claims success and a ref that arrived are different facts.
- **Model the state exactly.** A trunk that has only moved ahead is not
  diverged, it fast-forwards; divergence needs commits on both sides. Getting
  that wrong writes a test that passes for the wrong reason.

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
  repository identifier, or credential-bearing command argument. Bound a failed
  command's output with `diagnostic.BoundedOutput` and nothing else — a private
  copy of it once truncated without redacting, and only the length was tested.

## Shared seams

Four things exist once and must not be reimplemented locally. Each was found as
several diverging copies, and in two cases the copies had already lost a
property the original had.

- `subprocess.CheckArgument` / `OptionLike` — refusing a value a process would
  read as an option. Callers keep their own wording, because which tool refused
  is what a reader acts on; only the rule is shared.
- `diagnostic.BoundedOutput` — bounding *and redacting* a failed command's
  output.
- `diagnostic.Revalidated` — the preview/apply revalidation check and its
  diagnostic event. `graph`'s `matched` delegates to it.
- `githubstack.PathStep.Classify` — what one branch's pull request is. `link`
  and `submit` apply different policy to the same answer; only the policy
  differs.

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

## The rename

The project was called `gt2gh`, which meant "Graphite to GitHub" and stopped
being true once the tool recorded its own structure. Everything is `g2g` now:
project, module path, command, repository, formula, skill and prose.

`gt2gh` survives in exactly three places, all of them statements about the past
rather than names still in use. Do not "tidy" any of them away:

- **Tags and their release assets.** A tag published `gt2gh-darwin-arm64.tar.gz`
  and always will; those archives are immutable and their checksums are
  published. This is also why a download from an old tag unpacks a binary named
  `gt2gh`.
- **`formula_renames.json` in `shhac/homebrew-tap`.** The `{"gt2gh": "g2g"}`
  mapping is what migrates an install made under the old name. Deleting it does
  not clean anything up; it strands every install that has not yet updated.
- **History.** Commit messages and design-doc passages describing the old name
  were accurate when written.

The ordering constraint that made this a migration rather than a rename is worth
keeping in mind for any future one: the rename mapping and the renamed formula
must land *together*. A mapping pointing at a formula that does not exist yet is
as broken as a renamed formula with no mapping.

## Release and distribution

- Follow `.agents/commands/release.md` literally: a version tag is the release
  trigger. Do not hand-build a release or edit the generated Homebrew formula.
  Verify both the `Release` and `Publish skill` tag workflows afterwards.
- The shared release generator lives in `shhac/homebrew-tap`; this repository's
  durable distribution knobs are `.github/workflows/release.yml` (not a formula
  edit). Name-derived inputs are left at their defaults now that everything is
  `g2g`, so `cmd_path` and `installed_binary_name` are deliberately absent
  rather than forgotten. Check the generated formula's alias and
  completion/test lines after a release.
- `CLAUDE.md` is a symlink to this file, so keep instructions harness-neutral
  and edit `AGENTS.md` only.
