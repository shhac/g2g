// Package diagnostic provides opt-in, stderr-only operational diagnostics.
package diagnostic

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
)

type (
	contextKey        struct{}
	warningContextKey struct{}
)

// Field is one stable, safe diagnostic attribute.
type Field struct {
	Key   string
	Value string
}

// Sink receives diagnostics for one command invocation.
type Sink interface {
	Event(string, ...Field)
}

// WithSink makes sink available to adapters through ctx.
func WithSink(ctx context.Context, sink Sink) context.Context {
	return context.WithValue(ctx, contextKey{}, sink)
}

// Event writes an opt-in diagnostic only when ctx has a sink.
func Event(ctx context.Context, name string, fields ...Field) {
	if sink, ok := ctx.Value(contextKey{}).(Sink); ok && sink != nil {
		sink.Event(name, fields...)
	}
}

type warningSink struct {
	out  io.Writer
	mu   sync.Mutex
	seen map[string]bool
}

// WithWarningWriter makes non-debug compatibility warnings available to
// adapters. Each key is printed at most once for a command context.
func WithWarningWriter(ctx context.Context, out io.Writer) context.Context {
	if out == nil {
		return ctx
	}
	return context.WithValue(ctx, warningContextKey{}, &warningSink{out: out, seen: make(map[string]bool)})
}

// Warn writes one concise, non-debug warning when configured by the CLI.
func Warn(ctx context.Context, key, message string) {
	sink, ok := ctx.Value(warningContextKey{}).(*warningSink)
	if !ok || sink == nil {
		return
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if sink.seen[key] {
		return
	}
	sink.seen[key] = true
	fmt.Fprintf(sink.out, "warning: %s\n", message)
}

// Writer emits one stable, newline-delimited record per event.
type Writer struct{ Out io.Writer }

func (w Writer) Event(name string, fields ...Field) {
	if w.Out == nil {
		return
	}
	fmt.Fprintf(w.Out, "debug event=%s", name)
	for _, field := range fields {
		fmt.Fprintf(w.Out, " %s=%q", field.Key, strings.ReplaceAll(field.Value, "\n", "\\n"))
	}
	_, _ = fmt.Fprintln(w.Out)
}

// BoundedOutput returns a compact, redacted process diagnostic. It is only
// used by an explicit local --debug invocation; it never reads environment.
func BoundedOutput(output []byte) string {
	const limit = 500
	message := redact(strings.TrimSpace(string(output)))
	if len(message) > limit {
		return message[:limit] + "…"
	}
	return message
}

// SafeCommand summarizes a subprocess invocation without exposing sensitive
// flags or GraphQL query contents.
func SafeCommand(name string, args []string) string {
	if name == "gh" && len(args) >= 2 && args[0] == "api" && args[1] == "graphql" {
		return "gh api graphql query=omitted"
	}
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, name)
	redactNext := false
	for _, argument := range args {
		switch {
		case redactNext:
			parts = append(parts, "[redacted]")
			redactNext = false
		case credentialFlag(argument):
			parts = append(parts, argument)
			redactNext = true
		default:
			parts = append(parts, redactPrefixed(argument))
		}
	}
	return strings.Join(parts, " ")
}

// credentialPrefixes are the flag forms that carry their secret inline.
var credentialPrefixes = []string{"--token=", "--auth=", "--header=", "--cookie="}

// redactPrefixed rewrites an inline credential flag, returning anything else
// unchanged. Returning the argument rather than blanking it keeps a genuinely
// empty argv element visible: the previous sentinel could not tell "already
// handled" apart from "empty", so empty arguments vanished from diagnostics.
func redactPrefixed(argument string) string {
	for _, prefix := range credentialPrefixes {
		if strings.HasPrefix(argument, prefix) {
			return prefix + "[redacted]"
		}
	}
	return argument
}

func credentialFlag(argument string) bool {
	switch argument {
	case "--token", "--auth", "--header", "--cookie", "-H":
		return true
	default:
		return false
	}
}

// sensitiveMarkers are the substrings that make a whole line unsafe to print.
// They are a list for the same reason credentialPrefixes above is: a security
// check is easier to audit, extend and test when the thing being checked for is
// data rather than a seven-clause condition.
var sensitiveMarkers = []string{
	"authorization:",
	"token:",
	"token=",
	"cookie:",
	"cookie=",
	"--token",
	"--header",
}

func redact(message string) string {
	lines := strings.Split(message, "\n")
	for index, line := range lines {
		if sensitive(strings.ToLower(strings.TrimSpace(line))) {
			lines[index] = "[redacted sensitive diagnostic]"
		}
	}
	return strings.Join(lines, "\n")
}

// sensitive reports a lowercased line that carries a credential.
func sensitive(lower string) bool {
	for _, marker := range sensitiveMarkers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// Revalidated records the outcome of a preview/apply revalidation and reports a
// mismatch as an error.
//
// Every mutating service re-reads the world immediately before writing and
// refuses if anything moved. That check is the safety contract, and it was
// written out six times across five packages — which is how internal/align came
// to emit no diagnostic event at all while the other four did. Stating it once
// means the event name, the field shape, and the wording cannot drift apart.
//
// subject names the operation in the refusal ("push plan", "the graphs"), and
// event names it in the diagnostic stream.
func Revalidated(ctx context.Context, event, subject string, equal bool) error {
	if !equal {
		Event(ctx, event+".revalidation", Field{Key: "match", Value: "false"})
		return fmt.Errorf("%s changed during revalidation; rerun without --apply to review the new plan", subject)
	}
	Event(ctx, event+".revalidation", Field{Key: "match", Value: "true"})
	return nil
}
