package land

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/githubstack"
	"github.com/shhac/g2g/internal/graph"
)

// assertBefore fails unless both events happened, the first one first.
func (w *world) assertBefore(t *testing.T, first, second string) {
	t.Helper()
	if one, other := w.events.index(first), w.events.index(second); one < 0 || other < 0 || one > other {
		t.Errorf("want %q before %q, got %v", first, second, w.events.seen)
	}
}

// Landing the bottom of a stack deletes the branch the next pull request sits
// on, and a base deleted with a push closes that pull request for good. It is
// moved first, although no step of the descent is about it.
func TestLandingTheBottomMovesThePullRequestAboveBeforeDeletingItsBase(t *testing.T) {
	w := newWorld(t)
	landingTheBottom(w)
	plan := w.plan(t, Defaults())
	want := []BaseMove{{Number: 42, ID: "PR_synthetic_42", Head: "synthetic-two", From: "synthetic-one", Base: "synthetic-main"}}
	if !slices.Equal(plan.Moves, want) {
		t.Fatalf("Moves = %+v, want %+v", plan.Moves, want)
	}
	recipe := make([]string, 0, len(plan.Commands()))
	for _, command := range plan.Commands() {
		recipe = append(recipe, command.Command)
	}
	edit := slices.Index(recipe, "gh pr edit 42 --base synthetic-main")
	deletion := slices.IndexFunc(recipe, func(command string) bool { return strings.Contains(command, "--delete refs/heads/synthetic-one") })
	if edit < 0 || deletion < 0 || edit > deletion {
		t.Errorf("recipe does not move #42 before deleting its base:\n%s", strings.Join(recipe, "\n"))
	}

	if err := w.service.Apply(context.Background(), plan); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	w.assertBefore(t, "merge:41:squash:admin=false", "retarget:42:synthetic-main")
	w.assertBefore(t, "retarget:42:synthetic-main", "delete-remote:synthetic-one@one-tip")
}

// The next branch up is moved by the cleanup below it, before the deletion,
// rather than at its own turn after it -- and only once.
func TestTheNextStepIsMovedBeforeTheBranchBelowIsDeleted(t *testing.T) {
	w := newWorld(t)
	plan := w.plan(t, Defaults())
	want := []BaseMove{{Number: 42, ID: "PR_synthetic_42", Head: "synthetic-two", From: "synthetic-one", Base: "synthetic-main"}}
	if !slices.Equal(plan.Moves, want) {
		t.Errorf("Moves = %+v, want %+v", plan.Moves, want)
	}

	if err := w.service.Apply(context.Background(), plan); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	w.assertBefore(t, "retarget:42:synthetic-main", "delete-remote:synthetic-one@one-tip")
	w.assertBefore(t, "retarget:42:synthetic-main", "merge:42:squash:admin=false")
	if got := w.events.only("retarget:"); !slices.Equal(got, []string{"retarget:42:synthetic-main"}) {
		t.Errorf("retargets = %v, want #42 once", got)
	}
	recipe := ""
	for _, command := range plan.Commands() {
		recipe += command.Command + "\n"
	}
	if strings.Count(recipe, "gh pr edit 42") != 1 {
		t.Errorf("recipe moves #42 other than once:\n%s", recipe)
	}
}

// Keeping the remote branch closes nothing, so nothing is moved early: each
// pull request is moved at its own turn, and one above the descent not at all.
func TestKeepingTheRemoteBranchMovesNothingEarly(t *testing.T) {
	w := newWorld(t)
	landingTheBottom(w)
	options := Defaults()
	options.DeleteRemote = false
	plan := w.plan(t, options)
	if len(plan.Moves) != 0 {
		t.Fatalf("Moves = %+v, want none when nothing is deleted", plan.Moves)
	}
	if err := w.service.Apply(context.Background(), plan); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if got := w.events.only("retarget:"); len(got) != 0 {
		t.Errorf("retargets = %v, want none", got)
	}
}

// A move GitHub refuses keeps the branch on the remote: deleting it would
// close the pull request the move was for. What merged stays merged, so the
// descent stops part-way, with the branch still recorded so that making the
// move by hand and landing again finishes the cleanup.
func TestARefusedMoveKeepsTheBranchUntilLandingAgainTidiesIt(t *testing.T) {
	w := newWorld(t)
	landingTheBottom(w)
	plan := w.plan(t, Defaults())
	w.github.retargetErr = errors.New("synthetic retarget refusal")

	err := w.service.Apply(context.Background(), plan)

	var stopped *Stopped
	if !errors.As(err, &stopped) || !stopped.PartWay() {
		t.Fatalf("Apply() error = %v, want a stop part-way", err)
	}
	for _, want := range []string{"#42", "synthetic retarget refusal", "synthetic-one is kept on origin", "gh pr edit 42 --base synthetic-main, then g2g land"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not say %q: %v", want, err)
		}
	}
	if got := w.events.only("delete-remote:"); len(got) != 0 {
		t.Errorf("deleted %v although the pull request on it could not be moved", got)
	}
	if !w.store.graph.Tracked("synthetic-one") {
		t.Fatal("synthetic-one was forgotten, so landing again has nothing left to tidy")
	}

	// The move made by hand, as advised, and land run again.
	w.github.retargetErr = nil
	if _, err := w.github.Retarget(context.Background(), []githubstack.BaseChange{{ID: "PR_synthetic_42", Number: 42, Base: "synthetic-main"}}); err != nil {
		t.Fatal(err)
	}
	w.github.prs[1].Base = "synthetic-main"
	w.git.absorbed["synthetic-one"] = true
	if err := w.service.Apply(context.Background(), w.plan(t, Defaults())); err != nil {
		t.Fatalf("landing again: %v", err)
	}
	if w.events.index("delete-remote:synthetic-one@one-tip") < 0 || w.store.graph.Tracked("synthetic-one") {
		t.Errorf("landing again did not finish tidying synthetic-one: %v", w.events.seen)
	}
}

// Two pull requests can sit on one landed branch: the next step up and a
// branch forked beside it outside the selection. Both are moved in one
// request before the branch goes, and one GitHub refuses keeps the branch
// while the other stays moved.
func TestEveryPullRequestOnALandedBranchIsMovedTogether(t *testing.T) {
	forked := func(t *testing.T) *world {
		w := newWorld(t)
		w.store.graph.Edges["synthetic-three"] = graph.Edge{Parent: "synthetic-one", ForkPoint: "one-tip"}
		w.github.prs = append(w.github.prs, githubstack.PullRequest{ID: "PR_synthetic_43", Number: 43, URL: "https://example.test/43", Head: "synthetic-three", HeadOID: "three-tip", Base: "synthetic-one", State: "OPEN"})
		return w
	}
	w := forked(t)
	plan := w.plan(t, Defaults())
	if err := w.service.Apply(context.Background(), plan); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	w.assertBefore(t, "retarget:42:synthetic-main", "delete-remote:synthetic-one@one-tip")
	w.assertBefore(t, "retarget:43:synthetic-main", "delete-remote:synthetic-one@one-tip")

	w = forked(t)
	plan = w.plan(t, Defaults())
	w.github.refuseMove = map[int]error{43: errors.New("synthetic refusal of #43")}
	err := w.service.Apply(context.Background(), plan)
	var stopped *Stopped
	if !errors.As(err, &stopped) || !strings.Contains(err.Error(), "could not point #43 at") || strings.Contains(err.Error(), "#42 at") {
		t.Fatalf("Apply() error = %v, want a stop naming #43 alone", err)
	}
	if !slices.Contains(stopped.Changed, "pointed #42 at synthetic-main") {
		t.Errorf("changed = %v, want the move of #42 reported", stopped.Changed)
	}
	if got := w.events.only("delete-remote:"); len(got) != 0 {
		t.Errorf("deleted %v although #43 still sits on it", got)
	}
}
