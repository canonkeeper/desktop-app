package agenttimeline

import (
	"context"
	"encoding/json"
	"strings"

	domain "canonkeeper/backend/internal/domain/agenttimeline"
	"canonkeeper/backend/internal/domain/sagamemory"
)

// Places names the chapters of a project by their keys as the author reads them, «Книга 2, гл. 12 „Белый порт“»; a
// key the text of the saga has no chapter for is left out (specs/2026-11-10-ux1-names.md N1.3).
type Places interface {
	Places(ctx context.Context, projectID string, keys []string) (map[string]string, error)
}

// keyArguments are the arguments of the saga and drawing tools that name a chapter by its key. A paragraph number of
// saga.read under the same name is not a string and is not taken.
var keyArguments = []string{"chapter", "from", "to", "at", "from_chapter"}

// placeMemo keeps what a stream already asked: the key and its place, empty for a key the text has no chapter for.
// A stream sends a snapshot after every item of the thread, and only a new key reads the chapters again.
type placeMemo map[string]string

// chapterKeys are the chapter keys an event shows the author (N2.1).
func chapterKeys(event EventDTO) []string {
	var keys []string
	if event.Tool != nil && len(event.Tool.Arguments) > 0 {
		var arguments map[string]json.RawMessage
		if json.Unmarshal(event.Tool.Arguments, &arguments) == nil {
			for _, name := range keyArguments {
				var key string
				if json.Unmarshal(arguments[name], &key) == nil && strings.TrimSpace(key) != "" && len(key) <= 64 {
					keys = append(keys, strings.TrimSpace(key))
				}
			}
		}
	}
	switch event.Kind {
	case domain.EventUser, domain.EventSteer, domain.EventAssistant:
		keys = append(keys, sagamemory.LinkedKeys(event.Text)...)
	}
	return keys
}

// namePlaces writes beside the keys of each event the places of their chapters, so the feed names a chapter while it
// is still being read and never shows its key (N2.1).
func (handlers *Handlers) namePlaces(ctx context.Context, projectID string, events []EventDTO, memo placeMemo) error {
	keys := make([][]string, len(events))
	var unknown []string
	asked := map[string]bool{}
	for index, event := range events {
		keys[index] = chapterKeys(event)
		for _, key := range keys[index] {
			if _, known := memo[key]; !known && !asked[key] {
				asked[key] = true
				unknown = append(unknown, key)
			}
		}
	}
	if len(unknown) > 0 {
		places, err := handlers.places.Places(ctx, projectID, unknown)
		if err != nil {
			return err
		}
		for _, key := range unknown {
			memo[key] = places[key]
		}
	}
	for index := range events {
		for _, key := range keys[index] {
			if place := memo[key]; place != "" {
				if events[index].Places == nil {
					events[index].Places = map[string]string{}
				}
				events[index].Places[key] = place
			}
		}
	}
	return nil
}
