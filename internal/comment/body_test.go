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

// recordedLine is a data line in the format this g2g writes.
func recordedLine(list string) string {
	return dataOpen + "v=" + dataVersion + " prs=" + list + dataClose
}

// The recorded numbers are editable by anyone who can edit the pull request,
// so reading them back tolerates what a person might leave there.
func TestRecordedInReadsWhatItCanAndIgnoresTheRest(t *testing.T) {
	for body, want := range map[string][]entry{
		"no data":                                     nil,
		recordedLine("3,1>3,2>1"):                     {{3, 0}, {1, 3}, {2, 1}},
		recordedLine("4,x,-5,0,4,6>6,7>"):             {{4, 0}},
		dataOpen + "v=1 prs=7,8":                      nil,
		"before " + recordedLine("9>2") + "\n":        {{9, 2}},
		dataOpen + "prs=10,11>10 synthetic=x v=1 -->": {{10, 0}, {11, 10}},
		dataOpen + "v=1 -->":                          {},
		dataOpen + "v=2 prs=12,13>12 -->":             nil,
		dataOpen + "v=x prs=12 -->":                   nil,
	} {
		if got := recordedIn(body); !slices.Equal(got, want) {
			t.Errorf("recordedIn(%q) = %v, want %v", body, got, want)
		}
	}
	long := make([]string, 0, recordedLimit+50)
	for number := 1; number <= recordedLimit+50; number++ {
		long = append(long, strconv.Itoa(number))
	}
	if got := recordedIn(recordedLine(strings.Join(long, ","))); len(got) != recordedLimit {
		t.Errorf("recordedIn read %d entries, want it bounded at %d", len(got), recordedLimit)
	}
	if encoded := encode([]entry{{11, 0}, {12, 11}}); encoded != "11,12>11" {
		t.Errorf("encode = %q", encoded)
	}
}

// A data line with no `v` is from 0.43.0 or earlier, and is read as the bare
// list it was then. This goes when recordedList's legacy case does.
func TestRecordedInReadsTheListBeforeItHadFields(t *testing.T) {
	for body, want := range map[string][]entry{
		dataOpen + "3,1>3,2>1" + dataClose:               {{3, 0}, {1, 3}, {2, 1}},
		dataOpen + " 4, x, -5, 0, 4 ,6>6,7>" + dataClose: {{4, 0}},
		dataOpen + "7,8": nil,
	} {
		if got := recordedIn(body); !slices.Equal(got, want) {
			t.Errorf("recordedIn(%q) = %v, want %v", body, got, want)
		}
		if !readable(body) {
			t.Errorf("readable(%q) = false, want a list with no version to be read", body)
		}
	}
}

// A comment in a format this g2g does not know is one it cannot rewrite
// without losing the history it could not read.
func TestReadableKnowsOnlyItsOwnFormat(t *testing.T) {
	for body, want := range map[string]bool{
		Marker + " -->\n**Stack**\n":        true,
		recordedLine("11,12>11"):            true,
		dataOpen + "11,12>11" + dataClose:   true,
		dataOpen + "v=2 prs=11" + dataClose: false,
		dataOpen + "v= prs=11" + dataClose:  false,
	} {
		if got := readable(body); got != want {
			t.Errorf("readable(%q) = %v, want %v", body, got, want)
		}
	}
}

// GitHub may report no head for a merged pull request; an empty code span
// would render as two stray backticks.
func TestBodyNamesAMergedPullRequestWithNoBranchByNumber(t *testing.T) {
	body := view{
		Lines: []line{{Branch: "synthetic-trunk", Trunk: true}, {Number: 4, State: StateMerged}, {Branch: "synthetic-open", Number: 5, State: StateOpen}},
		Here:  5, Recorded: []entry{{Number: 4}, {Number: 5}},
	}.body()
	if !strings.Contains(body, "- #4 · merged\n") || strings.Contains(body, "``") {
		t.Errorf("a merged pull request with no branch is not named by its number alone:\n%s", body)
	}
}

// Nothing a branch can be called reaches the HTML comments: only numbers are
// written inside them.
func TestBodyKeepsBranchNamesOutOfItsHTMLComments(t *testing.T) {
	hostile := "synthetic--><script>"
	body := view{
		Lines: []line{{Branch: "synthetic-trunk", Trunk: true}, {Branch: hostile, Number: 5, State: StateOpen}},
		Here:  5, Recorded: []entry{{Number: 5}},
	}.body()
	for _, comment := range []string{Marker, recordedLine("5")} {
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
	if revIn(body) == "" {
		t.Errorf("body does not open with the marker and its rev:\n%s", body)
	}
}

// The version sits beside the rev without being part of it, and nothing a
// build calls itself can close the HTML comment or run into the next field.
func TestBodyRecordsTheVersionBesideTheRev(t *testing.T) {
	written := func(version string) string {
		return view{
			Lines:   []line{{Branch: "synthetic-trunk", Trunk: true}, {Branch: "synthetic-one", Number: 5, State: StateOpen}},
			Here:    5,
			Version: version,
		}.body()
	}
	body := written("1.0.0")
	if got := markerFields(body)["version"]; got != "1.0.0" {
		t.Errorf("version = %q, want 1.0.0:\n%s", got, body)
	}
	if revIn(body) == "" || revIn(body) != revIn(written("2.0.0")) {
		t.Errorf("the version reached the rev:\n%s", body)
	}
	for _, version := range []string{"", "synthetic-->", "synthetic version", "synthetic rev=0000000000000000"} {
		body := written(version)
		if fields := markerFields(body); fields["version"] != "" || !revShape.MatchString(fields["rev"]) {
			t.Errorf("version %q: fields = %v:\n%s", version, fields, body)
		}
	}
}

// The marker is read by the fields it names, so a comment from a g2g that
// writes fields this one has never heard of, or in another order, is still
// one whose rev it can read.
func TestRevIsReadByNameFromTheMarker(t *testing.T) {
	const r = "0123456789abcdef"
	for first, want := range map[string]string{
		Marker + " rev=" + r + " -->":                           r,
		Marker + " rev=" + r + " version=1.0.0 -->":             r,
		Marker + " version=1.0.0 synthetic=x rev=" + r + " -->": r,
		Marker + " synthetic rev=" + r + "-->":                  r,
		"  " + Marker + "  rev=" + r + "   -->  ":               r,
		Marker + " -->":                                    "",
		Marker + " rev=" + r:                               "",
		Marker + " rev=0123 -->":                           "",
		Marker + " rev=" + r + "0 -->":                     "",
		Marker + "x rev=" + r + " -->":                     "",
		"synthetic words " + Marker + " rev=" + r + " -->": "",
	} {
		if got := revIn(first + "\n**Stack**\n"); got != want {
			t.Errorf("revIn(%q) = %q, want %q", first, got, want)
		}
	}
}

// Only a release has notes to link to; any other build links to the homepage,
// and one with no version says nothing about who last updated the comment.
func TestFooterLinksTheVersionThatWroteIt(t *testing.T) {
	for version, want := range map[string]string{
		"1.2.3":       " · last updated by [g2g@1.2.3](https://github.com/shhac/g2g/releases/tag/v1.2.3)</sub>",
		"dev":         " · last updated by [g2g@dev](https://g2g.foo)</sub>",
		"1.2.3-rc.1":  " · last updated by [g2g@1.2.3-rc.1](https://g2g.foo)</sub>",
		"":            "when the stack changes</sub>",
		"synthetic](": "when the stack changes</sub>",
	} {
		body := view{Lines: []line{{Branch: "synthetic-trunk", Trunk: true}}, Version: version}.body()
		if !strings.Contains(body, want) {
			t.Errorf("version %q: footer does not end %q:\n%s", version, want, body)
		}
	}
}
