package cli_test

import (
	"strings"
	"testing"
)

// After a parent is squash-merged on GitHub and fetched, the local trunk can
// still be behind -- checked out in another worktree, nobody has pulled it.
// Restacking the child onto it keeps the parent's original commits, and the
// pull request conflicts with the squash. Nothing in the preview said the
// trunk was behind, though the fetched ref said so locally.
func TestJourneyARestackOntoATrunkBehindItsRemoteSaysSo(t *testing.T) {
	w := newWorld(t)
	w.branchOff("main", "synthetic-a", "a.txt")
	mustRun(t, "track", "--parent", "main", "--apply")
	w.commit(w.Local, "main", "local.txt", "the trunk moved here")
	w.git(w.Local, "push", "-q", "origin", "main")
	w.git(w.Other, "pull", "-q")
	w.commit(w.Other, "main", "upstream.txt", "and then upstream")
	w.git(w.Other, "push", "-q", "origin", "main")
	w.git(w.Local, "fetch", "-q", "origin")
	w.git(w.Local, "switch", "-q", "synthetic-a")

	preview := mustRun(t, "restack")
	for _, want := range []string{
		"main is 1 commit behind origin/main as last fetched",
		"g2g pull brings it up to date",
		"g2g restack --onto origin/main",
	} {
		if !strings.Contains(preview, want) {
			t.Errorf("the preview does not say %q:\n%s", want, preview)
		}
	}

	// A trunk level with its remote says nothing.
	w.git(w.Local, "switch", "-q", "main")
	w.git(w.Local, "merge", "-q", "--ff-only", "origin/main")
	w.git(w.Local, "switch", "-q", "synthetic-a")
	if level := mustRun(t, "restack"); strings.Contains(level, "behind") {
		t.Errorf("a trunk level with its remote was reported behind:\n%s", level)
	}
}

// A trunk that moved here and upstream both is not one this checkout lags:
// rewritten or diverged, the remote's copy is no evidence about it, and the
// note says nothing rather than send someone to replay onto it.
func TestJourneyARestackOntoADivergedTrunkSaysNothingOfItsRemote(t *testing.T) {
	w := newWorld(t)
	w.branchOff("main", "synthetic-a", "a.txt")
	mustRun(t, "track", "--parent", "main", "--apply")
	w.commit(w.Other, "main", "upstream.txt", "upstream")
	w.git(w.Other, "push", "-q", "origin", "main")
	w.git(w.Local, "fetch", "-q", "origin")
	w.commit(w.Local, "main", "local.txt", "only here")
	w.git(w.Local, "switch", "-q", "synthetic-a")

	if preview := mustRun(t, "restack"); strings.Contains(preview, "behind") {
		t.Errorf("a diverged trunk was reported behind its remote:\n%s", preview)
	}
}
