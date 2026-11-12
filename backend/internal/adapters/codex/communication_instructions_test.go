package codex

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"canonkeeper/backend/internal/domain/agent"
)

func TestCommunicationRulesAreLimitedToTheAgentsOwnFeed(t *testing.T) {
	for _, boundary := range []string{
		"только к твоим собственным сообщениям пользователю в ленте",
		"Не применяй эти правила к авторскому тексту", "вариантам правок Writer",
		"summaries", "цитатам", "JSON", "без отдельного skill",
	} {
		if !strings.Contains(communicationInstructions, boundary) {
			t.Errorf("communication boundary missing: %q", boundary)
		}
	}
}

func TestAllFlowsReceiveInlineCommunicationRulesOnStartAndResume(t *testing.T) {
	for _, flow := range []string{"indexing", "research-chat", "writer", "canon-correction"} {
		t.Run(flow, func(t *testing.T) {
			transport := newFakeTransport()
			transport.autoComplete = true
			client := newTestClient(t, transport)
			var boundTurn string
			request := testRunRequest(flow, flow+"-turn", "", false, &boundTurn)
			request.DeveloperInstructions = "Role protocol remains exact."
			request.InitialInput = "Author manuscript must remain untouched."
			result, err := client.Run(t.Context(), request)
			if err != nil {
				t.Fatal(err)
			}
			request.ExistingRootThreadID = result.RootThreadID
			if _, err := client.Run(t.Context(), request); err != nil {
				t.Fatal(err)
			}
			starts := transport.threadStarts()
			transport.mu.Lock()
			resumes := append(transport.resumes[:0:0], transport.resumes...)
			transport.mu.Unlock()
			if len(starts) != 1 || len(resumes) != 1 {
				t.Fatalf("start/resume counts = %d/%d", len(starts), len(resumes))
			}
			startInstructions, _ := starts[0].DeveloperInstructions.Value()
			resumeInstructions, _ := resumes[0].DeveloperInstructions.Value()
			for _, instructions := range []string{startInstructions, resumeInstructions} {
				if strings.Count(instructions, "## Общение агента в ленте") != 1 ||
					!strings.HasSuffix(instructions, request.DeveloperInstructions) {
					t.Fatal("inline communication rules or original role instructions missing")
				}
			}
			turns := transport.turnStartParams()
			if len(turns) != 1 || len(turns[0].Input) != 1 {
				t.Fatal("communication rules changed or replayed the author input")
			}
			input, _ := turns[0].Input[0].Text.Value()
			if input != request.InitialInput {
				t.Fatal("communication rules rewrote the author input")
			}
		})
	}
}

// recordChatSkill writes testdata/research-chat-skill-with-layer.golden anew: only when the skill of the chat changes on
// purpose.
const recordChatSkill = "CANONKEEPER_CHAT_SKILL_RECORD"

// specs/2026-11-12-desk-window.md WN2.1: the skill of the chat of the site — its set with saga.threads and saga.image — is
// the text it was before the slice to the letter (recorded on the tree before it); the set of a build without the thread
// layer reads no section of the threads and no mark of their place.
func TestTheChatSkillOfTheSiteIsAsItWas(t *testing.T) {
	skill := filepath.Join("..", "..", "..", "..", "codex-homes", "research-chat", "skills", "canonkeeper-research-chat")
	body, err := os.ReadFile(filepath.Join(skill, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	site, err := withSections(string(body), skill, chatSkillTools(
		"search", "read", "chapters", "glossary", "entity", "timeline", "threads", "changes", "scan", "remember", "memory", "image", "attachment",
	))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("testdata", "research-chat-skill-with-layer.golden")
	if os.Getenv(recordChatSkill) == "1" {
		if err := os.WriteFile(path, []byte(site), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	golden, err := os.ReadFile(path)
	if err != nil || site != string(golden) {
		t.Fatalf("the skill of the chat of the site is not the recorded one: %v", err)
	}
	desktop, err := withSections(string(body), skill, chatSkillTools(
		"search", "read", "chapters", "glossary", "entity", "timeline", "changes", "scan", "remember", "memory", "attachment",
	))
	if err != nil || strings.Contains(desktop, "threads") || strings.Contains(desktop, "{{") {
		t.Fatalf("the skill without the layer names the threads: %v", err)
	}
}

func chatSkillTools(names ...string) []agent.ToolSpec {
	tools := make([]agent.ToolSpec, 0, len(names))
	for _, name := range names {
		tools = append(tools, agent.ToolSpec{Namespace: "saga", Name: name})
	}
	return tools
}

// specs/2026-11-07-desktop-app.md DA9.2, DA9.3: the chat's skill is its core and the sections of its tools; the site's
// chat, which offers saga.threads and saga.image, reads every section in its place, and a build without them reads
// none of theirs and no mark of a place.
func TestTheChatSkillTakesTheSectionsOfItsTools(t *testing.T) {
	skill := filepath.Join("..", "..", "..", "..", "codex-homes", "research-chat", "skills", "canonkeeper-research-chat")
	body, err := os.ReadFile(filepath.Join(skill, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	site, err := withSections(string(body), skill, chatSkillTools(
		"search", "read", "chapters", "glossary", "entity", "timeline", "threads", "changes", "scan", "remember", "memory", "image", "attachment",
	))
	if err != nil || strings.Contains(site, "{{") || !strings.Contains(site, "- `threads` — открытые нити саги") ||
		!strings.Contains(site, "\n## Рисунки\n") || !strings.Contains(site, "Что можно подхватить, подскажет\n   `threads`.") {
		t.Fatalf("the site's chat skill misses its sections: %v", err)
	}
	desktop, err := withSections(string(body), skill, chatSkillTools(
		"search", "read", "chapters", "glossary", "entity", "timeline", "changes", "scan", "remember", "memory", "attachment",
	))
	if err != nil || strings.Contains(desktop, "{{") || strings.Contains(desktop, "`threads`") || strings.Contains(desktop, "Рисунки") ||
		strings.Contains(desktop, "`image`") || !strings.Contains(desktop, "опорного места; смысловые места без ключевых слов") {
		t.Fatalf("a chat without threads and image reads their sections: %v", err)
	}
	if _, err := withSections("{{saga.threads:missing}}", skill, chatSkillTools("threads")); err == nil {
		t.Fatal("a section without its file was taken")
	}
}
