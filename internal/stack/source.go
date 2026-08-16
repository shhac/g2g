package stack

import (
	"context"
	"fmt"
	"strings"

	"github.com/shhac/gt2gh/internal/diagnostic"
)

// Source names where a branch's structure came from.
type Source string

const (
	// SourceG2G is an edge the user adopted into gt2gh's own store. Adoption
	// is the claim, so it takes precedence over anything inferred.
	SourceG2G Source = "g2g"
	// SourceGraphite is a branch Graphite declares. gt2gh reads it and never
	// writes it back.
	SourceGraphite Source = "graphite"
)

// Selector produces the ordered path a command acts on, from one source.
//
// Describes is separate from Select so precedence can be decided without
// paying for discovery, and — more importantly — without side effects. Asking
// Graphite to describe a repository that has never used it creates Graphite
// state in that repository, which is not something a question should do.
type Selector interface {
	Source() Source
	Describes(ctx context.Context, branch string) (bool, error)
	Select(ctx context.Context, selection Selection, command string) (Snapshot, error)
}

// Resolver picks the first source that describes the selected branch.
//
// Resolution is per branch and never stored. A recorded owner goes stale
// through actions gt2gh never sees — tracking a branch in Graphite can join
// two previously separate trees — so the answer is derived each time and there
// is nothing to migrate or reconcile.
type Resolver struct {
	Git Git
	// Selectors are consulted in precedence order.
	Selectors []Selector
}

// Select resolves the branch's source and delegates the whole selection to it.
//
// The target's source decides for the path, rather than each branch answering
// separately: a source that describes a branch describes its ancestors too,
// and a mutation should act on a path one source vouches for.
func (r Resolver) Select(ctx context.Context, selection Selection, command string) (Snapshot, error) {
	if r.Git == nil || len(r.Selectors) == 0 {
		return Snapshot{}, fmt.Errorf("stack resolver is not fully configured")
	}
	target, _, err := resolveTarget(ctx, r.Git, selection.Branch)
	if err != nil {
		return Snapshot{}, err
	}
	selectors, err := r.pinned(selection.From)
	if err != nil {
		return Snapshot{}, err
	}
	for _, selector := range selectors {
		describes, err := selector.Describes(ctx, target)
		if err != nil {
			return Snapshot{}, err
		}
		if !describes {
			continue
		}
		diagnostic.Event(ctx, "source.resolved", diagnostic.Field{Key: "branch", Value: target}, diagnostic.Field{Key: "source", Value: string(selector.Source())})
		snapshot, err := selector.Select(ctx, selection, command)
		if err != nil {
			return Snapshot{}, err
		}
		snapshot.Source = selector.Source()
		return snapshot, nil
	}
	if selection.From != "" {
		return Snapshot{}, fmt.Errorf("%s does not describe %q · rerun without --from to let any source answer", selection.From, target)
	}
	return Snapshot{}, fmt.Errorf("no source describes %q · %s", target, r.remedy())
}

// pinned narrows resolution to one source when the caller named it.
//
// Pinning does not force that source to answer, only to be the only one asked:
// a source that does not describe the branch still says so, and the refusal
// then names the source the caller chose rather than a precedence they did not.
func (r Resolver) pinned(from Source) ([]Selector, error) {
	if from == "" {
		return r.Selectors, nil
	}
	for _, selector := range r.Selectors {
		if selector.Source() == from {
			return []Selector{selector}, nil
		}
	}
	return nil, fmt.Errorf("unknown source %q · this build has %s", from, strings.Join(r.sources(), ", "))
}

// sources lists the sources this build was given, in precedence order.
func (r Resolver) sources() []string {
	names := make([]string, 0, len(r.Selectors))
	for _, selector := range r.Selectors {
		names = append(names, string(selector.Source()))
	}
	return names
}

// remedy names what to do about a branch nothing describes, listing only the
// sources this build was actually given.
func (r Resolver) remedy() string {
	options := make([]string, 0, len(r.Selectors))
	for _, selector := range r.Selectors {
		switch selector.Source() {
		case SourceG2G:
			options = append(options, "run g2g track to record its parent")
		case SourceGraphite:
			options = append(options, "track it in Graphite")
		}
	}
	if len(options) == 0 {
		return "no structure source is configured"
	}
	return strings.Join(options, ", or ")
}

// GraphiteSelector describes branches Graphite declares.
type GraphiteSelector struct {
	Git      Git
	Graphite Graphite
	// Configured reports whether this repository already uses Graphite. It is
	// asked before anything else, because Graphite's discovery command creates
	// state in a repository that has never used it — so a repository that
	// deliberately has no Graphite would be enrolled into it merely by being
	// asked a question.
	Configured func(ctx context.Context) (bool, error)
}

func (s GraphiteSelector) Source() Source { return SourceGraphite }

// Describes reports whether Graphite is in use here at all. Whether it knows
// this particular branch is left to Select, which is the call that has to run
// Graphite anyway.
func (s GraphiteSelector) Describes(ctx context.Context, _ string) (bool, error) {
	if s.Git == nil || s.Graphite == nil {
		return false, nil
	}
	if s.Configured == nil {
		return true, nil
	}
	return s.Configured(ctx)
}

func (s GraphiteSelector) Select(ctx context.Context, selection Selection, command string) (Snapshot, error) {
	return Resolve(ctx, s.Git, s.Graphite, selection, command)
}
