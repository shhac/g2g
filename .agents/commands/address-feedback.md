---
description: Work through FEEDBACK/ — reproduce, fix, structural pass, docs, release
argument-hint: "[patch|minor|major] (default: minor)"
---

# Address feedback

`FEEDBACK/` collects what agents hit using g2g in other repositories (see
`FEEDBACK/README.md`). This is the cycle that turns it into fixes and a
release. Run it autonomously once started: decide, verify and note what was
decided, rather than stopping to ask.

The order matters. Work the whole list before the structural pass, so the pass
sees all the new code at once, and do not release between items.

## 1. Triage

- Read every file in `FEEDBACK/` except the README. Deduplicate entries that
  describe the same problem, and number what is left.
- Entries come from real repositories. Nothing from them — branch names, pull
  request numbers, remotes, output — goes into a commit, test, fixture or doc.
  Abstract each one to `synthetic-*` names first (`AGENTS.md`, *Fixtures and
  data hygiene*).

## 2. Reproduce each item before touching it

- Reproduce in a throwaway repository in a scratch directory, or as a failing
  journey test (`internal/cli/*_journey_test.go`, real Git and a real bare
  remote). A journey test that fails first becomes the regression test.
- A feature gap's reproduction is showing the gap: "we can't do X", or "X is
  harder than it should be".
- Can't reproduce? Ask for a second opinion on how to (Codex, e.g.
  `codex exec -m <model> --sandbox read-only`). Still can't? Leave the item
  for now and say so in the report. Don't fix what you couldn't see.

## 3. Fix

- Fix what is a real bug, and close a gap when it is worth closing. When an
  item asks for something the design rules out, keep the rule and make the
  refusal useful: say what was left out or refused, and give the exact
  recovery command (`selected.aim`, `repair.Note`).
- Read the design doc for the area first (`AGENTS.md` names them), and keep
  the shared seams it lists shared.
- Get a second opinion on design choices that are not obvious.
- One commit per item, straight on `main`, conventional format
  (`fix[area]: …`, `feat[area]: …`), staged with `git hunk`.
- Per change: `gofmt -w`, `go vet ./...`, and the package's tests. The full
  suite is `go test -timeout 40m ./...` (`internal/cli` takes minutes).

## 4. Structural pass

Once every item is done, run the improve-code-structure process over the
range since the last release tag (`git tag --list 'v*' --sort=-v:refname |
head -1`) and apply its recommendations without waiting for approval. In past
cycles it found real bugs in the new code, so treat it as part of the work,
not a formality. Then do a cost pass: anything new that asks Git or GitHub a
question per branch is checked against `design-docs/cost.md`.

## 5. Docs and skill

Bring every place that describes the changed behaviour up to date: `README.md`,
the `design-docs/` for each area, `skills/g2g/SKILL.md` (validate it with the
skill-creator quick validator), and `AGENTS.md` where a rule changed.
Record anything deliberately deferred where the next person will look for it
(e.g. `design-docs/cost.md` for known costs).

## 6. Clean up

Delete each feedback entry that was addressed, and a file once all of its
entries are. Keep `FEEDBACK/README.md`. Leave unreproduced items in place.
Ask before deleting any plan file under `.ai-cache/`.

## 7. Release

Full verification first: `gofmt`, `go vet ./...`, `go test -timeout 40m
./...`, and the race suite
(`go test -race ./internal/link ./internal/graph ./internal/parallel ./internal/land`).
Then follow `.agents/commands/release.md` with `$ARGUMENTS` (default
`minor`), watch the `Release`, `Publish skill` and CI workflows, and upgrade
the local install.

## Report

For each item: whether it was reproduced and how, what changed, and the
commit. Then what the structural pass found, what was deferred and why, and
the release link.
