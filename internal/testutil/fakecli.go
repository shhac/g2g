// Package testutil contains helpers shared by tests in internal packages.
package testutil

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// WithFakeExecutables places small executable scripts named after external
// CLIs at the front of PATH. It keeps tests offline and independent of local
// Graphite or GitHub CLI installations.
func WithFakeExecutables(t *testing.T, scripts map[string]string) {
	t.Helper()

	dir := t.TempDir()
	for name, body := range scripts {
		path := filepath.Join(dir, name)
		contents := "#!/bin/sh\nset -eu\n" + body
		if err := os.WriteFile(path, []byte(contents), 0o755); err != nil {
			t.Fatalf("write fake %s: %v", name, err)
		}
	}

	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// Route is one declarative response from a fake CLI. Prefix is matched against
// the invocation's joined arguments, first match wins, and an unmatched call
// fails loudly rather than silently returning nothing.
//
// Exactly one of Output, Lines, File, or Stderr carries the response. Output
// and Stderr are single-line; Lines writes one line each, which matters
// because several adapters split their input on newlines; File streams a
// fixture, keeping large ones in testdata where they read as themselves.
type Route struct {
	Prefix string
	Output string
	Lines  []string
	File   string
	Stderr string
	Exit   int
}

// Recorder captures every fake CLI invocation in order, so a test can assert
// what gt2gh actually ran rather than what a stub was asked for.
type Recorder struct {
	t    *testing.T
	path string
}

// FakeCLIs installs fixture-driven fakes for the named tools and returns a
// recorder of their invocations.
//
// This exercises gt2gh exactly as it runs for real: the production adapters
// build real argv, spawn real processes, and parse real bytes. Dependency
// injection at the service seam cannot catch a malformed argument, a changed
// response shape, or a mishandled exit status, because it replaces the code
// that would have to get those right.
func FakeCLIs(t *testing.T, tools map[string][]Route) *Recorder {
	t.Helper()

	dir := t.TempDir()
	log := filepath.Join(dir, "invocations.log")
	if err := os.WriteFile(log, nil, 0o600); err != nil {
		t.Fatalf("create invocation log: %v", err)
	}
	t.Setenv("FAKE_CLI_LOG", log)

	scripts := make(map[string]string, len(tools))
	for name, routes := range tools {
		routesPath := filepath.Join(dir, name+".routes")
		if err := os.WriteFile(routesPath, []byte(encodeRoutes(t, dir, name, routes)), 0o600); err != nil {
			t.Fatalf("write %s routes: %v", name, err)
		}
		scripts[name] = dispatchScript(name, routesPath)
	}
	WithFakeExecutables(t, scripts)

	return &Recorder{t: t, path: log}
}

func encodeRoutes(t *testing.T, dir, tool string, routes []Route) string {
	t.Helper()

	var out strings.Builder
	for index, route := range routes {
		mode, payload := "out", route.Output
		switch {
		case len(route.Lines) != 0:
			// Spilled to a file so the routes file stays one line per route
			// while the response itself can be many.
			path := filepath.Join(dir, tool+"."+strconv.Itoa(index)+".lines")
			if err := os.WriteFile(path, []byte(strings.Join(route.Lines, "\n")+"\n"), 0o600); err != nil {
				t.Fatalf("write %s route lines: %v", tool, err)
			}
			mode, payload = "file", path
		case route.File != "":
			mode, payload = "file", route.File
		case route.Stderr != "":
			mode, payload = "err", route.Stderr
		}
		if strings.ContainsAny(route.Prefix+payload, "\t\n") {
			t.Fatalf("route %q has a tab or newline; use File for multi-line fixtures", route.Prefix)
		}
		out.WriteString(strings.Join([]string{route.Prefix, mode, strconv.Itoa(route.Exit), payload}, "\t"))
		out.WriteString("\n")
	}
	return out.String()
}

// dispatchScript is the same for every tool: it logs the call, then walks the
// routes file for the first prefix match. An unmatched invocation exits 97 so
// a test fails on a call it never anticipated instead of on a confusing
// downstream parse error.
func dispatchScript(name, routesPath string) string {
	return `printf '` + name + ` %s\n' "$*" >> "$FAKE_CLI_LOG"
tab="$(printf '\t')"
while IFS="$tab" read -r prefix mode code payload; do
  case "$*" in
    "$prefix"*)
      case "$mode" in
        out) [ -n "$payload" ] && printf '%s\n' "$payload" ;;
        file) cat "$payload" ;;
        err) printf '%s\n' "$payload" >&2 ;;
      esac
      exit "$code" ;;
  esac
done < "` + routesPath + `"
printf '` + name + `: no fake route for: %s\n' "$*" >&2
exit 97`
}

// Calls returns every invocation in order, as "<tool> <args>".
func (r *Recorder) Calls() []string {
	r.t.Helper()

	contents, err := os.ReadFile(r.path)
	if err != nil {
		r.t.Fatalf("read invocation log: %v", err)
	}
	trimmed := strings.TrimRight(string(contents), "\n")
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "\n")
}

// Count returns how many invocations begin with prefix.
func (r *Recorder) Count(prefix string) int {
	total := 0
	for _, call := range r.Calls() {
		if strings.HasPrefix(call, prefix) {
			total++
		}
	}
	return total
}

// Find returns the first invocation beginning with prefix, failing if there
// is none. Asserting on the request matters because a fake answers from its
// routes regardless of what was asked: only the recorded argv proves gt2gh
// built the call correctly.
func (r *Recorder) Find(prefix string) string {
	r.t.Helper()

	for _, call := range r.Calls() {
		if strings.HasPrefix(call, prefix) {
			return call
		}
	}
	r.t.Fatalf("no %q invocation:\n%s", prefix, strings.Join(r.Calls(), "\n"))
	return ""
}

// AssertNone fails when any invocation begins with one of the prefixes. It is
// how a preview test proves it stayed read-only.
func (r *Recorder) AssertNone(prefixes ...string) {
	r.t.Helper()

	for _, prefix := range prefixes {
		if r.Count(prefix) != 0 {
			r.t.Fatalf("unexpected %q invocation:\n%s", prefix, strings.Join(r.Calls(), "\n"))
		}
	}
}

// AssertOrder fails unless the prefixes appear in the given relative order.
// Sequencing is a safety contract here — discovery before mutation, push
// before pull-request creation — so it deserves a direct assertion.
func (r *Recorder) AssertOrder(prefixes ...string) {
	r.t.Helper()

	calls, at := r.Calls(), 0
	for _, prefix := range prefixes {
		found := -1
		for index := at; index < len(calls); index++ {
			if strings.HasPrefix(calls[index], prefix) {
				found = index
				break
			}
		}
		if found < 0 {
			r.t.Fatalf("expected %q after position %d:\n%s", prefix, at, strings.Join(calls, "\n"))
		}
		at = found + 1
	}
}
