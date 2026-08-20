package stack

import (
	"context"
	"fmt"
	"strings"

	"github.com/shhac/g2g/internal/diagnostic"
)

// Source names where a branch's structure came from.
type Source string

const (
	// SourceG2G is an edge the user adopted into g2g's own store. Adoption
	// is the claim, so it takes precedence over anything inferred.
	SourceG2G Source = "g2g"
	// SourceGraphite is a branch Graphite declares. g2g reads it and never
	// writes it back.
	SourceGraphite Source = "graphite"
	// SourcePullRequest is the base of a branch's open pull request. It is
	// observed rather than adopted, and it is asked for rather than assumed.
	SourcePullRequest Source = "pull-request"
)

// ReadableSources are the sources a command may be pointed at when reading a
// pull request costs it nothing it has promised not to do.
var ReadableSources = []Source{SourceG2G, SourceGraphite, SourcePullRequest}

// OfflineSources are the sources a command may be pointed at when it must not
// invoke gh. push is the case: its whole contract is one atomic git push and no
// GitHub call, and resolving through pull request bases would break that before
// selection even began.
var OfflineSources = []Source{SourceG2G, SourceGraphite}

// Permits reports whether a source is one of these.
func Permits(sources []Source, from Source) bool {
	for _, source := range sources {
		if source == from {
			return true
		}
	}
	return false
}

// TrunkEvidence reports the branch a remote calls its default. It is evidence
// and never authority: it may choose how advice is phrased, and it may never
// decide structure.
type TrunkEvidence interface {
	DefaultBranch(ctx context.Context, remote string) (string, error)
}

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
// through actions g2g never sees — tracking a branch in Graphite can join
// two previously separate trees — so the answer is derived each time and there
// is nothing to migrate or reconcile.
type Resolver struct {
	Git Git
	// Selectors are consulted in precedence order.
	Selectors []Selector
	// Trunks answers whether a branch is the repository's default, so advice
	// for a trunk can differ from advice for a branch missing its parent. It is
	// optional: without it every branch reads as the latter, which is what it
	// did before.
	Trunks TrunkEvidence
	// OnRequest are sources reachable only when the caller names one with
	// --from. They are never consulted by precedence.
	//
	// A source belongs here when asking it a question has a cost the default
	// path must not pay. Reading pull request bases means invoking gh, and push
	// must never do that — so a source that would otherwise be consulted merely
	// to resolve a branch has to be opted into instead.
	OnRequest []Selector
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
		return Snapshot{}, fmt.Errorf("%s does not describe %q · %s", selection.From, target, r.pinnedRemedy(ctx, selection.From, target))
	}
	return Snapshot{}, Undescribed{Branch: target, Trunk: r.looksLikeTrunk(ctx, target), remedy: r.remedy(ctx, target)}
}

// Undescribed is the state of a branch no source places.
//
// It is a type rather than a sentence because two commands want different
// things from it. A command that mutates has nothing to act on and must
// refuse. A read-only one has been asked what the state is, and "nothing is
// stacked here" is a complete answer to that — refusing to say it is how
// status came to exit non-zero on a repository that is simply not stacked yet,
// while graph rendered the same fact happily.
type Undescribed struct {
	Branch string
	// Trunk reports that this branch is where stacks would start rather than a
	// branch missing its parent. Nothing sits under a trunk by definition, so
	// having no recorded parent is its ordinary state and not a gap to fill.
	Trunk  bool
	remedy string
}

func (e Undescribed) Error() string {
	return fmt.Sprintf("no source describes %q · %s", e.Branch, e.remedy)
}

// looksLikeTrunk asks what the remote calls its default branch.
//
// Not knowing is an ordinary answer and so is a failure to ask: the whole value
// of this is choosing a better sentence, so anything that goes wrong leaves the
// sentence as it was.
func (r Resolver) looksLikeTrunk(ctx context.Context, branch string) bool {
	if r.Trunks == nil {
		return false
	}
	def, err := r.Trunks.DefaultBranch(ctx, "")
	return err == nil && def != "" && def == branch
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
	for _, selector := range append(append([]Selector{}, r.Selectors...), r.OnRequest...) {
		if selector.Source() == from {
			return []Selector{selector}, nil
		}
	}
	return nil, fmt.Errorf("unknown source %q · this build has %s", from, strings.Join(r.sources(), ", "))
}

// sources lists the sources this build was given, in precedence order.
func (r Resolver) sources() []string {
	names := make([]string, 0, len(r.Selectors)+len(r.OnRequest))
	for _, selector := range append(append([]Selector{}, r.Selectors...), r.OnRequest...) {
		names = append(names, string(selector.Source()))
	}
	return names
}

// remedy names what to do about a branch nothing describes, listing only the
// sources this build was actually given.
//
// A trunk gets different advice, because the usual advice is wrong there:
// telling someone to record a parent for the branch their stacks start from
// asks them to break the one rule the graph has, and the command named would
// refuse anyway.
func (r Resolver) remedy(ctx context.Context, branch string) string {
	if r.looksLikeTrunk(ctx, branch) {
		return fmt.Sprintf("it is this repository's default branch, so nothing is stacked on it yet · start one with g2g track --branch <child> --parent %s", branch)
	}
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

// pinnedRemedy answers a --from that did not match.
//
// "rerun without --from to let any source answer" is advice worth giving only
// when another source might: in a repository with no pull requests at all, the
// reader has just been sent to try something that fails the same way.
func (r Resolver) pinnedRemedy(ctx context.Context, from Source, branch string) string {
	for _, selector := range r.Selectors {
		describes, err := selector.Describes(ctx, branch)
		if err == nil && describes {
			return "rerun without --from to let any source answer"
		}
	}
	if r.looksLikeTrunk(ctx, branch) {
		return fmt.Sprintf("nothing is stacked on it yet, and no other source describes it either · start one with g2g track --branch <child> --parent %s", branch)
	}
	return "and no other source describes it either · " + r.remedy(ctx, branch)
}
