package agent

import "testing"

func TestFlowIsClosedToTheResearchChat(t *testing.T) {
	if !FlowResearchChat.Valid() {
		t.Fatalf("flow %q is invalid", FlowResearchChat)
	}
	for _, flow := range []Flow{"", "indexing", "research_chat", "writer", "canon-correction", "unknown"} {
		if flow.Valid() {
			t.Fatalf("flow %q is valid", flow)
		}
	}
}
