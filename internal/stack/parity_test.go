package stack

import (
	"context"
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/graphite"
)

// parityGraphite answers from a forest, which is all the real client does now.
type parityGraphite struct{ forest graphite.Forest }

func (g parityGraphite) ReadForest(context.Context) (graphite.Forest, error) { return g.forest, nil }

type parityGit struct{ branches []string }

func (g parityGit) CurrentBranch(context.Context) (string, error) { return "synthetic-b", nil }
func (g parityGit) LocalBranches(context.Context) ([]string, error) {
	return append([]string(nil), g.branches...), nil
}

// A scope asked of Graphite must mean what it means asked of the g2g store.
//
// Nothing caught the original divergence because each record was
// self-consistent: Graphite resolved through a bool that could not carry six
// scopes, so branch returned the whole ancestry and path extended past the
// target, while the store's own traversal did neither. The only test that finds
// that is one that asks both the same question and compares.
//
//	synthetic-trunk
//	├─ synthetic-a
//	│  └─ synthetic-b   ← selected
//	│     ├─ synthetic-c
//	│     └─ synthetic-d
//	└─ synthetic-cousin
func TestEveryScopeSelectsTheSameBranchesFromEitherRecord(t *testing.T) {
	parents := map[string]string{
		"synthetic-trunk":  "",
		"synthetic-a":      "synthetic-trunk",
		"synthetic-b":      "synthetic-a",
		"synthetic-c":      "synthetic-b",
		"synthetic-d":      "synthetic-b",
		"synthetic-cousin": "synthetic-trunk",
	}
	local := []string{"synthetic-trunk", "synthetic-a", "synthetic-b", "synthetic-c", "synthetic-d", "synthetic-cousin"}

	for _, test := range []struct {
		scope Scope
		// want is the selection: the branches the command acts on.
		want string
		// base is what that selection hangs from. For a scope rooted at the
		// target it is the target's parent and sits outside the selection; for
		// one that reaches the trunk it opens the selection.
		base string
	}{
		{ScopeBranch, "synthetic-b", "synthetic-a"},
		{ScopePath, "synthetic-a,synthetic-b", "synthetic-trunk"},
		{ScopeSubtree, "synthetic-b,synthetic-c,synthetic-d", "synthetic-a"},
		{ScopeStack, "synthetic-a,synthetic-b,synthetic-c,synthetic-d", "synthetic-trunk"},
		{ScopeTrunk, "synthetic-a,synthetic-b,synthetic-c,synthetic-d,synthetic-cousin", "synthetic-trunk"},
	} {
		t.Run(string(test.scope), func(t *testing.T) {
			// What the shared traversal says, once the base is taken out of it
			// the same way a selector does.
			forest := Forest{Parents: parents}
			owned, err := forest.Select("synthetic-b", test.scope)
			if err != nil {
				t.Fatalf("Forest.Select() error = %v", err)
			}
			base, within, err := forest.Hangs(owned, "synthetic-b", test.scope)
			if err != nil {
				t.Fatalf("Hangs() error = %v", err)
			}
			if base != test.base {
				t.Errorf("the store hangs the selection from %q, want %q", base, test.base)
			}
			if within {
				owned = owned[1:]
			}
			if got := strings.Join(owned, ","); got != test.want {
				t.Errorf("the store selected %q, want %q", got, test.want)
			}

			// What Graphite answers, through the resolver commands actually use.
			snapshot, err := Resolve(
				context.Background(),
				parityGit{branches: local},
				parityGraphite{forest: graphite.Forest{Parents: parents, Roots: []string{"synthetic-trunk"}}},
				Selection{Scope: test.scope},
				"synthetic command",
			)
			if err != nil {
				t.Fatalf("Resolve() error = %v", err)
			}
			if got := strings.Join(snapshot.Branches, ","); got != test.want {
				t.Errorf("Graphite selected %q, want %q", got, test.want)
			}
			if snapshot.Base != test.base {
				t.Errorf("Graphite hangs the selection from %q, want %q", snapshot.Base, test.base)
			}
			if snapshot.Scope != test.scope {
				t.Errorf("snapshot reports scope %q, want %q", snapshot.Scope, test.scope)
			}
		})
	}
}

// The shape travels with the selection, so a renderer does not have to re-derive
// it and a fork is describable rather than refused.
func TestAForkedSelectionCarriesItsEdges(t *testing.T) {
	parents := map[string]string{
		"synthetic-trunk": "",
		"synthetic-b":     "synthetic-trunk",
		"synthetic-c":     "synthetic-b",
		"synthetic-d":     "synthetic-b",
	}
	snapshot, err := Resolve(
		context.Background(),
		parityGit{branches: []string{"synthetic-trunk", "synthetic-b", "synthetic-c", "synthetic-d"}},
		parityGraphite{forest: graphite.Forest{Parents: parents, Roots: []string{"synthetic-trunk"}}},
		Selection{Scope: ScopeStack},
		"synthetic command",
	)
	if err != nil {
		t.Fatalf("Resolve() error = %v; a branch with two children is the ordinary shape", err)
	}
	for branch, want := range map[string]string{"synthetic-c": "synthetic-b", "synthetic-d": "synthetic-b"} {
		if got := snapshot.Parents[branch]; got != want {
			t.Errorf("parent of %q = %q, want %q", branch, got, want)
		}
	}
}
