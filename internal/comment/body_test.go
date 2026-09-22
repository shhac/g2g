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
func TestRecordedInReadsWhatItCanAndIgnoresTheRest(t *testing.T) {
	for body, want := range map[string][]entry{
		"no data":                          nil,
		dataOpen + "3,1>3,2>1" + dataClose: {{3, 0}, {1, 3}, {2, 1}},
		dataOpen + " 4, x, -5, 0, 4 ,6>6,7>" + dataClose: {{4, 0}},
		dataOpen + "7,8": nil,
		"before " + dataOpen + "9>2" + dataClose + "\n": {{9, 2}},
	} {
		if got := recordedIn(body); !slices.Equal(got, want) {
			t.Errorf("recordedIn(%q) = %v, want %v", body, got, want)
		}
	}
	long := make([]string, 0, recordedLimit+50)
	for number := 1; number <= recordedLimit+50; number++ {
		long = append(long, strconv.Itoa(number))
	}
	if got := recordedIn(dataOpen + strings.Join(long, ",") + dataClose); len(got) != recordedLimit {
		t.Errorf("recordedIn read %d entries, want it bounded at %d", len(got), recordedLimit)
	}
	if encoded := encode([]entry{{11, 0}, {12, 11}}); encoded != "11,12>11" {
		t.Errorf("encode = %q", encoded)
	}
}

// Nothing a branch can be called reaches the HTML comments: only numbers are
// written inside them.
func TestBodyKeepsBranchNamesOutOfItsHTMLComments(t *testing.T) {
	hostile := "synthetic--><script>"
	body := view{
		Trunk: "synthetic-trunk",
		Lines: []line{{Branch: "synthetic-trunk", Trunk: true}, {Branch: hostile, Number: 5, State: StateOpen}},
		Here:  5, Recorded: []entry{{Number: 5}},
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
