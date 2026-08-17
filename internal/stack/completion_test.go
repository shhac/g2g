package stack

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/graphite"
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
	asked   *bool
}

func (g completionGraphite) TrackedBranches(context.Context) ([]string, error) {
	if g.asked != nil {
		*g.asked = true
	}
	return append([]string(nil), g.tracked...), nil
}
func (g completionGraphite) Discover(_ context.Context, selected string) (graphite.Stack, error) {
	if g.asked != nil {
		*g.asked = true
	}
	return g.paths[selected], nil
}

// storedCandidates stands in for the branches g2g records itself. The real
// one lives in the graph package, which depends on this one.
type storedCandidates struct {
	branches []string
	trunks   map[string][]string
	err      error
}

func (c storedCandidates) Branches(context.Context) ([]string, error) {
	return append([]string(nil), c.branches...), c.err
}
func (c storedCandidates) Trunks(_ context.Context, target string) ([]string, error) {
	return append([]string(nil), c.trunks[target]...), c.err
}

func graphiteSource(client CompletionGraphite) GraphiteCandidates {
	return GraphiteCandidates{Graphite: client}
}

type failingGit struct{}

func (failingGit) CurrentBranch(context.Context) (string, error) {
	return "", fmt.Errorf("synthetic git failure")
}
func (failingGit) LocalBranches(context.Context) ([]string, error) {
	return nil, fmt.Errorf("synthetic git failure")
}

type failingGraphite struct{}

func (failingGraphite) TrackedBranches(context.Context) ([]string, error) {
	return nil, fmt.Errorf("synthetic Graphite failure")
}
func (failingGraphite) Discover(context.Context, string) (graphite.Stack, error) {
	return graphite.Stack{}, fmt.Errorf("synthetic Graphite failure")
}

// Candidates are the intersection of described and locally present, so a branch
// a source knows but the checkout lacks is never offered.
func TestBranchCompletionsAreLocalTrackedAndSorted(t *testing.T) {
	completions := Completions{
		Git:     completionGit{current: "beta-two-deep", branches: []string{"main", "alpha", "beta", "beta-one", "beta-two", "beta-two-deep", "gamma"}},
		Sources: []Candidates{graphiteSource(completionGraphite{tracked: []string{"gamma", "beta-two", "beta", "beta-one", "beta-two-deep", "beta-elsewhere", "alpha", "main"}})},
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
		Git:     completionGit{current: "beta", branches: []string{"main", "develop", "staging", "alpha", "beta"}},
		Sources: []Candidates{graphiteSource(completionGraphite{paths: map[string]graphite.Stack{"beta": {Path: []string{"main", "alpha", "beta"}, Trunks: []string{"staging", "main", "develop"}}}})},
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
		Sources: []Candidates{graphiteSource(completionGraphite{paths: map[string]graphite.Stack{
			"current": {Path: []string{"main", "current"}, Trunks: []string{"main"}},
			"chosen":  {Path: []string{"develop", "chosen"}, Trunks: []string{"develop"}},
		}})},
	}

	trunks, err := completions.Trunks(context.Background(), "chosen", "d")
	if err != nil {
		t.Fatalf("Trunks() error = %v", err)
	}
	if got, want := strings.Join(trunks, ","), "develop"; got != want {
		t.Errorf("trunks = %q, want %q", got, want)
	}
}

// Which source owns a branch is decided per branch, so completion offers what
// any of them describes rather than picking one and hiding the rest.
func TestCompletionsMergeEverySource(t *testing.T) {
	completions := Completions{
		Git: completionGit{current: "shared", branches: []string{"main", "owned", "declared", "shared"}},
		Sources: []Candidates{
			storedCandidates{branches: []string{"owned", "shared"}, trunks: map[string][]string{"shared": {"main"}}},
			graphiteSource(completionGraphite{
				tracked: []string{"declared", "shared"},
				paths:   map[string]graphite.Stack{"shared": {Trunks: []string{"main"}}},
			}),
		},
	}

	branches, err := completions.Branches(context.Background(), "")
	if err != nil {
		t.Fatalf("Branches() error = %v", err)
	}
	if got, want := strings.Join(branches, ","), "declared,owned,shared"; got != want {
		t.Errorf("branches = %q, want %q (a branch both sources describe must appear once)", got, want)
	}
	trunks, err := completions.Trunks(context.Background(), "shared", "")
	if err != nil {
		t.Fatalf("Trunks() error = %v", err)
	}
	if got, want := strings.Join(trunks, ","), "main"; got != want {
		t.Errorf("trunks = %q, want %q", got, want)
	}
}

// The whole point: a repository that records its own structure completes from
// it, with no Graphite installed, configured, or consulted.
func TestCompletionsAnswerWithoutGraphite(t *testing.T) {
	asked := false
	completions := Completions{
		Git: completionGit{current: "owned", branches: []string{"main", "owned"}},
		Sources: []Candidates{
			storedCandidates{branches: []string{"owned"}, trunks: map[string][]string{"owned": {"main"}}},
			GraphiteCandidates{
				Graphite:   completionGraphite{tracked: []string{"never"}, asked: &asked},
				Configured: func(context.Context) (bool, error) { return false, nil },
			},
		},
	}

	branches, err := completions.Branches(context.Background(), "")
	if err != nil {
		t.Fatalf("Branches() error = %v", err)
	}
	if got, want := strings.Join(branches, ","), "owned"; got != want {
		t.Errorf("branches = %q, want %q", got, want)
	}
	if _, err := completions.Trunks(context.Background(), "owned", ""); err != nil {
		t.Fatalf("Trunks() error = %v", err)
	}
	if asked {
		t.Error("completion consulted Graphite in a repository that does not use it; that is what enrols the repository")
	}
}

// One unusable source costs its own candidates and nothing else. Completion
// degrading to fewer options beats it failing outright, because the command
// being completed can report the real problem properly.
func TestAnUnusableSourceDoesNotCostTheOthers(t *testing.T) {
	completions := Completions{
		Git: completionGit{current: "owned", branches: []string{"main", "owned"}},
		Sources: []Candidates{
			storedCandidates{err: fmt.Errorf("synthetic store failure")},
			graphiteSource(completionGraphite{tracked: []string{"owned"}}),
		},
	}

	branches, err := completions.Branches(context.Background(), "")
	if err != nil {
		t.Fatalf("Branches() error = %v, want the surviving source's candidates", err)
	}
	if got, want := strings.Join(branches, ","), "owned"; got != want {
		t.Errorf("branches = %q, want %q", got, want)
	}
}

// A repository that cannot be read is not the same as one with no candidates.
// Skipping a source is deliberate; inventing an answer for the repository
// itself is not.
func TestCompletionsReportARepositoryTheyCannotRead(t *testing.T) {
	completions := Completions{
		Git:     failingGit{},
		Sources: []Candidates{storedCandidates{branches: []string{"owned"}}},
	}

	if _, err := completions.Branches(context.Background(), ""); err == nil {
		t.Error("Branches() error = nil when the branch list could not be read")
	}
	if _, err := completions.Trunks(context.Background(), "", ""); err == nil {
		t.Error("Trunks() error = nil when the current branch could not be read")
	}
}

// A zero value must answer rather than panic: completion reaches these on a
// keystroke, in whatever state the process happens to be wired.
func TestZeroValueGraphiteCandidatesOfferNothing(t *testing.T) {
	candidates := GraphiteCandidates{}

	branches, err := candidates.Branches(context.Background())
	if err != nil || len(branches) != 0 {
		t.Errorf("Branches() = %v, %v; want none and no error", branches, err)
	}
	trunks, err := candidates.Trunks(context.Background(), "anything")
	if err != nil || len(trunks) != 0 {
		t.Errorf("Trunks() = %v, %v; want none and no error", trunks, err)
	}
}

// A Graphite that cannot answer says so. Completion is what decides to carry
// on regardless, and it can only decide that if it is told.
func TestGraphiteCandidatesReportTheirOwnFailure(t *testing.T) {
	candidates := GraphiteCandidates{Graphite: failingGraphite{}}

	if _, err := candidates.Trunks(context.Background(), "anything"); err == nil {
		t.Error("Trunks() error = nil for a Graphite that failed")
	}
}

// Asking whether Graphite applies can itself fail. Treating that as "not
// configured" is the fail-closed answer: the cost is missing candidates, and
// the alternative is running the command that enrols the repository.
func TestAnUnanswerableGateKeepsGraphiteOut(t *testing.T) {
	asked := false
	candidates := GraphiteCandidates{
		Graphite:   completionGraphite{tracked: []string{"never"}, asked: &asked},
		Configured: func(context.Context) (bool, error) { return false, fmt.Errorf("synthetic gate failure") },
	}

	branches, err := candidates.Branches(context.Background())
	if err != nil || len(branches) != 0 {
		t.Errorf("Branches() = %v, %v; want none and no error", branches, err)
	}
	if asked {
		t.Error("Graphite was consulted despite an unanswerable gate")
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

// A source that cannot answer costs its own candidates and nothing else. A
// deadline is different in kind: it is global, so every remaining source will
// fail too, and continuing produces an empty list that is then presented as a
// complete answer. Silence and "there is nothing here" must not look the same.
func TestAnExpiredBudgetIsReportedRatherThanReadAsNoCandidates(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	completions := Completions{
		Git:     completionGit{current: "owned", branches: []string{"main", "owned"}},
		Sources: []Candidates{storedCandidates{err: context.Canceled}},
	}

	if _, err := completions.Branches(ctx, ""); err == nil {
		t.Error("Branches() error = nil for an expired budget; an empty completion list would claim there is nothing to offer")
	}
}

// The degradation the design actually wants is untouched: one broken source
// costs its own candidates while the budget still holds.
func TestABrokenSourceStillCostsOnlyItsOwnCandidates(t *testing.T) {
	completions := Completions{
		Git: completionGit{current: "owned", branches: []string{"main", "owned"}},
		Sources: []Candidates{
			storedCandidates{err: fmt.Errorf("synthetic store failure")},
			graphiteSource(completionGraphite{tracked: []string{"owned"}}),
		},
	}

	branches, err := completions.Branches(context.Background(), "")
	if err != nil {
		t.Fatalf("Branches() error = %v, want the surviving source's candidates", err)
	}
	if strings.Join(branches, ",") != "owned" {
		t.Errorf("branches = %v, want the surviving source's candidates", branches)
	}
}
