package indexing

import (
	"context"

	indexingdomain "canonkeeper/backend/internal/domain/indexing"
)

// ReadyWaves closes the wave of the preparation of a saga: it reports the wave only when this call closed it.
type ReadyWaves interface {
	CloseWave(ctx context.Context, projectID string) (indexingdomain.ReadyWave, bool, error)
}

// ReadyLetters writes the letter «Сага готова» to the author of a closed wave and tells whether an author would get
// one. Whether his address is confirmed and whether he wants such letters is the port's own business
// (specs/2026-11-09-first-session.md, FS2.1, FS5.6).
type ReadyLetters interface {
	Send(ctx context.Context, wave indexingdomain.ReadyWave) error
	Promised(ctx context.Context, userID string) (bool, error)
}

// AnnounceReady tells that a saga is ready (FS1.3, FS2): the cards Job that left every chapter of its text with a card
// asks it to close the wave, and a wave that closed after a long preparation is worth a letter. The closed wave is
// committed before the letter is queued, so a wave is told of at most once: a letter that failed is logged, not sent
// again. Nothing here fails the Job that asked.
type AnnounceReady struct {
	waves   ReadyWaves
	letters ReadyLetters
	failure func(projectID, stage string, err error)
}

// NewAnnounceReady builds the use case; letters is nil where nobody is written to (the desktop app), failure may be nil.
func NewAnnounceReady(waves ReadyWaves, letters ReadyLetters, failure func(projectID, stage string, err error)) *AnnounceReady {
	if waves == nil {
		panic("indexing service: ready waves port is required")
	}
	if failure == nil {
		failure = func(string, string, error) {}
	}
	return &AnnounceReady{waves: waves, letters: letters, failure: failure}
}

// Execute closes the open wave of the project if its saga is ready, and queues the letter of a long one.
func (use *AnnounceReady) Execute(ctx context.Context, projectID string) {
	wave, closed, err := use.waves.CloseWave(ctx, projectID)
	if err != nil {
		if ctx.Err() == nil {
			use.failure(projectID, "close", err)
		}
		return
	}
	if !closed || use.letters == nil || !wave.Long() {
		return
	}
	if err := use.letters.Send(ctx, wave); err != nil {
		use.failure(projectID, "letter", err)
	}
}
