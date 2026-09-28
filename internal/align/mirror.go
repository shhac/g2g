package align

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/shhac/g2g/internal/diagnostic"
	"github.com/shhac/g2g/internal/graph"
	"github.com/shhac/g2g/internal/graphite"
	"github.com/shhac/g2g/internal/repair"
)

// Change is one edge a mirror would write.
type Change struct {
	Branch string
	Parent string
	// Tracked reports whether Graphite already knows the branch, which is what
	// separates adding an edge from moving one.
	Tracked bool
	// Was is Graphite's current parent, meaningful only when Tracked.
	Was string
}

// Moves reports whether this change moves an edge Graphite already had.
func (c Change) Moves() bool { return c.Tracked }

// MirrorPlan is what a mirror would do, in the order it would do it.
type MirrorPlan struct {
	// Writes are ordered parents before children, because Graphite refuses a
	// parent it does not already track.
	Writes []Change
	// Strangers is what Graphite tracks that the g2g graph says nothing
	// about. It is filled in whether or not --prune was asked for: a branch
	// this command could remove is worth seeing before it can remove it.
	Strangers []string
	// Prunes are the strangers being removed, ordered deepest first because
	// untracking cascades. Empty unless --prune was asked for.
	Prunes []string
	// UnknownRoots are the roots of the g2g forest Graphite has never heard
	// of. They are what Blocked is about when it is set.
	UnknownRoots []string
	// Blocked is why an apply would refuse, empty when it would proceed. It
	// is Repair's sentence.
	Blocked string
	// Repair is the refusal in parts: why, and the ways out.
	Repair repair.Note
}

// Shielded returns the strangers a prune leaves alone because untracking them
// would cascade into branches g2g does know. It is meaningful only when a
// prune was asked for; without one, every stranger is simply untouched.
func (p MirrorPlan) Shielded() []string {
	pruning := map[string]bool{}
	for _, branch := range p.Prunes {
		pruning[branch] = true
	}
	shielded := make([]string, 0)
	for _, branch := range p.Strangers {
		if !pruning[branch] {
			shielded = append(shielded, branch)
		}
	}
	return shielded
}

// Aligned reports a plan with nothing to do.
func (p MirrorPlan) Aligned() bool { return len(p.Writes) == 0 && len(p.Prunes) == 0 }

// Added and Moved split the writes for presentation only; they are applied as
// one ordered sequence because the ordering constraint spans both.
func (p MirrorPlan) Added() []string { return p.branches(false) }
func (p MirrorPlan) Moved() []string { return p.branches(true) }

func (p MirrorPlan) branches(moves bool) []string {
	names := make([]string, 0, len(p.Writes))
	for _, write := range p.Writes {
		if write.Moves() == moves {
			names = append(names, write.Branch)
		}
	}
	return names
}

// PlanMirror works out how to make Graphite agree with the g2g graph, without
// performing any of it.
//
// The comparison is over the whole forest rather than one path. A path is what
// a projection consumes; alignment is about the record as a whole, and mirroring
// only the branch you happen to be on would leave the rest quietly wrong.
func (s Service) PlanMirror(ctx context.Context, prune bool) (MirrorPlan, error) {
	adopted, forest, err := s.both(ctx)
	if err != nil {
		return MirrorPlan{}, err
	}

	plan := MirrorPlan{}
	// The list is carried rather than rendered. Every other branch list a user
	// reads is composed in the presentation layer, and composing this one here
	// meant one preview printed two different phrasings of the same idea.
	if plan.UnknownRoots = unknownRoots(adopted, forest); len(plan.UnknownRoots) != 0 {
		plan.Repair = repair.Note{
			Reason: "Graphite does not track " + strings.Join(plan.UnknownRoots, ", ") + ", a root of the g2g graph, and cannot be told to without being given a parent",
			Ways: []repair.Step{
				{Effect: "track it in Graphite first"},
				{Command: "gt init", Effect: "give Graphite a trunk, if it has none"},
			},
		}
		plan.Blocked = plan.Repair.Sentence()
		return plan, nil
	}
	plan.Writes = writes(adopted, forest)
	plan.Strangers = strangers(adopted, forest)
	if prune {
		plan.Prunes = prunable(plan.Strangers, forest)
	}
	diagnostic.Event(ctx, "mirror.plan",
		diagnostic.Field{Key: "add", Value: strings.Join(plan.Added(), ",")},
		diagnostic.Field{Key: "move", Value: strings.Join(plan.Moved(), ",")},
		diagnostic.Field{Key: "prune", Value: strings.Join(plan.Prunes, ",")},
		diagnostic.Field{Key: "shielded", Value: strings.Join(plan.Shielded(), ",")},
	)
	return plan, nil
}

// ApplyMirror writes the plan in the order it was computed and stops at the
// first step that cannot finish, reporting how far it got.
//
// It does not unwind. A half-aligned Graphite is closer to correct than the
// state it started in, and re-running is how the rest gets done.
func (s Service) ApplyMirror(ctx context.Context, plan MirrorPlan) error {
	if plan.Blocked != "" {
		return fmt.Errorf("cannot mirror: %s", plan.Blocked)
	}
	for _, write := range plan.Writes {
		if err := s.Graphite.Track(ctx, write.Branch, write.Parent); err != nil {
			return err
		}
	}
	for _, branch := range plan.Prunes {
		if err := s.Graphite.Untrack(ctx, branch); err != nil {
			return err
		}
	}
	return nil
}

// RevalidateMirror recomputes immediately before the write, so a graph that
// moved between preview and apply is caught rather than acted on.
func (s Service) RevalidateMirror(ctx context.Context, prune bool, preview MirrorPlan) (MirrorPlan, error) {
	current, err := s.PlanMirror(ctx, prune)
	if err != nil {
		return MirrorPlan{}, err
	}
	if err := diagnostic.Revalidated(ctx, "mirror", "the graphs", current.Equal(preview)); err != nil {
		return MirrorPlan{}, err
	}
	return current, nil
}

// Equal compares everything that changes what the write does.
func (p MirrorPlan) Equal(other MirrorPlan) bool {
	if p.Blocked != other.Blocked || len(p.Writes) != len(other.Writes) {
		return false
	}
	for index, write := range p.Writes {
		if write != other.Writes[index] {
			return false
		}
	}
	return slices.Equal(p.Prunes, other.Prunes) &&
		slices.Equal(p.Strangers, other.Strangers) &&
		slices.Equal(p.UnknownRoots, other.UnknownRoots)
}

// unknownRoots names the roots of the g2g forest that Graphite has never
// heard of. Graphite can only track a branch under a parent it already tracks,
// so a root it does not know cannot be created — only `gt init` establishes a
// trunk, and enrolling a repository is not this command's business.
func unknownRoots(adopted graph.Graph, forest graphite.Forest) []string {
	missing := make([]string, 0)
	for _, root := range adopted.Roots() {
		if _, known := forest.Parents[root]; !known {
			missing = append(missing, root)
		}
	}
	sort.Strings(missing)
	return missing
}

// writes walks the g2g forest from its roots down, so every parent is written
// before the children that name it.
func writes(adopted graph.Graph, forest graphite.Forest) []Change {
	changes := make([]Change, 0)
	for _, branch := range adopted.Shape().BreadthFirst(adopted.Roots()) {
		edge, tracked := adopted.Edges[branch]
		if !tracked {
			continue
		}
		was, known := forest.Parents[branch]
		if known && was == edge.Parent {
			continue
		}
		changes = append(changes, Change{Branch: branch, Parent: edge.Parent, Tracked: known, Was: was})
	}
	return changes
}

// strangers are the branches Graphite tracks that the g2g graph says nothing
// about. Trunks and roots count as known: they anchor the forest even though no
// edge records them.
func strangers(adopted graph.Graph, forest graphite.Forest) []string {
	known := map[string]bool{}
	for _, branch := range adopted.Branches() {
		known[branch] = true
	}
	for _, root := range adopted.Roots() {
		known[root] = true
	}
	for _, trunk := range adopted.Trunks {
		known[trunk] = true
	}
	unknown := make([]string, 0)
	for _, branch := range forest.Branches() {
		if !known[branch] && forest.Parents[branch] != "" {
			unknown = append(unknown, branch)
		}
	}
	return unknown
}

// prunable decides which strangers can be removed safely.
//
// Untracking cascades to a branch's whole subtree, so removing one stranger
// whose child g2g does know would silently untrack the branch the mirror just
// aligned. A stranger with a surviving child is therefore kept, not pruned, and
// the rest are ordered deepest first so a parent never takes its children with
// it.
func prunable(candidates []string, forest graphite.Forest) []string {
	removing := map[string]bool{}
	for _, branch := range candidates {
		removing[branch] = true
	}
	prunes := make([]string, 0)
	for _, branch := range candidates {
		if keepsAChild(branch, forest, removing) {
			continue
		}
		prunes = append(prunes, branch)
	}
	sort.SliceStable(prunes, func(left, right int) bool {
		return depth(prunes[left], forest) > depth(prunes[right], forest)
	})
	return prunes
}

// keepsAChild reports whether untracking branch would take a branch with it
// that is not itself being removed.
func keepsAChild(branch string, forest graphite.Forest, removing map[string]bool) bool {
	for _, child := range forest.Children(branch) {
		if !removing[child] {
			return true
		}
	}
	return false
}

func depth(branch string, forest graphite.Forest) int {
	// The shared walk reports a cycle rather than stopping quietly, and a
	// forest parsed from another tool's display is exactly where one could
	// arrive. Depth is only an ordering key, so a cycle answers zero and the
	// branch sorts first, which is where a caller will notice it.
	path, err := forest.Shape().Path(branch)
	if err != nil {
		return 0
	}
	return len(path) - 1
}
