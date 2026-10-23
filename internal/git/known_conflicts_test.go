package git

import (
	"context"
	"os/exec"
	"testing"
)

func TestKnownTipsWithConflictsDistinguishesRewritesFromOrderedRecords(t *testing.T) {
	upstream, client := syntheticRemote(t)
	ctx := context.Background()
	run := func(args ...string) {
		t.Helper()
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	original := revision(t, "synthetic-trunk")
	fetched := advanceUpstream(t, upstream, "synthetic-trunk", "remote.txt")
	if err := client.FetchIsolated(ctx, "origin", []string{"synthetic-trunk"}); err != nil {
		t.Fatal(err)
	}
	tips, conflicts, err := client.KnownTipsWithConflicts(ctx, "origin", []string{"synthetic-trunk"})
	if err != nil || tips["synthetic-trunk"] != fetched || len(conflicts) != 0 {
		t.Fatalf("ordered records: %v, %v, %v", tips, conflicts, err)
	}
	// Rewrite the tracking record independently. Neither ref contains the
	// other now, and no timestamp or branch identity proves which came last.
	run("commit", "-q", "--allow-empty", "-m", "synthetic rewrite")
	tracking := revision(t, "synthetic-trunk")
	run("update-ref", TrackingRef("origin", "synthetic-trunk"), tracking)
	tips, conflicts, err = client.KnownTipsWithConflicts(ctx, "origin", []string{"synthetic-trunk"})
	if err != nil || tips["synthetic-trunk"] != tracking || !conflicts["synthetic-trunk"] {
		t.Fatalf("conflicting records: %v, %v, %v", tips, conflicts, err)
	}
	if revision(t, IsolatedRef("origin", "synthetic-trunk")) != fetched || revision(t, TrackingRef("origin", "synthetic-trunk")) != tracking {
		t.Fatal("reading knowledge moved a ref")
	}
	// The fetched record can also be older without any conflict.
	run("update-ref", IsolatedRef("origin", "synthetic-trunk"), original)
	tips, conflicts, err = client.KnownTipsWithConflicts(ctx, "origin", []string{"synthetic-trunk"})
	if err != nil || tips["synthetic-trunk"] != tracking || len(conflicts) != 0 {
		t.Fatalf("tracking descends from fetched: %v, %v, %v", tips, conflicts, err)
	}
}
