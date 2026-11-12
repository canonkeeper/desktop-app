package library

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"canonkeeper/backend/internal/auth"
	"canonkeeper/backend/internal/projects"
)

// specs/2026-11-11-ux7-reorder.md RK2.1: a move of a volume is updateBook with afterBookId behind the fences of every
// write — a session, the token against forged requests, the origin, the role, and a saga of another author is not
// found. The operation names the place to be, so the same request twice leaves the same shelf.
func TestAMoveOfABookKeepsTheFencesOfAWrite(t *testing.T) {
	move := func(store *memoryStoreForTest, authService *fakeImportAuthService, origin string) *httptest.ResponseRecorder {
		t.Helper()
		handler := newImportHandlerForTest(NewService(store, StaticIDsForTest(), StaticClockForTest()), authService)
		request := newAuthenticatedImportRequestForTest(t, http.MethodPatch, "/books/book-3", `{"afterBookId":null}`)
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Origin", origin)
		request.Header.Set("X-CSRF-Token", "csrf-for-test")
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		return recorder
	}
	shelf := func(role projects.Role, member bool) *memoryStoreForTest {
		store := NewMemoryStoreForTest()
		for _, id := range []string{"book-1", "book-2", "book-3"} {
			store.SeedBook("project-1", id)
		}
		delete(store.projectRoles, roleKey("user-1", "project-1"))
		if member {
			store.projectRoles[roleKey("user-1", "project-1")] = role
		}
		return store
	}
	unchanged := []string{"book-1", "book-2", "book-3"}
	for _, check := range []struct {
		name   string
		store  *memoryStoreForTest
		auth   func(*fakeImportAuthService)
		origin string
		status int
	}{
		{name: "no session", store: shelf(projects.RoleEditor, true), auth: func(service *fakeImportAuthService) { service.currentUserErr = auth.ErrUnauthenticated },
			origin: importAllowedOriginForTest, status: http.StatusUnauthorized},
		{name: "a forged request", store: shelf(projects.RoleEditor, true), auth: func(service *fakeImportAuthService) { service.validateErr = auth.ErrInvalidCSRF },
			origin: importAllowedOriginForTest, status: http.StatusForbidden},
		{name: "another origin", store: shelf(projects.RoleEditor, true), origin: "https://elsewhere.example", status: http.StatusForbidden},
		{name: "a reader of the saga", store: shelf(projects.RoleViewer, true), origin: importAllowedOriginForTest, status: http.StatusForbidden},
		{name: "a saga of another author", store: shelf(projects.RoleEditor, false), origin: importAllowedOriginForTest, status: http.StatusNotFound},
	} {
		authService := fakeImportAuthForTest("user-1")
		if check.auth != nil {
			check.auth(authService)
		}
		if recorder := move(check.store, authService, check.origin); recorder.Code != check.status {
			t.Fatalf("%s: status = %d, want %d; body: %s", check.name, recorder.Code, check.status, recorder.Body.String())
		}
		assertBookOrderForTest(t, check.store, "project-1", unchanged)
	}

	store := shelf(projects.RoleEditor, true)
	for range 2 {
		if recorder := move(store, fakeImportAuthForTest("user-1"), importAllowedOriginForTest); recorder.Code != http.StatusOK {
			t.Fatalf("an editor's move: status = %d; body: %s", recorder.Code, recorder.Body.String())
		}
		assertBookOrderForTest(t, store, "project-1", []string{"book-3", "book-1", "book-2"})
	}
}

// RK2.3: a move asks for what the order of the volumes rebuilds — once a move, and not for a rename or a refused move.
func TestAMoveOfABookAsksForTheRebuildOfWhatTheOrderHolds(t *testing.T) {
	store := NewMemoryStoreForTest()
	for _, id := range []string{"book-1", "book-2", "book-3"} {
		store.SeedBook("project-1", id)
	}
	var asked []string
	service := NewService(store, StaticIDsForTest(), StaticClockForTest()).
		WithReordered(func(_ context.Context, projectID string) { asked = append(asked, projectID) })
	editor := ActorEditor("project-1")
	first := "book-1"
	if _, err := service.UpdateBook(t.Context(), editor, "book-3", UpdateBookInput{AfterBookID: OrderPlacement{Present: true, AfterID: &first}}); err != nil {
		t.Fatal(err)
	}
	title := "Новое имя"
	if _, err := service.UpdateBook(t.Context(), editor, "book-2", UpdateBookInput{Title: StringPatch{Present: true, Value: &title}}); err != nil {
		t.Fatal(err)
	}
	self := "book-2"
	if _, err := service.UpdateBook(t.Context(), editor, "book-2", UpdateBookInput{AfterBookID: OrderPlacement{Present: true, AfterID: &self}}); err == nil {
		t.Fatal("a book was put after itself")
	}
	if len(asked) != 1 || asked[0] != "project-1" {
		t.Fatalf("the rebuild was asked for %v, want once for the saga", asked)
	}
}
