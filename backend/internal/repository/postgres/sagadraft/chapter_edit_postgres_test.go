package sagadraft

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	domain "canonkeeper/backend/internal/domain/sagadraft"
	"canonkeeper/backend/internal/library"

	"github.com/jackc/pgx/v5"
)

const editBook = "00000000-0000-4000-8000-000000009101"

// editSaga is a book of three chapters with their own text: the second is the one edited.
func editSaga(t *testing.T, fixture draftProject) []string {
	t.Helper()
	fixture.book(t, editBook, "Том")
	ctx := context.Background()
	after, ids := "", []string{}
	for _, chapter := range []struct {
		title      string
		paragraphs []string
	}{
		{"Первая", []string{"Начало."}}, {"Вторая", []string{"Кир пришёл в порт.", "Мила ждала."}}, {"Третья", []string{"Конец."}},
	} {
		if err := fixture.db.WithTx(ctx, func(tx pgx.Tx) error {
			var err error
			after, err = fixture.library.AddChapterInTx(ctx, tx, library.AddChapterInput{
				ProjectID: fixture.project, BookID: editBook, AfterChapterID: after, Title: chapter.title, Paragraphs: chapter.paragraphs, At: time.Now().UTC(),
			})
			return err
		}); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, after)
	}
	return ids
}

func bookChapter(t *testing.T, fixture draftProject, chapterID string) library.EditedChapter {
	t.Helper()
	var chapter library.EditedChapter
	if err := fixture.db.WithTx(context.Background(), func(tx pgx.Tx) error {
		var err error
		chapter, err = fixture.library.EditedChapterInTx(context.Background(), tx, fixture.project, chapterID, 0)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return chapter
}

// textIndexRevision is the newest catalog revision the text index of the project is asked for.
func textIndexRevision(t *testing.T, fixture draftProject) int {
	t.Helper()
	var revision int
	if err := fixture.db.Pool.QueryRow(context.Background(), `
		SELECT COALESCE(max((subject_json->>'catalogRevision')::integer), 0) FROM jobs WHERE project_id = $1 AND kind = 'canon_text_index'
	`, fixture.project).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	return revision
}

// specs/2026-11-05-voice-full-access.md A8.7: «Править главу» opens one draft per chapter with its text, its title and
// its place after the chapter before it; a reader may not, someone outside does not learn the chapter exists.
func TestChapterEditOpensOneDraftPerChapterPostgres(t *testing.T) {
	fixture := seedDraftProject(t, "ck_chapter_edit_open")
	ctx := context.Background()
	chapters := editSaga(t, fixture)

	draft, err := fixture.store.OpenChapterEdit(ctx, fixture.editor, chapters[1])
	if err != nil {
		t.Fatal(err)
	}
	if draft.Title != "Вторая" || draft.Text != "Кир пришёл в порт.\n\nМила ждала." || draft.Idea != "" {
		t.Fatalf("draft = %q %q", draft.Title, draft.Text)
	}
	if draft.Place.BookID != editBook || draft.Place.AfterChapterID != chapters[0] || draft.AddedChapterID != "" {
		t.Fatalf("place = %+v, added %q", draft.Place, draft.AddedChapterID)
	}
	edit := draft.Edits
	if edit == nil || edit.ChapterID != chapters[1] || edit.Gone || edit.Changed() || edit.ChapterTitle != "Вторая" || edit.BookWords != 6 || edit.PreviousRevision != 0 {
		t.Fatalf("edit = %+v", edit)
	}
	again, err := fixture.store.OpenChapterEdit(ctx, fixture.owner, chapters[1])
	if err != nil || again.ID != draft.ID {
		t.Fatalf("a second open = %s, %v", again.ID, err)
	}
	first, err := fixture.store.OpenChapterEdit(ctx, fixture.editor, chapters[0])
	if err != nil || first.ID == draft.ID || first.Place.AfterChapterID != "" {
		t.Fatalf("the first chapter = %+v, %v", first.Place, err)
	}
	if _, err := fixture.store.OpenChapterEdit(ctx, fixture.viewer, chapters[1]); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("a reader: %v", err)
	}
	if _, err := fixture.store.OpenChapterEdit(ctx, fixture.outsider, chapters[1]); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("an outsider: %v", err)
	}
	if _, err := fixture.store.OpenChapterEdit(ctx, fixture.editor, "00000000-0000-4000-8000-000000009199"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("no such chapter: %v", err)
	}
	page, err := fixture.store.ListChapterDrafts(ctx, fixture.editor.Actor, nil, 10, "")
	if err != nil || len(page.Drafts) != 2 || !page.Drafts[0].Edits || !page.Drafts[1].Edits {
		t.Fatalf("list = %+v, %v", page.Drafts, err)
	}
}

// A8.7: the working text without the helper's marks takes the place of the chapter in the book, at the next revision,
// on its place, under its id and the title the author confirmed; the text index is asked for again; the chapters around
// it stay as they were; «Вернуть прежний текст» gives the old text back as one step.
func TestChapterEditReplacesTheChapterInTheBookPostgres(t *testing.T) {
	fixture := seedDraftProject(t, "ck_chapter_edit_replace")
	ctx := context.Background()
	chapters := editSaga(t, fixture)
	before := bookChapter(t, fixture, chapters[1])
	around := []library.EditedChapter{bookChapter(t, fixture, chapters[0]), bookChapter(t, fixture, chapters[2])}

	draft, err := fixture.store.OpenChapterEdit(ctx, fixture.editor, chapters[1])
	if err != nil {
		t.Fatal(err)
	}
	text := "Кир пришёл в порт на рассвете.\n\n⟦Туман стоял над водой.⟧\n\nМила ждала."
	draft, err = fixture.store.UpdateChapterDraft(ctx, fixture.editor, draft.ID, draft.Revision, domain.Change{Text: &text})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.ReplaceChapterFromDraft(ctx, fixture.viewer, draft.ID, draft.Revision, "Вторая"); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("a reader: %v", err)
	}
	if _, err := fixture.store.ReplaceChapterFromDraft(ctx, fixture.editor, draft.ID, draft.Revision-1, "Вторая"); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("an old revision of the draft: %v", err)
	}
	indexed := textIndexRevision(t, fixture)
	replaced, err := fixture.store.ReplaceChapterFromDraft(ctx, fixture.editor, draft.ID, draft.Revision, "  Вторая: рассвет ")
	if err != nil {
		t.Fatal(err)
	}
	after := bookChapter(t, fixture, chapters[1])
	if after.Revision != before.Revision+1 || after.Title != "Вторая: рассвет" || after.BookID != editBook || after.PreviousChapterID != chapters[0] {
		t.Fatalf("the chapter = %+v", after)
	}
	if want := []string{"Кир пришёл в порт на рассвете.", "Туман стоял над водой.", "Мила ждала."}; !reflect.DeepEqual(after.Paragraphs, want) {
		t.Fatalf("paragraphs = %q", after.Paragraphs)
	}
	for index, chapter := range []library.EditedChapter{bookChapter(t, fixture, chapters[0]), bookChapter(t, fixture, chapters[2])} {
		if !reflect.DeepEqual(chapter, around[index]) {
			t.Fatalf("a chapter around it changed: %+v", chapter)
		}
	}
	if textIndexRevision(t, fixture) <= indexed {
		t.Fatal("the text index was not asked for again")
	}
	edit := replaced.Edits
	if edit == nil || edit.Revision != after.Revision || edit.PreviousRevision != before.Revision || edit.Changed() || replaced.Title != "Вторая: рассвет" {
		t.Fatalf("the draft after = %+v %q", edit, replaced.Title)
	}
	if replaced.Text != "Кир пришёл в порт на рассвете.\n\nТуман стоял над водой.\n\nМила ждала." {
		t.Fatalf("the draft keeps %q", replaced.Text)
	}

	reverted, err := fixture.store.RevertChapterFromDraft(ctx, fixture.editor, replaced.ID, replaced.Revision)
	if err != nil {
		t.Fatal(err)
	}
	back := bookChapter(t, fixture, chapters[1])
	if !reflect.DeepEqual(back.Paragraphs, before.Paragraphs) || back.Revision != after.Revision+1 || back.Title != "Вторая: рассвет" {
		t.Fatalf("reverted = %+v", back)
	}
	if reverted.Edits.PreviousRevision != 0 || reverted.Edits.Revision != back.Revision || reverted.Text != "Кир пришёл в порт.\n\nМила ждала." {
		t.Fatalf("the draft after the revert = %+v %q", reverted.Edits, reverted.Text)
	}
	if _, err := fixture.store.RevertChapterFromDraft(ctx, fixture.editor, reverted.ID, reverted.Revision); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("a second revert: %v", err)
	}
}

// A8.7: a chapter changed in the book while it was edited refuses the replacement and «Править главу» takes the new
// text; a deleted one refuses it, the draft says so and goes in as a new chapter.
func TestChapterEditKnowsAChangedOrDeletedChapterPostgres(t *testing.T) {
	fixture := seedDraftProject(t, "ck_chapter_edit_changed")
	ctx := context.Background()
	chapters := editSaga(t, fixture)
	draft, err := fixture.store.OpenChapterEdit(ctx, fixture.editor, chapters[1])
	if err != nil {
		t.Fatal(err)
	}
	mine := "Правка автора, которой нет в книге."
	if draft, err = fixture.store.UpdateChapterDraft(ctx, fixture.editor, draft.ID, draft.Revision, domain.Change{Text: &mine}); err != nil {
		t.Fatal(err)
	}
	if again, err := fixture.store.OpenChapterEdit(ctx, fixture.editor, chapters[1]); err != nil || again.Text != mine || again.Revision != draft.Revision {
		t.Fatalf("a second press = %q at %d, %v", again.Text, again.Revision, err)
	}
	if err := fixture.db.WithTx(ctx, func(tx pgx.Tx) error {
		_, err := fixture.library.ReplaceChapterTextInTx(ctx, tx, library.ReplaceChapterTextInput{
			ProjectID: fixture.project, BookID: editBook, ChapterID: chapters[1], ExpectedRevision: draft.Edits.Revision,
			Title: "Вторая", Paragraphs: []string{"Текст из файла."},
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	stale, err := fixture.store.GetChapterDraft(ctx, fixture.editor.Actor, draft.ID)
	if err != nil || !stale.Edits.Changed() {
		t.Fatalf("the draft does not know the chapter changed: %+v, %v", stale.Edits, err)
	}
	if _, err := fixture.store.ReplaceChapterFromDraft(ctx, fixture.editor, draft.ID, stale.Revision, "Вторая"); !errors.Is(err, domain.ErrChapterChanged) {
		t.Fatalf("a changed chapter: %v", err)
	}
	// A8.7.5: «Править главу» opens the draft as it stands, the text of the book comes only by the question of «Открыть главу заново».
	kept, err := fixture.store.OpenChapterEdit(ctx, fixture.editor, chapters[1])
	if err != nil || kept.ID != draft.ID || kept.Text != mine || !kept.Edits.Changed() || kept.Revision != stale.Revision {
		t.Fatalf("«Править главу» of a changed chapter = %q %+v, %v", kept.Text, kept.Edits, err)
	}
	if _, err := fixture.store.ReopenChapterFromBook(ctx, fixture.editor, draft.ID, stale.Revision+1); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("a stale reopen: %v", err)
	}
	reopened, err := fixture.store.ReopenChapterFromBook(ctx, fixture.editor, draft.ID, stale.Revision)
	if err != nil || reopened.ID != draft.ID || reopened.Text != "Текст из файла." || reopened.Edits.Changed() {
		t.Fatalf("reopened = %q %+v, %v", reopened.Text, reopened.Edits, err)
	}

	if _, err := fixture.library.FenceChapterDeletion(ctx, library.FenceChapterDeletionInput{
		ActorUserID: fixture.owner.UserID, ProjectID: fixture.project, BookID: editBook, ChapterID: chapters[1],
		TombstoneID: "00000000-0000-4000-8000-000000009102", DeletedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	gone, err := fixture.store.GetChapterDraft(ctx, fixture.editor.Actor, draft.ID)
	if err != nil || gone.Edits == nil || !gone.Edits.Gone || gone.Edits.Changed() {
		t.Fatalf("the draft of a deleted chapter = %+v, %v", gone.Edits, err)
	}
	if _, err := fixture.store.ReplaceChapterFromDraft(ctx, fixture.editor, draft.ID, gone.Revision, "Вторая"); !errors.Is(err, domain.ErrChapterGone) {
		t.Fatalf("a deleted chapter: %v", err)
	}
	if _, err := fixture.store.OpenChapterEdit(ctx, fixture.editor, chapters[1]); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("«Править главу» of a deleted chapter: %v", err)
	}
	if _, err := fixture.store.ReopenChapterFromBook(ctx, fixture.editor, draft.ID, gone.Revision); !errors.Is(err, domain.ErrChapterGone) {
		t.Fatalf("«Открыть главу заново» of a deleted chapter: %v", err)
	}
	added, err := fixture.store.AddChapterDraftToBook(ctx, fixture.editor, draft.ID, gone.Revision)
	if err != nil || added.AddedChapterID == "" || added.AddedChapterID == chapters[1] {
		t.Fatalf("added = %q, %v", added.AddedChapterID, err)
	}
}

// A8.7: a draft of a chapter still in the book is not added as a second copy of it.
func TestChapterEditIsNotAddedAsANewChapterPostgres(t *testing.T) {
	fixture := seedDraftProject(t, "ck_chapter_edit_add")
	ctx := context.Background()
	chapters := editSaga(t, fixture)
	draft, err := fixture.store.OpenChapterEdit(ctx, fixture.editor, chapters[1])
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.AddChapterDraftToBook(ctx, fixture.editor, draft.ID, draft.Revision); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("add: %v", err)
	}
}

// bookParagraphs are the paragraphs of a chapter of the book in both text versions as they lie: markdown, plain text and
// count of words, a line each.
func bookParagraphs(t *testing.T, fixture draftProject, chapterID string) []string {
	t.Helper()
	rows, err := fixture.db.Pool.Query(context.Background(), `
		SELECT paragraph.variant || '|' || paragraph.markdown || '|' || paragraph.plain_text || '|' || paragraph.word_count
		FROM chapter_paragraphs AS paragraph
		JOIN chapters AS chapter ON chapter.id = paragraph.chapter_id AND chapter.revision = paragraph.revision
		WHERE paragraph.chapter_id = $1::uuid
		ORDER BY paragraph.variant, paragraph."order"
	`, chapterID)
	if err != nil {
		t.Fatal(err)
	}
	lines, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	return lines
}

// specs/2026-11-08-italic-bold-in-text.md TF3.2: «Добавить в книгу» writes the italic and bold of the working text into
// markdown in the Markdown subset and its plain projection into plain_text and the count of words, a face over a line
// break cut at it; «Править главу», the replacement and «Вернуть» carry the faces between the book and the working text.
func TestTheFacesGoIntoTheBookAndBackPostgres(t *testing.T) {
	fixture := seedDraftProject(t, "ck_chapter_faces_book")
	ctx := context.Background()
	const book = "00000000-0000-4000-8000-000000009201"
	fixture.book(t, book, "Том")
	first := fixture.chapterIn(t, book, "", "Первая")
	draft := fixture.create(t, domain.KindSketch, "")
	placed, err := fixture.store.UpdateChapterDraft(ctx, fixture.editor, draft.ID, draft.Revision, domain.Change{Place: &domain.Place{BookID: book, AfterChapterID: first}})
	if err != nil {
		t.Fatal(err)
	}
	body := "Кир ⟨пришёл⟩ в порт.\n\n⟦⟪Туман⟫ стоял⟧ * * *\n⟨над\nводой⟩."
	written, err := fixture.store.UpdateChapterDraft(ctx, fixture.editor, draft.ID, placed.Revision, domain.Change{Text: &body})
	if err != nil {
		t.Fatal(err)
	}
	added, err := fixture.store.AddChapterDraftToBook(ctx, fixture.editor, draft.ID, written.Revision)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"draft|Кир *пришёл* в порт.|Кир пришёл в порт.|4", `draft|**Туман** стоял \* \* \*|Туман стоял * * *|5`, "draft|*над*|над|1", "draft|*водой*.|водой.|1",
		"published|Кир *пришёл* в порт.|Кир пришёл в порт.|4", `published|**Туман** стоял \* \* \*|Туман стоял * * *|5`, "published|*над*|над|1", "published|*водой*.|водой.|1",
	}
	if got := bookParagraphs(t, fixture, added.AddedChapterID); !reflect.DeepEqual(got, want) {
		t.Fatalf("the paragraphs of the book:\n%q\nwant\n%q", got, want)
	}

	edit, err := fixture.store.OpenChapterEdit(ctx, fixture.editor, added.AddedChapterID)
	if err != nil {
		t.Fatal(err)
	}
	// specs/2026-11-11-ux5-writing-path.md WP4.3: the chapter a draft added is edited in that draft, its own text kept — the
	// book holds the same words; the text of the book comes back by «Вернуть» and «Открыть главу заново» below.
	if edit.ID != draft.ID || edit.Text != body {
		t.Fatalf("«Править главу» opens %s %q", edit.ID, edit.Text)
	}
	changed := "Кир ⟨пришёл⟩ в ⟪порт⟫.\n\nТуман стоял."
	if edit, err = fixture.store.UpdateChapterDraft(ctx, fixture.editor, edit.ID, edit.Revision, domain.Change{Text: &changed}); err != nil {
		t.Fatal(err)
	}
	replaced, err := fixture.store.ReplaceChapterFromDraft(ctx, fixture.editor, edit.ID, edit.Revision, "Глава")
	if err != nil {
		t.Fatal(err)
	}
	if replaced.Text != changed || bookChapter(t, fixture, added.AddedChapterID).Paragraphs[0] != "Кир *пришёл* в **порт**." {
		t.Fatalf("the replacement: draft %q, book %q", replaced.Text, bookChapter(t, fixture, added.AddedChapterID).Paragraphs)
	}
	reverted, err := fixture.store.RevertChapterFromDraft(ctx, fixture.editor, replaced.ID, replaced.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if reverted.Text != "Кир ⟨пришёл⟩ в порт.\n\n⟪Туман⟫ стоял * * *\n\n⟨над⟩\n\n⟨водой⟩." {
		t.Fatalf("«Вернуть» gives %q", reverted.Text)
	}
	reopened, err := fixture.store.ReopenChapterFromBook(ctx, fixture.editor, reverted.ID, reverted.Revision)
	if err != nil || reopened.Text != reverted.Text {
		t.Fatalf("«Открыть главу заново» = %q, %v", reopened.Text, err)
	}
}

// specs/2026-11-11-ux5-writing-path.md WP4.3: one draft a chapter of the book. «Править главу» of a chapter a draft added
// opens that draft as the edit of its chapter — its text, its versions and its helper — not a second draft; a chapter the
// book changed since the draft put it there says so, and «Открыть главу заново» takes the text of the book as for any edit.
func TestTheChapterADraftAddedIsEditedInThatDraftPostgres(t *testing.T) {
	fixture := seedDraftProject(t, "ck_chapter_edit_added")
	ctx := context.Background()
	chapters := editSaga(t, fixture)
	add := func(text string) (domain.Draft, string) {
		t.Helper()
		draft := fixture.create(t, domain.KindSketch, text)
		placed, err := fixture.store.UpdateChapterDraft(ctx, fixture.editor, draft.ID, draft.Revision, domain.Change{Place: &domain.Place{BookID: editBook, AfterChapterID: chapters[2]}})
		if err != nil {
			t.Fatal(err)
		}
		added, err := fixture.store.AddChapterDraftToBook(ctx, fixture.editor, draft.ID, placed.Revision)
		if err != nil || added.AddedChapterID == "" {
			t.Fatalf("add = %+v, %v", added, err)
		}
		return added, added.AddedChapterID
	}
	added, chapter := add("Кира ⟦ждала у⟧ ворот.\n\nТарен не пришёл.")

	opened, err := fixture.store.OpenChapterEdit(ctx, fixture.editor, chapter)
	if err != nil || opened.ID != added.ID {
		t.Fatalf("«Править главу» of the chapter a draft added = %s, %v (the draft %s)", opened.ID, err, added.ID)
	}
	if opened.AddedChapterID != "" || opened.Edits == nil || opened.Edits.ChapterID != chapter || opened.Edits.Changed() || opened.Edits.Gone {
		t.Fatalf("the draft is not the edit of its chapter: added %q, edit %+v", opened.AddedChapterID, opened.Edits)
	}
	// What the helper wrote stays lit: the draft keeps its own working text while the book holds the same words.
	if opened.Text != added.Text || opened.Revision <= added.Revision {
		t.Fatalf("the working text = %q at %d", opened.Text, opened.Revision)
	}
	if again, err := fixture.store.OpenChapterEdit(ctx, fixture.owner, chapter); err != nil || again.ID != added.ID {
		t.Fatalf("a second press = %s, %v", again.ID, err)
	}
	page, err := fixture.store.ListChapterDrafts(ctx, fixture.editor.Actor, nil, 10, "")
	if err != nil || len(page.Drafts) != 1 || !page.Drafts[0].Edits || page.Drafts[0].Stage != domain.StageText {
		t.Fatalf("one draft a chapter: %+v, %v", page.Drafts, err)
	}

	// The book changed the chapter after the draft put it there: the draft knows, and the text of the book comes back
	// only by «Открыть главу заново».
	other, otherChapter := add("Ольм молчал.")
	if err := fixture.db.WithTx(ctx, func(tx pgx.Tx) error {
		_, err := fixture.library.ReplaceChapterTextInTx(ctx, tx, library.ReplaceChapterTextInput{
			ProjectID: fixture.project, BookID: editBook, ChapterID: otherChapter, ExpectedRevision: bookChapter(t, fixture, otherChapter).Revision,
			Title: "Глава", Paragraphs: []string{"Текст из файла."},
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	changed, err := fixture.store.OpenChapterEdit(ctx, fixture.editor, otherChapter)
	if err != nil || changed.ID != other.ID || !changed.Edits.Changed() || changed.Text != other.Text {
		t.Fatalf("a chapter the book changed = %+v %q, %v", changed.Edits, changed.Text, err)
	}
	if _, err := fixture.store.ReplaceChapterFromDraft(ctx, fixture.editor, changed.ID, changed.Revision, "Глава"); !errors.Is(err, domain.ErrChapterChanged) {
		t.Fatalf("the book's text is not replaced unasked: %v", err)
	}
	reopened, err := fixture.store.ReopenChapterFromBook(ctx, fixture.editor, changed.ID, changed.Revision)
	if err != nil || reopened.Text != "Текст из файла." || reopened.Edits.Changed() {
		t.Fatalf("«Открыть главу заново» = %q %+v, %v", reopened.Text, reopened.Edits, err)
	}
}

// specs/2026-11-11-ux9-writing-a.md WA1.3, WA4.1: the draft of an edit and its row in the list say whether the book holds
// it — opened, it is the book's text; changed by the author, it is not; replaced, it is again — and the edit is nearly
// done whatever its length.
func TestAnEditSaysWhetherTheBookHoldsItPostgres(t *testing.T) {
	fixture := seedDraftProject(t, "ck_chapter_edit_held")
	ctx := context.Background()
	chapters := editSaga(t, fixture)
	state := func(draftID string) domain.EditState {
		t.Helper()
		page, err := fixture.store.ListChapterDrafts(ctx, fixture.editor.Actor, nil, 10, "")
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range page.Drafts {
			if row.ID == draftID {
				return row.EditState
			}
		}
		t.Fatalf("no row of %s", draftID)
		return ""
	}
	draft, err := fixture.store.OpenChapterEdit(ctx, fixture.editor, chapters[1])
	if err != nil {
		t.Fatal(err)
	}
	if draft.Edits.State() != domain.EditSame || state(draft.ID) != domain.EditSame || draft.Case("") != domain.CaseNearlyDone {
		t.Fatalf("opened: %q, row %q, case %q", draft.Edits.State(), state(draft.ID), draft.Case(""))
	}
	text := draft.Text + "\n\nМила ждала у причала."
	draft, err = fixture.store.UpdateChapterDraft(ctx, fixture.editor, draft.ID, draft.Revision, domain.Change{Text: &text})
	if err != nil {
		t.Fatal(err)
	}
	if draft.Edits.State() != domain.EditEdited || state(draft.ID) != domain.EditEdited {
		t.Fatalf("changed by the author: %q, row %q", draft.Edits.State(), state(draft.ID))
	}
	replaced, err := fixture.store.ReplaceChapterFromDraft(ctx, fixture.editor, draft.ID, draft.Revision, draft.Title)
	if err != nil {
		t.Fatal(err)
	}
	if replaced.Edits.State() != domain.EditSame || state(replaced.ID) != domain.EditSame {
		t.Fatalf("replaced: %q, row %q", replaced.Edits.State(), state(replaced.ID))
	}
	title := "Другое название"
	renamed, err := fixture.store.UpdateChapterDraft(ctx, fixture.editor, replaced.ID, replaced.Revision, domain.Change{Title: &title})
	if err != nil {
		t.Fatal(err)
	}
	if renamed.Edits.State() != domain.EditEdited {
		t.Fatalf("renamed: %q", renamed.Edits.State())
	}
}
