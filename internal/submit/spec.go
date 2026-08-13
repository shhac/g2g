// Package submit implements safe, spec-driven pull-request submission.
package submit

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const manifestName = "submission.json"

// Spec is deliberately a single JSON document. Bodies are strings so an
// --edit workflow opens one file rather than a set of editor buffers. Complex
// Markdown is preserved exactly by encoding/json.
type Spec struct {
	Version  int    `json:"version"`
	Draft    bool   `json:"draft"`
	Pulls    []Pull `json:"pulls"`
	Template string `json:"template,omitempty"`
}

type Pull struct {
	Branch    string   `json:"branch"`
	Title     string   `json:"title"`
	Body      string   `json:"body"`
	Reviewers []string `json:"reviewers,omitempty"`
}

func NewSpec(branches []string, template string) Spec {
	pulls := make([]Pull, len(branches))
	for i, branch := range branches {
		pulls[i] = Pull{Branch: branch, Body: template}
	}
	return Spec{Version: 1, Draft: true, Pulls: pulls, Template: template}
}

func Write(dir string, spec Spec) (string, error) {
	if dir == "" {
		return "", fmt.Errorf("spec directory is required; use a private temporary directory outside the repository")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, manifestName)
	data, err := json.MarshalIndent(spec, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		return "", err
	}
	return path, nil
}

func Read(path string, branches []string) (Spec, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Spec{}, fmt.Errorf("read submission spec: %w", err)
	}
	var spec Spec
	if err := json.Unmarshal(data, &spec); err != nil {
		return Spec{}, fmt.Errorf("parse submission spec: %w", err)
	}
	if spec.Version != 1 {
		return Spec{}, fmt.Errorf("submission spec version must be 1")
	}
	if len(spec.Pulls) != len(branches) {
		return Spec{}, fmt.Errorf("submission spec has %d PR entries, want %d for the selected stack", len(spec.Pulls), len(branches))
	}
	for i, branch := range branches {
		pull := spec.Pulls[i]
		if pull.Branch != branch {
			return Spec{}, fmt.Errorf("submission spec entry %d is for %q, want %q", i+1, pull.Branch, branch)
		}
		if strings.TrimSpace(pull.Title) == "" {
			return Spec{}, fmt.Errorf("submission spec is incomplete: missing title for branch %q", branch)
		}
	}
	return spec, nil
}
