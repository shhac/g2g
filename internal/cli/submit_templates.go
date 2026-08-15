package cli

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

func resolveTemplate(requested string, disabled bool) (string, string, error) {
	if disabled {
		return "", "", nil
	}
	templates, err := findTemplates()
	if err != nil {
		return "", "", err
	}
	if requested != "" {
		content, ok := templates[requested]
		if !ok {
			return "", "", fmt.Errorf("pull request template %q was not found; available templates: %s; or use --no-template", requested, strings.Join(templateNames(templates), ", "))
		}
		return content, requested, nil
	}
	if len(templates) == 0 {
		return "", "", nil
	}
	if len(templates) == 1 {
		name := slices.Collect(maps.Keys(templates))[0]
		return templates[name], name, nil
	}
	return "", "", fmt.Errorf("multiple pull request templates found (%s); rerun with --template <name> or --no-template", strings.Join(templateNames(templates), ", "))
}

func findTemplates() (map[string]string, error) {
	paths := []string{".github/PULL_REQUEST_TEMPLATE.md", ".github/pull_request_template.md", "PULL_REQUEST_TEMPLATE.md", "pull_request_template.md", "docs/PULL_REQUEST_TEMPLATE.md", "docs/pull_request_template.md"}
	found := map[string]string{}
	for _, path := range paths {
		if b, err := os.ReadFile(path); err == nil {
			found[path] = string(b)
		} else if !os.IsNotExist(err) {
			return nil, err
		}
	}
	for _, dir := range []string{".github/PULL_REQUEST_TEMPLATE", "PULL_REQUEST_TEMPLATE", "docs/PULL_REQUEST_TEMPLATE"} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".md") {
				continue
			}
			path := filepath.Join(dir, entry.Name())
			b, err := os.ReadFile(path)
			if err != nil {
				return nil, err
			}
			found[entry.Name()] = string(b)
		}
	}
	return found, nil
}
func templateNames(templates map[string]string) []string {
	names := make([]string, 0, len(templates))
	for name := range templates {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
