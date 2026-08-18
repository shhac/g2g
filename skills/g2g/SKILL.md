---
name: g2g
description: |
  Develop, test, or safely use the g2g Go CLI, which records stacked branches
  itself and projects them onto GitHub. Graphite is an optional source it can
  read, mirror to, and import from — not a requirement. Use when working on
  g2g commands, stack structure or linking, restacking, alignment with
  Graphite, CLI tests, or release readiness. Triggers: "gt2gh", "g2g",
  "gh stack link", "g2g link", "g2g sync", "g2g push", "g2g submit",
  "g2g graph", "g2g track", "g2g untrack", "g2g restack", "g2g mirror",
  "g2g import", "g2g retarget", "g2g-owned graph", "stack without Graphite",
  "branch graph without Graphite", "restack after squash merge",
  "source resolution", "source alignment", "retarget pull request base".
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

- Read `README.md` and `design-docs/initial-scope.md` before changing behavior.
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
- How much of the structure a command means is `--scope`, and it means the same
  thing whichever record answered. Read `design-docs/stack-scope.md` before
  changing it. `--from` pins which source answers for one invocation.
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
- `mirror` and `import` must never remove a branch from the g2g graph.
  Alignment keeps the two records in step; it does not transfer ownership.
  `mirror` writes Graphite only, `import` writes the g2g graph only, and
  `import` refuses a branch the g2g graph already records under a different
  parent rather than resolving the disagreement.
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
  still sits exactly where its fork point says. Every range passed to an engine
  starts at the topmost step's fork point, because the engines replay the union
  onto one base.
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
  The type and the traversal live in `stack` because both records answer them.
- **A command must refuse any scope it did not offer, and name its own
  default.** They genuinely differ: `status` and `graph` default to `stack`
  because reading is free, `restack` to `subtree` because rewriting is not, and
  only a read-only command offers `all`. `ParseScope` takes both the accepted
  set and the fallback; there is no global default left to inherit.
- Projection is a capability, not a scope. `link`, `submit`, `push` and
  `retarget` take `stack|path` and refuse a forked selection through
  `Snapshot.RequireLinear`, which names the remedy instead of choosing a line.
- Selected from a trunk, `stack` is the whole tree under it — a trunk's path is
  itself. That is how a rewrite asks for an entire shape without being handed a
  scope that could reach another trunk.
- `trunk` and `all` are deliberately absent from `RewriteScopes`. A wide rewrite
  is far likelier to reach a branch checked out in another worktree, and Git
  refuses to check out a branch already checked out elsewhere.

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
  no separate reconcile command. `sync` means fetch, advance the base, replay,
  prune — the meaning `gt sync` has.
- A diverged base is reported, never merged or reset. Pruning edits the graph
  and never deletes a branch.

- `retarget` is the only command that changes what a merge will do. It writes
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
- Prefer `--json` (or `--porcelain`) over parsing the human preview. Both are
  renderers over the same validated view, they suppress colour and every
  human-facing line, and `schemaVersion` signals breaking changes. Never scrape
  the pretty graph.
- A blocked preview names the repairing command: merged pull requests point at
  `g2g sync` when the recorded stack needs its base advanced and replayed,
  missing or closed ones at `g2g submit`, and a wrong base at `g2g retarget`.
  Two open pull requests for one branch is deliberately unadvised — a person
  must choose.
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
- Require read-only discovery and dry-run output before `sync --apply` can
  change GitHub state.
