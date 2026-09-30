package push

import (
	"strings"
	"testing"
)

func TestReplacementAdvicePinsOnlyRejectedBranchesToObservedTips(t *testing.T) {
	note := blockedBy("synthetic-remote", []string{"synthetic-a", "synthetic-b", "synthetic-c"},
		map[string]Publication{"synthetic-a": {Standing: Behind}, "synthetic-b": {Standing: Current}, "synthetic-c": {Standing: Diverged}},
		map[string]string{"synthetic-a": strings.Repeat("a", 40), "synthetic-b": strings.Repeat("b", 40), "synthetic-c": strings.Repeat("c", 40)})
	want := "git push --atomic --force-with-lease=refs/heads/synthetic-a:" + strings.Repeat("a", 40) + " --force-with-lease=refs/heads/synthetic-c:" + strings.Repeat("c", 40) + " synthetic-remote synthetic-a synthetic-c"
	if got := note.Ways[1].Command; got != want {
		t.Fatalf("replacement command = %s, want %s", got, want)
	}
	if !strings.Contains(note.Ways[1].Effect, "dropping what the remote has") {
		t.Fatal("replacement advice hid its destructive effect")
	}
}
