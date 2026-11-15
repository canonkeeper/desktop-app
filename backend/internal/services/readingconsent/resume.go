// SPDX-License-Identifier: AGPL-3.0-only

package readingconsent

import (
	"context"
	"errors"
	"slices"
	"time"

	domain "canonkeeper/backend/internal/domain/readingconsent"
)

// PausedStore finds the sagas whose consent of a source stands paused with one of the reasons, and changes a consent under
// its lock.
type PausedStore interface {
	ConsentStore
	Paused(ctx context.Context, source domain.Source, pauses []domain.Pause) ([]string, error)
}

// Resume is the reading of the helper of the account going on by itself (specs/2026-11-13-desk-account-ai.md AI3.6): the
// consents of the account that stand at one of the pauses named go on — the same consent and count, the Jobs of the reading
// queued — while the helper is connected; the pause of the author and «Свой ИИ» never go on by themselves. Its caller says
// which pauses are due: no connection and the reading of the site unavailable at most once in ten minutes and at the
// start, the limits of the tier once the site told room, an app the site did not take after the restart of a new version.
type Resume struct {
	Settings Settings
	Store    PausedStore
	Work     Work
	Now      func() time.Time
}

// errNotDue is a consent that no longer stands at a pause the resume takes: it is left as it is.
var errNotDue = errors.New("reading consent: the pause is not due")

// Execute lets the reading of the sagas paused with the reasons go on; it returns how many.
func (resume Resume) Execute(ctx context.Context, pauses ...domain.Pause) (int, error) {
	to, ok := targetOf(resume.Settings)
	if len(pauses) == 0 || !ok || to.source != domain.SourceAccount || !resume.Settings.Connected(ctx) {
		return 0, nil
	}
	projects, err := resume.Store.Paused(ctx, domain.SourceAccount, pauses)
	if err != nil {
		return 0, err
	}
	resumed := 0
	for _, projectID := range projects {
		now := resume.Now().UTC().Truncate(time.Microsecond)
		_, err := resume.Store.ChangeConsent(ctx, projectID, now, func(current *domain.Consent) (domain.Consent, error) {
			if !current.Acts(to.source, to.host) || current.State != domain.StatePaused || !slices.Contains(pauses, current.Pause) {
				return domain.Consent{}, errNotDue
			}
			return domain.Set(current, domain.Change{State: domain.StateAllowed}, now, to.source, to.host, to.unit)
		}, false)
		switch {
		case errors.Is(err, errNotDue), errors.Is(err, ErrNotFound):
			continue
		case err != nil:
			return resumed, err
		}
		if err := resume.Work.Queue(ctx, projectID); err != nil {
			return resumed, err
		}
		resumed++
	}
	return resumed, nil
}
