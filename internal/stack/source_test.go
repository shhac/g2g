package stack

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/graphite"
)

// recordingSelector is an injected source. Precedence is a decision matrix, so
// spawning a process per case would buy nothing.
type recordingSelector struct {
	source    Source
	describes bool
	err       error
	snapshot  Snapshot
	asked     int
	selected  int
}

func (s *recordingSelector) Source() Source { return s.source }

func (s *recordingSelector) Describes(context.Context, string) (bool, error) {
	s.asked++
	return s.describes, s.err
}

func (s *recordingSelector) Select(context.Context, Selection, string) (Snapshot, error) {
	s.selected++
	return s.snapshot, nil
}

type resolverGit struct {
	current string
	err     error
}

func (g resolverGit) CurrentBranch(context.Context) (string, error) { return g.current, g.err }
func (g resolverGit) LocalBranches(context.Context) ([]string, error) {
	return []string{g.current}, g.err
}

func TestResolverPrefersTheFirstSourceThatDescribes(t *testing.T) {
	first := &recordingSelector{source: SourceG2G, describes: true, snapshot: Snapshot{Target: "synthetic-b"}}
	second := &recordingSelector{source: SourceGraphite, describes: true}
	resolver := Resolver{Git: resolverGit{current: "synthetic-b"}, Selectors: []Selector{first, second}}

	snapshot, err := resolver.Select(context.Background(), Selection{}, "g2g test")
	if err != nil {
		t.Fatalf("Select() error = %v", err)
	}
	if first.selected != 1 {
		t.Error("the preferred source was not used")
	}
	if second.selected != 0 {
		t.Error("a later source was consulted after an earlier one answered")
	}
	if snapshot.Source != SourceG2G {
		t.Errorf("Source = %q, want the source that answered", snapshot.Source)
	}
}

func TestResolverFallsThroughWhenTheFirstDoesNotDescribe(t *testing.T) {
	first := &recordingSelector{source: SourceG2G, describes: false}
	second := &recordingSelector{source: SourceGraphite, describes: true, snapshot: Snapshot{Target: "synthetic-b"}}
	resolver := Resolver{Git: resolverGit{current: "synthetic-b"}, Selectors: []Selector{first, second}}

	snapshot, err := resolver.Select(context.Background(), Selection{}, "g2g test")
	if err != nil {
		t.Fatalf("Select() error = %v", err)
	}
	if second.selected != 1 {
		t.Error("the fallback source was not used")
	}
	if snapshot.Source != SourceGraphite {
		t.Errorf("Source = %q", snapshot.Source)
	}
}

// Asking a source whether it describes a branch must be free of consequences,
// which is why it is a separate call from selecting.
func TestResolverDoesNotSelectFromASourceThatDeclines(t *testing.T) {
	declining := &recordingSelector{source: SourceGraphite, describes: false}
	resolver := Resolver{Git: resolverGit{current: "synthetic-b"}, Selectors: []Selector{declining}}

	_, err := resolver.Select(context.Background(), Selection{}, "g2g test")
	if err == nil {
		t.Fatal("Select() error = nil when nothing describes the branch")
	}
	if declining.selected != 0 {
		t.Error("a declining source was asked to select anyway")
	}
}

// The remedy has to name a source this build actually has, or it sends the
// reader somewhere that does not exist.
func TestResolverNamesOnlyTheSourcesItWasGiven(t *testing.T) {
	only := &recordingSelector{source: SourceG2G}
	resolver := Resolver{Git: resolverGit{current: "synthetic-b"}, Selectors: []Selector{only}}

	_, err := resolver.Select(context.Background(), Selection{}, "g2g test")
	if err == nil {
		t.Fatal("Select() error = nil")
	}
	if !strings.Contains(err.Error(), "g2g track") {
		t.Errorf("error = %v, want the remedy for the source present", err)
	}
	if strings.Contains(err.Error(), "Graphite") {
		t.Errorf("error = %v, names a source this build was not given", err)
	}
}

func TestResolverPropagatesAFailedQuestion(t *testing.T) {
	failing := &recordingSelector{source: SourceG2G, err: errors.New("synthetic failure")}
	resolver := Resolver{Git: resolverGit{current: "synthetic-b"}, Selectors: []Selector{failing}}

	if _, err := resolver.Select(context.Background(), Selection{}, "g2g test"); err == nil {
		t.Fatal("Select() error = nil")
	}
}

func TestResolverRequiresGitAndASource(t *testing.T) {
	if _, err := (Resolver{}).Select(context.Background(), Selection{}, "g2g test"); err == nil {
		t.Fatal("Select() error = nil for an unconfigured resolver")
	}
}

// Graphite's discovery creates state in a repository that has never used it,
// so the question "does Graphite describe this?" must be answerable without
// asking Graphite.
func TestGraphiteSelectorDeclinesWithoutRunningGraphite(t *testing.T) {
	asked := false
	selector := GraphiteSelector{
		Git:      resolverGit{current: "synthetic-b"},
		Graphite: refusingGraphite{onCall: func() { asked = true }},
		Configured: func(context.Context) (bool, error) {
			return false, nil
		},
	}

	describes, err := selector.Describes(context.Background(), "synthetic-b")
	if err != nil {
		t.Fatalf("Describes() error = %v", err)
	}
	if describes {
		t.Error("Describes() = true for a repository that does not use Graphite")
	}
	if asked {
		t.Error("Graphite was invoked merely to answer whether it applies")
	}
}

func TestGraphiteSelectorDescribesAConfiguredRepository(t *testing.T) {
	selector := GraphiteSelector{
		Git:        resolverGit{current: "synthetic-b"},
		Graphite:   refusingGraphite{},
		Configured: func(context.Context) (bool, error) { return true, nil },
	}

	describes, err := selector.Describes(context.Background(), "synthetic-b")
	if err != nil || !describes {
		t.Errorf("Describes() = %t, %v; want true", describes, err)
	}
}

func TestGraphiteSelectorWithoutClientsDescribesNothing(t *testing.T) {
	describes, err := (GraphiteSelector{}).Describes(context.Background(), "synthetic-b")
	if err != nil || describes {
		t.Errorf("Describes() = %t, %v; want false", describes, err)
	}
}

// refusingGraphite fails if it is ever consulted, which is the assertion.
type refusingGraphite struct{ onCall func() }

func (g refusingGraphite) DiscoverStack(context.Context, string, bool) (graphite.Stack, error) {
	if g.onCall != nil {
		g.onCall()
	}
	return graphite.Stack{}, errors.New("Graphite should not have been consulted")
}

func (g refusingGraphite) ReadForest(context.Context) (graphite.Forest, error) {
	return graphite.Forest{}, errors.New("synthetic Graphite refusal")
}
