// Package align keeps g2g's graph and Graphite's in step.
//
// Source resolution decided which source answers for a branch and left them
// free to disagree. This is the other half, in both directions: mirror makes
// Graphite agree with g2g, import adopts what Graphite declares. Neither ever
// removes a branch from the g2g graph — alignment is not ownership transfer.
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
)

// Graphite is the whole Graphite surface alignment needs. It is an interface
// rather than the client so the decision matrix can be tested without spawning
// a process per case; one path per command still runs against a real adapter.
type Graphite interface {
	ReadForest(ctx context.Context) (graphite.Forest, error)
	Track(ctx context.Context, branch, parent string) error
	Untrack(ctx context.Context, branch string) error
}

// Service compares the two graphs and reconciles one into the other.
//
// The g2g side is three narrow dependencies rather than the whole
// graph.Service, because alignment never asks that service to do anything — it
// reads ancestry, reads and writes the store, and pins a fork point. Naming
// them is what makes that legible from the type instead of from every call
// site. restack embeds the whole service because it genuinely calls Discover.
type Service struct {
	Git      graph.Ancestry
	Store    graph.Store
	Refs     graph.Pinner
	Graphite Graphite
	// Configured reports whether this repository already uses Graphite.
	//
	// Alignment is the one place with a standing excuse to skip this check —
	// the user asked for Graphite to be written, which is consent. It is asked
	// anyway, and the reason is that a *preview* must not enrol anyone: reading
	// Graphite's forest runs its discovery command, which creates state in a
	// repository that has never used it. A repository with no Graphite also has
	// no trunk, so a mirror into it would be blocked for want of a root in any
	// case. Refusing first reaches the same answer without the side effect, and
	// keeps "no g2g command enrols a repository" true without exception.
	Configured func(ctx context.Context) (bool, error)
}

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
	// Blocked is why an apply would refuse, empty when it would proceed.
	Blocked string
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
		plan.Blocked = "Graphite does not track every root of the g2g graph, and cannot be told to without being given a parent"
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

func (s Service) both(ctx context.Context) (graph.Graph, graphite.Forest, error) {
	if s.Store == nil || s.Graphite == nil {
		return graph.Graph{}, graphite.Forest{}, fmt.Errorf("alignment service is not fully configured")
	}
	if err := s.requireGraphite(ctx); err != nil {
		return graph.Graph{}, graphite.Forest{}, err
	}
	adopted, err := s.Store.Load(ctx)
	if err != nil {
		return graph.Graph{}, graphite.Forest{}, err
	}
	forest, err := s.Graphite.ReadForest(ctx)
	if err != nil {
		return graph.Graph{}, graphite.Forest{}, err
	}
	return adopted, forest, nil
}

// requireGraphite refuses before anything reads Graphite, so a preview in a
// repository that has never used it stays a preview.
func (s Service) requireGraphite(ctx context.Context) error {
	if s.Configured == nil {
		return nil
	}
	configured, err := s.Configured(ctx)
	if err != nil {
		return err
	}
	if !configured {
		return fmt.Errorf("this repository does not use Graphite · there is nothing to align, and asking Graphite would enrol it")
	}
	return nil
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
	queue := adopted.Roots()
	for len(queue) != 0 {
		branch := queue[0]
		queue = append(queue[1:], adopted.Children(branch)...)
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
	seen := map[string]bool{}
	steps := 0
	for parent := forest.Parents[branch]; parent != "" && !seen[parent]; parent = forest.Parents[parent] {
		seen[parent] = true
		steps++
	}
	return steps
}
