package cli

import (
	"testing"

	"github.com/spf13/cobra"
)

// draftCommand builds a command whose --draft flag has been set or not, because
// resolveDraft's middle rule turns on whether the user actually typed it rather
// than on its value.
func draftCommand(t *testing.T, typed bool) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{}
	var draft bool
	cmd.Flags().BoolVar(&draft, "draft", false, "")
	if typed {
		if err := cmd.Flags().Set("draft", "true"); err != nil {
			t.Fatalf("Set(draft) error = %v", err)
		}
	}
	return cmd
}

// Three inputs decide one boolean, and getting the order wrong publishes a
// pull request that was meant to stay a draft — or holds one back that was
// meant to go out.
func TestResolveDraftPrecedence(t *testing.T) {
	for _, test := range []struct {
		name       string
		typedDraft bool
		specDraft  bool
		draft      bool
		ready      bool
		want       bool
	}{
		// --ready is the only explicit "publish it", so it wins outright.
		{"ready beats an explicit draft flag", true, true, true, true, false},
		{"ready beats the spec", false, true, false, true, false},
		// A typed --draft outranks whatever the spec document says.
		{"typed draft overrides a spec that says ready", true, false, true, false, true},
		{"typed draft=false is still an answer", true, true, false, false, false},
		// Untyped falls through to the spec, which is the document the user
		// edited on purpose.
		{"spec decides when the flag was not typed", false, true, false, false, true},
		{"spec decides when it says ready", false, false, false, false, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := resolveDraft(draftCommand(t, test.typedDraft), test.specDraft, test.draft, test.ready)
			if got != test.want {
				t.Errorf("resolveDraft(specDraft=%v, draft=%v, ready=%v) = %v, want %v",
					test.specDraft, test.draft, test.ready, got, test.want)
			}
		})
	}
}
