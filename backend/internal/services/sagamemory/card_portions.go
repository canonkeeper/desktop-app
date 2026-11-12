package sagamemory

import (
	"context"
	"time"

	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/jobs"
)

// handOut gives the chapters to the workers one by one, in portions of time (R12.1). When a portion ends and cards of
// another project wait for a worker, it stops and reports true: the chapters already started are finished and the Job
// gives up its turn. While nobody waits, the end of a portion stops nothing — the places of the model do not stand idle
// while the last chapters of a portion are written (specs/2026-11-09-first-session.md, FS6.1) — and only the map is
// prepared from the cards there are, so it grows with a long build. A store that cannot tell who waits is treated as a
// waiting project. It reports false when every chapter was handed out or the Job was stopped.
func (build *BuildCards) handOut(ctx, runCtx context.Context, claim jobs.WorkerClaim, view string, work chan<- int, chapters int) bool {
	portion := time.NewTimer(build.portion)
	defer portion.Stop()
	for index := 0; index < chapters; {
		select {
		case work <- index:
			index++
		case <-runCtx.Done():
			return false
		case <-portion.C:
			if waits, err := build.store.OthersWait(ctx, claim); err != nil || waits {
				return true
			}
			build.growMaps(ctx, claim, view)
			portion.Reset(build.portion)
		}
	}
	return false
}

// growMaps prepares the maps while a long build goes on: of the Job's own text and, for the draft text, of the published
// one, whose cards Job waits for it (specs/2026-11-11-cards-once.md CO2.2) — the map the site shows grows with the cards
// either way (specs/2026-11-04-saga-map.md R10.1). At its end the Job of the published text prepares its own map.
func (build *BuildCards) growMaps(ctx context.Context, claim jobs.WorkerClaim, view string) {
	build.refreshMap(ctx, claim, view)
	if view == string(governance.SourceViewDraft) {
		build.refreshMap(ctx, claim, string(governance.SourceViewPublished))
	}
}
