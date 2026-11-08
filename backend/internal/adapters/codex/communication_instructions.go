package codex

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"canonkeeper/backend/internal/domain/agent"
)

// Feed-only adaptation of Slopotron; source and MIT notice are in slopotron.LICENSE.
//
//go:embed communication_instructions.md
var communicationInstructions string

func (client *Client) developerInstructions(request RunRequest) (string, error) {
	roleInstructions := request.DeveloperInstructions
	if request.SkillName != "" {
		skill := filepath.Join(client.repositoryHome, "skills", request.SkillName)
		body, err := os.ReadFile(filepath.Join(skill, "SKILL.md"))
		if err != nil {
			return "", runStageError{code: "runtime.skill_load_failed", err: err}
		}
		roleInstructions, err = withSections(string(body), skill, request.Tools)
		if err != nil {
			return "", runStageError{code: "runtime.skill_load_failed", err: err}
		}
	}
	return communicationInstructions + "\n\n" + roleInstructions, nil
}

// sectionPlace marks where a section of a skill stands in its SKILL.md: {{namespace.tool:part}}. The section is the
// file sections/tool.part.md of the skill without its final newline (specs/2026-11-07-desktop-app.md DA9.3).
var sectionPlace = regexp.MustCompile(`\{\{([a-z]+)\.([a-z]+):([a-z]+)\}\}`)

// withSections puts into the skill the sections of the tools the turn offers and drops those of the tools it does not
// (DA9.3): the site's chat, with saga.threads and saga.image, reads the whole text, a build without them reads none
// of their sections.
func withSections(body, skill string, tools []agent.ToolSpec) (string, error) {
	offered := make(map[string]bool, len(tools))
	for _, tool := range tools {
		offered[tool.Namespace+"."+tool.Name] = true
	}
	var failure error
	text := sectionPlace.ReplaceAllStringFunc(body, func(place string) string {
		match := sectionPlace.FindStringSubmatch(place)
		if !offered[match[1]+"."+match[2]] || failure != nil {
			return ""
		}
		section, err := os.ReadFile(filepath.Join(skill, "sections", match[2]+"."+match[3]+".md"))
		if err != nil {
			failure = fmt.Errorf("skill section %s: %w", place, err)
			return ""
		}
		return strings.TrimSuffix(string(section), "\n")
	})
	return text, failure
}
