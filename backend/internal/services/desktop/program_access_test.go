// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"context"
	"errors"
	"testing"
	"time"

	domain "canonkeeper/backend/internal/domain/desktop"
)

type programSettings struct{ programs domain.Programs }

func (settings *programSettings) Programs() domain.Programs { return settings.programs }
func (settings *programSettings) KeepPrograms(programs domain.Programs) error {
	settings.programs = domain.Programs{Enabled: programs.Enabled, Ever: programs.Ever || programs.Enabled}
	return nil
}

type programDoor struct {
	open, refuses, failed bool
	programs              []ProgramSeen
}

func (door *programDoor) Open(context.Context) error {
	if door.refuses {
		door.failed = true
		return errors.New("the socket did not open")
	}
	door.open, door.failed = true, false
	return nil
}
func (door *programDoor) Close() { door.open = false }
func (door *programDoor) State() ProgramDoorState {
	return ProgramDoorState{Open: door.open, Failed: door.failed, Programs: door.programs}
}

type lastReads struct {
	client string
	at     time.Time
}

func (reads lastReads) LastRead(context.Context) (string, time.Time, bool, error) {
	return reads.client, reads.at, reads.client != "", nil
}

// specs/2026-11-15-desk-mcp.md MC3.1, MC3.2: off by default → on: the door opens, the commands stand; a program turned to the
// door and read — the state says who and when, from the journal; off: the door closes, «включали» stays; a door that did not
// open is a refusal; without the program of the bridge there are no commands.
func TestTheReadingByTheProgramsTurnsOnAndOff(t *testing.T) {
	ctx := context.Background()
	settings, door := &programSettings{}, &programDoor{}
	commands := domain.Commands(domain.Bridge{Program: "/usr/bin/canonkeeper-mcp"}, "/home/author/данные", false)
	access := ProgramAccess{Settings: settings, Door: door, Reads: lastReads{}, Commands: &commands}
	state, err := access.Get(ctx)
	if err != nil || state.Enabled || state.Ever || state.Open || state.Failed || state.LastRead != nil {
		t.Fatalf("by default: %+v %v", state, err)
	}
	if state, _ = access.Update(ctx, true); !state.Enabled || !state.Open || state.Failed || state.Commands == nil {
		t.Fatalf("on: %+v", state)
	}
	at := time.Date(2026, 11, 13, 11, 5, 0, 0, time.UTC)
	door.programs = []ProgramSeen{{Name: "Claude Code", At: at, Matches: true}}
	access.Reads = lastReads{client: "Claude Code", at: at}
	if state, _ = access.Get(ctx); len(state.Programs) != 1 || state.LastRead == nil || state.LastRead.Name != "Claude Code" {
		t.Fatalf("a program read: %+v", state)
	}
	if state, _ = access.Update(ctx, false); state.Enabled || !state.Ever || state.Open || state.Failed {
		t.Fatalf("off: %+v", state)
	}
	door.refuses = true
	if state, _ = access.Update(ctx, true); !state.Enabled || state.Open || !state.Failed {
		t.Fatalf("a door that did not open: %+v", state)
	}
	// «Не удалось включить» is the word of the door: turned off, the reading has no failure, and a door not yet tried has none.
	if state, _ = access.Update(ctx, false); state.Failed {
		t.Fatalf("off after a door that did not open: %+v", state)
	}
	settings.programs = domain.Programs{Enabled: true, Ever: true}
	if state, _ = (ProgramAccess{Settings: settings, Door: &programDoor{}, Reads: lastReads{}}).Get(ctx); state.Failed || state.Open {
		t.Fatalf("on, the door not yet tried: %+v", state)
	}
	if state, _ = (ProgramAccess{Settings: settings, Door: door, Reads: lastReads{}}).Get(ctx); state.Commands != nil {
		t.Fatal("commands without the program of the bridge")
	}
}
