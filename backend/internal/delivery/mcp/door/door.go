// SPDX-License-Identifier: AGPL-3.0-only

// Package door is the door of the app for the bridge of the programs of the author (specs/2026-11-15-desk-mcp.md MC1.5–MC1.7,
// MC2): one line of a request over a local socket, one line of its answer. It answers only the names of its set, finds the
// saga of the argument, calls the handler of the chat built for the call, puts the words of a program in place of the words
// of a turn, cuts a long answer into pages and has the reading recorded before the answer goes. It holds no record of the
// journal: the readings of the programs write it.
package door

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"canonkeeper/backend/internal/delivery/codex/researchchat/toolspecs"
	"canonkeeper/backend/internal/delivery/mcp/programtools"
	"canonkeeper/backend/internal/domain/agent"
	"canonkeeper/backend/internal/services/programreads"
	researchservice "canonkeeper/backend/internal/services/researchchat"
)

// Reads are the readings of the programs of the local author the door calls.
type Reads interface {
	Sagas(ctx context.Context) ([]programreads.Saga, error)
	Find(ctx context.Context, argument string) (programreads.Saga, error)
	Record(ctx context.Context, read programreads.Read) error
}

// Handler answers one call of a tool of the chat, as it answers Codex.
type Handler func(context.Context, agent.ToolCall) (agent.ToolResult, error)

// Handlers builds the handler of the chat for one call: the reading of the local author in the saga without a turn, naming
// what it shows to the naming of the call. The composition of the app builds it, a fresh one for every call.
type Handlers func(projectID string, naming researchservice.Naming) Handler

// Program is a program that turned to the door since the start of the app (MC3.1): its name, the time of its last word,
// whether its bridge has the set of the door and whether it is the copy in the extension of Claude Desktop.
type Program struct {
	Name      string
	At        time.Time
	Matches   bool
	Extension bool
}

// caller is the context of a call of a program: no thread and no turn of Codex — the reading has no manifest to keep.
const caller = "program"

// Door answers the lines of the bridges.
type Door struct {
	reads    Reads
	handlers Handlers
	version  string
	now      func() time.Time

	mu       sync.Mutex
	programs []Program
}

// New makes the door of an app of a version over the readings of its author and the handler of its chat.
func New(reads Reads, handlers Handlers, version string) *Door {
	if reads == nil || handlers == nil {
		panic("door: the readings and the handlers are required")
	}
	return &Door{reads: reads, handlers: handlers, version: version, now: time.Now}
}

// Version is the version of the app the door answers for.
func (door *Door) Version() string { return door.version }

// Programs are the programs that turned to the door since the start, newest first; nothing is kept beyond the process.
func (door *Door) Programs() []Program {
	door.mu.Lock()
	defer door.mu.Unlock()
	programs := slices.Clone(door.programs)
	slices.SortStableFunc(programs, func(a, b Program) int { return b.At.Compare(a.At) })
	return programs
}

// saw keeps a word of a program: its first word on initialize and every call (MC1.5).
func (door *Door) saw(request programtools.Request) {
	program := Program{
		Name: programreads.ProgramName(request.Client, request.Title), At: door.now(),
		Matches: request.Fingerprint == programtools.Fingerprint(), Extension: request.Kind == programtools.KindExtension,
	}
	door.mu.Lock()
	defer door.mu.Unlock()
	for index := range door.programs {
		if door.programs[index].Name == program.Name {
			door.programs[index] = program
			return
		}
	}
	door.programs = append(door.programs, program)
}

func refused(words string) programtools.Answer { return programtools.Answer{Text: words, Error: true} }

// Answer answers one request of a bridge.
func (door *Door) Answer(ctx context.Context, request programtools.Request) programtools.Answer {
	door.saw(request)
	if request.Hello {
		return programtools.Answer{}
	}
	if request.Fingerprint != programtools.Fingerprint() {
		return refused(programtools.Mismatch(door.version, request.Version, request.Kind))
	}
	if !slices.Contains(programtools.Names(), request.Tool) {
		return refused(programtools.WordsNoTool())
	}
	arguments, saga, page, err := split(request.Arguments)
	if err != nil {
		return refused(invalid(request.Tool))
	}
	asked, err := parsePage(page)
	if err != nil {
		return refused(programtools.WordsPageForm)
	}
	read := programreads.Read{Client: request.Client, Title: request.Title}
	var text string
	if request.Tool == programtools.Sagas {
		if len(arguments) > 0 || saga != nil {
			return refused(invalid(request.Tool))
		}
		sagas, err := door.reads.Sagas(ctx)
		if err != nil {
			return refused(programtools.WordsNoAnswer)
		}
		if len(sagas) == 0 {
			return refused(programtools.WordsNoSagas)
		}
		text, read.Sagas = sagasText(sagas), len(sagas)
	} else {
		argument := ""
		if saga != nil {
			argument = *saga
		}
		found, err := door.reads.Find(ctx, argument)
		if err != nil {
			return refused(sagaWords(err))
		}
		naming := programreads.NewNaming()
		raw, err := json.Marshal(arguments)
		if err != nil {
			return refused(invalid(request.Tool))
		}
		result, err := door.handlers(found.ID, naming)(ctx, agent.ToolCall{
			Caller: agent.ToolCaller{ThreadID: caller, RootThreadID: caller}, TurnID: caller, CallID: caller,
			Namespace: toolspecs.Namespace, Tool: request.Tool, Arguments: raw,
		})
		if err != nil {
			return refused(programtools.WordsNoAnswer)
		}
		if !result.Success {
			return refused(failureWords(result.Payload))
		}
		text, read.ProjectID, read.Named = string(result.Payload), found.ID, naming.Named()
	}
	shown, _, err := pageOf(text, PageBound(request.Client), asked)
	switch {
	case errors.Is(err, errChanged):
		return refused(programtools.WordsChanged)
	case errors.Is(err, errNoPage):
		return refused(programtools.WordsNoPage(asked.number, pagesOf(text, PageBound(request.Client))))
	case err != nil:
		return refused(programtools.WordsNoAnswer)
	}
	read.Bytes, read.Page, read.Pages = int64(len(shown.text)), shown.number, shown.pages
	if err := door.reads.Record(ctx, read); err != nil {
		if errors.Is(err, programreads.ErrNoSuch) {
			return refused(programtools.WordsNoSuch)
		}
		return refused(programtools.WordsNotRecord)
	}
	return programtools.Answer{Text: shown.text}
}

// pagesOf is the number of the pages of an answer.
func pagesOf(text string, bound int) int {
	first, _, err := pageOf(text, bound, pageAsked{number: 1})
	if err != nil {
		return 1
	}
	return first.pages
}

// split takes saga and page off the arguments; the rest goes to the handler of the chat as it came. No arguments are {}.
func split(raw json.RawMessage) (map[string]json.RawMessage, *string, string, error) {
	arguments := map[string]json.RawMessage{}
	if len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &arguments); err != nil || arguments == nil {
			return nil, nil, "", errors.New("door: the arguments are no object")
		}
	}
	var saga *string
	if value, ok := arguments[programtools.SagaProperty]; ok {
		var text string
		if err := json.Unmarshal(value, &text); err != nil {
			return nil, nil, "", err
		}
		saga = &text
		delete(arguments, programtools.SagaProperty)
	}
	page := ""
	if value, ok := arguments[programtools.PageProperty]; ok {
		if err := json.Unmarshal(value, &page); err != nil {
			return nil, nil, "", err
		}
		delete(arguments, programtools.PageProperty)
	}
	return arguments, saga, page, nil
}

// invalid are the words of arguments that do not fit the schema of a tool, as the handler of the chat says them.
func invalid(tool string) string {
	payload, _ := json.Marshal(failure{
		Status: "error", Type: "invalid_arguments", Path: "$", Message: "аргументы не соответствуют схеме " + tool,
		NextValidActions: []string{"correct_arguments_and_retry"},
	})
	return string(payload)
}

func sagaWords(err error) string {
	switch {
	case errors.Is(err, programreads.ErrNoSagas):
		return programtools.WordsNoSagas
	case errors.Is(err, programreads.ErrSeveral):
		return programtools.WordsSeveral
	case errors.Is(err, programreads.ErrNoSuch):
		return programtools.WordsNoSuch
	case errors.Is(err, programreads.ErrAmbiguous):
		return programtools.WordsAmbiguous
	default:
		return programtools.WordsNoAnswer
	}
}

// failure is the refusal of the handler of the chat as it gives it Codex.
type failure struct {
	Status           string   `json:"status"`
	Type             string   `json:"type"`
	Path             string   `json:"path"`
	Message          string   `json:"message"`
	NextValidActions []string `json:"next_valid_actions"`
}

// failureWords are the refusal of the handler as it is, with the words of two kinds put in the words of a program (MC2.7): a
// turn that can read the saga no more is a saga that is not there, a saga in preparation is said for a program.
func failureWords(payload json.RawMessage) string {
	var refusal failure
	if err := json.Unmarshal(payload, &refusal); err != nil {
		return string(payload)
	}
	switch refusal.Type {
	case "authority_lost":
		refusal.Message = programtools.WordsNoSuch
	case "saga_not_ready":
		refusal.Message = programtools.WordsNotReady
	default:
		return string(payload)
	}
	replaced, err := json.Marshal(refusal)
	if err != nil {
		return string(payload)
	}
	return string(replaced)
}

// sagasText is the answer of sagas: every saga with its key, its books and chapters.
func sagasText(sagas []programreads.Saga) string {
	var text strings.Builder
	fmt.Fprintf(&text, "Саги автора в CanonKeeper: %d. Ключ саги называйте в аргументе saga.\n", len(sagas))
	for index, saga := range sagas {
		fmt.Fprintf(&text, "%d. «%s» — saga: %q; книг: %d, глав: %d\n", index+1, saga.Title, saga.Key, saga.Books, saga.Chapters)
	}
	return text.String()
}
