package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/submit"
)

func TestSubmitWritesOnlyMissingMetadataAndCreatesOnExistingParent(t *testing.T) {
	recorder := fakeRepository(t, "")
	dir := t.TempDir()
	if _, _, err := run(t, "submit", "--write-spec", dir); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "submission.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var spec submit.Spec
	if err := json.Unmarshal(data, &spec); err != nil {
		t.Fatal(err)
	}
	if len(spec.Pulls) != 1 || spec.Pulls[0].Branch != "synthetic-top" {
		t.Fatalf("generated spec %#v", spec)
	}
	spec.Pulls[0].Title = "Synthetic top"
	data, err = json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := run(t, "submit", "--spec", path, "--apply", "--no-comment"); err != nil {
		t.Fatal(err)
	}
	create := recorder.Find("gh pr create")
	if create == "" || !strings.Contains(create, "--base synthetic-lower") {
		t.Fatalf("PR creation calls %#v", create)
	}
}

func TestMissingSpecPreviewReportsBlockedForMachines(t *testing.T) {
	recorder := fakeRepository(t, "")
	out, _, err := run(t, "submit", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var view struct {
		Blocked string `json:"blocked"`
		Repair  struct {
			Reason string `json:"reason"`
			Ways   []struct {
				Command string `json:"command"`
			} `json:"ways"`
		} `json:"repair"`
	}
	if err := json.Unmarshal([]byte(out), &view); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(view.Blocked, "submission spec") || len(view.Repair.Ways) != 1 || !strings.Contains(view.Repair.Ways[0].Command, "--write-spec") {
		t.Fatalf("machine preview %s", out)
	}
	recorder.AssertNone("git push", "gh pr create")
}
