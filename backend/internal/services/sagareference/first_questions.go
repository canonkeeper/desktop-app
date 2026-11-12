package sagareference

import (
	"context"

	"canonkeeper/backend/internal/domain/sagamemory"
)

// FirstQuestionsResult is the answer of getSagaFirstQuestions: the questions and whether the map is still to come (FQ1.9) —
// the page asks again then.
type FirstQuestionsResult struct {
	Questions []sagamemory.FirstQuestion
	Pending   bool
}

// GetFirstQuestions is getSagaFirstQuestions (specs/2026-11-10-first-questions.md FQ1.4, FQ1.9): the questions an empty chat
// offers, with the names of the author's book, from the prepared row of the map after the decisions of the author. A saga
// without a chapter has none. One whose text has chapters and no prepared row yet has none and is pending: its cards are
// being written, or written and the map not yet built — an author opens the chat right after the upload, and the letter
// «Сага готова» goes before the map. A map behind its cards answers by what is ready. It builds nothing, queues nothing
// and calls no model: asking for a build is the read of the map.
type GetFirstQuestions struct {
	access Access
	cards  MapCards
	maps   PreparedMaps
}

func NewGetFirstQuestions(access Access, cards MapCards, maps PreparedMaps) *GetFirstQuestions {
	if access == nil || cards == nil || maps == nil {
		panic("saga reference: first questions dependencies are required")
	}
	return &GetFirstQuestions{access: access, cards: cards, maps: maps}
}

func (useCase *GetFirstQuestions) Execute(ctx context.Context, input Scope) (FirstQuestionsResult, error) {
	scope, err := readable(ctx, useCase.access, input)
	if err != nil {
		return FirstQuestionsResult{}, err
	}
	now, err := useCase.cards.CurrentMap(ctx, scope.ProjectID, scope.View)
	if err != nil {
		return FirstQuestionsResult{}, err
	}
	none := FirstQuestionsResult{Questions: []sagamemory.FirstQuestion{}}
	if now.Coverage.Chapters == 0 {
		return none, nil
	}
	if now.Coverage.WithCards == 0 {
		none.Pending = true
		return none, nil
	}
	_, stored, found, err := useCase.maps.PreparedMap(ctx, scope.ProjectID, scope.View, nil)
	if err != nil {
		return FirstQuestionsResult{}, err
	}
	if !found {
		none.Pending = true
		return none, nil
	}
	items, err := useCase.maps.MapHiddenItems(ctx, scope.ProjectID)
	if err != nil {
		return FirstQuestionsResult{}, err
	}
	var document sagamemory.MapDocument
	if err := sagamemory.DecodeStored(stored, &document); err != nil {
		return FirstQuestionsResult{}, err
	}
	document.Hide(sagamemory.NewMapHidden(items))
	return FirstQuestionsResult{Questions: sagamemory.FirstQuestions(&document)}, nil
}
