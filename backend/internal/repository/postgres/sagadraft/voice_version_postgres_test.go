package sagadraft

import (
	"context"
	"errors"
	"testing"

	domain "canonkeeper/backend/internal/domain/sagadraft"
	service "canonkeeper/backend/internal/services/sagadraft"
)

// specs/2026-11-10-ux5-voice.md VO1.1, VO1.3: the first change of a conversation keeps the draft before it — text and
// idea — as the version «до разговора» in the same transaction, and alone it keeps it without touching the revision;
// «Вернуть эту версию» of it gives the text and the idea back, and the version kept before that return keeps the idea
// too, so «Отменить» gives the conversation back.
func TestTheVersionBeforeAConversationKeepsTheIdeaPostgres(t *testing.T) {
	fixture := seedDraftProject(t, "ck_chapter_draft_before_voice")
	ctx := context.Background()
	update := service.Update{Store: fixture.store}
	draft := fixture.create(t, domain.KindSketch, "Ночь у ворот.")
	idea := "Кира ждёт."
	draft, err := update.Execute(ctx, fixture.editor, draft.ID, draft.Revision, domain.Change{Idea: &idea})
	if err != nil {
		t.Fatal(err)
	}

	alone, err := update.Execute(ctx, fixture.editor, draft.ID, draft.Revision, domain.Change{KeepVoice: true})
	if err != nil || alone.KeptVersion == 0 || alone.Revision != draft.Revision {
		t.Fatalf("the version kept alone = %+v, %v", alone, err)
	}
	told := "Кира ждёт.\n\nТарен не пришёл."
	changed, err := update.Execute(ctx, fixture.editor, draft.ID, draft.Revision, domain.Change{Idea: &told, KeepVoice: true})
	if err != nil || changed.KeptVersion != alone.KeptVersion+1 || changed.Revision != draft.Revision+1 || changed.Idea != told {
		t.Fatalf("the first change of a conversation = %+v, %v", changed, err)
	}
	before, err := fixture.store.ChapterDraftVersion(ctx, fixture.viewer.Actor, draft.ID, changed.KeptVersion)
	if err != nil || before.Reason != domain.VersionBeforeVoice || before.Text != "Ночь у ворот." || before.Idea == nil || *before.Idea != idea {
		t.Fatalf("the version before the conversation = %+v, %v", before, err)
	}
	dictated := "Ночь у ворот.\n\nКира берёт фонарь."
	written, err := update.Execute(ctx, fixture.editor, draft.ID, changed.Revision, domain.Change{Text: &dictated})
	if err != nil || written.KeptVersion != 0 {
		t.Fatalf("a later change keeps nothing = %+v, %v", written, err)
	}

	back, err := fixture.store.TakeChapterDraftVersion(ctx, fixture.editor, draft.ID, changed.KeptVersion, written.Revision, domain.Take{Whole: true})
	if err != nil || back.Text != "Ночь у ворот." || back.Idea != idea {
		t.Fatalf("«Вернуть как было до разговора» = %+v, %v", back, err)
	}
	versions, err := fixture.store.ChapterDraftVersions(ctx, fixture.viewer.Actor, draft.ID)
	if err != nil || versions[0].Reason != domain.VersionBeforeRestore {
		t.Fatalf("versions = %+v, %v", versions, err)
	}
	undo, err := fixture.store.ChapterDraftVersion(ctx, fixture.viewer.Actor, draft.ID, versions[0].Number)
	if err != nil || undo.Text != dictated || undo.Idea == nil || *undo.Idea != told {
		t.Fatalf("the version before the return = %+v, %v", undo, err)
	}
	again, err := fixture.store.TakeChapterDraftVersion(ctx, fixture.editor, draft.ID, undo.Number, back.Revision, domain.Take{Whole: true})
	if err != nil || again.Text != dictated || again.Idea != told {
		t.Fatalf("«Отменить» = %+v, %v", again, err)
	}
	// Any other version keeps the text alone.
	cut := "Ночь у ворот."
	if _, err := update.Execute(ctx, fixture.editor, draft.ID, again.Revision, domain.Change{Text: &cut, KeepVersion: true}); err != nil {
		t.Fatal(err)
	}
	versions, err = fixture.store.ChapterDraftVersions(ctx, fixture.viewer.Actor, draft.ID)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := fixture.store.ChapterDraftVersion(ctx, fixture.viewer.Actor, draft.ID, versions[0].Number)
	if err != nil || plain.Reason != domain.VersionBeforeDelete || plain.Idea != nil {
		t.Fatalf("a version of the author = %+v, %v", plain, err)
	}
}

// specs/2026-11-11-ux5-writing-path.md WP5.4: a text of the author the draft could not take — the chapter changed in
// another place where he typed — goes into the versions as «Ваш вариант», with his idea when that was the field; the
// draft and its revision stay as they are.
func TestTheAuthorsVariantGoesIntoTheVersionsPostgres(t *testing.T) {
	fixture := seedDraftProject(t, "ck_chapter_draft_variant")
	ctx := context.Background()
	update := service.Update{Store: fixture.store}
	draft := fixture.create(t, domain.KindSketch, "Ночь у ворот.")
	mine := "Ночь у ворот. Кира ⟨ждёт⟩."
	kept, err := update.Execute(ctx, fixture.editor, draft.ID, draft.Revision, domain.Change{Variant: &domain.Variant{Text: &mine}})
	if err != nil || kept.KeptVersion == 0 || kept.Revision != draft.Revision || kept.Text != draft.Text {
		t.Fatalf("the variant = %+v, %v", kept, err)
	}
	version, err := fixture.store.ChapterDraftVersion(ctx, fixture.viewer.Actor, draft.ID, kept.KeptVersion)
	if err != nil || version.Reason != domain.VersionAuthorVariant || version.Text != mine || version.Idea != nil {
		t.Fatalf("the version of the variant = %+v, %v", version, err)
	}
	idea := "Кира ждёт Тарена."
	withIdea, err := update.Execute(ctx, fixture.editor, draft.ID, draft.Revision, domain.Change{Variant: &domain.Variant{Idea: &idea}})
	if err != nil {
		t.Fatal(err)
	}
	if version, err = fixture.store.ChapterDraftVersion(ctx, fixture.viewer.Actor, draft.ID, withIdea.KeptVersion); err != nil ||
		version.Text != draft.Text || version.Idea == nil || *version.Idea != idea {
		t.Fatalf("the variant of the idea = %+v, %v", version, err)
	}
	broken := "Ночь ⟦у ворот."
	if _, err := update.Execute(ctx, fixture.editor, draft.ID, draft.Revision, domain.Change{Variant: &domain.Variant{Text: &broken}}); err == nil {
		t.Fatal("a variant with broken marks is kept")
	}
	if _, err := update.Execute(ctx, fixture.viewer, draft.ID, draft.Revision, domain.Change{Variant: &domain.Variant{Text: &mine}}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("a reader keeps a variant: %v", err)
	}
	if _, err := update.Execute(ctx, fixture.outsider, draft.ID, draft.Revision, domain.Change{Variant: &domain.Variant{Text: &mine}}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("someone outside keeps a variant: %v", err)
	}
}
