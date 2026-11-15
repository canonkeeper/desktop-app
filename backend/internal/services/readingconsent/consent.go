// SPDX-License-Identifier: AGPL-3.0-only

package readingconsent

import (
	"context"
	"errors"
	"time"

	domain "canonkeeper/backend/internal/domain/readingconsent"
)

// ConsentStore changes the consent of a saga under its lock, with what the change asks done in the same transaction.
type ConsentStore interface {
	Consents
	ChangeConsent(ctx context.Context, projectID string, at time.Time, change func(current *domain.Consent) (domain.Consent, error), retryUnread bool) (domain.Consent, error)
}

// Work is the work of the reading of a saga: the Jobs of its cards and of its layer, queued by their owners of the queue
// and canceled the standard way (CN3.2).
type Work interface {
	Queue(ctx context.Context, projectID string) error
	Cancel(ctx context.Context, actorID, projectID string) error
}

// ReasonProviderChanged is why a consent of the saga does not act: it was given for another host (CN3.1).
const ReasonProviderChanged = "provider_changed"

// ConsentView is the consent as the window reads it: the consent that acts, or none — with why, when the saga has one
// for another provider.
type ConsentView struct {
	Consent *domain.Consent
	Reason  string
	// Source is who reads the books (specs/2026-11-13-desk-account-ai.md, the table «Страницам B2»): the source of the consent
	// that acts — always the source of the settings, since only such a consent acts —, or of the settings while none does;
	// empty without a helper.
	Source domain.Source
}

// GetConsent is getReadingConsent.
type GetConsent struct {
	Settings Settings
	Owners   Owners
	Consents Consents
}

func (get GetConsent) Execute(ctx context.Context, actorID, projectID string) (ConsentView, error) {
	if err := get.Owners.Owned(ctx, actorID, projectID); err != nil {
		return ConsentView{}, err
	}
	consent, err := get.Consents.Consent(ctx, projectID)
	if err != nil {
		return ConsentView{}, err
	}
	return viewOf(consent, get.Settings), nil
}

// viewOf is the consent that acts counted in the unit of the settings now (domain In: a consent without a sum follows it). A
// consent given to another source or host does not act: the reading waits for a new one (CN3.1;
// specs/2026-11-13-desk-account-ai.md AI3.4).
func viewOf(consent *domain.Consent, settings Settings) ConsentView {
	to, _ := targetOf(settings)
	if consent.Acts(to.source, to.host) {
		shown := consent.In(to.unit)
		return ConsentView{Consent: &shown, Source: to.source}
	}
	if consent != nil && consent.Source != "" {
		return ConsentView{Reason: ReasonProviderChanged, Source: to.source}
	}
	return ConsentView{Source: to.source}
}

// SetConsent is setReadingConsent (CN3.2): only the owner of the saga, with the helper connected. A new count begins only
// when no consent acts; «allowed» queues the Jobs of the cards and of the layer by their owners of the queue, «paused» puts
// the pause of the author in the same transaction and has the live Jobs of the reading canceled the standard way — what was
// built stays. The repeat of the chapters that did not read forgets their marks in the same transaction.
type SetConsent struct {
	Settings Settings
	Owners   Owners
	Store    ConsentStore
	Work     Work
	Now      func() time.Time
}

func (set SetConsent) Execute(ctx context.Context, actorID, projectID string, change domain.Change) (ConsentView, error) {
	if err := set.Owners.Owned(ctx, actorID, projectID); err != nil {
		return ConsentView{}, err
	}
	to, ok := targetOf(set.Settings)
	if !set.Settings.Connected(ctx) || !ok {
		return ConsentView{}, ErrNotConnected
	}
	if to.source == domain.SourceAccount && (change.StopAt != nil || change.ContinueBy != nil) {
		// The consent of the helper of the account has no stop sum: the tier of the site bounds the reading (AI3.4).
		field := "stopAt"
		if change.ContinueBy != nil {
			field = "continueBy"
		}
		return ConsentView{}, &FieldError{Field: field}
	}
	now := set.Now().UTC().Truncate(time.Microsecond)
	kept, err := set.Store.ChangeConsent(ctx, projectID, now, func(current *domain.Consent) (domain.Consent, error) {
		return domain.Set(current, change, now, to.source, to.host, to.unit)
	}, change.RetryUnread && change.State == domain.StateAllowed)
	switch {
	case errors.Is(err, domain.ErrNothingToPause):
		return ConsentView{}, &Conflict{Code: "reading.no_consent"}
	case errors.Is(err, domain.ErrInvalidChange):
		return ConsentView{}, &FieldError{Field: "state"}
	case err != nil:
		return ConsentView{}, err
	}
	if kept.State == domain.StatePaused {
		err = set.Work.Cancel(ctx, actorID, projectID)
	} else {
		err = set.Work.Queue(ctx, projectID)
	}
	if err != nil {
		return ConsentView{}, err
	}
	return ConsentView{Consent: &kept, Source: to.source}, nil
}
