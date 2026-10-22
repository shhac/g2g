package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/cli"
)

func queueFile(t *testing.T, dir, name, value string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(value), 0600); err != nil {
		t.Fatal(err)
	}
}

// GitHub accepts the request first and merges later, in a clone of its own.
// Each external squash genuinely advances the bare remote before g2g may tidy.
func finishQueuedMerge(t *testing.T, w *world, state, number, branch string) {
	t.Helper()
	work := filepath.Join(t.TempDir(), "merge")
	w.git(w.Local, "clone", "-q", w.Remote, work)
	w.git(work, "config", "user.name", "synthetic-queue")
	w.git(work, "config", "user.email", "queue@example.test")
	w.git(work, "switch", "-q", "main")
	w.git(work, "merge", "-q", "--squash", "origin/"+branch)
	w.git(work, "commit", "-qm", "queued squash "+branch)
	w.git(work, "push", "-q", "origin", "main")
	queueFile(t, state, "pr-"+number+".merge", w.tip(work, "HEAD"))
	queueFile(t, state, "pr-"+number+".state", "MERGED")
}

func TestJourneyQueueHandoffAndTwoSquashesAcrossReruns(t *testing.T) {
	w := newWorld(t)
	w.branchOff("main", "synthetic-a", "a.txt")
	w.commit(w.Local, "synthetic-a", "a-more.txt", "more")
	w.branchOff("synthetic-a", "synthetic-b", "b.txt")
	for _, edge := range [][2]string{{"synthetic-a", "main"}, {"synthetic-b", "synthetic-a"}} {
		mustRun(t, "track", "--branch", edge[0], "--parent", edge[1], "--apply")
	}
	w.git(w.Local, "push", "-q", "origin", "synthetic-a", "synthetic-b")
	w.git(w.Local, "switch", "-q", "synthetic-b")
	state := landingGitHub(t, w.Remote)
	queueFile(t, state, "queue", "")
	before := w.tip(w.Local, "synthetic-b")
	trunk := w.tip(w.Local, "main")
	preview := mustRun(t, "land", "--json")
	if !strings.Contains(preview, `"merge": "queue"`) || strings.Contains(preview, "g2g prune") {
		t.Fatalf("unsafe queue preview:\n%s", preview)
	}
	out, _, err := run(t, "land", "--apply")
	if !cli.StoppedPartWayForTest(err) || !strings.Contains(out, "Pending at synthetic-a") || !strings.Contains(out, "Nothing merged.") {
		t.Fatalf("out=%s err=%v", out, err)
	}
	if w.tip(w.Local, "synthetic-b") != before || w.tip(w.Local, "main") != trunk {
		t.Fatal("pending request rewrote local branches")
	}
	// Observing an existing request must also return pending without enqueueing again.
	_, _, err = run(t, "land", "--apply")
	if !cli.StoppedPartWayForTest(err) {
		t.Fatal(err)
	}
	if got := readState(t, state, "queue-attempts"); got != "41" {
		t.Fatalf("duplicate request: %q", got)
	}
	finishQueuedMerge(t, w, state, "41", "synthetic-a")
	out, _, err = run(t, "land", "--apply")
	if !cli.StoppedPartWayForTest(err) || !strings.Contains(out, "Pending at synthetic-b") {
		t.Fatalf("out=%s err=%v", out, err)
	}
	if w.tip(w.Local, "synthetic-b") == before {
		t.Fatal("survivor was not replayed after confirmed squash")
	}
	if !w.contains(w.Local, "main", "synthetic-b") {
		t.Fatal("survivor is not based on advanced trunk")
	}
	if got := readState(t, state, "queue-attempts"); got != "41\n42" {
		t.Fatalf("requests=%q", got)
	}
	finishQueuedMerge(t, w, state, "42", "synthetic-b")
	mustRun(t, "land", "--branch", "synthetic-b", "--apply")
	w.git(w.Local, "fetch", "-q", "origin")
	for _, file := range []string{"a.txt", "a-more.txt", "b.txt"} {
		if !remoteHasFile(t, w, file) {
			t.Fatalf("remote lacks %s", file)
		}
	}
	if len(strings.Fields(w.git(w.Local, "rev-list", "origin/main"))) != 3 {
		t.Fatal("queue squash history duplicated work")
	}
	if w.hasRef("refs/heads/synthetic-a") || w.hasRef("refs/heads/synthetic-b") {
		t.Fatal("confirmed queue merges were not cleaned up")
	}
	w.assertClean(w.Local)
}

func TestJourneyQueueWithoutAutoMergeUsesDirectEnrollment(t *testing.T) {
	w := newWorld(t)
	w.branchOff("main", "synthetic-a", "a.txt")
	mustRun(t, "track", "--branch", "synthetic-a", "--parent", "main", "--apply")
	w.git(w.Local, "push", "-q", "origin", "synthetic-a")
	state := landingGitHubStack(t, w.Remote, "synthetic-a")
	queueFile(t, state, "queue", "no-auto")
	before := w.tip(w.Local, "main")
	out, _, err := run(t, "land", "--apply")
	if !cli.StoppedPartWayForTest(err) || !strings.Contains(out, "Pending at synthetic-a") {
		t.Fatalf("out=%s err=%v", out, err)
	}
	calls := readState(t, state, "calls.log")
	if !strings.Contains(calls, "enqueuePullRequest") || strings.Contains(calls, "enablePullRequestAutoMerge") {
		t.Fatalf("wrong request path:\n%s", calls)
	}
	if w.tip(w.Local, "main") != before || w.tip(w.Remote, "main") != before {
		t.Fatal("enrollment was mistaken for a merge")
	}
	finishQueuedMerge(t, w, state, "41", "synthetic-a")
	mustRun(t, "land", "--branch", "synthetic-a", "--apply")
	w.assertClean(w.Local)
}
