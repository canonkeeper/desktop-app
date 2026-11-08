// SPDX-License-Identifier: AGPL-3.0-only

// Package sagaarchive is the saga file canonkeeper-saga v1 of the site and the desktop app
// (specs/2026-11-07-desktop-app.md DA12.1): one format written and read by one code. It holds the builder of the
// transcripts of chats, which the copy of an account's data on the site reads too (DA12.1.5). It performs no I/O.
package sagaarchive

import (
	"strings"

	timeline "canonkeeper/backend/internal/domain/agenttimeline"
)

// Outcomes of a turn in a transcript.
const (
	TurnAnswered    = "answered"
	TurnInterrupted = "interrupted"
	TurnFailed      = "failed"
	TurnInProgress  = "in_progress"
)

// TranscriptTurn is a turn of a chat as a transcript holds it: the question, what the author told the agent during
// the turn (specs/2026-10-30-chat-steering.md R3.7), the answer of a completed turn and the outcome.
type TranscriptTurn struct {
	ProviderTurnID string
	Question       string
	Corrections    []string
	Answer         string
	Outcome        string
}

// Transcript reads the turns of a chat root thread: the question, and the last agent remark of a completed turn as
// its answer, like the chat feed (feed-model.ts#buildTurn). Steps, reasoning and tool calls stay out; a turn without
// a question of the writer (no public assignment) is not one.
func Transcript(chatID string, items []timeline.RawItem) ([]TranscriptTurn, error) {
	snapshot, err := timeline.Project(chatID, items)
	if err != nil {
		return nil, err
	}
	turns := make([]TranscriptTurn, 0)
	index := make(map[string]int)
	lastRemark := make(map[string]string)
	for _, event := range snapshot.Events {
		if event.Role != timeline.RoleMain {
			continue
		}
		position, seen := index[event.TurnID]
		if !seen {
			position = len(turns)
			index[event.TurnID] = position
			turns = append(turns, TranscriptTurn{ProviderTurnID: event.TurnID, Outcome: TurnInProgress})
		}
		turn := &turns[position]
		switch event.Kind {
		case timeline.EventUser:
			if turn.Question == "" {
				turn.Question = event.Text
			}
		case timeline.EventSteer:
			turn.Corrections = append(turn.Corrections, event.Text)
		case timeline.EventAssistant:
			if strings.TrimSpace(event.Text) != "" {
				lastRemark[event.TurnID] = event.Text
			}
		case timeline.EventTurnOutcome:
			turn.Outcome = turnOutcome(event.Outcome)
		}
	}
	kept := turns[:0]
	for _, turn := range turns {
		if turn.Question == "" {
			continue
		}
		if turn.Outcome == TurnAnswered {
			turn.Answer = lastRemark[turn.ProviderTurnID]
		}
		kept = append(kept, turn)
	}
	return kept, nil
}

func turnOutcome(outcome timeline.Outcome) string {
	switch outcome {
	case timeline.OutcomeCompleted:
		return TurnAnswered
	case timeline.OutcomeInterrupted:
		return TurnInterrupted
	case timeline.OutcomeFailed:
		return TurnFailed
	default:
		return TurnInProgress
	}
}
