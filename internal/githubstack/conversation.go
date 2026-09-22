// Reading and writing the comments g2g keeps on a pull request.
//
// Kept apart from the pull request read in inspect.go for the reason that one
// is kept apart from the mutations: this reads a pull request by number rather
// than by head, because the comment it looks for can live on one whose branch
// is long gone, and what it reads back is conversation rather than structure.
package githubstack

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/shhac/g2g/internal/diagnostic"
)

// Conversation is one pull request as its comments need it: where a new one is
// added, and the ones already there that carry a marker.
type Conversation struct {
	// ID is the pull request's node id, which is what a comment is added to.
	ID     string
	Number int
	Head   string
	// Base is where the pull request merges, or merged. For a merged one it
	// is the branch that took the work, which is how a stack tells its own
	// history from a pull request that only passed through it.
	Base  string
	State string
	// Commentable is whether the person running this may add a comment. A
	// locked conversation takes none, and finding that out from a failed
	// write part-way down a stack is the wrong time.
	Commentable bool
	// Comments are only those whose body opens with the marker asked for,
	// oldest first. Everything else in the conversation is somebody's words
	// and none of this tool's business — including a comment that merely
	// quotes the marker, as one about this tool would.
	Comments []Comment
}

// Merged reports a pull request that landed, as opposed to one closed without.
func (c Conversation) Merged() bool { return c.State == stateMerged }

// Comment is one comment carrying the marker.
type Comment struct {
	// ID is the comment's node id. The REST id would do as well, except that
	// GitHub's comment ids have outgrown the GraphQL Int that reports them.
	ID     string
	Body   string
	Author string
	// Editable is whether the person running this may change it. A comment
	// written by someone else is only theirs to change, and adding a second
	// beside it would leave two comments saying the same thing differently.
	Editable bool
}

// commentPages bounds how far one conversation is read. A hundred comments a
// page, so this is ten thousand comments, which is a conversation nobody will
// be navigating; stopping says so rather than reading forever.
const commentPages = 100

// Conversations reads the pull requests with these numbers and the comments on
// each that carry marker.
//
// One query per round answers every pull request still being read, so a stack
// costs one round trip unless a conversation runs past a page. A number that is
// an issue rather than a pull request, or that nothing answers to, is left out:
// the numbers come from comments a person can edit, and an edit is not a reason
// to fail the command. A caller that needs a number to exist checks for it.
func (c Client) Conversations(ctx context.Context, numbers []int, marker string) ([]Conversation, error) {
	if c.Runner == nil {
		return nil, fmt.Errorf("GitHub runner is not configured")
	}
	if marker == "" {
		return nil, fmt.Errorf("a comment marker is required")
	}
	pending := make([]conversationPage, 0, len(numbers))
	seen := make(map[int]bool, len(numbers))
	for _, number := range numbers {
		if number <= 0 || seen[number] {
			continue
		}
		seen[number] = true
		pending = append(pending, conversationPage{Number: number})
	}

	read := make(map[int]*Conversation, len(pending))
	for page := 0; len(pending) != 0; page++ {
		if page == commentPages {
			return nil, fmt.Errorf("pull request #%d has more than %d pages of comments; stopped reading it", pending[0].Number, commentPages)
		}
		diagnostic.Event(ctx, "github.query", diagnostic.Field{Key: "kind", Value: "pull_request_comments"}, diagnostic.Field{Key: "pull_requests", Value: strconv.Itoa(len(pending))}, diagnostic.Field{Key: "query", Value: "omitted"})
		output, err := c.Runner.Run(ctx, "gh", "api", "graphql", "-F", "owner={owner}", "-F", "name={repo}", "-f", "query="+conversationQuery(pending))
		// gh exits non-zero when any alias failed, and a number nothing answers
		// to is one; the response it printed still answers the rest.
		if err != nil && !unresolvedOnly(output, len(pending)) {
			return nil, repositoryError(err, output)
		}
		next, err := parseConversations(output, pending, marker, read)
		if err != nil {
			return nil, err
		}
		pending = next
	}

	conversations := make([]Conversation, 0, len(read))
	for _, conversation := range read {
		conversations = append(conversations, *conversation)
	}
	slices.SortFunc(conversations, func(left, right Conversation) int { return left.Number - right.Number })
	return conversations, nil
}

// conversationPage is one pull request still being read, and where to resume.
type conversationPage struct {
	Number int
	After  string
}

// onlyUnresolved reports errors that are every one an alias naming no issue or
// pull request. That is the only failure this read can live with: the numbers
// come from comments a person can edit.
func onlyUnresolved(aliases int) func([]graphqlError) bool {
	return func(failures []graphqlError) bool {
		for _, failure := range failures {
			if failure.Type != "NOT_FOUND" || len(failure.Path) != 2 || failure.Path[0] != "repository" {
				return false
			}
			alias, _ := failure.Path[1].(string)
			index, err := strconv.Atoi(strings.TrimPrefix(alias, "c"))
			if !strings.HasPrefix(alias, "c") || err != nil || index < 0 || index >= aliases {
				return false
			}
		}
		return true
	}
}

// unresolvedOnly reports a failed run whose response carried nothing worse
// than aliases that resolved to nothing.
func unresolvedOnly(output []byte, aliases int) bool {
	var response graphqlResponse
	if err := decodeFirstJSON(output, &response); err != nil || len(response.Errors) == 0 {
		return false
	}
	return onlyUnresolved(aliases)(response.Errors)
}

func conversationQuery(pending []conversationPage) string {
	fields := make([]string, 0, len(pending))
	for index, page := range pending {
		after := ""
		if page.After != "" {
			after = ", after: " + graphqlString(page.After)
		}
		fields = append(fields, fmt.Sprintf("c%d: issueOrPullRequest(number: %d) { __typename ... on PullRequest { id number headRefName baseRefName state viewerCanComment comments(first: 100%s) { pageInfo { hasNextPage endCursor } nodes { id body viewerCanUpdate author { login } } } } }", index, page.Number, after))
	}
	// Named, as the mergeability query is, because both go to the same
	// endpoint as the head-ref lookup and a reader of a recorded call — or a
	// fake answering one — tells them apart by the operation.
	return fmt.Sprintf("query StackComments($owner: String!, $name: String!) { repository(owner: $owner, name: $name) { %s } }", strings.Join(fields, " "))
}

type conversationNode struct {
	TypeName    string `json:"__typename"`
	ID          string `json:"id"`
	Number      int    `json:"number"`
	Head        string `json:"headRefName"`
	Base        string `json:"baseRefName"`
	State       string `json:"state"`
	Commentable bool   `json:"viewerCanComment"`
	Comments    struct {
		PageInfo struct {
			HasNextPage bool   `json:"hasNextPage"`
			EndCursor   string `json:"endCursor"`
		} `json:"pageInfo"`
		Nodes []struct {
			ID       string `json:"id"`
			Body     string `json:"body"`
			Editable bool   `json:"viewerCanUpdate"`
			Author   *struct {
				Login string `json:"login"`
			} `json:"author"`
		} `json:"nodes"`
	} `json:"comments"`
}

// parseConversations folds one round into what has been read, and returns the
// pull requests that have another page.
func parseConversations(output []byte, pending []conversationPage, marker string, read map[int]*Conversation) ([]conversationPage, error) {
	repository, err := repositoryFields(output, onlyUnresolved(len(pending)))
	if err != nil {
		return nil, err
	}
	next := make([]conversationPage, 0)
	for index, page := range pending {
		alias := fmt.Sprintf("c%d", index)
		var node *conversationNode
		if err := aliasField(repository, alias, "", &node); err != nil {
			return nil, err
		}
		if node == nil || node.TypeName != "PullRequest" {
			continue
		}
		if node.Number != page.Number || node.ID == "" || node.State == "" {
			return nil, fmt.Errorf("gh api graphql response has an invalid pull request for %s", alias)
		}
		conversation := read[page.Number]
		if conversation == nil {
			conversation = &Conversation{ID: node.ID, Number: node.Number, Head: node.Head, Base: node.Base, State: node.State, Commentable: node.Commentable}
			read[page.Number] = conversation
		}
		for _, comment := range node.Comments.Nodes {
			if !strings.HasPrefix(strings.TrimSpace(comment.Body), marker) {
				continue
			}
			if comment.ID == "" {
				return nil, fmt.Errorf("gh api graphql response has a comment with no id on #%d", page.Number)
			}
			author := ""
			if comment.Author != nil {
				author = comment.Author.Login
			}
			conversation.Comments = append(conversation.Comments, Comment{ID: comment.ID, Body: comment.Body, Author: author, Editable: comment.Editable})
		}
		if node.Comments.PageInfo.HasNextPage {
			if node.Comments.PageInfo.EndCursor == "" {
				return nil, fmt.Errorf("gh api graphql response has another page of comments on #%d and no cursor to it", page.Number)
			}
			next = append(next, conversationPage{Number: page.Number, After: node.Comments.PageInfo.EndCursor})
		}
	}
	return next, nil
}

// AddComment adds a comment to the pull request with this node id.
//
// The body travels as a raw field, which gh passes through as a string: it
// reads no file for a value starting with @ and fills no {owner} placeholder,
// so a body is posted exactly as written.
func (c Client) AddComment(ctx context.Context, subject, body string) error {
	if subject == "" || body == "" {
		return fmt.Errorf("a pull request and a comment body are required")
	}
	diagnostic.Event(ctx, "github.comment_add", diagnostic.Field{Key: "subject", Value: subject})
	_, err := c.runAs(ctx, "gh api graphql addComment …", "api", "graphql",
		"-f", "query=mutation($subject: ID!, $body: String!) { addComment(input: {subjectId: $subject, body: $body}) { clientMutationId } }",
		"-f", "subject="+subject, "-f", "body="+body)
	return err
}

// UpdateComment replaces the body of the comment with this node id.
func (c Client) UpdateComment(ctx context.Context, id, body string) error {
	if id == "" || body == "" {
		return fmt.Errorf("a comment and a comment body are required")
	}
	diagnostic.Event(ctx, "github.comment_update", diagnostic.Field{Key: "comment", Value: id})
	_, err := c.runAs(ctx, "gh api graphql updateIssueComment …", "api", "graphql",
		"-f", "query=mutation($id: ID!, $body: String!) { updateIssueComment(input: {id: $id, body: $body}) { clientMutationId } }",
		"-f", "id="+id, "-f", "body="+body)
	return err
}
