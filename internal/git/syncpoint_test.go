package git

import (
	"context"
	"slices"
	"testing"
)

// A sync point is written with a reflog, read back with what its message
// carried, and every earlier one stays in the history.
func TestASyncPointIsRecordedReadAndKeptInHistory(t *testing.T) {
	repo, client := publishedRepo(t)
	repo.Run("remote", "add", "origin", repo.Dir)
	ctx := context.Background()
	first, second := repo.Revision("synthetic-work~1"), repo.Revision("synthetic-work")

	if _, ok, err := client.ReadSync(ctx, "origin", "synthetic-work"); err != nil || ok {
		t.Fatalf("ReadSync() before any = %v, %v, want none", ok, err)
	}
	if err := client.RecordSync(ctx, "origin", "synthetic-work", SyncPoint{Tip: first, Local: first, Command: "push"}); err != nil {
		t.Fatalf("RecordSync() error = %v", err)
	}
	if err := client.RecordSync(ctx, "origin", "synthetic-work", SyncPoint{Tip: second, Local: second, Command: "pull", Dropped: []string{first}}); err != nil {
		t.Fatalf("RecordSync() error = %v", err)
	}
	point, ok, err := client.ReadSync(ctx, "origin", "synthetic-work")
	if err != nil || !ok {
		t.Fatalf("ReadSync() = %v, %v", ok, err)
	}
	if point.Tip != second || point.Local != second || point.Command != "pull" || !slices.Equal(point.Dropped, []string{first}) || point.At.IsZero() {
		t.Errorf("ReadSync() = %+v", point)
	}
	history, err := client.SyncHistory(ctx, "origin", "synthetic-work", 10)
	if err != nil || len(history) != 2 || history[1].Tip != first {
		t.Errorf("SyncHistory() = %+v, %v, want both points, newest first", history, err)
	}
}

// A sync point describes a branch whose history, or reflog, holds the local
// tip it recorded. A branch deleted and made again under the name holds
// neither.
func TestASyncPointDescribesOnlyTheBranchItWasRecordedFor(t *testing.T) {
	repo, client := publishedRepo(t)
	ctx := context.Background()
	recorded := SyncPoint{Tip: repo.Revision("synthetic-work"), Local: repo.Revision("synthetic-work")}

	if ok, err := client.Describes(ctx, "synthetic-work", recorded); err != nil || !ok {
		t.Errorf("Describes() of the branch it was recorded on = %v, %v", ok, err)
	}
	repo.Run("reset", "-q", "--hard", "synthetic-work~1")
	if ok, err := client.Describes(ctx, "synthetic-work", recorded); err != nil || !ok {
		t.Errorf("Describes() after a reset the reflog remembers = %v, %v", ok, err)
	}
	repo.Run("switch", "-q", "synthetic-main")
	repo.Run("branch", "-q", "-D", "synthetic-work")
	repo.Run("switch", "-qc", "synthetic-work")
	repo.Commit("synthetic other work", "other.txt", "other")
	if ok, err := client.Describes(ctx, "synthetic-work", recorded); err != nil || ok {
		t.Errorf("Describes() of a branch made again under the name = %v, %v, want false", ok, err)
	}
}

func TestASyncPointMovesWithARenameAndGoesWithTheBranch(t *testing.T) {
	repo, client := publishedRepo(t)
	repo.Run("remote", "add", "origin", repo.Dir)
	ctx := context.Background()
	tip, dropped := repo.Revision("synthetic-work"), repo.Revision("synthetic-work~1")
	if err := client.RecordSync(ctx, "origin", "synthetic-work", SyncPoint{Tip: dropped, Local: dropped, Command: "push"}); err != nil {
		t.Fatal(err)
	}
	if err := client.RecordSync(ctx, "origin", "synthetic-work", SyncPoint{Tip: tip, Local: tip, Command: "pull", Dropped: []string{dropped}}); err != nil {
		t.Fatal(err)
	}

	if err := client.MoveSync(ctx, "synthetic-work", "synthetic-renamed"); err != nil {
		t.Fatalf("MoveSync() error = %v", err)
	}
	if _, ok, _ := client.ReadSync(ctx, "origin", "synthetic-work"); ok {
		t.Error("the old name kept its sync point")
	}
	if point, ok, err := client.ReadSync(ctx, "origin", "synthetic-renamed"); err != nil || !ok || point.Tip != tip || point.Local != tip {
		t.Errorf("ReadSync(new name) = %+v, %v, %v", point, ok, err)
	}
	// The whole history moves: it is what keeps a dropped commit reachable
	// and lets status name it.
	history, err := client.SyncHistory(ctx, "origin", "synthetic-renamed", 10)
	if err != nil || len(history) != 2 || history[0].Command != "pull" || !slices.Equal(history[0].Dropped, []string{dropped}) || history[1].Tip != dropped {
		t.Errorf("SyncHistory(new name) = %+v, %v, want both points carried, newest first", history, err)
	}

	if err := client.ForgetSync(ctx, "synthetic-renamed"); err != nil {
		t.Fatalf("ForgetSync() error = %v", err)
	}
	if _, ok, _ := client.ReadSync(ctx, "origin", "synthetic-renamed"); ok {
		t.Error("ForgetSync() left the sync point")
	}
	if err := client.ForgetSync(ctx, "synthetic-never"); err != nil {
		t.Errorf("ForgetSync() of a branch with none = %v", err)
	}
}
