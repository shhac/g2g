package graphite

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseLogIrregularForkTree(t *testing.T) {
	fixture, err := os.ReadFile(filepath.Join("testdata", "irregular-stack.txt"))
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := parseLog(string(fixture))
	if err != nil {
		t.Fatalf("parseLog() error = %v", err)
	}
	if got, want := parsed.trunk, "main"; got != want {
		t.Fatalf("trunk = %q, want %q", got, want)
	}
	cases := map[string][]string{
		"alpha":         {"alpha"},
		"beta":          {"alpha", "beta"},
		"beta-one":      {"alpha", "beta", "beta-one"},
		"beta-two":      {"alpha", "beta", "beta-two"},
		"beta-two-deep": {"alpha", "beta", "beta-two", "beta-two-deep"},
		"gamma":         {"alpha", "gamma"},
		"gamma-deep":    {"alpha", "gamma", "gamma-deep"},
	}
	for selected, want := range cases {
		t.Run(selected, func(t *testing.T) {
			got := pathToTrunk(t, parsed, selected)
			if strings.Join(got, ",") != strings.Join(want, ",") {
				t.Errorf("path = %v, want %v", got, want)
			}
		})
	}
}

func TestParseLogRejectsUnknownLine(t *testing.T) {
	fixture, err := os.ReadFile(filepath.Join("testdata", "irregular-stack.txt"))
	if err != nil {
		t.Fatal(err)
	}
	broken := strings.Replace(string(fixture), "│ 8 seconds ago", "unexpected Graphite output", 1)
	if _, err := parseLog(broken); err == nil || !strings.Contains(err.Error(), "unsupported Graphite display grammar") {
		t.Fatalf("parseLog() error = %v, want grammar drift", err)
	}
}

func TestParseLogRejectsGrammarDrift(t *testing.T) {
	fixture, err := os.ReadFile(filepath.Join("testdata", "irregular-stack.txt"))
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(string) string{
		"heading":          func(text string) string { return strings.Replace(text, "◯ main", "? main", 1) },
		"time":             func(text string) string { return strings.Replace(text, "9 seconds ago", "soon", 1) },
		"blank guide":      func(text string) string { return strings.Replace(text, "│ \n", "│ not blank\n", 1) },
		"commit":           func(text string) string { return strings.Replace(text, "612a36f - base", "not-a-commit", 1) },
		"connector":        func(text string) string { return strings.Replace(text, "│\n◯ alpha", "connector\n◯ alpha", 1) },
		"fork marker":      func(text string) string { return strings.Replace(text, "├──┐", "fork", 1) },
		"graph prefix":     func(text string) string { return strings.Replace(text, "│  ◯ beta", "│ ◯ beta", 1) },
		"truncated record": func(text string) string { return strings.TrimSuffix(text, " \n") },
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
	duplicated := strings.Replace(string(fixture), "beta-one", "beta-two", 2)
	if _, err := parseLog(duplicated); err == nil || !strings.Contains(err.Error(), "repeats branch") {
		t.Fatalf("parseLog() error = %v, want duplicate branch", err)
	}
}

func pathToTrunk(t *testing.T, parsed graph, selected string) []string {
	t.Helper()
	node := parsed.nodes[selected]
	var reversed []string
	for node.name != parsed.trunk {
		reversed = append(reversed, node.name)
		node = parsed.nodes[node.parent]
	}
	for left, right := 0, len(reversed)-1; left < right; left, right = left+1, right-1 {
		reversed[left], reversed[right] = reversed[right], reversed[left]
	}
	return reversed
}
