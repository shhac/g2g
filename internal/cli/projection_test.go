package cli_test

import (
	"encoding/json"
	"strings"
	"testing"
)

// synthetic-lower carries two branches, so every selection that includes it
// and both of them forks.
const forkedGraph = `{"storeSchemaVersion":1,"trunks":["synthetic-trunk","synthetic-other"],"branches":{
	"synthetic-lower":{"parent":"synthetic-trunk","origin":"user"},
	"synthetic-top":{"parent":"synthetic-lower","origin":"user"},
	"synthetic-side":{"parent":"synthetic-lower","origin":"user"}}}`

// Every pull request correctly based, so nothing but the shape could refuse.
// Each alias carries all three nodes; the head filter keeps the one it asked
// about, whatever order the branches are selected in.
const forkedNodes = `{"nodes":[` +
	`{"id":"PR_synthetic_301","number":301,"url":"https://example.test/301","headRefName":"synthetic-lower","baseRefName":"synthetic-trunk","state":"OPEN"},` +
	`{"id":"PR_synthetic_302","number":302,"url":"https://example.test/302","headRefName":"synthetic-top","baseRefName":"synthetic-lower","state":"OPEN"},` +
	`{"id":"PR_synthetic_303","number":303,"url":"https://example.test/303","headRefName":"synthetic-side","baseRefName":"synthetic-lower","state":"OPEN"}]}`

const forkedPullRequests = `{"data":{"repository":{"pr0":` + forkedNodes + `,"pr1":` + forkedNodes + `,"pr2":` + forkedNodes + `}}}`

// GitHub's native stack is one ordered list, so a command that links one
// cannot represent a fork. link used to link the siblings as a chain.
func TestLinkingRefusesAFork(t *testing.T) {
	for _, test := range []struct {
		args     []string
		mutation string
	}{
		{args: []string{"github", "link", "--branch", "synthetic-lower", "--apply"}, mutation: "gh stack link"},
		{args: []string{"submit", "--branch", "synthetic-lower", "--link", "--apply"}, mutation: "git push"},
	} {
		t.Run(strings.Join(test.args[:2], " "), func(t *testing.T) {
			recorder, _ := g2gOwnedRepositoryWithPullRequests(t, forkedGraph, forkedPullRequests)

			stdout, _, err := run(t, test.args...)
			if err == nil || !strings.Contains(err.Error()+stdout, "more than one branch above it") {
				t.Fatalf("%v: error = %v, want the fork refused\n%s", test.args, err, stdout)
			}
			if !strings.Contains(err.Error()+stdout, "--scope path") {
				t.Errorf("refusal does not name the way out: %v\n%s", err, stdout)
			}
			recorder.AssertNone(test.mutation, "gh pr create")
		})
	}
}

// A pull request has one base, and on a fork it is the branch the pull
// request's branch is recorded on, so submit and retarget take a fork whole.
// They used to treat it as a line: retarget moved a correctly based pull
// request onto its sibling. With every base already right, neither moves one.
func TestSubmitAndRetargetLeaveAForkCorrectlyBased(t *testing.T) {
	for _, args := range [][]string{
		{"github", "retarget", "--branch", "synthetic-lower", "--apply"},
		{"submit", "--branch", "synthetic-lower"},
	} {
		t.Run(args[0], func(t *testing.T) {
			recorder, _ := g2gOwnedRepositoryWithPullRequests(t, forkedGraph, forkedPullRequests)

			stdout, _, err := run(t, args...)
			if err != nil {
				t.Fatalf("%v: %v\n%s", args, err, stdout)
			}
			for _, branch := range []string{"synthetic-top", "synthetic-side"} {
				if !strings.Contains(stdout, branch) {
					t.Errorf("%v does not show %s:\n%s", args, branch, stdout)
				}
			}
			recorder.AssertNone("gh "+retargetMutationPrefix, "gh pr create", "gh pr edit")
		})
	}
}

// push is not a projection: one atomic push has no order to keep, so the same
// fork linking refuses is published whole.
func TestPushPublishesTheForkLinkingRefuses(t *testing.T) {
	recorder, _ := g2gOwnedRepositoryWithPullRequests(t, forkedGraph, forkedPullRequests)

	stdout, _, err := run(t, "push", "--branch", "synthetic-lower", "--apply")
	if err != nil {
		t.Fatalf("push error = %v\n%s", err, stdout)
	}
	pushed := recorder.Find("git push --atomic")
	for _, branch := range []string{"synthetic-lower", "synthetic-top", "synthetic-side"} {
		if !strings.Contains(pushed, " "+branch) {
			t.Errorf("push %q does not publish %s", pushed, branch)
		}
	}
}

// Reading a fork is the ordinary case, and status is how a person sees one.
func TestStatusStillReadsAFork(t *testing.T) {
	g2gOwnedRepositoryWithPullRequests(t, forkedGraph, forkedPullRequests)

	stdout, _, err := run(t, "github", "status", "--branch", "synthetic-lower")
	if err != nil {
		t.Fatalf("status: %v\n%s", err, stdout)
	}
	for _, branch := range []string{"synthetic-top", "synthetic-side"} {
		if !strings.Contains(stdout, branch) {
			t.Errorf("status omits %s:\n%s", branch, stdout)
		}
	}
}

// A machine is told to read repair rather than parse blocked, so a blocked
// plan with a way out has to carry one.
func TestBlockedPlansCarryTheirRepairForAMachine(t *testing.T) {
	missing := `{"data":{"repository":{` +
		`"pr0":{"nodes":[{"id":"PR_synthetic_201","number":201,"url":"https://example.test/201","headRefName":"synthetic-lower","baseRefName":"synthetic-trunk","state":"OPEN"}]},` +
		`"pr1":{"nodes":[]}}}}`
	for _, command := range []string{"github status", "github link"} {
		t.Run(command, func(t *testing.T) {
			g2gOwnedRepositoryWithPullRequests(t, ownedGraph, missing)

			stdout, _, err := run(t, append(strings.Fields(command), "--json")...)
			if err != nil {
				t.Fatalf("%s --json: %v\n%s", command, err, stdout)
			}
			var document struct {
				Blocked string `json:"blocked"`
				Repair  *struct {
					Ways []struct {
						Command string `json:"command"`
					} `json:"ways"`
				} `json:"repair"`
			}
			if err := json.Unmarshal([]byte(stdout), &document); err != nil {
				t.Fatalf("not JSON: %v\n%s", err, stdout)
			}
			if document.Blocked == "" || document.Repair == nil || len(document.Repair.Ways) != 1 || document.Repair.Ways[0].Command != "g2g submit" {
				t.Errorf("document = %s, want blocked with a repair naming g2g submit", stdout)
			}
		})
	}
}

// A blocked preview must not close by inviting an apply that will refuse.
func TestBlockedPreviewsDoNotInviteAnApply(t *testing.T) {
	missing := `{"data":{"repository":{` +
		`"pr0":{"nodes":[{"id":"PR_synthetic_201","number":201,"url":"https://example.test/201","headRefName":"synthetic-lower","baseRefName":"synthetic-trunk","state":"OPEN"},{"id":"PR_synthetic_209","number":209,"url":"https://example.test/209","headRefName":"synthetic-lower","baseRefName":"synthetic-trunk","state":"OPEN"}]},` +
		`"pr1":{"nodes":[{"id":"PR_synthetic_202","number":202,"url":"https://example.test/202","headRefName":"synthetic-top","baseRefName":"synthetic-trunk","state":"OPEN"}]}}}}`
	for _, command := range []string{"github link", "github retarget"} {
		t.Run(command, func(t *testing.T) {
			g2gOwnedRepositoryWithPullRequests(t, ownedGraph, missing)

			stdout, _, err := run(t, strings.Fields(command)...)
			if err != nil {
				t.Fatalf("%s: %v\n%s", command, err, stdout)
			}
			if strings.Contains(stdout, "with --apply") || !strings.Contains(stdout, "Apply would refuse") {
				t.Errorf("%s preview invites an apply it would refuse:\n%s", command, stdout)
			}
		})
	}
}
