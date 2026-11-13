package quotastore

import (
	"context"
	"errors"
	"fmt"
	"time"

	"canonkeeper/backend/internal/domain/quota"
	storedb "canonkeeper/backend/internal/store/db"

	"github.com/jackc/pgx/v5"
)

// LimitSources is the port of services/quota.GetWriterQuota (specs/2026-11-08-subscription-tiers.md TR2.6): where each
// limit of the writer comes from — his own value, his plan or the default — by the order of quota.Resolve.
func (store *Store) LimitSources(ctx context.Context, userID string) (map[quota.Key]quota.Source, error) {
	overrides, err := Overrides(ctx, store.db.Pool, userID)
	if err != nil {
		return nil, err
	}
	own := quota.Settings{}
	for key, value := range overrides {
		if value != nil {
			own[key] = *value
		}
	}
	plan, err := store.fence.planInTx(ctx, store.db.Pool, userID)
	if err != nil {
		return nil, err
	}
	return quota.LimitSources(own, plan), nil
}

// SetOverrides is the port of services/quota.SetOverrides: the changes to one live writer in one
// transaction (UC6). An unknown or deleted writer is quota.ErrNotFound and changes nothing.
func (store *Store) SetOverrides(ctx context.Context, userID string, changes []quota.OverrideChange, at time.Time) ([]quota.OverrideChanged, error) {
	if _, err := storedb.ParseUUID(userID); err != nil {
		return nil, quota.ErrNotFound
	}
	var changed []quota.OverrideChanged
	err := store.db.WithTx(ctx, func(tx pgx.Tx) error {
		var live bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS (SELECT 1 FROM users WHERE id = $1::uuid AND deleted_at IS NULL)
		`, userID).Scan(&live); err != nil {
			return err
		}
		if !live {
			return quota.ErrNotFound
		}
		var err error
		changed, err = SetOverridesInTx(ctx, tx, userID, changes, at)
		return err
	})
	return changed, err
}

// Overrides reads the writer's own values (nil — the default).
func Overrides(ctx context.Context, db Querier, userID string) (map[quota.Key]*int64, error) {
	values, err := readOverrides(ctx, db, userID, false)
	if err != nil {
		return nil, err
	}
	result := make(map[quota.Key]*int64, len(values))
	for index, key := range quota.Keys() {
		result[key] = values[index]
	}
	return result, nil
}

// SetOverridesInTx applies the changes to one writer in the caller's transaction (UC6). The
// columns are named by quota.Keys only; a row whose values all return to the default is
// removed.
func SetOverridesInTx(ctx context.Context, tx pgx.Tx, userID string, changes []quota.OverrideChange, at time.Time) ([]quota.OverrideChanged, error) {
	if err := quota.CheckOverrides(changes); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO writer_quota_overrides (user_id, updated_at) VALUES ($1::uuid, $2)
		ON CONFLICT (user_id) DO NOTHING
	`, userID, at); err != nil {
		return nil, err
	}
	current, err := readOverrides(ctx, tx, userID, true)
	if err != nil {
		return nil, err
	}
	index := make(map[quota.Key]int, len(quota.Keys()))
	for position, key := range quota.Keys() {
		index[key] = position
	}
	changed := make([]quota.OverrideChanged, 0, len(changes))
	for _, change := range changes {
		position := index[change.Key]
		before := current[position]
		current[position] = change.Value
		changed = append(changed, quota.OverrideChanged{Key: change.Key, Before: before, After: change.Value})
		// The key is one of quota.Keys, so it is a column name and safe to splice.
		if _, err := tx.Exec(ctx, fmt.Sprintf(
			`UPDATE writer_quota_overrides SET %s = $2, updated_at = $3 WHERE user_id = $1::uuid`, change.Key,
		), userID, change.Value, at); err != nil {
			return nil, err
		}
	}
	empty := true
	for _, value := range current {
		empty = empty && value == nil
	}
	if empty {
		if _, err := tx.Exec(ctx, `DELETE FROM writer_quota_overrides WHERE user_id = $1::uuid`, userID); err != nil {
			return nil, err
		}
	}
	return changed, nil
}

func readOverrides(ctx context.Context, db Querier, userID string, lock bool) ([]*int64, error) {
	keys := quota.Keys()
	values := make([]*int64, len(keys))
	targets := make([]any, len(values))
	for index := range values {
		targets[index] = &values[index]
	}
	// The columns are named by quota.Keys only, so they are safe to splice.
	query := `SELECT ` + limitColumns("own", keys) + ` FROM writer_quota_overrides AS own WHERE own.user_id = $1::uuid`
	if lock {
		query += ` FOR UPDATE`
	}
	err := db.QueryRow(ctx, query, userID).Scan(targets...)
	if errors.Is(err, pgx.ErrNoRows) {
		return values, nil
	}
	return values, err
}
