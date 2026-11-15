// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"context"
	"time"

	"canonkeeper/backend/internal/domain/aisettings"
	readingdomain "canonkeeper/backend/internal/domain/readingconsent"
)

// resumeTerm is how often the reading of the helper of the account checks the pauses of no connection and of the reading of
// the site unavailable (specs/2026-11-13-desk-account-ai.md AI3.6); the limits of the tier are checked every sixth time —
// once an hour.
const (
	resumeTerm   = 10 * time.Minute
	limitsEveryN = 6
)

// keepReading lets the reading of the helper of the account go on by itself (AI3.6): at the start every pause that is due —
// an app the site did not take only after the restart of a new version —, then no connection and the reading of the site
// unavailable at most once in ten minutes, and the limits of the tier once an hour, when the site tells room for them. The
// pause of the author never goes on by itself. On the same schedule the questions left pending — by a run before or by this
// one — are settled by the word of the site once it is final (AI2.9).
func (desktop *build) keepReading(ctx context.Context, updated bool) {
	every := desktop.resumeEvery
	if every <= 0 {
		every = resumeTerm
	}
	if desktop.work != nil {
		desktop.work.Recall(ctx)
	}
	desktop.resumeReading(ctx, updated, true)
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for round := 1; ; round++ {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if desktop.work != nil {
				desktop.work.Settle(ctx)
			}
			desktop.resumeReading(ctx, false, round%limitsEveryN == 0)
		}
	}
}

// resumeReading lets go on the consents of the account paused with the reasons due now, by the one rule of the domain
// (readingconsent.DueOfAccount): the check is a fresh read of the account — no answer of the site lets nothing go on, so a
// pause of no connection does not queue a Job that would fail again. The site is asked only while a saga stands at a pause
// that could go on at this check.
func (desktop *build) resumeReading(ctx context.Context, newVersion, limits bool) {
	desktop.mu.Lock()
	resume := desktop.resume
	desktop.mu.Unlock()
	if resume == nil || desktop.settings.AI().Source != aisettings.SourceAccount || !desktop.account.parts.Linked() {
		return
	}
	could := readingdomain.DueOfAccount(readingdomain.Resumption{NewVersion: newVersion, Limits: limits, Reaches: true, Characters: true, Work: true})
	if paused, err := resume.Store.Paused(ctx, readingdomain.SourceAccount, could); err != nil || len(paused) == 0 {
		return
	}
	state := desktop.account.parts.Read.Execute(ctx, true)
	check := readingdomain.Resumption{NewVersion: newVersion, Limits: limits, Reaches: !state.Unreachable && state.Account != nil}
	// The site tells its counters at most once a minute (the link): fresh ones say whether the tier has room again.
	if told := state.Account; told != nil && told.Quota {
		check.Characters = told.Reading.Limit > told.Reading.Used
		check.Work = told.Work.Limit > told.Work.Used
	}
	if _, err := resume.Execute(ctx, readingdomain.DueOfAccount(check)...); err != nil && ctx.Err() == nil {
		desktop.logger.Print(`{"event":"desktop_reading_not_resumed"}`)
	}
}
