package stack

import (
	"context"
	"strings"
	"testing"

	"github.com/shhac/gt2gh/internal/graphite"
)

type completionGit struct {
	current  string
	branches []string
}

func (g completionGit) CurrentBranch(context.Context) (string, error) { return g.current, nil }
func (g completionGit) LocalBranches(context.Context) ([]string, error) {
	return append([]string(nil), g.branches...), nil
}

type completionGraphite struct {
	tracked []string
	paths   map[string]graphite.Stack
}

func (g completionGraphite) TrackedBranches(context.Context) ([]string, error) {
	return append([]string(nil), g.tracked...), nil
}
func (g completionGraphite) Discover(_ context.Context, selected string) (graphite.Stack, error) {
	return g.paths[selected], nil
}

// Candidates are the intersection of Graphite-tracked and locally present, so
// a branch Graphite knows but the checkout lacks is never offered.
func TestBranchCompletionsAreLocalTrackedAndSorted(t *testing.T) {
	completions := Completions{
		Git:      completionGit{current: "beta-two-deep", branches: []string{"main", "alpha", "beta", "beta-one", "beta-two", "beta-two-deep", "gamma"}},
		Graphite: completionGraphite{tracked: []string{"gamma", "beta-two", "beta", "beta-one", "beta-two-deep", "beta-elsewhere", "alpha", "main"}},
	}

	branches, err := completions.Branches(context.Background(), "beta")
	if err != nil {
		t.Fatalf("Branches() error = %v", err)
	}
	if got, want := strings.Join(branches, ","), "beta,beta-one,beta-two,beta-two-deep"; got != want {
		t.Errorf("branches = %q, want %q", got, want)
	}
}

func TestTrunkCompletionsAreLocalAndSorted(t *testing.T) {
	completions := Completions{
		Git:      completionGit{current: "beta", branches: []string{"main", "develop", "staging", "alpha", "beta"}},
		Graphite: completionGraphite{paths: map[string]graphite.Stack{"beta": {Path: []string{"main", "alpha", "beta"}, Trunks: []string{"staging", "main", "develop"}}}},
	}

	trunks, err := completions.Trunks(context.Background(), "", "")
	if err != nil {
		t.Fatalf("Trunks() error = %v", err)
	}
	if got, want := strings.Join(trunks, ","), "develop,main,staging"; got != want {
		t.Errorf("trunks = %q, want %q", got, want)
	}
}

// An explicit target must be resolved without consulting, or changing, the
// checked-out branch.
func TestTrunkCompletionsUseExplicitTargetWithoutCheckout(t *testing.T) {
	completions := Completions{
		Git: completionGit{current: "current", branches: []string{"main", "develop", "current", "chosen"}},
		Graphite: completionGraphite{paths: map[string]graphite.Stack{
			"current": {Path: []string{"main", "current"}, Trunks: []string{"main"}},
			"chosen":  {Path: []string{"develop", "chosen"}, Trunks: []string{"develop"}},
		}},
	}

	trunks, err := completions.Trunks(context.Background(), "chosen", "d")
	if err != nil {
		t.Fatalf("Trunks() error = %v", err)
	}
	if got, want := strings.Join(trunks, ","), "develop"; got != want {
		t.Errorf("trunks = %q, want %q", got, want)
	}
}

func TestCompletionsRefuseWhenNotConfigured(t *testing.T) {
	if _, err := (Completions{}).Branches(context.Background(), ""); err == nil {
		t.Error("Branches() on an unconfigured value = nil, want error")
	}
	if _, err := (Completions{}).Trunks(context.Background(), "", ""); err == nil {
		t.Error("Trunks() on an unconfigured value = nil, want error")
	}
}
