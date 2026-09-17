package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/testutil"
)

// Landing, against a real repository and a real remote.
//
// Every other test of land drives injected fakes, which answer whatever they
// are asked. That covers the ordering and the refusals and cannot cover the one
// question the command exists for: whether a branch replays cleanly onto a
// trunk that has swallowed its parent as a single commit. Only Git can answer
// that, and a squash merge is the case Git is worst at describing.
//
// So gh is the only thing faked here, and it is not the usual inert route
// table. A gh that exits zero without merging would leave the trunk unchanged,
// the merge wait would never settle, the replay would have nothing to replay
// onto, and the test would pass having proved nothing but argv construction.
// This one performs the squash merge itself, in the real bare remote, and
// reports the commit it actually produced.

// landingGitHub installs a gh that behaves like GitHub rather than merely
// answering like it.
//
// State lives in files under one directory: a pull request's base moves when
// the retarget asks it to, and its merged state and merge commit appear only
// once the merge has really happened. Head oids are read from the remote, so
// the wait after a push settles when the push has genuinely arrived and not
// before.
func landingGitHub(t *testing.T, remote string) string {
	t.Helper()
	state := t.TempDir()
	if err := os.WriteFile(filepath.Join(state, "pr-41.base"), []byte("main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(state, "pr-42.base"), []byte("synthetic-a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GH_STATE", state)
	t.Setenv("GH_REMOTE", remote)
	testutil.WithFakeExecutables(t, map[string]string{"gh": landingGitHubScript})
	return state
}

// The two pull requests are fixed: #41 heads synthetic-a, #42 heads
// synthetic-b. Both aliased queries are answered by walking the numbers or
// head names out of the query in the order they appear, because the aliases
// are positional and land asks about different subsets as it goes.
const landingGitHubScript = `
state_dir="$GH_STATE"
remote="$GH_REMOTE"

number_for() { case "$1" in synthetic-a) echo 41 ;; synthetic-b) echo 42 ;; *) echo 0 ;; esac; }
branch_for() { case "$1" in 41) echo synthetic-a ;; 42) echo synthetic-b ;; *) echo "" ;; esac; }
read_state() { cat "$state_dir/$1" 2>/dev/null || printf '%s' "$2"; }
head_oid() { git --git-dir="$remote" rev-parse "refs/heads/$1" 2>/dev/null || printf ''; }

query="$*"
printf '%s\n' "$*" >> "$state_dir/calls.log"

case "$1 $2" in
"api graphql")
  case "$query" in
  *"query Mergeability"*)
    printf '{"data":{"repository":{"squashMergeAllowed":true,"mergeCommitAllowed":false,"rebaseMergeAllowed":false'
    index=0
    for number in $(printf '%s' "$query" | grep -o 'pullRequest(number: [0-9][0-9]*)' | grep -o '[0-9][0-9]*'); do
      branch=$(branch_for "$number")
      merged=$(read_state "pr-$number.state" OPEN)
      base=$(read_state "pr-$number.base" main)
      commit=$(read_state "pr-$number.merge" "")
      if [ -n "$commit" ]; then commit="{\"oid\":\"$commit\"}"; else commit=null; fi
      printf ',"pr%s":{"number":%s,"headRefName":"%s","headRefOid":"%s","baseRefName":"%s","state":"%s","isDraft":false,"mergeable":"MERGEABLE","mergeStateStatus":"CLEAN","reviewDecision":null,"mergeCommit":%s}' \
        "$index" "$number" "$branch" "$(head_oid "$branch")" "$base" "$merged" "$commit"
      index=$((index + 1))
    done
    printf '}}}\n'
    ;;
  *)
    printf '{"data":{"repository":{"nameWithOwner":"example/synthetic"'
    index=0
    for branch in $(printf '%s' "$query" | grep -o 'headRefName: "[^"]*"' | sed 's/.*"\(.*\)"$/\1/'); do
      number=$(number_for "$branch")
      merged=$(read_state "pr-$number.state" OPEN)
      base=$(read_state "pr-$number.base" main)
      printf ',"pr%s":{"nodes":[{"number":%s,"url":"https://example.test/%s","headRefName":"%s","headRefOid":"%s","baseRefName":"%s","state":"%s"}]}' \
        "$index" "$number" "$number" "$branch" "$(head_oid "$branch")" "$base" "$merged"
      index=$((index + 1))
    done
    printf '}}}\n'
    ;;
  esac
  ;;
"pr edit")
  number="$3"
  shift 3
  while [ "$#" -gt 0 ]; do
    if [ "$1" = "--base" ]; then printf '%s\n' "$2" > "$state_dir/pr-$number.base"; fi
    shift
  done
  ;;
"pr merge")
  number="$3"
  branch=$(branch_for "$number")
  base=$(read_state "pr-$number.base" main)
  work="$state_dir/work-$number"
  rm -rf "$work"
  git clone -q "$remote" "$work"
  git -C "$work" config user.name synthetic
  git -C "$work" config user.email synthetic@example.test
  git -C "$work" switch -q "$base"
  # A real squash: the branch's commits become one commit that is equivalent to
  # none of them, which is exactly what makes the replay above it interesting.
  git -C "$work" merge -q --squash "origin/$branch"
  git -C "$work" commit -qm "squash $branch (#$number)"
  git -C "$work" push -q origin "$base"
  git -C "$work" rev-parse HEAD > "$state_dir/pr-$number.merge"
  printf 'MERGED\n' > "$state_dir/pr-$number.state"
  ;;
esac
`

// The whole point: a stack goes down, and what is left above each merge is
// replayed onto a trunk that has taken its parent as one squashed commit.
func TestJourneyLandTakesAStackDownOntoASquashedTrunk(t *testing.T) {
	w := newWorld(t)
	w.branchOff("main", "synthetic-a", "a.txt")
	w.branchOff("synthetic-a", "synthetic-b", "b.txt")
	mustRun(t, "track", "--branch", "synthetic-a", "--parent", "main", "--apply")
	mustRun(t, "track", "--branch", "synthetic-b", "--parent", "synthetic-a", "--apply")
	w.git(w.Local, "push", "-q", "origin", "synthetic-a", "synthetic-b")
	w.git(w.Local, "switch", "-q", "synthetic-b")
	state := landingGitHub(t, w.Remote)

	mustRun(t, "land", "--apply")

	// Both merges really happened, in the remote, as squashes.
	for _, number := range []string{"41", "42"} {
		if got := readState(t, state, "pr-"+number+".state"); got != "MERGED" {
			t.Errorf("pull request #%s state = %q, want MERGED", number, got)
		}
	}

	// The trunk carries both branches' work, by content.
	w.git(w.Local, "fetch", "-q", "origin")
	for _, file := range []string{"a.txt", "b.txt"} {
		if !remoteHasFile(t, w, file) {
			t.Errorf("the trunk does not carry %s after landing", file)
		}
	}

	// Nothing was replayed twice. A branch that carried its parent's commits
	// onto a trunk that had already squashed them is the failure this whole
	// design is arranged to avoid, and it shows up as the same change landing
	// under two commits.
	subjects := w.git(w.Local, "log", "--format=%s", "origin/main")
	if strings.Count(subjects, "squash synthetic-a") != 1 {
		t.Errorf("synthetic-a's work landed more than once:\n%s", subjects)
	}

	w.assertClean(w.Local)
}

// The recurring failure in this tool has never been a wrong answer. It is a ref
// moving and the working tree not following, which git then reports as changes
// nobody made. Landing moves refs and deletes the branch being stood on.
func TestJourneyLandLeavesTheCheckoutSomewhereThatExists(t *testing.T) {
	w := newWorld(t)
	w.branchOff("main", "synthetic-a", "a.txt")
	w.branchOff("synthetic-a", "synthetic-b", "b.txt")
	mustRun(t, "track", "--branch", "synthetic-a", "--parent", "main", "--apply")
	mustRun(t, "track", "--branch", "synthetic-b", "--parent", "synthetic-a", "--apply")
	w.git(w.Local, "push", "-q", "origin", "synthetic-a", "synthetic-b")
	w.git(w.Local, "switch", "-q", "synthetic-b")
	landingGitHub(t, w.Remote)

	mustRun(t, "land", "--apply")

	if current := w.git(w.Local, "branch", "--show-current"); current != "main" {
		t.Errorf("checkout left on %q, want the trunk after its branch was deleted", current)
	}
	w.assertClean(w.Local)
}

// Landed branches are gone: from the checkout, from the remote, and from the
// graph. A record that still names them is a record every later command
// measures against.
func TestJourneyLandRemovesWhatItLanded(t *testing.T) {
	w := newWorld(t)
	w.branchOff("main", "synthetic-a", "a.txt")
	w.branchOff("synthetic-a", "synthetic-b", "b.txt")
	mustRun(t, "track", "--branch", "synthetic-a", "--parent", "main", "--apply")
	mustRun(t, "track", "--branch", "synthetic-b", "--parent", "synthetic-a", "--apply")
	w.git(w.Local, "push", "-q", "origin", "synthetic-a", "synthetic-b")
	w.git(w.Local, "switch", "-q", "synthetic-b")
	landingGitHub(t, w.Remote)

	mustRun(t, "land", "--apply")

	local := w.git(w.Local, "branch", "--format=%(refname:short)")
	remote := w.git(w.Remote, "branch", "--format=%(refname:short)")
	for _, branch := range []string{"synthetic-a", "synthetic-b"} {
		if strings.Contains(local, branch) {
			t.Errorf("%s survives locally:\n%s", branch, local)
		}
		if strings.Contains(remote, branch) {
			t.Errorf("%s survives on the remote:\n%s", branch, remote)
		}
	}

	// And the graph knows nothing of them, with nothing orphaned behind.
	graph := mustRun(t, "graph", "--scope", "all", "--no-links")
	for _, branch := range []string{"synthetic-a", "synthetic-b"} {
		if strings.Contains(graph, branch) {
			t.Errorf("the graph still records %s:\n%s", branch, graph)
		}
	}
	if strings.Contains(graph, "parent missing") || strings.Contains(graph, "orphan") {
		t.Errorf("landing stranded something:\n%s", graph)
	}
}

// --no-delete-local keeps the branch and must still leave a usable checkout.
func TestJourneyLandCanKeepTheLocalBranches(t *testing.T) {
	w := newWorld(t)
	w.branchOff("main", "synthetic-a", "a.txt")
	mustRun(t, "track", "--branch", "synthetic-a", "--parent", "main", "--apply")
	w.git(w.Local, "push", "-q", "origin", "synthetic-a")
	w.git(w.Local, "switch", "-q", "synthetic-a")
	landingGitHub(t, w.Remote)

	mustRun(t, "land", "--apply", "--no-delete-local")

	if local := w.git(w.Local, "branch", "--format=%(refname:short)"); !strings.Contains(local, "synthetic-a") {
		t.Errorf("synthetic-a was deleted despite --no-delete-local:\n%s", local)
	}
	if remote := w.git(w.Remote, "branch", "--format=%(refname:short)"); strings.Contains(remote, "synthetic-a") {
		t.Errorf("synthetic-a survives on the remote:\n%s", remote)
	}
	w.assertClean(w.Local)
}

func readState(t *testing.T, dir, name string) string {
	t.Helper()
	contents, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(contents))
}

func remoteHasFile(t *testing.T, w *world, file string) bool {
	t.Helper()
	return strings.Contains(w.git(w.Local, "ls-tree", "-r", "--name-only", "origin/main"), file)
}
