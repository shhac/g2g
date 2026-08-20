package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/githubstack"
	"github.com/shhac/g2g/internal/link"
	"github.com/shhac/g2g/internal/stack"
)

func membershipPlan(prs ...githubstack.PullRequest) link.Plan {
	return link.Plan{Discovery: stack.Discovery{Snapshot: stack.Snapshot{Target: "synthetic-top", Base: "synthetic-main", Branches: []string{"synthetic-lower", "synthetic-top"}}, PullRequests: prs}}
}

func linkedPair() []githubstack.PullRequest {
	return []githubstack.PullRequest{
		{Head: "synthetic-lower", Number: 101, State: "OPEN", StackNumber: 42, StackSize: 2, StackPosition: 1},
		{Head: "synthetic-top", Number: 102, State: "OPEN", StackNumber: 42, StackSize: 2, StackPosition: 2},
	}
}

// status reports native membership from the same batched read, so requiring
// the number by hand made the user copy a value the command already had.
func TestUnlinkDiscoversTheStackNumberFromTheSelectedPath(t *testing.T) {
	number, source, err := resolveStackNumber(0, membershipPlan(linkedPair()...))
	if err != nil {
		t.Fatalf("resolveStackNumber() error = %v", err)
	}
	if number != 42 {
		t.Errorf("number = %d, want 42", number)
	}
	if source != "discovered on the selected path" {
		t.Errorf("source = %q", source)
	}
}

// A partially linked path still names exactly one stack, which is the one an
// unlink would remove.
func TestUnlinkDiscoversTheStackNumberFromAPartiallyLinkedPath(t *testing.T) {
	prs := linkedPair()
	prs[1] = githubstack.PullRequest{Head: "synthetic-top", Number: 102, State: "OPEN"}

	number, _, err := resolveStackNumber(0, membershipPlan(prs...))
	if err != nil {
		t.Fatalf("resolveStackNumber() error = %v", err)
	}
	if number != 42 {
		t.Errorf("number = %d, want 42", number)
	}
}

func TestUnlinkPrefersAnExplicitStackNumber(t *testing.T) {
	number, source, err := resolveStackNumber(7, membershipPlan(linkedPair()...))
	if err != nil {
		t.Fatalf("resolveStackNumber() error = %v", err)
	}
	if number != 7 || source != "--stack-number" {
		t.Errorf("number = %d from %q, want 7 from --stack-number", number, source)
	}
}

// Discovery must refuse rather than guess: unlink is a mutation, and picking a
// stack number for an ambiguous or unlinked path could remove the wrong one.
func TestUnlinkRefusesToGuessAStackNumber(t *testing.T) {
	conflicting := linkedPair()
	conflicting[1].StackPosition = 1

	for name, test := range map[string]struct {
		plan link.Plan
		want string
	}{
		"unlinked": {
			plan: membershipPlan(
				githubstack.PullRequest{Head: "synthetic-lower", Number: 101, State: "OPEN"},
				githubstack.PullRequest{Head: "synthetic-top", Number: 102, State: "OPEN"},
			),
			want: "nothing to unlink",
		},
		"conflicting": {plan: membershipPlan(conflicting...), want: "--stack-number"},
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := resolveStackNumber(0, test.plan)
			if err == nil {
				t.Fatal("resolveStackNumber() error = nil, want a refusal")
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Errorf("error = %q, want it to mention %q", err, test.want)
			}
		})
	}
}

// The number rendered immediately before the mutation has to be the revalidated
// plan's, not the preview's.
//
// It used to live in variables the closures captured, so render was not a
// function of the plan it was handed: the number printed came from whichever
// resolution ran last, and only link.Revalidate refusing any inequality kept
// that honest — a property asserted two files away.
func TestUnlinkResolvesTheStackNumberFromEachPlan(t *testing.T) {
	plan := link.Plan{Discovery: stack.Discovery{
		Snapshot: stack.Snapshot{Target: "synthetic-top", Base: "main", Branches: []string{"synthetic-top"}},
		PullRequests: []githubstack.PullRequest{
			{Head: "synthetic-top", Number: 12, State: "OPEN", StackNumber: 77, StackSize: 1, StackPosition: 1},
		},
	}}

	resolved, err := newUnlinkPlan(0, plan)
	if err != nil {
		t.Fatalf("newUnlinkPlan() error = %v", err)
	}
	if resolved.Number != 77 {
		t.Errorf("Number = %d, want the discovered stack number", resolved.Number)
	}

	// Render reads only what it was handed, so a plan carrying a different
	// number renders that one.
	var out bytes.Buffer
	other := resolved
	other.Number, other.Source = 99, "--stack-number"
	if err := writeUnlinkPlan(&out, other.Plan, other.Number, other.Source, Presentation{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "99") {
		t.Errorf("render did not use the number it was handed:\n%s", out.String())
	}
}

// A plan with unresolved pull request mappings never reaches a number at all.
func TestUnlinkRefusesAPlanWithUnresolvedMappings(t *testing.T) {
	plan := link.Plan{
		Discovery: stack.Discovery{Snapshot: stack.Snapshot{Target: "synthetic-top", Base: "main", Branches: []string{"synthetic-top"}}},
		Issues:    []link.Issue{{Branch: "synthetic-top", Kind: link.IssueMissing, Reason: "no open PR"}},
	}

	if _, err := newUnlinkPlan(0, plan); err == nil {
		t.Error("newUnlinkPlan() = nil for a plan with unresolved mappings")
	}
}
