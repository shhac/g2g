// Package diagnostic provides opt-in, stderr-only operational diagnostics.
package diagnostic

import (
	"context"
	"fmt"
	"io"
	"strings"
)

type contextKey struct{}

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
		if redactNext {
			parts = append(parts, "[redacted]")
			redactNext = false
			continue
		}
		if credentialFlag(argument) {
			parts = append(parts, argument)
			redactNext = true
			continue
		}
		for _, prefix := range []string{"--token=", "--auth=", "--header=", "--cookie="} {
			if strings.HasPrefix(argument, prefix) {
				parts = append(parts, prefix+"[redacted]")
				argument = ""
				break
			}
		}
		if argument != "" {
			parts = append(parts, argument)
		}
	}
	return strings.Join(parts, " ")
}

func credentialFlag(argument string) bool {
	switch argument {
	case "--token", "--auth", "--header", "--cookie", "-H":
		return true
	default:
		return false
	}
}

func redact(message string) string {
	lines := strings.Split(message, "\n")
	for index, line := range lines {
		lower := strings.ToLower(strings.TrimSpace(line))
		if strings.Contains(lower, "authorization:") || strings.Contains(lower, "token:") || strings.Contains(lower, "token=") || strings.Contains(lower, "cookie:") || strings.Contains(lower, "cookie=") || strings.Contains(lower, "--token") || strings.Contains(lower, "--header") {
			lines[index] = "[redacted sensitive diagnostic]"
		}
	}
	return strings.Join(lines, "\n")
}
