package sagadraft

import "canonkeeper/backend/internal/domain/sagamemory"

// End is the index in reading order where a chapter at the place goes: the chapters before it are [0, End). After a
// chapter — right after it; only a book — at the start of that book; no place — after the last chapter
// (specs/2026-11-06-writing-agent.md, WA1.4). A place whose chapter or book is gone falls to the end.
func (place Place) End(chapters []sagamemory.Chapter) int {
	for i := range chapters {
		if place.AfterChapterID != "" && chapters[i].ID == place.AfterChapterID {
			return i + 1
		}
		if place.AfterChapterID == "" && place.BookID != "" && chapters[i].BookID == place.BookID {
			return i
		}
	}
	return len(chapters)
}
