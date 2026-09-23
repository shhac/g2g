# g2g agent notes

Start with the repository skill at `skills/g2g/SKILL.md`, then use the
README and the design doc for the area for product behavior;
`design-docs/initial-scope.md` is the historical starting point. This file keeps
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

## Command surface

- The top level is your own stack and git-like verbs — `status`, `doctor`,
  `create`, `adopt`, `track`, `pull`, `push` and the rest. Anything that
  reaches past Git into another tool lives under that tool's name:
  `g2g github status|adopt|link|unlink|retarget|comment`,
  `g2g graphite adopt|mirror`. A new command goes on the side its dependencies
  put it, so the name says when it will talk to GitHub or Graphite. `push`,
  `submit` and `land` are top level because publishing is part of managing a
  stack. The groups live in `internal/cli/namespace.go`.
- No aliases were kept when the commands moved (`graph` → `status`, the old
  `status` → `github status`, `sync` → `pull`, `track --stack` → `adopt`,
  `import` → `graphite adopt`/`github adopt`, `mirror` → `graphite mirror`,
  source `pull-request` → `github`); an old name is an unknown command. Do not
  add one back. `schemaVersion` 3 exists because `--json`'s `operation` is now
  the command's path, and a consumer switching on the old names would have read
  the offline `status` as the pull request one.
- Exit status is `0`, `2` for failure, `3` for stopped part-way, and `1` from
  `doctor` alone, meaning it found something.

## Discovery and external CLIs

- Graphite parsing is a narrow compatibility boundary. Before changing it, read
  `design-docs/graphite-cli-contract.md` and the parser/fixture tests in
  `internal/graphite`, capture only synthetic regression coverage, and do not
  "improve" discovery by reading Graphite metadata or enabling Graphite debug
  output.
- Each source's adapter package builds its own forest and `internal/stack`
  selects within it: `graphite.ReadForest`, `graph.Graph.Shape`,
  `githubstack.BuildForest`. Each keeps its own type and exposes the edges
  through `Shape()`, delegating every walk to `internal/shape` rather than
  carrying a copy. Each also owns one file named for it —
  `internal/stack/graphite.go`, `g2g.go`, `pullrequest.go` — while `stack.go`
  holds only what every command shares. Graphite's lived in `stack.go` for
  historical reasons, which made the file named for the package the file
  describing one particular record. `graphite.Forest` was the last one walking its own, and the
  copy had lost the self-loop guard — which matters there and nowhere else,
  because a parsed display can name a branch as its own parent and the g2g store
  cannot. Do not assemble a structure inside
  `internal/stack` — the pull request source used to, and it is why the walk
  that follows non-local bases had nowhere to live. `githubstack.BuildForest`
  is bounded by rounds, not by branches, because `Inspect` answers a whole
  round in one query; a branch it places that is not local is carried as
  `Snapshot.Absent` and refused by `RequireActionable` before any mutation.
- Treat `internal/subprocess` as the sole process seam. Extend adapters through
  it, retain context cancellation, and route opt-in diagnostics through
  `internal/diagnostic`; diagnostic tests deliberately exercise redaction and
  bounded output.
- One fake per consumer-defined interface, and share the *answer* rather than
  the fake. A fake shared across a package boundary is a second interface nobody
  declared, so `testutil.RemoteTips` and `testutil.OwnCommits` are values every
  package's own fake delegates to. `graphiteRoutes` in `internal/cli` is the
  same rule for the PATH-fake route table, which had been written out four times
  and had already drifted: one copy was missing the `cherry` route the others
  carried.
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

- Read `design-docs/g2g-owned-graphs.md` before touching `internal/graph`,
  `design-docs/restack.md` before anything that rewrites history or reads the
  remote, and `design-docs/land.md` before anything in `internal/land`.
- Never move the user's remote-tracking refs. `RemoteTips` reads through
  `ls-remote` and writes nothing; `FetchIsolated` writes only under
  `refs/g2g/remotes/` and needs both `--refmap=` and `--no-write-fetch-head`.
  Its refspecs are forced, and must stay so: those refs are g2g's own record of
  what the remote holds rather than the user's, they carry no work to lose, and
  without the plus a branch the remote rewrote cannot be fetched at all — git
  refuses the non-fast-forward and fails the whole command, so the second
  `pull` after any force push could not fetch. Restacking a stack and republishing it
  is the ordinary way to get there. A bare `--force-with-lease` takes its
  baseline from the remote-tracking ref, so refreshing it silently disarms the
  check; leases are pinned to the tips the plan observed. The forest model,
  per-branch authority, and derived (never stored) graph identity are
  decisions, not accidents.
- `status --from` reads another record and draws it in g2g's own format, which
  is how a divergence between the two becomes visible on a real repository
  rather than only in `internal/stack/parity_test.go`'s fixtures. It offers
  `stack.OfflineSources` and refuses `github`, because reading a base
  invokes `gh` and answering without a network is why this command exists apart
  from `github status`. The flag is on the command, in `internal/cli`; it does
  not breach the rule below, which is about the package.
- `status` compares each branch with its remote **from local refs only** and
  never fetches: `git.Client.KnownTips` reads `refs/remotes/<remote>/` and
  g2g's own `refs/g2g/remotes/<remote>/` in one `for-each-ref`, and takes
  whichever of the two tips descends from the other — a push moves the first, a
  pull fetches into the second and leaves the first behind — preferring the
  remote-tracking ref when they are not in order, because that is what a push
  moves and what `git status` compares with. `push.Known` then counts by
  content through the same `Compare` `push` uses, so a restacked branch reads
  as replayed rather than diverged. `push` itself still asks the remote,
  because a lease must be pinned to what is there now; a report that needed a
  network would not be one to run before deciding whether to fetch. The default
  remote missing draws no marks; a `--remote` named on purpose that does not
  exist is an error.
- `doctor` is `status` narrowed to what is wrong, across every recorded stack,
  offline. Each finding carries the one command that puts it right, and it
  exits `1` when it finds anything (`foundError`), `0` when it finds nothing,
  and `2` when it could not tell — the `diff`/`grep` convention, so a script
  can ask. Keep the split: `status` is the full overview, `doctor` only the
  unexpected. A branch with no commits of its own is not a finding.
- `internal/graph` must depend on Git alone. Importing Graphite or GitHub into
  it, or making any of `status`/`doctor`/`adopt`/`track`/`untrack` need a
  network, removes the only reason the package exists. The scope vocabulary and the forest traversal
  therefore live in `internal/shape`, which depends on nothing: taking them
  from `internal/stack` pulled Graphite and GitHub in transitively, through an
  import line that named neither. `internal/graph/boundary_test.go` checks the
  whole transitive set, because no single import line looked wrong.
- `Candidates` is `related` plus a fallback, and only `track`'s single-branch
  preview wants the fallback. It measures every local branch when the preferred
  set comes back empty, so there is something to offer where nothing strictly
  qualifies — and those are branches the target cannot reach, so none of them is
  ever an ancestor. Any caller that filters on `Ancestor` must ask `related`:
  `attach`, `chain`, `trunkFor` and `originOf` all do, and asking for the
  fallback made a whole-stack adoption quadratic in the repository's branches,
  measuring every one against every other and discarding the answer. It did not
  show up because every test of it used four branches, where quadratic and
  linear are the same shape; `internal/graph/cost_test.go` pins the growth
  rather than the seconds.
- A trunk is evidenced, never guessed. `git.Client.DefaultBranch` reads
  `refs/remotes/<remote>/HEAD`, which clone writes, so the ordinary case is
  answered locally with no network and no config. It is wired as an optional
  `TrunkEvidence` on `graph.Service` and `stack.Resolver`, and it never
  chooses what a command selects. Two commands that record structure may let it
  *permit* a root the user is building on — `create` from the default branch
  and `github adopt` onto it — because the user named the branch
  and the evidence only confirms it is a trunk; without it they refuse and name
  `g2g track`. It never picks a trunk nobody named. An unset ref is
  an empty answer rather than an error, because a repository nobody has told is
  ordinary. The g2g graph's own trunks cannot fill this role on their own: they
  are branches nothing sits under, so an empty store has none at all, which is
  exactly the repository where someone standing on `main` was told to give it a
  parent.
- A branch's annotation is a list of `stackMark`, one per axis, each with its
  own severity: `base✓`/`base✗` is about a pull request's base and never its
  contents, `head✗` is about currency, and a subject-less mark carries what is
  about neither. A merged pull request is `pr✓` and neutral — it did what it
  was for, and only the branch left in the stack is a problem, which is the
  reading the offline view (`status`, once `graph`) has always taken of an
  already-landed branch. They used to be one string under one colour, which is
  how a line came to open with the word "aligned" and go on to describe a divergence,
  in whichever colour the worse of them won. `stackNode.marked` renders `State`
  and the worst `Severity` from the marks, so nothing downstream has to
  understand them; do not set `State` beside them. Currency comes from
  `githubstack.PullRequest.HeadOID` — a field on a query already being made.
  `push` says the same thing from the other side, out of the `RemoteTips` it
  already reads for its leases. Both are local-only and add nothing over the
  network, both are optional capabilities (`link.Service.Tips`, `push.Git`),
  and in both the zero value must not read as the reassuring answer: an
  uncompared branch says nothing rather than "up to date".
- Read `design-docs/cost.md` before changing anything that asks git or GitHub
  a question per branch. Three rules there have each been broken already:
  bound a content comparison by the branch's own commits (from the trunk's
  start, every trunk commit reads as the branch's own work); let
  `git.Client.Untouched` shortcut only a "not landed", never `landed.Missing`;
  and leave branches merged into the trunk out of an adoption, which
  `cost_test.go` pins for both kinds of sediment. `graph.Service.Structure` is
  the selection without the per-branch assessment; a caller that only needs
  the shape uses it.
- Batch before parallelising. `Inspect` is one GitHub round trip: the pull
  requests for every selected branch come back from one aliased GraphQL query,
  and the repository is `{owner}`/`{repo}` placeholders that `gh` fills from the
  directory it runs in — documented for `--field` — which removed the
  `gh repo view` call that existed only to name it. `git.Client.ResolveAll`
  is the same move locally: one `rev-parse` for every branch and every pull
  request head, falling back to asking individually only when the batch cannot
  say which revision it could not resolve. What is genuinely per-pair —
  `git cherry`, `git merge-tree` — is what concurrency is for.
- The per-branch reads run concurrently, bounded by `parallel.Each`. They are
  independent process spawns and were two thirds of a status on a fourteen-
  branch stack; asking eight at once took it from 4.2s to 2.6s, and what is
  left is four external CLI calls that cannot overlap each other. Results land
  in a slice sized before the reads start, so each read owns one element and
  needs no lock — keep it that way rather than adding one. Anything a read
  touches must be safe for it: `diagnostic.Writer.Event` assembles its line and
  writes once for exactly this reason, and a fake handed to `link.Tips` must not
  accumulate state without synchronising. Run `go test -race` on
  `internal/link` and `internal/cli` after touching any of it; CI runs the
  scoped race suite on every push, so the rule has a check behind it.
- Currency is counted **by content and bounded to a branch's own commits**, so
  `link.Tips` needs `Cherry` and not `Divergence`. Counting commit ids answered
  the ordinary case wrongly in both directions: a branch replayed onto a trunk
  that has moved on carries the same work under new ids, so every commit the
  trunk had gained was reported as unpushed work of the reader's own — 1532 of
  them on one real stack, none of which were theirs — and the old ids left on
  the pull request read as a divergence. The two `Cherry` calls are deliberately
  asymmetric: this branch's side stops at its parent, because everything below
  belongs to the branch it is stacked on, and the pull request's side needs no
  limit because `branch..head` already excludes everything the branch can
  reach. That state is `Currency.Rewritten` — nothing is missing, it needs
  pushing — and it is what a restacked stack is in.
  `internal/link/currency_real_test.go` builds a throwaway repository for this,
  because a fake answers whatever it is asked and the question is what Git
  considers equivalent.
- `github status` says a branch has landed rather than that it has no pull
  request,
  because GitHub cannot answer that one: a squash merge lands the work under a
  head the branch never had, and a cherry-picked series under no pull request
  at all — so the branch reads as missing one, and the advice for missing is to
  open one, for a change already in the trunk. `link.markLanded` asks only the
  branches whose sole problem is a missing or closed pull request, which is what
  bounds the cost, and asks Cherry before Absorbed. Which command forgets a
  landed branch depends on the source: `g2g prune` edits g2g's own graph and
  finds nothing in a Graphite-declared repository, where the answer is
  `gt sync`; a structure read from pull request bases is not a record anything
  here edits, and that case names no command rather than a wrong one.
- `github status` renders a branch nothing describes instead of refusing it,
  through the typed `stack.Undescribed`. "Nothing is stacked here" is an answer
  to what a read-only triage command was asked; only `github status` renders
  it, and every
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
- "Has this landed" has two forms and needs both. `Cherry` answers per commit
  and cannot see a squash merge, which combines a branch's commits into one so
  the result is equivalent to none of them — on the commonest way a branch
  lands. `git.Client.Absorbed` merges the branch into the base and checks for
  the base's own tree back, which answers it of the whole branch at once.
  `status`'s landed state, `prune`'s, and a step's collapse all consult it —
  `prune` did not, so the command whose whole job is forgetting landed branches
  was blind to the commonest way they land, and `graph` sent people to it
  saying "already in the trunk · run g2g prune to forget them" about branches
  it then found nothing to forget. without the second, a
  child's replay range starts below its parent's landed work and each of those
  commits conflicts with the squashed version of itself.
- `internal/restack` is the only package allowed to rewrite history. The replay
  range is `forkPoint..branch`; the fork point must be an ancestor of the
  branch before any rewrite, or the range silently widens to include the base's
  own commits. Each independent root of a selection is its own replay, and
  every range handed to an engine starts at that root's fork point; a branch
  whose parent is being rewritten is rewritten too. A replay that fails before
  the checkout is touched puts back every tip it moved, and the journal records
  the structure as well as the tips, so `--abort` restores both and returns to
  the branch the user was standing on.
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
  for the branch to move, so it is both. `ToLocation` is `pull`'s
  (`internal/sync`): it replays onto
  a ref it fetched under `refs/g2g/` because that is where the trunk is about to
  be, and that ref is a place, not a parent. Deriving the recorded parent from
  the replay target instead put `refs/g2g/remotes/origin/main` in the store on
  the ordinary sync path, so every synced stack reported "parent missing"
  immediately after a sync that said it succeeded.
- Which side wins a divergence is normally answered by which command runs:
  `pull` only moves toward this checkout, `push` only toward the remote. Keep
  that one-direction-per-command rule — it is what makes the model legible.
  `pull --take <enum>` exists only for the outcome neither command can otherwise
  reach, is an enum so the vocabulary can grow, and has no `mine` value. It is
  the one path where `pull` discards work that exists nowhere else, so the
  preview names every commit it would lose rather than counting them.
- `land` owns no rules of its own and must not grow any: it publishes through
  `push`, advances and replays through `pull`, and asks Git by content whether
  a branch has landed through the check `prune` uses. Every rule an early draft
  reached past cost a property the bypassed service already had. It aims every
  pull request at the trunk rather than at the branch below, because by the time
  a branch's turn comes the branch below has merged and gone. It tells its own
  replay from a reviewer's commit by remembering what the remote held when the
  descent was planned — the one question `push` cannot answer from tips alone,
  since both leave the remote holding work the branch does not have. It is not
  journaled: re-entrancy comes from recomputation, a merged branch being
  detected by content and skipped.
- A command that did part of what it was asked and stopped exits `3`, not `0`
  and not the failure status. What it achieved is not coming back — merged
  stays merged, replayed stays replayed — so it is not a failure to retry, and
  it plainly is not success. `stoppedPartWay` marks it and the top-level
  printer then says nothing further, because the report is already on stdout
  with the detail in it. `pull --prune` whose prune refuses after the pull is
  this too.
- `pull --prune` is composition, not a new rule: `pull`'s flow and then
  `prune`'s over the same selection, each with its own revalidation. Its
  preview can only say it will prune, because what has landed is known once
  the base moves. The two reports are why `--json`/`--porcelain` refuse it.
- `prune` records a child it would strand on the branch below **only on
  evidence**: when Git shows that branch is an ancestor of the child, which is
  what a pull's replay leaves, and with the fork point `track` would record.
  That is recording where the child already sits, not reparenting around a
  gap. Without the ancestry — a trunk advanced by hand and the stack not
  replayed — or for a child outside the selection it still refuses, offering
  `g2g pull --prune` and then one `g2g track --branch <child> --parent
  <branch>` per child. Do not relax the check into a guess; `untrack` still
  never reparents, and `delete` remains the one command that does on request.
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

`design-docs/scenarios.md` names the situations and says what each one should
do. Use those names: a bug report, a test, and a design argument saying
"friendly-fixer" all mean the same thing, and the entries record where today's
answer is not the wanted one.

`internal/cli/journey_test.go` drives a person through a stack while the remote
moves under them, against a real bare remote and a real second clone standing in
for a colleague. Everything is real except GitHub, which has no local stand-in.

`internal/cli/land_journey_test.go` does the same for a descent, and its `gh`
goes further: it performs the squash merge itself, in the real remote. It has
to. A `gh` that exits zero without merging leaves the trunk unchanged, so the
wait after a merge never settles, the replay above has nothing to replay onto,
and the test passes having proved nothing but argv construction. Keep that
property in anything added there — a route that only answers is a route that
proves the fake works.

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

Five things exist once and must not be reimplemented locally. Each was found as
several diverging copies, and in two cases the copies had already lost a
property the original had.

- `subprocess.CheckArgument` / `OptionLike` — refusing a value a process would
  read as an option. Callers keep their own wording, because which tool refused
  is what a reader acts on; only the rule is shared.
- `diagnostic.BoundedOutput` — bounding *and redacting* a failed command's
  output.
- `diagnostic.Revalidated` — the preview/apply revalidation check and its
  diagnostic event. `graph`'s `matched` delegates to it.
- `githubstack.PathStep.Classify` — what one branch's pull request is.
  `github link` and `submit` apply different policy to the same answer; only the policy
  differs.
- `repair.Note` — a refusal in two shapes: why, and the ways out. A package
  that refuses builds one and derives its `Blocked` sentence from it, so the
  line a machine reads and the column a person reads cannot name different
  commands. It depends on nothing, which is what lets `internal/graph` describe
  its own repair without reaching Graphite or GitHub. The joining lives on the
  type — `SentenceWith` takes the decoration rather than exposing the join —
  because a caller assembling its own sentence is free to word it differently
  from the one a machine reads, and that is the drift this replaced.

## Change and verification workflow

- An `applyFlow` closure reads the plan it is handed and nothing else. Where a
  command needs more than the service's plan carries, wrap it — `unlinkPlan` is
  the pattern — rather than capturing a variable the closures share. `unlink`
  did the latter, so the stack number rendered immediately before the mutation
  came from the preview rather than the revalidated plan, and the only thing
  making that safe was `link.Revalidate` refusing any inequality two files away.
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
