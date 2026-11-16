package restack

import (
	"bytes"
	"context"
	"errors"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/diagnostic"
	localgit "github.com/shhac/g2g/internal/git"
	"github.com/shhac/g2g/internal/worktree"
)

type agreementGit struct {
	*fakeGit
	known                    map[string]string
	knownBranches            []string
	knownCalls, historyCalls int
	recorded                 map[string]localgit.SyncPoint
	err                      error
	recordErr, historyErr    error
}

func (g *agreementGit) KnownTips(_ context.Context, remote string, branches []string) (map[string]string, error) {
	if remote != localgit.DefaultRemote {
		panic("unexpected remote")
	}
	g.knownCalls++
	g.knownBranches = slices.Clone(branches)
	return g.known, g.err
}
func (g *agreementGit) SyncHistories(_ context.Context, _ string, branches []string) (map[string][]localgit.SyncPoint, error) {
	g.historyCalls++
	return nil, g.historyErr
}
func (g *agreementGit) RecordSync(_ context.Context, _ string, branch string, point localgit.SyncPoint) error {
	if g.recordErr != nil {
		return g.recordErr
	}
	if g.recorded == nil {
		g.recorded = map[string]localgit.SyncPoint{}
	}
	g.recorded[branch] = point
	return nil
}

func TestPlanRecordsLevelBranchesBeforeReplay(t *testing.T) {
	git := &agreementGit{fakeGit: stackGit(), known: map[string]string{"synthetic-a": "a-old", "synthetic-b": "b-old", "synthetic-trunk": "trunk-new"}}
	service, _, _ := newService(git.fakeGit, stack())
	service.Git = git
	plan, err := service.Plan(context.Background(), selection(), Onto{}, false, Pending{}, "")
	if err != nil || plan.Blocked() != "" {
		t.Fatalf("Plan() = %+v, %v", plan, err)
	}
	if len(git.recorded) != 2 || git.knownCalls != 1 || git.historyCalls != 1 || !slices.Equal(git.knownBranches, []string{"synthetic-a", "synthetic-b"}) {
		t.Fatalf("recorded=%v, known=%v (%d calls), histories=%d", git.recorded, git.knownBranches, git.knownCalls, git.historyCalls)
	}
	for branch, point := range git.recorded {
		if point.Tip != git.objects[branch] || point.Local != point.Tip || point.Command != "restack" {
			t.Errorf("%s: %+v", branch, point)
		}
	}
	// Applying cannot replace the observed agreement with the replay's new tip.
	before := maps.Clone(git.recorded)
	if err := service.Apply(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	if !maps.EqualFunc(before, git.recorded, func(a, b localgit.SyncPoint) bool { return a.Tip == b.Tip && a.Local == b.Local }) {
		t.Fatalf("apply changed agreement: %v", git.recorded)
	}
}

func TestReplayAgreementRequiresExactObservedLocalTip(t *testing.T) {
	for _, test := range []struct {
		name, tip, head, known string
		absorb                 bool
		err                    error
		want                   bool
	}{
		{name: "level", tip: "synthetic-old", known: "synthetic-old", want: true},
		{name: "ahead", tip: "synthetic-ahead", known: "synthetic-old"},
		{name: "behind", tip: "synthetic-old", known: "synthetic-ahead"},
		{name: "unpublished", tip: "synthetic-old"},
		{name: "pending incoming head is not local agreement", tip: "synthetic-old", head: "synthetic-incoming", known: "synthetic-incoming"},
		{name: "local agreement precedes pending incoming head", tip: "synthetic-old", head: "synthetic-incoming", known: "synthetic-old", want: true},
		{name: "absorb moves no branch", tip: "synthetic-old", known: "synthetic-old", absorb: true},
		{name: "no remote", tip: "synthetic-old", err: localgit.ErrNoSuchRemote},
		{name: "unreadable known tips", tip: "synthetic-old", err: errors.New("synthetic read failure")},
	} {
		t.Run(test.name, func(t *testing.T) {
			git := &agreementGit{fakeGit: stackGit(), known: map[string]string{"synthetic-a": test.known}, err: test.err}
			service := Service{Git: git}
			service.recordLevel(context.Background(), Plan{Absorb: test.absorb, Steps: []Step{{Branch: "synthetic-a", Tip: test.tip, Head: test.head}}})
			if (len(git.recorded) != 0) != test.want {
				t.Fatalf("recorded=%v, want agreement=%t", git.recorded, test.want)
			}
		})
	}
}

func TestReplayAgreementRecordingFailureIsDiagnostic(t *testing.T) {
	for _, historyFails := range []bool{false, true} {
		t.Run(map[bool]string{false: "write", true: "read"}[historyFails], func(t *testing.T) {
			git := &agreementGit{fakeGit: stackGit(), known: map[string]string{"synthetic-a": "a-old"}}
			if historyFails {
				git.historyErr = errors.New("synthetic history failure")
			} else {
				git.recordErr = errors.New("synthetic write failure")
			}
			service, _, _ := newService(git.fakeGit, stack())
			service.Git = git
			var out bytes.Buffer
			ctx := diagnostic.WithSink(context.Background(), diagnostic.Writer{Out: &out})
			plan, err := service.Plan(ctx, selection(), Onto{}, false, Pending{}, "")
			if err != nil || plan.Blocked() != "" {
				t.Fatalf("recording failure stopped restack: %+v, %v", plan, err)
			}
			if !strings.Contains(out.String(), `event=restack.sync_point branch="synthetic-a" decision="not recorded"`) {
				t.Fatalf("missing recording diagnostic: %s", out.String())
			}
		})
	}
}

type heldAgreementGit struct{ *agreementGit }

func (g heldAgreementGit) HoldersElsewhere(context.Context) (map[string]worktree.Holder, error) {
	return map[string]worktree.Holder{"synthetic-b": {Branch: "synthetic-b", Path: "/synthetic/held"}}, nil
}
func (g heldAgreementGit) WorktreeClean(context.Context, string) (bool, error) { return false, nil }

func TestPlanRecordsAgreementOnlyForMovingBranches(t *testing.T) {
	git := heldAgreementGit{&agreementGit{fakeGit: stackGit(), known: map[string]string{"synthetic-a": "a-old", "synthetic-b": "b-old"}}}
	service, _, _ := newService(git.fakeGit, stack())
	service.Git = git
	plan, err := service.Plan(context.Background(), selection(), Onto{}, false, Pending{}, worktree.Follow)
	if err != nil || plan.Blocked() != "" {
		t.Fatalf("Plan()=%+v, %v", plan, err)
	}
	if !slices.Equal(git.knownBranches, []string{"synthetic-a"}) || len(git.recorded) != 1 {
		t.Fatalf("left branch recorded: known=%v, recorded=%v", git.knownBranches, git.recorded)
	}
}
