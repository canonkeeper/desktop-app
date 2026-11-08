package library

import (
	"context"
	"strings"
	"unicode/utf8"

	"canonkeeper/backend/internal/projects"
)

const (
	maxBookTitleLength    = 180
	maxBookSubtitleLength = 240
	maxChapterTitleLength = 180
)

type Service struct {
	store             Store
	ids               IDGenerator
	clock             Clock
	importQueues      ImportQueueProjectionStore
	startImportQueues StartImportQueueStore
}

func NewService(store Store, ids IDGenerator, clock Clock) *Service {
	return &Service{
		store:             store,
		ids:               ids,
		clock:             clock,
		importQueues:      importQueueStoreFor(store),
		startImportQueues: startImportQueueStoreFor(store),
	}
}

func normalizeOrderPlacement(placement OrderPlacement) (OrderPlacement, error) {
	if !placement.Present || placement.AfterID == nil {
		return placement, nil
	}
	afterID := strings.TrimSpace(*placement.AfterID)
	if afterID == "" {
		return OrderPlacement{}, ErrValidation
	}
	return OrderPlacement{Present: true, AfterID: &afterID}, nil
}

func (s *Service) requireRead(ctx context.Context, actor Actor, projectID string) error {
	role, err := s.activeProjectRole(ctx, actor, projectID)
	if err != nil {
		return err
	}
	if !projects.CanReadProject(role) {
		return ErrForbidden
	}
	return nil
}

func (s *Service) requireMutation(ctx context.Context, actor Actor, projectID string) error {
	role, err := s.activeProjectRole(ctx, actor, projectID)
	if err != nil {
		return err
	}
	if !projects.CanMutateProject(role) {
		return ErrForbidden
	}
	return nil
}

func (s *Service) activeProjectRole(ctx context.Context, actor Actor, projectID string) (projects.Role, error) {
	if actor.UserID == "" || projectID == "" {
		return "", ErrNotFound
	}
	return s.store.ActiveProjectRole(ctx, actor.UserID, projectID)
}

func validateRequiredTitle(title string, maxLength int) (string, error) {
	title = strings.TrimSpace(title)
	if title == "" || utf8.RuneCountInString(title) > maxLength {
		return "", ErrValidation
	}
	return title, nil
}
