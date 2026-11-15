package researchchat

import (
	"context"

	"canonkeeper/backend/internal/domain/disclosure"
	researchdomain "canonkeeper/backend/internal/domain/researchchat"
	"canonkeeper/backend/internal/domain/sagamemory"
)

// Naming hears what a turn gave Codex of the saga before Codex sends it (specs/2026-11-14-desk-journal.md JR1.3, JR1.4): the
// tools name what they showed before the handler answers the app-server, the turn its question before turn/start, a
// correction before turn/steer. The record of the next sending of the thread takes it, in whatever turn. The desktop app
// keeps it by the thread; the site has none, and its answers do not change by a sign.
type Naming interface {
	Name(ctx context.Context, threadID, projectID string, named disclosure.Named) (string, error)
	// Withdraw takes back a naming Codex did not take: a correction it refused.
	Withdraw(ctx context.Context, id string) error
}

// WithNaming makes the tools name what they show.
func (tools *ReadTools) WithNaming(naming Naming) *ReadTools {
	tools.naming = naming
	return tools
}

// name names what a tool showed in the thread of its turn; nothing shown, or a build without the journal, names nothing.
func name(ctx context.Context, naming Naming, authority researchdomain.ReadAuthority, named disclosure.Named) error {
	if naming == nil || named.Empty() {
		return nil
	}
	_, err := naming.Name(ctx, authority.RootThreadID, authority.ProjectID, disclosure.Merge(disclosure.Named{}, named))
	return err
}

// show names a chapter of the answer with what of it went (JR1.3): paragraphs from..to, excerpts, its title or what the
// helper learned of it. It does not retain the chapter: use does.
func (saga *sagaScope) show(chapter *sagamemory.Chapter, precision disclosure.Precision, from, to int) {
	if !saga.names {
		return
	}
	shown := disclosure.Chapter{ID: chapter.ID, Precision: precision}
	if precision == disclosure.Paragraphs {
		shown.Paragraphs = []disclosure.Range{{From: from, To: to}}
	}
	saga.shown.Chapters = append(saga.shown.Chapters, shown)
}

// showMaterial names a record of the author's materials the answer shows.
func (saga *sagaScope) showMaterial(id string) {
	if !saga.names {
		return
	}
	saga.shown.Materials = append(saga.shown.Materials, id)
}
