package comment

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/githubstack"
	"github.com/shhac/g2g/internal/shape"
	"github.com/shhac/g2g/internal/stack"
)

// fakeSelector answers a selection from a forest the way a real selector does,
// scope included, so what these tests exercise is the widening to the whole
// stack rather than a fixed answer that already contains it.
type fakeSelector struct {
	forest  shape.Forest
	current string
}

func (f fakeSelector) Select(_ context.Context, selection stack.Selection, _ string) (stack.Snapshot, error) {
	target := selection.Branch
	if target == "" {
		target = f.current
	}
	scope := selection.EffectiveScope()
	selected, err := f.forest.Select(target, scope)
	if err != nil {
		return stack.Snapshot{}, err
	}
	base, within, err := f.forest.Hangs(selected, target, scope)
	if err != nil {
		return stack.Snapshot{}, err
	}
	branches := append([]string(nil), selected...)
	if within {
		branches = branches[1:]
	}
	return stack.Snapshot{Target: target, Base: base, Branches: branches, Scope: scope, Parents: f.forest.Restrict(selected)}, nil
}

type fakeGitHub struct {
	prs           []githubstack.PullRequest
	conversations map[int]githubstack.Conversation
	asked         [][]int
	sent          []string
	failOn        int
}

func (f *fakeGitHub) Inspect(context.Context, []string) ([]githubstack.PullRequest, error) {
	return f.prs, nil
}

func (f *fakeGitHub) Conversations(_ context.Context, numbers []int, marker string) ([]githubstack.Conversation, error) {
	if marker != Marker {
		return nil, fmt.Errorf("asked for marker %q", marker)
	}
	f.asked = append(f.asked, append([]int(nil), numbers...))
	found := make([]githubstack.Conversation, 0)
	for _, number := range numbers {
		if conversation, ok := f.conversations[number]; ok {
			found = append(found, conversation)
		}
	}
	return found, nil
}

func (f *fakeGitHub) AddComment(_ context.Context, subject, _ string) error {
	return f.send("add " + subject)
}

func (f *fakeGitHub) UpdateComment(_ context.Context, id, _ string) error {
	return f.send("update " + id)
}

func (f *fakeGitHub) send(what string) error {
	f.sent = append(f.sent, what)
	if f.failOn != 0 && len(f.sent) == f.failOn {
		return errors.New("synthetic write failure")
	}
	return nil
}

// chain is trunk ← one ← two ← three, the ordinary stack.
func chain() shape.Forest {
	return shape.Forest{Parents: map[string]string{
		"synthetic-trunk": "",
		"synthetic-one":   "synthetic-trunk",
		"synthetic-two":   "synthetic-one",
		"synthetic-three": "synthetic-two",
	}}
}

func pr(number int, head, base, state string) githubstack.PullRequest {
	return githubstack.PullRequest{Number: number, Head: head, Base: base, State: state}
}

func conversation(number int, head, state string, bodies ...string) githubstack.Conversation {
	found := githubstack.Conversation{ID: fmt.Sprintf("PR_synthetic_%d", number), Number: number, Head: head, Base: "synthetic-trunk", State: state, Commentable: true}
	for index, body := range bodies {
		found.Comments = append(found.Comments, githubstack.Comment{ID: fmt.Sprintf("IC_synthetic_%d_%d", number, index), Body: body, Author: "synthetic-author", Editable: true})
	}
	return found
}

func chainGitHub() *fakeGitHub {
	return &fakeGitHub{
		prs: []githubstack.PullRequest{
			pr(11, "synthetic-one", "synthetic-trunk", "OPEN"),
			pr(12, "synthetic-two", "synthetic-one", "OPEN"),
			pr(13, "synthetic-three", "synthetic-two", "OPEN"),
		},
		conversations: map[int]githubstack.Conversation{
			11: conversation(11, "synthetic-one", "OPEN"),
			12: conversation(12, "synthetic-two", "OPEN"),
			13: conversation(13, "synthetic-three", "OPEN"),
		},
	}
}

func plan(t *testing.T, forest shape.Forest, current string, github *fakeGitHub) Plan {
	t.Helper()
	planned, err := Service{Selector: fakeSelector{forest: forest, current: current}, GitHub: github}.Plan(context.Background(), stack.Selection{})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	return planned
}

func actions(p Plan) string {
	said := make([]string, 0, len(p.Writes))
	for _, write := range p.Writes {
		said = append(said, fmt.Sprintf("#%d:%s", write.Number, write.Action))
	}
	return strings.Join(said, " ")
}

func bodyFor(t *testing.T, p Plan, number int) string {
	t.Helper()
	for _, write := range p.Writes {
		if write.Number == number {
			return write.Body
		}
	}
	t.Fatalf("no write for #%d in %s", number, actions(p))
	return ""
}

func TestPlanAddsACommentToEveryOpenPullRequest(t *testing.T) {
	got := plan(t, chain(), "synthetic-two", chainGitHub())
	if actions(got) != "#11:create #12:create #13:create" {
		t.Fatalf("writes = %s", actions(got))
	}
	body := bodyFor(t, got, 12)
	for _, want := range []string{
		Marker,
		"- `synthetic-trunk`\n- #11 `synthetic-one`\n- **#12 `synthetic-two`** 👈 this pull request\n- #13 `synthetic-three`\n",
		dataOpen + "11,12>11,13>12" + dataClose,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body for #12 missing %q:\n%s", want, body)
		}
	}
	if got.Requested != "synthetic-two" || got.Target != "synthetic-one" {
		t.Errorf("requested %q, selected from %q; want the request kept and the stack taken from its bottom", got.Requested, got.Target)
	}
	if got.Writes[0].Subject != "PR_synthetic_11" {
		t.Errorf("create is addressed to %q, want the pull request's node id", got.Writes[0].Subject)
	}
}

// Whichever branch a run starts from, the comments it would write are the
// same. A run that rewrote them differently from each branch would never
// settle.
func TestPlanConvergesWhereverItStarts(t *testing.T) {
	forest := forked()
	want := plan(t, forest, "synthetic-one", forkedGitHub())
	for _, from := range []string{"synthetic-left", "synthetic-right", "synthetic-left-top"} {
		got := plan(t, forest, from, forkedGitHub())
		if !slices.Equal(got.Writes, want.Writes) {
			t.Errorf("from %s the writes differ:\n%s\nwant\n%s", from, actions(got), actions(want))
		}
	}
}

// trunk ← one ← {left ← left-top, right}
func forked() shape.Forest {
	return shape.Forest{Parents: map[string]string{
		"synthetic-trunk":    "",
		"synthetic-one":      "synthetic-trunk",
		"synthetic-left":     "synthetic-one",
		"synthetic-left-top": "synthetic-left",
		"synthetic-right":    "synthetic-one",
	}}
}

func forkedGitHub() *fakeGitHub {
	github := &fakeGitHub{conversations: map[int]githubstack.Conversation{}}
	for number, head := range map[int]string{21: "synthetic-one", 22: "synthetic-left", 23: "synthetic-left-top", 24: "synthetic-right"} {
		github.prs = append(github.prs, pr(number, head, "synthetic-synthetic", "OPEN"))
		github.conversations[number] = conversation(number, head, "OPEN")
	}
	return github
}

// A branch's comment lists what it stands on and what is built on it. The fork
// is nested where it happens; a cousin is not listed at all.
func TestPlanDrawsEachPullRequestsOwnLineage(t *testing.T) {
	got := plan(t, forked(), "synthetic-one", forkedGitHub())

	bottom := bodyFor(t, got, 21)
	if !strings.Contains(bottom, "- **#21 `synthetic-one`** 👈 this pull request\n  - #22 `synthetic-left`\n    - #23 `synthetic-left-top`\n  - #24 `synthetic-right`\n") {
		t.Errorf("bottom comment does not nest the fork above it:\n%s", bottom)
	}
	left := bodyFor(t, got, 22)
	if strings.Contains(left, "synthetic-right") {
		t.Errorf("a cousin is listed on #22:\n%s", left)
	}
	if !strings.Contains(left, "- `synthetic-trunk`\n- #21 `synthetic-one`\n- **#22 `synthetic-left`** 👈 this pull request\n- #23 `synthetic-left-top`\n") {
		t.Errorf("#22 does not list its own line flat:\n%s", left)
	}
	// Every comment records the whole stack, so the next run can find all of it
	// from any one of them.
	if !strings.Contains(left, dataOpen+"21,22>21,23>22,24>21"+dataClose) {
		t.Errorf("#22 does not record the whole stack:\n%s", left)
	}
}

func TestPlanEditsTheCommentItFinds(t *testing.T) {
	first := plan(t, chain(), "synthetic-one", chainGitHub())
	github := chainGitHub()
	github.conversations[11] = conversation(11, "synthetic-one", "OPEN", bodyFor(t, first, 11))
	github.conversations[12] = conversation(12, "synthetic-two", "OPEN", Marker+" stale")
	// A comment edited in a browser comes back with CRLF endings, and that is
	// no reason to edit it again.
	github.conversations[13] = conversation(13, "synthetic-three", "OPEN", strings.ReplaceAll(bodyFor(t, first, 13), "\n", "\r\n"))

	got := plan(t, chain(), "synthetic-one", github)
	if actions(got) != "#11:current #12:update #13:current" {
		t.Fatalf("writes = %s", actions(got))
	}
	if got.Writes[1].Comment != "IC_synthetic_12_0" {
		t.Errorf("update is addressed to %q, want the existing comment", got.Writes[1].Comment)
	}
	if got.Changing() != 1 || got.NothingToDo() {
		t.Errorf("Changing() = %d, NothingToDo() = %t", got.Changing(), got.NothingToDo())
	}
}

// The pull request at the bottom merged and its branch was pruned and deleted.
// Nothing local remembers it; the comments do, and it stays listed.
func TestPlanKeepsListingAPullRequestThatMergedOutOfTheStack(t *testing.T) {
	landed := shape.Forest{Parents: map[string]string{
		"synthetic-trunk": "",
		"synthetic-two":   "synthetic-trunk",
		"synthetic-three": "synthetic-two",
	}}
	previous := Marker + "\nold\n" + dataOpen + "11,12>11,13>12" + dataClose
	github := &fakeGitHub{
		prs: []githubstack.PullRequest{
			pr(12, "synthetic-two", "synthetic-trunk", "OPEN"),
			pr(13, "synthetic-three", "synthetic-two", "OPEN"),
		},
		conversations: map[int]githubstack.Conversation{
			11: conversation(11, "synthetic-one", "MERGED", previous),
			12: conversation(12, "synthetic-two", "OPEN", previous),
			13: conversation(13, "synthetic-three", "OPEN"),
		},
	}
	got := plan(t, landed, "synthetic-three", github)
	if !slices.Equal(got.Merged, []int{11}) {
		t.Fatalf("Merged = %v, want #11 kept", got.Merged)
	}
	if actions(got) != "#12:update #13:create #11:update" {
		t.Fatalf("writes = %s", actions(got))
	}
	if body := bodyFor(t, got, 13); !strings.Contains(body, "Merged into `synthetic-trunk`: #11\n") || !strings.Contains(body, dataOpen+"11,12,13>12"+dataClose) {
		t.Errorf("#13 does not list what merged:\n%s", body)
	}
	merged := bodyFor(t, got, 11)
	if !strings.Contains(merged, "Merged into `synthetic-trunk`: **#11** 👈 this pull request\n") || !strings.Contains(merged, "- #12 `synthetic-two`\n- #13 `synthetic-three`\n") {
		t.Errorf("the merged pull request's comment does not show where the stack went:\n%s", merged)
	}
	// Round one reads what the stack carries; round two, what its comments
	// named that it no longer does.
	if len(github.asked) != 2 || !slices.Equal(github.asked[1], []int{11}) {
		t.Errorf("asked = %v, want #11 read in a second round", github.asked)
	}
}

// Only a merged pull request is kept. One closed without merging, or open in
// some other stack now, has left this one.
func TestPlanDropsWhatDidNotMerge(t *testing.T) {
	previous := Marker + "\n" + dataOpen + "11,12>11,13>12,17,18>12" + dataClose
	github := chainGitHub()
	github.conversations[12] = conversation(12, "synthetic-two", "OPEN", previous)
	github.conversations[17] = conversation(17, "synthetic-abandoned", "CLOSED")
	github.conversations[18] = conversation(18, "synthetic-moved", "OPEN")
	got := plan(t, chain(), "synthetic-one", github)
	if len(got.Merged) != 0 {
		t.Errorf("Merged = %v, want nothing kept", got.Merged)
	}
	if body := bodyFor(t, got, 12); !strings.Contains(body, dataOpen+"11,12>11,13>12"+dataClose) {
		t.Errorf("#12 still records what left:\n%s", body)
	}
}

// A merged pull request is never given a comment it did not have: nobody is
// reviewing it, and a new comment notifies everyone who did.
func TestPlanNeverAddsACommentToAMergedPullRequest(t *testing.T) {
	github := chainGitHub()
	github.prs[0].State = "MERGED"
	github.conversations[11] = conversation(11, "synthetic-one", "MERGED")
	got := plan(t, chain(), "synthetic-two", github)
	if actions(got) != "#12:create #13:create" {
		t.Fatalf("writes = %s", actions(got))
	}
	if body := bodyFor(t, got, 12); !strings.Contains(body, "- #11 `synthetic-one` · merged\n") {
		t.Errorf("a merged branch still in the stack is not shown as merged:\n%s", body)
	}
}

func TestPlanLeavesAloneWhatItCannotSafelyEdit(t *testing.T) {
	github := chainGitHub()
	github.conversations[11] = conversation(11, "synthetic-one", "OPEN", Marker+" first", Marker+" second")
	notMine := conversation(12, "synthetic-two", "OPEN", Marker+" theirs")
	notMine.Comments[0].Editable = false
	github.conversations[12] = notMine
	got := plan(t, chain(), "synthetic-one", github)
	if actions(got) != "#11:skip #12:skip #13:create" {
		t.Fatalf("writes = %s", actions(got))
	}
	if !strings.Contains(got.Writes[0].Reason, "2 stack comments on #11") || !strings.Contains(got.Writes[1].Reason, "@synthetic-author") {
		t.Errorf("reasons = %q, %q", got.Writes[0].Reason, got.Writes[1].Reason)
	}
}

func TestPlanBlocksABranchWithTwoOpenPullRequests(t *testing.T) {
	github := chainGitHub()
	github.prs = append(github.prs, pr(19, "synthetic-two", "synthetic-one", "OPEN"))
	got := plan(t, chain(), "synthetic-one", github)
	if got.Blocked == "" || !slices.Equal(got.Ambiguous, []string{"synthetic-two"}) || len(got.Writes) != 0 {
		t.Fatalf("plan = %+v, want blocked on synthetic-two", got)
	}
	if err := (Service{Selector: fakeSelector{}, GitHub: github}).Execute(context.Background(), got); err == nil {
		t.Error("Execute() ran a blocked plan")
	}
}

// One pull request is not a stack, and a comment saying so is noise. A branch
// with no pull request still appears where it sits.
func TestPlanAddsNothingToAStackOfOne(t *testing.T) {
	github := chainGitHub()
	github.prs = github.prs[:1]
	got := plan(t, chain(), "synthetic-one", github)
	if len(got.Writes) != 0 || !got.NothingToDo() {
		t.Fatalf("writes = %s, want none", actions(got))
	}

	github = chainGitHub()
	github.prs = []githubstack.PullRequest{github.prs[0], github.prs[2]}
	got = plan(t, chain(), "synthetic-one", github)
	if body := bodyFor(t, got, 13); !strings.Contains(body, "- `synthetic-two` · no pull request yet\n") {
		t.Errorf("a branch with no pull request is not shown:\n%s", body)
	}
}

// From a trunk, every stack on it is kept, and each keeps its own history.
func TestPlanFromATrunkKeepsEachStacksHistoryApart(t *testing.T) {
	forest := shape.Forest{Parents: map[string]string{
		"synthetic-trunk": "",
		"synthetic-a":     "synthetic-trunk",
		"synthetic-a-top": "synthetic-a",
		"synthetic-b":     "synthetic-trunk",
		"synthetic-b-top": "synthetic-b",
	}}
	github := &fakeGitHub{
		prs: []githubstack.PullRequest{
			pr(31, "synthetic-a", "synthetic-trunk", "OPEN"),
			pr(32, "synthetic-a-top", "synthetic-a", "OPEN"),
			pr(41, "synthetic-b", "synthetic-trunk", "OPEN"),
			pr(42, "synthetic-b-top", "synthetic-b", "OPEN"),
		},
		conversations: map[int]githubstack.Conversation{
			30: conversation(30, "synthetic-a-landed", "MERGED"),
			31: conversation(31, "synthetic-a", "OPEN", Marker+"\n"+dataOpen+"30,31>30,32>31"+dataClose),
			32: conversation(32, "synthetic-a-top", "OPEN"),
			41: conversation(41, "synthetic-b", "OPEN"),
			42: conversation(42, "synthetic-b-top", "OPEN"),
		},
	}
	got := plan(t, forest, "synthetic-trunk", github)
	if body := bodyFor(t, got, 32); !strings.Contains(body, "Merged into `synthetic-trunk`: #30\n") {
		t.Errorf("stack a lost its history:\n%s", body)
	}
	if body := bodyFor(t, got, 42); strings.Contains(body, "#30") || strings.Contains(body, "synthetic-a") {
		t.Errorf("stack b lists stack a:\n%s", body)
	}
}

func TestExecuteSendsEachChangeInOrderAndStopsAtAFailure(t *testing.T) {
	github := chainGitHub()
	github.conversations[12] = conversation(12, "synthetic-two", "OPEN", Marker+" stale")
	service := Service{Selector: fakeSelector{forest: chain(), current: "synthetic-one"}, GitHub: github}
	planned, err := service.Plan(context.Background(), stack.Selection{})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Execute(context.Background(), planned); err != nil {
		t.Fatal(err)
	}
	if strings.Join(github.sent, ", ") != "add PR_synthetic_11, update IC_synthetic_12_0, add PR_synthetic_13" {
		t.Errorf("sent = %v", github.sent)
	}

	github.sent, github.failOn = nil, 2
	err = service.Execute(context.Background(), planned)
	if err == nil || !strings.Contains(err.Error(), "#12") {
		t.Fatalf("Execute() error = %v, want the failing pull request named", err)
	}
	if len(github.sent) != 2 {
		t.Errorf("sent = %v, want it to stop at the failure", github.sent)
	}
}

func TestRevalidateRefusesACommentThatChangedUnderneath(t *testing.T) {
	github := chainGitHub()
	service := Service{Selector: fakeSelector{forest: chain(), current: "synthetic-one"}, GitHub: github}
	preview, err := service.Plan(context.Background(), stack.Selection{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Revalidate(context.Background(), stack.Selection{}, preview); err != nil {
		t.Fatalf("Revalidate() of an unchanged world = %v", err)
	}
	github.conversations[12] = conversation(12, "synthetic-two", "OPEN", Marker+" someone else's")
	if _, err := service.Revalidate(context.Background(), stack.Selection{}, preview); err == nil {
		t.Fatal("Revalidate() accepted a comment added since the preview")
	}
}

// A pull request moved in from another stack brings a comment describing that
// one. Its merged history is somebody else's, and adopting it would record it
// here for every later run.
func TestPlanDoesNotAdoptHistoryAPullRequestBroughtFromAnotherStack(t *testing.T) {
	github := chainGitHub()
	// #13 used to sit on #51 in another stack, whose bottom #50 merged.
	github.conversations[13] = conversation(13, "synthetic-three", "OPEN", Marker+"\n"+dataOpen+"50,51,13>51"+dataClose)
	github.conversations[50] = conversation(50, "synthetic-elsewhere-landed", "MERGED")
	github.conversations[51] = conversation(51, "synthetic-elsewhere", "OPEN")
	got := plan(t, chain(), "synthetic-one", github)
	if len(got.Merged) != 0 {
		t.Fatalf("Merged = %v, want another stack's history left alone", got.Merged)
	}
	if body := bodyFor(t, got, 13); strings.Contains(body, "#50") {
		t.Errorf("#13 still lists the other stack:\n%s", body)
	}
}

// The branch below landed and #12 was put on the trunk in its place: its
// comment still records #11 below it, and that is this stack's own history.
func TestPlanTrustsACommentWhosePullRequestMovedOntoWhatItsParentMergedInto(t *testing.T) {
	landed := shape.Forest{Parents: map[string]string{"synthetic-trunk": "", "synthetic-two": "synthetic-trunk"}}
	github := &fakeGitHub{
		prs: []githubstack.PullRequest{pr(12, "synthetic-two", "synthetic-trunk", "OPEN")},
		conversations: map[int]githubstack.Conversation{
			11: conversation(11, "synthetic-one", "MERGED"),
			12: conversation(12, "synthetic-two", "OPEN", Marker+"\n"+dataOpen+"11,12>11"+dataClose),
		},
	}
	got := plan(t, landed, "synthetic-two", github)
	if !slices.Equal(got.Merged, []int{11}) {
		t.Fatalf("Merged = %v, want #11", got.Merged)
	}

	// Merged somewhere other than where #12 sits now is not the same story.
	elsewhere := conversation(11, "synthetic-one", "MERGED")
	elsewhere.Base = "synthetic-release"
	github.conversations[11] = elsewhere
	if got := plan(t, landed, "synthetic-two", github); len(got.Merged) != 0 {
		t.Errorf("Merged = %v, want nothing adopted from a pull request that merged elsewhere", got.Merged)
	}
}

// From a trunk, two stacks may both name a number; each still sees it, so the
// comments agree with a run from inside either stack.
func TestPlanFromATrunkAgreesWithAPlanFromEachStack(t *testing.T) {
	forest := shape.Forest{Parents: map[string]string{
		"synthetic-trunk": "",
		"synthetic-a":     "synthetic-trunk",
		"synthetic-b":     "synthetic-trunk",
	}}
	shared := Marker + "\n" + dataOpen + "30,31,41" + dataClose
	fresh := func() *fakeGitHub {
		return &fakeGitHub{
			prs: []githubstack.PullRequest{pr(31, "synthetic-a", "synthetic-trunk", "OPEN"), pr(41, "synthetic-b", "synthetic-trunk", "OPEN")},
			conversations: map[int]githubstack.Conversation{
				30: conversation(30, "synthetic-landed", "MERGED"),
				31: conversation(31, "synthetic-a", "OPEN", shared),
				41: conversation(41, "synthetic-b", "OPEN", shared),
			},
		}
	}
	whole := plan(t, forest, "synthetic-trunk", fresh())
	for _, from := range []string{"synthetic-a", "synthetic-b"} {
		alone := plan(t, forest, from, fresh())
		for _, write := range alone.Writes {
			if write.Historic {
				continue
			}
			if body := bodyFor(t, whole, write.Number); body != write.Body {
				t.Errorf("#%d differs between a trunk run and a run from %s:\n%s\nvs\n%s", write.Number, from, body, write.Body)
			}
		}
	}
}

// A locked conversation takes no new comment, and finding that out from a
// failed write part-way down a stack is the wrong time.
func TestPlanSkipsAConversationThatTakesNoComments(t *testing.T) {
	github := chainGitHub()
	locked := github.conversations[12]
	locked.Commentable = false
	github.conversations[12] = locked
	got := plan(t, chain(), "synthetic-one", github)
	if actions(got) != "#11:create #12:skip #13:create" || !strings.Contains(got.Writes[1].Reason, "locked") {
		t.Fatalf("writes = %s, reason %q", actions(got), got.Writes[1].Reason)
	}
}

// A recorded number GitHub no longer answers for is dropped rather than
// failing the run; one the stack carries is not allowed to vanish.
func TestPlanDropsARecordedNumberNothingAnswersTo(t *testing.T) {
	github := chainGitHub()
	github.conversations[12] = conversation(12, "synthetic-two", "OPEN", Marker+"\n"+dataOpen+"9,11>9,12>11,13>12"+dataClose)
	got := plan(t, chain(), "synthetic-one", github)
	if len(got.Merged) != 0 || len(got.Unread) != 0 {
		t.Errorf("Merged = %v, Unread = %v", got.Merged, got.Unread)
	}

	delete(github.conversations, 13)
	if _, err := (Service{Selector: fakeSelector{forest: chain(), current: "synthetic-one"}, GitHub: github}).Plan(context.Background(), stack.Selection{}); err == nil {
		t.Error("Plan() accepted a stack whose pull request GitHub did not answer for")
	}
}

// The branch a fork grew from merged, so two stacks now sit on the trunk and
// both record it. Drawn from either one alone, its comment would say the other
// does not exist, and runs from each would keep undoing one another; it is
// left as it is, and listed once.
func TestPlanLeavesAMergedForkPointAloneAndListsItOnce(t *testing.T) {
	forest := shape.Forest{Parents: map[string]string{
		"synthetic-trunk": "",
		"synthetic-a":     "synthetic-trunk",
		"synthetic-b":     "synthetic-trunk",
	}}
	recorded := Marker + "\n" + dataOpen + "30,31>30,41>30" + dataClose
	fresh := func() *fakeGitHub {
		return &fakeGitHub{
			prs: []githubstack.PullRequest{pr(31, "synthetic-a", "synthetic-trunk", "OPEN"), pr(41, "synthetic-b", "synthetic-trunk", "OPEN")},
			conversations: map[int]githubstack.Conversation{
				30: conversation(30, "synthetic-fork-point", "MERGED", recorded),
				31: conversation(31, "synthetic-a", "OPEN", recorded),
				41: conversation(41, "synthetic-b", "OPEN", recorded),
			},
		}
	}
	for _, from := range []string{"synthetic-trunk", "synthetic-a", "synthetic-b"} {
		got := plan(t, forest, from, fresh())
		if !slices.Equal(got.Merged, []int{30}) {
			t.Errorf("from %s: Merged = %v, want #30 once", from, got.Merged)
		}
		thirty := 0
		for _, write := range got.Writes {
			if write.Number != 30 {
				continue
			}
			thirty++
			if write.Changes() {
				t.Errorf("from %s: #30 would be %s, want it left alone", from, write.Action)
			}
		}
		if thirty != 1 {
			t.Errorf("from %s: %d writes for #30, want one", from, thirty)
		}
	}
}

// The refusal names the branch to fix, and says there is no command for it.
func TestPlanNamesTheAmbiguousBranchInItsRefusal(t *testing.T) {
	github := chainGitHub()
	github.prs = append(github.prs, pr(19, "synthetic-two", "synthetic-one", "OPEN"))
	got := plan(t, chain(), "synthetic-one", github)
	if !strings.Contains(got.Blocked, "synthetic-two") || len(got.Repair.Ways) != 1 || got.Repair.Ways[0].Command != "" {
		t.Errorf("Blocked = %q, Repair = %+v", got.Blocked, got.Repair)
	}
	if got.Members["synthetic-two"].State != StateAmbiguous || got.Members["synthetic-two"].Number != 0 {
		t.Errorf("member = %+v, want ambiguous with no number", got.Members["synthetic-two"])
	}
}

// A run that fails after writing some comments reports which, because those
// stay written; one that fails on the first is an ordinary failure.
func TestExecuteReportsWhatItWroteBeforeStopping(t *testing.T) {
	github := chainGitHub()
	service := Service{Selector: fakeSelector{forest: chain(), current: "synthetic-one"}, GitHub: github}
	planned, err := service.Plan(context.Background(), stack.Selection{})
	if err != nil {
		t.Fatal(err)
	}
	github.failOn = 3
	err = service.Execute(context.Background(), planned)
	var stopped *Stopped
	if !errors.As(err, &stopped) || !slices.Equal(stopped.Written, []int{11, 12}) || stopped.Failed != 13 {
		t.Fatalf("Execute() = %v, want stopped at #13 after #11 and #12", err)
	}

	github.sent, github.failOn = nil, 1
	if err := service.Execute(context.Background(), planned); err == nil || errors.As(err, &stopped) {
		t.Errorf("a failure on the first write = %v, want an ordinary error", err)
	}
}

// Only a create or an update reaches GitHub: a comment someone else owns, or a
// pull request carrying two, is never sent.
func TestExecuteSendsOnlyCreatesAndUpdates(t *testing.T) {
	github := &fakeGitHub{}
	service := Service{Selector: fakeSelector{}, GitHub: github}
	planned := Plan{Writes: []Write{
		{Number: 1, Action: ActionCurrent, Comment: "IC_current"},
		{Number: 2, Action: ActionSkip, Comment: "IC_theirs"},
		{Number: 3, Action: ActionUpdate, Comment: "IC_merged", Historic: true, Body: "synthetic"},
	}}
	if err := service.Execute(context.Background(), planned); err != nil {
		t.Fatal(err)
	}
	if strings.Join(github.sent, ",") != "update IC_merged" {
		t.Errorf("sent = %v", github.sent)
	}
}

// A branch no local checkout has is structure and not somewhere to act.
func TestPlanRefusesABranchThisCheckoutDoesNotHave(t *testing.T) {
	github := chainGitHub()
	selector := absentSelector{fakeSelector{forest: chain(), current: "synthetic-one"}}
	got, err := Service{Selector: selector, GitHub: github}.Plan(context.Background(), stack.Selection{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Blocked == "" || len(got.Writes) != 0 || len(github.asked) != 0 {
		t.Errorf("plan = %+v, asked = %v; want refused before any conversation is read", got, github.asked)
	}
}

type absentSelector struct{ fakeSelector }

func (a absentSelector) Select(ctx context.Context, selection stack.Selection, command string) (stack.Snapshot, error) {
	snapshot, err := a.fakeSelector.Select(ctx, selection, command)
	snapshot.Absent = []string{"synthetic-three"}
	return snapshot, err
}

// The gate is on adding: a stack down to one pull request keeps the comment it
// has, and one open pull request with merged history is a stack of two.
func TestPlanAddsOnlyWhereThereIsAStackAndKeepsWhatIsThere(t *testing.T) {
	alone := shape.Forest{Parents: map[string]string{"synthetic-trunk": "", "synthetic-one": "synthetic-trunk"}}
	github := &fakeGitHub{
		prs:           []githubstack.PullRequest{pr(11, "synthetic-one", "synthetic-trunk", "OPEN")},
		conversations: map[int]githubstack.Conversation{11: conversation(11, "synthetic-one", "OPEN", Marker+" stale")},
	}
	if got := plan(t, alone, "synthetic-one", github); actions(got) != "#11:update" {
		t.Errorf("writes = %s, want the existing comment kept up to date", actions(got))
	}

	github.conversations[11] = conversation(11, "synthetic-one", "OPEN")
	github.conversations[10] = conversation(10, "synthetic-landed", "MERGED")
	if got := plan(t, alone, "synthetic-one", github); actions(got) != "" {
		t.Errorf("writes = %s, want nothing added to a stack of one with nothing recorded", actions(got))
	}
}

// A branch whose only pull request was closed is drawn, and neither written to
// nor recorded: nothing will merge it.
func TestPlanDrawsAClosedPullRequestsBranchAndRecordsNothingForIt(t *testing.T) {
	github := chainGitHub()
	github.prs[1].State = "CLOSED"
	github.conversations[12] = conversation(12, "synthetic-two", "CLOSED", Marker+" old")
	got := plan(t, chain(), "synthetic-one", github)
	if actions(got) != "#11:create #13:create" {
		t.Fatalf("writes = %s", actions(got))
	}
	body := bodyFor(t, got, 13)
	if !strings.Contains(body, "- `synthetic-two` · no open pull request\n") || !strings.Contains(body, dataOpen+"11,13"+dataClose) {
		t.Errorf("body:\n%s", body)
	}
}
