// SPDX-License-Identifier: AGPL-3.0-only

package sagamemory

import (
	"context"
	"time"

	domain "canonkeeper/backend/internal/domain/sagamemory"
	service "canonkeeper/backend/internal/services/sagamemory"
	storedb "canonkeeper/backend/internal/store/db"
	"canonkeeper/backend/internal/store/sqlc"

	"github.com/jackc/pgx/v5/pgtype"
)

// CardModelAllowed reports whether the cards Job may call the model for a saga in the desktop build
// (specs/2026-11-07-desktop-app.md, DA8.1): only when the author's consent is kept and not paused.
func (repository *Repository) CardModelAllowed(ctx context.Context, projectID string) (bool, error) {
	project, err := storedb.ParseUUID(projectID)
	if err != nil {
		return false, service.ErrInvalid
	}
	return sqlc.New(repository.db.Pool).CardModelAllowed(ctx, project)
}

// SetCardBuildPermission keeps the consent of the author to build the cards of a live saga (DA8.4), replacing the one
// it held. A saga that is gone or being deleted is not written.
func (repository *Repository) SetCardBuildPermission(ctx context.Context, projectID string, permission domain.CardBuildPermission, at time.Time) error {
	project, err := storedb.ParseUUID(projectID)
	if err != nil || at.IsZero() {
		return service.ErrInvalid
	}
	if err := permission.Validate(); err != nil {
		return err
	}
	written, err := sqlc.New(repository.db.Pool).UpsertCardBuildPermission(ctx, sqlc.UpsertCardBuildPermissionParams{
		ProjectID:       project,
		State:           string(permission.State),
		BudgetUsdMicros: optionalInt8(permission.BudgetUSDMicros),
		BudgetTokens:    optionalInt8(permission.BudgetTokens),
		Since:           pgtype.Timestamptz{Time: permission.Since, Valid: true},
		UpdatedAt:       pgtype.Timestamptz{Time: at, Valid: true},
	})
	if err != nil {
		return err
	}
	if written == 0 {
		return service.ErrInvalid
	}
	return nil
}

func optionalInt8(value *int64) pgtype.Int8 {
	if value == nil {
		return pgtype.Int8{}
	}
	return pgtype.Int8{Int64: *value, Valid: true}
}
