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
gt log --all --reverse --no-interactive
```

The fixture at
[`internal/graphite/testdata/irregular-stack.txt`](../internal/graphite/testdata/irregular-stack.txt)
was captured from an isolated disposable Git repository tracked with Graphite
1.8.6. It includes unequal sibling paths and a nested fork. No user repository,
Graphite submission, PR creation, or Graphite API mutation was involved.

## Accepted grammar

The parser accepts an ordered sequence of branch records. Each record is:

1. a tree-guide prefix made of `│  ` or `   ` groups, followed by `◯ ` or `◉ `,
   a nonempty branch name, and optional ` (current)`;
2. a graph-prefixed relative-time line;
3. a graph-only blank line;
4. a graph-prefixed abbreviated lowercase commit hash, ` - `, and title;
5. a graph-only connector line.

Between records, only `├──┐` or `└──┐` fork markers with the same graph prefix
are accepted. An increased depth must immediately follow its marker. Traversal
depth reconstructs a selected branch's parent chain; sibling and descendant
paths are excluded from the returned link path. The first depth-zero record is
the declared Graphite trunk for this pinned display contract.

Any different version, unclassified line, duplicate branch, malformed record,
or inconsistent fork/depth transition is an error. The parser tests mutate each
record component and graph marker in the captured fixture to guard that
fail-closed boundary. A future Graphite CLI with supported structured
target-stack output should replace this version-pinned text parser.
