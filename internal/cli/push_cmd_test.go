package cli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/shhac/gt2gh/internal/graphite"
	"github.com/shhac/gt2gh/internal/link"
	"github.com/shhac/gt2gh/internal/push"
	syncer "github.com/shhac/gt2gh/internal/sync"
)

func TestPushPreviewAndApplyUseOneAtomicLeasePush(t *testing.T) {
	git := &cliPushGit{current: "synthetic-middle", branches: []string{"synthetic-main", "synthetic-lower", "synthetic-middle", "synthetic-top"}}
	pushService := push.Service{Git: git, Graphite: cliPushGraphite{}}
	for _, test := range []struct {
		name string
		args []string
		want []string
	}{
		{"preview current", []string{"push"}, []string{"Target: synthetic-middle", "synthetic-top", "git push --atomic --force-with-lease origin synthetic-lower synthetic-middle synthetic-top", "Atomic push: all selected refs advance together or none do.", "No changes were made."}},
		{"preview default full stack", []string{"push", "--branch", "synthetic-middle"}, []string{"synthetic-top", "git push --atomic --force-with-lease origin synthetic-lower synthetic-middle synthetic-top"}},
		{"preview no stack", []string{"push", "--branch", "synthetic-middle", "--no-stack"}, []string{"git push --atomic --force-with-lease origin synthetic-lower synthetic-middle"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			command := newWithPresentation("v", "gt2gh", &stdout, &stderr, link.Service{}, syncer.Service{}, pushService, Presentation{})
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
	command := newWithPresentation("v", "gt2gh", &stdout, &stderr, link.Service{}, syncer.Service{}, pushService, Presentation{})
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
		Target: "synthetic-top", Base: "synthetic-main", Remote: "origin",
		Branches: []string{"synthetic-lower", "synthetic-top"},
	}
	for _, test := range []struct {
		name         string
		presentation Presentation
		want         string
	}{
		{
			name: "plain",
			want: "Target: synthetic-top\n\n  synthetic-main (trunk)\n  └─ synthetic-lower\n    └─ synthetic-top\n\nCommand to run\ngit push --atomic --force-with-lease origin synthetic-lower synthetic-top\nAtomic push: all selected refs advance together or none do.\n",
		},
		{
			name:         "color",
			presentation: Presentation{Color: true},
			want:         "\x1b[1;36mTarget\x1b[0m: \x1b[1;37msynthetic-top\x1b[0m\n\n  \x1b[1;33msynthetic-main (trunk)\x1b[0m\n  └─ \x1b[1;37msynthetic-lower\x1b[0m\n    └─ \x1b[1;37msynthetic-top\x1b[0m\n\n\x1b[1;36mCommand to run\x1b[0m\n\x1b[1;97;48;5;236mgit push --atomic --force-with-lease origin synthetic-lower synthetic-top\x1b[0m\n\x1b[2mAtomic push: all selected refs advance together or none do.\x1b[0m\n",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			if err := writePushPlan(&output, plan, test.presentation); err != nil {
				t.Fatal(err)
			}
			if got := output.String(); got != test.want {
				t.Errorf("snapshot = %q, want %q", got, test.want)
			}
			if strings.Contains(output.String(), "$ ") || strings.Contains(output.String(), "bottom to top") {
				t.Errorf("command card contains decoration: %q", output.String())
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
		{"fork", &cliPushGit{current: "synthetic-middle", branches: []string{"synthetic-main", "synthetic-lower", "synthetic-middle"}}, cliPushGraphite{stackErr: errors.New("multiple descendants")}, []string{"push"}, "multiple descendants"},
		{"fork opt out", &cliPushGit{current: "synthetic-middle", branches: []string{"synthetic-main", "synthetic-lower", "synthetic-middle"}}, cliPushGraphite{stackErr: errors.New("multiple descendants")}, []string{"push", "--no-stack"}, ""},
		{"remote", &cliPushGit{current: "synthetic-middle", branches: []string{"synthetic-main", "synthetic-lower", "synthetic-middle"}, remoteErr: errors.New("unknown remote")}, cliPushGraphite{}, []string{"push", "--remote", "synthetic"}, "unknown remote"},
		{"atomic", &cliPushGit{current: "synthetic-middle", branches: []string{"synthetic-main", "synthetic-lower", "synthetic-middle", "synthetic-top"}, pushErr: errors.New("atomic unsupported")}, cliPushGraphite{}, []string{"push", "--apply"}, "Not applied\natomic unsupported"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			service := push.Service{Git: test.git, Graphite: test.graphite}
			command := newWithPresentation("v", "gt2gh", &stdout, &stderr, link.Service{}, syncer.Service{}, service, Presentation{})
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
	command := newWithPresentation("v", "gt2gh", &stdout, &stderr, link.Service{}, syncer.Service{}, push.Service{Git: git, Graphite: cliPushGraphite{}}, Presentation{})
	command.SetArgs([]string{"push", "--debug"})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"operation=\"push\"", "event=push.plan", "target=\"synthetic-middle\"",
		"full_stack=\"true\"", "remote=\"origin\"",
		"command=\"git push --atomic --force-with-lease origin synthetic-lower synthetic-middle synthetic-top\"",
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
}

func (f *cliPushGit) CurrentBranch(context.Context) (string, error)   { return f.current, nil }
func (f *cliPushGit) LocalBranches(context.Context) ([]string, error) { return f.branches, nil }
func (f *cliPushGit) Remote(context.Context, string) error            { return f.remoteErr }
func (f *cliPushGit) PushAtomic(_ context.Context, _ string, branches []string) error {
	f.pushes++
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
