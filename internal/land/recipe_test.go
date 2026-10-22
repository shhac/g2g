package land

import (
	"context"
	"os/exec"
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
		`gh pr merge 41 --squash --match-head-commit "${g2g_landed_41:?}"`,
		"gh pr edit 42 --base synthetic-main",
		`gh pr merge 42 --squash --match-head-commit "${g2g_landed_42:?}"`,
		"g2g prune --branch synthetic-one --scope branch --apply",
		`git push --force-with-lease=refs/heads/synthetic-one:"${g2g_landed_41:?}" origin --delete refs/heads/synthetic-one`,
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
	for _, absent := range []string{"git branch -D", "--delete"} {
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
	if want := `gh pr merge 42 --squash --match-head-commit "${g2g_landed_42:?}" --admin`; len(merges) != 2 || merges[1] != want {
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

// recipeLines are the recipe's commands in order.
func recipeLines(plan Plan) []string {
	lines := make([]string, 0)
	for _, command := range plan.Commands() {
		lines = append(lines, command.Command)
	}
	return lines
}

// lineIndex is where the first line starting with prefix sits, -1 for none.
func lineIndex(lines []string, prefix string) int {
	return slices.IndexFunc(lines, func(line string) bool { return strings.HasPrefix(line, prefix) })
}

// A merge the recipe prints is pinned to the head it captures just before it,
// once the branch is published as it is now, and the remote deletion after
// the replay is leased on that same head: a reviewer's commit pushed after
// the merge makes git refuse rather than delete it. The plan's own head is
// not it -- every branch above the first is replayed before its turn -- and
// neither is anything read when the deletion runs.
func TestTheRecipePinsEachMergeAndLeasesItsDeletionOnTheCapturedHead(t *testing.T) {
	plan := Plan{
		Options: Defaults(),
		Trunk:   "synthetic-main",
		Steps:   []Step{{Branch: "synthetic-one", Number: 41, HeadOID: "stale-plan-head", Push: true}},
	}
	lines := recipeLines(plan)
	push, capture := lineIndex(lines, "g2g push "), lineIndex(lines, "g2g_landed_41=")
	merge, pull := lineIndex(lines, "gh pr merge 41"), lineIndex(lines, "g2g pull --apply")
	deletion := lineIndex(lines, "git push ")
	if !(push < capture && capture < merge && merge < pull && pull < deletion) || push < 0 {
		t.Fatalf("recipe order is not publish, capture, merge, pull, delete:\n%s", strings.Join(lines, "\n"))
	}
	if want := "g2g_landed_41=$(git rev-parse --verify refs/heads/synthetic-one)"; lines[capture] != want {
		t.Errorf("capture = %q, want %q", lines[capture], want)
	}
	if want := `gh pr merge 41 --squash --match-head-commit "${g2g_landed_41:?}"`; lines[merge] != want {
		t.Errorf("merge = %q, want %q", lines[merge], want)
	}
	if want := `git push --force-with-lease=refs/heads/synthetic-one:"${g2g_landed_41:?}" origin --delete refs/heads/synthetic-one`; lines[deletion] != want {
		t.Errorf("deletion = %q, want %q", lines[deletion], want)
	}
	if recipe := strings.Join(lines, "\n"); strings.Contains(recipe, "stale-plan-head") {
		t.Errorf("recipe pins the plan's head, which the publish replaced:\n%s", recipe)
	}
}

// A branch already landed has no merge to capture a head from. Its deletion is
// leased on the head the plan found in the trunk, as Apply's is; with none,
// the recipe offers no remote deletion rather than an unpinned one, and Apply
// leaves that branch too.
func TestTheRecipeLeasesACleanupOnlyDeletionOnTheLandedHead(t *testing.T) {
	landed := "1111111111111111111111111111111111111111"
	plan := Plan{Options: Defaults(), Trunk: "synthetic-main", Steps: []Step{{Branch: "synthetic-one", Number: 41, Landed: true, HeadOID: "synthetic-local-head", LandedHead: landed}}}
	if want := "git push --force-with-lease=refs/heads/synthetic-one:" + landed + " origin --delete refs/heads/synthetic-one"; !slices.Contains(recipeLines(plan), want) {
		t.Errorf("recipe missing %q:\n%s", want, recipeOf(plan))
	}

	plan.Steps[0].LandedHead = ""
	recipe := recipeOf(plan)
	if strings.Contains(recipe, "--delete") || strings.Contains(recipe, "force-with-lease") {
		t.Errorf("recipe offers a remote deletion with no head to pin:\n%s", recipe)
	}
	if !strings.Contains(recipe, "git branch -D synthetic-one") {
		t.Errorf("recipe dropped the local deletion too:\n%s", recipe)
	}
}

// A default queue recipe stops at the handoff, and the request it prints is
// pinned to the captured head whichever way the queue is asked; it deletes
// nothing. An existing request is only observed, and --wait is the one
// scoped g2g command.
func TestTheQueueRecipePinsTheRequestItPrintsAndDeletesNothing(t *testing.T) {
	for name, test := range map[string]struct {
		autoMerge bool
		want      string
	}{
		"auto-merge": {autoMerge: true, want: `gh pr merge 41 --match-head-commit "${g2g_landed_41:?}"`},
		"enqueue":    {autoMerge: false, want: `-f "head=${g2g_landed_41:?}"`},
	} {
		t.Run(name, func(t *testing.T) {
			plan := Plan{
				Options: Defaults(),
				Trunk:   "synthetic-main",
				Queue:   &githubstack.Queue{AutoMergeAllowed: test.autoMerge},
				Steps:   []Step{{Branch: "synthetic-one", Number: 41, ID: "synthetic-id", HeadOID: "stale-plan-head", Queue: true}},
			}
			lines := recipeLines(plan)
			if len(lines) != 2 || !strings.HasPrefix(lines[0], "g2g_landed_41=") || !strings.Contains(lines[1], test.want) {
				t.Fatalf("recipe = %q, want the capture and a request containing %q", lines, test.want)
			}
			if recipe := strings.Join(lines, "\n"); strings.Contains(recipe, "stale-plan-head") || strings.Contains(recipe, "--delete") {
				t.Errorf("queue recipe pins the plan's head or deletes:\n%s", recipe)
			}

			plan.Steps[0].Pending = "queued"
			if lines := recipeLines(plan); len(lines) != 1 || lines[0] != "gh pr view 41" {
				t.Errorf("pending recipe = %q, want observation only", lines)
			}

			plan.Steps[0].Pending = ""
			plan.Options.Wait = true
			if lines := recipeLines(plan); len(lines) != 1 || !strings.HasPrefix(lines[0], "g2g land ") || !strings.HasSuffix(lines[0], " --wait --apply") {
				t.Errorf("--wait recipe = %q, want the one scoped g2g land", lines)
			}
		})
	}
}

// The capture, the pin and the lease survive a shell: a branch and remote Git
// allows and the shell would expand reach git as the arguments they are, with
// the captured head substituted where it is named and nowhere else.
func TestTheRecipesPinnedLinesKeepTheirArgumentsInAShell(t *testing.T) {
	branch, remote := "synthetic-$x'o", "synthetic up"
	options := Defaults()
	options.Remote = remote
	plan := Plan{Options: options, Trunk: "synthetic-main", Steps: []Step{{Branch: branch, Number: 41}}}
	lines := recipeLines(plan)
	deletion := lineIndex(lines, "git push ")
	if deletion < 0 {
		t.Fatalf("no deletion in:\n%s", strings.Join(lines, "\n"))
	}
	for _, test := range []struct {
		line string
		want []string
	}{
		{lines[lineIndex(lines, "gh pr merge")], []string{"gh", "pr", "merge", "41", "--squash", "--match-head-commit", "synthetic-head"}},
		{lines[deletion], []string{"git", "push", "--force-with-lease=refs/heads/" + branch + ":synthetic-head", remote, "--delete", "refs/heads/" + branch}},
	} {
		script := `g2g_landed_41=synthetic-head; set -- ` + test.line + `; printf '%s\n' "$@"`
		output, err := exec.Command("sh", "-c", script).CombinedOutput()
		if err != nil {
			t.Fatalf("sh: %v\n%s", err, output)
		}
		if got := strings.Split(strings.TrimSuffix(string(output), "\n"), "\n"); !slices.Equal(got, test.want) {
			t.Errorf("%s\nreaches the command as %q, want %q", test.line, got, test.want)
		}
	}
	capture := lines[lineIndex(lines, "g2g_landed_41=")]
	if want := `g2g_landed_41=$(git rev-parse --verify 'refs/heads/synthetic-$x'\''o')`; capture != want {
		t.Errorf("capture = %q, want %q", capture, want)
	}
}

// An empty capture must not become an empty --match-head-commit, which gh
// treats as no head check at all. The shell refuses before invoking a tool.
func TestTheRecipeRefusesAnEmptyCapturedHead(t *testing.T) {
	for _, queue := range []bool{false, true} {
		plan := Plan{Options: Defaults(), Trunk: "synthetic-main", Steps: []Step{{Branch: "synthetic-one", Number: 41, Queue: queue}}}
		if queue {
			plan.Queue = &githubstack.Queue{AutoMergeAllowed: false}
		}
		for _, line := range recipeLines(plan) {
			if !strings.HasPrefix(line, "gh ") && !strings.HasPrefix(line, "git push ") {
				continue
			}
			for _, value := range []string{"unset g2g_landed_41", "g2g_landed_41="} {
				if output, err := exec.Command("sh", "-c", value+"; set -- "+line+"; printf 'synthetic-called'").CombinedOutput(); err == nil || strings.Contains(string(output), "synthetic-called") {
					t.Errorf("empty head did not prevent command expansion: %s\n%s", line, output)
				}
			}
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
