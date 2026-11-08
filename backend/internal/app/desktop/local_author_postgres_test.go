// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"context"
	"errors"
	"testing"
	"time"

	"canonkeeper/backend/internal/auth"
	"canonkeeper/backend/internal/domain/quota"
	service "canonkeeper/backend/internal/services/desktop"
	authstore "canonkeeper/backend/internal/store/auth"
	quotastore "canonkeeper/backend/internal/store/quota"
	"canonkeeper/backend/internal/testutil"
)

// specs/2026-11-07-desktop-app.md DA2.2, DA3.3: the first start creates exactly one author with a verified address and
// the platform's limits at Key.Max() by the standard defaults; the second start creates and changes nothing and only
// revokes the session of the earlier run.
func TestTheFirstStartCreatesOneVerifiedAuthorWithTheLargestLimitsPostgres(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_desktop_author")
	ctx := context.Background()
	users := func() int {
		t.Helper()
		var count int
		if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&count); err != nil {
			t.Fatal(err)
		}
		return count
	}

	authorID, err := prepareLocalAuthor(ctx, db)
	if err != nil {
		t.Fatalf("first start = %v", err)
	}
	if found, ok, err := authstore.New(db).UserIDByEmail(ctx, service.LocalAuthorEmail); err != nil || !ok || found != authorID || users() != 1 {
		t.Fatalf("local author = %q %v %v, users %d; want the one author %q", found, ok, err, users(), authorID)
	}
	defaults, err := quotastore.New(db).PlatformDefaults(ctx)
	if err != nil || defaults.UpdatedAt == nil || defaults.UpdatedBy != authorID {
		t.Fatalf("platform defaults = %+v, %v", defaults, err)
	}
	for _, key := range quota.DefaultKeys() {
		if defaults.Values[key] != key.Max() {
			t.Errorf("platform default %s = %d, want %d", key, defaults.Values[key], key.Max())
		}
	}
	limits, err := quotastore.EffectiveLimitsInTx(ctx, db.Pool, authorID)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range quota.Keys() {
		if limits.Value(key) != key.Max() {
			t.Errorf("limit %s of the author = %d, want %d", key, limits.Value(key), key.Max())
		}
	}

	sessions := auth.NewService(authstore.New(db), auth.NewSecureIDGenerator(), auth.SystemClock{})
	session, _, err := sessions.CreateSession(ctx, auth.NewSession{UserID: authorID, ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	user, err := sessions.CurrentUser(ctx, session.CookieValue)
	if err != nil || !user.EmailVerified || user.DisplayName != service.LocalAuthorName {
		t.Fatalf("the author of the session = %+v, %v", user, err)
	}

	again, err := prepareLocalAuthor(ctx, db)
	if err != nil || again != authorID || users() != 1 {
		t.Fatalf("second start = %q, %v, users %d", again, err, users())
	}
	if after, err := quotastore.New(db).PlatformDefaults(ctx); err != nil || !after.UpdatedAt.Equal(*defaults.UpdatedAt) {
		t.Fatalf("the second start changed the defaults: %+v, %v", after, err)
	}
	if _, err := sessions.CurrentUser(ctx, session.CookieValue); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Fatalf("the session of the earlier run = %v, want revoked", err)
	}
}
