package librarystore

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"canonkeeper/backend/internal/auth"
	"canonkeeper/backend/internal/library"
	"canonkeeper/backend/internal/projects"
	storedb "canonkeeper/backend/internal/store/db"

	"github.com/go-chi/chi/v5"
)

// The lists of chapters and books are read page after page by their cursors, and none is lost
// (specs/2026-11-07-chapter-list-paging.md, P1).
func TestLibraryListsHandEveryChapterAndBookByPagesPostgres(t *testing.T) {
	ctx := context.Background()
	db := openCurrentStoreTestDB(t)
	actorID, strangerID := indexingStoreUUIDForTest(7001), indexingStoreUUIDForTest(7002)
	projectID, bookID, otherBookID := indexingStoreUUIDForTest(7003), indexingStoreUUIDForTest(7004), indexingStoreUUIDForTest(7005)
	now := time.Now().UTC().Truncate(time.Microsecond)
	insertProjectStoreUserForTest(t, db, actorID, "paging-author@example.test")
	insertProjectStoreUserForTest(t, db, strangerID, "paging-stranger@example.test")
	insertProjectStoreProjectForTest(t, db, projectID, actorID, "Paging Saga", now.Add(-3*time.Hour))
	insertProjectStoreMembershipForTest(t, db, indexingStoreUUIDForTest(7006), projectID, actorID, projects.RoleEditor, now.Add(-3*time.Hour), false)
	store := New(db)
	createBookForOrderTest(t, store, projectID, bookID, "Long Book", 1, "1", now.Add(-2*time.Hour))
	createBookForOrderTest(t, store, projectID, otherBookID, "Other Book", 2, "2", now.Add(-2*time.Hour))
	chapterIDs := make([]string, 120)
	for index := range chapterIDs {
		chapterIDs[index] = indexingStoreUUIDForTest(8000 + index)
		createChapterForOrderTest(t, store, projectID, bookID, chapterIDs[index], fmt.Sprintf("Глава %d", index+1), index+1, now.Add(-time.Hour))
	}
	author := pagingHandlerForTest(db, actorID)

	t.Run("every chapter once in reading order by the default page", func(t *testing.T) {
		got, sizes := readEveryPageForTest(t, author, "/books/"+bookID+"/chapters", url.Values{})
		assertPagedIDsForTest(t, got, chapterIDs)
		if fmt.Sprint(sizes) != "[50 50 20]" {
			t.Fatalf("page sizes = %v, want [50 50 20]", sizes)
		}
	})
	t.Run("the largest page", func(t *testing.T) {
		got, sizes := readEveryPageForTest(t, author, "/books/"+bookID+"/chapters", url.Values{"limit": {"100"}})
		assertPagedIDsForTest(t, got, chapterIDs)
		if fmt.Sprint(sizes) != "[100 20]" {
			t.Fatalf("page sizes = %v, want [100 20]", sizes)
		}
	})
	t.Run("a cursor of another list, a broken cursor and a too large page are refused", func(t *testing.T) {
		first := readPageForTest(t, author, "/books/"+bookID+"/chapters", url.Values{"limit": {"1"}}, http.StatusOK)
		if !first.Meta.HasMore || first.Meta.NextCursor == nil {
			t.Fatalf("first page meta = %#v, want a next page", first.Meta)
		}
		cursor := *first.Meta.NextCursor
		readPageForTest(t, author, "/books/"+otherBookID+"/chapters", url.Values{"cursor": {cursor}}, http.StatusBadRequest)
		readPageForTest(t, author, "/books/"+bookID+"/chapters", url.Values{"cursor": {cursor}, "state": {"deleted"}}, http.StatusBadRequest)
		readPageForTest(t, author, "/projects/"+projectID+"/books", url.Values{"cursor": {cursor}}, http.StatusBadRequest)
		readPageForTest(t, pagingHandlerForTest(db, strangerID), "/books/"+bookID+"/chapters", url.Values{"cursor": {cursor}}, http.StatusNotFound)
		readPageForTest(t, author, "/books/"+bookID+"/chapters", url.Values{"cursor": {"not-a-cursor"}}, http.StatusBadRequest)
		readPageForTest(t, author, "/books/"+bookID+"/chapters", url.Values{"limit": {"101"}}, http.StatusBadRequest)
	})

	t.Run("books and deleted sources", func(t *testing.T) {
		bookIDs := []string{bookID, otherBookID}
		deletedChapterIDs := make([]string, 3)
		deletedBookIDs := make([]string, 3)
		for index := range 3 {
			deletedChapterIDs[index] = indexingStoreUUIDForTest(8200 + index)
			createChapterForOrderTest(t, store, projectID, otherBookID, deletedChapterIDs[index], fmt.Sprintf("Удалённая %d", index+1), index+1, now.Add(-time.Hour))
			deletedBookIDs[index] = indexingStoreUUIDForTest(8300 + index)
			createBookForOrderTest(t, store, projectID, deletedBookIDs[index], fmt.Sprintf("Удалённая книга %d", index+1), 3+index, fmt.Sprint(3+index), now.Add(-time.Hour))
		}
		for index := range 3 {
			deletedAt := now.Add(time.Duration(index) * time.Minute)
			if _, err := store.FenceChapterDeletion(ctx, library.FenceChapterDeletionInput{
				ActorUserID: actorID, ProjectID: projectID, BookID: otherBookID, ChapterID: deletedChapterIDs[index],
				TombstoneID: indexingStoreUUIDForTest(8400 + index), DeletedAt: deletedAt,
			}); err != nil {
				t.Fatalf("FenceChapterDeletion(%d) error = %v", index, err)
			}
			if _, err := store.FenceBookDeletion(ctx, library.FenceBookDeletionInput{
				ActorUserID: actorID, ProjectID: projectID, BookID: deletedBookIDs[index],
				TombstoneID: indexingStoreUUIDForTest(8500 + index), DeletedAt: deletedAt,
			}); err != nil {
				t.Fatalf("FenceBookDeletion(%d) error = %v", index, err)
			}
		}
		newestFirst := func(ids []string) []string { return []string{ids[2], ids[1], ids[0]} }
		for _, list := range []struct {
			path  string
			query url.Values
			want  []string
		}{
			{path: "/projects/" + projectID + "/books", query: url.Values{"limit": {"1"}}, want: bookIDs},
			{path: "/projects/" + projectID + "/books", query: url.Values{"limit": {"2"}, "state": {"deleted"}}, want: newestFirst(deletedBookIDs)},
			{path: "/books/" + otherBookID + "/chapters", query: url.Values{"limit": {"2"}, "state": {"deleted"}}, want: newestFirst(deletedChapterIDs)},
		} {
			got, sizes := readEveryPageForTest(t, author, list.path, list.query)
			assertPagedIDsForTest(t, got, list.want)
			if len(sizes) < 2 {
				t.Fatalf("%s %v read in %v pages, want more than one", list.path, list.query, sizes)
			}
		}
	})
}

type pagedListForTest struct {
	Data []struct {
		ID string `json:"id"`
	} `json:"data"`
	Meta struct {
		HasMore    bool    `json:"hasMore"`
		NextCursor *string `json:"nextCursor"`
	} `json:"meta"`
}

func readEveryPageForTest(t *testing.T, handler http.Handler, path string, query url.Values) ([]string, []int) {
	t.Helper()
	var ids []string
	var sizes []int
	for range 10 {
		page := readPageForTest(t, handler, path, query, http.StatusOK)
		sizes = append(sizes, len(page.Data))
		for _, item := range page.Data {
			ids = append(ids, item.ID)
		}
		if !page.Meta.HasMore {
			if page.Meta.NextCursor != nil {
				t.Fatalf("%s: last page carries cursor %q", path, *page.Meta.NextCursor)
			}
			return ids, sizes
		}
		if page.Meta.NextCursor == nil || *page.Meta.NextCursor == "" {
			t.Fatalf("%s: page with more has no cursor", path)
		}
		next := url.Values{}
		for key, values := range query {
			next[key] = values
		}
		next.Set("cursor", *page.Meta.NextCursor)
		query = next
	}
	t.Fatalf("%s: more than 10 pages", path)
	return nil, nil
}

func readPageForTest(t *testing.T, handler http.Handler, path string, query url.Values, wantStatus int) pagedListForTest {
	t.Helper()
	target := path
	if encoded := query.Encode(); encoded != "" {
		target += "?" + encoded
	}
	request := httptest.NewRequest(http.MethodGet, target, nil)
	request.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: "paging-session"})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != wantStatus {
		t.Fatalf("GET %s status = %d, want %d: %s", target, recorder.Code, wantStatus, recorder.Body.String())
	}
	var page pagedListForTest
	if wantStatus == http.StatusOK {
		if err := json.Unmarshal(recorder.Body.Bytes(), &page); err != nil {
			t.Fatalf("GET %s: decode list: %v", target, err)
		}
	}
	return page
}

func assertPagedIDsForTest(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("listed %d items, want %d", len(got), len(want))
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("item %d = %s, want %s", index, got[index], want[index])
		}
	}
}

// pagingHandlerForTest is the library API of the actor over the PostgreSQL store.
func pagingHandlerForTest(db *storedb.DB, actorID string) http.Handler {
	router := chi.NewRouter()
	service := library.NewService(New(db), library.NewSecureIDGenerator(), auth.SystemClock{})
	library.NewHandlers(service, pagingActorForTest{id: actorID}, nil).Mount(router)
	return router
}

type pagingActorForTest struct{ id string }

func (actor pagingActorForTest) CurrentUser(context.Context, string) (auth.User, error) {
	return auth.User{ID: actor.id, Email: actor.id + "@example.test", EmailVerified: true}, nil
}

func (actor pagingActorForTest) AuthenticateWrite(context.Context, string, string) (auth.User, auth.SessionWriteProof, error) {
	return auth.User{}, auth.SessionWriteProof{}, auth.ErrUnauthenticated
}
