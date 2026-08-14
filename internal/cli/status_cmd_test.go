package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/shhac/gt2gh/internal/githubstack"
	"github.com/shhac/gt2gh/internal/link"
)

func TestStatusRendersCompactAlignedAndBlockedPath(t *testing.T) {
	plan := link.Plan{Target: "synthetic/top", Base: "main", Branches: []string{"synthetic/lower", "synthetic/top"}, PullRequests: []githubstack.PullRequest{{Head: "synthetic/lower", Number: 11, State: "OPEN"}, {Head: "synthetic/top", Number: 12, State: "OPEN"}}, Issues: []link.Issue{{Branch: "synthetic/top", Reason: "no open pull request"}}}
	var out bytes.Buffer
	if err := writeStatus(&out, plan, Presentation{}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Target: synthetic/top", "main (trunk)", "synthetic/lower (#11) [aligned]", "synthetic/top [blocked: no open pull request]", "Safe next action: repair"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q in %q", want, out.String())
		}
	}
}
