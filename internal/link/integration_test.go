package link_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	localgit "github.com/shhac/g2g/internal/git"
	"github.com/shhac/g2g/internal/githubstack"
	"github.com/shhac/g2g/internal/graphite"
	"github.com/shhac/g2g/internal/link"
	"github.com/shhac/g2g/internal/stack"
	"github.com/shhac/g2g/internal/subprocess"
	"github.com/shhac/g2g/internal/testutil"
)

func TestProductionAdaptersUseOnlyFakedReadOnlyDiscoveryUntilApply(t *testing.T) {
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
  "api graphql") printf '%s\n' '{"data":{"repository":{"pr0":{"nodes":[{"number":1,"headRefName":"alpha","baseRefName":"main","state":"OPEN"}]},"pr1":{"nodes":[{"number":2,"headRefName":"gamma","baseRefName":"alpha","state":"OPEN"}]},"pr2":{"nodes":[{"number":3,"headRefName":"gamma-deep","baseRefName":"gamma","state":"OPEN"}]}}}}' ;;
  "stack link") ;;
  *) exit 9 ;;
esac`,
	})
	runner := subprocess.ExecRunner{}
	gitClient := localgit.Client{Runner: runner}
	service := link.Service{
		Git:      gitClient,
		Selector: stack.GraphiteSelector{Git: gitClient, Graphite: graphite.Client{Runner: runner}},
		GitHub:   githubstack.Client{Runner: runner},
	}

	plan, err := service.Plan(context.Background(), link.Selection{Branch: "gamma-deep"})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if got, want := strings.Join(plan.Branches, ","), "alpha,gamma,gamma-deep"; got != want {
		t.Fatalf("planned branches = %q, want %q", got, want)
	}
	arguments, err := os.ReadFile(argumentsPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(arguments), "gh stack link") || strings.Contains(string(arguments), "checkout") {
		t.Fatalf("preview invoked a mutation or checkout:\n%s", arguments)
	}
	if !strings.Contains(string(arguments), "gt log short --all --reverse --no-interactive") {
		t.Fatalf("preview did not use compact Graphite discovery:\n%s", arguments)
	}

	// Drive the production sequence: revalidate, then execute.
	validated, err := service.Revalidate(context.Background(), link.Selection{Branch: "gamma-deep"}, plan)
	if err != nil {
		t.Fatalf("Revalidate() error = %v", err)
	}
	if err := service.Execute(context.Background(), validated); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	arguments, err = os.ReadFile(argumentsPath)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Count(string(arguments), "gh stack link --base main alpha gamma gamma-deep"), 1; got != want {
		t.Fatalf("apply link calls = %d, want %d:\n%s", got, want, arguments)
	}
}
