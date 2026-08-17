package stack

import (
	"strings"
	"testing"
)

// syntheticForest is two stacks that share nothing, one of which forks. It is
// the smallest shape that tells every scope apart: a subtree that is not the
// whole stack, a stack that is not the whole trunk, a trunk that is not
// everything.
//
//	synthetic-trunk
//	├─ synthetic-a
//	│  ├─ synthetic-a-one
//	│  └─ synthetic-a-two
//	└─ synthetic-b
//	synthetic-other
//	└─ synthetic-other-child
func syntheticForest() Forest {
	return Forest{Parents: map[string]string{
		"synthetic-trunk":       "",
		"synthetic-a":           "synthetic-trunk",
		"synthetic-a-one":       "synthetic-a",
		"synthetic-a-two":       "synthetic-a",
		"synthetic-b":           "synthetic-trunk",
		"synthetic-other":       "",
		"synthetic-other-child": "synthetic-other",
	}}
}

func TestScopeSelectsWhatItNames(t *testing.T) {
	forest := syntheticForest()
	for _, test := range []struct {
		scope Scope
		from  string
		want  string
	}{
		{ScopeBranch, "synthetic-a", "synthetic-a"},
		{ScopePath, "synthetic-a-one", "synthetic-trunk,synthetic-a,synthetic-a-one"},
		{ScopeSubtree, "synthetic-a", "synthetic-a,synthetic-a-one,synthetic-a-two"},
		// stack is the two halves together: down to the trunk and up to the
		// tips, without the cousin that merely shares the trunk.
		{ScopeStack, "synthetic-a", "synthetic-trunk,synthetic-a,synthetic-a-one,synthetic-a-two"},
		{ScopeTrunk, "synthetic-a-one", "synthetic-trunk,synthetic-a,synthetic-a-one,synthetic-a-two,synthetic-b"},
		// The whole point of forest: it reaches a stack the selected branch
		// cannot, which every other scope stops short of by design.
		{ScopeAll, "synthetic-a", "synthetic-other,synthetic-other-child,synthetic-trunk,synthetic-a,synthetic-a-one,synthetic-a-two,synthetic-b"},
	} {
		t.Run(string(test.scope), func(t *testing.T) {
			selected, err := forest.Select(test.from, test.scope)
			if err != nil {
				t.Fatalf("Select(%q, %q) error = %v", test.from, test.scope, err)
			}
			if got := strings.Join(selected, ","); got != test.want {
				t.Errorf("Select(%q, %q) = %q, want %q", test.from, test.scope, got, test.want)
			}
		})
	}
}

// A scope narrower than all must not reach another trunk. This is the property
// that lets a mutating command accept trunk and never all.
func TestTrunkScopeStopsAtItsOwnRoot(t *testing.T) {
	selected, err := syntheticForest().Select("synthetic-a", ScopeTrunk)
	if err != nil {
		t.Fatalf("Select() error = %v", err)
	}
	for _, branch := range selected {
		if strings.HasPrefix(branch, "synthetic-other") {
			t.Errorf("scope trunk reached %q, which belongs to a different trunk", branch)
		}
	}
}

// Restrict is what tells a renderer where the selection's own roots are. An
// edge pointing outside the selection is not a parent for rendering.
func TestRestrictDropsEdgesLeavingTheSelection(t *testing.T) {
	forest := syntheticForest()
	selected, err := forest.Select("synthetic-a", ScopeSubtree)
	if err != nil {
		t.Fatalf("Select() error = %v", err)
	}
	edges := forest.Restrict(selected)
	if parent, present := edges["synthetic-a"]; present {
		t.Errorf("selection root synthetic-a kept parent %q; it hangs from outside the selection", parent)
	}
	if got := edges["synthetic-a-one"]; got != "synthetic-a" {
		t.Errorf("edges[synthetic-a-one] = %q, want synthetic-a", got)
	}
}

// stack excludes the cousins that merely share a trunk. That exclusion is the
// only thing separating it from trunk, so it is worth asserting directly.
func TestStackExcludesCousins(t *testing.T) {
	selected, err := syntheticForest().Select("synthetic-a-one", ScopeStack)
	if err != nil {
		t.Fatalf("Select() error = %v", err)
	}
	if got, want := strings.Join(selected, ","), "synthetic-trunk,synthetic-a,synthetic-a-one"; got != want {
		t.Errorf("Select(stack) = %q, want %q; synthetic-b shares the trunk but not the stack", got, want)
	}
}

// Every branch appears once. A forest that could list one branch twice would
// mean two parents for it, which is the shape the per-branch resolution rule
// exists to prevent.
func TestEveryScopeSelectsEachBranchOnce(t *testing.T) {
	forest := syntheticForest()
	for _, scope := range ReadScopes {
		selected, err := forest.Select("synthetic-a", scope)
		if err != nil {
			t.Fatalf("Select(%q) error = %v", scope, err)
		}
		seen := map[string]bool{}
		for _, branch := range selected {
			if seen[branch] {
				t.Errorf("scope %q listed %q twice", scope, branch)
			}
			seen[branch] = true
		}
	}
}

// A parent cycle must stop rather than recurse forever. The display is parsed
// from another tool's output, so a shape this package never creates is still a
// shape it can be handed.
func TestACycleIsReportedRatherThanWalkedForever(t *testing.T) {
	cyclic := Forest{Parents: map[string]string{
		"synthetic-one": "synthetic-two",
		"synthetic-two": "synthetic-one",
	}}
	if _, err := cyclic.Path("synthetic-one"); err == nil {
		t.Error("Path() error = nil for a parent cycle")
	}
	// Subtree walks downward, where the honest answer is to stop.
	if got := len(cyclic.Subtree("synthetic-one")); got != 2 {
		t.Errorf("Subtree() visited %d branches, want 2 without repeating", got)
	}
}

func TestOnlyBranchAndPathAreLinear(t *testing.T) {
	for _, scope := range []Scope{ScopeBranch, ScopePath} {
		if !scope.Linear() {
			t.Errorf("%q must be linear: a projection onto a GitHub stack consumes it", scope)
		}
	}
	for _, scope := range []Scope{ScopeSubtree, ScopeTrunk, ScopeAll} {
		if scope.Linear() {
			t.Errorf("%q can fork, so nothing may project or rewrite from it", scope)
		}
	}
}

// A command offering a narrow set must refuse a value another command allows,
// and say what it does take.
func TestParseScopeRefusesAValueThisCommandDoesNotOffer(t *testing.T) {
	if _, err := ParseScope(string(ScopeAll), ReadScopes, ScopeStack); err != nil {
		t.Fatalf("ParseScope(all, ReadScopes) error = %v", err)
	}
	_, err := ParseScope(string(ScopeAll), RewriteScopes, ScopeSubtree)
	if err == nil {
		t.Fatal("ParseScope(all, RewriteScopes) error = nil; a command that rewrites must refuse it")
	}
	if !strings.Contains(err.Error(), string(ScopeSubtree)) {
		t.Errorf("refusal %q does not list the scopes this command accepts", err)
	}
}

// Each command names its own default, because they genuinely differ: reading is
// free and rewriting is not.
func TestAnAbsentScopeTakesTheCommandsOwnDefault(t *testing.T) {
	for name, test := range map[string]struct {
		accepted []Scope
		fallback Scope
	}{
		"read":    {ReadScopes, ScopeStack},
		"rewrite": {RewriteScopes, ScopeSubtree},
		"project": {ProjectScopes, ScopeStack},
	} {
		got, err := ParseScope("", test.accepted, test.fallback)
		if err != nil || got != test.fallback {
			t.Errorf("%s: ParseScope(\"\") = %q, %v; want %q", name, got, err, test.fallback)
		}
	}
}
