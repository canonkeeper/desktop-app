package quota

import (
	"context"
	"strings"
	"time"

	domain "canonkeeper/backend/internal/domain/quota"
)

// LimitSourcesReader reads where each limit of a writer comes from: his own value, his plan or the default
// (specs/2026-11-08-subscription-tiers.md TR2.6).
type LimitSourcesReader interface {
	LimitSources(ctx context.Context, userID string) (map[domain.Key]domain.Source, error)
}

// GetWriterQuota shows the owner one writer's quotas (writer-quotas R6.1, admin-panel R3.1): the
// limits in force with their use, as getAccountQuota reads them, and the source of each.
type GetWriterQuota struct {
	Reader  AccountQuotaReader
	Sources LimitSourcesReader
	Now     func() time.Time
}

func (useCase GetWriterQuota) Execute(ctx context.Context, userID string) (domain.WriterQuota, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return domain.WriterQuota{}, domain.ErrNotFound
	}
	sources, err := useCase.Sources.LimitSources(ctx, userID)
	if err != nil {
		return domain.WriterQuota{}, err
	}
	account, err := useCase.Reader.AccountQuota(ctx, userID, useCase.Now())
	if err != nil {
		return domain.WriterQuota{}, err
	}
	return domain.WriterQuota{Account: account, LimitSources: sources}, nil
}
