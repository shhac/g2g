package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/testutil"
)

// Moving the checkout is a question about what Git does — whether the tree
// follows, whether a local change is carried or refused — so these walk a
// real repository. The stack is made with create, which is how one is made in
// the ordinary course:
//
//	main
//	└─ synthetic-a
//	   ├─ synthetic-b
//	   │  └─ synthetic-c
//	   └─ synthetic-side
func navigationWorld(t *testing.T) *world {
	t.Helper()
	w := newWorld(t)
	for _, step := range [][]string{
		{"create", "synthetic-a", "--apply"},
		{"create", "synthetic-b", "--apply"},
		{"create", "synthetic-c", "--apply"},
		{"create", "synthetic-side", "--parent", "synthetic-a", "--apply"},
	} {
		mustRun(t, step...)
	}
	// Each branch gets a file of its own, so a switch that did not bring the
	// tree along would show as changes nobody made.
	for _, branch := range []string{"synthetic-a", "synthetic-b", "synthetic-c", "synthetic-side"} {
		w.commit(w.Local, branch, branch+".txt", branch)
	}
	return w
}

func (w *world) on() string {
	w.t.Helper()
	return w.git(w.Local, "branch", "--show-current")
}

func TestNavigationWalksARealStack(t *testing.T) {
	w := navigationWorld(t)
	w.git(w.Local, "switch", "-q", "synthetic-c")

	for _, test := range []struct {
		args []string
		to   string
	}{
		{args: []string{"down"}, to: "synthetic-b"},
		{args: []string{"down", "2"}, to: "main"},
		// Off the trunk: one stack on it, so there is one answer.
		{args: []string{"up"}, to: "synthetic-a"},
		{args: []string{"down", "--dry-run"}, to: "synthetic-a"},
		{args: []string{"bottom"}, to: "synthetic-a"},
	} {
		mustRun(t, test.args...)
		if got := w.on(); got != test.to {
			t.Fatalf("g2g %s left the checkout on %q, want %q", strings.Join(test.args, " "), got, test.to)
		}
		w.assertClean(w.Local)
	}

	w.git(w.Local, "switch", "-q", "synthetic-b")
	mustRun(t, "top")
	if got := w.on(); got != "synthetic-c" {
		t.Errorf("top from synthetic-b is %q, want synthetic-c", got)
	}
	mustRun(t, "bottom")
	if got := w.on(); got != "synthetic-a" {
		t.Errorf("bottom from synthetic-c is %q, want synthetic-a", got)
	}
	w.assertHas(w.Local, "synthetic-a", "synthetic-a.txt")
	w.assertClean(w.Local)
}

// At a fork the answer is not one branch, and choosing is a guess: the move is
// refused, the branches are named as the ways out, and the checkout stays put.
func TestNavigationRefusesAtAForkAndNamesTheBranches(t *testing.T) {
	w := navigationWorld(t)
	w.git(w.Local, "switch", "-q", "synthetic-a")

	for _, args := range [][]string{{"up"}, {"top"}} {
		stdout, _, err := run(t, args...)
		if err == nil {
			t.Fatalf("g2g %s at a fork succeeded:\n%s", args[0], stdout)
		}
		for _, want := range []string{"git switch synthetic-b", "git switch synthetic-side"} {
			if !strings.Contains(stdout, want) {
				t.Errorf("g2g %s does not offer %q:\n%s", args[0], want, stdout)
			}
		}
		if got := w.on(); got != "synthetic-a" {
			t.Errorf("a refused %s moved the checkout to %q", args[0], got)
		}
	}

	if _, _, err := run(t, "down", "5"); err == nil {
		t.Error("down past the trunk succeeded")
	}
	w.git(w.Local, "switch", "-q", "main")
	if _, _, err := run(t, "down"); err == nil {
		t.Error("down from the trunk succeeded")
	}
}

// A dry run is for a script that wants the destination and not the move, so it
// says both where and how, and moves nothing.
func TestNavigationDryRunNamesTheDestinationAndStaysPut(t *testing.T) {
	w := navigationWorld(t)
	w.git(w.Local, "switch", "-q", "synthetic-c")

	pretty := mustRun(t, "bottom", "--dry-run")
	if !strings.Contains(pretty, "git switch synthetic-a") {
		t.Errorf("dry run does not show the switch:\n%s", pretty)
	}
	var document struct {
		Operation string   `json:"operation"`
		Target    string   `json:"target"`
		Trunk     string   `json:"trunk"`
		Command   []string `json:"command"`
	}
	if err := json.Unmarshal([]byte(mustRun(t, "down", "--dry-run", "--json")), &document); err != nil {
		t.Fatal(err)
	}
	if document.Operation != "down" || document.Target != "synthetic-b" || document.Trunk != "main" || strings.Join(document.Command, " ") != "git switch synthetic-b" {
		t.Errorf("document = %+v", document)
	}
	if got := w.on(); got != "synthetic-c" {
		t.Errorf("a dry run moved the checkout to %q", got)
	}
}

// git switch refuses to overwrite a local change, and that refusal is the whole
// reason a move needs no preview. It has to reach the user intact.
func TestNavigationLeavesAConflictingChangeAlone(t *testing.T) {
	w := navigationWorld(t)
	w.git(w.Local, "switch", "-q", "synthetic-c")
	// synthetic-c.txt does not exist on synthetic-b, so carrying the edit would
	// mean deleting it.
	edited := filepath.Join(w.Local, "synthetic-c.txt")
	if err := os.WriteFile(edited, []byte("synthetic edit\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, _, err := run(t, "down")

	if err == nil {
		t.Fatal("down carried a change git would have to overwrite")
	}
	if got := w.on(); got != "synthetic-c" {
		t.Errorf("the checkout moved to %q", got)
	}
	if contents, _ := os.ReadFile(edited); string(contents) != "synthetic edit\n" {
		t.Errorf("the local change was lost: %q", contents)
	}
}

// Through the real adapters against a PATH fake, from a Graphite-described
// stack: the switch refuses to guess a branch from a remote, and nothing asks
// GitHub.
func TestDownSwitchesWithoutGuessingThroughAGraphiteStack(t *testing.T) {
	routes, _ := graphiteRoutes(t, nil)
	routes["git"] = append([]testutil.Route{{Prefix: "switch"}}, routes["git"]...)
	recorder := testutil.FakeCLIs(t, routes)

	if stdout, stderr, err := run(t, "down"); err != nil {
		t.Fatalf("down: %v\n%s%s", err, stdout, stderr)
	}

	recorder.Find("gt log")
	if recorder.Find("git switch --no-guess synthetic-lower") == "" {
		t.Errorf("no guarded switch to synthetic-lower:\n%s", strings.Join(recorder.Calls(), "\n"))
	}
	recorder.AssertNone("gh ")
}
