package restack

import (
	"context"
	"os"
	"path/filepath"
	"slices"
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
	if err := os.MkdirAll(filepath.Join(checkout, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(privateDir, "gitdir"), []byte(filepath.Join(checkout, ".git")+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	journal := &memoryJournal{present: true, record: Record{Worktree: privateDir, Branch: "synthetic/feature"}}
	err := (Service{Journal: journal}).RequireIdle(context.Background())
	if err == nil || !strings.Contains(err.Error(), checkout) || strings.Contains(err.Error(), "private Git directory") {
		t.Fatalf("owner advice %v", err)
	}
}

// locatedGit answers which private Git directory it runs in.
type locatedGit struct {
	*fakeGit
	dir string
}

func (g locatedGit) WorktreeDir(context.Context) (string, error) { return g.dir, nil }

// One restack stopped on a conflict in an agent's worktree refused every
// mutation in every worktree. Only the branches it is moving are untrue.
func TestIdleForRefusesOnlyTheBranchesARestackIsMoving(t *testing.T) {
	record := Record{
		Worktree:  "/synthetic/repo/.git/worktrees/owner",
		Branch:    "synthetic-a",
		Original:  map[string]string{"synthetic-a": "1", "synthetic-b": "2"},
		Structure: map[string]RecordedEdge{"synthetic-a": {Parent: "main"}, "synthetic-b": {Parent: "synthetic-a"}},
	}
	elsewhere := Service{Journal: &memoryJournal{present: true, record: record}, Git: locatedGit{&fakeGit{}, "/synthetic/repo/.git"}}
	for _, touching := range [][]string{{}, {"synthetic-other", "main"}} {
		if err := elsewhere.RequireIdleFor(context.Background(), touching); err != nil {
			t.Errorf("touching %v from another worktree refused: %v", touching, err)
		}
	}
	for _, touching := range [][]string{{"synthetic-b"}, {"synthetic-child", "synthetic-a"}} {
		if err := elsewhere.RequireIdleFor(context.Background(), touching); err == nil || !strings.Contains(err.Error(), "already in progress") {
			t.Errorf("touching %v while the restack moves it = %v, want the refusal", touching, err)
		}
	}

	owner := Service{Journal: &memoryJournal{present: true, record: record}, Git: locatedGit{&fakeGit{}, record.Worktree}}
	if err := owner.RequireIdleFor(context.Background(), []string{"synthetic-other"}); err == nil {
		t.Error("the worktree the restack belongs to was allowed another mutation")
	}
	unowned := record
	unowned.Worktree = ""
	anonymous := Service{Journal: &memoryJournal{present: true, record: unowned}, Git: locatedGit{&fakeGit{}, "/synthetic/repo/.git"}}
	if err := anonymous.RequireIdleFor(context.Background(), []string{"synthetic-other"}); err == nil {
		t.Error("a journal that does not say which worktree owns it was treated as somebody else's")
	}
}

// Two restacks overlap when either moves what the other reads. One paused
// part-way through replaying a child onto its parent is undone by another
// moving that parent, though neither moves what the other moves; two stacks
// on the same trunk overlap in neither direction, since a trunk never moves.
func TestRestacksOverlapWhenEitherMovesWhatTheOtherReads(t *testing.T) {
	paused := Record{
		Original:  map[string]string{"synthetic-child": "1"},
		Structure: map[string]RecordedEdge{"synthetic-child": {Parent: "synthetic-parent"}},
	}.footprint()
	for _, test := range []struct {
		name string
		mine footprint
		want []string
	}{
		{"moving the paused one's parent", footprint{moves: set([]string{"synthetic-parent"}), reads: set([]string{"synthetic-parent", "main"})}, []string{"synthetic-parent"}},
		{"building on what it moves", footprint{moves: set([]string{"synthetic-above"}), reads: set([]string{"synthetic-above", "synthetic-child"})}, []string{"synthetic-child"}},
		{"another stack on the same trunk", footprint{moves: set([]string{"synthetic-free"}), reads: set([]string{"synthetic-free", "main"})}, nil},
	} {
		if got := test.mine.meets(paused); !slices.Equal(got, test.want) {
			t.Errorf("%s: meets = %v, want %v", test.name, got, test.want)
		}
	}
}
