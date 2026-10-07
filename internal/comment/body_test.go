package comment

import (
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
