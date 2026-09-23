package cli_test

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/cli"
)

// The descent's own properties, against a real repository and a real remote.
//
// land_journey_test.go shows that a stack comes down. These show how: that a
// rerun continues rather than repeats, that the remote sees each branch once and
// only on its own turn, that a reviewer's commit stops it rather than being
// merged or overwritten, and that the flags narrowing what it does narrow only
// that. Each of those is a claim about what reached the remote, so each is
// asserted there -- in the fake's record of what GitHub was asked, and in the
// bare remote's record of what its refs did -- rather than in what land said.

const zeroOID = "0000000000000000000000000000000000000000"

// threeBranchStack records synthetic-a, -b and -c on the trunk, publishes all
// three, and leaves the checkout on the branch named. bottom is how many
// commits synthetic-a carries.
func threeBranchStack(t *testing.T, w *world, standing string, bottom int) {
	t.Helper()
	w.branchOff("main", "synthetic-a", "a.txt")
	for extra := 1; extra < bottom; extra++ {
		w.commit(w.Local, "synthetic-a", fmt.Sprintf("a-%d.txt", extra), "more")
	}
	w.branchOff("synthetic-a", "synthetic-b", "b.txt")
	w.branchOff("synthetic-b", "synthetic-c", "c.txt")
	for _, edge := range [][2]string{{"synthetic-a", "main"}, {"synthetic-b", "synthetic-a"}, {"synthetic-c", "synthetic-b"}} {
		mustRun(t, "track", "--branch", edge[0], "--parent", edge[1], "--apply")
	}
	w.git(w.Local, "push", "-q", "origin", "synthetic-a", "synthetic-b", "synthetic-c")
	w.git(w.Local, "switch", "-q", standing)
}

// twoBranchStack is the same with synthetic-a and -b.
func twoBranchStack(t *testing.T, w *world) {
	t.Helper()
	w.branchOff("main", "synthetic-a", "a.txt")
	w.branchOff("synthetic-a", "synthetic-b", "b.txt")
	mustRun(t, "track", "--branch", "synthetic-a", "--parent", "main", "--apply")
	mustRun(t, "track", "--branch", "synthetic-b", "--parent", "synthetic-a", "--apply")
	w.git(w.Local, "push", "-q", "origin", "synthetic-a", "synthetic-b")
	w.git(w.Local, "switch", "-q", "synthetic-b")
}

// recordRemoteRefs makes the bare remote write every ref it commits into the
// same log the gh fake writes its calls to, as "ref <old> <new> <name>".
//
// One log rather than two, because the claim is about order: a branch updated
// on the remote before its own pull request's turn restarts its checks for
// nothing. And a hook rather than the reflog, because landing deletes the
// branches it lands and git deletes a ref's reflog with it.
func recordRemoteRefs(t *testing.T, w *world, state string) {
	t.Helper()
	hook := filepath.Join(w.Remote, "hooks", "reference-transaction")
	script := "#!/bin/sh\n" +
		"[ \"$1\" = committed ] || exit 0\n" +
		"while read -r old new ref; do printf 'ref %s %s %s\\n' \"$old\" \"$new\" \"$ref\" >> '" + filepath.Join(state, "calls.log") + "'; done\n"
	if err := os.MkdirAll(filepath.Dir(hook), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hook, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
}

// calls is the timeline the gh fake and the remote's hook wrote, one line per
// event. A comment body can span lines, which is why every match below is on a
// line's start rather than anywhere in it.
func calls(t *testing.T, state string) []string {
	t.Helper()
	contents, err := os.ReadFile(filepath.Join(state, "calls.log"))
	if err != nil {
		return nil
	}
	return strings.Split(strings.TrimRight(string(contents), "\n"), "\n")
}

func matching(lines []string, keep func(string) bool) []int {
	var at []int
	for index, line := range lines {
		if keep(line) {
			at = append(at, index)
		}
	}
	return at
}

func prefixed(prefix string) func(string) bool {
	return func(line string) bool { return strings.HasPrefix(line, prefix) }
}

func merging(number string) func(string) bool {
	return prefixed("pr merge " + number + " ")
}

// rewrote matches a remote ref moving from one commit to another: neither its
// creation nor its deletion, which are the initial publish and the cleanup.
func rewrote(branch string) func(string) bool {
	return func(line string) bool {
		fields := strings.Fields(line)
		return len(fields) == 4 && fields[0] == "ref" && fields[3] == "refs/heads/"+branch &&
			fields[1] != zeroOID && fields[2] != zeroOID
	}
}

func commentWrite(line string) bool {
	return strings.HasPrefix(line, "api graphql") &&
		(strings.Contains(line, "addComment(") || strings.Contains(line, "updateIssueComment("))
}

// colleagueSquashMerges does from the second clone what GitHub's merge button
// does, so the trunk moves and the pull request reads merged without this
// checkout having done either.
func colleagueSquashMerges(t *testing.T, w *world, state, number, branch string) {
	t.Helper()
	w.git(w.Other, "fetch", "-q", "origin")
	w.git(w.Other, "switch", "-q", "main")
	w.git(w.Other, "reset", "-q", "--hard", "origin/main")
	w.git(w.Other, "merge", "-q", "--squash", "origin/"+branch)
	w.git(w.Other, "commit", "-qm", "squash "+branch+" (#"+number+")")
	w.git(w.Other, "push", "-q", "origin", "main")
	for name, value := range map[string]string{"merge": w.tip(w.Other, "HEAD"), "state": "MERGED"} {
		if err := os.WriteFile(filepath.Join(state, "pr-"+number+"."+name), []byte(value+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func flag(t *testing.T, state, name, value string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(state, name), []byte(value+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// Most branches in a stack are one commit, and a squash of one commit is
// equivalent to that commit by content. That makes the published version of
// every branch still above it -- which land deliberately leaves unpublished
// until its turn -- read to pull as this branch reworded, so the replay after
// the second merge takes the stale published version and cannot measure it.
// The several-commit test beside it passes only because a squash of two
// commits is equivalent to neither.
//
// pull once took any published version whose commits all had content
// equivalents here, base and all, as this branch reworded. That is exactly
// what a single-commit squash produces, so the replay after the second merge
// took the stale version and stopped the descent part-way.
func TestJourneyLandTakesDownThreeSingleCommitBranches(t *testing.T) {
	w := newWorld(t)
	threeBranchStack(t, w, "synthetic-c", 1)
	state := landingGitHubStack(t, w.Remote, "synthetic-a", "synthetic-b", "synthetic-c")

	mustRun(t, "land", "--apply")

	for _, number := range []string{"41", "42", "43"} {
		if got := readState(t, state, "pr-"+number+".state"); got != "MERGED" {
			t.Errorf("pull request #%s state = %q, want MERGED", number, got)
		}
	}
	w.git(w.Local, "fetch", "-q", "origin")
	for _, file := range []string{"a.txt", "b.txt", "c.txt"} {
		if !remoteHasFile(t, w, file) {
			t.Errorf("the trunk does not carry %s after landing", file)
		}
	}
	w.assertClean(w.Local)
}

// A colleague's squash of a one-commit parent, then pulling twice and landing.
// The first pull replays synthetic-b onto the squash while the remote still
// holds the version built on the original, and that version's commits all have
// equivalents here -- which is what once read as synthetic-b reworded, so the
// second pull, and land's own plan, took it and refused to measure it.
func TestJourneyPullTwiceThenLandAfterAOneCommitParentIsSquashed(t *testing.T) {
	w := newWorld(t)
	twoBranchStack(t, w)
	state := landingGitHub(t, w.Remote)
	colleagueSquashMerges(t, w, state, "41", "synthetic-a")
	replaced := w.tip(w.Local, "synthetic-b")

	mustRun(t, "pull", "--apply")
	w.assertClean(w.Local)
	replayed := w.tip(w.Local, "synthetic-b")
	if replayed == replaced || !w.contains(w.Local, "main", "synthetic-b") {
		t.Fatal("the first pull did not replay synthetic-b onto the squashed trunk")
	}

	mustRun(t, "pull", "--apply")
	w.assertClean(w.Local)
	if got := w.tip(w.Local, "synthetic-b"); got != replayed {
		t.Errorf("the second pull moved synthetic-b from %s to %s · it took the version published before the squash", replayed[:8], got[:8])
	}

	mustRun(t, "land", "--apply")

	log := calls(t, state)
	if got := len(matching(log, merging("41"))); got != 0 {
		t.Errorf("#41 was merged again %d times after a colleague had merged it:\n%s", got, strings.Join(log, "\n"))
	}
	if got := readState(t, state, "pr-42.state"); got != "MERGED" {
		t.Errorf("pull request #42 state = %q, want MERGED", got)
	}
	w.git(w.Local, "fetch", "-q", "origin")
	if !remoteHasFile(t, w, "b.txt") {
		t.Error("the trunk does not carry synthetic-b's work")
	}
	if got := len(strings.Fields(w.git(w.Local, "rev-list", "origin/main"))); got != 3 {
		t.Errorf("origin/main has %d commits, want the base plus one squash per branch", got)
	}
	if local := w.git(w.Local, "branch", "--format=%(refname:short)"); strings.Contains(local, "synthetic-a") || strings.Contains(local, "synthetic-b") {
		t.Errorf("a landed branch survives locally:\n%s", local)
	}
	w.assertClean(w.Local)
}

// Re-entrancy is recomputation: a merged branch is detected by content and
// only tidied. Here the merge was not even this tool's -- a colleague pressed
// the button -- so the only evidence it happened is the trunk, and merging
// #41 again would be asking GitHub to merge a pull request that is already
// closed.
func TestJourneyLandTidiesABranchAColleagueAlreadyMerged(t *testing.T) {
	w := newWorld(t)
	twoBranchStack(t, w)
	state := landingGitHub(t, w.Remote)
	colleagueSquashMerges(t, w, state, "41", "synthetic-a")

	mustRun(t, "land", "--apply")

	log := calls(t, state)
	if got := len(matching(log, merging("41"))); got != 0 {
		t.Errorf("#41 was merged again %d times after a colleague had merged it:\n%s", got, strings.Join(log, "\n"))
	}
	if got := len(matching(log, merging("42"))); got != 1 {
		t.Errorf("#42 was merged %d times, want once:\n%s", got, strings.Join(log, "\n"))
	}
	if got := readState(t, state, "pr-42.state"); got != "MERGED" {
		t.Errorf("pull request #42 state = %q, want MERGED", got)
	}
	w.git(w.Local, "fetch", "-q", "origin")
	subjects := w.git(w.Local, "log", "--format=%s", "origin/main")
	if strings.Count(subjects, "squash synthetic-a") != 1 {
		t.Errorf("synthetic-a's work landed other than once:\n%s", subjects)
	}
	if !remoteHasFile(t, w, "b.txt") {
		t.Error("the trunk does not carry synthetic-b's work")
	}
	if local := w.git(w.Local, "branch", "--format=%(refname:short)"); strings.Contains(local, "synthetic-a") {
		t.Errorf("the branch a colleague landed was not tidied away:\n%s", local)
	}
	w.assertClean(w.Local)
}

// A descent that stops after a merge has landed that merge, and says so with
// the status that means "part of it happened". Rerunning it is how it is
// finished, and the rerun must pick up at the refused branch rather than
// starting again at the bottom.
func TestJourneyLandFinishesOnRerunAfterARefusedMerge(t *testing.T) {
	w := newWorld(t)
	twoBranchStack(t, w)
	state := landingGitHub(t, w.Remote)
	flag(t, state, "refuse-merge-42", "")

	stdout, stderr, err := run(t, "land", "--apply")
	if !cli.StoppedPartWayForTest(err) {
		t.Fatalf("land after a refused merge: err = %v, want the part-way status\n%s%s", err, stdout, stderr)
	}
	if !strings.Contains(stdout, "Merged synthetic-a, and they stay merged") {
		t.Errorf("the report does not say synthetic-a merged and stays merged:\n%s", stdout)
	}
	if got := readState(t, state, "pr-41.state"); got != "MERGED" {
		t.Errorf("pull request #41 state = %q after the stop, want MERGED", got)
	}
	if got := readState(t, state, "pr-42.state"); got == "MERGED" {
		t.Error("pull request #42 reads merged although GitHub refused it")
	}
	w.assertClean(w.Local)

	mustRun(t, "land", "--apply")

	log := calls(t, state)
	if got := len(matching(log, merging("41"))); got != 1 {
		t.Errorf("#41 was merged %d times across the two runs, want once:\n%s", got, strings.Join(log, "\n"))
	}
	if got := readState(t, state, "pr-42.state"); got != "MERGED" {
		t.Errorf("pull request #42 state = %q after the rerun, want MERGED", got)
	}
	w.git(w.Local, "fetch", "-q", "origin")
	for _, file := range []string{"a.txt", "b.txt"} {
		if !remoteHasFile(t, w, file) {
			t.Errorf("the trunk does not carry %s after the rerun", file)
		}
	}
	if got := len(strings.Fields(w.git(w.Local, "rev-list", "origin/main"))); got != 3 {
		t.Errorf("origin/main has %d commits, want the base plus one squash per branch", got)
	}
	w.assertClean(w.Local)
}

// The reason land exists rather than gt merge: only the branch about to merge
// is published, so the checks restart on each branch once, on its own turn,
// and never on the ones still waiting. Asserted from the remote's own record,
// because a push land thought better of and a push that never happened read
// the same in its output.
func TestJourneyLandTouchesTheRemoteLinearly(t *testing.T) {
	w := newWorld(t)
	threeBranchStack(t, w, "synthetic-c", 2)
	state := landingGitHubStack(t, w.Remote, "synthetic-a", "synthetic-b", "synthetic-c")
	recordRemoteRefs(t, w, state)

	mustRun(t, "land", "--apply")

	log := calls(t, state)
	dump := strings.Join(log, "\n")
	if got := len(matching(log, prefixed("pr merge "))); got != 3 {
		t.Errorf("%d merges, want one per branch:\n%s", got, dump)
	}
	// Everything above the bottom is aimed at the trunk before its turn, and
	// nothing else is moved: the bottom one is already there.
	retargets := matching(log, func(line string) bool {
		return strings.HasPrefix(line, "pr edit ") && strings.HasSuffix(line, " --base main")
	})
	if got := len(retargets); got != 2 {
		t.Errorf("%d pull requests retargeted to the trunk, want one per branch above the bottom:\n%s", got, dump)
	}
	if got := len(matching(log, prefixed("pr edit "))); got != len(retargets) {
		t.Errorf("a pull request was edited other than to aim it at the trunk:\n%s", dump)
	}

	// Each branch above the bottom is republished exactly once, after the
	// merge below it and before its own.
	turns := []struct{ branch, after, before string }{
		{"synthetic-b", "41", "42"},
		{"synthetic-c", "42", "43"},
	}
	for _, turn := range turns {
		updates := matching(log, rewrote(turn.branch))
		if len(updates) != 1 {
			t.Errorf("%s was rewritten on the remote %d times, want once on its own turn:\n%s", turn.branch, len(updates), dump)
			continue
		}
		after, before := matching(log, merging(turn.after)), matching(log, merging(turn.before))
		if len(after) != 1 || len(before) != 1 || updates[0] < after[0] || updates[0] > before[0] {
			t.Errorf("%s was republished outside its turn (between #%s and #%s):\n%s", turn.branch, turn.after, turn.before, dump)
		}
	}
	// The bottom branch was already published as it is, and nothing it
	// carried changed, so the remote never saw it move.
	if got := len(matching(log, rewrote("synthetic-a"))); got != 0 {
		t.Errorf("synthetic-a was rewritten on the remote %d times, want none:\n%s", got, dump)
	}
	// Nothing is left above a whole stack, so there is no comment to keep.
	if writes := matching(log, commentWrite); len(writes) != 0 {
		t.Errorf("stack comments were written with nothing left above the landed stack:\n%s", dump)
	}
	w.assertClean(w.Local)
}

// A reviewer's commit that arrives on a branch mid-descent is work this
// descent has not seen. From the tips alone it looks exactly like land's own
// replay having left the remote behind, so the only thing standing between it
// and being force-pushed away -- or merged unreviewed -- is land remembering
// what the remote held when it planned.
func TestJourneyLandStopsForAReviewersCommitMidDescent(t *testing.T) {
	w := newWorld(t)
	twoBranchStack(t, w)
	state := landingGitHub(t, w.Remote)
	flag(t, state, "review-on-merge-41", "synthetic-b")

	stdout, stderr, err := run(t, "land", "--apply")
	if !cli.StoppedPartWayForTest(err) {
		t.Fatalf("land with a reviewer's commit arriving: err = %v, want the part-way status\n%s%s", err, stdout, stderr)
	}
	// Stopped by what land remembered, not by a sync or push refusal that
	// happened to be in the way: that memory is the property under test.
	if !strings.Contains(stdout, "has moved on synthetic-b since this was planned") {
		t.Errorf("land did not stop for the reviewer's commit on synthetic-b:\n%s", stdout)
	}
	reviewed := readState(t, state, "review-41.commit")
	if reviewed == "" {
		t.Fatal("the reviewer's commit was never pushed; the fake did not arrange the scenario")
	}
	if got := w.tip(w.Remote, "refs/heads/synthetic-b"); got != reviewed {
		t.Errorf("the remote synthetic-b is %s, want the reviewer's commit %s still there", got[:8], reviewed[:8])
	}
	if got := readState(t, state, "pr-42.state"); got == "MERGED" {
		t.Error("#42 merged with a reviewer's commit on it that this descent never saw")
	}
	if got := len(matching(calls(t, state), merging("42"))); got != 0 {
		t.Errorf("#42 was asked to merge %d times", got)
	}
	if got := readState(t, state, "pr-41.state"); got != "MERGED" {
		t.Errorf("pull request #41 state = %q, want MERGED", got)
	}
	w.assertClean(w.Local)
}

// path is land's default scope, and from the middle of a stack it means "as
// far as here". The branch above is not landed and not published -- its pull
// request's checks are not restarted for a descent that is not its -- but it is
// replayed onto the trunk here and recorded there, because the branch it sat
// on is gone.
func TestJourneyLandFromTheMiddleStopsWhereItWasAsked(t *testing.T) {
	w := newWorld(t)
	threeBranchStack(t, w, "synthetic-b", 2)
	state := landingGitHubStack(t, w.Remote, "synthetic-a", "synthetic-b", "synthetic-c")
	recordRemoteRefs(t, w, state)
	// The comments already exist, as submit would have left them: they are
	// what records the pull requests that merge out of the stack, and without
	// them what remains is a stack of one with nothing to map.
	mustRun(t, "github", "comment", "--apply")
	seeded := len(calls(t, state))
	published := w.tip(w.Remote, "refs/heads/synthetic-c")

	mustRun(t, "land", "--apply")

	for _, number := range []string{"41", "42"} {
		if got := readState(t, state, "pr-"+number+".state"); got != "MERGED" {
			t.Errorf("pull request #%s state = %q, want MERGED", number, got)
		}
	}
	if got := readState(t, state, "pr-43.state"); got == "MERGED" {
		t.Error("#43 merged, though land was asked to go no further than synthetic-b")
	}
	log := calls(t, state)[seeded:]
	dump := strings.Join(log, "\n")
	if got := len(matching(log, merging("43"))); got != 0 {
		t.Errorf("#43 was asked to merge %d times:\n%s", got, dump)
	}
	if got := w.tip(w.Remote, "refs/heads/synthetic-c"); got != published {
		t.Errorf("the remote synthetic-c moved from %s to %s during a descent that stopped below it", published[:8], got[:8])
	}
	if updates := matching(log, prefixed("ref ")); slices.ContainsFunc(updates, func(index int) bool {
		return strings.HasSuffix(log[index], " refs/heads/synthetic-c")
	}) {
		t.Errorf("the remote synthetic-c was touched at all:\n%s", dump)
	}

	// Replayed onto the advanced trunk, carrying its own work and nothing of
	// the two originals below it.
	if !w.contains(w.Local, "main", "synthetic-c") {
		t.Error("synthetic-c was not replayed onto the advanced trunk")
	}
	w.assertHas(w.Local, "synthetic-c", "c.txt")
	if own := strings.Fields(w.git(w.Local, "rev-list", "main..synthetic-c")); len(own) != 1 {
		t.Errorf("synthetic-c carries %d commits over the trunk, want only its own", len(own))
	}
	if parent := w.readStructure()["synthetic-c"]; parent != "main" {
		t.Errorf("synthetic-c is recorded under %q, want the trunk", parent)
	}

	// The comments are kept once, on what remains, after the last merge --
	// a map drawn between merges is a map of a stack that is about to change.
	writes := matching(log, commentWrite)
	last := matching(log, merging("42"))
	if len(writes) == 0 {
		t.Errorf("no stack comment was kept on what remains:\n%s", dump)
	}
	if len(last) == 1 && len(writes) != 0 && writes[0] < last[0] {
		t.Errorf("a stack comment was written before the last merge:\n%s", dump)
	}
	if kept := readState(t, state, "comment-43"); !strings.Contains(kept, "#41") || !strings.Contains(kept, "#42") {
		t.Errorf("the comment kept on #43 does not list what landed below it:\n%s", kept)
	}
	w.assertClean(w.Local)
}

// --no-delete-remote keeps the published branch, and with it the one thing
// GitHub would otherwise do for us: retarget the pull request above when its
// base is deleted. Land never relies on that, so the next pull request is
// still aimed at the trunk -- merged into a branch that has already landed,
// it would report success and put the work nowhere.
func TestJourneyLandCanKeepTheRemoteBranches(t *testing.T) {
	w := newWorld(t)
	twoBranchStack(t, w)
	state := landingGitHub(t, w.Remote)

	mustRun(t, "land", "--apply", "--no-delete-remote")

	remote := w.git(w.Remote, "branch", "--format=%(refname:short)")
	if !strings.Contains(remote, "synthetic-a") {
		t.Errorf("synthetic-a was deleted on the remote despite --no-delete-remote:\n%s", remote)
	}
	if got := readState(t, state, "pr-42.base"); got != "main" {
		t.Errorf("#42 was merged against %q, want the trunk", got)
	}
	for _, number := range []string{"41", "42"} {
		if got := readState(t, state, "pr-"+number+".state"); got != "MERGED" {
			t.Errorf("pull request #%s state = %q, want MERGED", number, got)
		}
	}
	w.git(w.Local, "fetch", "-q", "origin")
	if !remoteHasFile(t, w, "b.txt") {
		t.Error("synthetic-b's work did not reach the trunk")
	}
	if local := w.git(w.Local, "branch", "--format=%(refname:short)"); strings.Contains(local, "synthetic-a") {
		t.Errorf("synthetic-a survives locally, which --no-delete-remote does not ask for:\n%s", local)
	}
	w.assertClean(w.Local)
}
