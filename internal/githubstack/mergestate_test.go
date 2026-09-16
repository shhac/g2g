package githubstack

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/subprocess"
	"github.com/shhac/g2g/internal/testutil"
)

const mergeabilityAnswer = `{"data":{"repository":{` +
	`"squashMergeAllowed":true,"mergeCommitAllowed":false,"rebaseMergeAllowed":false,` +
	`"pr0":{"number":41,"headRefName":"synthetic-one","headRefOid":"1111111111111111111111111111111111111111","baseRefName":"synthetic-main","state":"OPEN","isDraft":false,"mergeable":"MERGEABLE","mergeStateStatus":"BLOCKED","reviewDecision":"APPROVED","mergeCommit":null},` +
	`"pr1":{"number":42,"headRefName":"synthetic-two","headRefOid":"2222222222222222222222222222222222222222","baseRefName":"synthetic-one","state":"MERGED","isDraft":false,"mergeable":"CONFLICTING","mergeStateStatus":"DIRTY","reviewDecision":null,"mergeCommit":{"oid":"3333333333333333333333333333333333333333"}}` +
	`}}}`

func TestMergeabilityAsksByNumberAndCarriesRepositoryPolicy(t *testing.T) {
	arguments := filepath.Join(t.TempDir(), "gh-arguments")
	t.Setenv("GH_ARGUMENTS", arguments)
	testutil.WithFakeExecutables(t, map[string]string{
		"gh": `printf '%s\n' "$*" >> "$GH_ARGUMENTS"
if [ "$1 $2" = "api graphql" ]; then printf '` + mergeabilityAnswer + `\n'; fi`,
	})

	got, err := (Client{Runner: subprocess.ExecRunner{}}).Mergeability(context.Background(), []int{41, 42})
	if err != nil {
		t.Fatalf("Mergeability() error = %v", err)
	}

	if !got.Allowed.Squash || got.Allowed.Merge || got.Allowed.Rebase {
		t.Errorf("Allowed = %#v, want squash alone", got.Allowed)
	}
	if !got.Allowed.Permits(MethodSquash) || got.Allowed.Permits(MethodRebase) {
		t.Errorf("Permits disagrees with Allowed = %#v", got.Allowed)
	}
	if got.States[41].StateStatus != StatusBlocked || got.States[41].Review != ReviewApproved {
		t.Errorf("States[41] = %#v", got.States[41])
	}
	// A merged pull request keeps reporting whatever mergeable was when it
	// closed, which is why nothing may read it without checking State first.
	if !got.States[42].Merged() || got.States[42].MergeCommit != "3333333333333333333333333333333333333333" {
		t.Errorf("States[42] = %#v", got.States[42])
	}

	called, err := os.ReadFile(arguments)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"api graphql -F owner={owner} -F name={repo}",
		"squashMergeAllowed mergeCommitAllowed rebaseMergeAllowed",
		"pr0: pullRequest(number: 41)",
		"pr1: pullRequest(number: 42)",
		"mergeable mergeStateStatus reviewDecision mergeCommit { oid }",
	} {
		if !strings.Contains(string(called), want) {
			t.Errorf("gh calls missing %q: %q", want, called)
		}
	}
	// By number, never by head: the caller has already decided which pull
	// request each branch means, and a second derivation can disagree.
	if strings.Contains(string(called), "headRefName:") {
		t.Errorf("merge state was resolved by head branch: %q", called)
	}
}

// gh's --delete-branch removes the local branch as well as the remote one, so
// passing it would make publishing cleanup quietly delete the user's own work,
// outside this tool's guarded ref handling and without previewing it.
func TestMergeNeverAsksGhToDeleteTheBranch(t *testing.T) {
	arguments := filepath.Join(t.TempDir(), "gh-arguments")
	t.Setenv("GH_ARGUMENTS", arguments)
	testutil.WithFakeExecutables(t, map[string]string{"gh": `printf '%s\n' "$*" >> "$GH_ARGUMENTS"`})

	if err := (Client{Runner: subprocess.ExecRunner{}}).Merge(context.Background(), 41, MethodSquash, false); err != nil {
		t.Fatalf("Merge() error = %v", err)
	}

	called, err := os.ReadFile(arguments)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(called)) != "pr merge 41 --squash" {
		t.Errorf("gh call = %q, want exactly the squash merge", called)
	}
	for _, forbidden := range []string{"--delete-branch", "-d ", "--auto"} {
		if strings.Contains(string(called), forbidden) {
			t.Errorf("gh call carries %q: %q", forbidden, called)
		}
	}
}

func TestMergeAddsAdminOnlyWhenItIsAsked(t *testing.T) {
	for _, testCase := range []struct {
		admin bool
		want  string
	}{
		{admin: false, want: "pr merge 42 --rebase"},
		{admin: true, want: "pr merge 42 --rebase --admin"},
	} {
		arguments := filepath.Join(t.TempDir(), "gh-arguments")
		t.Setenv("GH_ARGUMENTS", arguments)
		testutil.WithFakeExecutables(t, map[string]string{"gh": `printf '%s\n' "$*" >> "$GH_ARGUMENTS"`})

		if err := (Client{Runner: subprocess.ExecRunner{}}).Merge(context.Background(), 42, MethodRebase, testCase.admin); err != nil {
			t.Fatalf("Merge(admin=%t) error = %v", testCase.admin, err)
		}
		called, err := os.ReadFile(arguments)
		if err != nil {
			t.Fatal(err)
		}
		if strings.TrimSpace(string(called)) != testCase.want {
			t.Errorf("gh call = %q, want %q", called, testCase.want)
		}
	}
}

func TestMergeRefusesAMethodItDoesNotKnow(t *testing.T) {
	testutil.WithFakeExecutables(t, map[string]string{"gh": `exit 1`})
	err := (Client{Runner: subprocess.ExecRunner{}}).Merge(context.Background(), 41, Method("--admin"), false)
	if err == nil || !strings.Contains(err.Error(), "unsupported merge method") {
		t.Fatalf("Merge() error = %v, want a refusal before anything ran", err)
	}
}

func TestParseMethodDefaultsToSquashAndNamesWhatItTakes(t *testing.T) {
	if method, err := ParseMethod(""); err != nil || method != MethodSquash {
		t.Errorf("ParseMethod(\"\") = %q, %v", method, err)
	}
	for _, name := range []string{"squash", "merge", "rebase"} {
		if _, err := ParseMethod(name); err != nil {
			t.Errorf("ParseMethod(%q) error = %v", name, err)
		}
	}
	_, err := ParseMethod("fast-forward")
	if err == nil {
		t.Fatal("ParseMethod(\"fast-forward\") error = nil")
	}
	// The refusal names what it takes, so nobody has to read the source to
	// find out what was allowed instead.
	for _, want := range []string{"fast-forward", "squash", "merge", "rebase"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err, want)
		}
	}
}

// An alias answering about a different pull request than it was asked about
// would silently attribute one branch's readiness to another.
func TestParseMergeabilityRejectsAnAnswerAboutADifferentPullRequest(t *testing.T) {
	output := `{"data":{"repository":{"squashMergeAllowed":true,"mergeCommitAllowed":true,"rebaseMergeAllowed":true,` +
		`"pr0":{"number":99,"headRefName":"synthetic-one","baseRefName":"synthetic-main","state":"OPEN"}}}}`
	_, err := parseMergeability([]byte(output), []int{41})
	if err == nil || !strings.Contains(err.Error(), "want 41") {
		t.Fatalf("parseMergeability() error = %v", err)
	}
}

func TestParseMergeabilityRejectsIncompleteResponses(t *testing.T) {
	for name, output := range map[string]string{
		"graphql errors":  `{"errors":[{"message":"synthetic failure"}]}`,
		"no repository":   `{"data":{}}`,
		"no merge policy": `{"data":{"repository":{"pr0":{"number":41,"baseRefName":"synthetic-main","state":"OPEN"}}}}`,
		"missing alias":   `{"data":{"repository":{"squashMergeAllowed":true,"mergeCommitAllowed":true,"rebaseMergeAllowed":true}}}`,
		"invalid node":    `{"data":{"repository":{"squashMergeAllowed":true,"mergeCommitAllowed":true,"rebaseMergeAllowed":true,"pr0":{"number":0}}}}`,
	} {
		if _, err := parseMergeability([]byte(output), []int{41}); err == nil {
			t.Errorf("parseMergeability(%s) error = nil", name)
		}
	}
}

func TestMergeabilityRefusesAPullRequestNumberItCannotAskAbout(t *testing.T) {
	testutil.WithFakeExecutables(t, map[string]string{"gh": `exit 1`})
	_, err := (Client{Runner: subprocess.ExecRunner{}}).Mergeability(context.Background(), []int{41, 0})
	if err == nil || !strings.Contains(err.Error(), "pull request number is required") {
		t.Fatalf("Mergeability() error = %v", err)
	}
}

// A repository that asks for no review at all reports null, which is not the
// same as a review that has not happened yet, and must not become a refusal.
func TestMergeStateReadsANullReviewDecisionAsNoReviewRequired(t *testing.T) {
	output := `{"data":{"repository":{"squashMergeAllowed":true,"mergeCommitAllowed":true,"rebaseMergeAllowed":true,` +
		`"pr0":{"number":41,"headRefName":"synthetic-one","baseRefName":"synthetic-main","state":"OPEN","reviewDecision":null,"mergeCommit":null}}}}`
	got, err := parseMergeability([]byte(output), []int{41})
	if err != nil {
		t.Fatalf("parseMergeability() error = %v", err)
	}
	if got.States[41].Review != "" || got.States[41].MergeCommit != "" {
		t.Errorf("States[41] = %#v, want both empty", got.States[41])
	}
}

func TestDeleteRemoteBranchRefusesAnOptionLikeName(t *testing.T) {
	testutil.WithFakeExecutables(t, map[string]string{"gh": `exit 1`})
	err := (Client{Runner: subprocess.ExecRunner{}}).DeleteRemoteBranch(context.Background(), "--all")
	if err == nil || !strings.Contains(err.Error(), "cannot be passed safely") {
		t.Fatalf("DeleteRemoteBranch() error = %v", err)
	}
}
