// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"context"
	"time"

	domain "canonkeeper/backend/internal/domain/desktop"
)

// ProgramDoor is the door of the app for the bridges of the programs of the author (specs/2026-11-15-desk-mcp.md MC1.6, MC3.1):
// it opens and closes, and tells whether it is open, failed to open and who turned to it since the start.
type ProgramDoor interface {
	Open(ctx context.Context) error
	Close()
	State() ProgramDoorState
}

// ProgramDoorState is the door now.
type ProgramDoorState struct {
	Open     bool
	Failed   bool
	Programs []ProgramSeen
}

// ProgramSeen is a program that turned to the door since the start: its name, when, whether its bridge has the set of the
// door, whether it is the copy in the extension of Claude Desktop.
type ProgramSeen struct {
	Name      string
	At        time.Time
	Matches   bool
	Extension bool
}

// ProgramSettings keep whether the programs may read the sagas (the section programs of settings.json).
type ProgramSettings interface {
	Programs() domain.Programs
	KeepPrograms(domain.Programs) error
}

// LastReads tell the latest reading of a program the journal keeps.
type LastReads interface {
	LastRead(ctx context.Context) (client string, at time.Time, ok bool, err error)
}

// ProgramAccess is getProgramAccess and updateProgramAccess (MC3.1): the reading of the sagas by the programs of the author.
// Commands are the commands of the bridge of this installation, nil when the shell named no program of the bridge; Windows
// says the system, which chooses the commands and the way of Claude Desktop.
type ProgramAccess struct {
	Settings ProgramSettings
	Door     ProgramDoor
	Reads    LastReads
	Commands *domain.ProgramCommands
	Windows  bool
	// Extension is the name of the file of the extension of Claude Desktop, "" where there is none.
	Extension string
}

// ProgramAccessState is the reading now: on, ever on, the door open or failed, the commands, the programs since the start and
// the last reading of the journal.
type ProgramAccessState struct {
	Enabled   bool
	Ever      bool
	Open      bool
	Failed    bool
	Windows   bool
	Commands  *domain.ProgramCommands
	Extension string
	Programs  []ProgramSeen
	LastRead  *ProgramSeen
}

// Get is getProgramAccess.
func (access ProgramAccess) Get(ctx context.Context) (ProgramAccessState, error) {
	settings := access.Settings.Programs()
	door := access.Door.State()
	state := ProgramAccessState{
		Enabled: settings.Enabled, Ever: settings.Ever || settings.Enabled, Open: door.Open, Failed: settings.Enabled && door.Failed,
		Windows: access.Windows, Commands: access.Commands, Extension: access.Extension, Programs: door.Programs,
	}
	client, at, ok, err := access.Reads.LastRead(ctx)
	if err != nil {
		return ProgramAccessState{}, err
	}
	if ok {
		state.LastRead = &ProgramSeen{Name: client, At: at}
	}
	return state, nil
}

// Update is updateProgramAccess: the setting is kept, the door opens or closes, and the state after it is the answer; a door
// that did not open is a state that says so.
func (access ProgramAccess) Update(ctx context.Context, enabled bool) (ProgramAccessState, error) {
	current := access.Settings.Programs()
	if err := access.Settings.KeepPrograms(domain.Programs{Enabled: enabled, Ever: current.Ever}); err != nil {
		return ProgramAccessState{}, err
	}
	if enabled {
		_ = access.Door.Open(ctx)
	} else {
		access.Door.Close()
	}
	return access.Get(ctx)
}
