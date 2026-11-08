package sagamemory

import (
	"context"

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
func (build *BuildCards) refreshNames(ctx context.Context, claim jobs.WorkerClaim, selection Selection) {
	stage, err := build.buildNames(ctx, claim, selection)
	if err != nil && ctx.Err() == nil && build.namesFailure != nil {
		build.namesFailure(claim.ProjectID, stage, err)
	}
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
		output, spent, err := build.model.Complete(ctx, domain.NameInstructions, names.list)
		build.recordUsage(ctx, claim.ProjectID, spent)
		if err != nil {
			return "model", err
		}
		if registry, err = domain.ParseNameRegistry(output); err != nil {
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
	version := outlineVersion(selection)
	keys := make([]CardKey, len(version.Chapters))
	for i, chapter := range version.Chapters {
		keys[i] = CardKey{ChapterID: chapter.ID, ContentHash: chapter.ContentHash}
	}
	found, err := build.store.Cards(ctx, projectID, keys, domain.CardParts{Mentions: true, Facts: true, Knows: true})
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
	stored, err := build.store.NamesDigest(ctx, projectID)
	if err != nil {
		return nameList{}, err
	}
	return nameList{keys: keys, list: list, digest: digest, changed: stored != digest}, nil
}

// namesNeedModel reports whether refreshing the registry would call the model: the names changed and there are some.
func (build *BuildCards) namesNeedModel(ctx context.Context, projectID string, selection Selection) (bool, error) {
	names, err := build.nameList(ctx, projectID, selection)
	return names.changed && names.list != "", err
}
