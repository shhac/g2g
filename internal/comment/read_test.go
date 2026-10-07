package comment

import (
	"slices"
	"strconv"
	"strings"
	"testing"
)

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
