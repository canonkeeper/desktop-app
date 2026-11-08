package search

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"canonkeeper/backend/internal/auth"
	"canonkeeper/backend/internal/domain/library"
	"canonkeeper/backend/internal/governance"

	"github.com/go-chi/chi/v5"
)

func TestHandlersSearchProjectReturnsNoStoreSearchResponse(t *testing.T) {
	row := canonicalChapterRowForTest(t, canonicalSearchChapterID, 0.75, 0)
	row.ChapterSource.SourceRevision, row.ChapterNumber = 7, "12"
	row.ParagraphMatch = &ChapterParagraphMatch{ID: canonicalSearchResultID, Position: 18, Content: "private needle"}
	chapterResult, err := searchResultFromRow(SearchRequest{ProjectID: row.ProjectID, SourceView: governance.SourceViewPublished, Query: "private needle"}, row)
	if err != nil {
		t.Fatal(err)
	}
	service := &searchProjectServiceForHandlerTest{
		response: SearchResponse{
			Query: "private needle", Scope: SearchScopeAll,
			TextCoverage: TextCoverage{CurrentBooks: 1, TotalBooks: 1},
			Items: []SearchResult{
				{
					ID: canonicalSearchResultID, Title: "The White Port - A Debt in Ink",
					Excerpt: "private needle appears here", Score: 0.75, Locator: chapterResult.Locator,
				},
			},
		},
	}
	currentUser := &currentUserForSearchHandlerTest{userID: "user-1", csrfToken: "csrf-ok"}
	router := chi.NewRouter()
	NewHandlers(service, currentUser, []string{"https://app.example.test"}).Mount(router)

	req := httptest.NewRequest(http.MethodPost, "/projects/"+canonicalSearchProjectID+"/search", strings.NewReader(`{"query":"private needle","sourceView":"published"}`))
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: "session-1"})
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://app.example.test")
	req.Header.Set("X-CSRF-Token", "csrf-ok")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", rec.Header().Get("Cache-Control"))
	}
	var body struct {
		Query        string       `json:"query"`
		Scope        string       `json:"scope"`
		TextCoverage TextCoverage `json:"textCoverage"`
		Data         []struct {
			ID      string  `json:"id"`
			Kind    string  `json:"kind"`
			Title   string  `json:"title"`
			Excerpt string  `json:"excerpt"`
			Score   float64 `json:"score"`
			Locator struct {
				ProjectID        string                `json:"projectId"`
				BookID           string                `json:"bookId"`
				ChapterID        string                `json:"chapterId"`
				SourceRef        string                `json:"sourceRef"`
				SourceView       governance.SourceView `json:"sourceView"`
				SourceRevision   uint64                `json:"sourceRevision"`
				SourceGeneration governance.Digest     `json:"sourceGeneration"`
				ContentHash      governance.Digest     `json:"contentHash"`
				SourceAnchor     struct {
					Kind    string                `json:"kind"`
					Label   string                `json:"label"`
					Locator library.ReaderLocator `json:"locator"`
				} `json:"sourceAnchor"`
			} `json:"locator"`
		} `json:"data"`
		Meta struct {
			HasMore    bool    `json:"hasMore"`
			NextCursor *string `json:"nextCursor"`
		} `json:"meta"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Query != "private needle" || body.Scope != "all" || body.TextCoverage.CurrentBooks != 1 || body.TextCoverage.TotalBooks != 1 {
		t.Fatalf("response metadata = %#v, want query/all/current coverage", body)
	}
	if len(body.Data) != 1 || body.Data[0].ID != canonicalSearchResultID || body.Data[0].Kind != "chapter" || body.Data[0].Title != "The White Port - A Debt in Ink" {
		t.Fatalf("data = %#v, want chapter search result", body.Data)
	}
	if body.Data[0].Score != 0.75 {
		t.Fatalf("score = %v, want 0.75", body.Data[0].Score)
	}
	if body.Data[0].Locator.SourceRef != canonicalSearchChapterID || body.Data[0].Locator.SourceView != governance.SourceViewPublished {
		t.Fatalf("locator = %#v, want chapter locator", body.Data[0].Locator)
	}
	if body.Data[0].Locator.SourceGeneration != (governance.Digest{1}) || body.Data[0].Locator.ContentHash != (governance.Digest{2}) {
		t.Fatalf("locator digests = %#v, want canonical source identity", body.Data[0].Locator)
	}
	anchor := body.Data[0].Locator.SourceAnchor
	if anchor.Kind != "paragraph" || anchor.Label != "Глава 12 · абзац 18 · опубликованная версия" ||
		anchor.Locator.ParagraphID == nil || *anchor.Locator.ParagraphID != canonicalSearchResultID ||
		anchor.Locator.TargetView != "published" || anchor.Locator.Revision != 7 {
		t.Fatalf("sourceAnchor = %#v, want exact persisted paragraph navigation", anchor)
	}
	if body.Meta.HasMore || body.Meta.NextCursor != nil {
		t.Fatalf("meta = %#v, want no cursor", body.Meta)
	}
	if len(service.calls) != 1 {
		t.Fatalf("service calls = %d, want 1", len(service.calls))
	}
	call := service.calls[0]
	if call.actor.UserID != "user-1" || call.input.ProjectID != canonicalSearchProjectID || call.input.Query != "private needle" ||
		call.input.SourceView != governance.SourceViewPublished || call.input.Scope != SearchScopeAll || call.input.Limit != 10 {
		t.Fatalf("service call = %#v, want actor/project/default search input", call)
	}
}

func TestHandlersSearchProjectValidationDoesNotEchoPrivateQuery(t *testing.T) {
	privateQuery := "PRIVATE-QUERY-SHOULD-NOT-LEAK"
	service := &searchProjectServiceForHandlerTest{}
	currentUser := &currentUserForSearchHandlerTest{userID: "user-1", csrfToken: "csrf-ok"}
	router := chi.NewRouter()
	NewHandlers(service, currentUser, []string{"https://app.example.test"}).Mount(router)

	req := httptest.NewRequest(http.MethodPost, "/projects/"+canonicalSearchProjectID+"/search", strings.NewReader(`{"query":"`+privateQuery+`","sourceView":"published","limit":0}`))
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: "session-1"})
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://app.example.test")
	req.Header.Set("X-CSRF-Token", "csrf-ok")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	assertPrivateQueryNotLeakedBySearchHandler(t, privateQuery, req, rec)
	if len(service.calls) != 0 {
		t.Fatalf("service was called for invalid search request: %#v", service.calls)
	}
}

func TestHandlersSearchProjectRejectsOversizedCursorBeforeService(t *testing.T) {
	oversizedCursor := strings.Repeat("A", searchCursorEncodedLength+1)
	service := &searchProjectServiceForHandlerTest{}
	currentUser := &currentUserForSearchHandlerTest{userID: "user-1", csrfToken: "csrf-ok"}
	router := chi.NewRouter()
	NewHandlers(service, currentUser, []string{"https://app.example.test"}).Mount(router)

	req := httptest.NewRequest(http.MethodPost, "/projects/"+canonicalSearchProjectID+"/search", strings.NewReader(`{"query":"needle","sourceView":"published","cursor":"`+oversizedCursor+`"}`))
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: "session-1"})
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://app.example.test")
	req.Header.Set("X-CSRF-Token", "csrf-ok")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", rec.Header().Get("Cache-Control"))
	}
	assertPrivateValueNotLeakedBySearchResponse(t, oversizedCursor, rec)
	if len(service.calls) != 0 {
		t.Fatalf("service was called for oversized cursor: %#v", service.calls)
	}
}

func TestHandlersSearchProjectRejectsRawQueryBeforeBodyAndService(t *testing.T) {
	for _, tc := range []struct {
		name         string
		target       string
		privateValue string
	}{
		{name: "query text in URL", target: "/projects/project-1/search?query=PRIVATE-QUERY-SHOULD-NOT-LEAK", privateValue: "PRIVATE-QUERY-SHOULD-NOT-LEAK"},
		{name: "unrelated query param", target: "/projects/project-1/search?foo=bar"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service := &searchProjectServiceForHandlerTest{}
			currentUser := &currentUserForSearchHandlerTest{userID: "user-1", csrfToken: "csrf-ok"}
			router := chi.NewRouter()
			NewHandlers(service, currentUser, []string{"https://app.example.test"}).Mount(router)

			req := httptest.NewRequest(http.MethodPost, tc.target, strings.NewReader(`{"query":"body private needle"}`))
			req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: "session-1"})
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Origin", "https://app.example.test")
			req.Header.Set("X-CSRF-Token", "csrf-ok")
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusBadRequest, rec.Body.String())
			}
			if rec.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("Cache-Control = %q, want no-store", rec.Header().Get("Cache-Control"))
			}
			if tc.privateValue != "" {
				assertPrivateValueNotLeakedBySearchResponse(t, tc.privateValue, rec)
			}
			if len(service.calls) != 0 {
				t.Fatalf("service was called for raw query search request: %#v", service.calls)
			}
		})
	}
}

func TestHandlersSearchProjectRejectsUnsupportedContractFieldsBeforeService(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{name: "missing source view", body: `{"query":"needle"}`},
		{name: "unknown field", body: `{"query":"needle","sourceView":"published","sideChannel":"nope"}`},
		{name: "empty query", body: `{"query":"   ","sourceView":"published"}`},
		{name: "too long query", body: `{"query":"` + strings.Repeat("q", 501) + `","sourceView":"published"}`},
		{name: "unknown scope", body: `{"query":"needle","sourceView":"published","scope":"everything"}`},
		{name: "limit too low", body: `{"query":"needle","sourceView":"published","limit":0}`},
		{name: "limit too high", body: `{"query":"needle","sourceView":"published","limit":51}`},
		{name: "empty cursor", body: `{"query":"needle","sourceView":"published","cursor":""}`},
		{name: "unknown result kind", body: `{"query":"needle","sourceView":"published","filters":{"resultKinds":["scene"]}}`},
		{name: "duplicate result kind", body: `{"query":"needle","sourceView":"published","filters":{"resultKinds":["chapter","chapter"]}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service := &searchProjectServiceForHandlerTest{}
			currentUser := &currentUserForSearchHandlerTest{userID: "user-1", csrfToken: "csrf-ok"}
			router := chi.NewRouter()
			NewHandlers(service, currentUser, []string{"https://app.example.test"}).Mount(router)

			req := httptest.NewRequest(http.MethodPost, "/projects/"+canonicalSearchProjectID+"/search", strings.NewReader(tc.body))
			req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: "session-1"})
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Origin", "https://app.example.test")
			req.Header.Set("X-CSRF-Token", "csrf-ok")
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusBadRequest, rec.Body.String())
			}
			if len(service.calls) != 0 {
				t.Fatalf("service was called for invalid request: %#v", service.calls)
			}
		})
	}
}

func TestHandlersSearchProjectRequiresCSRFAndOriginBeforeService(t *testing.T) {
	for _, tc := range []struct {
		name       string
		origin     string
		csrf       string
		wantStatus int
		wantCode   string
	}{
		{name: "invalid origin", origin: "https://evil.example.test", csrf: "csrf-ok", wantStatus: http.StatusForbidden, wantCode: "origin.invalid"},
		{name: "missing csrf", origin: "https://app.example.test", csrf: "", wantStatus: http.StatusForbidden, wantCode: "csrf.required"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service := &searchProjectServiceForHandlerTest{}
			currentUser := &currentUserForSearchHandlerTest{userID: "user-1", csrfToken: "csrf-ok"}
			router := chi.NewRouter()
			NewHandlers(service, currentUser, []string{"https://app.example.test"}).Mount(router)

			req := httptest.NewRequest(http.MethodPost, "/projects/project-1/search", strings.NewReader(`{"query":"needle"}`))
			req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: "session-1"})
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Origin", tc.origin)
			if tc.csrf != "" {
				req.Header.Set("X-CSRF-Token", tc.csrf)
			}
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d; body: %s", rec.Code, tc.wantStatus, rec.Body.String())
			}
			var problem struct {
				Code string `json:"code"`
			}
			if err := json.NewDecoder(rec.Body).Decode(&problem); err != nil {
				t.Fatalf("decode problem: %v", err)
			}
			if problem.Code != tc.wantCode {
				t.Fatalf("problem code = %q, want %q", problem.Code, tc.wantCode)
			}
			if len(service.calls) != 0 {
				t.Fatalf("service was called before security rejection: %#v", service.calls)
			}
		})
	}
}

func TestHandlersSearchProjectMapsServiceErrorsSafely(t *testing.T) {
	service := &searchProjectServiceForHandlerTest{err: errors.New("raw search SHOULD_NOT_LEAK")}
	currentUser := &currentUserForSearchHandlerTest{userID: "user-1", csrfToken: "csrf-ok"}
	router := chi.NewRouter()
	NewHandlers(service, currentUser, []string{"https://app.example.test"}).Mount(router)

	req := httptest.NewRequest(http.MethodPost, "/projects/"+canonicalSearchProjectID+"/search", strings.NewReader(`{"query":"needle","sourceView":"published"}`))
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: "session-1"})
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://app.example.test")
	req.Header.Set("X-CSRF-Token", "csrf-ok")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusInternalServerError, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "SHOULD_NOT_LEAK") {
		t.Fatalf("problem response leaked raw error: %s", rec.Body.String())
	}
}

type searchProjectServiceForHandlerTest struct {
	response SearchResponse
	err      error
	calls    []searchProjectCallForHandlerTest
}

type searchProjectCallForHandlerTest struct {
	actor Actor
	input SearchRequest
}

func (s *searchProjectServiceForHandlerTest) Search(_ context.Context, actor Actor, input SearchRequest) (SearchResponse, error) {
	s.calls = append(s.calls, searchProjectCallForHandlerTest{actor: actor, input: input})
	if s.err != nil {
		return SearchResponse{}, s.err
	}
	return s.response, nil
}

type currentUserForSearchHandlerTest struct {
	userID    string
	csrfToken string
}

func (s *currentUserForSearchHandlerTest) CurrentUser(context.Context, string) (auth.User, error) {
	if s.userID == "" {
		return auth.User{}, auth.ErrSessionNotFound
	}
	return auth.User{ID: s.userID}, nil
}

func (s *currentUserForSearchHandlerTest) ValidateCSRF(_ context.Context, _ string, token string) error {
	if token != s.csrfToken {
		return auth.ErrInvalidCSRF
	}
	return nil
}

func assertPrivateQueryNotLeakedBySearchHandler(t *testing.T, privateQuery string, req *http.Request, rec *httptest.ResponseRecorder) {
	t.Helper()
	if strings.Contains(req.URL.String(), privateQuery) {
		t.Fatalf("request URL leaked private query: %s", req.URL.String())
	}
	assertPrivateValueNotLeakedBySearchResponse(t, privateQuery, rec)
}

func assertPrivateValueNotLeakedBySearchResponse(t *testing.T, privateQuery string, rec *httptest.ResponseRecorder) {
	t.Helper()
	if strings.Contains(rec.Body.String(), privateQuery) {
		t.Fatalf("problem response leaked private query: %s", rec.Body.String())
	}
	for name, values := range rec.Header() {
		for _, value := range values {
			if strings.Contains(value, privateQuery) {
				t.Fatalf("response header %s leaked private query: %q", name, value)
			}
		}
	}
}
