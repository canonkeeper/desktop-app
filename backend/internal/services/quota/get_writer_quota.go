package quota

import (
	"context"
	"strings"
	"time"

	domain "canonkeeper/backend/internal/domain/quota"
)

// OverridesReader reads the writer's own values (nil — the default).
type OverridesReader interface {
	Overrides(ctx context.Context, userID string) (map[domain.Key]*int64, error)
}

// GetWriterQuota shows the owner one writer's quotas (writer-quotas R6.1, admin-panel R3.1): the
// limits in force with their use, as getAccountQuota reads them, and the keys that are not the
// default.
type GetWriterQuota struct {
	Reader    AccountQuotaReader
	Overrides OverridesReader
	Now       func() time.Time
}

func (useCase GetWriterQuota) Execute(ctx context.Context, userID string) (domain.WriterQuota, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return domain.WriterQuota{}, domain.ErrNotFound
	}
	overrides, err := useCase.Overrides.Overrides(ctx, userID)
	if err != nil {
		return domain.WriterQuota{}, err
	}
	account, err := useCase.Reader.AccountQuota(ctx, userID, useCase.Now())
	if err != nil {
		return domain.WriterQuota{}, err
	}
	overridden := map[domain.Key]bool{}
	for key, value := range overrides {
		if value != nil {
			overridden[key] = true
		}
	}
	return domain.WriterQuota{Account: account, Overridden: overridden}, nil
}
