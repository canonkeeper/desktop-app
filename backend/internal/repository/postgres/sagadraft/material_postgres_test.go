package sagadraft

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	domain "canonkeeper/backend/internal/domain/sagadraft"
	"canonkeeper/backend/internal/library"
	service "canonkeeper/backend/internal/services/sagadraft"

	"github.com/jackc/pgx/v5"
)

// specs/2026-11-02-writer-workshop.md, W2.4: the usual length of a chapter at the place of a draft is the median of
// the words of the last chapters of the published text before it; with fewer than three there is none.
func TestTypicalWordsStandBeforeThePlacePostgres(t *testing.T) {
	fixture := seedDraftProject(t, "ck_chapter_draft_typical")
	ctx := context.Background()
	const book = "00000000-0000-4000-8000-00000000a101"
	fixture.book(t, book, "Том")
	var chapters []string
	after := ""
	for _, words := range []int{10, 30, 20, 40} {
		if err := fixture.db.WithTx(ctx, func(tx pgx.Tx) error {
			var err error
			after, err = fixture.library.AddChapterInTx(ctx, tx, library.AddChapterInput{
				ProjectID: fixture.project, BookID: book, AfterChapterID: after, Title: "Глава",
				Paragraphs: []string{strings.TrimSpace(strings.Repeat("слово ", words))}, At: time.Now().UTC(),
			})
			return err
		}); err != nil {
			t.Fatal(err)
		}
		chapters = append(chapters, after)
	}
	draft := fixture.create(t, domain.KindSketch, "набросок")
	if typical := fixture.get(t, draft.ID).TypicalWords; typical == nil || *typical != 25 {
		t.Fatalf("without a place: %v", typical)
	}
	for _, test := range []struct {
		after string
		want  int
	}{{chapters[2], 20}, {chapters[1], 0}, {"", 0}} {
		current := fixture.get(t, draft.ID)
		placed, err := fixture.store.UpdateChapterDraft(ctx, fixture.editor, draft.ID, current.Revision, domain.Change{Place: &domain.Place{BookID: book, AfterChapterID: test.after}})
		if err != nil {
			t.Fatal(err)
		}
		if test.want == 0 && placed.TypicalWords != nil || test.want != 0 && (placed.TypicalWords == nil || *placed.TypicalWords != test.want) {
			t.Fatalf("after %q: %v, want %d", test.after, placed.TypicalWords, test.want)
		}
	}
}

// W2.3: what the author did not write stays between its marks in the working text as he keeps it; an edit that breaks
// the marks is refused by the use case before the store.
func TestTheDraftKeepsItsMarksPostgres(t *testing.T) {
	fixture := seedDraftProject(t, "ck_chapter_draft_marks")
	draft := fixture.create(t, domain.KindSketch, "Он пришёл. ⟦Она ждала у ворот.⟧")
	if draft.Text != "Он пришёл. ⟦Она ждала у ворот.⟧" {
		t.Fatalf("kept %q", draft.Text)
	}
	broken := "Он пришёл. ⟦Она ждала"
	if _, err := (service.Update{Store: fixture.store}).Execute(context.Background(), fixture.editor, draft.ID, draft.Revision, domain.Change{Text: &broken}); !errors.Is(err, domain.ErrInvalidMarks) {
		t.Fatalf("broken marks: %v", err)
	}
}
