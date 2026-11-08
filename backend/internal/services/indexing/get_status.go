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
	return service.getStatus.GetStatus(ctx, input)
}
