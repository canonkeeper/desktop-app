// SPDX-License-Identifier: AGPL-3.0-only

package sagaarchive

import (
	"slices"
	"strings"
	"testing"

	"canonkeeper/backend/internal/domain/sagalayer"
	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/library"
	service "canonkeeper/backend/internal/services/sagaarchive"
)

// rows lists what a saga holds that a file carries, by its words and not by its ids.
func (world *world) rows(projectID string) map[string][]string {
	world.t.Helper()
	return map[string][]string{
		"books": world.strings(`SELECT key_number || ' ' || "order" || ' ' || title || ' ' || coalesce(subtitle, '') || ' ' || display_number || ' ' || coalesce(cover_color, '')
			FROM books WHERE project_id = $1 AND deleted_at IS NULL`, projectID),
		"text": world.strings(`SELECT chapter.title || ' ' || chapter."order" || ' ' || paragraph.variant || ' ' || paragraph."order" || ' ' || paragraph.kind || ' ' || paragraph.markdown || ' | ' || paragraph.plain_text
			FROM chapter_paragraphs AS paragraph JOIN chapters AS chapter ON chapter.id = paragraph.chapter_id
			WHERE chapter.project_id = $1 AND chapter.deleted_at IS NULL`, projectID),
		"cards": world.strings(`SELECT chapter.title || ' ' || card.version || ' ' || card.model || ' ' || (card.built_from IS NOT NULL) || ' ' || card.card_json::text
			FROM chapter_cards AS card JOIN chapters AS chapter ON chapter.id = card.chapter_id WHERE card.project_id = $1`, projectID),
		"names":     world.strings(`SELECT digest || ' ' || model || ' ' || names_json::text FROM saga_names WHERE project_id = $1`, projectID),
		"materials": world.strings(`SELECT name || ' ' || name_key || ' ' || kind || ' ' || properties::text || ' ' || source_kind || ' ' || created_at FROM author_materials WHERE project_id = $1`, projectID),
		"memory": world.strings(`SELECT record.body || ' ' || coalesce(record.carried_origin, 'manual') || ' ' || coalesce(chapter.title, '-')
			FROM author_memory AS record LEFT JOIN chapters AS chapter ON chapter.id = record.chapter_id WHERE record.project_id = $1`, projectID),
		"hidden":      world.strings(`SELECT node_key || ' ' || node_name FROM saga_map_hidden WHERE project_id = $1`, projectID),
		"transcripts": world.strings(`SELECT title || ' ' || others_turns || ' ' || turns::text FROM saga_transcripts WHERE project_id = $1`, projectID),
		"counter":     world.strings(`SELECT book_key_numbers::text FROM projects WHERE id = $1`, projectID),
	}
}

func (world *world) ids(projectID string) []string {
	return append(append(
		world.strings(`SELECT id::text FROM books WHERE project_id = $1`, projectID),
		world.strings(`SELECT id::text FROM chapters WHERE project_id = $1`, projectID)...),
		world.strings(`SELECT paragraph.id::text FROM chapter_paragraphs AS paragraph JOIN chapters AS chapter ON chapter.id = paragraph.chapter_id WHERE chapter.project_id = $1`, projectID)...)
}

// due is what the layer of a saga says of a rebuild.
func (world *world) due(projectID string) bool {
	world.t.Helper()
	stamps, err := world.saga.Stamps(world.ctx, projectID, "published")
	if err != nil {
		world.t.Fatal(err)
	}
	built, err := world.saga.BuiltChapters(world.ctx, projectID)
	if err != nil || built == nil {
		world.t.Fatalf("the layer of the saga = %v, %v", built, err)
	}
	return sagalayer.Due(stamps, built)
}

// specs/2026-11-13-desk-sagafile.md SF3.3–SF3.7, SF3.11, SF4.2: a saga goes into its file and comes out of it as a new saga
// with the same text, faces and keys of every chapter in both texts, new ids of everything, the cards under the digests of
// its own chapters, a fresh registry of names, the layer behind as it was, the records of the author with their origins —
// and the memory is ready without a call of the model or a Job of the threads.
func TestASagaGoesThroughItsFileWithItsMemory(t *testing.T) {
	world := newWorld(t, "ck_saga_file_round")
	notesBefore := world.count(`SELECT count(*) FROM personal_notes`)
	first := world.mustOpen(northernLimit(t, fixtureOptions{covers: map[int][]byte{1: coverPNG(t, 600, 900)}}), "first-open-key")
	if first.Renamed || first.Report.Cards != 6 || first.Report.ChaptersWithoutCards.Count != 0 || !first.Report.NamesFresh || !first.Report.Threads ||
		first.Report.Notes != 3 || first.Report.Chats != 1 || len(first.Report.CoversSkipped) != 0 {
		t.Fatalf("the open of the file = %+v", first)
	}
	// SF3.8: the chat of the file is a record of the saga, not a note; a note of a chat keeps its quote in its body.
	if notes := world.count(`SELECT count(*) FROM personal_notes`); notes != notesBefore+3 {
		t.Fatalf("notes after the open = %d; want the three of the file", notes-notesBefore)
	}
	if world.ready(first.ProjectID) != 0 || world.model.calls != 0 {
		t.Fatalf("the memory of the opened saga called the model %d times or queued the threads", world.model.calls)
	}
	// SF3.5: the digest of every card written is the digest of its chapter the text index found.
	if stray := world.count(`
		SELECT count(*) FROM chapter_cards AS card
		WHERE card.project_id = $1 AND NOT EXISTS (
		  SELECT 1 FROM chapter_text_index_state AS state
		  WHERE state.chapter_id = card.chapter_id AND state.chapter_content_hash = card.content_hash)
	`, first.ProjectID); stray != 0 {
		t.Fatalf("%d cards are not under the digest the text index found", stray)
	}
	if world.due(first.ProjectID) {
		t.Fatal("the layer behind by one chapter is due after the open")
	}
	// SF3.11: no wave of «Сага готова» is opened — neither by the open nor by the Jobs after it — so no letter goes.
	if waves := world.count(`SELECT count(*) FROM saga_ready_waves`); waves != 0 {
		t.Fatalf("the open opened %d waves of «Сага готова»", waves)
	}

	second := world.mustOpen(world.save(first.ProjectID), "second-open-key")
	if !second.Renamed || !strings.HasPrefix(second.Name, "Северный предел — из файла от ") {
		t.Fatalf("the second saga of the name = %q, renamed %v", second.Name, second.Renamed)
	}
	if world.ready(second.ProjectID) != 0 || world.model.calls != 0 || world.count(`SELECT count(*) FROM saga_ready_waves`) != 0 {
		t.Fatalf("the memory of the saga of the saved file called the model %d times, queued the threads or opened a wave", world.model.calls)
	}
	for _, view := range []governance.SourceView{governance.SourceViewDraft, governance.SourceViewPublished} {
		if before, after := world.keys(first.ProjectID, view), world.keys(second.ProjectID, view); !slices.Equal(before, after) || len(before) == 0 {
			t.Fatalf("the keys of the %s text:\n%v\nafter the file:\n%v", view, before, after)
		}
	}
	before, after := world.rows(first.ProjectID), world.rows(second.ProjectID)
	for part, rows := range before {
		if !slices.Equal(rows, after[part]) {
			t.Errorf("the %s of the saga:\n%v\nafter the file:\n%v", part, rows, after[part])
		}
	}
	if world.due(first.ProjectID) != world.due(second.ProjectID) {
		t.Fatal("the layer of the new saga says another thing of a rebuild")
	}
	for _, id := range world.ids(second.ProjectID) {
		if slices.Contains(world.ids(first.ProjectID), id) {
			t.Fatalf("the id %s came from the file", id)
		}
	}
	// SF3.4: a book added later gets the number after every number the saga gave.
	book, err := world.library.CreateBook(world.ctx, world.actor(), second.ProjectID, library.BookInput{Title: "Новая книга", DisplayNumber: "4"})
	if err != nil {
		t.Fatal(err)
	}
	if number := world.count(`SELECT key_number FROM books WHERE id = $1`, book.ID); number != 5 {
		t.Fatalf("the number of a new book = %d; want 5", number)
	}
	// SF3.8: the chat of the file reads with the place of the chapter its answer cites — «Застава», the second chapter of its
	// book, whose published order is 1 from zero (specs/2026-11-14-chapter-place-number.md CP1.1).
	transcripts, err := New(world.db).Transcripts(world.ctx, world.user, second.ProjectID)
	if err != nil || len(transcripts) != 1 {
		t.Fatalf("the chats of the file = %+v, %v", transcripts, err)
	}
	_, places, err := service.GetTranscript{Store: New(world.db), Outlines: memoryOutlines{world}}.Execute(world.ctx, world.user, second.ProjectID, transcripts[0].ID)
	if err != nil || places["b1c1"] != "Книга 2, гл. 2 „Застава“" {
		t.Fatalf("the places of the answer = %v, %v", places, err)
	}
}
