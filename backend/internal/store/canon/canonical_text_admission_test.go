package canonstore

import (
	"context"
	"errors"
	"testing"

	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/search"

	"github.com/jackc/pgx/v5"
)

func TestLoadCurrentCanonicalProjectTextInTxRejectsPartialCoverage(t *testing.T) {
	fixture := newCanonicalIndexStoreFixture(t, 9820, governance.SourceViewDraft)
	addCanonicalSearchBookForTest(t, fixture, 20)
	indexCanonicalSearchFixtureForTest(t, fixture)
	mustExecCanonicalSearchTest(
		t,
		fixture,
		`UPDATE chapter_text_index_state SET source_revision = source_revision + 1 WHERE project_id = $1 AND source_view = $2 AND chapter_id = $3`,
		fixture.projectID,
		fixture.view,
		fixture.chapterID,
	)

	err := fixture.db.WithTx(context.Background(), func(tx pgx.Tx) error {
		_, err := fixture.db.LoadCurrentCanonicalProjectTextInTx(context.Background(), tx, CanonicalTextSelectionInput{
			ProjectID: fixture.projectID, SourceView: fixture.view,
		})
		return err
	})
	if !errors.Is(err, search.ErrBookTextNotCurrent) {
		t.Fatalf("error = %v, want book text not current", err)
	}
	if action := canonicalSearchActionForTest(t, err); action != search.ActionStartIndexing {
		t.Fatalf("action = %q, want %q", action, search.ActionStartIndexing)
	}
}

func TestLoadCurrentCanonicalProjectTextInTxRejectsZeroBooksWithExistingAction(t *testing.T) {
	fixture := newCanonicalIndexStoreFixture(t, 9850, governance.SourceViewDraft)
	if _, err := fixture.db.Pool.Exec(context.Background(), `
		DELETE FROM book_source_members
		WHERE project_id = $1 AND source_view = $2
	`, fixture.projectID, fixture.view); err != nil {
		t.Fatal(err)
	}

	err := fixture.db.WithTx(context.Background(), func(tx pgx.Tx) error {
		_, err := fixture.db.LoadCurrentCanonicalProjectTextInTx(context.Background(), tx, CanonicalTextSelectionInput{
			ProjectID: fixture.projectID, SourceView: fixture.view,
		})
		return err
	})
	if !errors.Is(err, search.ErrSourceViewUnavailable) {
		t.Fatalf("error = %v, want source view unavailable", err)
	}
	if action := canonicalSearchActionForTest(t, err); action != search.ActionCreateOrImportSource {
		t.Fatalf("action = %q, want %q", action, search.ActionCreateOrImportSource)
	}
}

func TestLoadCurrentCanonicalTextBaselineInTxReturnsExactCurrentText(t *testing.T) {
	fixture := newCanonicalIndexStoreFixture(t, 9870, governance.SourceViewDraft)
	indexCanonicalSearchFixtureForTest(t, fixture)

	var baseline CanonicalTextBaseline
	err := fixture.db.WithTx(context.Background(), func(tx pgx.Tx) error {
		var loadErr error
		baseline, loadErr = fixture.db.LoadCurrentCanonicalTextBaselineInTx(context.Background(), tx, CanonicalTextSelectionInput{
			ProjectID: fixture.projectID, SourceView: fixture.view, BookID: fixture.bookID,
		})
		return loadErr
	})
	if err != nil {
		t.Fatal(err)
	}
	if baseline.Content != "dragon" || baseline.Locator.BookID != fixture.bookID ||
		baseline.Locator.ChapterID != fixture.chapterID || len(baseline.SourceRefs) != 1 {
		t.Fatalf("baseline = %+v", baseline)
	}
	fields, err := baseline.SourceRefs[0].Fields()
	view, viewOK := fields.SourceView.View()
	if err != nil || !viewOK || fields.BookID != fixture.bookID || fields.ChapterID != fixture.chapterID ||
		view != fixture.view || fields.ChatID != "" || fields.TextIdentityState != governance.SourceIdentityTextDerivedCurrent {
		t.Fatalf("baseline ref = %+v, err = %v", fields, err)
	}
}
