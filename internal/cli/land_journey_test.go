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
	pulls := make([][2]string, 0, len(branches))
	base := "main"
	for _, branch := range branches {
		pulls = append(pulls, [2]string{branch, base})
		base = branch
	}
	return landingGitHubPulls(t, remote, pulls...)
}

// landingGitHubPulls opens one pull request per head and base pair, numbered
// from #41, for shapes that are not one straight stack on main.
func landingGitHubPulls(t *testing.T, remote string, pulls ...[2]string) string {
	t.Helper()
	state := t.TempDir()
	for index, pull := range pulls {
		number := fmt.Sprint(41 + index)
		for name, value := range map[string]string{"head": pull[0], "base": pull[1]} {
			if err := os.WriteFile(filepath.Join(state, "pr-"+number+"."+name), []byte(value+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
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
json_string() {
  awk 'BEGIN { ORS = ""; printf "\"" }
    { gsub(/\\/, "\\\\"); gsub(/"/, "\\\""); gsub(/\t/, "\\t"); gsub(/\r/, "\\r"); if (NR > 1) printf "\\n"; print }
    END { printf "\"" }' "$1"
}

query="$*"
printf '%s\n' "$*" >> "$state_dir/calls.log"

case "$1 $2" in
"api graphql")
  case "$query" in
  *"query Mergeability"*)
    printf '{"data":{"repository":{"squashMergeAllowed":true,"mergeCommitAllowed":true,"rebaseMergeAllowed":true'
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
  *"query StackComments"*)
    # The comments are kept, not merely acknowledged: a comment names the pull
    # requests that merged out of the stack, and only a comment read back can
    # tell a later run that history exists.
    printf '{"data":{"repository":{'
    index=0
    for number in $(printf '%s' "$query" | grep -o 'issueOrPullRequest(number: [0-9][0-9]*)' | grep -o '[0-9][0-9]*'); do
      if [ "$index" -gt 0 ]; then printf ','; fi
      nodes=""
      if [ -f "$state_dir/comment-$number" ]; then
        nodes=$(printf '{"id":"IC_%s","body":%s,"viewerCanUpdate":true,"author":{"login":"synthetic"}}' "$number" "$(json_string "$state_dir/comment-$number")")
      fi
      printf '"c%s":{"__typename":"PullRequest","id":"PR_%s","number":%s,"headRefName":"%s","baseRefName":"%s","state":"%s","viewerCanComment":true,"comments":{"pageInfo":{"hasNextPage":false,"endCursor":null},"nodes":[%s]}}' \
        "$index" "$number" "$number" "$(branch_for "$number")" "$(read_state "pr-$number.base" main)" "$(read_state "pr-$number.state" OPEN)" "$nodes"
      index=$((index + 1))
    done
    printf '}}}\n'
    ;;
  *"addComment("*|*"updateIssueComment("*)
    # One comment per pull request, which is all this tool keeps: the subject
    # is PR_<number> and the comment is IC_<number>, so either names the file.
    target=""
    for arg in "$@"; do
      case "$arg" in
      subject=PR_*) target="${arg#subject=PR_}" ;;
      id=IC_*) target="${arg#id=IC_}" ;;
      esac
    done
    for arg in "$@"; do
      case "$arg" in body=*) printf '%s' "${arg#body=}" > "$state_dir/comment-$target" ;; esac
    done
    printf '{"data":{}}\n'
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
  # GitHub declining a merge -- a check that failed at the last moment -- is
  # arranged by a flag file, and happens once: the next attempt is accepted.
  if [ -f "$state_dir/refuse-merge-$number" ]; then
    rm -f "$state_dir/refuse-merge-$number"
    printf 'synthetic refusal: #%s is not mergeable right now\n' "$number" >&2
    exit 1
  fi
  branch=$(branch_for "$number")
  base=$(read_state "pr-$number.base" main)
  work="$state_dir/work-$number"
  rm -rf "$work"
  git clone -q "$remote" "$work"
  git -C "$work" config user.name synthetic
  git -C "$work" config user.email synthetic@example.test
  git -C "$work" switch -q "$base"
  method=squash
  for arg in "$@"; do
    case "$arg" in --rebase) method=rebase ;; --merge) method=merge ;; esac
  done
  case "$method" in
  squash)
    # A real squash: the branch's commits become one commit that is
    # equivalent to none of them, which is exactly what makes the replay
    # above it interesting.
    git -C "$work" merge -q --squash "origin/$branch"
    git -C "$work" commit -qm "squash $branch (#$number)"
    ;;
  rebase)
    # GitHub's rebase merge: each commit replayed onto the base under a new
    # id, and no merge commit.
    for commit in $(git -C "$work" rev-list --reverse "$base..origin/$branch"); do
      git -C "$work" cherry-pick "$commit" >/dev/null
    done
    ;;
  merge)
    git -C "$work" merge -q --no-ff -m "merge $branch (#$number)" "origin/$branch"
    ;;
  esac
  git -C "$work" push -q origin "$base"
  git -C "$work" rev-parse HEAD > "$state_dir/pr-$number.merge"
  printf 'MERGED\n' > "$state_dir/pr-$number.state"
  # A reviewer pushing a fix onto another branch of the stack while this merge
  # happens, from a clone of their own, arranged by a flag file naming it.
  if [ -f "$state_dir/review-on-merge-$number" ]; then
    reviewed=$(cat "$state_dir/review-on-merge-$number")
    review="$state_dir/review-$number"
    rm -rf "$review"
    git clone -q "$remote" "$review"
    git -C "$review" config user.name synthetic-reviewer
    git -C "$review" config user.email reviewer@example.test
    git -C "$review" switch -q "$reviewed"
    printf 'reviewed\n' > "$review/review.txt"
    git -C "$review" add review.txt
    git -C "$review" commit -qm "synthetic review fix on $reviewed"
    git -C "$review" push -q origin "$reviewed"
    git -C "$review" rev-parse HEAD > "$state_dir/review-$number.commit"
  fi
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
	graph := mustRun(t, "status", "--scope", "all", "--no-links")
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

// landing-branch, all the way down: the small branches squash into the
// declared trunk one at a time, and the trunk then reaches main by the method
// it was declared with, keeping those squashes as separate commits.
func TestJourneyLandingBranchReachesMainByItsDeclaredMethod(t *testing.T) {
	w := newWorld(t)
	w.branchOff("main", "synthetic-feature", "feature.txt")
	w.branchOff("synthetic-feature", "synthetic-a1", "a1.txt")
	w.branchOff("synthetic-a1", "synthetic-a2", "a2.txt")
	mustRun(t, "adopt", "--trunk", "main", "--apply")
	mustRun(t, "track", "--branch", "synthetic-feature", "--as-trunk", "--into", "main", "--by", "rebase", "--apply")
	// Somebody else's stack on main, which landing the trunk must not replay.
	w.branchOff("main", "synthetic-other", "other.txt")
	mustRun(t, "track", "--branch", "synthetic-other", "--parent", "main", "--apply")
	other := w.tip(w.Local, "synthetic-other")
	w.git(w.Local, "push", "-q", "origin", "synthetic-feature", "synthetic-a1", "synthetic-a2")
	w.git(w.Local, "switch", "-q", "synthetic-a2")
	state := landingGitHubPulls(t, w.Remote,
		[2]string{"synthetic-a1", "synthetic-feature"},
		[2]string{"synthetic-a2", "synthetic-a1"},
		[2]string{"synthetic-feature", "main"},
	)

	// The stack above the trunk lands into it, and main is not touched.
	mainBefore := w.tip(w.Remote, "main")
	mustRun(t, "land", "--apply")
	for _, number := range []string{"41", "42"} {
		if got := readState(t, state, "pr-"+number+".state"); got != "MERGED" {
			t.Errorf("pull request #%s state = %q, want MERGED", number, got)
		}
	}
	if w.tip(w.Remote, "main") != mainBefore {
		t.Fatal("landing the stack above the declared trunk moved main")
	}
	if got := readState(t, state, "pr-43.state"); got == "MERGED" {
		t.Fatal("landing the stack above the declared trunk merged the trunk itself")
	}

	preview := mustRun(t, "land", "--branch", "synthetic-feature")
	for _, want := range []string{"lands into main by rebase, as declared", "gh pr merge 43 --rebase", "git fetch origin main:main", "g2g untrack --branch synthetic-feature --apply"} {
		if !strings.Contains(preview, want) {
			t.Errorf("the preview does not say %q:\n%s", want, preview)
		}
	}
	mustRun(t, "land", "--branch", "synthetic-feature", "--apply")

	if got := readState(t, state, "pr-43.state"); got != "MERGED" {
		t.Fatalf("pull request #43 state = %q, want MERGED", got)
	}
	w.git(w.Local, "fetch", "-q", "origin")
	for _, file := range []string{"feature.txt", "a1.txt", "a2.txt"} {
		if !remoteHasFile(t, w, file) {
			t.Errorf("main does not carry %s after the trunk landed", file)
		}
	}
	// Rebased, not squashed: each small merge is still its own commit on main.
	subjects := w.git(w.Local, "log", "--format=%s", "origin/main")
	for _, want := range []string{"squash synthetic-a1 (#41)", "squash synthetic-a2 (#42)", "synthetic feature.txt"} {
		if !strings.Contains(subjects, want) {
			t.Errorf("main does not keep %q as its own commit:\n%s", want, subjects)
		}
	}
	if w.tip(w.Local, "main") != w.tip(w.Local, "origin/main") {
		t.Error("main here was not advanced to the merge")
	}
	if strings.Contains(w.readStore(), "synthetic-feature") {
		t.Errorf("the landed trunk is still recorded:\n%s", w.readStore())
	}
	if w.hasRef("refs/heads/synthetic-feature") {
		t.Error("the landed trunk was not deleted here")
	}
	if w.tip(w.Local, "synthetic-other") != other {
		t.Error("landing the trunk replayed another stack on main")
	}
	w.assertClean(w.Local)
}
