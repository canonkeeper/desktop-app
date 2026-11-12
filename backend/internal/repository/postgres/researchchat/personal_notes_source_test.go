package researchchat

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"canonkeeper/backend/internal/auth"
	"canonkeeper/backend/internal/domain/agent"
	librarydomain "canonkeeper/backend/internal/domain/library"
	notesdomain "canonkeeper/backend/internal/domain/notes"
	researchdomain "canonkeeper/backend/internal/domain/researchchat"
	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/library"
	notesrepo "canonkeeper/backend/internal/repository/postgres/notes"
	notesservice "canonkeeper/backend/internal/services/notes"
	librarystore "canonkeeper/backend/internal/store/library"
	"canonkeeper/backend/internal/testutil"
)

func TestPersonalNoteChatCaptureAllowsStreamingAndRetainsBodyAfterDeletion(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_note_chat_origin")
	seedReadAuthorityState(t, db, time.Now().UTC().Truncate(time.Microsecond), governance.SourceViewPublished, "viewer")
	ctx := context.Background()
	repo := notesrepo.New(db)
	actor := notesservice.Write{UserID: authorityActorID, Proof: auth.SessionWriteProof{SessionHash: strings.Repeat("a", 64), CSRFSecretHash: strings.Repeat("b", 64)}}
	source := notesdomain.Source{Kind: "chat", ChatID: authorityChatID, ProviderTurnID: "provider-turn-rch-1", ProviderItemID: "still-streaming-item", Quote: "Visible partial copy"}
	create := notesservice.Create{Repository: repo}
	note, err := create.Execute(ctx, actor, notesservice.CreateInput{Body: "My independent note", Source: &source}, "note-chat-key", "POST", "/personal-notes")
	if err != nil {
		t.Fatal(err)
	}
	chatPage, err := (notesservice.List{Repository: repo}).Execute(ctx, actor.UserID, notesservice.ListInput{ProjectID: authorityProjectID, Source: "chat"})
	if err != nil || chatPage.Meta.TotalCount != 1 || chatPage.Data[0].ProjectTitle == "" || chatPage.Data[0].ChatTitle == "" || chatPage.Data[0].Quote != "Visible partial copy" {
		t.Fatalf("chat summary metadata=%#v,%v", chatPage, err)
	}
	resolved, err := repo.ReadSource(ctx, actor.UserID, note.ID)
	if err != nil || resolved.Chat == nil || resolved.Chat.TurnID != authorityTurnID || resolved.Chat.JobID != authorityJobID || resolved.Resolution.Source.SourceView != "published" {
		t.Fatalf("binding=%#v, %v", resolved, err)
	}
	source.ProviderTurnID = "unbound-turn"
	if _, err = create.Execute(ctx, actor, notesservice.CreateInput{Body: "Forged source", Source: &source}, "invalid-turn-key", "POST", "/personal-notes"); !errors.Is(err, notesdomain.ErrNotFound) {
		t.Fatalf("unknown turn accepted: %v", err)
	}
	if _, err = db.Pool.Exec(ctx, `UPDATE project_memberships SET role='editor' WHERE id=$1`, authorityMembershipID); err != nil {
		t.Fatal(err)
	}
	if _, err = New(db).MarkDeleted(ctx, researchdomain.Actor{UserID: authorityActorID}, authorityChatID, agent.SessionResearch); err != nil {
		t.Fatal(err)
	}
	resolved, err = repo.ReadSource(ctx, actor.UserID, note.ID)
	if err != nil || resolved.Resolution.State != "unavailable" || resolved.Resolution.Source != nil {
		t.Fatalf("deleted origin=%#v, %v", resolved, err)
	}
	// The note of a deleted chat stays in its saga, without the chat (specs/2026-11-11-ux7-notes.md NT3.2).
	chatPage, err = (notesservice.List{Repository: repo}).Execute(ctx, actor.UserID, notesservice.ListInput{ProjectID: authorityProjectID})
	if err != nil || chatPage.Meta.TotalCount != 1 || chatPage.Data[0].ProjectTitle == "" || chatPage.Data[0].ChatTitle != "" {
		t.Fatalf("deleted chat metadata=%#v,%v", chatPage, err)
	}
	retained, err := repo.Get(ctx, actor.UserID, note.ID)
	if err != nil || retained.Body != "My independent note" || retained.Source.Quote != "Visible partial copy" {
		t.Fatalf("lost actor copy: %#v, %v", retained, err)
	}
}

func TestPersonalNoteChapterViewerCaptureCurrentChangedAndRevoked(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_note_chapter_origin")
	proof := seedSubmitTurnCanonicalBaseline(t, db, time.Now().UTC().Truncate(time.Microsecond))
	ctx := context.Background()
	repo := notesrepo.New(db)
	manuscripts := librarystore.New(db)
	publishChapterDraftForTest(t, manuscripts, 1)
	if _, err := db.Pool.Exec(ctx, `UPDATE project_memberships SET role='viewer' WHERE id=$1`, submitTurnMembershipID); err != nil {
		t.Fatal(err)
	}
	actor := notesservice.Write{UserID: submitTurnActorID, Proof: proof}
	paragraphID := submitTurnParagraphID
	source := notesdomain.Source{Kind: "chapter", Quote: "canonical text", Locator: &librarydomain.ReaderLocator{
		ProjectID: submitTurnProjectID, BookID: submitTurnBookID, ChapterID: submitTurnChapterID, TargetView: "published", Revision: 1, ParagraphID: &paragraphID,
	}}
	create := notesservice.Create{Repository: repo}
	note, err := create.Execute(ctx, actor, notesservice.CreateInput{Body: "My chapter copy", Source: &source}, "chapter-note-key", "POST", "/personal-notes")
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := repo.ReadSource(ctx, actor.UserID, note.ID)
	if err != nil || resolved.Resolution.State != "current" || resolved.Resolution.Source.Locator.ParagraphID == nil {
		t.Fatalf("current=%#v, %v", resolved, err)
	}
	chapterPage, err := (notesservice.List{Repository: repo}).Execute(ctx, actor.UserID, notesservice.ListInput{ProjectID: submitTurnProjectID, BookID: submitTurnBookID})
	if err != nil || chapterPage.Meta.TotalCount != 1 || chapterPage.Data[0].ChapterTitle == "" {
		t.Fatalf("chapter summary metadata=%#v,%v", chapterPage, err)
	}
	source.Locator.Revision = 2
	if _, err = create.Execute(ctx, actor, notesservice.CreateInput{Body: "Stale", Source: &source}, "stale-source-key", "POST", "/personal-notes"); !errors.Is(err, notesdomain.ErrConflict) {
		t.Fatalf("stale capture=%v", err)
	}
	if _, err = db.Pool.Exec(ctx, `UPDATE project_memberships SET role='owner' WHERE id=$1`, submitTurnMembershipID); err != nil {
		t.Fatal(err)
	}
	// A legacy edit made before the editor was removed: draft revision 2, then published.
	changed := []library.Paragraph{{
		ID: paragraphID, ChapterID: submitTurnChapterID, Revision: 2, Variant: library.ParagraphVariantDraft,
		Kind: library.ParagraphKindParagraph, Order: 1, Markdown: "Changed source", PlainText: "Changed source",
		WordCount: 2, UpdatedAt: time.Now().UTC(),
	}}
	if _, err = manuscripts.SaveChapterDraft(ctx, library.SaveChapterDraftStoreInput{
		ProjectID: submitTurnProjectID, BookID: submitTurnBookID, ChapterID: submitTurnChapterID,
		ExpectedRevision: 1, Title: "RCH Chapter", Revision: 2, Status: library.ChapterStatusStale,
		UpdatedAt: time.Now().UTC(), Paragraphs: changed,
	}); err != nil {
		t.Fatal(err)
	}
	publishChapterDraftForTest(t, manuscripts, 2)
	resolved, err = repo.ReadSource(ctx, actor.UserID, note.ID)
	if err != nil || resolved.Resolution.State != "changed" || resolved.Resolution.Source.Locator.ParagraphID != nil || resolved.Resolution.Source.Locator.Revision != 2 {
		t.Fatalf("changed=%#v, %v", resolved, err)
	}
	if _, err = db.Pool.Exec(ctx, `UPDATE project_memberships SET removed_at=clock_timestamp() WHERE id=$1`, submitTurnMembershipID); err != nil {
		t.Fatal(err)
	}
	resolved, err = repo.ReadSource(ctx, actor.UserID, note.ID)
	if err != nil || resolved.Resolution.State != "unavailable" || resolved.Resolution.Source != nil {
		t.Fatalf("revoked=%#v, %v", resolved, err)
	}
	chapterPage, err = (notesservice.List{Repository: repo}).Execute(ctx, actor.UserID, notesservice.ListInput{})
	if err != nil || chapterPage.Meta.TotalCount != 1 || chapterPage.Data[0].ProjectTitle != "" || chapterPage.Data[0].BookTitle != "" || chapterPage.Data[0].ChapterTitle != "" {
		t.Fatalf("revoked metadata=%#v,%v", chapterPage, err)
	}
	chapterPage, err = (notesservice.List{Repository: repo}).Execute(ctx, actor.UserID, notesservice.ListInput{ProjectID: submitTurnProjectID})
	if err != nil || chapterPage.Meta.TotalCount != 0 {
		t.Fatalf("revoked source filter=%#v,%v", chapterPage, err)
	}
	updatedBody := "Still mine"
	retained, err := repo.Update(ctx, actor, note.ID, notesservice.UpdateInput{Body: &updatedBody, ExpectedRevision: 1})
	if err != nil || retained.Source.Quote != "canonical text" || retained.Source.Locator.Revision != 1 {
		t.Fatalf("independent text/source=%#v, %v", retained, err)
	}
}

// publishChapterDraftForTest publishes the chapter's current draft revision through the library store.
func publishChapterDraftForTest(t *testing.T, manuscripts *librarystore.Store, revision int64) {
	t.Helper()
	ctx := context.Background()
	draft, err := manuscripts.ListChapterParagraphs(ctx, library.ListChapterParagraphsStoreInput{
		ProjectID: submitTurnProjectID, BookID: submitTurnBookID, ChapterID: submitTurnChapterID,
		Revision: revision, Variant: library.ParagraphVariantDraft,
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	published := make([]library.Paragraph, 0, len(draft))
	for _, paragraph := range draft {
		paragraph.Variant = library.ParagraphVariantPublished
		paragraph.UpdatedAt = now
		published = append(published, paragraph)
	}
	if _, err := manuscripts.PublishChapter(ctx, library.PublishChapterStoreInput{
		ProjectID: submitTurnProjectID, BookID: submitTurnBookID, ChapterID: submitTurnChapterID,
		Revision: revision, PublishedAt: now, Paragraphs: published,
	}); err != nil {
		t.Fatal(err)
	}
}

// A note from a selection of the book keeps the quote apart and is saved without a thought; the catalog names its place
// by the words of the server and its saga, finds it by the quote and keeps the own thoughts under a saga; when the
// chapter is gone the note stays in its saga, without a place (specs/2026-11-11-ux7-notes.md NT1.2, NT1.3, NT2.4, NT3.2).
func TestPersonalNoteOfAPlaceKeepsTheQuoteApartAndNamesItsPlace(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_note_place_parts")
	proof := seedSubmitTurnCanonicalBaseline(t, db, time.Now().UTC().Truncate(time.Microsecond))
	ctx := context.Background()
	repo := notesrepo.New(db)
	manuscripts := librarystore.New(db)
	publishChapterDraftForTest(t, manuscripts, 1)
	actor := notesservice.Write{UserID: submitTurnActorID, Proof: proof}
	paragraphID := submitTurnParagraphID
	source := notesdomain.Source{Kind: "chapter", Quote: "canonical text", Locator: &librarydomain.ReaderLocator{
		ProjectID: submitTurnProjectID, BookID: submitTurnBookID, ChapterID: submitTurnChapterID, TargetView: "published", Revision: 1, ParagraphID: &paragraphID,
	}}
	create := notesservice.Create{Repository: repo}
	update := notesservice.Update{Repository: repo}
	list := notesservice.List{Repository: repo}
	note, err := create.Execute(ctx, actor, notesservice.CreateInput{Body: "", Source: &source}, "quote-only-key", "POST", "/personal-notes")
	if err != nil || note.Body != "" || note.Source == nil || note.Source.Quote != "canonical text" {
		t.Fatalf("quote without a thought=%#v,%v", note, err)
	}
	if _, err = create.Execute(ctx, actor, notesservice.CreateInput{Body: " "}, "own-blank-key", "POST", "/personal-notes"); !errors.Is(err, notesdomain.ErrValidation) {
		t.Fatalf("own note without a thought=%v", err)
	}
	own, err := create.Execute(ctx, actor, notesservice.CreateInput{Body: "Своя мысль"}, "own-thought-key", "POST", "/personal-notes")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = update.Execute(ctx, actor, own.ID, notesservice.UpdateInput{Body: notesText(""), ExpectedRevision: 1}); !errors.Is(err, notesdomain.ErrValidation) {
		t.Fatalf("own thought emptied=%v", err)
	}
	thought, err := update.Execute(ctx, actor, note.ID, notesservice.UpdateInput{Body: notesText("Сверить с судом"), ExpectedRevision: 1})
	if err != nil || thought.Body != "Сверить с судом" {
		t.Fatalf("thought written=%#v,%v", thought, err)
	}
	if thought, err = update.Execute(ctx, actor, note.ID, notesservice.UpdateInput{Body: notesText(""), ExpectedRevision: 2}); err != nil || thought.Body != "" {
		t.Fatalf("thought emptied beside the quote=%#v,%v", thought, err)
	}
	saga, err := list.Execute(ctx, actor.UserID, notesservice.ListInput{ProjectID: submitTurnProjectID})
	if err != nil || saga.Meta.TotalCount != 2 {
		t.Fatalf("saga with own thoughts=%#v,%v", saga, err)
	}
	var placed notesdomain.Entry
	for _, entry := range saga.Data {
		if entry.ID == note.ID {
			placed = entry
		}
	}
	if placed.Quote != "canonical text" || placed.Preview != "" || placed.ProjectTitle != "RCH Submit" || !strings.Contains(placed.Place, "„RCH Chapter“") ||
		!strings.HasPrefix(placed.Place, "Книга 1, гл. ") {
		t.Fatalf("entry of a place=%#v", placed)
	}
	found, err := list.Execute(ctx, actor.UserID, notesservice.ListInput{Query: "CANONICAL"})
	if err != nil || found.Meta.TotalCount != 1 || found.Data[0].ID != note.ID {
		t.Fatalf("search by the quote=%#v,%v", found, err)
	}
	resolved, err := repo.ReadSource(ctx, actor.UserID, note.ID)
	if err != nil || resolved.Resolution.State != "current" || resolved.Resolution.Place != placed.Place {
		t.Fatalf("resolved place=%#v,%v", resolved, err)
	}
	if _, err = manuscripts.FenceChapterDeletion(ctx, library.FenceChapterDeletionInput{ActorUserID: submitTurnActorID, ProjectID: submitTurnProjectID,
		BookID: submitTurnBookID, ChapterID: submitTurnChapterID, TombstoneID: "00000000-0000-4000-8000-0000000000f1", DeletedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	saga, err = list.Execute(ctx, actor.UserID, notesservice.ListInput{ProjectID: submitTurnProjectID, Source: "chapter"})
	if err != nil || saga.Meta.TotalCount != 1 || saga.Data[0].Place != "" || saga.Data[0].ChapterTitle != "" ||
		saga.Data[0].ProjectTitle != "RCH Submit" || saga.Data[0].Quote != "canonical text" {
		t.Fatalf("gone place stays in its saga=%#v,%v", saga, err)
	}
	resolved, err = repo.ReadSource(ctx, actor.UserID, note.ID)
	if err != nil || resolved.Resolution.State != "unavailable" || resolved.Resolution.Place != "" {
		t.Fatalf("gone place resolved=%#v,%v", resolved, err)
	}
}

func notesText(value string) *string { return &value }
