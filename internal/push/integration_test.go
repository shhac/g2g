package push

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shhac/gt2gh/internal/diagnostic"
	localgit "github.com/shhac/gt2gh/internal/git"
	"github.com/shhac/gt2gh/internal/graphite"
	"github.com/shhac/gt2gh/internal/link"
	"github.com/shhac/gt2gh/internal/stack"
	"github.com/shhac/gt2gh/internal/subprocess"
	"github.com/shhac/gt2gh/internal/testutil"
)

func TestProductionAdaptersUseOneAtomicLeasePush(t *testing.T) {
	fixture, err := os.ReadFile(filepath.Join("..", "graphite", "testdata", "irregular-stack.txt"))
	if err != nil {
		t.Fatal(err)
	}
	fixturePath := filepath.Join(t.TempDir(), "synthetic-stack.txt")
	if err := os.WriteFile(fixturePath, fixture, 0o600); err != nil {
		t.Fatal(err)
	}
	arguments := filepath.Join(t.TempDir(), "git-arguments")
	t.Setenv("GT_FIXTURE", fixturePath)
	t.Setenv("GIT_ARGUMENTS", arguments)
	testutil.WithFakeExecutables(t, map[string]string{
		"gt": `if [ "$1" = "--version" ]; then printf '1.8.6\n'; exit 0; fi
if [ "$*" = "log short --all --reverse --no-interactive" ]; then cat "$GT_FIXTURE"; exit 0; fi
exit 9`,
		"git": `if [ "$1 $2" = "remote get-url" ]; then printf 'https://example.test/synthetic.git\n'; exit 0; fi
if [ "$1" = "ls-remote" ]; then printf '1111111111111111111111111111111111111111\trefs/heads/alpha\n2222222222222222222222222222222222222222\trefs/heads/beta\n'; exit 0; fi
if [ "$1 $2" = "branch --show-current" ]; then printf 'beta\n'; exit 0; fi
if [ "$1" = "branch" ]; then printf 'main\nalpha\nbeta\nbeta-top\nbeta-side\ngamma\ngamma-deep\n'; exit 0; fi
if [ "$1" = "push" ]; then printf '%s\n' "$*" >> "$GIT_ARGUMENTS"; exit 0; fi
exit 9`,
	})
	var debug bytes.Buffer
	ctx := diagnostic.WithSink(context.Background(), diagnostic.Writer{Out: &debug})
	runner := subprocess.ObservingRunner{Runner: subprocess.ExecRunner{}}
	gitClient := localgit.Client{Runner: runner}
	service := Service{
		Git:      gitClient,
		Selector: stack.GraphiteSelector{Git: gitClient, Graphite: graphite.Client{Runner: runner}},
	}
	selection := link.Selection{}
	preview, err := service.Plan(ctx, selection, "origin")
	if err != nil {
		t.Fatal(err)
	}
	validated, err := service.Revalidate(ctx, selection, "origin", preview)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Execute(ctx, validated); err != nil {
		t.Fatal(err)
	}
	called, err := os.ReadFile(arguments)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.TrimSpace(string(called)), "push --atomic --force-with-lease=refs/heads/alpha:1111111111111111111111111111111111111111 --force-with-lease=refs/heads/beta:2222222222222222222222222222222222222222 --force-with-lease=refs/heads/beta-top:0000000000000000000000000000000000000000 --force-with-lease=refs/heads/beta-side:0000000000000000000000000000000000000000 origin alpha beta beta-top beta-side"; got != want {
		t.Errorf("push = %q, want %q", got, want)
	}
	for _, expected := range []string{
		"event=graphite.path", "full_stack=\"true\"", "event=push.plan",
		"event=push.revalidation match=\"true\"", "event=push.apply",
		"command=\"git push --atomic --force-with-lease=refs/heads/alpha:1111111111111111111111111111111111111111 --force-with-lease=refs/heads/beta:2222222222222222222222222222222222222222 --force-with-lease=refs/heads/beta-top:0000000000000000000000000000000000000000 --force-with-lease=refs/heads/beta-side:0000000000000000000000000000000000000000 origin alpha beta beta-top beta-side\"",
		"event=subprocess.end command=\"git push --atomic --force-with-lease=refs/heads/alpha:1111111111111111111111111111111111111111 --force-with-lease=refs/heads/beta:2222222222222222222222222222222222222222 --force-with-lease=refs/heads/beta-top:0000000000000000000000000000000000000000 --force-with-lease=refs/heads/beta-side:0000000000000000000000000000000000000000 origin alpha beta beta-top beta-side\"",
		"status=\"ok\"",
	} {
		if !strings.Contains(debug.String(), expected) {
			t.Errorf("debug missing %q: %q", expected, debug.String())
		}
	}
}
