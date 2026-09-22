# The stack comment

`g2g comment` keeps one comment on every pull request in a stack, listing the
stack from where that pull request stands. It is the map a reviewer does not
otherwise have: GitHub shows a pull request in isolation, and finding the other
four in a stack of five means reading bases one at a time.

```
<!-- g2g:stack-comment -->
**Stack**

Merged into `synthetic-main`: #10

- `synthetic-main`
- #11 `synthetic-one`
- **#12 `synthetic-two`** 👈 this pull request
- #13 `synthetic-three`

<sub>Kept up to date by g2g, which edits this comment when the stack changes.</sub>
<!-- g2g:stack-prs 10,11,12,13,14 -->
```

It previews by default. `--apply` revalidates and then writes.

## Which comments a run keeps

**The whole stack the branch belongs to, whichever branch it is run from.** Each
comment lists its own pull request's ancestors and descendants — a cousin that
merely shares an ancestor is another branch's business — so a fork shows up in
some comments and not in others. Keeping only the part of a stack a scope
reached would leave the rest describing a different stack, and a run from a
different branch would rewrite them again. Taking the whole stack from its
bottom is what makes every run from anywhere on it converge on the same
comments. That is also why the command has no `--scope`.

From a trunk it keeps every stack on it, each separately: one stack's merged
pull requests do not appear in another's map.

## Why the history lives in the comment

A pull request that merged out of a stack is still listed. Nothing local can
answer for it: `prune` forgot the branch, `land` deleted it, and GitHub has no
record that it was ever part of a stack. The comments do, so each one records
every pull request the stack has listed on its last line, and the next run reads
that back.

- Only numbers go inside the HTML comment, so nothing a branch name contains can
  close it and spill into what GitHub renders.
- The numbers are editable by anyone who can edit the pull request, so reading
  them back tolerates junk, is bounded, and follows them for a few rounds at
  most. A number that turns out to be an issue answers nothing.
- A listed pull request is kept only when GitHub says it **merged**. One closed
  without merging, or open in some other stack now, has left this one and drops
  out.
- Every comment in a stack records the same numbers, so any one of them is
  enough to recover the whole history.

## What it writes, and what it will not

| The pull request | Its comment |
|---|---|
| open, no comment yet | added, if the stack lists at least two pull requests |
| any, one comment that is out of date | edited |
| any, one comment already saying this | left alone |
| merged, no comment | **never added** — nobody is reviewing it, and a new comment notifies everyone who did |
| one comment you cannot edit | left alone, and said |
| two or more comments | left alone, and said: a person deletes the extra |
| a branch with two open pull requests | the whole run refuses, as `link` and `retarget` do |

A comment is recognised by its marker alone, never by author. Adding a second
beside one somebody else wrote would leave two comments saying the same thing
differently, which is worse than one that is out of date and says who can fix it.

A body edited in a browser comes back with CRLF endings. That is not a reason to
edit it again, so the comparison normalises line endings.

## The GitHub seam

Reading is `Conversations`: one aliased GraphQL query per round,
`issueOrPullRequest(number:)` for every number still being read, paging only
the conversations that run past a hundred comments. The query is named
`StackComments` for the same reason the mergeability query is named — all three
reads go to one endpoint, and a recorded call or a fake tells them apart by the
operation.

Writing is `addComment` and `updateIssueComment`, addressed by node id. The REST
comment id would do as well, except that GitHub's comment ids have outgrown the
GraphQL `Int` that reports them. The body travels as a raw `-f` field, which gh
passes through as a string: it reads no file for a value starting with `@` and
fills no `{owner}` placeholder. A failed write names the mutation and never
repeats the body back.

## What was deliberately left out

- **Titles.** They would make every retitle an edit, and GitHub already shows a
  referenced pull request's title on hover.
- **Running from `submit` or `land`.** Posting to every pull request is a
  different class of act from creating one, and wants its own preview.
- **Deleting a comment.** A stack that shrinks to one pull request keeps the
  comment it has; nothing here removes a person's view of history.
