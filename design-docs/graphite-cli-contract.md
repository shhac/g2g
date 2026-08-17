# Graphite CLI contract

`g2g` reaches Graphite only through its supported, noninteractive CLI. It
never reads Graphite's private metadata database or configuration and never
enables `--debug`.

Discovery is read-only. `mirror` is the one command that writes, and it writes
only through the two commands documented under [Write contract](#write-contract).

## Command contract

The production adapter first calls:

```text
gt --version
```

It requires a conventional `major.minor.patch` response and supports major
version 1. Version 1.8.6 is the fixture-tested baseline. A different compatible
patch or minor version writes one stderr warning, then continues to the strict
display parser; an unsupported major or unrecognizable version response fails
before discovery. This preserves forward compatibility without treating a
version string as proof that the display grammar is safe.

It then runs exactly:

```text
gt log short --all --reverse --no-interactive
```

The fully synthetic fixture at
[`internal/graphite/testdata/irregular-stack.txt`](../internal/graphite/testdata/irregular-stack.txt)
models the compact 1.8.6 tree grammar with unequal sibling paths, a repeated
connector, and a nested fork. It contains no user repository data. Tests use
only fake executables on `PATH`.

## Write contract

`g2g mirror` writes Graphite through exactly two commands, both gated on the
same version check as discovery:

```text
gt track <branch> --parent <parent> --no-interactive
gt untrack <branch> --force --no-interactive
```

Three properties of these commands shape the mirror design, and each was read
from `gt track --help` / `gt untrack --help` on 1.8.6 rather than assumed:

- **Both take an explicit branch argument.** `gt track [branch]` and
  `gt untrack [branch]` default to the current branch when it is omitted, so
  passing it is what keeps mirroring checkout-free. Nothing else would be
  acceptable: a mirror that checked out every branch in the forest would be a
  worse operation than the drift it fixes.
- **`--parent` must name an already-tracked branch.** Writes are therefore
  ordered **parents before children**, and a mirror that adds a chain adds it
  from the root down.
- **`gt untrack` cascades to children.** Untracking a branch untracks its whole
  subtree, which is why `--prune` refuses a branch with a Graphite child that
  is not itself being pruned, and why prunes are ordered **deepest first**.
  Without both, pruning one stale branch would silently untrack the branches
  the mirror had just aligned.

`--force` on `untrack` suppresses only the confirmation prompt for a branch with
children; it does not change what is removed. `--no-interactive` is passed for
the same reason it is passed to `gt log`.

## Accepted grammar

The parser accepts an ordered sequence of compact branch rows. Each row has a
tree prefix made of `│ ` or `  ` groups, followed by `◯` or `◉`, an even run of
at least two spaces, and a nonempty branch name. Its accepted terminal label
forms are exactly: bare; ` (current)`; ` (needs restack)`; one nonempty, flat
opaque worktree annotation; or ` (needs restack)` followed by one opaque
worktree annotation. Worktree annotation text is display metadata, not part of
the branch name. Extra, reordered, duplicate, empty, or nested parenthetical
suffixes fail closed. The spaces are visual label padding.
A row may put a connector between the node glyph and its name padding: `─┐`
opens one child lane, and `─┬─…┬─┐` opens exactly its number of visual child
lanes. One empty separator line begins a separate configured-trunk component;
it may appear only after a row that does not open a connector.

The next deeper row must occupy the exact lane opened by its preceding
connector. Equal-depth rows extend a branch; shallower rows attach to the node
that opened that lane. Default traversal extends from the selected branch
through one unique direct-child chain to its tip, excluding siblings; a
descendant fork is an ambiguity error, never an inferred child choice.
`--no-stack` opts out of this extension and reconstructs only the selected
branch's declared-trunk-to-selected-branch ancestry. Each
separator-delimited component has its own root; components are never connected
by inference. Link-base resolution considers only Graphite-declared trunk roots
on the selected ancestry and fails closed when more than one is valid unless
the user supplies a valid `--trunk` override.

An unrecognized version response, unsupported major, unclassified line,
duplicate branch, malformed record, or inconsistent fork/depth transition is
an error. The parser tests mutate each record component and graph marker in the
captured fixture to guard that fail-closed boundary. A future Graphite CLI with
supported structured target-stack output should replace this compact text
parser.
