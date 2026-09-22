package cli_test

import (
	"fmt"
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
	return landingGitHubStack(t, remote, "synthetic-a", "synthetic-b")
}

// landingGitHubStack opens one pull request per branch, numbered from #41 up
// the stack, each based on the branch below it and the first on the trunk --
// which is where a stack's pull requests sit before any of it lands.
func landingGitHubStack(t *testing.T, remote string, branches ...string) string {
	t.Helper()
	state := t.TempDir()
	base := "main"
	for index, branch := range branches {
		number := fmt.Sprint(41 + index)
		for name, value := range map[string]string{"head": branch, "base": base} {
			if err := os.WriteFile(filepath.Join(state, "pr-"+number+"."+name), []byte(value+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		base = branch
	}
	t.Setenv("GH_STATE", state)
	t.Setenv("GH_REMOTE", remote)
	testutil.WithFakeExecutables(t, map[string]string{"gh": landingGitHubScript})
	return state
}

// Each pull request's head and base live in files, so the stack is whatever
// the test opened. Both aliased queries are answered by walking the numbers out
// of the query, or the head variables out of the arguments, in the order they
// appear, because the aliases
// are positional and land asks about different subsets as it goes.
const landingGitHubScript = `
state_dir="$GH_STATE"
remote="$GH_REMOTE"

read_state() { cat "$state_dir/$1" 2>/dev/null || printf '%s' "$2"; }
branch_for() { read_state "pr-$1.head" ""; }
number_for() {
  for file in "$state_dir"/pr-*.head; do
    if [ "$(cat "$file")" = "$1" ]; then
      number="${file##*/pr-}"
      echo "${number%.head}"
      return
    fi
  done
  echo 0
}
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
    for branch in $(for arg in "$@"; do case "$arg" in (head[0-9]*=*) printf '%s\n' "${arg#*=}" ;; esac; done); do
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

	// Every commit the stack carries, before any of it lands.
	carried := map[string][]string{}
	for _, branch := range []string{"synthetic-a", "synthetic-b"} {
		carried[branch] = strings.Fields(w.git(w.Local, "rev-list", "main.."+branch))
		if len(carried[branch]) == 0 {
			t.Fatalf("%s has no commits of its own to land", branch)
		}
	}

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

	// A squash lands the work under a commit the branch never had, and none of
	// the branch's own commits reaches the trunk. That is not incidental: it is
	// the entire reason Cherry cannot see a squash merge and Absorbed has to
	// exist, so a fake whose squash preserved the original commits would be
	// exercising a world where the hard case does not arise.
	history := w.git(w.Local, "rev-list", "origin/main")
	for branch, commits := range carried {
		for _, commit := range commits {
			if strings.Contains(history, commit) {
				t.Errorf("%s reached the trunk carrying its original commit %s · the squash did not produce a new one", branch, commit[:8])
			}
		}
	}
	// And the trunk did gain something: one new commit per branch landed.
	if got := len(strings.Fields(history)); got != 3 {
		t.Errorf("origin/main has %d commits, want the base plus one squash per branch", got)
	}

	w.assertClean(w.Local)
}

// Three branches, and the bottom one has more than one commit.
//
// Only the branch about to merge is published, so by the third cycle the top
// branch has been replayed once and its pull request still holds the version
// from before. That version carries the bottom branch's two commits, which the
// trunk now has as one squash equivalent to neither -- so counted by commit, the
// top branch had moved on both sides and the descent stopped with one branch
// merged, advising a --take published that would have undone the replay. Two
// branches never reach a third cycle, and a one-commit bottom branch squashes
// into a commit equivalent to itself, which is why nothing saw it.
func TestJourneyLandTakesDownThreeBranchesAboveASeveralCommitBranch(t *testing.T) {
	w := newWorld(t)
	w.branchOff("main", "synthetic-a", "a.txt")
	w.commit(w.Local, "synthetic-a", "a-more.txt", "more")
	w.branchOff("synthetic-a", "synthetic-b", "b.txt")
	w.branchOff("synthetic-b", "synthetic-c", "c.txt")
	for _, edge := range [][2]string{{"synthetic-a", "main"}, {"synthetic-b", "synthetic-a"}, {"synthetic-c", "synthetic-b"}} {
		mustRun(t, "track", "--branch", edge[0], "--parent", edge[1], "--apply")
	}
	w.git(w.Local, "push", "-q", "origin", "synthetic-a", "synthetic-b", "synthetic-c")
	w.git(w.Local, "switch", "-q", "synthetic-c")
	state := landingGitHubStack(t, w.Remote, "synthetic-a", "synthetic-b", "synthetic-c")

	mustRun(t, "land", "--apply")

	for _, number := range []string{"41", "42", "43"} {
		if got := readState(t, state, "pr-"+number+".state"); got != "MERGED" {
			t.Errorf("pull request #%s state = %q, want MERGED", number, got)
		}
	}
	w.git(w.Local, "fetch", "-q", "origin")
	for _, file := range []string{"a.txt", "a-more.txt", "b.txt", "c.txt"} {
		if !remoteHasFile(t, w, file) {
			t.Errorf("the trunk does not carry %s after landing", file)
		}
	}
	if got := len(strings.Fields(w.git(w.Local, "rev-list", "origin/main"))); got != 4 {
		t.Errorf("origin/main has %d commits, want the base plus one squash per branch", got)
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
