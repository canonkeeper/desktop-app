package quota

import (
	"context"
	"strings"
	"time"

	domain "canonkeeper/backend/internal/domain/quota"
)

// DefaultsReader reads what the administrator set as the platform's limits by default.
type DefaultsReader interface {
	PlatformDefaults(ctx context.Context) (domain.PlatformDefaults, error)
}

// DefaultsWriter changes the platform's limits by default in one transaction and returns the keys
// whose value changed.
type DefaultsWriter interface {
	SetPlatformDefaults(ctx context.Context, adminID string, changes []domain.OverrideChange, at time.Time) ([]domain.OverrideChanged, error)
}

// GetQuotaDefaults is getAdminQuotaDefaults and the operator's `quota defaults`
// (specs/2026-10-29-admin-panel.md R7.4, R7.10): every limit by default with the value of the
// code, the platform's and the one in force, then the trial drawings of a new account.
type GetQuotaDefaults struct {
	Reader DefaultsReader
}

func (useCase GetQuotaDefaults) Execute(ctx context.Context) (domain.DefaultList, error) {
	platform, err := useCase.Reader.PlatformDefaults(ctx)
	if err != nil {
		return domain.DefaultList{}, err
	}
	list := domain.DefaultList{Items: make([]domain.Default, 0, len(domain.DefaultKeys())), UpdatedAt: platform.UpdatedAt}
	for _, key := range domain.DefaultKeys() {
		item := domain.Default{Key: key, Code: codeDefault(key)}
		if value, set := platform.Values[key]; set {
			item.Platform = &value
		}
		list.Items = append(list.Items, item)
	}
	return list, nil
}

// codeDefault is the original value of a key: quota.Defaults for a writer limit,
// domain.TrialDrawings for the trial drawings.
func codeDefault(key domain.Key) int64 {
	if key == domain.KeyTrialDrawings {
		return domain.TrialDrawings
	}
	return domain.Defaults().Value(key)
}

// SetQuotaDefaults is setAdminQuotaDefaults (R7.5): it checks the change once — known keys, each at
// most once, a value from 0 to what the key keeps or the code's value — and applies it as the
// administrator. It returns the keys whose value changed with the code's value; the caller
// journals one quota_default_changed line per key (R7.7). The new defaults hold from the next
// operation of every writer without his own limit.
type SetQuotaDefaults struct {
	Writer DefaultsWriter
	Now    func() time.Time
}

func (useCase SetQuotaDefaults) Execute(ctx context.Context, adminID string, changes []domain.OverrideChange) ([]domain.DefaultChanged, error) {
	adminID = strings.TrimSpace(adminID)
	if adminID == "" {
		return nil, domain.ErrInvalidOverride
	}
	if err := domain.CheckDefaults(changes); err != nil {
		return nil, err
	}
	changed, err := useCase.Writer.SetPlatformDefaults(ctx, adminID, changes, useCase.Now().UTC())
	if err != nil {
		return nil, err
	}
	result := make([]domain.DefaultChanged, len(changed))
	for index, item := range changed {
		result[index] = domain.DefaultChanged{OverrideChanged: item, Code: codeDefault(item.Key)}
	}
	return result, nil
}
