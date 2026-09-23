package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	localgit "github.com/shhac/g2g/internal/git"
	"github.com/shhac/g2g/internal/graph"
	"github.com/shhac/g2g/internal/prune"
	"github.com/shhac/g2g/internal/restack"
	syncer "github.com/shhac/g2g/internal/sync"
)

// syncCLIGit is the remote half of a sync: what the base is now, what the
// remote says it is, and whether the two can be fast-forwarded.
type syncCLIGit struct {
	// remoteTip is what the isolated ref resolves to. Empty means the base was
	// never pushed, which is ordinary for a local trunk.
	remoteTip string
	// diverged makes the local base unreachable from the remote one, which is
	// the state sync refuses rather than reconciles.
	diverged bool

	fetches      int
	fastForwards []string
	// published maps a branch to the tip the remote holds for it. Only what a
	// case sets is on the remote at all.
	published map[string]string
}

func (g *syncCLIGit) Remote(context.Context, string) error { return nil }

func (g *syncCLIGit) FetchIsolated(context.Context, string, []string) error {
	g.fetches++
	return nil
}

func (g *syncCLIGit) FastForward(_ context.Context, branch, to string) error {
	g.fastForwards = append(g.fastForwards, branch+"->"+to)
	return nil
}

func (g *syncCLIGit) Resolve(_ context.Context, revision string) (string, error) {
	if branch, isolated := strings.CutPrefix(revision, "refs/g2g/remotes/origin/"); isolated {
		if tip := g.published[branch]; tip != "" {
			return tip, nil
		}
		if branch == "synthetic-main" && g.remoteTip != "" {
			return g.remoteTip, nil
		}
		return "", fmt.Errorf("synthetic: no such ref %q", revision)
	}
	return "base-local", nil
}

// IsAncestor is asked with ref names rather than resolved objects, so what it
// answers is simply whether the base can be fast-forwarded.
func (g *syncCLIGit) IsAncestor(context.Context, string, string) (bool, error) {
	return !g.diverged, nil
}

// syncCLIRestack stands in for the replay. Sync's own job is ordering, so what
// matters here is that it is asked at all, and exactly once.
type syncCLIRestack struct {
	steps []string
	// applyErr is what the replay fails with, and stopped reports whether it
	// left a resumable rewrite behind rather than simply failing.
	applyErr error
	stopped  bool

	applies int
	// scopes records the scope each replay was planned at, which is the only
	// way to see that the flag reached the step it is meant to widen.
	scopes []string
	// reparented records that sync asked for a structural change, which it
	// never should.
	reparented bool
}

func (r *syncCLIRestack) Plan(_ context.Context, selection graph.Selection, onto restack.Onto, _ bool, _ restack.Pending) (restack.Plan, error) {
	r.scopes = append(r.scopes, string(selection.Scope))
	// sync asks for a location, never a parent: recording the fetched ref as a
	// parent is what left every synced branch hanging from refs/g2g/.
	if onto.Reparents() {
		r.reparented = true
	}
	plan := restack.Plan{Onto: onto}
	for _, branch := range r.steps {
		plan.Steps = append(plan.Steps, restack.Step{Branch: branch, Parent: "synthetic-main", Base: "base-local", ForkPoint: "fork", Tip: "tip"})
	}
	return plan, nil
}

func (r *syncCLIRestack) Apply(context.Context, restack.Plan) error {
	r.applies++
	return r.applyErr
}

func (r *syncCLIRestack) InProgress(context.Context) (bool, error) { return r.stopped, nil }

func runSync(t *testing.T, git *syncCLIGit, replay *syncCLIRestack, args ...string) (string, error) {
	t.Helper()
	return runPull(t, git, replay, nil, args...)
}

// runPull is runSync with a prune service, for pull --prune; a nil one leaves
// prune unconfigured.
func runPull(t *testing.T, git *syncCLIGit, replay *syncCLIRestack, pruning prune.Git, args ...string) (string, error) {
	t.Helper()

	recorded := graph.New()
	for _, edge := range []struct{ branch, parent string }{
		{"synthetic-auth", "synthetic-main"},
		{"synthetic-login", "synthetic-auth"},
	} {
		updated, err := recorded.Track(edge.branch, graph.Edge{Parent: edge.parent, ForkPoint: "0000000000000000000000000000000000000000"})
		if err != nil {
			t.Fatalf("Track(%q) error = %v", edge.branch, err)
		}
		recorded = updated
	}

	graphService := graph.Service{Git: graphGitFixture(), Store: &graphStore{graph: recorded}}
	var stdout, stderr bytes.Buffer
	options := Options{
		Version:      "v0.1.0",
		Stdout:       &stdout,
		Stderr:       &stderr,
		Graph:        graphService,
		Sync:         syncer.Service{Git: git, Graph: graphService, Restack: replay},
		Presentation: &Presentation{},
	}
	if pruning != nil {
		options.Prune = prune.Service{Git: pruning, Graph: graphService}
	}
	command := NewWithOptions(options)
	command.SetArgs(args)
	err := command.Execute()
	return stdout.String(), err
}

// Sync is the widest mutation in the tool — it advances a shared base and
// rewrites history behind it — and had no command-level test at all. Every
// assertion below is about the sequence rather than any one step, which is the
// only thing sync itself owns.
func TestSyncPreviewChangesNothing(t *testing.T) {
	git := &syncCLIGit{remoteTip: "base-remote", published: map[string]string{"synthetic-main": "base-remote"}}
	replay := &syncCLIRestack{steps: []string{"synthetic-login"}}

	out, err := runSync(t, git, replay, "pull", "--branch", "synthetic-login")
	if err != nil {
		t.Fatalf("sync error = %v", err)
	}

	if len(git.fastForwards) != 0 || replay.applies != 0 {
		t.Errorf("the preview mutated: fast-forwards=%v applies=%d", git.fastForwards, replay.applies)
	}
	if !strings.Contains(out, "Rerun with --apply") {
		t.Errorf("preview does not say how to apply:\n%s", out)
	}
}

func TestSyncAdvancesTheBaseThenReplaysExactlyOnce(t *testing.T) {
	git := &syncCLIGit{remoteTip: "base-remote", published: map[string]string{"synthetic-main": "base-remote"}}
	replay := &syncCLIRestack{steps: []string{"synthetic-login"}}

	out, err := runSync(t, git, replay, "pull", "--branch", "synthetic-login", "--apply")
	if err != nil {
		t.Fatalf("sync --apply error = %v\n%s", err, out)
	}

	if want := "synthetic-main->" + localgit.IsolatedRef("origin", "synthetic-main"); strings.Join(git.fastForwards, ",") != want {
		t.Errorf("fast-forwards = %v, want exactly %q", git.fastForwards, want)
	}
	if replay.applies != 1 {
		t.Errorf("replayed %d times, want exactly once", replay.applies)
	}
	if !strings.Contains(out, "Pulled.") {
		t.Errorf("output does not report the pull:\n%s", out)
	}
	if !strings.Contains(out, "Suggested next step: g2g push") {
		t.Errorf("successful sync does not suggest publishing what it replayed:\n%s", out)
	}
}

// A diverged base is reported, never merged or reset. The refusal has to reach
// both halves of the command: a preview that invited an apply which then
// refuses is advice for a command that will not run.
func TestSyncRefusesADivergedBaseInBothPreviewAndApply(t *testing.T) {
	for _, test := range []struct {
		name string
		args []string
		want string
	}{
		{name: "preview", args: []string{"pull", "--branch", "synthetic-login"}, want: "Apply would refuse"},
		{name: "apply", args: []string{"pull", "--branch", "synthetic-login", "--apply"}, want: "Not applied"},
	} {
		t.Run(test.name, func(t *testing.T) {
			git := &syncCLIGit{remoteTip: "base-remote", diverged: true, published: map[string]string{"synthetic-main": "base-remote"}}
			replay := &syncCLIRestack{steps: []string{"synthetic-login"}}

			out, _ := runSync(t, git, replay, test.args...)

			if !strings.Contains(out, "both sides have moved") {
				t.Errorf("output does not name the divergence:\n%s", out)
			}
			if !strings.Contains(out, test.want) {
				t.Errorf("output does not say %q:\n%s", test.want, out)
			}
			if len(git.fastForwards) != 0 || replay.applies != 0 {
				t.Errorf("a refused sync mutated: fast-forwards=%v applies=%d", git.fastForwards, replay.applies)
			}
			if strings.Contains(out, "Suggested next step:") {
				t.Errorf("a refused sync offered a success-path suggestion:\n%s", out)
			}
		})
	}
}

// A replay that stops on a conflict has half-applied the sync, so reporting it
// as "not applied" would be a lie — the base really was advanced. It is
// reported once, and the exit is clean because there is nothing to retry:
// the remedy is g2g restack --continue.
func TestSyncReportsAStoppedReplayOnceAndDoesNotCallItUnapplied(t *testing.T) {
	git := &syncCLIGit{remoteTip: "base-remote", published: map[string]string{"synthetic-main": "base-remote"}}
	replay := &syncCLIRestack{
		steps:    []string{"synthetic-login"},
		applyErr: errors.New("synthetic conflict"),
		stopped:  true,
	}

	out, err := runSync(t, git, replay, "pull", "--branch", "synthetic-login", "--apply")

	// It reports the stop rather than failing at the user, and the status says
	// so too: zero would have told a script the sync had finished.
	if !wasStopped(err) {
		t.Errorf("a stopped replay did not mark itself stopped: %v", err)
	}
	if alreadyPresented(err) || err == nil {
		t.Errorf("a stopped replay should carry a status without a second report: %v", err)
	}
	if !strings.Contains(out, "stopped part-way") {
		t.Errorf("output does not report the stop:\n%s", out)
	}
	if strings.Contains(out, "Not applied") {
		t.Errorf("a half-applied sync was reported as unapplied:\n%s", out)
	}
	if strings.Contains(out, "Pulled.") {
		t.Errorf("a stopped replay was reported as a completed sync:\n%s", out)
	}
}

// A replay that fails without leaving a resumable rewrite, when nothing else
// moved first, is an ordinary failure and takes the ordinary failure path.
func TestSyncReportsAFailedReplayThatLeftNothingResumable(t *testing.T) {
	git := &syncCLIGit{remoteTip: "base-local", published: map[string]string{"synthetic-main": "base-local"}}
	replay := &syncCLIRestack{
		steps:    []string{"synthetic-login"},
		applyErr: errors.New("synthetic replay failure"),
	}

	out, err := runSync(t, git, replay, "pull", "--branch", "synthetic-login", "--apply")

	if err == nil {
		t.Error("a failed replay returned no error")
	}
	if !strings.Contains(out, "Not applied") {
		t.Errorf("the ordinary failure path did not run:\n%s", out)
	}
}

// The same failure after the trunk advanced is not "not applied": the trunk
// is where the remote has it and stays there, so the run stopped part-way.
func TestSyncReportsAFailedReplayAfterTheTrunkAdvanced(t *testing.T) {
	git := &syncCLIGit{remoteTip: "base-remote", published: map[string]string{"synthetic-main": "base-remote"}}
	replay := &syncCLIRestack{
		steps:    []string{"synthetic-login"},
		applyErr: errors.New("synthetic replay failure"),
	}

	out, err := runSync(t, git, replay, "pull", "--branch", "synthetic-login", "--apply")

	if !wasStopped(err) {
		t.Errorf("error = %v, want the part-way status", err)
	}
	if strings.Contains(out, "Not applied") || !strings.Contains(out, "Brought synthetic-main to what the remote holds") {
		t.Errorf("output does not say the trunk moved:\n%s", out)
	}
}

// sync was the only mutating stack command with no scope at all, so the
// boundary it acted on was whatever it hardcoded. Only two values mean
// anything: replaying less than a whole stack leaves the branches below it on
// the old base, and the subtree's own fork point did not move, so the replay
// would do nothing.
func TestSyncOffersOnlyTheTwoScopesThatMeanSomething(t *testing.T) {
	for _, test := range []struct {
		scope   string
		refused bool
	}{
		{scope: "stack"},
		{scope: "trunk"},
		{scope: "subtree", refused: true},
		{scope: "path", refused: true},
		{scope: "branch", refused: true},
		{scope: "all", refused: true},
	} {
		t.Run(test.scope, func(t *testing.T) {
			git := &syncCLIGit{remoteTip: "base-remote", published: map[string]string{"synthetic-main": "base-remote"}}
			replay := &syncCLIRestack{steps: []string{"synthetic-login"}}

			out, err := runSync(t, git, replay, "pull", "--branch", "synthetic-login", "--scope", test.scope)

			if !test.refused {
				if err != nil {
					t.Fatalf("sync --scope %s: %v\n%s", test.scope, err, out)
				}
				return
			}
			if err == nil {
				t.Fatalf("sync --scope %s was accepted", test.scope)
			}
			if !strings.Contains(err.Error(), "stack, trunk") {
				t.Errorf("refusal does not list what sync takes: %v", err)
			}
		})
	}
}

// The widening is the whole point of the flag: trunk brings every stack on the
// trunk up to date, not just the one the target sits in.
func TestSyncTrunkScopeReachesTheWholeTrunk(t *testing.T) {
	git := &syncCLIGit{remoteTip: "base-remote", published: map[string]string{"synthetic-main": "base-remote"}}
	replay := &syncCLIRestack{steps: []string{"synthetic-login"}}

	if _, err := runSync(t, git, replay, "pull", "--branch", "synthetic-login", "--scope", "trunk", "--apply"); err != nil {
		t.Fatalf("sync --scope trunk --apply: %v", err)
	}

	if replay.scopes[len(replay.scopes)-1] != "trunk" {
		t.Errorf("the replay was planned at scope %q, want trunk", replay.scopes[len(replay.scopes)-1])
	}
}

// ResetBranch takes a published version that supersedes the local one.
func (g *syncCLIGit) ResetBranch(_ context.Context, branch, to string) error {
	g.fastForwards = append(g.fastForwards, branch+"<-"+to)
	return nil
}

// Cherry reports what this side has that the other does not, by content.
//
// A diverged base only refuses when taking the published version would lose
// something; where it would lose nothing, it supersedes instead. So a case that
// means "refuse" has to say it has work of its own.
func (g *syncCLIGit) Cherry(context.Context, string, string, string) ([]string, []string, error) {
	if g.diverged {
		return []string{"synthetic-commit-only-here"}, nil, nil
	}
	return nil, nil, nil
}

// Absorbed says no, so a case that means "diverged" stays diverged.
func (g *syncCLIGit) Absorbed(context.Context, string, string) (bool, error) { return false, nil }

// RemoteTips reports the base as published and nothing else, so these tests
// stay about the sequence. A branch the remote has moved on is collection's
// subject, and it has its own tests.
func (g *syncCLIGit) RemoteTips(_ context.Context, _ string, branches []string) (map[string]string, error) {
	tips := map[string]string{}
	for _, branch := range branches {
		if g.published[branch] != "" {
			tips[branch] = g.published[branch]
		}
	}
	return tips, nil
}

// failingPrune cannot answer whether anything has landed.
type failingPrune struct{}

func (failingPrune) Cherry(context.Context, string, string, string) ([]string, []string, error) {
	return nil, nil, errors.New("synthetic cherry failure")
}

func (failingPrune) Absorbed(context.Context, string, string) (bool, error) { return false, nil }

// The pull happened, so a prune that fails after it is a stop part-way — and
// the exit status says only that, so the reason has to be on the page. It was
// not: a failure before the prune's own flow printed anything was carried by
// the error alone, which a stop part-way does not print.
func TestPullPruneThatCannotRunSaysWhyAndStopsPartWay(t *testing.T) {
	git := &syncCLIGit{remoteTip: "base-remote", published: map[string]string{"synthetic-main": "base-remote"}}
	replay := &syncCLIRestack{steps: []string{"synthetic-login"}}

	out, err := runPull(t, git, replay, failingPrune{}, "pull", "--prune", "--branch", "synthetic-login", "--apply")
	if !wasStopped(err) || exitCode(err) != stoppedExitCode {
		t.Fatalf("error = %v, want a stop part-way", err)
	}
	for _, want := range []string{"Pulled.", "synthetic cherry failure", "The pull stands and nothing was forgotten"} {
		if !strings.Contains(out, want) {
			t.Errorf("output does not say %q:\n%s", want, out)
		}
	}
	if replay.applies != 1 {
		t.Errorf("replayed %d times, want the pull to have happened", replay.applies)
	}
}
