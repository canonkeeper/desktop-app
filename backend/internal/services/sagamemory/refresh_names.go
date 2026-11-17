package sagamemory

import (
	"context"
	"errors"
	"strings"

	"canonkeeper/backend/internal/domain/disclosure"
	"canonkeeper/backend/internal/domain/modelthread"
	domain "canonkeeper/backend/internal/domain/sagamemory"
	"canonkeeper/backend/internal/jobs"
)

// StoredNames is the name registry of a project with what it was built from: a save finds out
// whether the chapter texts are still current, so a deleted source leaves no names behind
// (specs/2026-11-01-index-foundation.md, R5.3).
type StoredNames struct {
	Keys     []CardKey
	Digest   string
	Registry domain.NameRegistry
	Model    string
}

// NamesFailureLog hears why a registry was not refreshed: the stage (read, model, parse, store) and
// the error, for the log only; the Job goes on (R5.1).
type NamesFailureLog func(projectID, stage string, err error)

// refreshNames is the last step of a cards Job with every chapter carded (UC5): the list of the names
// of the version, one model call unless the names are those of the stored registry, and the save
// under the claim. Whatever fails is logged and leaves the cards and the Job alone: the glossary keeps
// its rules or the older registry, and the next Job tries again. A stopped Job reports nothing.
// A stop of the reading on the register (specs/2026-11-13-desk-consent.md CN3.4) is returned: the Job ends with it.
// A request of the register the journal of the app did not send (specs/2026-11-14-desk-journal.md JR1.6) is returned as
// journaled: the Job is not done, and the worker repeats it.
func (build *BuildCards) refreshNames(ctx context.Context, claim jobs.WorkerClaim, selection Selection) (stop domain.Stop, stopped bool, journaled error) {
	stage, err := build.buildNames(ctx, claim, selection)
	if stop, stopped := domain.StopOf(err); stopped {
		return stop, true, nil
	}
	if errors.Is(err, disclosure.ErrJournal) {
		return "", false, err
	}
	if err != nil && ctx.Err() == nil && build.namesFailure != nil {
		build.namesFailure(claim.ProjectID, stage, err)
	}
	return "", false, nil
}

func (build *BuildCards) buildNames(ctx context.Context, claim jobs.WorkerClaim, selection Selection) (stage string, err error) {
	names, err := build.nameList(ctx, claim.ProjectID, selection)
	if err != nil {
		return "read", err
	}
	if !names.changed {
		return "", nil
	}
	var registry domain.NameRegistry
	if names.list != "" {
		// The names of a set that did not come together for this helper are not asked again without the author
		// (specs/2026-11-15-desk-reading-rest.md RR1.2): the register stays as it is.
		unread, err := build.namesUnread(ctx, claim.ProjectID, names)
		if err != nil {
			return "read", err
		}
		if unread != "" {
			return "", nil
		}
		// The list of the names of the saga goes, named by how many names it has (JR1.3).
		named := disclosure.Named{Names: strings.Count(strings.TrimSpace(names.list), "\n") + 1}
		// The register goes under the thread of the names of the Job (specs/2026-11-17-model-cache.md MC1.3).
		output, spent, err := build.model.Complete(modelthread.With(disclosure.With(ctx, named), "names", jobs.JobIDOf(ctx)), domain.NameInstructions, names.list)
		build.recordUsage(ctx, claim.ProjectID, spent)
		if errors.Is(err, disclosure.ErrJournal) {
			return "model", err
		}
		if err != nil {
			build.markNames(ctx, claim.ProjectID, selection.View, names.digest, domain.UnreadOf(err))
			return "model", err
		}
		if registry, err = domain.ParseNameRegistry(output); err != nil {
			build.markNames(ctx, claim.ProjectID, selection.View, names.digest, domain.UnreadUnreadable)
			return "parse", err
		}
	}
	stored := StoredNames{Keys: names.keys, Digest: names.digest, Registry: registry, Model: build.modelName}
	if err := build.store.SaveNames(ctx, claim, stored); err != nil {
		return "store", err
	}
	return "", nil
}

// nameList is the list of the names of the version's cards with its digest, and whether it differs from the stored
// registry's.
type nameList struct {
	keys    []CardKey
	list    string
	digest  string
	changed bool
}

func (build *BuildCards) nameList(ctx context.Context, projectID string, selection Selection) (nameList, error) {
	return nameListOf(ctx, build.store, projectID, selection)
}

// namesSource is what the list of the names of a version reads: the cards and the digest of the stored register.
type namesSource interface {
	Cards(ctx context.Context, projectID string, keys []CardKey, parts domain.CardParts) (map[CardKey]domain.Card, error)
	NamesDigest(ctx context.Context, projectID string) (string, error)
}

func nameListOf(ctx context.Context, store namesSource, projectID string, selection Selection) (nameList, error) {
	version := outlineVersion(selection)
	keys := make([]CardKey, len(version.Chapters))
	for i, chapter := range version.Chapters {
		keys[i] = CardKey{ChapterID: chapter.ID, ContentHash: chapter.ContentHash}
	}
	found, err := store.Cards(ctx, projectID, keys, domain.CardParts{Mentions: true, Facts: true, Knows: true})
	if err != nil {
		return nameList{}, err
	}
	cards := make(map[int]*domain.Card, len(found))
	for i, key := range keys {
		if card, ok := found[key]; ok {
			cards[i] = &card
		}
	}
	list, digest := domain.BuildNameList(cards)
	stored, err := store.NamesDigest(ctx, projectID)
	if err != nil {
		return nameList{}, err
	}
	return nameList{keys: keys, list: list, digest: digest, changed: stored != digest}, nil
}

// namesNeedModel reports whether refreshing the registry would call the model: the names changed, there are some, and they
// are not marked (specs/2026-11-15-desk-reading-rest.md RR1.2) — marked names need no consent.
func (build *BuildCards) namesNeedModel(ctx context.Context, projectID string, selection Selection) (bool, error) {
	wait, _, err := NamesOf(ctx, build.store, build.policy, projectID, selection)
	return wait, err
}

// namesUnread is why the names of the list are marked, by the policy of the reading; a build without one — the site — marks
// none.
func (build *BuildCards) namesUnread(ctx context.Context, projectID string, names nameList) (string, error) {
	if build.policy == nil {
		return "", nil
	}
	return build.policy.NamesUnread(ctx, projectID, names.digest)
}

// markNames marks the names of a set whose register did not come together for a named reason (RR1.1, RR1.6): only by the
// policy of the reading of the app and only for a Job that was not canceled; a mark that was not written is logged and leaves
// the Job as it is.
func (build *BuildCards) markNames(ctx context.Context, projectID, view, digest, reason string) {
	if build.policy == nil || reason == "" || ctx.Err() != nil {
		return
	}
	if err := build.policy.MarkNamesUnread(ctx, projectID, view, digest, reason); err != nil && build.namesFailure != nil {
		build.namesFailure(projectID, "mark", err)
	}
}

// NamesOf says what the register of names of a text version waits for (specs/2026-11-13-desk-consent.md CN1.6;
// specs/2026-11-15-desk-reading-rest.md RR1.2, RR2.1): the model — the names of its cards are not those of the stored register,
// there are some and they are not marked —, or nothing because they are marked, with why. Without marks — the site — no
// names are marked.
func NamesOf(ctx context.Context, store namesSource, marks NamesMarks, projectID string, selection Selection) (wait bool, unread string, err error) {
	names, err := nameListOf(ctx, store, projectID, selection)
	if err != nil || !names.changed || names.list == "" {
		return false, "", err
	}
	if marks != nil {
		if unread, err = marks.NamesUnread(ctx, projectID, names.digest); err != nil || unread != "" {
			return false, unread, err
		}
	}
	return true, "", nil
}
