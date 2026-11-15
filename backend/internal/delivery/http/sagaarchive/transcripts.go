// SPDX-License-Identifier: AGPL-3.0-only

package sagaarchive

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	domain "canonkeeper/backend/internal/domain/sagaarchive"
	service "canonkeeper/backend/internal/services/sagaarchive"

	"github.com/go-chi/chi/v5"
)

// ListTranscriptsUseCase lists the chats a saga file brought.
type ListTranscriptsUseCase interface {
	Execute(ctx context.Context, actorUserID, projectID string) ([]service.Transcript, error)
}

// GetTranscriptUseCase reads one of them with the places of the chapters its answers cite.
type GetTranscriptUseCase interface {
	Execute(ctx context.Context, actorUserID, projectID, transcriptID string) (service.Transcript, map[string]string, error)
}

type transcriptSummary struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	CreatedAt   string `json:"createdAt"`
	LastTurnAt  string `json:"lastTurnAt"`
	Turns       int    `json:"turns"`
	OthersTurns int    `json:"othersTurns"`
}

type transcriptTurn struct {
	AskedAt  string `json:"askedAt"`
	Question string `json:"question"`
	Answer   string `json:"answer,omitempty"`
	Outcome  string `json:"outcome"`
	Attached string `json:"attached,omitempty"`
}

type transcriptBody struct {
	ID          string            `json:"id"`
	Title       string            `json:"title"`
	CreatedAt   string            `json:"createdAt"`
	LastTurnAt  string            `json:"lastTurnAt"`
	OthersTurns int               `json:"othersTurns"`
	Turns       []transcriptTurn  `json:"turns"`
	Places      map[string]string `json:"places"`
}

func moment(value time.Time) string { return value.UTC().Format("2006-01-02T15:04:05.000Z07:00") }

// MountTranscripts serves listSagaTranscripts and getSagaTranscript (SF3.8): reads under the session of a reader of the
// saga.
func MountTranscripts(router chi.Router, list ListTranscriptsUseCase, get GetTranscriptUseCase, currentUser CurrentUserService) {
	if list == nil || get == nil || currentUser == nil {
		panic("saga transcripts HTTP: dependencies are required")
	}
	router.Get("/projects/{projectId}/transcripts", func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Cache-Control", "no-store")
		user, ok := currentAuthor(response, request, currentUser)
		if !ok {
			return
		}
		transcripts, err := list.Execute(request.Context(), user.ID, chi.URLParam(request, "projectId"))
		if err != nil {
			writeTranscriptError(response, err)
			return
		}
		data := make([]transcriptSummary, len(transcripts))
		for index, transcript := range transcripts {
			data[index] = transcriptSummary{
				ID: transcript.ID, Title: transcript.Title, CreatedAt: moment(transcript.CreatedAt), LastTurnAt: moment(transcript.LastTurnAt),
				Turns: len(transcript.Turns), OthersTurns: transcript.OthersTurns,
			}
		}
		response.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(response).Encode(map[string]any{"data": data})
	})
	router.Get("/projects/{projectId}/transcripts/{transcriptId}", func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Cache-Control", "no-store")
		user, ok := currentAuthor(response, request, currentUser)
		if !ok {
			return
		}
		transcript, places, err := get.Execute(request.Context(), user.ID, chi.URLParam(request, "projectId"), chi.URLParam(request, "transcriptId"))
		if err != nil {
			writeTranscriptError(response, err)
			return
		}
		body := transcriptBody{
			ID: transcript.ID, Title: transcript.Title, CreatedAt: moment(transcript.CreatedAt), LastTurnAt: moment(transcript.LastTurnAt),
			OthersTurns: transcript.OthersTurns, Turns: make([]transcriptTurn, len(transcript.Turns)), Places: places,
		}
		for index, turn := range transcript.Turns {
			body.Turns[index] = presentTurn(turn)
		}
		response.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(response).Encode(body)
	})
}

func presentTurn(turn domain.ChatTurn) transcriptTurn {
	return transcriptTurn{AskedAt: moment(turn.AskedAt), Question: turn.Question, Answer: turn.Answer, Outcome: turn.Outcome, Attached: turn.Attached}
}

func writeTranscriptError(response http.ResponseWriter, err error) {
	if errors.Is(err, service.ErrNotFound) {
		writeProblem(response, http.StatusNotFound, "Not Found", "chat not found", "not_found")
		return
	}
	writeProblem(response, http.StatusInternalServerError, "Internal Server Error", "unexpected server error", "server.error")
}
