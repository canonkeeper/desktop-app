package trash

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"canonkeeper/backend/internal/auth"
	"canonkeeper/backend/internal/domain/trash"

	"github.com/go-chi/chi/v5"
)

type userStub struct{ signedIn bool }

func (stub userStub) CurrentUser(context.Context, string) (auth.User, error) {
	if !stub.signedIn {
		return auth.User{}, auth.ErrUnauthenticated
	}
	return auth.User{ID: "author"}, nil
}

type listStub struct {
	list  trash.List
	asked []string
}

func (stub *listStub) Execute(_ context.Context, actorUserID string) (trash.List, error) {
	stub.asked = append(stub.asked, actorUserID)
	return stub.list, nil
}

func serve(handlers Handlers, target string) *httptest.ResponseRecorder {
	router := chi.NewRouter()
	handlers.Mount(router)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, target, nil))
	return response
}

// specs/2026-11-11-ux7-trash.md TB1.1: listRecentlyDeleted is the signed-in author's: without a session it is 401, with a
// parameter 400; it names each row's saga and place and its term, and says drawings only in a build that has them.
func TestListRecentlyDeleted(t *testing.T) {
	deletedAt := time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC)
	until := deletedAt.Add(7 * 24 * time.Hour)
	list := &listStub{list: trash.List{Sources: trash.Sources{
		Sagas: trash.Group[trash.Saga]{Items: []trash.Saga{{ID: "s1", Title: "Северный берег", Books: 3, DeletedAt: deletedAt, RestoreUntil: until}}, Total: 1},
		Books: trash.Group[trash.Book]{Items: []trash.Book{}},
		Chapters: trash.Group[trash.Chapter]{Items: []trash.Chapter{{ID: "c1", ProjectID: "p1", SagaTitle: "Сага", BookID: "b1", BookTitle: "Туманный берег",
			BookDisplayNumber: "2", Title: "Белый порт", Order: 12, DeletedAt: deletedAt, RestoreUntil: until}}, Total: 120},
		// specs/2026-11-12-ux10-book-revert.md BR3.3: a book updated from a file, its version before the update returned until its term.
		BookUpdates: trash.Group[trash.BookUpdate]{Items: []trash.BookUpdate{{BookID: "b2", ProjectID: "p1", SagaTitle: "Сага", Title: "Туманный берег",
			UpdatedAt: deletedAt, RevertUntil: until}}, Total: 1},
	}}}
	if response := serve(Handlers{List: list, Auth: userStub{}}, "/recently-deleted"); response.Code != http.StatusUnauthorized || len(list.asked) != 0 {
		t.Fatalf("without a session = %d", response.Code)
	}
	if response := serve(Handlers{List: list, Auth: userStub{signedIn: true}}, "/recently-deleted?projectId=p1"); response.Code != http.StatusBadRequest {
		t.Fatalf("with a parameter = %d", response.Code)
	}
	response := serve(Handlers{List: list, Auth: userStub{signedIn: true}}, "/recently-deleted")
	body := response.Body.String()
	for _, want := range []string{
		`"sagas":{"items":[{"id":"s1","title":"Северный берег","books":3,"deletedAt":"2026-11-11T09:00:00.000Z","restoreUntil":"2026-11-18T09:00:00.000Z"}],"total":1}`,
		`"books":{"items":[],"total":0}`,
		`"bookDisplayNumber":"2","title":"Белый порт","order":12`, `"total":120`,
		`"bookUpdates":{"items":[{"id":"b2","projectId":"p1","sagaTitle":"Сага","title":"Туманный берег","updatedAt":"2026-11-11T09:00:00.000Z","revertUntil":"2026-11-18T09:00:00.000Z"}],"total":1}`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("listRecentlyDeleted = %d %s; want %s", response.Code, body, want)
		}
	}
	if strings.Contains(body, `"drawings"`) || response.Header().Get("Cache-Control") != "no-store" || list.asked[0] != "author" {
		t.Fatalf("a build without drawings = %s, %v", body, response.Header())
	}
	list.list.Drawings = &trash.Group[trash.Drawing]{Items: []trash.Drawing{{ID: "d1", ProjectID: "p1", SagaTitle: "Сага", Title: "Дракон", Number: 12, DeletedAt: deletedAt, RestoreUntil: until}}, Total: 1}
	if body := serve(Handlers{List: list, Auth: userStub{signedIn: true}}, "/recently-deleted").Body.String(); !strings.Contains(body, `"drawings":{"items":[{"id":"d1","projectId":"p1","sagaTitle":"Сага","title":"Дракон","number":12,`) {
		t.Fatalf("a build with drawings = %s", body)
	}
}
