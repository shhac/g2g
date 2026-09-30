#!/usr/bin/env bash
# The CLI fixtures change cwd and PATH, so parallelise in separate processes.
set -eo pipefail

shards=${1:-4}
if [[ $# -gt 1 || ! $shards =~ ^([1-9]|1[0-6])$ ]]; then
  echo 'Usage: bash scripts/test-fast.sh [1-16 CLI shards, default 4]' >&2
  exit 2
fi

repo_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_dir"
# Helpers already isolate their Git calls; code under test must see the same
# configuration rather than inheriting signing, hooks or aliases from the host.
export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_SYSTEM=/dev/null
test_dir=$(mktemp -d "${TMPDIR:-/tmp}/g2g-tests.XXXXXX")
pids=()
trap 'rm -rf "$test_dir"' EXIT
trap 'for pid in "${pids[@]}"; do kill "$pid" 2>/dev/null || :; done; wait || :; exit 130' INT TERM

# Compile once, including both internal and external tests. Keep the package's
# working directory, since its golden files and repository checks are relative.
go test -c -o "$test_dir/cli.test" ./internal/cli
(cd internal/cli && "$test_dir/cli.test" -test.list '^(Test|Example|Fuzz)') > "$test_dir/tests"
go list ./... > "$test_dir/packages"
packages=()
while IFS= read -r package; do
  [[ $package == */internal/cli ]] || packages+=("$package")
done < "$test_dir/packages"

patterns=()
counts=()
for ((shard=0; shard<shards; shard++)); do
  patterns[shard]=''
  counts[shard]=0
done
total=0
while IFS= read -r name; do
  [[ $name == Test* || $name == Example* || $name == Fuzz* ]] || continue
  shard=$((total % shards))
  patterns[shard]="${patterns[shard]}${patterns[shard]:+|}$name"
  counts[shard]=$((counts[shard] + 1))
  total=$((total + 1))
done < "$test_dir/tests"
if [[ $total -eq 0 ]]; then
  echo 'No CLI tests discovered; refusing to report a successful empty run.' >&2
  exit 1
fi
printf 'Running %d CLI tests across %d processes, plus the other packages.\n' "$total" "$shards"

# Every top-level test belongs to exactly one anchored expression. Its subtests
# stay together, and each process gets its own cwd, environment and temp dirs.
for ((shard=0; shard<shards; shard++)); do
  [[ ${counts[shard]} -gt 0 ]] || continue
  (cd internal/cli && exec "$test_dir/cli.test" \
    "-test.run=^(${patterns[shard]})$" -test.v -test.timeout=10m) \
    > "$test_dir/shard-$shard.log" 2>&1 &
  pids[shard]=$!
done

# Other packages already run concurrently through go test. Retain Go's cache
# for those packages; the CLI shards deliberately run fresh each time.
go test "${packages[@]}" > "$test_dir/packages.log" 2>&1 &
packages_pid=$!
pids+=("$packages_pid")

failed=0
for ((shard=0; shard<shards; shard++)); do
  [[ ${counts[shard]} -gt 0 ]] || continue
  if wait "${pids[shard]}"; then
    printf 'ok  internal/cli shard %d/%d (%d top-level tests)\n' \
      "$((shard + 1))" "$shards" "${counts[shard]}"
  else
    cat "$test_dir/shard-$shard.log"
    failed=1
  fi
done
if ! wait "$packages_pid"; then
  failed=1
fi
cat "$test_dir/packages.log"
printf 'Ran all packages, including %d CLI tests, in %ds.\n' "$total" "$SECONDS"
exit "$failed"
