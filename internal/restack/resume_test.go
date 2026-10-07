package restack

import (
	"context"
	"errors"
	"strings"
	"testing"

	localgit "github.com/shhac/g2g/internal/git"
	"github.com/shhac/g2g/internal/graph"
)

// stuckGit is a rebase engine that reports success and moves nothing, so every
// resumed pass plans exactly the work the last one claimed to have done.
type stuckGit struct{ *fakeGit }

func (g stuckGit) Rebase(_ context.Context, onto string, replayed localgit.Range) error {
	g.rebases = append(g.rebases, replayed)
	g.ontos = append(g.ontos, onto)
	return nil
}

// A resume that is not converging has to say so and stop. Each pass is meant
// to move at least one branch, so the loop has no other end: without the bound
// on passes, --continue spins forever re-running the same rebases.
func TestAResumeThatDoesNotSettleStopsAndKeepsTheJournal(t *testing.T) {
	git := stackGit()
	git.previewClean = false
	service, _, journal := newService(git, stack())
	service.Git = stuckGit{git}
	journal.record = Record{Branch: "synthetic-b", Scope: string(graph.ScopeTrunk), Original: map[string]string{"synthetic-a": "a-old", "synthetic-b": "b-old"}}
	journal.present = true

	err := service.Continue(context.Background())
	if err == nil || !strings.Contains(err.Error(), "did not settle") {
		t.Fatalf("Continue() error = %v, want the non-convergence refusal", err)
	}
	if !journal.present || journal.cleared != 0 {
		t.Error("a resume that did not settle cleared the journal, so --abort has nothing to undo")
	}
	if len(git.rebases) == 0 {
		t.Error("no pass ran; the case is meant to loop until the bound stops it")
	}
}

// flakyGit fails one recovery call once, at the point named, and then behaves.
type flakyGit struct {
	*fakeGit
	failUpdate      string
	failRebaseAbort bool
}

func (g *flakyGit) UpdateBranch(ctx context.Context, branch, object string) error {
	if branch == g.failUpdate {
		g.failUpdate = ""
		return errors.New("synthetic update-ref failure")
	}
	return g.fakeGit.UpdateBranch(ctx, branch, object)
}

func (g *flakyGit) RebaseAbort(ctx context.Context) error {
	if g.failRebaseAbort {
		g.failRebaseAbort = false
		return errors.New("synthetic rebase --abort failure")
	}
	return g.fakeGit.RebaseAbort(ctx)
}

// Abort is itself a run of separate Git calls, and one can fail after others
// have moved refs. The journal is the only record of where every branch began,
// so a failure part-way has to leave it in place: a second --abort then puts
// back every tip and every recorded edge, including the ones the first attempt
// had already restored.
func TestAnAbortThatFailsPartWayCanBeRetried(t *testing.T) {
	for _, test := range []struct {
		name  string
		flaky func(*fakeGit) *flakyGit
	}{
		{name: "restoring the second tip", flaky: func(git *fakeGit) *flakyGit {
			return &flakyGit{fakeGit: git, failUpdate: "synthetic-b"}
		}},
		{name: "aborting git's own rebase", flaky: func(git *fakeGit) *flakyGit {
			return &flakyGit{fakeGit: git, failRebaseAbort: true}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			git := stackGit()
			git.inProgress = true
			// Both branches were rewritten before the stop, and a resumed pass
			// recorded fork points to match.
			git.tips = map[string]string{"synthetic-a": "a-new", "synthetic-b": "b-new"}
			original := stack()
			resumed := stack()
			resumed.Edges["synthetic-a"] = graph.Edge{Parent: "synthetic-trunk", ForkPoint: "trunk-new"}
			resumed.Edges["synthetic-b"] = graph.Edge{Parent: "synthetic-a", ForkPoint: "a-new"}
			service, store, journal := newService(git, resumed)
			service.Git = test.flaky(git)
			journal.record = Record{
				Branch:   "synthetic-b",
				Scope:    string(graph.ScopeTrunk),
				Original: map[string]string{"synthetic-a": "a-old", "synthetic-b": "b-old"},
				Structure: map[string]RecordedEdge{
					"synthetic-a": {Parent: "synthetic-trunk", ForkPoint: "trunk-old"},
					"synthetic-b": {Parent: "synthetic-a", ForkPoint: "a-old"},
				},
			}
			journal.present = true
			ctx := context.Background()

			if err := service.Abort(ctx); err == nil {
				t.Fatal("Abort() error = nil; the first attempt was meant to fail part-way")
			}
			if inProgress, _ := service.InProgress(ctx); !inProgress {
				t.Fatal("a failed abort cleared the journal, so nothing can finish putting the stack back")
			}

			if err := service.Abort(ctx); err != nil {
				t.Fatalf("second Abort() error = %v", err)
			}
			for branch, want := range journal.record.Original {
				if got, _ := git.Resolve(ctx, branch); got != want {
					t.Errorf("%s = %s after the retried abort, want %s", branch, got, want)
				}
			}
			for branch, want := range original.Edges {
				if got := store.graph.Edges[branch]; got.Parent != want.Parent || got.ForkPoint != want.ForkPoint {
					t.Errorf("%s is recorded as %+v after the retried abort, want %+v", branch, got, want)
				}
			}
			if inProgress, _ := service.InProgress(ctx); inProgress {
				t.Error("the journal outlived the abort that finished")
			}
		})
	}
}
