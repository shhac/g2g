package githubstack

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/shhac/g2g/internal/diagnostic"
	"github.com/shhac/g2g/internal/subprocess"
)

// Method is how a pull request is merged.
//
// The vocabulary is GitHub's, and a repository may forbid any of them, so the
// choice is checked against Allowed before anything is merged rather than
// discovered from a failure part-way down a stack.
type Method string

const (
	MethodSquash Method = "squash"
	MethodMerge  Method = "merge"
	MethodRebase Method = "rebase"
)

// Methods is every method in the order they are offered.
var Methods = []Method{MethodSquash, MethodMerge, MethodRebase}

// ParseMethod resolves a method name, defaulting to squash.
//
// Squash is the default because it is the case a stack needs help with: the
// others leave the parent's commits in the child by the same identity, so the
// child needs no replay and the whole stack can go down in one step.
func ParseMethod(value string) (Method, error) {
	if value == "" {
		return MethodSquash, nil
	}
	for _, method := range Methods {
		if Method(value) == method {
			return method, nil
		}
	}
	names := make([]string, 0, len(Methods))
	for _, method := range Methods {
		names = append(names, string(method))
	}
	return "", fmt.Errorf("unsupported merge method %q (want %s)", value, strings.Join(names, ", "))
}

// flag is the gh flag that selects this method.
func (m Method) flag() string { return "--" + string(m) }

// The mergeable, mergeStateStatus and reviewDecision values this tool acts on.
// The enums have more members than these; naming only what is read keeps the
// set honest about what has actually been thought about.
const (
	MergeableConflicting = "CONFLICTING"
	MergeableUnknown     = "UNKNOWN"

	StatusBlocked = "BLOCKED"
	StatusBehind  = "BEHIND"
	StatusUnknown = "UNKNOWN"

	ReviewApproved = "APPROVED"
)

// MergeState is what GitHub reports about one pull request's readiness.
//
// These fields are deliberately not on PullRequest. Discovery.Equal compares
// pull requests by value and is what link, retarget and submit revalidate
// against, and mergeable legitimately moves from UNKNOWN to CLEAN while GitHub
// computes it — so carrying them there would make three commands that have
// nothing to do with merging refuse intermittently.
type MergeState struct {
	Number  int
	Head    string
	HeadOID string
	Base    string
	State   string
	Draft   bool
	// Mergeable and StateStatus are meaningless once State is MERGED: GitHub
	// keeps reporting whatever they were when the pull request closed.
	Mergeable   string
	StateStatus string
	// Review is empty when the repository asks for no review at all, which is
	// not the same as a review that has not happened yet.
	Review string
	// MergeCommit is what landed, and is what makes "has the merge reached the
	// remote base" answerable by ancestry rather than by watching a tip move.
	MergeCommit string
}

// Merged reports a pull request GitHub has recorded as merged.
func (m MergeState) Merged() bool { return m.State == stateMerged }

// Allowed is the merge methods a repository permits.
type Allowed struct {
	Squash bool
	Merge  bool
	Rebase bool
}

// Permits reports whether a method may be used here.
func (a Allowed) Permits(method Method) bool {
	switch method {
	case MethodSquash:
		return a.Squash
	case MethodMerge:
		return a.Merge
	case MethodRebase:
		return a.Rebase
	}
	return false
}

// Mergeability is one round trip's answer: what the repository permits, and
// where each pull request stands.
type Mergeability struct {
	Allowed Allowed
	// States is keyed by pull request number, because the caller already
	// resolved which pull request it means. Asking again by head branch would
	// be a second identity derivation that could disagree with the first.
	States map[int]MergeState
}

const stateMerged = "MERGED"

// Mergeability asks what every selected pull request's merge would do.
//
// One aliased query for every number, plus the repository's own merge policy,
// which rides along as three more fields rather than costing a second round
// trip. The repository is named by gh's {owner}/{repo} placeholders, exactly
// as Inspect does.
func (c Client) Mergeability(ctx context.Context, numbers []int) (Mergeability, error) {
	if c.Runner == nil {
		return Mergeability{}, fmt.Errorf("GitHub runner is not configured")
	}
	for _, number := range numbers {
		if number <= 0 {
			return Mergeability{}, fmt.Errorf("pull request number is required")
		}
	}
	query := mergeabilityQuery(numbers)
	diagnostic.Event(ctx, "github.query",
		diagnostic.Field{Key: "kind", Value: "batched_merge_state"},
		diagnostic.Field{Key: "pull_requests", Value: strconv.Itoa(len(numbers))},
		diagnostic.Field{Key: "query", Value: "omitted"},
	)
	output, err := c.Runner.Run(ctx, "gh", "api", "graphql", "-F", "owner={owner}", "-F", "name={repo}", "-f", "query="+query)
	if err != nil {
		return Mergeability{}, repositoryError(err, output)
	}
	return parseMergeability(output, numbers)
}

// mergeabilityQuery batches one aliased lookup per pull request number.
//
// By number rather than by head branch: the caller has already decided which
// pull request each branch means, and resolving it a second time by a
// different rule is how two answers come to disagree about the same branch.
func mergeabilityQuery(numbers []int) string {
	fields := make([]string, 0, len(numbers)+3)
	fields = append(fields, "squashMergeAllowed", "mergeCommitAllowed", "rebaseMergeAllowed")
	for index, number := range numbers {
		fields = append(fields, fmt.Sprintf("pr%d: pullRequest(number: %d) { number headRefName headRefOid baseRefName state isDraft mergeable mergeStateStatus reviewDecision mergeCommit { oid } }", index, number))
	}
	return fmt.Sprintf("query($owner: String!, $name: String!) { repository(owner: $owner, name: $name) { %s } }", strings.Join(fields, " "))
}

type mergeStateNode struct {
	Number      int    `json:"number"`
	Head        string `json:"headRefName"`
	HeadOID     string `json:"headRefOid"`
	Base        string `json:"baseRefName"`
	State       string `json:"state"`
	Draft       bool   `json:"isDraft"`
	Mergeable   string `json:"mergeable"`
	StateStatus string `json:"mergeStateStatus"`
	Review      string `json:"reviewDecision"`
	MergeCommit *struct {
		OID string `json:"oid"`
	} `json:"mergeCommit"`
}

// mergeState validates one node and converts it.
//
// An unmerged pull request has no merge commit, which is ordinary; a merged
// one without a number is a response this cannot act on.
func (n mergeStateNode) mergeState(alias string) (MergeState, error) {
	if n.Number <= 0 || n.Base == "" || n.State == "" {
		return MergeState{}, fmt.Errorf("gh api graphql response has invalid %s merge state", alias)
	}
	state := MergeState{
		Number:      n.Number,
		Head:        n.Head,
		HeadOID:     n.HeadOID,
		Base:        n.Base,
		State:       n.State,
		Draft:       n.Draft,
		Mergeable:   n.Mergeable,
		StateStatus: n.StateStatus,
		Review:      n.Review,
	}
	if n.MergeCommit != nil {
		state.MergeCommit = n.MergeCommit.OID
	}
	return state, nil
}

func parseMergeability(output []byte, numbers []int) (Mergeability, error) {
	var response graphqlResponse
	if err := json.Unmarshal(output, &response); err != nil {
		return Mergeability{}, fmt.Errorf("parse gh api graphql JSON: %w", err)
	}
	if len(response.Errors) != 0 {
		return Mergeability{}, fmt.Errorf("gh api graphql returned errors: %s", diagnostic.BoundedOutput([]byte(response.Errors[0].Message)))
	}
	if response.Data.Repository == nil {
		return Mergeability{}, fmt.Errorf("gh api graphql returned no repository; check that the GitHub CLI can read this repository")
	}

	result := Mergeability{States: make(map[int]MergeState, len(numbers))}
	allowed := map[string]*bool{
		"squashMergeAllowed": &result.Allowed.Squash,
		"mergeCommitAllowed": &result.Allowed.Merge,
		"rebaseMergeAllowed": &result.Allowed.Rebase,
	}
	for field, into := range allowed {
		raw, exists := response.Data.Repository[field]
		if !exists {
			return Mergeability{}, fmt.Errorf("gh api graphql response is missing %s", field)
		}
		if err := json.Unmarshal(raw, into); err != nil {
			return Mergeability{}, fmt.Errorf("gh api graphql response has invalid %s", field)
		}
	}

	for index, number := range numbers {
		alias := fmt.Sprintf("pr%d", index)
		raw, exists := response.Data.Repository[alias]
		if !exists {
			return Mergeability{}, fmt.Errorf("gh api graphql response is missing %s", alias)
		}
		var node mergeStateNode
		if err := json.Unmarshal(raw, &node); err != nil {
			return Mergeability{}, fmt.Errorf("gh api graphql response has invalid %s merge state", alias)
		}
		state, err := node.mergeState(alias)
		if err != nil {
			return Mergeability{}, err
		}
		if state.Number != number {
			return Mergeability{}, fmt.Errorf("gh api graphql answered %s with pull request %d, want %d", alias, state.Number, number)
		}
		result.States[number] = state
	}
	return result, nil
}

// Merge merges one pull request.
//
// It never passes --delete-branch. That flag deletes the local branch as well
// as the remote one, which would make deleting the published branch quietly
// remove the user's own, outside this tool's own guarded ref handling and
// without previewing it. The two deletions are separate acts and stay separate.
func (c Client) Merge(ctx context.Context, number int, method Method, admin bool) error {
	if c.Runner == nil {
		return fmt.Errorf("GitHub runner is not configured")
	}
	if number <= 0 {
		return fmt.Errorf("pull request number is required")
	}
	if _, err := ParseMethod(string(method)); err != nil {
		return err
	}
	args := []string{"pr", "merge", strconv.Itoa(number), method.flag()}
	if admin {
		args = append(args, "--admin")
	}
	diagnostic.Event(ctx, "github.pr_merge",
		diagnostic.Field{Key: "number", Value: strconv.Itoa(number)},
		diagnostic.Field{Key: "method", Value: string(method)},
		diagnostic.Field{Key: "admin", Value: strconv.FormatBool(admin)},
	)
	_, err := c.run(ctx, args...)
	return err
}

// DeleteRemoteBranch removes a branch from the remote after its work has
// landed. It is deliberately not part of Merge; see that method's note.
func (c Client) DeleteRemoteBranch(ctx context.Context, branch string) error {
	if err := subprocess.CheckArgument("gh", "branch", branch); err != nil {
		return err
	}
	diagnostic.Event(ctx, "github.branch_delete", diagnostic.Field{Key: "branch", Value: branch})
	_, err := c.run(ctx, "api", "--method", "DELETE", "repos/{owner}/{repo}/git/refs/heads/"+branch)
	return err
}
