package land

import (
	"context"
	"slices"
	"strings"
	"testing"

	localgit "github.com/shhac/g2g/internal/git"
	"github.com/shhac/g2g/internal/githubstack"
	"github.com/shhac/g2g/internal/graph"
)

// A push line that left out the remote would publish to origin when copied,
// whichever remote the descent itself publishes to.
func TestTheRecipePublishesToTheDescentsRemote(t *testing.T) {
	w := newWorld(t)
	landingTheBottom(w)
	options := Defaults()
	options.Remote = "synthetic-fork"
	plan := w.plan(t, options)
	pushes := 0
	for _, command := range plan.Commands() {
		if !strings.HasPrefix(command.Command, "g2g push ") {
			continue
		}
		pushes++
		if !strings.Contains(command.Command, " --remote synthetic-fork ") {
			t.Errorf("recipe line %q does not name the remote", command.Command)
		}
	}
	if pushes == 0 {
		t.Fatal("recipe has no push to check")
	}
}

// The recipe is what someone would run by hand, so a descent that leaves
// upstreams alone says so on every push it lists, and the push it makes is
// planned the same way.
func TestNoSetUpstreamReachesTheRecipeAndThePush(t *testing.T) {
	w := newWorld(t)
	landingTheBottom(w)
	options := Defaults()
	options.Upstream = localgit.LeaveUpstream
	plan := w.plan(t, options)
	pushes := 0
	for _, command := range plan.Commands() {
		if !strings.HasPrefix(command.Command, "g2g push ") {
			continue
		}
		pushes++
		if !strings.Contains(command.Command, " --no-set-upstream ") {
			t.Errorf("recipe line %q does not leave upstreams alone", command.Command)
		}
	}
	if pushes == 0 {
		t.Fatal("recipe has no push to check")
	}
	if err := w.service.Apply(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(w.pusher.upstreams, []localgit.Upstream{localgit.LeaveUpstream, localgit.LeaveUpstream}) {
		t.Errorf("pushes planned with %v, want every one leaving upstreams alone", w.pusher.upstreams)
	}
}

// The recipe a preview shows and the work an apply does are built from the same
// steps, so they cannot come to describe different things.
func TestCommandsDescribeTheStepsApplyWalks(t *testing.T) {
	w := newWorld(t)
	plan := w.plan(t, Defaults())

	commands := plan.Commands()
	joined := make([]string, 0, len(commands))
	for _, command := range commands {
		if command.Command == "" {
			t.Errorf("a recipe line has nothing to run: %+v", command)
		}
		joined = append(joined, command.Command)
	}
	recipe := strings.Join(joined, "\n")

	for _, want := range []string{
		"gh pr merge 41 --squash",
		"gh pr edit 42 --base synthetic-main",
		"gh pr merge 42 --squash",
		"g2g prune --branch synthetic-one --scope branch --apply",
		"git push origin --delete synthetic-one",
		"git branch -D synthetic-one",
	} {
		if !strings.Contains(recipe, want) {
			t.Errorf("recipe missing %q:\n%s", want, recipe)
		}
	}
	// Never this: gh's own --delete-branch removes the local branch too.
	if strings.Contains(recipe, "--delete-branch") {
		t.Errorf("recipe offers gh's --delete-branch:\n%s", recipe)
	}
	if first, second := strings.Index(recipe, "merge 41"), strings.Index(recipe, "merge 42"); first > second {
		t.Error("the recipe merges top-down")
	}
}

// Apply syncs after every branch, the last included, because that is what
// brings the trunk here onto the final merge. The recipe left the last one out,
// so following it by hand ended with the trunk behind its remote.
func TestTheRecipeSyncsAfterEveryBranchAsApplyDoes(t *testing.T) {
	w := newWorld(t)
	plan := w.plan(t, Defaults())
	recipe := 0
	for _, command := range plan.Commands() {
		if command.Command == "g2g pull --apply" {
			recipe++
		}
	}

	if err := w.service.Apply(context.Background(), plan); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	if applied := len(w.events.only("sync:")); recipe != applied {
		t.Errorf("the recipe syncs %d times and Apply %d", recipe, applied)
	}
}

// A descent that only tidies up after merges made in a browser still keeps the
// comments on what is left, and the recipe says so. The two were once decided
// apart, and the recipe left the line out while the command wrote them.
func TestATidyOnlyDescentSaysItKeepsTheComments(t *testing.T) {
	plan := Plan{
		Options: Defaults(),
		Trunk:   "synthetic-main",
		Steps:   []Step{{Branch: "synthetic-one", Number: 41, Landed: true}},
		Above:   []string{"synthetic-two"},
	}
	if plan.Landing() != 0 || !plan.KeepsComments() {
		t.Fatalf("Landing() = %d, KeepsComments() = %t, want a tidy-only descent that keeps them", plan.Landing(), plan.KeepsComments())
	}
	if recipe := recipeOf(plan); !strings.Contains(recipe, "g2g github comment --branch synthetic-two --apply") {
		t.Errorf("recipe does not say it keeps the comments:\n%s", recipe)
	}

	plan.Options.Comment = false
	if plan.KeepsComments() || strings.Contains(recipeOf(plan), "g2g github comment") {
		t.Error("--no-comment still keeps the comments")
	}
}

func recipeOf(plan Plan) string {
	recipe := ""
	for _, command := range plan.Commands() {
		recipe += command.Command + "\n"
	}
	return recipe
}

func TestCommandsFollowTheCleanupFlags(t *testing.T) {
	w := newWorld(t)
	plan := w.plan(t, Options{Remote: "origin", Method: githubstack.MethodRebase, Admin: true})

	recipe := ""
	for _, command := range plan.Commands() {
		recipe += command.Command + "\n"
	}
	for _, absent := range []string{"git branch -D", "git push origin --delete"} {
		if strings.Contains(recipe, absent) {
			t.Errorf("recipe offers %q with both deletions turned off:\n%s", absent, recipe)
		}
	}
	if !strings.Contains(recipe, "gh pr merge 41 --rebase") {
		t.Errorf("recipe does not carry the chosen method:\n%s", recipe)
	}
}

// With --admin on a protected repository the recipe says --admin where the
// merge will need it, rather than a merge GitHub would refuse if run by hand.
func TestTheRecipeForecastsAdminOnTheBranchesAReplayWillBlock(t *testing.T) {
	w := newWorld(t)
	blocked := w.github.states[41]
	blocked.StateStatus = githubstack.StatusBlocked
	w.github.states[41] = blocked
	options := Defaults()
	options.Admin = true

	plan := w.plan(t, options)

	if len(plan.Protected) != 0 {
		t.Errorf("Protected = %v, want no warning once --admin was given", plan.Protected)
	}
	var merges []string
	for _, command := range plan.Commands() {
		if strings.HasPrefix(command.Command, "gh pr merge") {
			merges = append(merges, command.Command)
		}
	}
	if want := "gh pr merge 42 --squash --admin"; len(merges) != 2 || merges[1] != want {
		t.Errorf("merges in the recipe = %v, want the second to be %q", merges, want)
	}
}

// What sits on the last branch landed is what remains of the stack, and the
// recipe ends by keeping the comments there, so what landed reads as merged
// history. --no-comment leaves them; a descent with nothing above has none.
func TestTheRecipeKeepsTheStackCommentsOnWhatRemains(t *testing.T) {
	w := newWorld(t)
	w.store.graph.Edges["synthetic-three"] = graph.Edge{Parent: "synthetic-two", ForkPoint: "two-tip"}

	plan := w.plan(t, Defaults())
	if !slices.Equal(plan.Above, []string{"synthetic-three"}) {
		t.Fatalf("Above = %v, want what sits on the last branch landed", plan.Above)
	}
	commands := plan.Commands()
	if last := commands[len(commands)-1].Command; last != "g2g github comment --branch synthetic-three --apply" {
		t.Errorf("last step = %q, want the comments kept on what remains", last)
	}

	options := Defaults()
	options.Comment = false
	for _, command := range w.plan(t, options).Commands() {
		if strings.HasPrefix(command.Command, "g2g github comment") {
			t.Errorf("--no-comment still keeps comments: %q", command.Command)
		}
	}
}

// A recipe is pasted into a shell, and Git allows a branch name the shell
// would expand, so every name in it is quoted.
func TestTheRecipeQuotesNamesTheShellWouldExpand(t *testing.T) {
	options := Defaults()
	options.Remote = "synthetic up"
	got := Plan{Options: options}.pushCommand("synthetic-$(touch x)")
	if want := "g2g push --branch 'synthetic-$(touch x)' --scope path --remote 'synthetic up' --apply"; got != want {
		t.Errorf("pushCommand = %q, want %q", got, want)
	}
}
