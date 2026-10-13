package worktree

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestEachPolicyMovesOrLeavesAHeldBranch(t *testing.T) {
	clean := Holder{Branch: "synthetic-b", Path: "/synthetic/b", Clean: true}
	dirty := Holder{Branch: "synthetic-b", Path: "/synthetic/b"}
	busy := Holder{Branch: "synthetic-b", Path: "/synthetic/b", Busy: "rebase"}
	for _, tc := range []struct {
		policy        Policy
		holder        Holder
		moves, leaves bool
	}{
		{Follow, clean, true, false},
		{Follow, dirty, false, true},
		{Follow, busy, false, true},
		{Skip, clean, false, true},
		{Skip, dirty, false, true},
		{Refuse, clean, false, false},
		{Refuse, dirty, false, false},
		// A caller that never chose refuses, as every command did before.
		{"", clean, false, false},
	} {
		if got := tc.policy.Moves(tc.holder); got != tc.moves {
			t.Errorf("%q.Moves(%+v) = %t, want %t", tc.policy, tc.holder, got, tc.moves)
		}
		if got := tc.policy.Leaves(tc.holder); got != tc.leaves {
			t.Errorf("%q.Leaves(%+v) = %t, want %t", tc.policy, tc.holder, got, tc.leaves)
		}
	}
}

func TestParseNamesTheValuesItAccepts(t *testing.T) {
	for _, policy := range Policies {
		if got, err := Parse(string(policy)); err != nil || got != policy {
			t.Errorf("Parse(%q) = %q, %v", policy, got, err)
		}
	}
	_, err := Parse("sometimes")
	if err == nil || !strings.Contains(err.Error(), "follow, skip, refuse") {
		t.Errorf("Parse(sometimes) error = %v, want the accepted values named", err)
	}
}

type reader struct {
	holders map[string]Holder
	clean   map[string]bool
	asked   []string
}

func (r *reader) HoldersElsewhere(context.Context) (map[string]Holder, error) {
	return r.holders, nil
}

func (r *reader) WorktreeClean(_ context.Context, path string) (bool, error) {
	r.asked = append(r.asked, path)
	clean, known := r.clean[path]
	if !known {
		return false, errors.New("synthetic: no such worktree")
	}
	return clean, nil
}

func TestReadAsksOnlyTheWorktreesHoldingTheBranchesInQuestion(t *testing.T) {
	git := &reader{
		holders: map[string]Holder{
			"synthetic-a": {Path: "/synthetic/a"},
			"synthetic-b": {Path: "/synthetic/b"},
			"synthetic-c": {Path: "/synthetic/c", Busy: "rebase"},
			"synthetic-d": {Path: "/synthetic/gone"},
		},
		clean: map[string]bool{"/synthetic/a": true, "/synthetic/b": false},
	}
	held, err := Read(context.Background(), git, []string{"synthetic-a", "synthetic-c", "synthetic-d", "synthetic-free"})
	if err != nil {
		t.Fatal(err)
	}
	if !held["synthetic-a"].Clean || held["synthetic-a"].Branch != "synthetic-a" {
		t.Errorf("synthetic-a = %+v, want a clean holder naming its branch", held["synthetic-a"])
	}
	if _, asked := held["synthetic-b"]; asked {
		t.Error("synthetic-b was not asked about and should not be reported")
	}
	// Busy is never clean, and is never asked: Git owns that worktree.
	if held["synthetic-c"].Clean {
		t.Error("a worktree part-way through a rebase read as clean")
	}
	// A worktree whose state cannot be read is not one to move.
	if held["synthetic-d"].Clean {
		t.Error("a worktree that could not be read counted as clean")
	}
	if strings.Join(git.asked, ",") != "/synthetic/a,/synthetic/gone" {
		t.Errorf("asked %v, want only the worktrees holding a branch in question", git.asked)
	}
}

func TestReadWithoutAReaderHoldsNothing(t *testing.T) {
	held, err := Read(context.Background(), struct{}{}, []string{"synthetic-a"})
	if err != nil || len(held) != 0 {
		t.Errorf("Read() = %v, %v, want nothing held", held, err)
	}
}

func TestWithNeverLetsAFollowOverruleALeave(t *testing.T) {
	a := Holder{Branch: "synthetic-a", Path: "/synthetic/a", Clean: true}
	b := Holder{Branch: "synthetic-b", Path: "/synthetic/b", Clean: true}
	merged := Outcome{Follow: []Holder{b, a}}.With(Outcome{Leave: []Holder{b}})
	if !merged.Equal(Outcome{Follow: []Holder{a}, Leave: []Holder{b}}) {
		t.Errorf("With() = %+v", merged)
	}
	if _, follows := merged.Follows("synthetic-b"); follows || !merged.Leaves("synthetic-b") {
		t.Error("a branch one side leaves was followed")
	}
}

func TestNotBroughtNamesTheCommandThatFinishesIt(t *testing.T) {
	err := error(&NotBrought{Holder: Holder{Branch: "synthetic-b", Path: "/synthetic/my work"}, From: "aaa1111", To: "bbb2222", Err: errors.New("synthetic refusal")})
	want := "git -C '/synthetic/my work' read-tree -m -u aaa1111 bbb2222"
	if !strings.Contains(err.Error(), want) {
		t.Errorf("error = %q, want it to contain %q", err, want)
	}
}

// Every held branch lands in exactly one of the three, and a policy nobody
// chose refuses.
func TestSplitSortsEachHeldBranchOnce(t *testing.T) {
	held := Held{
		"synthetic-clean": {Branch: "synthetic-clean", Path: "/synthetic/clean", Clean: true},
		"synthetic-dirty": {Branch: "synthetic-dirty", Path: "/synthetic/dirty"},
	}
	branches := []string{"synthetic-free", "synthetic-dirty", "synthetic-clean"}
	names := func(holders []Holder) string {
		said := make([]string, 0, len(holders))
		for _, holder := range holders {
			said = append(said, holder.Branch)
		}
		return strings.Join(said, ",")
	}
	for _, tc := range []struct {
		policy                Policy
		follow, leave, refuse string
	}{
		{Follow, "synthetic-clean", "synthetic-dirty", ""},
		{Skip, "", "synthetic-dirty,synthetic-clean", ""},
		{Refuse, "", "", "synthetic-dirty,synthetic-clean"},
		{"", "", "", "synthetic-dirty,synthetic-clean"},
	} {
		split := tc.policy.Split(held, branches)
		if got := [3]string{names(split.Follow), names(split.Leave), names(split.Refuse)}; got != [3]string{tc.follow, tc.leave, tc.refuse} {
			t.Errorf("%q.Split() = %q, want %q", tc.policy, got, [3]string{tc.follow, tc.leave, tc.refuse})
		}
		if refuses := tc.policy.Refuses(); refuses != (tc.refuse != "") {
			t.Errorf("%q.Refuses() = %t", tc.policy, refuses)
		}
	}
}
