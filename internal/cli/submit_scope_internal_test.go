package cli

import (
	"io"
	"strings"
	"testing"
)

func TestSubmitScopeCompletionAndHelpOfferOnlyStackAndPath(t *testing.T) {
	root := New("v0.0.0-test", io.Discard, io.Discard)
	cmd, _, err := root.Find([]string{"submit"})
	if err != nil {
		t.Fatal(err)
	}
	complete, ok := cmd.GetFlagCompletionFunc("scope")
	if !ok {
		t.Fatal("submit has no scope completion")
	}
	if values, _ := complete(cmd, nil, ""); strings.Join(values, ",") != "stack,path" {
		t.Fatalf("submit scope completion = %v, want stack,path", values)
	}
	usage := cmd.Flags().Lookup("scope").Usage
	if strings.Contains(usage, "branch (just this branch)") || !strings.Contains(usage, "stack (") || !strings.Contains(usage, "path (") {
		t.Fatalf("submit scope help offers the wrong scopes: %s", usage)
	}
}
