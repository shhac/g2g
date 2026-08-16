package align

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/shhac/gt2gh/internal/diagnostic"
	"github.com/shhac/gt2gh/internal/graph"
	"github.com/shhac/gt2gh/internal/graphite"
)

// Adoption is one edge an import would record in the gt2gh graph.
type Adoption struct {
	Branch string
	Parent string
	// ForkPoint is the parent's tip, resolved now. Graphite has no equivalent
	// field, so importing does not copy this — it manufactures the one thing
	// that makes a restack possible, and it cannot be recovered later.
	ForkPoint string
}

// Conflict is a branch both graphs describe, differently.
type Conflict struct {
	Branch string
	// Ours is the parent the gt2gh graph records; Theirs is Graphite's.
	Ours   string
	Theirs string
}

// ImportPlan is what an import would adopt.
type ImportPlan struct {
	// Adopt is ordered roots first, so a parent is recorded before the branches
	// that name it.
	Adopt []Adoption
	// Conflicts are branches the gt2gh graph already records under a different
	// parent. They block: overwriting them would silently undo a deliberate
	// gt2gh change, which is the one thing an additive command must not do.
	Conflicts []Conflict
	// Agreed is what both graphs already say the same thing about.
	Agreed []string
	// NewTrunks are parents about to become roots of the gt2gh forest.
	NewTrunks []string
	Updated   graph.Graph
	Blocked   string
}

// Claims returns the branches this import would start answering for. Adoption
// is the authority claim, so this is the list that matters most in a preview:
// afterwards gt2gh decides for every one of them, and --from graphite is the
// only way to see Graphite's view again.
func (p ImportPlan) Claims() []string {
	names := make([]string, 0, len(p.Adopt))
	for _, adoption := range p.Adopt {
		names = append(names, adoption.Branch)
	}
	return names
}

// PlanImport works out what Graphite declares that the gt2gh graph does not.
//
// It is additive by construction. Graphite declares each parent, so this is not
// the guess `track` refuses to make — but a branch gt2gh already records
// differently is a disagreement, not a gap, and gets refused rather than
// resolved.
func (s Service) PlanImport(ctx context.Context) (ImportPlan, error) {
	adopted, forest, err := s.both(ctx)
	if err != nil {
		return ImportPlan{}, err
	}
	local, err := s.Graph.Git.LocalBranches(ctx)
	if err != nil {
		return ImportPlan{}, err
	}

	plan := ImportPlan{Updated: adopted}
	for _, branch := range declaredOrder(forest) {
		parent := forest.Parents[branch]
		switch {
		case parent == "":
			// A Graphite root has no edge to import. It becomes a gt2gh trunk
			// only if something is adopted onto it.
		case !slices.Contains(local, branch) || !slices.Contains(local, parent):
			// Graphite can name a branch this checkout does not have.
		case adopted.Tracked(branch) && adopted.Edges[branch].Parent == parent:
			plan.Agreed = append(plan.Agreed, branch)
		case adopted.Tracked(branch):
			plan.Conflicts = append(plan.Conflicts, Conflict{Branch: branch, Ours: adopted.Edges[branch].Parent, Theirs: parent})
		default:
			plan.Adopt = append(plan.Adopt, Adoption{Branch: branch, Parent: parent})
		}
	}
	if len(plan.Conflicts) != 0 {
		plan.Blocked = fmt.Sprintf("the gt2gh graph already records %s under a different parent · untrack %s to take Graphite's answer, or leave it as it is",
			branchList(conflicting(plan.Conflicts)), pluralThem(conflicting(plan.Conflicts)))
		return plan, nil
	}
	if plan.Updated, plan.NewTrunks, err = s.adopt(ctx, adopted, plan.Adopt); err != nil {
		return ImportPlan{}, err
	}
	diagnostic.Event(ctx, "import.plan",
		diagnostic.Field{Key: "adopt", Value: strings.Join(plan.Claims(), ",")},
		diagnostic.Field{Key: "agreed", Value: strings.Join(plan.Agreed, ",")},
		diagnostic.Field{Key: "conflicts", Value: strings.Join(conflicting(plan.Conflicts), ",")},
	)
	return plan, nil
}

// adopt builds the resulting graph, resolving a fork point per edge as it goes.
// The adoptions are mutated in place so the plan carries the same fork points
// the write will use, rather than resolving them twice and hoping they agree.
func (s Service) adopt(ctx context.Context, adopted graph.Graph, adoptions []Adoption) (graph.Graph, []string, error) {
	updated := adopted
	trunks := make([]string, 0)
	for index, adoption := range adoptions {
		forkPoint, err := s.Graph.Git.Resolve(ctx, adoption.Parent)
		if err != nil {
			return graph.Graph{}, nil, err
		}
		adoptions[index].ForkPoint = forkPoint
		// Origin records how far Git agrees with the edge, not which tool
		// supplied it, so an imported edge is assessed exactly as a tracked one
		// is. Graphite declaring a relationship does not make the commits line
		// up, and that difference is worth keeping visible.
		confirmed, err := s.Graph.Git.IsAncestor(ctx, adoption.Parent, adoption.Branch)
		if err != nil {
			return graph.Graph{}, nil, err
		}
		next, err := updated.Track(adoption.Branch, graph.Edge{
			Parent:    adoption.Parent,
			Origin:    originFor(confirmed),
			ForkPoint: forkPoint,
		})
		if err != nil {
			return graph.Graph{}, nil, err
		}
		if !updated.Tracked(adoption.Parent) && !updated.IsTrunk(adoption.Parent) {
			trunks = append(trunks, adoption.Parent)
			next = next.WithTrunks(append(slices.Clone(updated.Trunks), adoption.Parent)...)
		}
		updated = next
	}
	sort.Strings(trunks)
	return updated, trunks, nil
}

// ApplyImport writes the adopted graph and pins each fork point.
//
// Nothing is removed and nothing is written to Graphite. Graphite keeps
// tracking every branch it tracked; the only change is which record gt2gh reads
// when asked about them.
func (s Service) ApplyImport(ctx context.Context, plan ImportPlan) error {
	if plan.Blocked != "" {
		return fmt.Errorf("cannot import: %s", plan.Blocked)
	}
	if len(plan.Adopt) == 0 {
		return nil
	}
	if err := s.Graph.Store.Save(ctx, plan.Updated); err != nil {
		return err
	}
	if s.Graph.Refs == nil {
		return nil
	}
	for _, adoption := range plan.Adopt {
		if err := s.Graph.Refs.PinForkPoint(ctx, adoption.Branch, adoption.ForkPoint); err != nil {
			return err
		}
	}
	return nil
}

// RevalidateImport recomputes immediately before the write.
func (s Service) RevalidateImport(ctx context.Context, preview ImportPlan) (ImportPlan, error) {
	current, err := s.PlanImport(ctx)
	if err != nil {
		return ImportPlan{}, err
	}
	if !current.Equal(preview) {
		return ImportPlan{}, fmt.Errorf("the graphs changed between preview and apply · rerun to see the current plan")
	}
	return current, nil
}

// Equal compares everything that changes what the write does.
func (p ImportPlan) Equal(other ImportPlan) bool {
	if p.Blocked != other.Blocked || len(p.Adopt) != len(other.Adopt) || len(p.Conflicts) != len(other.Conflicts) {
		return false
	}
	for index, adoption := range p.Adopt {
		if adoption != other.Adopt[index] {
			return false
		}
	}
	for index, conflict := range p.Conflicts {
		if conflict != other.Conflicts[index] {
			return false
		}
	}
	return equalStrings(p.Agreed, other.Agreed) && p.Updated.Equal(other.Updated)
}

// declaredOrder walks Graphite's forest from its roots down, so a parent is
// always considered before the branches that name it.
func declaredOrder(forest graphite.Forest) []string {
	ordered := make([]string, 0, len(forest.Parents))
	queue := append([]string(nil), forest.Roots...)
	sort.Strings(queue)
	seen := map[string]bool{}
	for len(queue) != 0 {
		branch := queue[0]
		queue = queue[1:]
		if seen[branch] {
			continue
		}
		seen[branch] = true
		ordered = append(ordered, branch)
		queue = append(queue, forest.Children(branch)...)
	}
	// A forest whose display named no root still has branches worth reporting.
	for _, branch := range forest.Branches() {
		if !seen[branch] {
			ordered = append(ordered, branch)
		}
	}
	return ordered
}

func originFor(confirmed bool) graph.Origin {
	if confirmed {
		return graph.OriginAncestry
	}
	return graph.OriginUser
}

func conflicting(conflicts []Conflict) []string {
	names := make([]string, 0, len(conflicts))
	for _, conflict := range conflicts {
		names = append(names, conflict.Branch)
	}
	return names
}
