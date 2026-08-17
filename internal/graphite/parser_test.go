package graphite

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseLogCompactMultiForkTree(t *testing.T) {
	parsed := parseFixture(t, "irregular-stack.txt")
	if got, want := strings.Join(parsed.roots, ","), "main"; got != want {
		t.Fatalf("roots = %q, want %q", got, want)
	}
	cases := map[string][]string{
		"alpha":        {"main", "alpha"},
		"beta":         {"main", "alpha", "beta"},
		"beta-top":     {"main", "alpha", "beta", "beta-top"},
		"beta-side":    {"main", "alpha", "beta", "beta-top", "beta-side"},
		"gamma":        {"main", "alpha", "gamma"},
		"gamma-deep":   {"main", "alpha", "gamma", "gamma-deep"},
		"delta":        {"main", "alpha", "delta"},
		"delta-deep":   {"main", "alpha", "delta", "delta-deep"},
		"epsilon":      {"main", "alpha", "epsilon"},
		"epsilon-deep": {"main", "alpha", "epsilon", "epsilon-deep"},
	}
	for selected, want := range cases {
		t.Run(selected, func(t *testing.T) {
			got := pathToRoot(t, parsed, selected)
			if strings.Join(got, ",") != strings.Join(want, ",") {
				t.Errorf("path = %v, want %v", got, want)
			}
		})
	}
}

func TestParseLogKeepsMultipleTrunksSeparate(t *testing.T) {
	parsed := parseFixture(t, "multiple-trunks.txt")
	if got, want := strings.Join(parsed.roots, ","), "main,develop,staging"; got != want {
		t.Fatalf("roots = %q, want %q", got, want)
	}
	if got, want := strings.Join(pathToRoot(t, parsed, "feature-two"), ","), "main,feature-one,feature-two"; got != want {
		t.Errorf("feature path = %q, want %q", got, want)
	}
	for _, trunk := range []string{"main", "develop", "staging"} {
		if got, want := strings.Join(pathToRoot(t, parsed, trunk), ","), trunk; got != want {
			t.Errorf("%s path = %q, want %q", trunk, got, want)
		}
	}
}

func TestParseLogTracksNeedsRestackBranch(t *testing.T) {
	parsed := parseFixture(t, "needs-restack-stack.txt")
	if got, want := strings.Join(pathToRoot(t, parsed, "synthetic-feature"), ","), "trunk,foundation,synthetic-feature"; got != want {
		t.Errorf("path = %q, want %q", got, want)
	}
}

func TestParseLogAcceptsKnownBranchLabelSuffixes(t *testing.T) {
	for label, want := range map[string]string{
		"synthetic-branch":                                      "synthetic-branch",
		"synthetic-branch (current)":                            "synthetic-branch",
		"synthetic-branch (needs restack)":                      "synthetic-branch",
		"synthetic-branch (synthetic worktree)":                 "synthetic-branch",
		"synthetic-branch (needs restack) (synthetic worktree)": "synthetic-branch",
	} {
		t.Run(label, func(t *testing.T) {
			parsed, err := parseLog("◯  trunk\n◯  " + label + "\n")
			if err != nil {
				t.Fatalf("parseLog() error = %v", err)
			}
			if _, exists := parsed.nodes[want]; !exists {
				t.Errorf("missing branch %q", want)
			}
		})
	}
}

func TestParseLogKeepsWorktreeAnnotatedSiblingOutsideSelectedPath(t *testing.T) {
	parsed := parseFixture(t, "worktree-annotation-stack.txt")
	if got, want := strings.Join(pathToRoot(t, parsed, "synthetic-selected"), ","), "trunk,foundation,synthetic-selected"; got != want {
		t.Errorf("selected path = %q, want %q", got, want)
	}
	if got, want := strings.Join(pathToRoot(t, parsed, "synthetic-worktree-sibling"), ","), "trunk,foundation,synthetic-worktree-sibling"; got != want {
		t.Errorf("sibling path = %q, want %q", got, want)
	}
}

func TestParseLogRejectsMalformedBranchLabelSuffixes(t *testing.T) {
	for _, label := range []string{
		"synthetic-feature (synthetic annotation) (needs restack)",
		"synthetic-feature (needs restack) (synthetic annotation) (another annotation)",
		"synthetic-feature (needs restack) (current)",
		"synthetic-feature (current) (needs restack)",
		"synthetic-feature (current) (synthetic worktree)",
		"synthetic-feature (needs restack) (nested (annotation))",
		"synthetic-feature ()",
		"synthetic-feature (needs restack) (needs restack)",
	} {
		t.Run(label, func(t *testing.T) {
			_, err := parseLog("◯  trunk\n◯  " + label + "\n")
			if err == nil || !strings.Contains(err.Error(), "unsupported Graphite display grammar") {
				t.Fatalf("parseLog() error = %v, want grammar drift", err)
			}
		})
	}
}

func TestParseLogRejectsCompactGrammarDrift(t *testing.T) {
	fixture, err := os.ReadFile(filepath.Join("testdata", "irregular-stack.txt"))
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(string) string{
		"heading": func(text string) string { return strings.Replace(text, "◯  main", "?  main", 1) },
		"connector join": func(text string) string {
			return strings.Replace(text, "─┬─┬─┬─┐", "─┬─┴─┬─┐", 1)
		},
		"connector ending": func(text string) string {
			return strings.Replace(text, "─┬─┬─┬─┐", "─┬─┬─┬─", 1)
		},
		"prefix": func(text string) string { return strings.Replace(text, "│ │ │ │ ◯", "││ │ │ ◯", 1) },
		"one-space name": func(text string) string {
			return strings.Replace(text, "◯  main", "◯ main", 1)
		},
		"odd name padding": func(text string) string {
			return strings.Replace(text, "│ │ │ │ ◯    beta", "│ │ │ │ ◯   beta", 1)
		},
		"leading blank": func(text string) string { return "\n" + text },
		"consecutive blank": func(text string) string {
			return strings.Replace(text, "alpha\n│ │ │ │", "alpha\n\n\n│ │ │ │", 1)
		},
		"trailing blank": func(text string) string { return strings.TrimSuffix(text, "\n") + "\n\n" },
		"after connector": func(text string) string {
			return strings.Replace(text, "alpha\n│ │ │ │", "alpha\n\n│ │ │ │", 1)
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := parseLog(mutate(string(fixture))); err == nil || !strings.Contains(err.Error(), "unsupported Graphite display grammar") {
				t.Fatalf("parseLog() error = %v, want grammar drift", err)
			}
		})
	}
}

func TestParseLogRejectsDuplicateBranch(t *testing.T) {
	fixture, err := os.ReadFile(filepath.Join("testdata", "irregular-stack.txt"))
	if err != nil {
		t.Fatal(err)
	}
	duplicated := strings.Replace(string(fixture), "gamma", "beta", 1)
	if _, err := parseLog(duplicated); err == nil || !strings.Contains(err.Error(), "repeats branch") {
		t.Fatalf("parseLog() error = %v, want duplicate branch", err)
	}
}

func TestParseLogRejectsUnresolvedConnector(t *testing.T) {
	_, err := parseLog("◯─┐  main\n")
	if err == nil || !strings.Contains(err.Error(), "ends after a fork connector") {
		t.Fatalf("parseLog() error = %v, want unresolved connector", err)
	}
}

func parseFixture(t *testing.T, name string) graph {
	t.Helper()
	fixture, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := parseLog(string(fixture))
	if err != nil {
		t.Fatalf("parseLog() error = %v", err)
	}
	return parsed
}

func pathToRoot(t *testing.T, parsed graph, selected string) []string {
	t.Helper()
	node, ok := parsed.nodes[selected]
	if !ok {
		t.Fatalf("missing node %q", selected)
	}
	var reversed []string
	for {
		reversed = append(reversed, node.name)
		if node.parent == "" {
			break
		}
		node = parsed.nodes[node.parent]
	}
	for left, right := 0, len(reversed)-1; left < right; left, right = left+1, right-1 {
		reversed[left], reversed[right] = reversed[right], reversed[left]
	}
	return reversed
}

// A trunk with three children, where one child carries its own child and the
// last sibling returns to the trunk's own depth.
//
// The grammar here was confirmed against Graphite 1.8.6 by tracking a synthetic
// forest in a throwaway repository and reading what the CLI actually printed —
// the display is reproduced, the repository it came from was invented. It is
// worth pinning because it is the shape a trunk has in practice, and it is the
// one the linear walk could never express: `◯` at the trunk's own depth extends
// the trunk rather than starting a second root.
func TestParseLogAcceptsATrunkWithSeveralChildren(t *testing.T) {
	display := "◉─┬─┐  synthetic-trunk\n│ │ ◯  synthetic-a\n│ │ ◯  synthetic-a-one\n│ ◯    synthetic-b\n◯      synthetic-c\n"

	parsed, err := parseLog(display)
	if err != nil {
		t.Fatalf("parseLog() error = %v", err)
	}
	for branch, want := range map[string]string{
		"synthetic-trunk": "",
		"synthetic-a":     "synthetic-trunk",
		"synthetic-a-one": "synthetic-a",
		"synthetic-b":     "synthetic-trunk",
		"synthetic-c":     "synthetic-trunk",
	} {
		node, tracked := parsed.nodes[branch]
		if !tracked {
			t.Errorf("branch %q missing from the parsed display", branch)
			continue
		}
		if node.parent != want {
			t.Errorf("parent of %q = %q, want %q", branch, node.parent, want)
		}
	}
	if len(parsed.roots) != 1 || parsed.roots[0] != "synthetic-trunk" {
		t.Errorf("roots = %v, want exactly synthetic-trunk; a sibling at the trunk's depth is not a second root", parsed.roots)
	}
}
