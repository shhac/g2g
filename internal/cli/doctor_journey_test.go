package cli_test

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// A healthy repository says so and exits zero, so a script can ask.
func TestJourneyDoctorFindsNothingInAHealthyStack(t *testing.T) {
	w := newWorld(t)
	w.branchOff("main", "synthetic-a", "a.txt")
	w.branchOff("synthetic-a", "synthetic-b", "b.txt")
	mustRun(t, "adopt", "--trunk", "main", "--apply")
	mustRun(t, "push", "--apply")

	out := mustRun(t, "doctor")
	if !strings.Contains(out, "Nothing needs putting right across 2 recorded branches.") {
		t.Errorf("a healthy stack is not reported as one:\n%s", out)
	}
	w.assertClean(w.Local)
}

// What doctor exists for is what broke outside this tool: a parent committed
// to by hand, a branch deleted with plain git, work that landed. It names each
// with the command that puts it right, draws nothing that is fine, and exits
// with the status that says it found something.
func TestJourneyDoctorNamesWhatBrokeOutsideTheTool(t *testing.T) {
	w := newWorld(t)
	w.branchOff("main", "synthetic-a", "a.txt")
	w.branchOff("synthetic-a", "synthetic-b", "b.txt")
	w.branchOff("synthetic-b", "synthetic-c", "c.txt")
	w.branchOff("main", "synthetic-gone", "gone.txt")
	w.branchOff("main", "synthetic-fine", "fine.txt")
	for _, branch := range []string{"synthetic-a", "synthetic-gone", "synthetic-fine"} {
		mustRun(t, "track", "--branch", branch, "--parent", "main", "--apply")
	}
	mustRun(t, "track", "--branch", "synthetic-b", "--parent", "synthetic-a", "--apply")
	mustRun(t, "track", "--branch", "synthetic-c", "--parent", "synthetic-b", "--apply")

	w.commit(w.Local, "synthetic-a", "more.txt", "more")
	w.git(w.Local, "switch", "-q", "main")
	w.git(w.Local, "branch", "-D", "synthetic-gone")
	w.git(w.Local, "merge", "-q", "--squash", "synthetic-fine")
	w.git(w.Local, "commit", "-qm", "synthetic squash of fine")

	out, _, err := run(t, "doctor", "--no-links")
	// Landing moves the trunk, so what sits on it needs a restack too: four.
	if err == nil || !strings.Contains(err.Error(), "found 4 problems") {
		t.Fatalf("doctor error = %v, want it to report four problems\n%s", err, out)
	}
	for _, want := range []string{
		"synthetic-a: its parent moved underneath it · run g2g restack --branch synthetic-a.",
		"synthetic-b: its parent moved underneath it · run g2g restack --branch synthetic-b.",
		"synthetic-gone: recorded, and no longer a local branch · run g2g untrack --branch synthetic-gone.",
		"synthetic-fine: already landed in main · run g2g prune --branch synthetic-fine.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("doctor does not say %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "synthetic-c") {
		t.Errorf("doctor draws a branch that is fine:\n%s", out)
	}
	w.assertClean(w.Local)
}

// A restack that stopped part-way is the one finding about the repository
// rather than a branch, and the most urgent: everything else refuses until it
// is finished.
func TestJourneyDoctorFindsAnUnfinishedRestack(t *testing.T) {
	w := newWorld(t)
	w.branchOff("main", "synthetic-a", "a.txt")
	mustRun(t, "track", "--branch", "synthetic-a", "--parent", "main", "--apply")
	plantRestackJournal(t, filepath.Join(w.Local, ".git"))

	out, _, err := run(t, "doctor", "--json")
	if err == nil {
		t.Fatalf("doctor error = nil with a restack part-way\n%s", out)
	}
	var document struct {
		Operation string `json:"operation"`
		Notes     []struct {
			Text string `json:"text"`
		} `json:"notes"`
	}
	if err := json.Unmarshal([]byte(out), &document); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	said := ""
	for _, note := range document.Notes {
		said += note.Text + "\n"
	}
	if document.Operation != "doctor" || !strings.Contains(said, "g2g restack --continue") {
		t.Errorf("document = %s, want the unfinished restack and its way out", out)
	}
}

// A detached HEAD is where someone mid-rebase stands, which is exactly when
// doctor is wanted, and there is no current branch to anchor a read on.
func TestJourneyDoctorWorksFromADetachedHead(t *testing.T) {
	w := newWorld(t)
	w.branchOff("main", "synthetic-a", "a.txt")
	mustRun(t, "track", "--branch", "synthetic-a", "--parent", "main", "--apply")
	w.git(w.Local, "switch", "-q", "--detach", "main")

	if out := mustRun(t, "doctor"); !strings.Contains(out, "Nothing needs putting right") {
		t.Errorf("doctor from a detached HEAD:\n%s", out)
	}
}
