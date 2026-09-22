---
name: g2g
description: |
  Develop, test, or safely use the g2g Go CLI, which records stacked
  branches itself and projects them onto GitHub. Graphite is an optional
  source it can read, mirror to, and import from, never a requirement. Use
  when working on g2g's commands (track, create, up/down/top/bottom, link,
  sync, prune, restack, retarget, submit, push, land, comment, graph, mirror,
  import), stack scope and structure, source resolution and alignment, CLI
  tests, or release readiness.
  Triggers: gt2gh, stack without Graphite, restack after squash merge,
  merge a stack down.
---

# g2g

## Command identity and discovery

- The command, project, module, repository, formula and this skill are all
  `g2g`. The tool was once called `gt2gh`, and that name survives only where it
  is a historical fact: tags and release assets published under it, and the
  tap's `formula_renames.json`, which is what migrates installs made under the
  old name. Do not reintroduce it anywhere else.
- At the start of a task, reuse a usable command already resolved in the task
  context. Otherwise discover it once locally: try `g2g --version`, then
  `gt2gh --version`, which is worth one attempt only for an install predating
  the rename. Select the first that succeeds. If neither works, say the user
  must run `brew install shhac/tap/g2g` or provide a built binary; do not
  assume either exists.
- Record the selected command and use it for every later invocation. Do not
  re-detect unless it fails or the environment changes.

## Work in this repository

- Read `README.md` and the design doc for the area before changing behavior;
  `design-docs/initial-scope.md` is the historical starting point, not the
  current contract.
  For anything touching the g2g-owned branch forest, read
  `design-docs/g2g-owned-graphs.md` first.
- **Graphite is not authoritative.** Structure is resolved per invocation, and a
  branch the g2g graph records wins over anything Graphite declares; Graphite
  answers for whatever g2g has not adopted. Read
  `design-docs/source-resolution.md` before changing how a command selects a
  stack, and never reintroduce the assumption that Graphite decides.
- `link` previews by default. Its optional `--branch` target must work without
  checkout; `--apply` is the only path that may invoke `gh stack link`. Bare
  invocation prints help.
- `--take` only ever changes the outcome for a branch that has genuinely
  diverged from its own published version; every other classification `collect`
  makes is take-independent. `--through <branch>` bounds it to that branch and
  what it is stacked on — its ancestry, never a position in a flattened list,
  so a sibling fork is outside the boundary — and refuses the divergence
  elsewhere, which is a narrowing rather than an
  enabler: unbounded, `--take published` reaches every diverged branch in the
  selection, including ones the user was not thinking about.
- `sync` has nothing to do with pull requests. It brings a stack up to date with
  its remote: fetch into g2g's own ref namespace, fast-forward the base or
  refuse if it has diverged, and replay. It never calls `gh`.
- `prune` forgets branches whose work has landed. It is its own command rather
  than sync's tail because it answers a different question on the same
  boundary, it edits the recorded graph and deletes no branch, and it refuses to
  strand a branch recorded under a landed one rather than reparenting around it.
- `push` is a preview-first publication escape hatch. It selects a path through
  source resolution, must never submit or restack, and must never call `gh`;
  only `--apply` may run exactly one
  `git push --atomic --force-with-lease <remote> <branches>` call. Keep the
  remote default explicit (`origin`), validate it, and never fall back to a
  weaker push mode.
- A command that did part of what it was asked and stopped exits `3` — not `0`,
  which told a script the work had finished, and not the failure status, because
  what it achieved is not coming back. `sync` stopping mid-replay, `land`
  stopping after something merged or was tidied, `comment` stopping after
  writing some comments, and `create -m` whose commit failed after the record
  are all this; a descent that changed nothing is an ordinary failure.
  `stoppedPartWay` marks it and nothing
  further is printed, because the report is already on stdout.
- `land` takes a finished stack down onto its trunk, bottom branch first. Read
  `design-docs/land.md` before changing it. It refuses a stack g2g has not
  adopted: it resolves through whichever source describes the branch but
  replays and forgets in g2g's own graph, so on a Graphite-described stack it
  would merge every pull request and then restack and forget nothing. It owns
  no rules of its own: it publishes through `push`, advances and replays
  through `sync`, and asks Git by content whether a branch has landed through
  the same check `prune` uses. Do not give it its own copies of those
  refusals — a lease built from tips it read itself always matches, so a
  direct push would overwrite a reviewer's commit and then merge it. It aims
  every pull request at the trunk rather than at the branch below it, which is
  where each correctly sits now and which will not exist by the time its turn
  comes. It waits for GitHub twice, on the push and on the merge, and for
  neither does it wait on CI; the merge wait is answered by ancestry from the
  merge commit, never by watching a tip change, because a colleague's push
  changes that too. It merges nothing until every branch has been found
  landable. Cleanup never fails a descent. It is not journaled and must not
  become so: re-entrancy comes from recomputation, and `restack` stays the only
  resumable operation.
- How much of the structure a command means is `--scope`, and it means the same
  thing whichever record answered. Read `design-docs/stack-scope.md` before
  changing it. `--from` pins which source answers for one invocation.
- A command named inside a hint is marked at the point the sentence is written
  (`runnable("g2g link")`) and drawn when the sentence is styled. Do not add a
  highlighter that finds commands by pattern: what is drawn as runnable must be
  exactly what can be copied and run. Marks are control characters, never
  content, so keep them out of anything that becomes an `error` — stderr is
  undecorated and would print them verbatim — and strip them for `--json` and
  `--porcelain`. `Presentation.style` re-opens the enclosing style after each
  command, because ANSI ends a style by returning to the default and a subdued
  hint would otherwise come back bright from its first command onwards. The
  chip's padding column is painted, never written into the sentence, so plain
  output is unchanged.
- A package that refuses says why and what to do as a `repair.Note`, and
  derives its `Blocked` sentence from it. `internal/cli` renders the sentence
  for a machine and lays the same values out for a person — reason on its own
  line, one way out per line, each command drawn as a command. Do not hand-write
  a refusal sentence beside the structure: the pair drifts, and the sentence is
  the half a machine reads. A refusal delegated from another package carries no
  structure here, so `refusing` takes the sentence too and still says why.
- Linking has two halves and they must stay apart: `Presentation.hyperlink` is
  the capability (may this output carry a link), and `internal/cli/links.go` is
  the policy (what does a thing point at, and which service wins). A render site
  never builds a URL. Add a destination by adding a resolver to an ordered list;
  add a linkable thing by adding a subject type and its own list. GitHub
  outranks Graphite for a pull request because its address was reported rather
  than assembled.
- `--debug` is a persistent, stderr-only diagnostic flag on every command. It is
  safe for local investigation but must not alter command behavior or cause
  agents to enable Graphite's own `gt --debug`.
- Never guess a Graphite trunk from its name. The selected ancestry determines
  the inferred trunk; multiple valid declared trunks require `--trunk`, whose
  value must itself be declared and ancestral. A g2g-owned path has one root,
  so `--trunk` may only confirm it and must refuse any other value rather than
  ignoring it.
- Read `design-docs/graphite-cli-contract.md` before changing discovery. Do not
  read Graphite internal metadata/configuration or use `gt --debug`: supported
  production discovery is strict, compatibility-gated noninteractive CLI
  parsing.
- `mirror` is the **only** command that writes Graphite, through exactly
  `gt track <branch> --parent <p> --no-interactive` and
  `gt untrack <branch> --force --no-interactive`, both behind the same version
  gate as discovery. Every other command's Graphite use stays read-only. Read
  `design-docs/source-alignment.md` before touching `internal/align`.
- No g2g command may enrol a repository into Graphite — **including the ones
  that write it**. Reading Graphite's forest is what creates state, so `mirror`
  and `import` check `graphite.Configured` and refuse before reading. A
  repository with no Graphite has no trunk and could not be mirrored into
  anyway, so nothing is lost by refusing first.

## g2g-owned graphs

- `graph`, `track`, and `untrack` operate on a branch forest g2g owns
  itself. They read Git only: never call Graphite or GitHub from these paths,
  and never make them require a network. That independence is the feature.
- The model is a forest: at most one parent per branch, many children per
  parent, several roots. Do not reintroduce a linear assumption. Graph identity
  is derived from the edges, never stored; do not add graph IDs.
- Authority is per branch (`g2g` or `graphite`), never per graph. A whole-graph
  rule cannot survive two components becoming connected by an action g2g
  never observed.
- `track` must never choose a parent. Preview the ordered candidates and block.
  Recording a structure every later command trusts is not a place for a good
  guess. `track --stack` is not an exception: the user asserts the trunk and
  ancestry supplies the rest, and it refuses wherever ancestry cannot order two
  branches. It records a forest, never a chain — a branch whose only selected
  ancestor is the trunk is a separate stack and must be left alone.
- A trunk is a branch nothing sits under. `Graph.Adopt` owns both halves of that
  invariant; do not pair `Track` with a hand-rolled promotion step, and never
  take the trunk list from the graph as it was before the edge was recorded.
- `untrack` must never reparent the children it strands. Report them.
- `create` takes its parent from the branch you stand on or `--parent`, which
  is the user stating it, so it offers no candidates. It refuses a parent the
  graph does not record (tracked, or a trunk something sits under) unless it is
  the repository's default branch, because recording a child under an unknown
  branch silently makes that branch a trunk. It records through `PlanTrack` and
  `ApplyTrack`, never its own write. Order is switch, record, commit: a failed
  record is rolled back completely (switch back, delete the branch, which has
  nothing on it), and a failed commit after the record keeps both and exits `3`.
- `up`/`down`/`top`/`bottom` are the only commands that act without `--apply`,
  and that exception is deliberate: moving the checkout changes no ref, record
  or remote, and `git switch` refuses to clobber local changes. Do not add a
  preview, and do not extend the exception to anything that writes. They
  resolve through the same `stack.Resolver` as every stack command, refuse at a
  fork naming the children, switch with `--no-guess`, honour the restack guard,
  and offer `--dry-run`. A trunk is undescribed by every source, so from one
  they ask the g2g graph for its recorded children and resolve onward from the
  single child.
- `mirror` and `import` must never remove a branch from the g2g graph.
  Alignment keeps the two records in step; it does not transfer ownership.
  `mirror` writes Graphite only, `import` writes the g2g graph only, and
  `import` refuses a branch the g2g graph already records under a different
  parent rather than resolving the disagreement.
- `import --from pull-request` adopts a published stack through the same
  planning (`planAdoptions`: additive, conflict refusal, parents first); only
  the record read and the fork point differ. `--from graphite` stays the
  default and unchanged, `--from g2g` is refused, and `--branch`/`--scope
  stack|trunk` are refused with Graphite because it is read whole. It selects
  through the pull request source's own `Select` and is the only import that
  invokes `gh`. It never creates a branch: anything the pull requests place that
  is not local (`Snapshot.Absent`, or a base not here) refuses the plan with
  `git fetch && git switch <branch>` / `git branch <branch> origin/<branch>`.
  The stack's base must already be recorded or be `DefaultBranch` — evidence
  permitting a root, as in `create`, never choosing a parent. The fork point is
  the merge base with the base, never the base's tip, because the base may have
  moved since the pull request was opened. Its revalidation re-reads GitHub, so
  it is wired with a selector that has no memo.
- Mirror ordering is dictated by Graphite's CLI, not by taste: writes go
  parents before children because `gt track --parent` requires a tracked
  parent, and prunes go deepest first — refusing any stranger with a surviving
  child — because `gt untrack` cascades to the subtree.
- Do not record commit SHAs in the store: commits and force-pushes are content
  movement, not structural drift. Validate against Git at read time instead.
- `restack` is the only code permitted to rewrite history, and only through
  `internal/git`'s two engines. `git replay` previews exact object ids without
  moving a ref and applies cleanly without touching the checkout; `git rebase
  --update-refs` is used only once a preview has established the rewrite
  conflicts, and it runs in the user's own working tree because resolving a
  conflict needs a tree they can edit. Do not move it to a private worktree:
  git refuses to check out a branch already checked out elsewhere, and the
  `--detach` workaround silently splits the stack in two.
- The replay range is `forkPoint..branch`, never `base..branch`. Before any
  rewrite, the fork point must be an ancestor of the branch — a branch someone
  rebased by hand fails that, and replaying anyway pulls the base's own commits
  into the range. Refuse and tell the user to retrack.
- A branch whose parent is being rewritten must be rewritten too, even when it
  still sits exactly where its fork point says. Each independent root is its
  own replay, and every range passed to an engine starts at that root's fork
  point, because an engine replays the union onto one base.
- restack is the only resumable operation. Every other mutating command must
  refuse while its journal exists, `--continue` recomputes from the refs rather
  than resuming a stored queue, and `--abort` restores tips the journal
  recorded because git only rolls back the invocation it was running.
- The store lives under the Git common directory and is located with
  `git rev-parse --path-format=absolute --git-common-dir`. The bare form is
  relative to the working directory and silently wrong from a subdirectory.
  Writes are temp-file plus rename. `storeSchemaVersion` is separate from the
  `--json` `schemaVersion`; an unrecognised store version fails closed.
- `--scope branch|path|subtree|stack|trunk|all` is selection, not projection
  policy. Displaying a subtree does not imply a subtree can be linked on GitHub.
  The type and the traversal live in `internal/shape`, which depends on
  nothing, because every record answers them and `internal/graph` needs them.
- **A command must refuse any scope it did not offer, and name its own
  default.** They genuinely differ: `status` and `graph` default to `stack`
  because reading is free, `restack` to `subtree` because rewriting is not, and
  `all` is offered only where nothing is rewritten: the read-only commands, and
  `prune`, which edits the record and forgets only what has landed. `ParseScope` takes both the accepted
  set and the fallback; there is no global default left to inherit.
- Projection is a capability, not a scope. `link`, `submit`, `push` and
  `retarget` take `stack|path` and refuse a forked selection through
  `Snapshot.RequireLinear`, which names the remedy instead of choosing a line.
- Selected from a trunk, `stack` is the whole tree under it — a trunk's path is
  itself. That is how a rewrite asks for an entire shape without being handed a
  scope that could reach another trunk.
- `all` is deliberately absent from `RewriteScopes`: it spans trunks, and a
  rewrite acts on one. `trunk` is offered, and a rewrite that wide is the
  likeliest to reach a branch checked out in another worktree, which the
  rewrite then refuses by name.

## Source resolution

- Every stack-selecting command resolves which source describes the branch:
  g2g's own store first (adoption is the claim), then Graphite. The answer is
  derived per branch on every run and never stored — there is no owner field,
  and adding one reintroduces state that goes stale through actions g2g never
  observes.
- **Never run Graphite in a repository that does not already use it.** Its
  discovery command creates state, so `Describes` is answered from the
  repository's own configuration and `Select` is the only call that runs `gt`.
  Checking for that file is the single deliberate exception to reading none of
  Graphite's paths, and only its existence is ever read.
- Authority governs mutation, not description. Reading composes across sources;
  `restack` refuses a branch it has no fork point for and names `g2g track`.
- GitHub's native stack is not a source. It is written from the others, and is
  only ever read to report membership or to find a stack to unlink.
- `pull-request` is the third source and answers only via `--from
  pull-request`, never by precedence: reading a base invokes `gh`, and `push`
  must never do that. It describes published branches only, and GitHub
  retargets a child when its base is deleted on merge, so it reports what a
  merge will do rather than what the stack was.
- `link` covers both creating and repairing the GitHub relationship; there is
  no separate reconcile command. `sync` means fetch, advance the base, replay.
  It forgets nothing: `gt sync` prunes as its tail and this does not, because
  forgetting a landed branch is a different question on the same boundary and
  belongs to `prune`.
- A diverged base is reported, never merged or reset. Pruning edits the graph
  and never deletes a branch.

- `comment` keeps one marked comment per pull request listing its stack. Read
  `design-docs/stack-comment.md` before changing it. It always keeps the whole
  stack the branch belongs to — each comment lists its own ancestors and
  descendants, so a partial run would leave comments disagreeing — and has no
  `--scope`. Merged history lives in the comments' own data line because
  nothing local remembers a pruned branch; keep only what GitHub says merged,
  never create a comment on a merged pull request, never edit a comment without
  the marker, and leave alone one the viewer cannot edit or a pull request
  carrying two. Writes are `addComment`/`updateIssueComment` by node id with the
  body as a raw `-f` field; errors must not echo the body.
- `retarget` is the only command a user runs to change what a merge will do,
  and `land` reaches for the same client method rather than growing its own —
  a child's base only goes stale during a descent, once the branch below it has
  merged, and every move it makes appears in the preview. It writes
  through exactly `gh pr edit <number> --base <branch>`, moves only the bases
  that disagree with the resolved stack, and refuses a branch with more than one
  open pull request rather than choosing between them. Do not fold it into
  `submit` or run it as the tail of `restack`.

## Develop and test

- Keep external process calls behind `internal/subprocess.Runner`. Tests must
  use fake `gt` and `gh` executables on `PATH`, including captured supported
  Graphite text fixtures; never require credentials, network access, or real
  CLI installations. Graph ancestry is the one exception where a PATH fake
  proves nothing — it answers whatever it is asked, and the question is what
  Git considers reachable — so those cases build a throwaway local repository
  with synthetic branch names and no remote.
- Preserve the `completion bash|zsh|fish` interface. Dynamic `--branch` and
  `--trunk` completion must remain deterministic, read-only, and checkout-free —
  and must reach no source the command itself would not reach. Completing a
  flag must never be what enrols a repository into Graphite, and must keep
  working with no Graphite installed.
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
- A refusal reaches a machine as `blocked` (the reason, no label) and `repair`
  (the ways out, each with its command separate from the prose). Read `repair`
  rather than parsing the sentence, and treat a way with no `command` as a real
  answer that is not a thing to run.
- Prefer `--json` (or `--porcelain`) over parsing the human preview. Both are
  renderers over the same validated view, they suppress colour and every
  human-facing line, and `schemaVersion` signals breaking changes. Never scrape
  the pretty graph.
- A blocked preview names the repairing command, decided once in
  `link.Plan.Repair` (a `repair.Note`) and rendered from it for both readers:
  merged pull requests point at `g2g sync` for a g2g-recorded stack and `gt
  sync` for a Graphite one, landed branches at `g2g prune`/`gt sync`, missing or
  closed ones at `g2g submit`, and a wrong base at `g2g retarget` — never
  `sync`, which does not touch pull requests. A structure read from pull request
  bases gets no command, because nothing here records it. Two open pull
  requests for one branch is deliberately unadvised — a person must choose.
- A branch's annotation is a list of `stackMark` — one axis each, one severity
  each: `base✓`/`base✗`, `head✗`, `pr✗`, and a subject-less mark for what is
  about no axis. Build them and call `stackNode.marked`, which renders `State`
  and the worst `Severity` from them; never set `State` alongside marks, and do
  not fold two axes into one mark, which is the failure this replaced. A merged
  pull request is `pr✓` and neutral, never grouped with a missing or closed
  one: it succeeded, and only the leftover branch is a problem.
- Batch before parallelising, and do not reintroduce a call whose only purpose
  is to feed the next one: `Inspect` is a single `gh api graphql` naming the
  repository through `{owner}`/`{repo}`, and revisions resolve through
  `git.Client.ResolveAll` in one process.
- Per-branch Git reads go through `link.eachBranch`, which runs them several at
  a time and cancels the rest on the first failure. Write results into a slice
  index the read was given, never a shared map, and keep any fake it can reach
  safe to call concurrently. `go test -race ./internal/link ./internal/cli`.
- Currency is counted by content (`Cherry`, never `Divergence`) and bounded to a
  branch's own commits — above its parent, not above the trunk. Counting commit
  ids reported every commit the trunk had gained as unpushed work of the
  reader's own. A branch replayed since it was pushed is `Currency.Rewritten`:
  nothing missing, needs pushing, and not a divergence.
- "Landed" is a content question Git answers and GitHub cannot, and it needs
  both halves: `Cherry` per commit, then `Absorbed` for the squash merge. A
  landed branch is `link.IssueLanded`, never `IssueMissing` — the advice for
  missing is `g2g submit`, and submitting work already in the trunk is the bug
  this prevents. What forgets it depends on the source (`g2g prune` for g2g's
  graph, `gt sync` for Graphite), so do not hardcode one.
- `status` is the read-only triage entry point. It renders one selected
  path from the resolved g2g or Graphite structure and reports each selected
  PR's native GitHub stack membership from the same batched PR query; keep the
  healthy case to one compact summary line and annotate only
  missing/conflicting nodes. `unlink` is the deliberate inverse of `link`: it
  discovers the GitHub stack number from the selected path and refuses rather
  than guesses when that path is unlinked or spans several stacks, accepts
  `--stack-number` to override, previews first, and only `--apply` invokes
  `gh stack unstack`. It must never alter Graphite, branches, PR content,
  reviewers, or PR lifecycle.

- After command discovery, use the resolved command's `--help` or `link --help`
  to inspect the current interface (for example, `g2g link --help` after a
  Homebrew install).
- Read the preview before any `--apply`. `sync` never changes GitHub; the
  commands that do are `link`, `unlink`, `submit`, `retarget`, `comment` and
  `land`.
