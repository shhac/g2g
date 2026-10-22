package restack

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIdleRefusalNamesOwnerWithoutTreatingGitDirAsCheckout(t *testing.T) {
	journal := &memoryJournal{present: true, record: Record{Worktree: "/synthetic/repo/.git/worktrees/owner", Branch: "synthetic/feature"}}
	err := (Service{Journal: journal}).RequireIdle(context.Background())
	for _, want := range []string{"synthetic/feature", "private Git directory: /synthetic/repo/.git/worktrees/owner", "--continue there"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("error %v missing %s", err, want)
		}
	}
	if strings.Contains(err.Error(), "cd ") {
		t.Fatalf("advice treats Git directory as checkout: %v", err)
	}
}

func TestIdleRefusalMapsLinkedWorktreeBacklink(t *testing.T) {
	privateDir := t.TempDir()
	checkout := filepath.Join(t.TempDir(), "synthetic-owner")
	if err := os.WriteFile(filepath.Join(privateDir, "gitdir"), []byte(filepath.Join(checkout, ".git")+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	journal := &memoryJournal{present: true, record: Record{Worktree: privateDir, Branch: "synthetic/feature"}}
	err := (Service{Journal: journal}).RequireIdle(context.Background())
	if err == nil || !strings.Contains(err.Error(), checkout) || strings.Contains(err.Error(), "private Git directory") {
		t.Fatalf("owner advice %v", err)
	}
}
