// Package quota holds the quota use cases (specs/2026-10-28-writer-quotas-and-fair-queue.md): the
// writer reads his own quotas; the owner reads and changes a writer's quotas from the operator
// command or the admin page (UC6, specs/2026-10-29-admin-panel.md UC3).
package quota

import (
	"context"
	"strings"
	"time"

	domain "canonkeeper/backend/internal/domain/quota"
)

// AccountQuotaReader is the narrow port of getAccountQuota.
type AccountQuotaReader interface {
	AccountQuota(ctx context.Context, userID string, at time.Time) (domain.Account, error)
}

// GetAccountQuota returns the signed-in writer's own limits and use (R5.1).
type GetAccountQuota struct {
	Reader AccountQuotaReader
	Now    func() time.Time
}

func (useCase GetAccountQuota) Execute(ctx context.Context, userID string) (domain.Account, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return domain.Account{}, domain.ErrNotFound
	}
	return useCase.Reader.AccountQuota(ctx, userID, useCase.Now())
}
