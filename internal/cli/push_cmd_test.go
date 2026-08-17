package cli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	localgit "github.com/shhac/g2g/internal/git"
	"github.com/shhac/g2g/internal/graphite"
	"github.com/shhac/g2g/internal/link"
	"github.com/shhac/g2g/internal/push"
	"github.com/shhac/g2g/internal/stack"
	"github.com/shhac/g2g/internal/testutil/forest"
)

func TestPushPreviewAndApplyUseOneAtomicLeasePush(t *testing.T) {
	git := &cliPushGit{current: "synthetic-middle", branches: []string{"synthetic-main", "synthetic-lower", "synthetic-middle", "synthetic-top"}}
	pushService := push.Service{Git: git, Selector: stack.GraphiteSelector{Git: git, Graphite: cliPushGraphite{}}}
	for _, test := range []struct {
		name string
		args []string
		want []string
	}{
		{"preview current", []string{"push"}, []string{"Target  synthetic-middle", "synthetic-top", "git push --atomic --force-with-lease origin synthetic-lower synthetic-middle synthetic-top", "Atomic push: all selected refs advance together or none do.", "No changes were made."}},
		{"preview default full stack", []string{"push", "--branch", "synthetic-middle"}, []string{"synthetic-top", "git push --atomic --force-with-lease origin synthetic-lower synthetic-middle synthetic-top"}},
		{"preview no stack", []string{"push", "--branch", "synthetic-middle", "--scope", "path"}, []string{"git push --atomic --force-with-lease origin synthetic-lower synthetic-middle"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			command := newWithPresentation("v", "g2g", &stdout, &stderr, link.Service{}, pushService, Presentation{})
			command.SetArgs(test.args)
			if err := command.Execute(); err != nil {
				t.Fatal(err)
			}
			for _, expected := range test.want {
				if !strings.Contains(stdout.String(), expected) {
					t.Errorf("output missing %q: %q", expected, stdout.String())
				}
			}
			if git.pushes != 0 || stderr.Len() != 0 {
				t.Errorf("pushes=%d stderr=%q", git.pushes, stderr.String())
			}
		})
	}

	var stdout, stderr bytes.Buffer
	command := newWithPresentation("v", "g2g", &stdout, &stderr, link.Service{}, pushService, Presentation{})
	command.SetArgs([]string{"push", "--apply"})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	if git.pushes != 1 || strings.Join(git.pushed, ",") != "synthetic-lower,synthetic-middle,synthetic-top" || !strings.Contains(stdout.String(), "Ready to apply") || !strings.Contains(stdout.String(), "Applied — remote refs updated atomically") {
		t.Errorf("pushes=%d branches=%v output=%q", git.pushes, git.pushed, stdout.String())
	}
}

func TestPushPlanSnapshotsRemainSpacedAndCopyable(t *testing.T) {
	plan := push.Plan{
		Snapshot: stack.Snapshot{Target: "synthetic-top", Base: "synthetic-main", Branches: []string{"synthetic-lower", "synthetic-top"}},
		Remote:   "origin",
	}
	for _, test := range []struct {
		name         string
		presentation Presentation
	}{
		{name: "push-plain"},
		{name: "push-color", presentation: Presentation{Color: true}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			if err := writePushPlan(&output, plan, test.presentation); err != nil {
				t.Fatal(err)
			}
			assertGolden(t, test.name, output.String())
		})
	}
}

func TestPushApplyRendersAndFlushesBeforeMutation(t *testing.T) {
	events := []string{}
	writer := &recordingWriter{events: &events}
	git := &cliPushGit{events: &events, current: "synthetic-middle", branches: []string{"synthetic-main", "synthetic-lower", "synthetic-middle", "synthetic-top"}}
	command := newWithPresentation("v", "g2g", writer, writer, link.Service{}, push.Service{Git: git, Selector: stack.GraphiteSelector{Git: git, Graphite: cliPushGraphite{}}}, Presentation{})
	command.SetArgs([]string{"push", "--apply"})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	write, flush, push := eventIndex(events, "write"), eventIndex(events, "flush"), eventIndex(events, "push")
	if write < 0 || flush < 0 || push < 0 || write > flush || flush > push {
		t.Errorf("events = %v, want write -> flush -> push", events)
	}
}

func TestPushApplyDoesNotMutateWhenReadyOutputFails(t *testing.T) {
	for _, test := range []struct {
		name   string
		writer *recordingWriter
	}{
		{"write", &recordingWriter{writeErr: context.Canceled}},
		{"flush", &recordingWriter{flushErr: context.Canceled}},
	} {
		t.Run(test.name, func(t *testing.T) {
			git := &cliPushGit{current: "synthetic-middle", branches: []string{"synthetic-main", "synthetic-lower", "synthetic-middle", "synthetic-top"}}
			command := newWithPresentation("v", "g2g", test.writer, test.writer, link.Service{}, push.Service{Git: git, Selector: stack.GraphiteSelector{Git: git, Graphite: cliPushGraphite{}}}, Presentation{})
			command.SetArgs([]string{"push", "--apply"})
			if err := command.Execute(); err == nil {
				t.Fatal("Execute() error = nil")
			}
			if git.pushes != 0 {
				t.Errorf("pushes=%d, want 0", git.pushes)
			}
		})
	}
}

func TestPushFailsClosedForForkRaceAndFailure(t *testing.T) {
	for _, test := range []struct {
		name     string
		git      *cliPushGit
		graphite cliPushGraphite
		args     []string
		want     string
	}{
		// A fork is refused because one atomic push needs one ordered path, and
		// the refusal names the remedy rather than picking a line.
		{"fork", &cliPushGit{current: "synthetic-middle", branches: []string{"synthetic-main", "synthetic-lower", "synthetic-middle", "synthetic-top", "synthetic-side"}}, cliPushGraphite{stackErr: errors.New("forked")}, []string{"push"}, "one ordered path"},
		{"fork opt out", &cliPushGit{current: "synthetic-middle", branches: []string{"synthetic-main", "synthetic-lower", "synthetic-middle", "synthetic-top", "synthetic-side"}}, cliPushGraphite{stackErr: errors.New("forked")}, []string{"push", "--scope", "path"}, ""},
		{"remote", &cliPushGit{current: "synthetic-middle", branches: []string{"synthetic-main", "synthetic-lower", "synthetic-middle"}, remoteErr: errors.New("unknown remote")}, cliPushGraphite{}, []string{"push", "--remote", "synthetic"}, "unknown remote"},
		{"atomic", &cliPushGit{current: "synthetic-middle", branches: []string{"synthetic-main", "synthetic-lower", "synthetic-middle", "synthetic-top"}, pushErr: errors.New("atomic unsupported")}, cliPushGraphite{}, []string{"push", "--apply"}, "Not applied\natomic unsupported"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			service := push.Service{Git: test.git, Selector: stack.GraphiteSelector{Git: test.git, Graphite: test.graphite}}
			command := newWithPresentation("v", "g2g", &stdout, &stderr, link.Service{}, service, Presentation{})
			command.SetArgs(test.args)
			err := command.Execute()
			if test.want == "" {
				if err != nil {
					t.Fatalf("error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), strings.TrimPrefix(test.want, "Not applied\n")) {
				t.Fatalf("error = %v", err)
			}
			if !strings.Contains(stdout.String(), test.want) && test.name == "atomic" {
				t.Errorf("output=%q", stdout.String())
			}
			if test.git.pushes > 1 {
				t.Errorf("pushes=%d, want one attempt and no fallback", test.git.pushes)
			}
		})
	}
}

func TestPushDebugIsStderrOnly(t *testing.T) {
	git := &cliPushGit{current: "synthetic-middle", branches: []string{"synthetic-main", "synthetic-lower", "synthetic-middle", "synthetic-top"}}
	var stdout, stderr bytes.Buffer
	command := newWithPresentation("v", "g2g", &stdout, &stderr, link.Service{}, push.Service{Git: git, Selector: stack.GraphiteSelector{Git: git, Graphite: cliPushGraphite{}}}, Presentation{})
	command.SetArgs([]string{"push", "--debug"})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"operation=\"push\"", "event=push.plan", "target=\"synthetic-middle\"",
		"scope=\"stack\"", "remote=\"origin\"",
		"command=\"git push --atomic --force-with-lease=refs/heads/synthetic-lower:",
	} {
		if !strings.Contains(stderr.String(), expected) {
			t.Errorf("debug missing %q: %q", expected, stderr.String())
		}
	}
	if strings.Contains(stdout.String(), "debug event=") {
		t.Errorf("stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

type cliPushGit struct {
	current            string
	branches, pushed   []string
	remoteErr, pushErr error
	pushes             int
	events             *[]string
}

func (f *cliPushGit) CurrentBranch(context.Context) (string, error)   { return f.current, nil }
func (f *cliPushGit) LocalBranches(context.Context) ([]string, error) { return f.branches, nil }
func (f *cliPushGit) Remote(context.Context, string) error            { return f.remoteErr }
func (f *cliPushGit) RemoteTips(_ context.Context, _ string, branches []string) (map[string]string, error) {
	tips := map[string]string{}
	for _, branch := range branches {
		tips[branch] = "remote-" + branch
	}
	return tips, nil
}

func (f *cliPushGit) PushAtomic(_ context.Context, _ string, leases []localgit.Lease) error {
	branches := make([]string, 0, len(leases))
	for _, lease := range leases {
		branches = append(branches, lease.Branch)
	}
	f.pushes++
	if f.events != nil {
		*f.events = append(*f.events, "push")
	}
	f.pushed = append([]string(nil), branches...)
	return f.pushErr
}

type cliPushGraphite struct{ stackErr error }

func (f cliPushGraphite) Discover(context.Context, string) (graphite.Stack, error) {
	return f.DiscoverStack(context.Background(), "synthetic-middle", false)
}
func (f cliPushGraphite) DiscoverStack(_ context.Context, branch string, stack bool) (graphite.Stack, error) {
	if stack && f.stackErr != nil {
		return graphite.Stack{}, f.stackErr
	}
	path := []string{"synthetic-main", "synthetic-lower", "synthetic-middle"}
	if branch == "synthetic-top" || stack {
		path = append(path, "synthetic-top")
	}
	return graphite.Stack{Path: path, Trunks: []string{"synthetic-main"}}, nil
}
func (cliPushGraphite) TrackedBranches(context.Context) ([]string, error) {
	return []string{"synthetic-lower", "synthetic-middle", "synthetic-top"}, nil
}

// A fork is a shape rather than an error the read returns, because that is
// what it is: reading one is ordinary, and only a linear projection cannot
// represent it.
func (f cliPushGraphite) ReadForest(context.Context) (graphite.Forest, error) {
	forest := forest.Of([]string{"synthetic-main", "synthetic-lower", "synthetic-middle", "synthetic-top"})
	if f.stackErr != nil {
		forest.Parents["synthetic-side"] = "synthetic-middle"
	}
	return forest, nil
}
