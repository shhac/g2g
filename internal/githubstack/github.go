// Package githubstack provides read-only PR inspection and the explicit
// GitHub stack-link mutation boundary.
package githubstack

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/shhac/g2g/internal/diagnostic"
	"github.com/shhac/g2g/internal/subprocess"
)

// PullRequest is the GitHub information relevant to a planned stack link.
type PullRequest struct {
	Number        int    `json:"number"`
	URL           string `json:"url"`
	Head          string `json:"headRefName"`
	Base          string `json:"baseRefName"`
	State         string `json:"state"`
	StackNumber   int
	StackSize     int
	StackPosition int
}

// Client invokes gh. Inspect is read-only; Link is only called by --apply.
type Client struct {
	Runner subprocess.Runner
}

// run invokes gh and reports a failure as the command that produced it.
//
// Five call sites were each repeating the nil check, the invocation and the
// error wrap, and three of them built the command string by a different rule
// than the other two. internal/git and internal/graphite both extracted exactly
// this for exactly that reason; this package is the one that had not.
//
// The wrapped string keeps its "gh " prefix because remediationHint matches on
// it to decide whether a failure is worth advising on.
func (c Client) run(ctx context.Context, args ...string) ([]byte, error) {
	return c.runAs(ctx, "gh "+strings.Join(args, " "), args...)
}

// runAs is run for a command whose full argv should not be repeated back — a
// pull request body file and title make for a wrapped error nobody can read.
// The abbreviation is an argument rather than a slice index at the call site,
// so what is being hidden is stated rather than inferred.
func (c Client) runAs(ctx context.Context, display string, args ...string) ([]byte, error) {
	if c.Runner == nil {
		return nil, fmt.Errorf("GitHub runner is not configured")
	}
	output, err := c.Runner.Run(ctx, "gh", args...)
	if err != nil {
		return nil, commandError(display, err, output)
	}
	return output, nil
}

// Create creates one pull request without changing any existing PR. It owns
// the temporary body file required by gh, keeping that transport detail out of
// submission planning while preserving Markdown verbatim.
func (c Client) Create(ctx context.Context, branch, base, title, body string, draft bool, reviewers []string) error {
	if c.Runner == nil {
		return fmt.Errorf("GitHub runner is not configured")
	}
	if branch == "" || base == "" || title == "" {
		return fmt.Errorf("pull request branch, base, and title are required")
	}
	for _, reviewer := range reviewers {
		if err := subprocess.CheckArgument("gh", "reviewer", reviewer); err != nil {
			return err
		}
	}
	bodyFile, err := writeBody(body)
	if err != nil {
		return err
	}
	defer os.Remove(bodyFile)
	args := []string{"pr", "create", "--head", branch, "--base", base, "--title", title, "--body-file", bodyFile}
	if draft {
		args = append(args, "--draft")
	}
	for _, reviewer := range reviewers {
		args = append(args, "--reviewer", reviewer)
	}
	diagnostic.Event(ctx, "github.pr_create", diagnostic.Field{Key: "branch", Value: branch}, diagnostic.Field{Key: "base", Value: base}, diagnostic.Field{Key: "draft", Value: strconv.FormatBool(draft)})
	_, err = c.runAs(ctx, "gh "+strings.Join(args[:6], " ")+" …", args...)
	return err
}

func writeBody(body string) (string, error) {
	f, err := os.CreateTemp("", "g2g-submit-body-*.md")
	if err != nil {
		return "", err
	}
	path := f.Name()
	if _, err := f.WriteString(body); err != nil {
		f.Close()
		os.Remove(path)
		return "", err
	}
	if err := f.Close(); err != nil {
		os.Remove(path)
		return "", err
	}
	return path, nil
}

// CommandError separates a terse external-command summary from its bounded
// diagnostic output so callers can present each once in an appropriate place.
type CommandError struct {
	Command string
	Cause   error
	Output  string
}

func (e *CommandError) Error() string { return e.Command + " failed: " + e.Cause.Error() }
func (e *CommandError) Unwrap() error { return e.Cause }
func (e *CommandError) Summary() string {
	if strings.HasPrefix(e.Command, "gh stack link ") {
		return "gh stack link failed."
	}
	return e.Command + " failed."
}
func (e *CommandError) Diagnostic() string { return diagnostic.BoundedOutput([]byte(e.Output)) }

func (c Client) Link(ctx context.Context, trunk string, branches []string) error {
	if c.Runner == nil {
		return fmt.Errorf("GitHub runner is not configured")
	}
	args := append([]string{"stack", "link", "--base", trunk}, branches...)
	diagnostic.Event(ctx, "github.stack_link", diagnostic.Field{Key: "decision", Value: "invoke"}, diagnostic.Field{Key: "base", Value: trunk}, diagnostic.Field{Key: "branches", Value: strings.Join(branches, ",")})
	_, err := c.run(ctx, args...)
	return err
}

// Unstack removes only the GitHub-native stack relationship identified by its
// GitHub stack number. It does not change branches, PR contents, or Graphite.
func (c Client) Unstack(ctx context.Context, number int) error {
	if c.Runner == nil {
		return fmt.Errorf("GitHub runner is not configured")
	}
	if number <= 0 {
		return fmt.Errorf("GitHub stack number must be positive")
	}
	args := []string{"stack", "unstack", strconv.Itoa(number)}
	diagnostic.Event(ctx, "github.stack_unstack", diagnostic.Field{Key: "stack_number", Value: strconv.Itoa(number)})
	_, err := c.run(ctx, args...)
	return err
}

func commandError(command string, err error, output []byte) error {
	return &CommandError{Command: command, Cause: err, Output: string(output)}
}

// Retarget points an open pull request at a different base branch.
//
// It is the one GitHub mutation that changes what a merge will do rather than
// how the pull requests are displayed, which is why it is a deliberate command
// of its own rather than the tail of a restack.
func (c Client) Retarget(ctx context.Context, number int, base string) error {
	if c.Runner == nil {
		return fmt.Errorf("GitHub runner is not configured")
	}
	if number <= 0 {
		return fmt.Errorf("pull request number is required")
	}
	if err := subprocess.CheckArgument("gh", "base branch", base); err != nil {
		return err
	}
	args := []string{"pr", "edit", strconv.Itoa(number), "--base", base}
	diagnostic.Event(ctx, "github.pr_retarget", diagnostic.Field{Key: "number", Value: strconv.Itoa(number)}, diagnostic.Field{Key: "base", Value: base})
	_, err := c.run(ctx, args...)
	return err
}
