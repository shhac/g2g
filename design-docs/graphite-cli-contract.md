# Graphite CLI display contract

`gt2gh` discovers Graphite relationships only through Graphite's supported
read-only CLI surface. It never reads Graphite's private metadata database or
configuration and never enables `--debug`.

## Pinned command

The production adapter first requires this exact output:

```text
gt --version
1.8.6
```

It then runs exactly:

```text
gt log short --all --reverse --no-interactive
```

The fully synthetic fixture at
[`internal/graphite/testdata/irregular-stack.txt`](../internal/graphite/testdata/irregular-stack.txt)
models the compact 1.8.6 tree grammar with unequal sibling paths, a repeated
connector, and a nested fork. It contains no user repository data. Tests use
only fake executables on `PATH`.

## Accepted grammar

The parser accepts an ordered sequence of compact branch rows. Each row has a
tree prefix made of `│ ` or `  ` groups, followed by `◯` or `◉`, an even run of
at least two spaces, and a nonempty branch name with optional ` (current)`. The
spaces are visual label padding. A row may put a connector between the node glyph
and its name padding: `─┐` opens one child lane, and `─┬─…┬─┐` opens exactly its
number of visual child lanes. One empty separator line may appear only between
rows that do not open a connector.

The next deeper row must occupy the exact lane opened by its preceding
connector. Equal-depth rows extend a branch; shallower rows attach to the node
that opened that lane. Traversal reconstructs only the selected branch's
declared-trunk-to-leaf ancestry, excluding siblings and descendants. The first
depth-zero row is the declared Graphite trunk for this pinned display contract.

Any different version, unclassified line, duplicate branch, malformed record,
or inconsistent fork/depth transition is an error. The parser tests mutate each
record component and graph marker in the captured fixture to guard that
fail-closed boundary. A future Graphite CLI with supported structured
target-stack output should replace this version-pinned text parser.
