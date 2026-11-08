package library

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	domain "canonkeeper/backend/internal/domain/library"
	"github.com/go-chi/chi/v5"
)

type chapterSearchUseCaseStub struct {
	actor   domain.Actor
	request domain.ChapterSearchRequest
	calls   int
}

func (stub *chapterSearchUseCaseStub) Execute(_ context.Context, actor domain.Actor, request domain.ChapterSearchRequest) (domain.ChapterSearchPage, error) {
	stub.actor, stub.request = actor, request
	stub.calls++
	return domain.ChapterSearchPage{Data: []domain.ChapterNavigationSearchResult{}}, nil
}

func TestChapterSearchHTTPRequiresOriginAndStrictBody(t *testing.T) {
	for _, test := range []struct {
		name, body, origin, url string
		status                  int
	}{
		{"valid", `{"query":"458","sourceView":"published"}`, "https://app.test", "/chapters/search", 200},
		{"missing origin", `{"query":"458","sourceView":"published"}`, "", "/chapters/search", 403},
		{"unknown field", `{"query":"458","sourceView":"published","actor":"other"}`, "https://app.test", "/chapters/search", 400},
		{"invalid limit", `{"query":"458","sourceView":"published","limit":0}`, "https://app.test", "/chapters/search", 400},
		{"null limit", `{"query":"458","sourceView":"published","limit":null}`, "https://app.test", "/chapters/search", 400},
		{"null cursor", `{"query":"458","sourceView":"published","cursor":null}`, "https://app.test", "/chapters/search", 400},
		{"url query", `{"query":"458","sourceView":"published"}`, "https://app.test", "/chapters/search?query=458", 400},
		{"extra document", `{"query":"458","sourceView":"published"} {}`, "https://app.test", "/chapters/search", 400},
	} {
		t.Run(test.name, func(t *testing.T) {
			stub := &chapterSearchUseCaseStub{}
			router := chi.NewRouter()
			MountChapterSearch(router, stub, coverCurrentUserStub{}, []string{"https://app.test"})
			request := httptest.NewRequest(http.MethodPost, test.url, strings.NewReader(test.body))
			request.Header.Set("Origin", test.origin)
			request.Header.Set("X-CSRF-Token", "test-csrf")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != test.status {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			if test.status == 200 && (stub.actor.UserID != "actor-1" || stub.request.Query != "458" || response.Header().Get("Cache-Control") != "no-store") {
				t.Fatalf("actor/request=%#v/%#v", stub.actor, stub.request)
			}
			if test.status != 200 && stub.calls != 0 {
				t.Fatal("invalid request reached use case")
			}
		})
	}
}
