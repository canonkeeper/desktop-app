package quotastore

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"canonkeeper/backend/internal/domain/quota"
	storedb "canonkeeper/backend/internal/store/db"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// The platform's limits by default (specs/2026-10-29-admin-panel.md UC7): one row of
// platform_quota_defaults that appears on the first change; no row and NULL keep the code's value.

// PlatformDefaults reads what the administrator set (R7.4); nothing set is empty values.
func (store *Store) PlatformDefaults(ctx context.Context) (quota.PlatformDefaults, error) {
	return readPlatformDefaults(ctx, store.db.Pool)
}

// SetPlatformDefaults is the port of services/quota.SetQuotaDefaults (R7.5): the changes in one
// transaction under the lock of the defaults, which serialises the administrator's changes while
// the quota checks read on. Only a key whose value changes is written and returned, with the
// moment and the administrator; a repeat of the same change writes nothing.
func (store *Store) SetPlatformDefaults(ctx context.Context, adminID string, changes []quota.OverrideChange, at time.Time) ([]quota.OverrideChanged, error) {
	if err := quota.CheckDefaults(changes); err != nil {
		return nil, err
	}
	admin, err := storedb.ParseUUID(adminID)
	if err != nil || at.IsZero() {
		return nil, quota.ErrInvalidOverride
	}
	var changed []quota.OverrideChanged
	err = store.db.WithTx(ctx, func(tx pgx.Tx) error {
		// The row may not exist yet, so FOR UPDATE could lock nothing (like lockOwnerQuotaInTx).
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('platform_quota_defaults', 0))`); err != nil {
			return err
		}
		current, err := readPlatformDefaults(ctx, tx)
		if err != nil {
			return err
		}
		columns := make([]string, 0, len(changes))
		args := []any{at, admin}
		for _, change := range changes {
			before, set := current.Values[change.Key]
			if set == (change.Value != nil) && (!set || before == *change.Value) {
				continue
			}
			item := quota.OverrideChanged{Key: change.Key, After: change.Value}
			if set {
				item.Before = &before
			}
			changed = append(changed, item)
			// The key is one of quota.DefaultKeys, so it is a column name and safe to splice.
			columns = append(columns, string(change.Key))
			args = append(args, change.Value)
		}
		if len(columns) == 0 {
			return nil
		}
		values, updates := make([]string, len(columns)), make([]string, len(columns))
		for index, column := range columns {
			values[index] = fmt.Sprintf("$%d", index+3)
			updates[index] = column + " = EXCLUDED." + column
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO platform_quota_defaults (updated_at, updated_by, `+strings.Join(columns, ", ")+`)
			VALUES ($1, $2, `+strings.Join(values, ", ")+`)
			ON CONFLICT (singleton) DO UPDATE
			SET updated_at = EXCLUDED.updated_at, updated_by = EXCLUDED.updated_by, `+strings.Join(updates, ", "), args...)
		return err
	})
	if err != nil {
		return nil, err
	}
	return changed, nil
}

func readPlatformDefaults(ctx context.Context, db querier) (quota.PlatformDefaults, error) {
	keys := quota.DefaultKeys()
	values := make([]*int64, len(keys))
	var updatedAt pgtype.Timestamptz
	var updatedBy pgtype.UUID
	targets := make([]any, 0, len(keys)+2)
	for index := range values {
		targets = append(targets, &values[index])
	}
	targets = append(targets, &updatedAt, &updatedBy)
	err := db.QueryRow(ctx, `SELECT `+limitColumns("platform", keys)+`, platform.updated_at, platform.updated_by
		FROM platform_quota_defaults AS platform`).Scan(targets...)
	if errors.Is(err, pgx.ErrNoRows) {
		return quota.PlatformDefaults{Values: quota.Settings{}}, nil
	}
	if err != nil {
		return quota.PlatformDefaults{}, err
	}
	defaults := quota.PlatformDefaults{Values: settingsOf(keys, values), UpdatedBy: storedb.UUIDString(updatedBy)}
	if updatedAt.Valid {
		moment := updatedAt.Time.UTC()
		defaults.UpdatedAt = &moment
	}
	return defaults, nil
}
