// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"context"
	_ "embed"
	"os"
	"runtime"

	"canonkeeper/backend/internal/app"
	httpdesktop "canonkeeper/backend/internal/delivery/http/desktop"
	"canonkeeper/backend/internal/delivery/mcp/door"
	"canonkeeper/backend/internal/delivery/mcp/programtools"
	"canonkeeper/backend/internal/domain/authormemory"
	domain "canonkeeper/backend/internal/domain/desktop"
	"canonkeeper/backend/internal/projects"
	"canonkeeper/backend/internal/repository/postgres/modeljournal"
	authormemoryservice "canonkeeper/backend/internal/services/authormemory"
	service "canonkeeper/backend/internal/services/desktop"
	"canonkeeper/backend/internal/services/programreads"
	researchservice "canonkeeper/backend/internal/services/researchchat"
)

// extensionIcon is the icon of the app (desktop/src-tauri/icons/128x128.png) the file of the extension of Claude Desktop
// carries.
//
//go:embed extension-icon.png
var extensionIcon []byte

// programParts are the parts of the handler of a call of a program over the core (specs/2026-11-15-desk-mcp.md MC2.4): the
// authority of the local author without a turn — the authority of the voice of the site, in the open core —, his memory by
// ListAuthorMemory, and the stores the chat of the app reads with.
func programParts(core app.Core, authorID string) door.ChatParts {
	return door.ChatParts{
		Reads: func(projectID string) researchservice.ReadRepository {
			return core.Chat.VoiceReads(authorID, projectID)
		},
		Memory: func(ctx context.Context, projectID string, filter authormemory.Filter) (authormemory.Page, error) {
			return core.AuthorMemory.ListAuthorMemory(ctx, authormemoryservice.Actor{UserID: authorID, ProjectID: projectID}, filter)
		},
		Saga: core.Saga, Layer: core.SagaStore, Materials: core.AuthorMaterials, Chapters: core.Saga,
	}
}

// useProgramDoor gives the door its answers over the core — the backend opens it when it serves and the reading is on — and
// is the operations of the reading by the programs: its state, its turning on and off, the commands of the bridge of this
// installation and the file of the extension of Claude Desktop.
func (desktop *build) useProgramDoor(core app.Core, sagas *projects.Service, journal *modeljournal.Journal) httpdesktop.Programs {
	reads := programreads.New(sagas, journal, desktop.authorID)
	desktop.programs.use(door.New(reads, door.ChatHandlers(programParts(core, desktop.authorID)), desktop.version))
	windows := runtime.GOOS == "windows"
	var commands *domain.ProgramCommands
	if desktop.bridge != nil {
		built := domain.Commands(*desktop.bridge, desktop.programs.dataDir, windows)
		commands = &built
	}
	var tools []domain.ExtensionTool
	for _, tool := range programtools.Tools() {
		tools = append(tools, domain.ExtensionTool{Name: tool.Name, Description: tool.Description})
	}
	extension := ""
	if windows && desktop.bridge != nil {
		extension = domain.ExtensionName(desktop.version)
	}
	return httpdesktop.Programs{
		Access: service.ProgramAccess{
			Settings: desktop.settings, Door: desktop.programs, Reads: journal, Commands: commands, Windows: windows, Extension: extension,
		},
		Extension: service.ClaudeDesktopExtension{
			Bridge: desktop.bridge, DataDir: desktop.programs.dataDir, Version: desktop.version, Icon: extensionIcon, Windows: windows,
			Read: os.ReadFile, Tools: tools, Kind: programtools.KindExtension,
		},
	}
}
