package cli

import (
	"bytes"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/stack"
	"github.com/shhac/g2g/internal/submit"
	"github.com/spf13/cobra"
)

func TestSubmitRetryHintsKeepSelectionAndPublicationFlags(t *testing.T) {
	o := submitOptions{
		selection: stackOptions{scopeOptions: scopeOptions{branch: "synthetic-target", scope: "path"}, trunk: "synthetic-main", from: "g2g"},
		remote:    "synthetic-remote", link: true, noComment: true, noSetUpstream: true,
		writeSpec: filepath.Join(t.TempDir(), "synthetic spec"),
	}
	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&out)
	plan := submit.Plan{Snapshot: stack.Snapshot{Branches: []string{"synthetic-target"}}}
	if err := o.writeDraft(cmd, plan, "", true, Presentation{}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(o.writeSpec, "submission.json")
	repair := o.actionableSpecError(fmt.Errorf("synthetic invalid spec"), path).Error()
	for _, text := range []string{out.String(), repair, o.retryCommand("--spec", path, "--apply")} {
		for _, want := range []string{"--branch synthetic-target", "--scope path", "--trunk synthetic-main", "--from g2g", "--remote synthetic-remote", "--link", "--no-comment", "--no-set-upstream", "--spec '" + path + "'"} {
			if !strings.Contains(text, want) {
				t.Errorf("hint lost %q:\n%s", want, text)
			}
		}
	}
}
