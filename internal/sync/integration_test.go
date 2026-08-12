package sync_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	localgit "github.com/shhac/gt2gh/internal/git"
	"github.com/shhac/gt2gh/internal/githubstack"
	"github.com/shhac/gt2gh/internal/graphite"
	"github.com/shhac/gt2gh/internal/link"
	"github.com/shhac/gt2gh/internal/subprocess"
	stackSync "github.com/shhac/gt2gh/internal/sync"
	"github.com/shhac/gt2gh/internal/testutil"
)

func TestProductionAdaptersPreviewAndApplyWithPathFakes(t *testing.T) {
	fixture, err := os.ReadFile(filepath.Join("..", "graphite", "testdata", "irregular-stack.txt"))
	if err != nil {
		t.Fatal(err)
	}
	temporary := t.TempDir()
	fixturePath := filepath.Join(temporary, "graphite-log.txt")
	argumentsPath := filepath.Join(temporary, "arguments.txt")
	if err := os.WriteFile(fixturePath, fixture, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GT_FIXTURE", fixturePath)
	t.Setenv("CLI_ARGUMENTS", argumentsPath)
	t.Setenv("GH_PRS", `{"data":{"pr0":{"nodes":[{"number":1,"url":"https://example.test/1","headRefName":"alpha","baseRefName":"main","state":"OPEN"}]},"pr1":{"nodes":[{"number":2,"url":"https://example.test/2","headRefName":"gamma","baseRefName":"alpha","state":"OPEN"}]},"pr2":{"nodes":[{"number":3,"url":"https://example.test/3","headRefName":"gamma-deep","baseRefName":"gamma","state":"OPEN"}]}}}`)
	testutil.WithFakeExecutables(t, map[string]string{
		"git": `printf 'git %s\n' "$*" >> "$CLI_ARGUMENTS"
case "$1 $2" in
  "branch --show-current") printf 'gamma-deep\n' ;;
  "branch --format=%(refname:short)") printf '%s\n' main alpha beta beta-top beta-side gamma gamma-deep delta delta-deep epsilon epsilon-deep ;;
  "status --porcelain") ;;
  *) exit 9 ;;
esac`,
		"gt": `printf 'gt %s\n' "$*" >> "$CLI_ARGUMENTS"
case "$1" in
  --version) printf '1.8.6\n' ;;
  log) cat "$GT_FIXTURE" ;;
  *) exit 9 ;;
esac`,
		"gh": `printf 'gh %s\n' "$*" >> "$CLI_ARGUMENTS"
case "$1 $2" in
  "repo view") printf '{"nameWithOwner":"example/fixture"}\n' ;;
  "api graphql") printf '%s\n' "$GH_PRS" ;;
  "stack link") ;;
  *) exit 9 ;;
esac`,
	})
	runner := subprocess.ExecRunner{}
	discoverer := link.Service{Git: localgit.Client{Runner: runner}, Graphite: graphite.Client{Runner: runner}, GitHub: githubstack.Client{Runner: runner}}
	service := stackSync.Service{Discoverer: discoverer, Git: localgit.Client{Runner: runner}, GitHub: githubstack.Client{Runner: runner}}

	preview, err := service.Preview(context.Background(), "gamma-deep")
	if err != nil {
		t.Fatalf("Preview() error = %v", err)
	}
	if !preview.CanApply() {
		t.Fatal("CanApply() = false, want true")
	}
	arguments, err := os.ReadFile(argumentsPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(arguments), "gh stack link") {
		t.Fatalf("preview invoked mutation:\n%s", arguments)
	}
	if _, err := service.Apply(context.Background(), "gamma-deep", preview); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	arguments, err = os.ReadFile(argumentsPath)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Count(string(arguments), "gh stack link --base main alpha gamma gamma-deep"), 1; got != want {
		t.Fatalf("sync link calls = %d, want %d:\n%s", got, want, arguments)
	}

	t.Setenv("GH_PRS", `{"data":{"pr0":{"nodes":[{"number":1,"url":"https://example.test/1","headRefName":"alpha","baseRefName":"main","state":"OPEN"}]},"pr1":{"nodes":[{"number":2,"url":"https://example.test/2","headRefName":"gamma","baseRefName":"alpha","state":"OPEN"}]},"pr2":{"nodes":[]}}}`)
	blocked, err := service.Preview(context.Background(), "gamma-deep")
	if err != nil {
		t.Fatalf("blocked Preview() error = %v", err)
	}
	if blocked.CanApply() {
		t.Fatal("blocked CanApply() = true")
	}
	if _, err := service.Apply(context.Background(), "gamma-deep", blocked); err == nil {
		t.Fatal("blocked Apply() error = nil")
	}
	arguments, err = os.ReadFile(argumentsPath)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Count(string(arguments), "gh stack link --base main alpha gamma gamma-deep"), 1; got != want {
		t.Fatalf("blocked sync link calls = %d, want %d:\n%s", got, want, arguments)
	}
}
