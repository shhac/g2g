package land

import (
	"context"
	"testing"

	"github.com/shhac/g2g/internal/githubstack"
	"github.com/shhac/g2g/internal/graph"
	"github.com/shhac/g2g/internal/shape"
	"github.com/shhac/g2g/internal/stack"
)

// world is main <- one <- two, both published, both with an open pull request
// sitting where a correctly stacked one sits: #41 on the trunk, #42 on one.
type world struct {
	service Service
	events  *events
	git     *fakeGit
	github  *fakeGitHub
	pusher  *fakePusher
	syncer  *fakeSyncer
	store   *memoryStore
}

func newWorld(t *testing.T) *world {
	t.Helper()
	seen := &events{}
	one := githubstack.PullRequest{Number: 41, URL: "https://example.test/41", Head: "synthetic-one", HeadOID: "one-tip", Base: "synthetic-main", State: "OPEN"}
	two := githubstack.PullRequest{Number: 42, URL: "https://example.test/42", Head: "synthetic-two", HeadOID: "two-tip", Base: "synthetic-one", State: "OPEN"}

	git := &fakeGit{
		events:  seen,
		current: "synthetic-two",
		local:   []string{"synthetic-main", "synthetic-one", "synthetic-two"},
		tips:    map[string]string{"synthetic-one": "one-tip", "synthetic-two": "two-tip"},
		objects: map[string]string{"synthetic-main": "main-tip", "synthetic-one": "one-tip", "synthetic-two": "two-tip"},
		ancestors: map[string][]string{
			"refs/g2g/remotes/origin/synthetic-main": {"merge-41", "merge-42"},
		},
		absorbed: map[string]bool{},
	}
	github := &fakeGitHub{
		events: seen,
		prs:    []githubstack.PullRequest{one, two},
		states: map[int]githubstack.MergeState{
			41: {Number: 41, HeadOID: "one-tip", Base: "synthetic-main", State: "OPEN", Mergeable: "MERGEABLE", StateStatus: "CLEAN"},
			42: {Number: 42, HeadOID: "two-tip", Base: "synthetic-one", State: "OPEN", Mergeable: "MERGEABLE", StateStatus: "CLEAN"},
		},
		allowed: githubstack.Allowed{Squash: true, Merge: true, Rebase: true},
	}
	store := &memoryStore{graph: graph.Graph{
		Edges: map[string]graph.Edge{
			"synthetic-one": {Parent: "synthetic-main", ForkPoint: "main-tip"},
			"synthetic-two": {Parent: "synthetic-one", ForkPoint: "one-tip"},
		},
		Trunks: []string{"synthetic-main"},
	}}
	pusher := &fakePusher{events: seen, git: git}
	sync := &fakeSyncer{events: seen}

	return &world{
		events: seen, git: git, github: github, pusher: pusher, syncer: sync, store: store,
		service: Service{
			Git:   git,
			Graph: graph.Service{Git: nil, Store: store},
			Selector: fakeSelector{snapshot: stack.Snapshot{
				Target:       "synthetic-two",
				TargetSource: "current Git branch",
				Ancestry:     []string{"synthetic-main", "synthetic-one", "synthetic-two"},
				Base:         "synthetic-main",
				Branches:     []string{"synthetic-one", "synthetic-two"},
				Scope:        shape.ScopeStack,
				Source:       stack.SourceG2G,
			}},
			GitHub: github,
			Pusher: pusher,
			Syncer: sync,
			Pruner: &fakePruner{events: seen, store: store},
			pause:  instant,
		},
	}
}

func (w *world) plan(t *testing.T, options Options) Plan {
	t.Helper()
	plan, err := w.service.Plan(context.Background(), stack.Selection{Scope: shape.ScopeStack}, options)
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	return plan
}

// landingTheBottom selects the path to synthetic-one alone, so synthetic-two
// is left above the descent: replayed by every sync and landed by none.
func landingTheBottom(w *world) {
	w.service.Selector = fakeSelector{snapshot: stack.Snapshot{
		Target:       "synthetic-one",
		TargetSource: "--branch",
		Ancestry:     []string{"synthetic-main", "synthetic-one"},
		Base:         "synthetic-main",
		Branches:     []string{"synthetic-one"},
		Scope:        shape.ScopePath,
		Source:       stack.SourceG2G,
	}}
}
