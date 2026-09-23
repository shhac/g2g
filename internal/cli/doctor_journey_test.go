package cli_test

import (
	"encoding/json"
	"path/filepath"
	"slices"
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

// doctorNames runs doctor and returns the one command it names for a branch,
// so a test can run exactly what a person would copy.
func doctorNames(t *testing.T, branch string) []string {
	t.Helper()
	out, _, err := run(t, "doctor", "--no-links")
	if err == nil {
		t.Fatalf("doctor found nothing to put right for %s:\n%s", branch, out)
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, branch+": ") {
			_, command, found := strings.Cut(line, " · run ")
			if found {
				return strings.Fields(strings.TrimSuffix(command, "."))[1:]
			}
		}
	}
	t.Fatalf("doctor names no command for %s:\n%s", branch, out)
	return nil
}

// A repair doctor names has to repair. Two of them named track with the parent
// already recorded, which was a no-op, so doctor named the same command again
// after it had been run.
func TestJourneyDoctorsRepairsRepair(t *testing.T) {
	for name, breakIt := range map[string]func(w *world){
		// The parent was amended and the child rebased onto it by hand, so the
		// recorded fork point is no longer in the child.
		"moved off parent": func(w *world) {
			w.git(w.Local, "switch", "-q", "synthetic-p")
			w.git(w.Local, "commit", "-q", "--amend", "-m", "synthetic p, amended")
			w.git(w.Local, "rebase", "-q", "--onto", "synthetic-p", "synthetic-p@{1}", "synthetic-c")
		},
		// The recorded fork point names a commit this repository does not have.
		"fork point gone": func(w *world) {
			w.rewriteStore(func(store string) string {
				tip := w.tip(w.Local, "synthetic-p")
				return strings.Replace(store, tip, "1234567890123456789012345678901234567890", 1)
			})
			w.git(w.Local, "update-ref", "-d", "refs/g2g/forkpoints/synthetic-c")
		},
	} {
		t.Run(name, func(t *testing.T) {
			w := newWorld(t)
			w.branchOff("main", "synthetic-p", "p.txt")
			w.branchOff("synthetic-p", "synthetic-c", "c.txt")
			mustRun(t, "adopt", "--trunk", "main", "--apply")
			breakIt(w)

			mustRun(t, append(doctorNames(t, "synthetic-c"), "--apply")...)
			if out, _, err := run(t, "doctor"); err != nil {
				t.Errorf("doctor after its own repair: %v\n%s", err, out)
			}
			w.assertClean(w.Local)
		})
	}
}

// Untracking a middle branch strands what sits on it, and doctor names track
// for the stranded branch. Naming the parent it is already recorded under has
// to make that parent a trunk: it used to find the edge written and do nothing,
// and adopt --trunk did the same, so no command led back out.
func TestJourneyAStrandedStackCanBeRootedWhereItStands(t *testing.T) {
	for name, repairIt := range map[string][]string{
		"track": {"track", "--branch", "synthetic-c", "--parent", "synthetic-p", "--apply"},
		"adopt": {"adopt", "--branch", "synthetic-c", "--trunk", "synthetic-p", "--apply"},
	} {
		t.Run(name, func(t *testing.T) {
			w := newWorld(t)
			w.branchOff("main", "synthetic-p", "p.txt")
			w.branchOff("synthetic-p", "synthetic-c", "c.txt")
			mustRun(t, "adopt", "--trunk", "main", "--apply")
			mustRun(t, "untrack", "--branch", "synthetic-p", "--apply")
			if got := doctorNames(t, "synthetic-c"); !slices.Equal(got, []string{"track", "--branch", "synthetic-c"}) {
				t.Fatalf("doctor names %v for the stranded branch", got)
			}

			out := mustRun(t, repairIt...)
			if !strings.Contains(out, "synthetic-p becomes a root of the graph") {
				t.Errorf("the repair did not say it records a trunk:\n%s", out)
			}
			if out, _, err := run(t, "doctor"); err != nil {
				t.Errorf("doctor after rooting the stranded stack: %v\n%s", err, out)
			}
			w.assertClean(w.Local)
		})
	}
}
