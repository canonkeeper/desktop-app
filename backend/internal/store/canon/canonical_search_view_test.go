package canonstore

import (
	"context"
	"errors"
	"testing"

	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/search"
)

func TestCanonicalLexicalDraftRequiresMutationRole(t *testing.T) {
	fixture := newCanonicalIndexStoreFixture(t, 9650, governance.SourceViewDraft)
	indexCanonicalSearchFixtureForTest(t, fixture)
	published := fixture
	published.view = governance.SourceViewPublished
	published.claim = addCanonicalIndexClaimForTest(t, published, 31)
	indexCanonicalSearchFixtureForTest(t, published)

	mustExecCanonicalSearchTest(t, fixture, `UPDATE project_memberships SET role='viewer' WHERE project_id=$1 AND user_id=$2`, fixture.projectID, fixture.actorID)
	if _, err := fixture.db.SearchLexical(context.Background(), search.LexicalSearchInput{
		ActorUserID: fixture.actorID, ProjectID: fixture.projectID, SourceView: governance.SourceViewDraft,
		Query: "dragon", Limit: 11,
	}); !errors.Is(err, search.ErrNotFound) {
		t.Fatalf("viewer draft error = %v, want leak-safe not found", err)
	}
	if _, err := fixture.db.SearchLexical(context.Background(), search.LexicalSearchInput{
		ActorUserID: fixture.actorID, ProjectID: fixture.projectID, SourceView: governance.SourceViewPublished,
		Query: "dragon", Limit: 11,
	}); err != nil {
		t.Fatalf("viewer published search: %v", err)
	}

	mustExecCanonicalSearchTest(t, fixture, `UPDATE project_memberships SET role='editor' WHERE project_id=$1 AND user_id=$2`, fixture.projectID, fixture.actorID)
	if _, err := fixture.db.SearchLexical(context.Background(), search.LexicalSearchInput{
		ActorUserID: fixture.actorID, ProjectID: fixture.projectID, SourceView: governance.SourceViewDraft,
		Query: "dragon", Limit: 11,
	}); err != nil {
		t.Fatalf("editor draft search: %v", err)
	}
}
