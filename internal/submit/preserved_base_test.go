package submit

import (
	"context"
	"errors"
	"strings"
	"testing"

	localgit "github.com/shhac/g2g/internal/git"
	"github.com/shhac/g2g/internal/githubstack"
	"github.com/shhac/g2g/internal/push"
	"github.com/shhac/g2g/internal/repair"
	"github.com/shhac/g2g/internal/stack"
	"github.com/shhac/g2g/internal/subprocess"
	"github.com/shhac/g2g/internal/testutil"
)

type preservedGit struct {
	fakeGit
	tips     map[string]string
	contains bool
	err      error
	asked    [][2]string
	resolved bool
}

func (f *preservedGit) ResolveAll(context.Context, []string) (map[string]string, error) {
	f.resolved = true
	return f.tips, nil
}
func (f *preservedGit) IsAncestor(_ context.Context, head, base string) (bool, error) {
	f.asked = append(f.asked, [2]string{head, base})
	return f.contains, f.err
}

func TestPreservedBaseRepair(t *testing.T) {
	for _, test := range []struct {
		name, state, base                        string
		contains, missing, landed, fail, blocked bool
	}{
		{name: "reordered selected base", state: "OPEN", base: "synthetic-base", contains: true, blocked: true},
		{name: "local base outside selection", state: "OPEN", base: "synthetic-outside", contains: true, blocked: true},
		{name: "intentional safe base", state: "OPEN", base: "synthetic-safe"},
		{name: "correct base contains head", state: "OPEN", base: "synthetic-main", contains: true},
		{name: "nonlocal base", state: "OPEN", base: "synthetic-outside", missing: true, contains: true},
		{name: "closed history", state: "CLOSED", base: "synthetic-base", contains: true},
		{name: "merged history", state: "MERGED", base: "synthetic-base", contains: true},
		{name: "head not published", state: "OPEN", base: "synthetic-base", landed: true, contains: true},
		{name: "ancestry failure", state: "OPEN", base: "synthetic-base", fail: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			git := &preservedGit{tips: map[string]string{"refs/heads/synthetic-head": "synthetic-head-tip"}, contains: test.contains}
			if !test.missing {
				git.tips["refs/heads/"+test.base] = "synthetic-base-tip"
			}
			if test.fail {
				git.err = errors.New("synthetic ancestry failure")
			}
			snapshot := stack.Snapshot{Target: "synthetic-head", Base: "synthetic-main", Scope: stack.ScopePath, Branches: []string{"synthetic-head"}}
			publication := push.Publication{}
			if test.landed {
				publication.Standing = push.Landed
			}
			plan := Plan{Snapshot: snapshot, Existing: []githubstack.PullRequest{{Number: 41, Head: "synthetic-head", Base: test.base, State: test.state}}, Push: push.Plan{Snapshot: snapshot, Publishing: map[string]push.Publication{"synthetic-head": publication}}}
			note, err := (Service{Git: git}).preservedBaseRepair(context.Background(), plan)
			if (err != nil) != test.fail {
				t.Fatalf("error = %v", err)
			}
			if (note.Sentence() != "") != test.blocked {
				t.Fatalf("repair = %#v", note)
			}
			if test.base == snapshot.Base && (git.resolved || len(git.asked) != 0) {
				t.Fatal("correctly based PR triggered a Git read")
			}
			if test.blocked {
				plan.Repair = note
				if err := (Service{Pusher: &git.fakeGit}).Apply(context.Background(), plan, NewSpec(nil, ""), false); err == nil || git.pushes != 0 {
					t.Fatalf("unsafe plan was applied: %v, pushes=%d", err, git.pushes)
				}
			}
			if test.blocked && (!strings.Contains(note.Sentence(), "would contain") || note.Ways[0].Command != "g2g github retarget --branch synthetic-head --scope path") {
				t.Errorf("repair = %#v", note)
			}
			if len(git.asked) != 0 && git.asked[0] != [2]string{"synthetic-head-tip", "synthetic-base-tip"} {
				t.Errorf("ancestry asked %v", git.asked)
			}
		})
	}
}

// An already merged head can still have an open PR. Retargeting a correctly
// based PR changes nothing, so that cannot be the repair for its ancestry.
func TestPreservedBaseRepairAllowsCorrectBaseContainingHead(t *testing.T) {
	repo := testutil.NewGitRepo(t, "synthetic-trunk")
	repo.Commit("synthetic base", "base.txt", "synthetic base")
	repo.Run("branch", "synthetic-head")
	repo.Commit("synthetic trunk advances", "trunk.txt", "synthetic trunk work")
	t.Chdir(repo.Dir)
	snapshot := stack.Snapshot{Target: "synthetic-head", Base: "synthetic-trunk", Branches: []string{"synthetic-head"}}
	plan := Plan{
		Snapshot: snapshot,
		Existing: []githubstack.PullRequest{{Number: 41, Head: "synthetic-head", Base: "synthetic-trunk", State: "OPEN"}},
		Push:     push.Plan{Snapshot: snapshot},
	}
	git := localgit.Client{Runner: subprocess.ExecRunner{}}
	if contains, err := git.IsAncestor(context.Background(), "synthetic-head", "synthetic-trunk"); err != nil || !contains {
		t.Fatalf("synthetic trunk does not contain head: %t, %v", contains, err)
	}
	note, err := (Service{Git: git}).preservedBaseRepair(context.Background(), plan)
	if err != nil || note.Sentence() != "" {
		t.Fatalf("correctly based PR refused: %s, error = %v", note.Sentence(), err)
	}
}

func TestSubmitLinkRepairPrecedesPreservedBaseRepair(t *testing.T) {
	snapshot := stack.Snapshot{
		Target: "synthetic-lower", Base: "synthetic-trunk",
		Branches: []string{"synthetic-lower", "synthetic-top", "synthetic-side"},
		Parents:  map[string]string{"synthetic-lower": "synthetic-trunk", "synthetic-top": "synthetic-lower", "synthetic-side": "synthetic-lower"},
	}
	plan := Plan{Snapshot: snapshot, Repair: repair.Note{Reason: "synthetic preserved base refusal"}}
	git := &fakeGit{}
	err := (Service{Pusher: git}).Apply(context.Background(), plan, NewSpec(nil, ""), true)
	if err == nil || !strings.Contains(err.Error(), plan.LinkBlocked()) || git.pushes != 0 {
		t.Fatalf("linking a fork: error = %v, pushes = %d; want LinkRepair first", err, git.pushes)
	}
}
