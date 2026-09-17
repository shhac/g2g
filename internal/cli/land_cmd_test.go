package cli

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/githubstack"
	"github.com/shhac/g2g/internal/land"
	"github.com/shhac/g2g/internal/repair"
	"github.com/shhac/g2g/internal/stack"
)

func landPlanFixture() land.Plan {
	plan := land.Plan{
		Options: land.Defaults(),
		Trunk:   "synthetic-main",
		Steps: []land.Step{
			{Branch: "synthetic-one", Number: 41, URL: "https://example.test/41", Base: "synthetic-main"},
			// The ordinary shape of everything above the bottom: republished
			// by its own replay, and still pointing at the branch below it.
			{Branch: "synthetic-two", Number: 42, URL: "https://example.test/42", Base: "synthetic-main", From: "synthetic-one", Push: true, Admin: true},
		},
	}
	plan.Snapshot = stack.Snapshot{
		Target:       "synthetic-two",
		TargetSource: "current Git branch",
		Base:         "synthetic-main",
		Branches:     []string{"synthetic-one", "synthetic-two"},
	}
	return plan
}

func TestLandPreviewSnapshotsStayReadableAndCopyable(t *testing.T) {
	for _, test := range []struct {
		name         string
		presentation Presentation
	}{
		{name: "land-plain"},
		{name: "land-color", presentation: Presentation{Color: true}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			if err := writeLandPlan(&output, landPlanFixture(), test.presentation); err != nil {
				t.Fatal(err)
			}
			assertGolden(t, test.name, output.String())
		})
	}
}

// A refused descent has no ordered set of commands that reaches the end, and
// showing the ones decided before the refusal invites running half of it.
func TestABlockedLandOffersNoRecipe(t *testing.T) {
	plan := landPlanFixture()
	plan.Steps = nil
	plan.Repair = repair.Note{
		Reason: "synthetic-two (#42) is a draft",
		Ways:   []repair.Step{{Command: "gh pr ready 42", Effect: "mark it ready for review"}},
	}
	plan.Blocked = plan.Repair.Sentence()

	var output bytes.Buffer
	if err := writeLandPlan(&output, plan, Presentation{}); err != nil {
		t.Fatal(err)
	}

	rendered := output.String()
	if strings.Contains(rendered, "Commands this would run") {
		t.Errorf("a refused descent offered a recipe:\n%s", rendered)
	}
	// The stack is still drawn in full: the branch the refusal is about is the
	// one a reader is looking for, and it has no step.
	for _, branch := range []string{"synthetic-one", "synthetic-two"} {
		if !strings.Contains(rendered, branch) {
			t.Errorf("preview omits %s:\n%s", branch, rendered)
		}
	}
	if !strings.Contains(rendered, "gh pr ready 42") {
		t.Errorf("preview omits the way out:\n%s", rendered)
	}
}

func TestLandRecipeIsNumberedAndRunnable(t *testing.T) {
	var output bytes.Buffer
	if err := writeLandPlan(&output, landPlanFixture(), Presentation{}); err != nil {
		t.Fatal(err)
	}

	rendered := output.String()
	for _, want := range []string{
		"Commands this would run, in order",
		"1  gh pr merge 41 --squash",
		"gh pr edit 42 --base synthetic-main",
		"gh pr merge 42 --squash --admin",
		"git branch -D synthetic-two",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("recipe missing %q:\n%s", want, rendered)
		}
	}
	// gh's own --delete-branch takes the local branch with it, so nothing here
	// may ever offer it.
	if strings.Contains(rendered, "--delete-branch") {
		t.Errorf("recipe offers gh's --delete-branch:\n%s", rendered)
	}
}

// Marks are control characters. They must not reach a machine format, where
// they would be read as part of a command.
func TestLandMachineOutputCarriesNoControlCharacters(t *testing.T) {
	for _, format := range []outputFormat{formatJSON, formatPorcelain} {
		var output bytes.Buffer
		if err := writeLandPlan(&output, landPlanFixture(), Presentation{Format: format}); err != nil {
			t.Fatal(err)
		}
		rendered := output.String()
		if strings.ContainsAny(rendered, markCommandOpen+markCommandClose) {
			t.Errorf("%s output carries command marks: %q", format, rendered)
		}
		if !strings.Contains(rendered, "gh pr merge 41 --squash") {
			t.Errorf("%s output omits the recipe: %s", format, rendered)
		}
	}
}

func TestPorcelainNumbersEachStepOfTheRecipe(t *testing.T) {
	var output bytes.Buffer
	if err := writeLandPlan(&output, landPlanFixture(), Presentation{Format: formatPorcelain}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "step\t1\tgh pr merge 41 --squash") {
		t.Errorf("porcelain does not number the recipe:\n%s", output.String())
	}
}

// Both refusals happen before the service is reached, so a zero one is enough
// to drive them -- and asserting that is itself worth something: a flag this
// wrong should never get as far as discovery.
func landCommand(t *testing.T, args ...string) error {
	t.Helper()
	cmd := newLand(land.Service{}, testCompletions(), nil, Presentation{})
	cmd.SetArgs(args)
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	return cmd.Execute()
}

func TestLandRefusesAMergeMethodItDoesNotKnow(t *testing.T) {
	err := landCommand(t, "--method", "fast-forward")
	if err == nil || !strings.Contains(err.Error(), "unsupported merge method") {
		t.Fatalf("land --method fast-forward error = %v", err)
	}
}

// Leaving a branch recorded under one that has merged and been deleted makes
// every later status and every later replay measure against a structure that
// is not there.
func TestLandRefusesToKeepTheGraphItWouldInvalidate(t *testing.T) {
	err := landCommand(t, "--no-forget")
	if err == nil || !strings.Contains(err.Error(), "no longer exists") {
		t.Fatalf("land --no-forget error = %v", err)
	}
}

func TestLandDefaultsToEveryCleanupAndToSquash(t *testing.T) {
	defaults := land.Defaults()
	if !defaults.DeleteRemote || !defaults.DeleteLocal || !defaults.Forget {
		t.Errorf("Defaults() = %+v, want every cleanup on", defaults)
	}
	if defaults.Method != githubstack.MethodSquash {
		t.Errorf("Method = %q, want squash", defaults.Method)
	}
}

// A descent that stopped part-way is not a failure to retry — the merges that
// happened are permanent — but it is not what was asked for either, and zero
// told a script the whole stack had landed.
func TestAStoppedDescentCarriesAStatusOfItsOwn(t *testing.T) {
	var out bytes.Buffer
	cmd := newLand(land.Service{}, testCompletions(), nil, Presentation{})
	cmd.SetOut(&out)

	err := stoppedMidLand(cmd, &land.Stopped{
		Landed: []string{"synthetic-one"},
		Branch: "synthetic-two",
		Err:    errors.New("synthetic refusal"),
	}, Presentation{})

	if !wasStopped(err) {
		t.Fatalf("stoppedMidLand() error = %v, want it marked as stopped", err)
	}
	if wasStopped(errors.New("synthetic other")) {
		t.Error("an ordinary failure was read as a stop")
	}
	// The report carries the detail, so the status is all that is left to say.
	rendered := out.String()
	for _, want := range []string{"Stopped part-way at synthetic-two", "synthetic-one", "stay merged"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("report missing %q:\n%s", want, rendered)
		}
	}
	// Distinct from the failure status, because the two want opposite responses.
	if stoppedExitCode == 2 || stoppedExitCode == 0 {
		t.Errorf("stoppedExitCode = %d, want its own", stoppedExitCode)
	}
}
