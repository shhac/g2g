package cli

import (
	"testing"

	localgit "github.com/shhac/g2g/internal/git"
	"github.com/shhac/g2g/internal/restack"
	"github.com/shhac/g2g/internal/shape"
)

// A suggestion reaches what the command acted on: the branch when it was
// named, and the scope when the suggested command's default would not cover
// it. Otherwise it is the bare command, as the reader would type it.
func TestSuggestionsAimAtWhatWasSelected(t *testing.T) {
	for _, test := range []struct {
		name  string
		acted selected
		want  string
	}{
		{name: "standing on it", acted: selected{branch: "synthetic-a", scope: shape.ScopeSubtree}, want: "g2g push"},
		{name: "named", acted: selected{branch: "synthetic-a", named: true, scope: shape.ScopeStack}, want: "g2g push --branch synthetic-a"},
		{name: "narrower than the default", acted: selected{branch: "synthetic-a", named: true, scope: shape.ScopePath}, want: "g2g push --branch synthetic-a"},
		{name: "no scope", acted: selected{branch: "synthetic-a"}, want: "g2g push"},
		{name: "wider than push takes", acted: selected{branch: "synthetic-a", scope: shape.ScopeTrunk}, want: "g2g status --scope trunk"},
		{name: "wider, named", acted: selected{branch: "synthetic-a", named: true, scope: shape.ScopeTrunk}, want: "g2g status --branch synthetic-a --scope trunk"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := test.acted.next(pushCommand); got != test.want {
				t.Errorf("next = %q, want %q", got, test.want)
			}
		})
	}
	if got := (selected{branch: "synthetic-a", scope: shape.ScopeTrunk}).next(pruneCommand); got != "g2g prune --scope trunk" {
		t.Errorf("prune over a trunk = %q, want it to name the scope it offers", got)
	}
}

// A suggestion compares with the remote the command did, since --remote names
// where a stack's branches are published for pull and push alike. Only a
// command that takes --remote is told it, and the default goes unnamed.
func TestSuggestionsCarryTheRemoteToCommandsThatTakeOne(t *testing.T) {
	upstream := selected{branch: "synthetic-a", scope: shape.ScopeStack}.from("synthetic-upstream")
	for _, test := range []struct {
		name   string
		acted  selected
		target suggestable
		want   string
	}{
		{name: "push", acted: upstream, target: pushCommand, want: "g2g push --remote synthetic-upstream"},
		{name: "prune takes none", acted: upstream, target: pruneCommand, want: "g2g prune"},
		{name: "the default", acted: upstream.from(localgit.DefaultRemote), target: pushCommand, want: "g2g push"},
		{name: "status in push's place", acted: selected{branch: "synthetic-a", named: true, scope: shape.ScopeTrunk}.from("synthetic-upstream"), target: pushCommand, want: "g2g status --branch synthetic-a --scope trunk --remote synthetic-upstream"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := test.acted.next(test.target); got != test.want {
				t.Errorf("next = %q, want %q", got, test.want)
			}
		})
	}
}

// pull and pull --prune follow the same pull, so they name the same remote:
// the replay's suggestion and the prune's both carry it.
func TestPullAndItsPruneSuggestTheSameRemote(t *testing.T) {
	replayed := restack.Plan{Steps: []restack.Step{{Branch: "synthetic-a"}}}
	replayed.Discovery.Target, replayed.Discovery.Scope = "synthetic-a", shape.ScopeStack
	if got := replayNext(replayed, "synthetic-upstream"); got != "g2g push --remote synthetic-upstream" {
		t.Errorf("after pull = %q", got)
	}
	pruned := prunePlan{remote: "synthetic-upstream", unpublished: true}
	pruned.Discovery = replayed.Discovery
	if got := pruneNext(pruned); got != "g2g push --remote synthetic-upstream" {
		t.Errorf("after pull --prune = %q", got)
	}
}
