package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/stack"
	"github.com/shhac/g2g/internal/submit"
)

// Draft is the default and --ready is the only way to ask for the irreversible
// thing. There is deliberately no --draft: nothing needs opting into about a
// draft, which can be marked ready at any time.
func TestResolveDraftDefaultsToDraftAndOptsIntoReady(t *testing.T) {
	for _, test := range []struct {
		name      string
		specDraft bool
		ready     bool
		noReady   bool
		want      bool
	}{
		{"a fresh spec opens drafts", submit.DefaultDraft, false, false, true},
		{"--ready opts into ready", submit.DefaultDraft, true, false, false},
		{"--ready overrides a spec asking for draft", true, true, false, false},
		{"a spec asking for ready is honoured", false, false, false, false},
		{"--no-ready overrules a spec asking for ready", false, false, true, true},
		{"--no-ready is redundant but harmless on a draft spec", true, false, true, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := resolveDraft(test.specDraft, test.ready, test.noReady); got != test.want {
				t.Errorf("resolveDraft(spec=%v, ready=%v, noReady=%v) = %v, want %v",
					test.specDraft, test.ready, test.noReady, got, test.want)
			}
		})
	}
}

// The default has to be the reversible one, and it is worth asserting directly
// rather than only through the table above.
func TestAFreshSpecAsksForDrafts(t *testing.T) {
	if !submit.DefaultDraft {
		t.Error("DefaultDraft is false; opening ready for review notifies reviewers and cannot be undone")
	}
	if spec := submit.NewSpec([]string{"synthetic-top"}, ""); !spec.Draft {
		t.Error("NewSpec produced a spec that opens pull requests ready for review")
	}
}

func submitPreview(t *testing.T, draft bool) string {
	t.Helper()

	plan := submit.Plan{Snapshot: stack.Snapshot{
		Target: "synthetic-top", Base: "synthetic-trunk",
		Branches: []string{"synthetic-lower", "synthetic-top"},
	}}
	var out bytes.Buffer
	if err := writeSubmitPreview(&out, plan, Presentation{}, "", draft, false, false); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

// applyFlow renders and flushes this preview immediately before mutating, so it
// is the last thing a person reads before reviewers are notified. Saying
// "draft" while about to open ready for review is not a cosmetic error.
func TestSubmitPreviewSaysWhatItIsAboutToOpen(t *testing.T) {
	drafts := submitPreview(t, true)
	if !strings.Contains(drafts, "create draft") || !strings.Contains(drafts, "as drafts") {
		t.Errorf("draft preview does not say draft:\n%s", drafts)
	}
	if strings.Contains(drafts, "ready for review") {
		t.Errorf("draft preview mentions ready for review:\n%s", drafts)
	}

	ready := submitPreview(t, false)
	if !strings.Contains(ready, "create ready for review") {
		t.Errorf("ready preview still announces drafts per node:\n%s", ready)
	}
	if strings.Contains(ready, "as drafts") {
		t.Errorf("ready preview still claims PRs will be drafts:\n%s", ready)
	}
}

// A copied command has to reproduce the run that was previewed, so the choice
// echoes back into whatever the preview suggests next.
func TestSuggestedCommandCarriesTheReadyChoice(t *testing.T) {
	if got := readyFlag(true); got != "" {
		t.Errorf("readyFlag(draft) = %q, want nothing to add", got)
	}
	if got := readyFlag(false); got != " --ready" {
		t.Errorf("readyFlag(ready) = %q, want \" --ready\"", got)
	}
}

// A stack of one pull request gets no stack comment, so a preview promising one
// on each pull request would leave a reader looking for a write that failed.
func TestSubmitPreviewSaysAStackOfOneGetsNoComment(t *testing.T) {
	for _, test := range []struct {
		branches []string
		want     string
	}{
		{[]string{"synthetic-top"}, "which a stack listing only one pull request does not get"},
		{[]string{"synthetic-lower", "synthetic-top"}, "Then keeps the stack comment on each pull request"},
	} {
		plan := submit.Plan{Snapshot: stack.Snapshot{Target: "synthetic-top", Base: "synthetic-trunk", Branches: test.branches}}
		var out bytes.Buffer
		if err := writeSubmitPreview(&out, plan, Presentation{}, "", true, false, true); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.String(), test.want) {
			t.Errorf("%d branches: preview missing %q:\n%s", len(test.branches), test.want, out.String())
		}
	}
}
