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
	if strings.HasPrefix(revision, "refs/g2g/remotes/") {
		if g.remoteTip == "" {
			return "", fmt.Errorf("synthetic: no such ref %q", revision)
		}
		return g.remoteTip, nil
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

func (r *syncCLIRestack) Plan(_ context.Context, selection graph.Selection, onto restack.Onto, _ bool) (restack.Plan, error) {
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
	command := NewWithOptions(Options{
		Version:      "v0.1.0",
		Stdout:       &stdout,
		Stderr:       &stderr,
		Graph:        graphService,
		Sync:         syncer.Service{Git: git, Graph: graphService, Restack: replay},
		Presentation: &Presentation{},
	})
	command.SetArgs(args)
	err := command.Execute()
	return stdout.String(), err
}

// Sync is the widest mutation in the tool — it advances a shared base and
// rewrites history behind it — and had no command-level test at all. Every
// assertion below is about the sequence rather than any one step, which is the
// only thing sync itself owns.
func TestSyncPreviewChangesNothing(t *testing.T) {
	git := &syncCLIGit{remoteTip: "base-remote"}
	replay := &syncCLIRestack{steps: []string{"synthetic-login"}}

	out, err := runSync(t, git, replay, "sync", "--branch", "synthetic-login")
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
	git := &syncCLIGit{remoteTip: "base-remote"}
	replay := &syncCLIRestack{steps: []string{"synthetic-login"}}

	out, err := runSync(t, git, replay, "sync", "--branch", "synthetic-login", "--apply")
	if err != nil {
		t.Fatalf("sync --apply error = %v\n%s", err, out)
	}

	if want := "synthetic-main->" + localgit.IsolatedRef("origin", "synthetic-main"); strings.Join(git.fastForwards, ",") != want {
		t.Errorf("fast-forwards = %v, want exactly %q", git.fastForwards, want)
	}
	if replay.applies != 1 {
		t.Errorf("replayed %d times, want exactly once", replay.applies)
	}
	if !strings.Contains(out, "Synced.") {
		t.Errorf("output does not report the sync:\n%s", out)
	}
	if !strings.Contains(out, "Suggested next step: g2g status") {
		t.Errorf("successful sync does not suggest inspecting status:\n%s", out)
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
		{name: "preview", args: []string{"sync", "--branch", "synthetic-login"}, want: "Apply would refuse"},
		{name: "apply", args: []string{"sync", "--branch", "synthetic-login", "--apply"}, want: "Not applied"},
	} {
		t.Run(test.name, func(t *testing.T) {
			git := &syncCLIGit{remoteTip: "base-remote", diverged: true}
			replay := &syncCLIRestack{steps: []string{"synthetic-login"}}

			out, _ := runSync(t, git, replay, test.args...)

			if !strings.Contains(out, "diverged") {
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
	git := &syncCLIGit{remoteTip: "base-remote"}
	replay := &syncCLIRestack{
		steps:    []string{"synthetic-login"},
		applyErr: errors.New("synthetic conflict"),
		stopped:  true,
	}

	out, err := runSync(t, git, replay, "sync", "--branch", "synthetic-login", "--apply")

	if err != nil {
		t.Errorf("a stopped replay returned an error: %v", err)
	}
	if !strings.Contains(out, "stopped part-way") {
		t.Errorf("output does not report the stop:\n%s", out)
	}
	if strings.Contains(out, "Not applied") {
		t.Errorf("a half-applied sync was reported as unapplied:\n%s", out)
	}
	if strings.Contains(out, "Synced.") {
		t.Errorf("a stopped replay was reported as a completed sync:\n%s", out)
	}
}

// A replay that fails without leaving a resumable rewrite is an ordinary
// failure, and must still take the ordinary failure path.
func TestSyncReportsAFailedReplayThatLeftNothingResumable(t *testing.T) {
	git := &syncCLIGit{remoteTip: "base-remote"}
	replay := &syncCLIRestack{
		steps:    []string{"synthetic-login"},
		applyErr: errors.New("synthetic replay failure"),
	}

	out, err := runSync(t, git, replay, "sync", "--branch", "synthetic-login", "--apply")

	if err == nil {
		t.Error("a failed replay returned no error")
	}
	if !strings.Contains(out, "Not applied") {
		t.Errorf("the ordinary failure path did not run:\n%s", out)
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
			git := &syncCLIGit{remoteTip: "base-remote"}
			replay := &syncCLIRestack{steps: []string{"synthetic-login"}}

			out, err := runSync(t, git, replay, "sync", "--branch", "synthetic-login", "--scope", test.scope)

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
	git := &syncCLIGit{remoteTip: "base-remote"}
	replay := &syncCLIRestack{steps: []string{"synthetic-login"}}

	if _, err := runSync(t, git, replay, "sync", "--branch", "synthetic-login", "--scope", "trunk", "--apply"); err != nil {
		t.Fatalf("sync --scope trunk --apply: %v", err)
	}

	if replay.scopes[len(replay.scopes)-1] != "trunk" {
		t.Errorf("the replay was planned at scope %q, want trunk", replay.scopes[len(replay.scopes)-1])
	}
}
