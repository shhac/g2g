package comment

import (
	"slices"
	"strconv"
	"strings"
	"testing"
)

// Git allows a backtick in a branch name, and a code span ends at a run of
// backticks as long as the one that opened it.
func TestCodeCannotBeClosedByTheNameInside(t *testing.T) {
	for name, want := range map[string]string{
		"synthetic-plain":     "`synthetic-plain`",
		"synthetic-`tick":     "`` synthetic-`tick ``",
		"synthetic-``two``":   "``` synthetic-``two`` ```",
		"synthetic/*_marked*": "`synthetic/*_marked*`",
	} {
		if got := code(name); got != want {
			t.Errorf("code(%q) = %q, want %q", name, got, want)
		}
	}
}

// The recorded numbers are editable by anyone who can edit the pull request,
// so reading them back tolerates what a person might leave there.
func TestListedInReadsWhatItCanAndIgnoresTheRest(t *testing.T) {
	for body, want := range map[string][]int{
		"no data":                      nil,
		dataOpen + "3,1,2" + dataClose: {3, 1, 2},
		dataOpen + " 4, x, -5, 0, 4 ,6" + dataClose: {4, 6},
		dataOpen + "7,8": nil,
		"before " + dataOpen + "9" + dataClose + "\n": {9},
	} {
		if got := listedIn(body); !slices.Equal(got, want) {
			t.Errorf("listedIn(%q) = %v, want %v", body, got, want)
		}
	}
	long := make([]string, 0, listedLimit+50)
	for number := 1; number <= listedLimit+50; number++ {
		long = append(long, strconv.Itoa(number))
	}
	if got := listedIn(dataOpen + strings.Join(long, ",") + dataClose); len(got) != listedLimit {
		t.Errorf("listedIn read %d numbers, want it bounded at %d", len(got), listedLimit)
	}
}

// Nothing a branch can be called reaches the HTML comments: only numbers are
// written inside them.
func TestBodyKeepsBranchNamesOutOfItsHTMLComments(t *testing.T) {
	hostile := "synthetic--><script>"
	body := view{
		Trunk: "synthetic-trunk",
		Lines: []line{{Branch: "synthetic-trunk", Trunk: true}, {Branch: hostile, Number: 5, State: stateOpen}},
		Here:  5, Listed: []int{5},
	}.body()
	for _, comment := range []string{Marker, dataOpen + "5" + dataClose} {
		if !strings.Contains(body, comment) {
			t.Fatalf("body is missing %q:\n%s", comment, body)
		}
	}
	if strings.Count(body, "-->") != 2+strings.Count(hostile, "-->") {
		t.Errorf("unexpected comment terminators in:\n%s", body)
	}
	if !strings.Contains(body, "`"+hostile+"`") {
		t.Errorf("hostile name is not inside a code span:\n%s", body)
	}
	if !strings.HasPrefix(body, Marker+"\n") {
		t.Errorf("body does not open with the marker:\n%s", body)
	}
}
