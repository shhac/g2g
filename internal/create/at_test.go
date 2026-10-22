package create

import (
	"context"
	"errors"
	"slices"
	"testing"
)

type upstreamGit struct{ *fakeGit }

func (g upstreamGit) KnownParent(context.Context, string) (string, error) {
	return g.tips["known"], nil
}
func (g upstreamGit) IsAncestor(_ context.Context, from, to string) (bool, error) {
	return from == to || from == "main-tip" && (to == "upstream-tip" || to == "newer-upstream-tip") || from == "upstream-tip" && to == "newer-upstream-tip", nil
}

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
