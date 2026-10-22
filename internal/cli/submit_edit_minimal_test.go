package cli

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/shhac/g2g/internal/githubstack"
	"github.com/shhac/g2g/internal/stack"
	"github.com/shhac/g2g/internal/submit"
	"github.com/spf13/cobra"
)

func TestEditedSpecOnlyRequestsMissingMetadata(t *testing.T) {
	t.Setenv("EDITOR", "true")
	cmd := &cobra.Command{}
	cmd.SetOut(io.Discard)
	plan := submit.Plan{Snapshot: stack.Snapshot{Branches: []string{"synthetic-existing", "synthetic-missing"}}, Existing: []githubstack.PullRequest{{Head: "synthetic-existing", State: "OPEN"}}}
	path, err := (submitOptions{}).editedSpec(context.Background(), cmd, plan, "synthetic template")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(filepath.Dir(path))
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var spec submit.Spec
	if err := json.Unmarshal(data, &spec); err != nil {
		t.Fatal(err)
	}
	if len(spec.Pulls) != 1 || spec.Pulls[0].Branch != "synthetic-missing" || spec.Pulls[0].Body != "synthetic template" {
		t.Fatalf("edited spec %#v", spec)
	}
}
