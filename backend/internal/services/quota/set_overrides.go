package quota

import (
	"context"
	"strings"
	"time"

	domain "canonkeeper/backend/internal/domain/quota"
)

// OverridesWriter applies the changes to one live writer in one transaction; an unknown or
// deleted writer is domain.ErrNotFound.
type OverridesWriter interface {
	SetOverrides(ctx context.Context, userID string, changes []domain.OverrideChange, at time.Time) ([]domain.OverrideChanged, error)
}

// SetOverrides changes a writer's limits (writer-quotas UC6, admin-panel R3.2): the operator's
// `quota set` and the admin page call it alike. It returns every changed key with its value
// before and after; the caller writes one quota_override_changed line per key. The new limits
// hold from the writer's next operation.
type SetOverrides struct {
	Writer OverridesWriter
	Now    func() time.Time
}

func (useCase SetOverrides) Execute(ctx context.Context, userID string, changes []domain.OverrideChange) ([]domain.OverrideChanged, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return nil, domain.ErrNotFound
	}
	if err := domain.CheckOverrides(changes); err != nil {
		return nil, err
	}
	return useCase.Writer.SetOverrides(ctx, userID, changes, useCase.Now())
}
