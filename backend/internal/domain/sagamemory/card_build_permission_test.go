// SPDX-License-Identifier: AGPL-3.0-only

package sagamemory

import (
	"errors"
	"testing"
	"time"
)

// specs/2026-11-07-desktop-app.md, DA8.4: a consent is allowed or paused, with no limit or one limit — in money or in
// tokens, above zero — and the start of its build.
func TestACardBuildPermissionHoldsOneStateAndAtMostOneLimit(t *testing.T) {
	since := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	limit := func(value int64) *int64 { return &value }
	for _, permission := range []CardBuildPermission{
		{State: CardBuildAllowed, Since: since},
		{State: CardBuildPaused, Since: since},
		{State: CardBuildAllowed, BudgetUSDMicros: limit(20_000_000), Since: since},
		{State: CardBuildPaused, BudgetTokens: limit(5_000_000), Since: since},
	} {
		if err := permission.Validate(); err != nil {
			t.Fatalf("%+v: %v", permission, err)
		}
	}
	for _, permission := range []CardBuildPermission{
		{State: "", Since: since},
		{State: "denied", Since: since},
		{State: CardBuildAllowed},
		{State: CardBuildAllowed, BudgetUSDMicros: limit(1), BudgetTokens: limit(1), Since: since},
		{State: CardBuildAllowed, BudgetUSDMicros: limit(0), Since: since},
		{State: CardBuildAllowed, BudgetTokens: limit(-1), Since: since},
	} {
		if err := permission.Validate(); !errors.Is(err, ErrInvalidCardBuildPermission) {
			t.Fatalf("%+v: %v, want ErrInvalidCardBuildPermission", permission, err)
		}
	}
	if !CardBuildAllowed.Valid() || !CardBuildPaused.Valid() || CardBuildState("allow").Valid() {
		t.Fatal("the valid states are allowed and paused")
	}
}
