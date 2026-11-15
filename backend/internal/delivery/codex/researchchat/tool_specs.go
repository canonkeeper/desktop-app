package researchchat

import "canonkeeper/backend/internal/delivery/codex/researchchat/toolspecs"

// The closed schemas and Russian descriptions of the tools of the chat live in toolspecs, a package without a service the
// set for the programs of the author builds on too (specs/2026-11-15-desk-mcp.md, MC2.1).
var (
	readToolSpecs      = toolspecs.Reading()
	memoryToolSpecs    = toolspecs.Memory()
	attachmentToolSpec = toolspecs.Attachment()
)
