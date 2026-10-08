package comment

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/shhac/g2g/internal/githubstack"
	"github.com/shhac/g2g/internal/shape"
	"github.com/shhac/g2g/internal/stack"
)

// The pull request at the bottom merged and its branch was pruned and deleted.
// Nothing local remembers it; the comments do, and it stays listed.
func TestPlanKeepsListingAPullRequestThatMergedOutOfTheStack(t *testing.T) {
	landed := shape.Forest{Parents: map[string]string{
		"synthetic-trunk": "",
		"synthetic-two":   "synthetic-trunk",
		"synthetic-three": "synthetic-two",
	}}
	previous := Marker + "\nold\n" + recordedLine("11,12>11,13>12")
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
	if body := bodyFor(t, got, 13); !strings.Contains(body, "- base `synthetic-trunk`\n- #11 `synthetic-one` · merged\n- #12 `synthetic-two`\n") || !strings.Contains(body, recordedLine("11,12,13>12")) {
		t.Errorf("#13 does not list what merged where it sat:\n%s", body)
	}
	merged := bodyFor(t, got, 11)
	if !strings.Contains(merged, "- base `synthetic-trunk`\n- **#11 `synthetic-one` · merged** 👈 this pull request\n- #12 `synthetic-two`\n- #13 `synthetic-three`\n") {
		t.Errorf("the merged pull request's comment does not show where the stack went:\n%s", merged)
	}
	if strings.Contains(merged, "Merged into") {
		t.Errorf("what merged is still set apart on a line of its own:\n%s", merged)
	}
	// Round one reads what the stack carries; round two, what its comments
	// named that it no longer does.
	if len(github.asked) != 2 || !slices.Equal(github.asked[1], []int{11}) {
		t.Errorf("asked = %v, want #11 read in a second round", github.asked)
	}
}

// Once the one below lands, the one above is recorded on the trunk, so the
// numbers alone no longer say where each merged pull request sat. The order
// they merged in does: a stack comes down from the bottom. #14 was opened
// before #15 but stacked on it, so it merged second.
func TestPlanListsWhatMergedInTheOrderItMerged(t *testing.T) {
	landed := shape.Forest{Parents: map[string]string{
		"synthetic-trunk": "",
		"synthetic-three": "synthetic-trunk",
	}}
	previous := Marker + "\nold\n" + recordedLine("13,14,15")
	first := conversation(15, "synthetic-one", "MERGED", previous)
	first.MergedAt = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	second := conversation(14, "synthetic-two", "MERGED", previous)
	second.MergedAt = first.MergedAt.Add(time.Hour)
	unnamed := conversation(13, "synthetic-three", "OPEN", previous)
	github := &fakeGitHub{
		prs:           []githubstack.PullRequest{pr(13, "synthetic-three", "synthetic-trunk", "OPEN")},
		conversations: map[int]githubstack.Conversation{13: unnamed, 14: second, 15: first},
	}
	got := plan(t, landed, "synthetic-three", github)
	want := "- base `synthetic-trunk`\n- #15 `synthetic-one` · merged\n- #14 `synthetic-two` · merged\n- **#13 `synthetic-three`** 👈 this pull request\n"
	if body := bodyFor(t, got, 13); !strings.Contains(body, want) {
		t.Errorf("#13 does not list what merged in the order it merged:\n%s", body)
	}
	if body := bodyFor(t, got, 14); !strings.Contains(body, "- #15 `synthetic-one` · merged\n- **#14 `synthetic-two` · merged** 👈 this pull request\n- #13 `synthetic-three`\n") {
		t.Errorf("the merged pull request's own comment does not place it:\n%s", body)
	}
}

// A comment 0.43.0 or earlier wrote records its history as the bare list.
// That history is read and carried into the comment that replaces it.
func TestPlanCarriesHistoryFromTheListBeforeItHadFields(t *testing.T) {
	landed := shape.Forest{Parents: map[string]string{
		"synthetic-trunk": "",
		"synthetic-two":   "synthetic-trunk",
	}}
	legacy := Marker + " rev=0123456789abcdef -->\nold\n" + dataOpen + "11,12>11" + dataClose
	github := &fakeGitHub{
		prs: []githubstack.PullRequest{pr(12, "synthetic-two", "synthetic-trunk", "OPEN")},
		conversations: map[int]githubstack.Conversation{
			11: conversation(11, "synthetic-one", "MERGED", legacy),
			12: conversation(12, "synthetic-two", "OPEN", legacy),
		},
	}
	got := plan(t, landed, "synthetic-two", github)
	if actions(got) != "#12:update #11:update" {
		t.Fatalf("writes = %s", actions(got))
	}
	if body := bodyFor(t, got, 12); !strings.Contains(body, "- #11 `synthetic-one` · merged\n") || !strings.Contains(body, recordedLine("11,12")) {
		t.Errorf("#12 lost the history its old comment recorded:\n%s", body)
	}
}

// A comment a newer g2g wrote, in a format this one cannot read, is left as
// it is: rewriting it would drop the history it could not read.
func TestPlanLeavesACommentInANewerFormatAlone(t *testing.T) {
	// The version is named when the marker gives one this g2g would write,
	// and is otherwise only newer: anyone who can edit the comment can edit it.
	for marker, want := range map[string]string{
		" rev=0123456789abcdef version=9.0.0 -->": "the stack comment on #12 was written by g2g@9.0.0, a newer g2g · upgrade g2g to keep it",
		" rev=0123456789abcdef -->":               "the stack comment on #12 was written by a newer g2g · upgrade g2g to keep it",
		" version=synthetic](x) -->":              "the stack comment on #12 was written by a newer g2g · upgrade g2g to keep it",
	} {
		github := chainGitHub()
		newer := Marker + marker + "\nnewer\n" + dataOpen + "v=2 prs=11,12>11" + dataClose
		github.conversations[12] = conversation(12, "synthetic-two", "OPEN", newer)
		got := plan(t, chain(), "synthetic-one", github)
		if actions(got) != "#11:create #12:skip #13:create" {
			t.Fatalf("%q: writes = %s", marker, actions(got))
		}
		for _, write := range got.Writes {
			if write.Number == 12 && write.Reason != want {
				t.Errorf("%q: reason = %q, want %q", marker, write.Reason, want)
			}
		}
	}
}

// Only a merged pull request is kept. One closed without merging, or open in
// some other stack now, has left this one.
func TestPlanDropsWhatDidNotMerge(t *testing.T) {
	previous := Marker + "\n" + recordedLine("11,12>11,13>12,17,18>12")
	github := chainGitHub()
	github.conversations[12] = conversation(12, "synthetic-two", "OPEN", previous)
	github.conversations[17] = conversation(17, "synthetic-abandoned", "CLOSED")
	github.conversations[18] = conversation(18, "synthetic-moved", "OPEN")
	got := plan(t, chain(), "synthetic-one", github)
	if len(got.Merged) != 0 {
		t.Errorf("Merged = %v, want nothing kept", got.Merged)
	}
	if body := bodyFor(t, got, 12); !strings.Contains(body, recordedLine("11,12>11,13>12")) {
		t.Errorf("#12 still records what left:\n%s", body)
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
			31: conversation(31, "synthetic-a", "OPEN", Marker+"\n"+recordedLine("30,31>30,32>31")),
			32: conversation(32, "synthetic-a-top", "OPEN"),
			41: conversation(41, "synthetic-b", "OPEN"),
			42: conversation(42, "synthetic-b-top", "OPEN"),
		},
	}
	got := plan(t, forest, "synthetic-trunk", github)
	if body := bodyFor(t, got, 32); !strings.Contains(body, "- base `synthetic-trunk`\n- #30 `synthetic-a-landed` · merged\n- #31 `synthetic-a`\n") {
		t.Errorf("stack a lost its history:\n%s", body)
	}
	if body := bodyFor(t, got, 42); strings.Contains(body, "#30") || strings.Contains(body, "synthetic-a") {
		t.Errorf("stack b lists stack a:\n%s", body)
	}
}

// A pull request moved in from another stack brings a comment describing that
// one. Its merged history is somebody else's, and adopting it would record it
// here for every later run.
func TestPlanDoesNotAdoptHistoryAPullRequestBroughtFromAnotherStack(t *testing.T) {
	github := chainGitHub()
	// #13 used to sit on #51 in another stack, whose bottom #50 merged.
	github.conversations[13] = conversation(13, "synthetic-three", "OPEN", Marker+"\n"+recordedLine("50,51,13>51"))
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
			12: conversation(12, "synthetic-two", "OPEN", Marker+"\n"+recordedLine("11,12>11")),
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

// A recorded number GitHub no longer answers for is dropped rather than
// failing the run; one the stack carries is not allowed to vanish.
func TestPlanDropsARecordedNumberNothingAnswersTo(t *testing.T) {
	github := chainGitHub()
	github.conversations[12] = conversation(12, "synthetic-two", "OPEN", Marker+"\n"+recordedLine("9,11>9,12>11,13>12"))
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
	recorded := Marker + "\n" + recordedLine("30,31>30,41>30")
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

// A branch folded into the one below it: publishing the parent puts the
// child's commits on the branch its pull request targets, so GitHub reads it
// as merged there. Nothing reached the trunk, and drawing it between the trunk
// and the open work would say otherwise. It hangs under the pull request it
// merged into, saying so.
func TestPlanDrawsAPullRequestMergedIntoTheStackUnderWhereItMerged(t *testing.T) {
	folded := shape.Forest{Parents: map[string]string{
		"synthetic-trunk": "",
		"synthetic-lower": "synthetic-trunk",
		"synthetic-upper": "synthetic-lower",
	}}
	previous := Marker + "\nold\n" + recordedLine("21,22>21,23>22")
	child := conversation(22, "synthetic-folded", "MERGED", previous)
	child.Base = "synthetic-lower"
	github := &fakeGitHub{
		prs: []githubstack.PullRequest{
			pr(21, "synthetic-lower", "synthetic-trunk", "OPEN"),
			pr(23, "synthetic-upper", "synthetic-lower", "OPEN"),
		},
		conversations: map[int]githubstack.Conversation{
			21: conversation(21, "synthetic-lower", "OPEN", previous),
			22: child,
			23: conversation(23, "synthetic-upper", "OPEN", previous),
		},
	}
	got := plan(t, folded, "synthetic-lower", github)
	if !slices.Equal(got.Merged, []int{22}) {
		t.Fatalf("Merged = %v, want #22 kept", got.Merged)
	}
	want := "- base `synthetic-trunk`\n- **#21 `synthetic-lower`** 👈 this pull request\n  - #22 `synthetic-folded` · merged into #21\n- #23 `synthetic-upper`\n"
	if body := bodyFor(t, got, 21); !strings.Contains(body, want) {
		t.Errorf("#21 does not draw #22 where it merged:\n%s", body)
	}
	want = "- base `synthetic-trunk`\n- #21 `synthetic-lower`\n  - #22 `synthetic-folded` · merged into #21\n- **#23 `synthetic-upper`** 👈 this pull request\n"
	if body := bodyFor(t, got, 23); !strings.Contains(body, want) {
		t.Errorf("#23 does not draw #22 where it merged:\n%s", body)
	}
	want = "- base `synthetic-trunk`\n- #21 `synthetic-lower`\n  - **#22 `synthetic-folded` · merged into #21** 👈 this pull request\n- #23 `synthetic-upper`\n"
	if body := bodyFor(t, got, 22); !strings.Contains(body, want) {
		t.Errorf("#22's own comment does not say where it merged:\n%s", body)
	}
}

// The branch it was folded into has landed since, so both are history. The
// folded one merged first, and is still drawn under the one it merged into
// rather than as though it reached the trunk ahead of it.
func TestPlanDrawsWhatMergedIntoMergedWorkUnderIt(t *testing.T) {
	landed := shape.Forest{Parents: map[string]string{
		"synthetic-trunk": "",
		"synthetic-upper": "synthetic-trunk",
	}}
	previous := Marker + "\nold\n" + recordedLine("21,22>21,23>21")
	folded := conversation(22, "synthetic-folded", "MERGED", previous)
	folded.Base = "synthetic-lower"
	folded.MergedAt = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	lower := conversation(21, "synthetic-lower", "MERGED", previous)
	lower.MergedAt = folded.MergedAt.Add(time.Hour)
	github := &fakeGitHub{
		prs: []githubstack.PullRequest{pr(23, "synthetic-upper", "synthetic-trunk", "OPEN")},
		conversations: map[int]githubstack.Conversation{
			21: lower,
			22: folded,
			23: conversation(23, "synthetic-upper", "OPEN", previous),
		},
	}
	got := plan(t, landed, "synthetic-upper", github)
	want := "- base `synthetic-trunk`\n- #21 `synthetic-lower` · merged\n  - #22 `synthetic-folded` · merged into #21\n- **#23 `synthetic-upper`** 👈 this pull request\n"
	if body := bodyFor(t, got, 23); !strings.Contains(body, want) {
		t.Errorf("#23 does not draw #22 under what it merged into:\n%s", body)
	}
}

// Merged into a branch this stack no longer reaches, it stays with the
// history, but does not claim to have reached the trunk.
func TestPlanNamesWhereAPullRequestMergedWhenTheStackDoesNotHaveIt(t *testing.T) {
	landed := shape.Forest{Parents: map[string]string{
		"synthetic-trunk": "",
		"synthetic-two":   "synthetic-trunk",
	}}
	previous := Marker + "\nold\n" + recordedLine("11,12>11")
	elsewhere := conversation(11, "synthetic-one", "MERGED", previous)
	elsewhere.Base = "synthetic-gone"
	github := &fakeGitHub{
		prs: []githubstack.PullRequest{pr(12, "synthetic-two", "synthetic-trunk", "OPEN")},
		conversations: map[int]githubstack.Conversation{
			11: elsewhere,
			12: conversation(12, "synthetic-two", "OPEN", Marker+"\nold\n"+recordedLine("11,12")),
		},
	}
	got := plan(t, landed, "synthetic-two", github)
	if body := bodyFor(t, got, 12); !strings.Contains(body, "- base `synthetic-trunk`\n- #11 `synthetic-one` · merged into `synthetic-gone`\n- **#12 `synthetic-two`**") {
		t.Errorf("#12 does not say where #11 merged:\n%s", body)
	}
}

// Bases are matched by name, so two merged pull requests whose branch names
// were reused can each name the other as where it merged. Neither can hang
// under the other; both stay listed, saying where they went.
func TestPlanListsMergedPullRequestsThatNameEachOtherAsTheirBase(t *testing.T) {
	landed := shape.Forest{Parents: map[string]string{
		"synthetic-trunk": "",
		"synthetic-three": "synthetic-trunk",
	}}
	previous := Marker + "\nold\n" + recordedLine("11,12,13")
	first := conversation(11, "synthetic-one", "MERGED", previous)
	first.Base = "synthetic-two"
	second := conversation(12, "synthetic-two", "MERGED", previous)
	second.Base = "synthetic-one"
	github := &fakeGitHub{
		prs:           []githubstack.PullRequest{pr(13, "synthetic-three", "synthetic-trunk", "OPEN")},
		conversations: map[int]githubstack.Conversation{11: first, 12: second, 13: conversation(13, "synthetic-three", "OPEN", previous)},
	}
	got := plan(t, landed, "synthetic-three", github)
	want := "- base `synthetic-trunk`\n- #11 `synthetic-one` · merged into `synthetic-two`\n- #12 `synthetic-two` · merged into `synthetic-one`\n- **#13 `synthetic-three`**"
	if body := bodyFor(t, got, 13); !strings.Contains(body, want) {
		t.Errorf("#13 lost the pull requests that name each other:\n%s", body)
	}
}
