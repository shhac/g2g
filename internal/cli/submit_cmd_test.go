package cli

import (
	"testing"

	"github.com/spf13/cobra"
)

func TestResolveDraftPreservesSpecUnlessFlagExplicitlyOverridesIt(t *testing.T) {
	for _, test := range []struct {
		name      string
		specDraft bool
		args      []string
		want      bool
	}{
		{name: "ready spec remains ready", specDraft: false, want: false},
		{name: "draft spec remains draft", specDraft: true, want: true},
		{name: "explicit draft overrides ready spec", specDraft: false, args: []string{"--draft=true"}, want: true},
		{name: "ready overrides draft", specDraft: true, args: []string{"--ready"}, want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			cmd := &cobra.Command{}
			cmd.Flags().Bool("draft", true, "")
			cmd.Flags().Bool("ready", false, "")
			if err := cmd.Flags().Parse(test.args); err != nil {
				t.Fatal(err)
			}
			draft, _ := cmd.Flags().GetBool("draft")
			ready, _ := cmd.Flags().GetBool("ready")
			if got := resolveDraft(cmd, test.specDraft, draft, ready); got != test.want {
				t.Errorf("resolveDraft() = %t, want %t", got, test.want)
			}
		})
	}
}
