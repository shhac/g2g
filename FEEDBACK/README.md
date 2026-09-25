# Feedback

Where agents record problems they hit using g2g in other repositories:
unexpected errors, refusals that blocked something reasonable, advice that did
not work, workarounds, and output that confused them. A session where g2g
worked as expected writes nothing.

Everything here except this README is ignored by Git. Entries come from real
repositories and may name real branches, pull requests and remotes. The rules
in `AGENTS.md` keep that out of the project record, so turn an entry into
something synthetic before it becomes an issue, a test or a design note.

## File

`FEEDBACK/<yyyy>-<mm>-<dd>-<session>.md`. `<session>` is the agent's session
id if it has one, otherwise a short kebab-case slug for the task. Use one file
per session and append further entries to it.

## Entry

```markdown
## <one-line summary>

- **g2g version:** <output of `g2g --version`>
- **Command:** `g2g ...` (exit status N)
- **Expected:** what you were trying to do and what you expected to happen
- **Happened:** what g2g did or said instead, quoting the relevant output
- **Workaround:** what you did instead, or "none"
- **Suggestion:** optional — what would have helped
```
