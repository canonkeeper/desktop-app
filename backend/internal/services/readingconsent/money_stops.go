// SPDX-License-Identifier: AGPL-3.0-only

package readingconsent

import (
	"context"

	domain "canonkeeper/backend/internal/domain/readingconsent"
)

// OwnedConsents read the consents of the live sagas an author owns.
type OwnedConsents interface {
	ConsentsOf(ctx context.Context, ownerID string) ([]domain.Consent, error)
}

// MoneyStops counts the sagas of the author read under a consent that acts with a stop sum in money of the currency the
// settings price in now (specs/2026-11-13-desk-consent-web.md RW3.1): getAiSettings says it, and the window asks before a
// change of the currency or the removal of the prices, which would pause their reading at «цены изменились» (CN3.4). The rule
// is the domain's — «согласие действует» (Acts) and a sum in money (StopsInMoney) —; the page goes through no consent.
type MoneyStops struct {
	Settings Settings
	Consents OwnedConsents
	// Author is the local author of the app.
	Author string
}

func (stops MoneyStops) Count(ctx context.Context) (int, error) {
	own := stops.Settings.AI().Own
	if own == nil {
		return 0, nil
	}
	consents, err := stops.Consents.ConsentsOf(ctx, stops.Author)
	if err != nil {
		return 0, err
	}
	host, unit := own.Host(), domain.UnitOf(own.Advanced)
	count := 0
	for _, consent := range consents {
		if consent.Acts(domain.SourceOwn, host) && consent.StopsInMoney(unit) {
			count++
		}
	}
	return count, nil
}
