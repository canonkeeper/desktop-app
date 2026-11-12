package projects

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// specs/2026-11-09-demo-saga.md DS3.1: the sign of the demo saga is not a field of a request. createProject with it
// is an invalid request and never reaches the use case; without it the use case makes an ordinary saga. The response
// names the sign of a saga that has it.
func TestCreateProjectOverHTTPNeverMakesADemoSaga(t *testing.T) {
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	calls := 0
	projectService := &fakeProjectService{
		createProject: func(_ context.Context, _ string, input CreateProjectInput) (ProjectRecord, error) {
			calls++
			if input.Demo {
				t.Fatalf("CreateProject input = %#v; the request made a demo saga", input)
			}
			return ProjectRecord{ID: "project-1", Title: input.Title, CreatedAt: now, UpdatedAt: now}, nil
		},
		getProject: func(context.Context, string, string) (ProjectRecord, error) {
			return ProjectRecord{ID: "project-2", Title: "Example", Demo: true, CreatedAt: now, UpdatedAt: now}, nil
		},
	}
	handler := newProjectHandlerForTest(projectService, fakeProjectAuthForTest("user-1"))
	post := func(body string) *httptest.ResponseRecorder {
		req := newAuthenticatedProjectRequestForTest(t, http.MethodPost, "/projects", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", projectAllowedOriginForTest)
		req.Header.Set("X-CSRF-Token", opaqueValueForTest())
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}

	assertProblemForTest(t, post(`{"title":"Mine","demo":true}`), http.StatusBadRequest, "request.invalid")
	if calls != 0 {
		t.Fatalf("the use case ran %d times for a request with the sign", calls)
	}
	created := post(`{"title":"Mine"}`)
	if created.Code != http.StatusCreated || calls != 1 {
		t.Fatalf("createProject = %d after %d calls; body: %s", created.Code, calls, created.Body.String())
	}
	var ordinary map[string]any
	if err := json.NewDecoder(created.Body).Decode(&ordinary); err != nil {
		t.Fatal(err)
	}
	if _, has := ordinary["demo"]; has {
		t.Fatalf("an ordinary saga = %v; want no sign", ordinary)
	}

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, newAuthenticatedProjectRequestForTest(t, http.MethodGet, "/projects/project-2", nil))
	var demo map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&demo); err != nil {
		t.Fatal(err)
	}
	if demo["demo"] != true {
		t.Fatalf("the demo saga = %v; want demo: true", demo)
	}
}

// specs/2026-11-09-demo-saga.md DS1.3, DS1.4: the use case of the saga makes the demo saga with the sign of its
// input and finds the live one of the owner.
func TestTheUseCaseMakesAndFindsTheDemoSagaOfTheOwner(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStoreForTest(t)
	service := NewService(store, StaticIDsForTest("project-1", "membership-1"), StaticClockForTest(time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)))

	if _, err := service.DemoProject(ctx, "user-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("the demo saga of an owner without one = %v; want ErrNotFound", err)
	}
	created, err := service.CreateProject(ctx, "user-1", CreateProjectInput{Title: "Example", Demo: true})
	if err != nil || !created.Demo {
		t.Fatalf("the demo saga = %+v, %v; want the sign", created, err)
	}
	found, err := service.DemoProject(ctx, "user-1")
	if err != nil || found.ID != created.ID {
		t.Fatalf("the demo saga of the owner = %+v, %v; want %s", found, err, created.ID)
	}
	if _, err := service.DemoProject(ctx, "user-2"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("the demo saga of another owner = %v; want ErrNotFound", err)
	}
	if _, err := service.DemoProject(ctx, ""); !errors.Is(err, ErrForbidden) {
		t.Fatalf("the demo saga without an actor = %v; want ErrForbidden", err)
	}
}
