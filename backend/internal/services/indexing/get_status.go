package indexing

import (
	"context"
	"strings"

	"canonkeeper/backend/internal/domain/canon"
	indexingdomain "canonkeeper/backend/internal/domain/indexing"
)

type GetStatusInput struct {
	ActorUserID string
	ProjectID   string
	BookID      string
	SourceView  indexingdomain.SourceView
	// Waiting is what the reading of the saga waits for, as the build says it (specs/2026-11-12-desk-window.md WN3.1): the
	// store then counts the reading of a text nobody reads and promises no time and no letter. None — the reading goes on.
	Waiting canon.PreparationWaiting
}

// Helper says whether the helper of the build is connected (specs/2026-11-12-desk-window.md WN3.1): the desktop has none
// until the author connects one; the site always has it and gives no port.
type Helper interface {
	Connected(ctx context.Context) bool
}

// Reading is the reading of the books of a saga in a build that asks its author (specs/2026-11-13-desk-consent.md CN3.8):
// what it waits for — the consent or a pause — and its state by the counts of the status. The app gives it, because the
// author pays for the reading; the site gives no port and its status is as it was.
type Reading interface {
	Waiting(ctx context.Context, projectID string) (canon.PreparationWaiting, error)
	State(ctx context.Context, projectID string, counts canon.IndexingPreparation) (*canon.IndexingReading, error)
}

type GetStatusPort interface {
	GetStatus(context.Context, GetStatusInput) (canon.IndexingStatusResponse, error)
}

func (service *Service) GetStatus(ctx context.Context, input GetStatusInput) (canon.IndexingStatusResponse, error) {
	if service == nil || service.getStatus == nil {
		return canon.IndexingStatusResponse{}, ErrUnavailable
	}
	if ctx == nil || strings.TrimSpace(input.ActorUserID) == "" || strings.TrimSpace(input.ProjectID) == "" ||
		(input.BookID == "") != (input.SourceView == "") {
		return canon.IndexingStatusResponse{}, ErrInvalid
	}
	if input.BookID != "" && (strings.TrimSpace(input.BookID) == "" ||
		input.SourceView != indexingdomain.SourceDraft && input.SourceView != indexingdomain.SourcePublished) {
		return canon.IndexingStatusResponse{}, ErrInvalid
	}
	input.Waiting = ""
	switch {
	case service.helper != nil && !service.helper.Connected(ctx):
		input.Waiting = canon.PreparationWaitingHelper
	case service.reading != nil:
		waiting, err := service.reading.Waiting(ctx, input.ProjectID)
		if err != nil {
			return canon.IndexingStatusResponse{}, err
		}
		input.Waiting = waiting
	}
	status, err := service.getStatus.GetStatus(ctx, input)
	if err != nil {
		return canon.IndexingStatusResponse{}, err
	}
	if service.reading != nil && input.Waiting != canon.PreparationWaitingHelper && status.Counts != nil {
		if status.Reading, err = service.reading.State(ctx, input.ProjectID, *status.Counts); err != nil {
			return canon.IndexingStatusResponse{}, err
		}
	}
	status.Counts = nil
	// The store promises the letter to whom the wave will be told to; whether he is written to at all is the account's
	// (FS5.6). A promise nobody can vouch for is not made.
	if status.Preparation != nil && status.Preparation.LetterPromised {
		promised := false
		if service.letters != nil {
			promised, err = service.letters.Promised(ctx, input.ActorUserID)
			promised = promised && err == nil
		}
		status.Preparation.LetterPromised = promised
	}
	return status, nil
}
