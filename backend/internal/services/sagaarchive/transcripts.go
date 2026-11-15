// SPDX-License-Identifier: AGPL-3.0-only

package sagaarchive

import (
	"context"
	"regexp"

	"canonkeeper/backend/internal/domain/sagamemory"
)

// TranscriptStore reads the chats a saga file brought into a saga (SF3.8): a reader who may not read the saga gets
// ErrNotFound, as an unknown chat.
type TranscriptStore interface {
	Transcripts(ctx context.Context, actorUserID, projectID string) ([]Transcript, error)
	Transcript(ctx context.Context, actorUserID, projectID, transcriptID string) (Transcript, error)
}

// Outlines read the text of a saga as the chat reads it, with the keys of its chapters.
type Outlines interface {
	Outline(ctx context.Context, projectID, view string) (*sagamemory.Outline, error)
}

// ListTranscripts is listSagaTranscripts.
type ListTranscripts struct{ Store TranscriptStore }

func (useCase ListTranscripts) Execute(ctx context.Context, actorUserID, projectID string) ([]Transcript, error) {
	return useCase.Store.Transcripts(ctx, actorUserID, projectID)
}

// GetTranscript is getSagaTranscript: the chat with the place of every chapter its answers cite, as the saga reads it now
// — the keys of the chapters of an opened saga are the keys they had, so the links of the answers lead into it (SF3.8).
type GetTranscript struct {
	Store    TranscriptStore
	Outlines Outlines
}

// sourceKey is the key of a chapter in a link of an answer: [гл. 12 ¶5](source:12/5).
var sourceKey = regexp.MustCompile(`\]\(source:([^)\s/]+)`)

func (useCase GetTranscript) Execute(ctx context.Context, actorUserID, projectID, transcriptID string) (Transcript, map[string]string, error) {
	transcript, err := useCase.Store.Transcript(ctx, actorUserID, projectID, transcriptID)
	if err != nil {
		return Transcript{}, nil, err
	}
	places := map[string]string{}
	var outline *sagamemory.Outline
	for _, turn := range transcript.Turns {
		for _, match := range sourceKey.FindAllStringSubmatch(turn.Answer, -1) {
			key := match[1]
			if _, named := places[key]; named {
				continue
			}
			if outline == nil {
				if outline, err = useCase.Outlines.Outline(ctx, projectID, "published"); err != nil {
					return Transcript{}, nil, err
				}
			}
			if chapter, found := outline.Version.Find(key); found {
				places[key] = chapter.Place()
			}
		}
	}
	return transcript, places, nil
}
