package align

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/shhac/g2g/internal/diagnostic"
	"github.com/shhac/g2g/internal/graph"
	"github.com/shhac/g2g/internal/repair"
)

// Adoption is one edge an adoption would record in the g2g graph.
type Adoption struct {
	Branch string
	Parent string
	// ForkPoint is where the branch's own work begins, resolved now: the
	// parent's tip for Graphite, the merge base for a pull request. Neither
	// record has an equivalent field, so adopting does not copy this — it
	// manufactures the one thing that makes a restack possible, and it cannot
	// be recovered later.
	ForkPoint string
}

// Conflict is a branch both graphs describe, differently.
type Conflict struct {
	Branch string
	// Ours is the parent the g2g graph records; Theirs is the other record's.
	Ours   string
	Theirs string
	// Declared means g2g records no parent because somebody named the branch a
	// trunk, and Ours is empty. Adopting the other record's edge would end the
	// declaration without a word, which only track --parent may do.
	Declared bool
}

// The records an adoption can read. They are the resolver's own source names, so
// a flag, a preview and the JSON output all use one word for each.
const (
	FromGraphite = "graphite"
	FromGitHub   = "github"
)

// AdoptPlan is what an adoption would adopt.
type AdoptPlan struct {
	// From names the record the edges were read from.
	From string
	// Adopt is ordered roots first, so a parent is recorded before the branches
	// that name it.
	Adopt []Adoption
	// Conflicts are branches the g2g graph already records under a different
	// parent. They block: overwriting them would silently undo a deliberate
	// g2g change, which is the one thing an additive command must not do.
	Conflicts []Conflict
	// Agreed is what both graphs already say the same thing about.
	Agreed []string
	// NewTrunks are parents about to become roots of the g2g forest.
	NewTrunks []string
	// Unconfirmed are adopted branches whose parent is not an ancestor, so
	// they will read as needing a restack. Only an adoption from pull requests
	// fills it: its fork point is the merge base, which a restack can replay
	// from, so saying "needs a restack" is true there.
	Unconfirmed []string
	Updated     graph.Graph
	Blocked     string
	// Repair is Blocked in the shape a caller can lay out.
	Repair repair.Note
}

// Claims returns the branches this adoption would start answering for. Adoption
// is the authority claim, so this is the list that matters most in a preview:
// afterwards g2g decides for every one of them, and --from on a read is the
// only way to see the other record's view again.
func (p AdoptPlan) Claims() []string {
	names := make([]string, 0, len(p.Adopt))
	for _, adoption := range p.Adopt {
		names = append(names, adoption.Branch)
	}
	return names
}

// record is what differs between the sources an adoption reads. Everything else
// — the additive rule, the conflict refusal, parents before children, how Git
// assesses an edge — is one policy, applied once whichever record declared
// the edges.
type record struct {
	from string
	// answer is how a conflict's repair names taking this record's side.
	answer string
	// forkPoint says where an adopted branch's own work begins.
	forkPoint func(ctx context.Context, adoption Adoption) (string, error)
}

// planAdoptions classifies declared edges and builds the graph adopting them
// would leave, refusing the whole plan on any disagreement.
func (s Service) planAdoptions(ctx context.Context, adopted graph.Graph, declared []Adoption, local []string, source record) (AdoptPlan, error) {
	plan := classify(adopted, declared, local)
	plan.From = source.from
	plan.Updated = adopted
	if declared := declaredConflicts(plan.Conflicts); len(declared) != 0 {
		plan.Repair = graph.DeclaredConflict(declared)
		plan.Blocked = plan.Repair.Sentence()
		return plan, nil
	}
	if len(plan.Conflicts) != 0 {
		plan.Repair = repair.Note{
			Reason: "the g2g graph already records a different parent",
			Ways: []repair.Step{
				{Command: "g2g untrack", Effect: "drop the recorded parent and " + source.answer},
				{Effect: "leave it as it is"},
			},
		}
		plan.Blocked = plan.Repair.Sentence()
		return plan, nil
	}
	updated, trunks, err := s.adopt(ctx, adopted, plan.Adopt, source.forkPoint)
	if err != nil {
		return AdoptPlan{}, err
	}
	plan.Updated, plan.NewTrunks = updated, trunks
	diagnostic.Event(ctx, "adopt.plan",
		diagnostic.Field{Key: "from", Value: source.from},
		diagnostic.Field{Key: "adopt", Value: strings.Join(plan.Claims(), ",")},
		diagnostic.Field{Key: "agreed", Value: strings.Join(plan.Agreed, ",")},
		diagnostic.Field{Key: "conflicts", Value: strings.Join(conflicting(plan.Conflicts), ",")},
	)
	return plan, nil
}

// classify sorts every declared edge into adopt, conflict, or agreed. The
// edges arrive parents first, and the adoptions keep that order.
//
// It is pure, like mirror's writes and strangers, so the decision matrix is
// testable from plain values with no repository, no fakes, and no Git.
func classify(adopted graph.Graph, declared []Adoption, local []string) AdoptPlan {
	plan := AdoptPlan{}
	for _, edge := range declared {
		branch, parent := edge.Branch, edge.Parent
		switch {
		case !slices.Contains(local, branch) || !slices.Contains(local, parent):
			// A record can name a branch this checkout does not have.
		default:
			switch adopted.Judge(branch, parent) {
			case graph.VerdictDeclared:
				plan.Conflicts = append(plan.Conflicts, Conflict{Branch: branch, Theirs: parent, Declared: true})
			case graph.VerdictAgreed:
				plan.Agreed = append(plan.Agreed, branch)
			case graph.VerdictDiffers:
				plan.Conflicts = append(plan.Conflicts, Conflict{Branch: branch, Ours: adopted.Edges[branch].Parent, Theirs: parent})
			default:
				plan.Adopt = append(plan.Adopt, Adoption{Branch: branch, Parent: parent})
			}
		}
	}
	return plan
}

// declaredConflicts names the conflicts that are declared trunks. Their way
// out is not untrack, which would strand what sits on them, so they get the
// refusal every adoption gives a declared trunk.
func declaredConflicts(conflicts []Conflict) []string {
	declared := make([]string, 0)
	for _, conflict := range conflicts {
		if conflict.Declared {
			declared = append(declared, conflict.Branch)
		}
	}
	return declared
}

// adopt builds the resulting graph, resolving a fork point per edge as it goes.
// The adoptions are mutated in place so the plan carries the same fork points
// the write will use, rather than resolving them twice and hoping they agree.
func (s Service) adopt(ctx context.Context, adopted graph.Graph, adoptions []Adoption, forkPointOf func(context.Context, Adoption) (string, error)) (graph.Graph, []string, error) {
	updated := adopted
	trunks := make([]string, 0)
	for index, adoption := range adoptions {
		forkPoint, err := forkPointOf(ctx, adoption)
		if err != nil {
			return graph.Graph{}, nil, err
		}
		adoptions[index].ForkPoint = forkPoint
		// Origin records how far Git agrees with the edge, not which tool
		// supplied it, so an adoptioned edge is assessed exactly as a tracked one
		// is. Another record declaring a relationship does not make the commits
		// line up, and that difference is worth keeping visible.
		confirmed, err := s.Git.IsAncestor(ctx, adoption.Parent, adoption.Branch)
		if err != nil {
			return graph.Graph{}, nil, err
		}
		recorded, promoted, err := updated.Adopt(adoption.Branch, graph.Edge{
			Parent:    adoption.Parent,
			Origin:    originFor(confirmed),
			ForkPoint: forkPoint,
		})
		if err != nil {
			return graph.Graph{}, nil, err
		}
		if promoted != "" {
			trunks = append(trunks, promoted)
		}
		updated = recorded
	}
	sort.Strings(trunks)
	return updated, trunks, nil
}

// ApplyAdopt writes the adopted graph and pins each fork point.
//
// Nothing is removed and nothing is written to Graphite. Graphite keeps
// tracking every branch it tracked; the only change is which record g2g reads
// when asked about them.
func (s Service) ApplyAdopt(ctx context.Context, plan AdoptPlan) error {
	if plan.Blocked != "" {
		return fmt.Errorf("cannot adopt: %s", plan.Blocked)
	}
	if len(plan.Adopt) == 0 {
		return nil
	}
	if err := s.Store.Save(ctx, plan.Updated); err != nil {
		return err
	}
	if s.Refs == nil {
		return nil
	}
	for _, adoption := range plan.Adopt {
		if err := s.Refs.PinForkPoint(ctx, adoption.Branch, adoption.ForkPoint); err != nil {
			return err
		}
	}
	return nil
}

// Equal compares everything that changes what the write does.
func (p AdoptPlan) Equal(other AdoptPlan) bool {
	if p.From != other.From || p.Blocked != other.Blocked || len(p.Adopt) != len(other.Adopt) || len(p.Conflicts) != len(other.Conflicts) {
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
	return slices.Equal(p.Agreed, other.Agreed) &&
		slices.Equal(p.Unconfirmed, other.Unconfirmed) &&
		p.Updated.Equal(other.Updated)
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
