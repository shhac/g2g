# g2g land trial, part 2: finishing the descent by hand

After `g2g land` stopped part-way (see `TRIAL-FINDINGS.md`), the remaining four
branches of the stack were landed manually with `git push`, `gh pr merge` and
`gt sync`. All four merged cleanly. This records the loop that worked, and the
two hazards it exposed that `land` would need to handle to be trusted with an
unattended descent.

The manual run is not an argument against `land`. The preview it produced was
the plan followed by hand, almost step for step. What the manual run supplies is
evidence about the two places the automated one has to be more careful than it
currently is.

## The loop

Per branch, bottom of the stack first:

1. `git checkout <branch>` — the one whose parent is `main`
2. `git push --force-with-lease origin <branch>`
3. Poll `gh pr view <pr> --json headRefOid,mergeable,state` until GitHub reports
   the pushed commit **and** `MERGEABLE`
4. `gh pr merge <pr> --squash --admin`
5. Confirm `state=MERGED` and read the resulting squash commit
6. `git fetch origin main` and confirm the squash is `origin/main`'s head
7. `gt sync --delete-all --no-interactive` — restacks what is left, deletes the
   merged branch, reparents the next one onto `main`
8. Repeat

Four iterations, four merges, no conflicts, no manual repair.

| PR    | Squash commit | Content                         |
| ----- | ------------- | ------------------------------- |
| 23151 | `3eaa0888203` | one rule per affiliation        |
| 23152 | `12926a4d398` | providers into one package      |
| 23153 | `4ca894a8615` | the Athletics Quebec provider   |
| 23154 | `210433adaaa` | checkout wiring and credentials |

`23150` had already been landed by `g2g land` itself at `5bb3d40b14d`.

## Finding 1: GitHub needs to be polled after a push, not waited on once

This is the finding that matters most for `land`.

On two of the four branches, GitHub reported stale information immediately after
a successful force-push:

```
23153  attempt 1: f639ede8187 CONFLICTING OPEN     ← old head
       attempt 2: 33ff7ad8de9 MERGEABLE   OPEN     ← settled

23154  attempt 1: 86285a1a442 UNKNOWN     OPEN     ← old head
       attempt 2: 8c5e1e40ae6 MERGEABLE   OPEN     ← settled
```

Both settled on the second poll, five seconds later. Merging on the first
reading would either have failed outright or merged the wrong commit.

Note the two distinct wrong answers. `CONFLICTING` is the dangerous one: it is a
plausible, actionable-looking state that an operator would believe, and the
obvious response — rebase again — would have been wrong. `UNKNOWN` at least
signals that GitHub does not know yet.

**Suggestion for `land`.** Treat `mergeable: UNKNOWN` as "not settled yet" rather
than as a state to act on, and do not trust `CONFLICTING` until the reported head
matches the pushed tip. The head OID is the reliable signal and the mergeability
is derived from it; checking the OID first removes both failure modes.

**This is not the same failure part 1 hit.** There, the remote never received the
commit at all (`origin` stayed at the pre-replay tip through 66 attempts), so
longer polling would not have helped. These are two separate problems that
present with the same symptom — a wait that does not resolve — and telling them
apart requires checking the remote ref directly, which is cheap.

## Finding 2: `gt sync` can take the remote's version of a branch

Three times during the loop, `gt sync` reported:

```
Synced paul/ex-1175-athletics-quebec-provider (PR #23153 v10) from remote
  (previously at 4f61bff7702...)
```

"from remote" is exactly the operation that discards local work when the remote
is behind. Here it was safe, because everything had been pushed by a prior
`gt submit`, but the message gives no way to tell a safe sync from a destructive
one.

Each branch was therefore checked by **content** before merging rather than by
SHA — the SHA changes on every restack and proves nothing about what survived:

- `23153`: `toCalendarDate` present (date normalisation, added last), `timeoutSpy`
  present (the rewritten `AbortSignal` assertion), `describe('membershipStanding')`
  present (the renamed suite)
- `23154`: the `EX-1216 step 3` comment present, `endsWith` present in the API
  root normalisation

All intact. But this needs verifying every time, and an automated descent that
calls `sync` between merges inherits the same exposure.

## Finding 3: re-tracking in Graphite offers to discard the replay

Between part 1 and this run, the stack had to be re-tracked in Graphite, because
`gt sync` untracked it when the first branch merged. `g2g mirror` restored the
structure correctly. Graphite then prompted, per branch:

```
? Branch paul/ex-1173-affiliation-rules-package exists locally, but has never
  been submitted or synced with Graphite. Overwrite it with the remote version?
  › (Y/n)
```

Three things make this sharp:

1. The premise is false. The branch **had** been submitted — it is PR #23151,
   open at the time. Graphite lost that association when it untracked the branch,
   and `mirror` restores the parent edge but not Graphite's record of submission.
2. The default is `Y`.
3. Accepting would have replaced the replayed local commit with the pre-replay
   remote one, orphaning the six branches stacked on it.

Answering `n` to each was correct, and Graphite then rebased the branches onto
current `main` by itself, which is what was wanted.

**Relevance to `land`.** A partially-completed descent leaves the repository in
exactly the state that produces this prompt. If `land` is expected to be
resumable after a stop, it should either leave Graphite's submission record
intact or warn that re-tracking will offer to discard work.

## Finding 4: `gt sync` operates on every branch, not the selection

Each `gt sync --delete-all` restacked unrelated branches and reported problems in
them:

```
WARNING: The following branches could not be restacked cleanly:
▸ shhac/shortio-export-script
▸ paul/ex-644-raceday-gdpr-redact-pii

WARNING: PR #22962 for paul/startlist-import-show-data-errors is merged but
cannot be cleaned up while it is checked out in another worktree.
```

None are part of the stack being landed. In a checkout with six worktrees and
several hundred branches this is most of the output, and the signal about the
stack under way is buried in it. `g2g sync` taking `stack` scope is the better
behaviour here, and worth keeping.

## What the manual run confirms about `land`'s design

- The 28-step preview was accurate. The manual loop is that plan, and nothing in
  it turned out to be wrong or missing.
- Aiming each PR at the trunk before merging is correct: every branch's base had
  to be `main` by the time its turn came, and `gt sync` reparenting the next
  branch onto `main` is the same move by another name.
- Merging bottom-first with a sync between each merge is the right shape. The
  four iterations produced no conflicts.
- `--admin` behaved as expected throughout; no merge waited on CI.

The gap is not the plan. It is the two moments where the tool has to decide
whether the outside world has caught up — after a push, and after a merge — and
in both the answer has to come from comparing a ref it can read, not from waiting
for a state to appear.

## Environment

- g2g 0.30.0, Graphite CLI, `gh`
- Repository: six worktrees, several hundred branches
- Stack: eight branches; five landed (Track A), three remaining (Track B)
- `main` at `210433adaaa` after the final merge
