package quota

import (
	"context"
	"slices"
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
// code, the platform's and the one in force. The keys the plan of the build sets have no platform
// default and are left out (specs/2026-11-08-admin-redesign.md AR7.1).
type GetQuotaDefaults struct {
	Reader DefaultsReader
	// Plan are the keys the writer's plan of this build always sets — the site's tier; none on the desktop.
	Plan []domain.Key
}

func (useCase GetQuotaDefaults) Execute(ctx context.Context) (domain.DefaultList, error) {
	platform, err := useCase.Reader.PlatformDefaults(ctx)
	if err != nil {
		return domain.DefaultList{}, err
	}
	list := domain.DefaultList{Items: make([]domain.Default, 0, len(domain.Keys())), UpdatedAt: platform.UpdatedAt}
	for _, key := range domain.Keys() {
		if slices.Contains(useCase.Plan, key) {
			continue
		}
		item := domain.Default{Key: key, Code: domain.Defaults().Value(key)}
		if value, set := platform.Values[key]; set {
			item.Platform = &value
		}
		list.Items = append(list.Items, item)
	}
	return list, nil
}

// SetQuotaDefaults is setAdminQuotaDefaults (R7.5): it checks the change once — known keys, each at
// most once, a value from 0 to what the key keeps or the code's value — and applies it as the
// administrator. It returns the keys whose value changed with the code's value; the caller
// journals one quota_default_changed line per key (R7.7). The new defaults hold from the next
// operation of every writer without his own limit. A key the plan of the build sets is refused
// (specs/2026-11-08-admin-redesign.md AR7.1): its limit is the tier's alone.
type SetQuotaDefaults struct {
	Writer DefaultsWriter
	Now    func() time.Time
	// Plan are the keys the writer's plan of this build always sets — the site's tier; none on the desktop.
	Plan []domain.Key
}

func (useCase SetQuotaDefaults) Execute(ctx context.Context, adminID string, changes []domain.OverrideChange) ([]domain.DefaultChanged, error) {
	adminID = strings.TrimSpace(adminID)
	if adminID == "" {
		return nil, domain.ErrInvalidOverride
	}
	if err := domain.CheckOverrides(changes); err != nil {
		return nil, err
	}
	for _, change := range changes {
		if slices.Contains(useCase.Plan, change.Key) {
			return nil, domain.ErrInvalidOverride
		}
	}
	changed, err := useCase.Writer.SetPlatformDefaults(ctx, adminID, changes, useCase.Now().UTC())
	if err != nil {
		return nil, err
	}
	result := make([]domain.DefaultChanged, len(changed))
	for index, item := range changed {
		result[index] = domain.DefaultChanged{OverrideChanged: item, Code: domain.Defaults().Value(item.Key)}
	}
	return result, nil
}
