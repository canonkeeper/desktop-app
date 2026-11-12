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
	reordered         Reordered
}

// Reordered hears that the volumes of a saga were put in another order: what is gathered in the order of the books — the
// threads of the saga — is to be built again (specs/2026-11-11-ux7-reorder.md RK2.3). It reports nothing back: the move is
// done, and what fails to queue now is queued by the next start.
type Reordered func(ctx context.Context, projectID string)

// WithReordered sets who hears a move of the volumes; without one a move asks for nothing.
func (s *Service) WithReordered(reordered Reordered) *Service {
	s.reordered = reordered
	return s
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
