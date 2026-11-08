// SPDX-License-Identifier: AGPL-3.0-only

package sagamemory

import (
	"errors"
	"time"
)

// ErrInvalidCardBuildPermission is a consent to build cards that a saga cannot hold.
var ErrInvalidCardBuildPermission = errors.New("saga memory: invalid card build permission")

// CardBuildState is whether the cards Job may call the model for a saga in the desktop build
// (specs/2026-11-07-desktop-app.md, DA8.1). A saga without a consent waits as a paused one does.
type CardBuildState string

const (
	CardBuildAllowed CardBuildState = "allowed"
	CardBuildPaused  CardBuildState = "paused"
)

// Valid reports whether a saga may hold the state.
func (state CardBuildState) Valid() bool {
	return state == CardBuildAllowed || state == CardBuildPaused
}

// CardBuildPermission is the consent of the author to build the cards of a saga with the model (DA8.4): its state, the
// spending limit — in money when he set prices, in tokens otherwise, never both, none at all is no limit — and the start
// of the current build, from which the journal counts what was spent.
type CardBuildPermission struct {
	State           CardBuildState
	BudgetUSDMicros *int64
	BudgetTokens    *int64
	Since           time.Time
}

// Validate checks the consent before it is kept.
func (permission CardBuildPermission) Validate() error {
	switch {
	case !permission.State.Valid(), permission.Since.IsZero():
		return ErrInvalidCardBuildPermission
	case permission.BudgetUSDMicros != nil && permission.BudgetTokens != nil:
		return ErrInvalidCardBuildPermission
	case permission.BudgetUSDMicros != nil && *permission.BudgetUSDMicros <= 0:
		return ErrInvalidCardBuildPermission
	case permission.BudgetTokens != nil && *permission.BudgetTokens <= 0:
		return ErrInvalidCardBuildPermission
	default:
		return nil
	}
}
