package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/githubstack"
	"github.com/shhac/g2g/internal/link"
	"github.com/shhac/g2g/internal/stack"
)

// GitHub wins when both could answer. Its address was reported rather than
// assembled, so it cannot be wrong about the repository the way a constructed
// one can.
func TestPullRequestPrefersGitHubOverGraphite(t *testing.T) {
	ref := pullRequestRef{Number: 42, URL: "https://github.com/synthetic-owner/synthetic-repo/pull/42", Repository: "synthetic-owner/synthetic-repo"}

	if got := pullRequestURL(ref); got != ref.URL {
		t.Errorf("pullRequestURL() = %q, want GitHub's own address %q", got, ref.URL)
	}
}

// Graphite answers only when GitHub did not, and builds the address from parts.
func TestPullRequestFallsBackToGraphite(t *testing.T) {
	ref := pullRequestRef{Number: 42, Repository: "synthetic-owner/synthetic-repo"}

	if got, want := pullRequestURL(ref), "https://app.graphite.com/github/pr/synthetic-owner/synthetic-repo/42"; got != want {
		t.Errorf("pullRequestURL() = %q, want %q", got, want)
	}
}

// A repository that does not use Graphite, or a reference too incomplete to
// build one from, simply does not link. Nothing degrades.
func TestPullRequestLinksNowhereWhenItCannotAnswer(t *testing.T) {
	for name, ref := range map[string]pullRequestRef{
		"no repository":                {Number: 42},
		"no number":                    {Repository: "synthetic-owner/synthetic-repo"},
		"nothing known":                {},
		"repository is not owner/name": {Number: 42, Repository: "synthetic-repo"},
	} {
		if got := pullRequestURL(ref); got != "" {
			t.Errorf("%s: pullRequestURL() = %q, want no link", name, got)
		}
	}
}

func TestRepositoryIsReadBackFromAPullRequestURL(t *testing.T) {
	for url, want := range map[string]string{
		"https://github.com/synthetic-owner/synthetic-repo/pull/42":         "synthetic-owner/synthetic-repo",
		"https://github.example.test/synthetic-owner/synthetic-repo/pull/1": "synthetic-owner/synthetic-repo",
		"https://github.com/synthetic-owner/synthetic-repo/issues/42":       "",
		"not-a-url": "",
		"":          "",
	} {
		if got := repositoryFromPullRequestURL(url); got != want {
			t.Errorf("repositoryFromPullRequestURL(%q) = %q, want %q", url, got, want)
		}
	}
}

// The capability and the policy are separate: asking for a link is
// unconditional at the call site, and presentation decides whether one appears.
func TestHyperlinkOnlyDecoratesWhenLinksAreEnabled(t *testing.T) {
	linked := Presentation{Links: true}.hyperlink("https://example.test/pr/1", "#1")
	if !strings.Contains(linked, "\x1b]8;;https://example.test/pr/1") || !strings.HasSuffix(linked, "\x1b]8;;\x1b\\") {
		t.Errorf("hyperlink() = %q, want an OSC 8 wrapper", linked)
	}
	if !strings.Contains(linked, "#1") {
		t.Errorf("hyperlink() dropped the text: %q", linked)
	}

	if got := (Presentation{}).hyperlink("https://example.test/pr/1", "#1"); got != "#1" {
		t.Errorf("hyperlink() with links off = %q, want the bare text", got)
	}
	if got := (Presentation{Links: true}).hyperlink("", "#1"); got != "#1" {
		t.Errorf("hyperlink() with no url = %q, want the bare text", got)
	}
}

// NO_COLOR asks for output without colour. A hyperlink is not colour: it adds
// no visible decoration and the text reads identically without it.
func TestNoColorSuppressesColourButNotLinks(t *testing.T) {
	if colorEnabled(true, "xterm", false, true) {
		t.Error("NO_COLOR did not suppress colour")
	}
	if !linksEnabled("xterm", false, true) {
		t.Error("NO_COLOR suppressed hyperlinks; it is a statement about colour")
	}
}

// Nothing that is not a person reading a terminal gets escape sequences.
func TestLinksAreOffWhereNobodyCanFollowThem(t *testing.T) {
	for name, enabled := range map[string]bool{
		"piped":         linksEnabled("xterm", false, false),
		"dumb terminal": linksEnabled("dumb", false, true),
		"CI":            linksEnabled("xterm", true, true),
	} {
		if enabled {
			t.Errorf("%s: links enabled where they cannot be followed", name)
		}
	}
}

func linkedStatusPlan() link.Plan {
	return link.Plan{Discovery: stack.Discovery{Snapshot: stack.Snapshot{
		Target: "synthetic-top", Base: "synthetic-trunk", Scope: stack.ScopePath, Source: stack.SourceGraphite,
		Branches: []string{"synthetic-top"},
	}, PullRequests: []githubstack.PullRequest{
		{Head: "synthetic-top", Number: 42, State: "OPEN", Base: "synthetic-trunk", URL: "https://github.com/synthetic-owner/synthetic-repo/pull/42"},
	}}}
}

// The number a person reads is unchanged; it merely becomes clickable.
func TestRenderedPullRequestNumberCarriesItsLink(t *testing.T) {
	var out bytes.Buffer
	if err := writeStatus(&out, linkedStatusPlan(), Presentation{Links: true}); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "\x1b]8;;https://github.com/synthetic-owner/synthetic-repo/pull/42") {
		t.Errorf("PR number is not linked:\n%q", got)
	}
	if !strings.Contains(got, "#42") {
		t.Errorf("PR number lost its text:\n%q", got)
	}
}

// Every machine format and every non-terminal must be byte-identical to what it
// produced before links existed. An escape sequence in --json is a broken
// document, not a decoration.
func TestMachineFormatsAndPlainOutputCarryNoEscapes(t *testing.T) {
	for name, p := range map[string]Presentation{
		"plain":     {},
		"json":      {Format: formatJSON},
		"porcelain": {Format: formatPorcelain},
		// Links can never be set alongside a machine format by resolve(), but
		// the renderer must not depend on that being true elsewhere.
		"json with links set": {Format: formatJSON, Links: true},
	} {
		var out bytes.Buffer
		if err := writeStatus(&out, linkedStatusPlan(), p); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if strings.Contains(out.String(), "\x1b") {
			t.Errorf("%s: output carries an escape sequence:\n%q", name, out.String())
		}
	}
}

// Asking for a link where none can be built must not change a single byte.
func TestOutputIsUnchangedWhenNothingCanBeLinked(t *testing.T) {
	plan := linkedStatusPlan()
	plan.PullRequests[0].URL = ""

	var linked, plain bytes.Buffer
	if err := writeStatus(&linked, plan, Presentation{Links: true}); err != nil {
		t.Fatal(err)
	}
	if err := writeStatus(&plain, plan, Presentation{}); err != nil {
		t.Fatal(err)
	}
	if linked.String() != plain.String() {
		t.Errorf("an unlinkable view differed with links on:\n%q\n%q", linked.String(), plain.String())
	}
}
