package graphite

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseLogCompactMultiForkTree(t *testing.T) {
	parsed := parseFixture(t, "irregular-stack.txt")
	if got, want := parsed.trunk, "main"; got != want {
		t.Fatalf("trunk = %q, want %q", got, want)
	}
	cases := map[string][]string{
		"alpha":        {"alpha"},
		"beta":         {"alpha", "beta"},
		"beta-top":     {"alpha", "beta", "beta-top"},
		"beta-side":    {"alpha", "beta", "beta-top", "beta-side"},
		"gamma":        {"alpha", "gamma"},
		"gamma-deep":   {"alpha", "gamma", "gamma-deep"},
		"delta":        {"alpha", "delta"},
		"delta-deep":   {"alpha", "delta", "delta-deep"},
		"epsilon":      {"alpha", "epsilon"},
		"epsilon-deep": {"alpha", "epsilon", "epsilon-deep"},
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
			return strings.Replace(text, "main\n\n", "main\n\n\n", 1)
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
