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
	if err != nil || chatPage.Meta.TotalCount != 1 || chatPage.Data[0].ProjectTitle == "" {
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
	chatPage, err = (notesservice.List{Repository: repo}).Execute(ctx, actor.UserID, notesservice.ListInput{})
	if err != nil || chatPage.Meta.TotalCount != 1 || chatPage.Data[0].ProjectTitle != "" {
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
