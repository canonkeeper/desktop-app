// SPDX-License-Identifier: AGPL-3.0-only

package sagamemory

import (
	"context"
	"errors"
	"testing"
	"time"

	domain "canonkeeper/backend/internal/domain/sagamemory"
	service "canonkeeper/backend/internal/services/sagamemory"
	"canonkeeper/backend/internal/testutil"
)

// specs/2026-11-07-desktop-app.md, DA8.1, DA8.4: the cards Job may call the model only for a saga whose consent is
// kept and allowed; no consent and a paused one wait, and a new consent replaces the one the saga held. The project
// purge removes it (TestProjectHardPurgeRemovesTheDesktopRowsOfTheSaga).
func TestTheModelBuildsCardsOnlyWithTheConsentOfTheAuthor(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_card_build_permission")
	seedMemoryProject(t, db)
	repository := New(db)
	ctx := context.Background()
	allowed := func() bool {
		t.Helper()
		value, err := repository.CardModelAllowed(ctx, memoryProjectID)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	set := func(permission domain.CardBuildPermission) {
		t.Helper()
		if err := repository.SetCardBuildPermission(ctx, memoryProjectID, permission, time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
	}
	since := time.Now().UTC().Truncate(time.Microsecond)
	limit := int64(20_000_000)

	if allowed() {
		t.Fatal("a saga without a consent builds cards with the model")
	}
	set(domain.CardBuildPermission{State: domain.CardBuildPaused, Since: since})
	if allowed() {
		t.Fatal("a paused saga builds cards with the model")
	}
	set(domain.CardBuildPermission{State: domain.CardBuildAllowed, BudgetUSDMicros: &limit, Since: since})
	if !allowed() {
		t.Fatal("an allowed saga does not build cards with the model")
	}
	set(domain.CardBuildPermission{State: domain.CardBuildPaused, BudgetUSDMicros: &limit, Since: since})
	if allowed() {
		t.Fatal("the consent paused at its limit still builds cards with the model")
	}

	if err := repository.SetCardBuildPermission(ctx, memoryProjectID, domain.CardBuildPermission{
		State: domain.CardBuildAllowed, BudgetUSDMicros: &limit, BudgetTokens: &limit, Since: since,
	}, time.Now().UTC()); !errors.Is(err, domain.ErrInvalidCardBuildPermission) {
		t.Fatalf("two limits = %v, want ErrInvalidCardBuildPermission", err)
	}
	if allowed() {
		t.Fatal("an invalid consent replaced the paused one")
	}
	if err := repository.SetCardBuildPermission(ctx, "93000000-0000-4000-8000-0000000000ff", domain.CardBuildPermission{
		State: domain.CardBuildAllowed, Since: since,
	}, time.Now().UTC()); !errors.Is(err, service.ErrInvalid) {
		t.Fatalf("the consent of a saga that is not there = %v, want ErrInvalid", err)
	}
	if _, err := repository.CardModelAllowed(ctx, "not-a-saga"); !errors.Is(err, service.ErrInvalid) {
		t.Fatalf("the consent of a bad reference = %v, want ErrInvalid", err)
	}
}
