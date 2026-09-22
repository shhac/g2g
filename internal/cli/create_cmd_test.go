package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/cli"
	"github.com/shhac/g2g/internal/testutil"
)

// create acts on the checkout the user is standing in, so the questions worth
// asking are what Git does: whether the tree follows the switch, what the
// commit holds, and what a failure leaves behind. These run in a real
// repository cloned from a real bare remote, which is also what gives the
// repository a default branch.

func TestCreateStartsARecordedBranchAndTheCheckoutFollows(t *testing.T) {
	w := newWorld(t)

	mustRun(t, "create", "synthetic-a", "--apply")
	w.assertClean(w.Local)
	if err := os.WriteFile(filepath.Join(w.Local, "b.txt"), []byte("b\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	w.git(w.Local, "add", "b.txt")
	mustRun(t, "create", "synthetic-b", "-m", "synthetic b", "--apply")

	w.assertClean(w.Local)
	if current := w.git(w.Local, "branch", "--show-current"); current != "synthetic-b" {
		t.Errorf("checked out %q, want synthetic-b", current)
	}
	structure := w.readStructure()
	for branch, parent := range map[string]string{"synthetic-a": "main", "synthetic-b": "synthetic-a", "trunks": "main"} {
		if structure[branch] != parent {
			t.Errorf("the graph records %s as %q, want %q (%v)", branch, structure[branch], parent, structure)
		}
	}
	if subject := w.git(w.Local, "log", "-1", "--format=%s", "synthetic-b"); subject != "synthetic b" {
		t.Errorf("synthetic-b's commit is %q", subject)
	}
	w.assertHas(w.Local, "synthetic-b", "b.txt")
	// The commit went on the new branch and nowhere else.
	if w.tip(w.Local, "synthetic-a") != w.tip(w.Local, "main") {
		t.Error("synthetic-a moved: the commit landed on the parent")
	}

	graph := mustRun(t, "graph")
	for _, branch := range []string{"main", "synthetic-a", "synthetic-b"} {
		if !strings.Contains(graph, branch) {
			t.Errorf("graph does not show %s:\n%s", branch, graph)
		}
	}
}

func TestCreatePreviewChangesNothing(t *testing.T) {
	w := newWorld(t)
	before := w.git(w.Local, "branch", "--format=%(refname:short)")

	stdout := mustRun(t, "create", "synthetic-a", "--parent", "main")

	for _, want := range []string{"git switch -c synthetic-a main", "Records synthetic-a under main", "Rerun with --apply"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("preview omits %q:\n%s", want, stdout)
		}
	}
	if after := w.git(w.Local, "branch", "--format=%(refname:short)"); after != before {
		t.Errorf("a preview changed the branches: %q to %q", before, after)
	}
	if _, err := os.Stat(filepath.Join(w.Local, ".git", "g2g", "graph.json")); !os.IsNotExist(err) {
		t.Errorf("a preview wrote the graph store: %v", err)
	}
}

// Recording a child under a branch the graph does not know would make that
// branch a trunk, and nothing here can tell a trunk from a feature branch on
// the repository's word alone except the default branch.
func TestCreateRefusesAParentTheGraphDoesNotKnow(t *testing.T) {
	w := newWorld(t)
	w.branchOff("main", "synthetic-stray", "stray.txt")

	preview := mustRun(t, "create", "synthetic-a")
	if !strings.Contains(preview, "would make synthetic-stray a trunk") || !strings.Contains(preview, "g2g track --stack --branch synthetic-stray") {
		t.Errorf("preview does not explain the refusal and its way out:\n%s", preview)
	}

	_, _, err := run(t, "create", "synthetic-a", "--apply")
	if err == nil {
		t.Fatal("create --apply under an unrecorded branch succeeded")
	}
	if branches := w.git(w.Local, "branch", "--format=%(refname:short)"); strings.Contains(branches, "synthetic-a") {
		t.Errorf("a refused create left synthetic-a behind:\n%s", branches)
	}
	if current := w.git(w.Local, "branch", "--show-current"); current != "synthetic-stray" {
		t.Errorf("a refused create moved the checkout to %q", current)
	}
}

// A recording that fails is undone completely: the branch has no commits of
// its own yet, so going back and deleting it loses nothing, and the staged
// change the commit would have taken is still staged where it started.
func TestAFailedRecordingLeavesNoBranchBehind(t *testing.T) {
	w := newWorld(t)
	mustRun(t, "create", "synthetic-a", "--apply")
	store := filepath.Join(w.Local, ".git", "g2g")
	if err := os.Chmod(store, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(store, 0o700) })
	if err := os.WriteFile(filepath.Join(w.Local, "b.txt"), []byte("b\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	w.git(w.Local, "add", "b.txt")

	stdout, _, err := run(t, "create", "synthetic-b", "-m", "synthetic b", "--apply")

	if err == nil {
		t.Fatalf("create succeeded with an unwritable store:\n%s", stdout)
	}
	if !strings.Contains(err.Error(), "removed") {
		t.Errorf("error does not say the branch was removed: %v", err)
	}
	if current := w.git(w.Local, "branch", "--show-current"); current != "synthetic-a" {
		t.Errorf("the checkout is on %q, want it back on synthetic-a", current)
	}
	if branches := w.git(w.Local, "branch", "--format=%(refname:short)"); strings.Contains(branches, "synthetic-b") {
		t.Errorf("synthetic-b survived a failed recording:\n%s", branches)
	}
	if staged := w.git(w.Local, "diff", "--cached", "--name-only"); staged != "b.txt" {
		t.Errorf("staged after the rollback = %q, want b.txt still staged", staged)
	}
}

// A commit that fails after the branch is recorded is a part-done apply: the
// branch and its record stay, the change stays staged, and the status says it
// stopped rather than failed.
func TestAFailedCommitStopsPartWayAndKeepsTheBranch(t *testing.T) {
	w := newWorld(t)
	mustRun(t, "create", "synthetic-a", "--apply")
	hook := filepath.Join(w.Local, ".git", "hooks", "pre-commit")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\necho synthetic hook refused >&2\nexit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(w.Local, "b.txt"), []byte("b\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	w.git(w.Local, "add", "b.txt")

	stdout, _, err := run(t, "create", "synthetic-b", "-m", "synthetic b", "--apply")

	if !cli.StoppedPartWayForTest(err) {
		t.Fatalf("error = %v, want the part-way status\n%s", err, stdout)
	}
	if !strings.Contains(stdout, "Stopped part-way") || !strings.Contains(stdout, "still staged") {
		t.Errorf("the report does not say what happened:\n%s", stdout)
	}
	if current := w.git(w.Local, "branch", "--show-current"); current != "synthetic-b" {
		t.Errorf("checked out %q, want synthetic-b kept", current)
	}
	if w.readStructure()["synthetic-b"] != "synthetic-a" {
		t.Errorf("synthetic-b is not recorded under synthetic-a: %v", w.readStructure())
	}
	if staged := w.git(w.Local, "diff", "--cached", "--name-only"); staged != "b.txt" {
		t.Errorf("staged = %q, want b.txt still staged", staged)
	}
}

// Through the real adapters against a PATH fake, which is what proves the
// argv: a name validated before anything moves, the branch started at its
// parent, the message passed so it cannot be read as an option, and neither
// Graphite nor GitHub reached.
func TestCreateRunsItsStepsInOrderThroughTheRealAdapters(t *testing.T) {
	routes, common := graphiteRoutes(t, nil)
	dir := filepath.Join(common, "g2g")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	adopted := `{"storeSchemaVersion":1,"trunks":["synthetic-main"],"branches":{
		"synthetic-lower":{"parent":"synthetic-main","origin":"user"},
		"synthetic-top":{"parent":"synthetic-lower","origin":"user"}}}`
	if err := os.WriteFile(filepath.Join(dir, "graph.json"), []byte(adopted), 0o600); err != nil {
		t.Fatal(err)
	}
	routes["git"] = append([]testutil.Route{
		{Prefix: "check-ref-format --branch synthetic-new", Output: "synthetic-new"},
		{Prefix: "symbolic-ref", Exit: 1},
		{Prefix: "merge-base --is-ancestor"},
		{Prefix: "for-each-ref", Lines: []string{"synthetic-top"}},
		{Prefix: "rev-list --left-right --count", Lines: []string{"0\t1"}},
		{Prefix: "diff --cached --name-only", Lines: []string{"synthetic.txt"}},
		{Prefix: "switch -c"},
		{Prefix: "update-ref"},
		{Prefix: "commit"},
	}, routes["git"]...)
	recorder := testutil.FakeCLIs(t, routes)

	if stdout, stderr, err := run(t, "create", "synthetic-new", "-m", "-synthetic first", "--apply"); err != nil {
		t.Fatalf("create --apply: %v\n%s%s", err, stdout, stderr)
	}

	recorder.AssertOrder(
		"git check-ref-format --branch synthetic-new",
		"git switch -c synthetic-new synthetic-top",
		"git update-ref",
		"git commit --quiet --message=-synthetic first",
	)
	recorder.AssertNone("gt ", "gh ")
	stored, err := os.ReadFile(filepath.Join(dir, "graph.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(stored), `"synthetic-new"`) {
		t.Errorf("the store does not record synthetic-new:\n%s", stored)
	}
}
