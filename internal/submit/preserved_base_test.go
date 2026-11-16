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
	tips        map[string]string
	contains    bool
	err         error
	asked       [][2]string
	resolved    bool
	remote      map[string]string
	remoteReads [][]string
}

func (f *preservedGit) RemoteTips(_ context.Context, _ string, branches []string) (map[string]string, error) {
	f.remoteReads = append(f.remoteReads, append([]string(nil), branches...))
	if f.remote != nil {
		return f.remote, nil
	}
	tips := map[string]string{}
	for _, branch := range branches {
		if tip := f.tips["refs/heads/"+branch]; tip != "" {
			tips[branch] = tip
			f.tips[tip] = tip
		}
	}
	return tips, nil
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

// The unchanged base is assessed where publication leaves it, not where this
// clone's base happens to be. All objects and refs are real, with a bare remote.
func TestPreservedBaseProjectedRemoteRealGit(t *testing.T) {
	for _, scenario := range []struct {
		name                                            string
		remoteContains, localContains, waiting, blocked bool
	}{
		{name: "stale local base", remoteContains: true, blocked: true},
		{name: "reordered branch only", localContains: true},
		{name: "waiting base left out", localContains: true, waiting: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			repo := testutil.NewGitRepo(t, "synthetic-trunk")
			repo.Commit("synthetic root", "root.txt", "synthetic root")
			root := repo.Revision("HEAD")
			repo.Run("switch", "-qc", "synthetic-head")
			repo.Commit("synthetic head", "head.txt", "synthetic head")
			head := repo.Revision("HEAD")
			remote := t.TempDir()
			repo.Run("init", "--bare", "--initial-branch=synthetic-trunk", remote)
			repo.Run("remote", "add", "origin", remote)
			publishedBase := root
			if scenario.remoteContains {
				publishedBase = head
			}
			repo.Run("push", "origin", publishedBase+":refs/heads/synthetic-base")
			localBase := root
			if scenario.localContains {
				localBase = head
			}
			repo.Run("branch", "synthetic-base", localBase)
			t.Chdir(repo.Dir)
			snapshot := stack.Snapshot{Target: "synthetic-head", Base: "synthetic-trunk", Scope: stack.ScopeBranch, Branches: []string{"synthetic-head"}}
			plan := Plan{Snapshot: snapshot, Remote: "origin", Existing: []githubstack.PullRequest{{Number: 41, Head: "synthetic-head", Base: "synthetic-base", State: "OPEN"}}, Push: push.Plan{Snapshot: snapshot, Remote: "origin"}}
			if scenario.waiting {
				plan.Waiting = []string{"synthetic-base"}
			}
			note, err := (Service{Git: localgit.Client{Runner: subprocess.ExecRunner{}}}).preservedBaseRepair(context.Background(), plan)
			if err != nil {
				t.Fatal(err)
			}
			if got := note.Sentence() != ""; got != scenario.blocked {
				t.Fatalf("blocked = %t, want %t: %s", got, scenario.blocked, note.Sentence())
			}
		})
	}
}

func TestPreservedBaseUsesPublicationMembership(t *testing.T) {
	for _, tc := range []struct {
		name                                 string
		selected, published, observed, known bool
		wantBase                             string
		reads                                int
	}{
		{name: "pushed base uses local", selected: true, published: true, wantBase: "synthetic-local-base"},
		{name: "landed base uses observed remote", selected: true, observed: true, known: true, wantBase: "synthetic-remote-base"},
		{name: "outside base read once", known: true, wantBase: "synthetic-remote-base", reads: 1},
		{name: "outside observed base reused", observed: true, known: true, wantBase: "synthetic-remote-base"},
		{name: "absent observed base is unknown", selected: true},
		{name: "unavailable object is unknown", observed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			git := &preservedGit{tips: map[string]string{"refs/heads/synthetic-head": "synthetic-head-tip", "refs/heads/synthetic-base": "synthetic-local-base"}, remote: map[string]string{"synthetic-base": "synthetic-remote-base"}}
			if tc.known {
				git.tips["synthetic-remote-base"] = "synthetic-remote-base"
			}
			snapshot := stack.Snapshot{Target: "synthetic-head", Base: "synthetic-trunk", Branches: []string{"synthetic-head"}}
			plan := Plan{Snapshot: snapshot, Remote: "synthetic-remote", Existing: []githubstack.PullRequest{{Head: "synthetic-head", Base: "synthetic-base", State: "OPEN"}}, Push: push.Plan{Snapshot: snapshot, Publishing: map[string]push.Publication{}}}
			if tc.selected {
				plan.Push.Branches = append(append([]string(nil), snapshot.Branches...), "synthetic-base")
				plan.Push.Publishing["synthetic-base"] = push.Publication{Standing: push.Landed}
			}
			if tc.published {
				delete(plan.Push.Publishing, "synthetic-base")
			}
			if tc.observed {
				plan.Push.RemoteTips = map[string]string{"synthetic-base": "synthetic-remote-base"}
			}
			note, err := (Service{Git: git}).preservedBaseRepair(context.Background(), plan)
			if err != nil || note.Sentence() != "" {
				t.Fatalf("note=%v, err=%v", note, err)
			}
			if len(git.remoteReads) != tc.reads {
				t.Errorf("remote reads=%v; want %d", git.remoteReads, tc.reads)
			}
			if tc.wantBase == "" {
				if len(git.asked) != 0 {
					t.Fatalf("asserted unknown ancestry: %v", git.asked)
				}
				return
			}
			if len(git.asked) != 1 || git.asked[0] != [2]string{"synthetic-head-tip", tc.wantBase} {
				t.Errorf("ancestry=%v; want base %s", git.asked, tc.wantBase)
			}
		})
	}
}

func TestPreservedBaseBatchesUnobservedBases(t *testing.T) {
	snapshot := stack.Snapshot{Target: "synthetic-a", Base: "synthetic-trunk", Branches: []string{"synthetic-a", "synthetic-b"}, Parents: map[string]string{"synthetic-a": "synthetic-trunk", "synthetic-b": "synthetic-a"}}
	plan := Plan{Snapshot: snapshot, Remote: "synthetic-remote", Push: push.Plan{Snapshot: snapshot}, Existing: []githubstack.PullRequest{{Head: "synthetic-a", Base: "synthetic-outside-a", State: "OPEN"}, {Head: "synthetic-b", Base: "synthetic-outside-b", State: "OPEN"}}}
	git := &preservedGit{remote: map[string]string{}}
	if _, err := (Service{Git: git}).preservedBaseRepair(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	if len(git.remoteReads) != 1 || strings.Join(git.remoteReads[0], ",") != "synthetic-outside-a,synthetic-outside-b" {
		t.Fatalf("remote reads not batched: %v", git.remoteReads)
	}
}
