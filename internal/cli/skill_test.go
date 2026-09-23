package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// The skill is published by a tag's workflow, which refuses a description over
// its budget — after the release itself has already gone out, so the skill is
// left a version behind. The budget lives in the workflow; this reads it from
// there rather than keeping a second copy that could drift from it.
func TestTheSkillDescriptionFitsThePublishingBudget(t *testing.T) {
	workflow, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "publish-skill.yml"))
	if err != nil {
		t.Fatal(err)
	}
	found := regexp.MustCompile(`max_description_chars:\s*(\d+)`).FindSubmatch(workflow)
	if found == nil {
		t.Fatal("publish-skill.yml names no max_description_chars")
	}
	budget, err := strconv.Atoi(string(found[1]))
	if err != nil {
		t.Fatal(err)
	}

	skill, err := os.ReadFile(filepath.Join("..", "..", "skills", "g2g", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	_, block, ok := strings.Cut(string(skill), "description: |\n")
	if !ok {
		t.Fatal("SKILL.md has no block description")
	}
	lines := make([]string, 0)
	for _, line := range strings.Split(block, "\n") {
		if !strings.HasPrefix(line, "  ") {
			break
		}
		lines = append(lines, strings.TrimPrefix(line, "  "))
	}
	// Counted as the publisher counts it: the block's lines, joined.
	if length := len(strings.Join(lines, "\n")); length > budget {
		t.Errorf("the skill description is %d characters, %d over the %d the publishing workflow allows", length, length-budget, budget)
	}
}
