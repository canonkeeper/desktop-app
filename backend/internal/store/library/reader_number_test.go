package librarystore

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"canonkeeper/backend/internal/auth"
	domainlibrary "canonkeeper/backend/internal/domain/library"
	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/library"
	"canonkeeper/backend/internal/projects"
	librarysearch "canonkeeper/backend/internal/repository/postgres/library"
	"canonkeeper/backend/internal/search"
	searchchapters "canonkeeper/backend/internal/services/library"
	canonstore "canonkeeper/backend/internal/store/canon"
)

// readerNumbered is a chapter as an answer of the reader names it: its number and its title without that number.
type readerNumbered struct {
	ID            string `json:"id"`
	DisplayNumber string `json:"displayNumber"`
	DisplayTitle  string `json:"displayTitle"`
}

func (fixture *readyWaveFixture) readerGet(handler http.Handler, target string, into any) {
	fixture.t.Helper()
	request := httptest.NewRequest(http.MethodGet, target, nil)
	request.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: "reader-session"})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		fixture.t.Fatalf("GET %s = %d: %s", target, recorder.Code, recorder.Body.String())
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), into); err != nil {
		fixture.t.Fatalf("GET %s: %v", target, err)
	}
}

// specs/2026-11-15-reader-chapter-number.md RN1.2: a book published from zero, with a chapter whose title writes its
// number, the second chapter deleted — the contents of the book (every page of it), a chapter with its neighbours, the
// search of chapters and the search of the text name the chapters 1, 2, 3 and the number the title writes, without the gap
// of the deleted one, and the place of each chapter says the same number; the return of the deleted chapter gives the
// numbers back. The stored order and the keys stay.
func TestTheReaderNumbersAChapterByItsPlaceAmongTheLivingChapters(t *testing.T) {
	fixture := newReadyWaveFixture(t, "f82")
	titles := []string{"Застава у перевала", "Тропа над ледником", "Сигнальные огни", "Каменный мост", "12. Северная башня"}
	book := fixture.loadTitled(testUUID(82_001), "reader-number", "Северный предел", titles...)
	fixture.settle()
	if orders := fixture.storedOrders(book.BookID); len(orders["published"]) != 5 || orders["published"][0] != 0 {
		t.Fatalf("the stored published orders = %v; want the book published from zero", orders)
	}
	chapters := fixture.chaptersOfBook(book.BookID)
	reader := pagingHandlerForTest(fixture.db, fixture.ownerID)
	service := library.NewService(fixture.store, library.NewSecureIDGenerator(), auth.SystemClock{})
	chapterSearch := searchchapters.NewSearchChapters(librarysearch.New(fixture.db))
	text := canonstore.New(fixture.db)
	words := map[string]string{chapters[0]: "Застава", chapters[1]: "Тропа", chapters[2]: "Сигнальные", chapters[3]: "Каменный", chapters[4]: "Северная"}

	reads := func(step string, ids, numbers, names []string) {
		t.Helper()
		// The contents of the book, one chapter a page: the place goes on from page to page.
		var listed []readerNumbered
		cursor := ""
		for range len(ids) + 1 {
			target := "/books/" + book.BookID + "/chapters?limit=1"
			if cursor != "" {
				target += "&cursor=" + cursor
			}
			var page struct {
				Data []readerNumbered `json:"data"`
				Meta struct {
					NextCursor *string `json:"nextCursor"`
				} `json:"meta"`
			}
			fixture.readerGet(reader, target, &page)
			listed = append(listed, page.Data...)
			if page.Meta.NextCursor == nil {
				break
			}
			cursor = *page.Meta.NextCursor
		}
		if len(listed) != len(ids) {
			t.Fatalf("%s: the contents list %d chapters, want %d", step, len(listed), len(ids))
		}
		_, version := fixture.keysOf()
		for i, id := range ids {
			if listed[i].ID != id || listed[i].DisplayNumber != numbers[i] || listed[i].DisplayTitle != names[i] {
				t.Fatalf("%s: the contents name chapter %d %+v, want %q «%s»", step, i+1, listed[i], numbers[i], names[i])
			}
			var chapter struct {
				Navigation struct {
					DisplayNumber string          `json:"displayNumber"`
					DisplayTitle  string          `json:"displayTitle"`
					Position      int             `json:"position"`
					Previous      *readerNumbered `json:"previous"`
					Next          *readerNumbered `json:"next"`
				} `json:"navigation"`
			}
			fixture.readerGet(reader, "/chapters/"+id+"?contentVariant=published", &chapter)
			navigation := chapter.Navigation
			if navigation.DisplayNumber != numbers[i] || navigation.DisplayTitle != names[i] || navigation.Position != i+1 {
				t.Fatalf("%s: the chapter %d reads %+v, want %q «%s» at %d", step, i+1, navigation, numbers[i], names[i], i+1)
			}
			if i > 0 && (navigation.Previous == nil || navigation.Previous.DisplayNumber != numbers[i-1] || navigation.Previous.DisplayTitle != names[i-1]) {
				t.Fatalf("%s: the chapter before %d = %+v, want %q", step, i+1, navigation.Previous, numbers[i-1])
			}
			if i+1 < len(ids) && (navigation.Next == nil || navigation.Next.DisplayNumber != numbers[i+1] || navigation.Next.DisplayTitle != names[i+1]) {
				t.Fatalf("%s: the chapter after %d = %+v, want %q", step, i+1, navigation.Next, numbers[i+1])
			}
			// The place of the chapter in the text of the saga says the same number.
			place := ""
			for _, chapter := range version.Chapters {
				if chapter.ID == id {
					place = chapter.Place()
				}
			}
			if !strings.HasSuffix(place, ", гл. "+numbers[i]+" „"+names[i]+"“") {
				t.Fatalf("%s: the place of chapter %d = %q, want its number %q", step, i+1, place, numbers[i])
			}
			// The search of chapters by the number finds it and names it so.
			page, err := chapterSearch.Execute(fixture.ctx, domainlibrary.Actor{UserID: fixture.ownerID}, domainlibrary.ChapterSearchRequest{Query: numbers[i], SourceView: "published"})
			if err != nil || len(page.Data) != 1 || page.Data[0].Locator.ChapterID != id || page.Data[0].ChapterNumber != numbers[i] || page.Data[0].ChapterDisplayTitle != names[i] {
				t.Fatalf("%s: the search of chapters for %q = %+v, %v; want chapter %d", step, numbers[i], page.Data, err, i+1)
			}
			// The search of the text names the chapter of its paragraph by the same number.
			found, err := text.SearchLexical(fixture.ctx, search.LexicalSearchInput{
				ActorUserID: fixture.ownerID, ProjectID: fixture.projectID, SourceView: governance.SourceViewPublished, Query: words[id], Limit: 5,
			})
			if err != nil || len(found.Rows) != 1 || found.Rows[0].ChapterID != id || found.Rows[0].ChapterNumber != numbers[i] {
				t.Fatalf("%s: the search of the text for %q = %+v, %v; want chapter %d numbered %q", step, words[id], found.Rows, err, i+1, numbers[i])
			}
		}
	}
	reads("the book read", chapters, []string{"1", "2", "3", "4", "12"}, []string{"Застава у перевала", "Тропа над ледником", "Сигнальные огни", "Каменный мост", "Северная башня"})
	// The place of «12. Северная башня» is 5, but its number is the one its title writes: the search for 5 finds nothing.
	if page, err := chapterSearch.Execute(fixture.ctx, domainlibrary.Actor{UserID: fixture.ownerID}, domainlibrary.ChapterSearchRequest{Query: "5", SourceView: "published"}); err != nil || len(page.Data) != 0 {
		t.Fatalf("the search of chapters for 5 = %+v, %v; want none", page.Data, err)
	}

	if _, err := fixture.store.FenceChapterDeletion(fixture.ctx, library.FenceChapterDeletionInput{
		ActorUserID: fixture.ownerID, ProjectID: fixture.projectID, BookID: book.BookID, ChapterID: chapters[1],
		TombstoneID: projects.NewSecureIDGenerator().NewDeletionTombstoneID(), DeletedAt: time.Now(),
	}); err != nil {
		t.Fatalf("FenceChapterDeletion() error = %v", err)
	}
	fixture.settle()
	reads("the second chapter deleted", []string{chapters[0], chapters[2], chapters[3], chapters[4]}, []string{"1", "2", "3", "12"},
		[]string{"Застава у перевала", "Сигнальные огни", "Каменный мост", "Северная башня"})
	if orders := fixture.storedOrders(book.BookID); len(orders["draft"]) != 4 || orders["draft"][1] != 3 {
		t.Fatalf("the stored orders after the deletion = %v; want them as they were", orders)
	}

	restored, err := service.RestoreChapter(fixture.ctx, library.Actor{UserID: fixture.ownerID}, chapters[1])
	if number, name := restored.Numbered(); err != nil || number != "2" || name != "Тропа над ледником" {
		t.Fatalf("RestoreChapter() = %q «%s», %v; want the chapter back at 2", number, name, err)
	}
	fixture.settle()
	reads("the chapter returned", chapters, []string{"1", "2", "3", "4", "12"}, []string{"Застава у перевала", "Тропа над ледником", "Сигнальные огни", "Каменный мост", "Северная башня"})
}
