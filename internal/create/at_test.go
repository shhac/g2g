package create

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/graph"
)

type upstreamGit struct{ *fakeGit }

func (g upstreamGit) KnownParent(context.Context, string) (string, error) {
	return g.tips["known"], nil
}
func (g upstreamGit) IsAncestor(_ context.Context, from, to string) (bool, error) {
	return from == to || from == "main-tip" && (to == "upstream-tip" || to == "newer-upstream-tip") || from == "upstream-tip" && to == "newer-upstream-tip", nil
}

// Unreferenced reports a revision no local branch is at, which is all these
// fixtures ever put HEAD on.
func (g upstreamGit) Unreferenced(_ context.Context, revision string) (bool, error) {
	for _, branch := range g.local {
		if g.tips[branch] == revision {
			return false, nil
		}
	}
	return true, nil
}
func (g upstreamGit) Cherry(context.Context, string, string, string) ([]string, []string, error) {
	return []string{"synthetic-own"}, nil, nil
}
func (g upstreamGit) Absorbed(context.Context, string, string) (bool, error) { return false, nil }

func TestExplicitStartPinsBothEndsAndRestoresOriginalDetachedHead(t *testing.T) {
	g, f := fixture()
	g.detached = true
	g.tips["HEAD"] = "main-tip"
	g.tips["synthetic-upstream"], g.tips["known"] = "upstream-tip", "upstream-tip"
	f.forkPoint = "upstream-tip"
	f.failApply = errors.New("synthetic recording failure")
	service := Service{Git: upstreamGit{g}, Graph: f}
	plan, err := service.Plan(context.Background(), Request{Name: "synthetic-new", Parent: "synthetic-main", At: "synthetic-upstream"})
	if err != nil || plan.Blocked() != "" {
		t.Fatalf("Plan: %v %s", err, plan.Blocked())
	}
	if plan.At != "upstream-tip" || plan.ParentAt != "main-tip" || plan.ReturnTip != "main-tip" {
		t.Fatalf("unpinned plan: %+v", plan)
	}
	if err := service.Apply(context.Background(), plan); err == nil {
		t.Fatal("recording unexpectedly succeeded")
	}
	for _, call := range []string{"switch -c synthetic-new upstream-tip", "switch --detach main-tip", "branch -D synthetic-new"} {
		if !slices.Contains(g.calls, call) {
			t.Fatalf("missing %q in %v", call, g.calls)
		}
	}
}

func TestExplicitStartAndParentMovementChangeThePlan(t *testing.T) {
	g, f := fixture()
	g.tips["synthetic-upstream"], g.tips["known"] = "upstream-tip", "newer-upstream-tip"
	service := Service{Git: upstreamGit{g}, Graph: f}
	request := Request{Name: "synthetic-new", Parent: "synthetic-main", At: "synthetic-upstream"}
	first, err := service.Plan(context.Background(), request)
	if err != nil || first.Blocked() != "" {
		t.Fatalf("first plan: %v %s", err, first.Blocked())
	}
	g.tips["synthetic-upstream"] = "newer-upstream-tip"
	second, err := service.Plan(context.Background(), request)
	if err != nil || first.Equal(second) {
		t.Fatalf("moving start did not change plan: %v", err)
	}
	g.tips["synthetic-main"] = "upstream-tip"
	third, err := service.Plan(context.Background(), request)
	if err != nil || second.Equal(third) {
		t.Fatalf("moving parent did not change plan: %v", err)
	}
}

type brokenUpstreamGit struct {
	upstreamGit
	knownError, ancestryError error
}

func (g brokenUpstreamGit) KnownParent(ctx context.Context, parent string) (string, error) {
	if g.knownError != nil {
		return "", g.knownError
	}
	return g.upstreamGit.KnownParent(ctx, parent)
}
func (g brokenUpstreamGit) IsAncestor(ctx context.Context, from, to string) (bool, error) {
	if g.ancestryError != nil {
		return false, g.ancestryError
	}
	return g.upstreamGit.IsAncestor(ctx, from, to)
}
func TestExplicitStartPreservesReadFailuresAndCancellation(t *testing.T) {
	for _, knownFailure := range []bool{false, true} {
		g, f := fixture()
		g.tips["synthetic-upstream"], g.tips["known"] = "upstream-tip", "upstream-tip"
		git := brokenUpstreamGit{upstreamGit: upstreamGit{g}}
		if knownFailure {
			git.knownError = context.DeadlineExceeded
		} else {
			git.ancestryError = context.Canceled
		}
		_, err := (Service{Git: git, Graph: f}).Plan(context.Background(), Request{Name: "synthetic-new", Parent: "synthetic-main", At: "synthetic-upstream"})
		if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("read failure became a product refusal: %v", err)
		}
	}
}

// aheadGit is a reader that can also say the remote's default branch and how
// far apart two commits are.
type aheadGit struct{ upstreamGit }

func (g aheadGit) DefaultBranch(context.Context, string) (string, error) {
	return "synthetic-main", nil
}

// Unreferenced reports nothing left behind: the trunk's remote copy reaches
// HEAD wherever these fixtures put it.
func (g aheadGit) Unreferenced(context.Context, string) (bool, error) { return false, nil }
func (g aheadGit) Divergence(context.Context, string, string) (int, int, error) {
	return 0, 2, nil
}

// A worktree made at the trunk's remote copy is detached ahead of the trunk
// here. create offers starting there when nothing names a parent, and when
// --parent names the trunk it says that starting at its tip moves the
// checkout back. HEAD anywhere else is offered nothing: a start past the
// trunk's tip is only ever on its known upstream.
func TestADetachedHeadOnTheTrunksUpstreamIsOfferedAsTheStart(t *testing.T) {
	for _, test := range []struct {
		name, head string
		offered    bool
	}{
		{"on the known upstream", "upstream-tip", true},
		{"somewhere else", "elsewhere-tip", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			g, f := fixture()
			g.detached = true
			g.tips["HEAD"], g.tips["known"] = test.head, "newer-upstream-tip"
			git := aheadGit{upstreamGit{g}}
			service := Service{Git: git, Graph: f, Trunks: git}

			refused, err := service.Plan(context.Background(), Request{Name: "synthetic-new"})
			if err != nil {
				t.Fatal(err)
			}
			offered := strings.Contains(refused.Blocked(), "g2g create synthetic-new --parent synthetic-main --at HEAD")
			if offered != test.offered || !strings.Contains(refused.Blocked(), "pass --parent") {
				t.Errorf("refusal %q offers starting at HEAD: %t, want %t", refused.Blocked(), offered, test.offered)
			}

			behind, err := service.Plan(context.Background(), Request{Name: "synthetic-new", Parent: "synthetic-main"})
			if err != nil {
				t.Fatal(err)
			}
			if want := map[bool]int{true: 2, false: 0}[test.offered]; behind.HeadAhead != want {
				t.Errorf("HeadAhead = %d, want %d", behind.HeadAhead, want)
			}
			atHead, err := service.Plan(context.Background(), Request{Name: "synthetic-new", Parent: "synthetic-main", At: "HEAD"})
			if err != nil {
				t.Fatal(err)
			}
			if atHead.HeadAhead != 0 {
				t.Errorf("starting at HEAD still warns that HEAD is ahead: %d", atHead.HeadAhead)
			}
		})
	}
}

// What a refusal offers is what create would accept. A default branch
// recorded with a parent of its own takes no start past its tip, so nothing
// is offered for it; and a create asked to commit is offered committing too.
func TestTheOfferedStartIsOneCreateAccepts(t *testing.T) {
	g, f := fixture()
	g.detached = true
	g.tips["HEAD"], g.tips["known"] = "upstream-tip", "newer-upstream-tip"
	git := aheadGit{upstreamGit{g}}
	service := Service{Git: git, Graph: f, Trunks: git}

	committing, err := service.Plan(context.Background(), Request{Name: "synthetic-new", Commit: true, Message: "synthetic message"})
	if err != nil {
		t.Fatal(err)
	}
	if want := "g2g create synthetic-new --parent synthetic-main --at HEAD --message 'synthetic message'"; !strings.Contains(committing.Blocked(), want) {
		t.Errorf("refusal %q does not offer %q", committing.Blocked(), want)
	}

	f.adopted.Edges["synthetic-main"] = graph.Edge{Parent: "synthetic-stray"}
	recorded, err := service.Plan(context.Background(), Request{Name: "synthetic-new"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(recorded.Blocked(), "--at HEAD") {
		t.Errorf("a start create would refuse is offered: %q", recorded.Blocked())
	}
}

// localAheadGit is a reader where HEAD carries a commit above the trunk's tip
// that the trunk's known upstream does not have: work of the reader's own.
type localAheadGit struct{ aheadGit }

func (g localAheadGit) IsAncestor(_ context.Context, from, to string) (bool, error) {
	return from == to || from == "main-tip" && (to == "local-tip" || to == "newer-upstream-tip"), nil
}

// Only a start create would accept is offered or warned of: HEAD at the
// trunk's tip is offered that tip; HEAD carrying work the remote does not
// have is offered nothing; and a feature parent, which takes no start past
// its tip, is never warned that HEAD is ahead of it.
func TestOnlyAStartCreateAcceptsIsOfferedOrWarnedOf(t *testing.T) {
	t.Run("HEAD at the trunk's tip", func(t *testing.T) {
		g, f := fixture()
		g.detached = true
		g.tips["HEAD"] = "main-tip"
		git := aheadGit{upstreamGit{g}}
		refused, err := (Service{Git: git, Graph: f, Trunks: git}).Plan(context.Background(), Request{Name: "synthetic-new"})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(refused.Blocked(), "g2g create synthetic-new --parent synthetic-main") || strings.Contains(refused.Blocked(), "--at HEAD") {
			t.Errorf("refusal = %q, want the trunk's tip offered", refused.Blocked())
		}
	})
	t.Run("HEAD with work of its own", func(t *testing.T) {
		g, f := fixture()
		g.detached = true
		g.tips["HEAD"], g.tips["known"] = "local-tip", "newer-upstream-tip"
		git := localAheadGit{aheadGit{upstreamGit{g}}}
		service := Service{Git: git, Graph: f, Trunks: git}
		if refused, _ := service.Plan(context.Background(), Request{Name: "synthetic-new"}); strings.Contains(refused.Blocked(), "--at HEAD") {
			t.Errorf("offered --at HEAD for work the remote does not have: %q", refused.Blocked())
		}
		if behind, _ := service.Plan(context.Background(), Request{Name: "synthetic-new", Parent: "synthetic-main"}); behind.HeadAhead != 0 {
			t.Errorf("HeadAhead = %d for work the remote does not have", behind.HeadAhead)
		}
	})
	t.Run("a feature parent", func(t *testing.T) {
		g, f := fixture()
		g.detached = true
		g.tips["synthetic-lower"] = "main-tip"
		g.tips["HEAD"], g.tips["known"] = "upstream-tip", "newer-upstream-tip"
		git := aheadGit{upstreamGit{g}}
		plan, err := (Service{Git: git, Graph: f, Trunks: git}).Plan(context.Background(), Request{Name: "synthetic-new", Parent: "synthetic-lower"})
		if err != nil {
			t.Fatal(err)
		}
		if plan.HeadAhead != 0 {
			t.Errorf("HeadAhead = %d under a feature parent, where --at HEAD is refused", plan.HeadAhead)
		}
	})
	t.Run("revalidation sees the warning change", func(t *testing.T) {
		if (Plan{HeadAhead: 1}).Equal(Plan{HeadAhead: 2}) {
			t.Error("plans differing only in how far HEAD is ahead compare equal")
		}
	})
}
