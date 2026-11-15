// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"context"
	"errors"
	"mime"
	"net/http"
	"strconv"

	"canonkeeper/backend/internal/httpapi"
	service "canonkeeper/backend/internal/services/desktop"
)

// ProgramAccess is the reading of the sagas by the programs of the author (services/desktop.ProgramAccess).
type ProgramAccess interface {
	Get(ctx context.Context) (service.ProgramAccessState, error)
	Update(ctx context.Context, enabled bool) (service.ProgramAccessState, error)
}

// ClaudeDesktopExtension builds the file of the extension of Claude Desktop; service.ErrNoExtension where there is none.
type ClaudeDesktopExtension interface {
	Execute(ctx context.Context) (name string, content []byte, err error)
}

// Programs are the operations of the reading by the programs of the author (specs/2026-11-15-desk-mcp.md MC3).
type Programs struct {
	Access    ProgramAccess
	Extension ClaudeDesktopExtension
}

type programCommandsJSON struct {
	ClaudeAdd    string  `json:"claudeAdd"`
	ClaudeRemove string  `json:"claudeRemove"`
	CodexAdd     string  `json:"codexAdd"`
	CodexRemove  string  `json:"codexRemove"`
	DesktopEntry *string `json:"desktopEntry"`
	DesktopFile  *string `json:"desktopFile"`
}

type programSeenJSON struct {
	Name      string `json:"name"`
	At        string `json:"at"`
	Matches   bool   `json:"matches"`
	Extension bool   `json:"extension"`
}

type programLastReadJSON struct {
	Name string `json:"name"`
	At   string `json:"at"`
}

type programAccessJSON struct {
	Enabled  bool                 `json:"enabled"`
	Ever     bool                 `json:"ever"`
	Open     bool                 `json:"open"`
	Failed   bool                 `json:"failed"`
	System   string               `json:"system"`
	Commands *programCommandsJSON `json:"commands"`
	// ExtensionFile is the file of the extension of Claude Desktop, null where there is none.
	ExtensionFile *string              `json:"extensionFile"`
	Programs      []programSeenJSON    `json:"programs"`
	LastRead      *programLastReadJSON `json:"lastRead"`
}

type programAccessUpdateJSON struct {
	Enabled *bool `json:"enabled"`
}

func programAccessOf(state service.ProgramAccessState) programAccessJSON {
	out := programAccessJSON{
		Enabled: state.Enabled, Ever: state.Ever, Open: state.Open, Failed: state.Failed, System: "linux", Programs: []programSeenJSON{},
	}
	if state.Windows {
		out.System = "windows"
	}
	if commands := state.Commands; commands != nil {
		out.Commands = &programCommandsJSON{ClaudeAdd: commands.ClaudeAdd, ClaudeRemove: commands.ClaudeRemove, CodexAdd: commands.CodexAdd, CodexRemove: commands.CodexRemove}
		if commands.DesktopFile != "" {
			out.Commands.DesktopEntry, out.Commands.DesktopFile = &commands.DesktopEntry, &commands.DesktopFile
		}
	}
	if state.Extension != "" {
		out.ExtensionFile = &state.Extension
	}
	for _, program := range state.Programs {
		out.Programs = append(out.Programs, programSeenJSON{Name: program.Name, At: moment(program.At), Matches: program.Matches, Extension: program.Extension})
	}
	if state.LastRead != nil {
		out.LastRead = &programLastReadJSON{Name: state.LastRead.Name, At: moment(state.LastRead.At)}
	}
	return out
}

// programAccess is getProgramAccess (MC3.1).
func (handlers Handlers) programAccess(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !handlers.signedIn(w, r) {
		return
	}
	state, err := handlers.Programs.Access.Get(r.Context())
	if err != nil {
		unexpected(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, programAccessOf(state))
}

// updateProgramAccess is updateProgramAccess (MC3.1): the answer is the state after the door opened or closed.
func (handlers Handlers) updateProgramAccess(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !handlers.signedIn(w, r) {
		return
	}
	var request programAccessUpdateJSON
	if _, ok := decode(r, &request); !ok || request.Enabled == nil {
		httpapi.WriteProblem(w, "", http.StatusBadRequest, "Bad Request", "invalid request body", "validation.failed")
		return
	}
	state, err := handlers.Programs.Access.Update(r.Context(), *request.Enabled)
	if err != nil {
		unexpected(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, programAccessOf(state))
}

// claudeDesktopExtension is getClaudeDesktopExtension (MC3.3): the file the server builds, saved as the file of a saga is.
func (handlers Handlers) claudeDesktopExtension(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !handlers.signedIn(w, r) {
		return
	}
	if handlers.Programs.Extension == nil {
		httpapi.WriteProblem(w, "", http.StatusNotFound, "Not Found", "there is no extension here", "not_found")
		return
	}
	name, content, err := handlers.Programs.Extension.Execute(r.Context())
	if errors.Is(err, service.ErrNoExtension) {
		httpapi.WriteProblem(w, "", http.StatusNotFound, "Not Found", "there is no extension here", "not_found")
		return
	}
	if err != nil {
		unexpected(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	w.Header().Set("Content-Length", strconv.Itoa(len(content)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(content)
}
