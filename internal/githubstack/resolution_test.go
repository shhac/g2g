package githubstack

import (
	"strings"
	"testing"
)

func TestResolveHeadsAppliesOpenIsIdentity(t *testing.T) {
	tests := []struct {
		name       string
		prs        []PullRequest
		wantOpen   int
		wantCount  int
		superseded bool
		ambiguous  bool
	}{
		{
			name:      "single open pull request",
			prs:       []PullRequest{{Number: 3, Head: "synthetic-a", State: "OPEN"}},
			wantOpen:  3,
			wantCount: 1,
		},
		{
			// The case that used to block: a long-lived stack whose branch name
			// carries a closed pull request from an earlier submission.
			name:      "closed history plus one open",
			prs:       []PullRequest{{Number: 3, Head: "synthetic-a", State: "CLOSED"}, {Number: 11, Head: "synthetic-a", State: "OPEN"}},
			wantOpen:  11,
			wantCount: 1,
		},
		{
			name:      "merged history plus one open",
			prs:       []PullRequest{{Number: 4, Head: "synthetic-a", State: "MERGED"}, {Number: 12, Head: "synthetic-a", State: "OPEN"}},
			wantOpen:  12,
			wantCount: 1,
		},
		{
			name:       "only closed history",
			prs:        []PullRequest{{Number: 3, Head: "synthetic-a", State: "CLOSED"}, {Number: 7, Head: "synthetic-a", State: "CLOSED"}},
			wantCount:  0,
			superseded: true,
		},
		{
			name:      "two open pull requests is the only ambiguity",
			prs:       []PullRequest{{Number: 3, Head: "synthetic-a", State: "OPEN"}, {Number: 7, Head: "synthetic-a", State: "OPEN"}},
			wantCount: 2,
			ambiguous: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resolution := ResolveHeads(test.prs)["synthetic-a"]

			if resolution.OpenCount != test.wantCount {
				t.Errorf("OpenCount = %d, want %d", resolution.OpenCount, test.wantCount)
			}
			if resolution.Ambiguous() != test.ambiguous {
				t.Errorf("Ambiguous() = %t, want %t", resolution.Ambiguous(), test.ambiguous)
			}
			if resolution.Superseded() != test.superseded {
				t.Errorf("Superseded() = %t, want %t", resolution.Superseded(), test.superseded)
			}
			if test.wantOpen == 0 {
				if resolution.Open != nil {
					t.Errorf("Open = #%d, want none", resolution.Open.Number)
				}
				return
			}
			if resolution.Open == nil || resolution.Open.Number != test.wantOpen {
				t.Errorf("Open = %v, want #%d", resolution.Open, test.wantOpen)
			}
		})
	}
}

func TestResolveHeadsReportsNewestSupersededPullRequest(t *testing.T) {
	resolution := ResolveHeads([]PullRequest{
		{Number: 3, Head: "synthetic-a", State: "CLOSED"},
		{Number: 21, Head: "synthetic-a", State: "MERGED"},
		{Number: 8, Head: "synthetic-a", State: "CLOSED"},
	})["synthetic-a"]

	if resolution.Latest == nil || resolution.Latest.Number != 21 {
		t.Fatalf("Latest = %v, want #21", resolution.Latest)
	}
}

func TestResolveHeadsKeepsBranchesSeparate(t *testing.T) {
	resolutions := ResolveHeads([]PullRequest{
		{Number: 1, Head: "synthetic-a", State: "OPEN"},
		{Number: 2, Head: "synthetic-b", State: "CLOSED"},
	})

	if resolutions["synthetic-a"].Open == nil || resolutions["synthetic-a"].Open.Number != 1 {
		t.Errorf("synthetic-a resolved to %v", resolutions["synthetic-a"].Open)
	}
	if !resolutions["synthetic-b"].Superseded() {
		t.Errorf("synthetic-b = %#v, want superseded", resolutions["synthetic-b"])
	}
}

// ByHead feeds native-stack assessment, so it must prefer the open pull
// request rather than whichever match happened to be last in the response.
func TestByHeadPrefersTheOpenPullRequest(t *testing.T) {
	byHead := ByHead([]PullRequest{
		{Number: 11, Head: "synthetic-a", State: "OPEN", StackNumber: 5, StackSize: 2, StackPosition: 1},
		{Number: 30, Head: "synthetic-a", State: "CLOSED"},
	})

	if byHead["synthetic-a"].Number != 11 {
		t.Fatalf("ByHead resolved #%d, want the open #11", byHead["synthetic-a"].Number)
	}
}

func TestParsePullRequestsSkipsForeignHeadsInsteadOfFailing(t *testing.T) {
	output := []byte(`{"data":{"repository":{"pr0":{"nodes":[
		{"number":5,"headRefName":"synthetic-ab","baseRefName":"main","state":"OPEN"},
		{"number":6,"headRefName":"synthetic-a","baseRefName":"main","state":"OPEN"}
	]}}}}`)

	prs, err := parsePullRequests(output, []string{"synthetic-a"})
	if err != nil {
		t.Fatalf("parsePullRequests() error = %v", err)
	}
	if len(prs) != 1 || prs[0].Number != 6 {
		t.Fatalf("parsePullRequests() = %#v, want only #6", prs)
	}
}

func TestParsePullRequestsReportsUnreadableRepository(t *testing.T) {
	_, err := parsePullRequests([]byte(`{"data":{"repository":null}}`), []string{"synthetic-a"})
	if err == nil {
		t.Fatal("parsePullRequests() error = nil, want an unreadable-repository error")
	}
}

// Node validation is now reachable without building a whole GraphQL envelope,
// which is the point of extracting it.
func TestPullRequestNodeValidation(t *testing.T) {
	valid := pullRequestNode{Number: 7, Head: "synthetic-a", Base: "main", State: "OPEN"}
	stacked := valid
	stacked.Stack = &struct {
		Number int `json:"number"`
		Size   int `json:"size"`
	}{Number: 3, Size: 2}
	stacked.StackEntry = &struct {
		Position int `json:"position"`
	}{Position: 1}

	orphanEntry := valid
	orphanEntry.StackEntry = stacked.StackEntry

	outOfRange := stacked
	outOfRange.StackEntry = &struct {
		Position int `json:"position"`
	}{Position: 3}

	for name, test := range map[string]struct {
		node    pullRequestNode
		wantErr string
	}{
		"valid":                 {node: valid},
		"valid with membership": {node: stacked},
		"zero number":           {node: pullRequestNode{Head: "a", Base: "main", State: "OPEN"}, wantErr: "invalid"},
		"missing base":          {node: pullRequestNode{Number: 1, Head: "a", State: "OPEN"}, wantErr: "invalid"},
		"missing state":         {node: pullRequestNode{Number: 1, Head: "a", Base: "main"}, wantErr: "invalid"},
		"entry without stack":   {node: orphanEntry, wantErr: "incomplete"},
		"position beyond size":  {node: outOfRange, wantErr: "invalid native stack"},
	} {
		t.Run(name, func(t *testing.T) {
			pr, err := test.node.pullRequest("pr0")
			if test.wantErr == "" {
				if err != nil {
					t.Fatalf("pullRequest() error = %v", err)
				}
				if pr.Number != test.node.Number {
					t.Errorf("Number = %d, want %d", pr.Number, test.node.Number)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("pullRequest() error = %v, want it to mention %q", err, test.wantErr)
			}
		})
	}
}
