package agent

// Flow names the Codex app-server an agent run belongs to: each flow has its own bundle, its own CODEX_HOME and its
// own app-server. The research chat answers questions about the saga; the helper of the drawing workshop works on
// one drawing (specs/2026-11-02-image-workshop.md IW4.1); the helper of the chapter workshop works on one chapter
// draft (specs/2026-11-02-writer-workshop.md W1.1).
type Flow string

const (
	FlowResearchChat   Flow = "research-chat"
	FlowImageWorkshop  Flow = "image-workshop"
	FlowWriterWorkshop Flow = "writer-workshop"
)

// Flows lists every flow in the order the app-servers start.
func Flows() []Flow { return []Flow{FlowResearchChat, FlowImageWorkshop, FlowWriterWorkshop} }

func (flow Flow) Valid() bool {
	for _, known := range Flows() {
		if flow == known {
			return true
		}
	}
	return false
}

// Session kinds of chat_sessions.kind: the conversation of a flow is a chat session of its kind, with the turns and
// the lifecycle of every turn (IW4.2).
const (
	SessionResearch = "research"
	SessionDrawing  = "drawing"
	SessionDraft    = "draft"
)

// ToolNamespaceDrawing is the namespace of the tools of the helper of the drawing workshop
// (specs/2026-11-02-image-workshop.md IW4.3); the timeline of a turn shows their calls as it shows those of the saga.
const ToolNamespaceDrawing = "drawing"

// SessionFlow is the flow of a chat session by its kind: reading its thread, deleting and exporting it go to that
// app-server; an unknown kind has none.
func SessionFlow(kind string) (Flow, bool) {
	switch kind {
	case SessionResearch:
		return FlowResearchChat, true
	case SessionDrawing:
		return FlowImageWorkshop, true
	case SessionDraft:
		return FlowWriterWorkshop, true
	default:
		return "", false
	}
}
