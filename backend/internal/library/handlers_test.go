package library

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"

	"canonkeeper/backend/internal/auth"
	"canonkeeper/backend/internal/projectaccess"
	"canonkeeper/backend/internal/projects"
)

func TestImportJobInputRejectsInitialPublicationOption(t *testing.T) {
	const payload = "<FictionBook>publication boundary</FictionBook>"
	for name, fields := range map[string]map[string]string{
		"dotted option": {"options.initialPublication": "draft_only"},
		"JSON option":   {"options": `{"initialPublication":"draft_only"}`},
	} {
		t.Run(name, func(t *testing.T) {
			req := newMultipartImportRequestWithFieldsForTest(t, "/projects/project-1/imports", "story.fb2", "application/x-fictionbook+xml", payload, fields)
			if _, err := importJobInputFromRequest(httptest.NewRecorder(), req); !errors.Is(err, ErrValidation) {
				t.Fatalf("importJobInputFromRequest error = %v, want ErrValidation", err)
			}
		})
	}
	req := newMultipartImportRequestWithFieldsForTest(t, "/projects/project-1/imports", "story.fb2", "application/x-fictionbook+xml", payload, map[string]string{"options": `{"importMode":"new_book"}`})
	if _, err := importJobInputFromRequest(httptest.NewRecorder(), req); err != nil {
		t.Fatalf("importJobInputFromRequest error = %v", err)
	}
}

func TestLibraryCSRFMiddlewareAuthenticatesSessionBeforeCheckingCSRF(t *testing.T) {
	infrastructureErr := errors.New("session store unavailable")
	tests := []struct {
		name            string
		cookieValue     string
		csrfToken       string
		authenticateErr error
		csrfErr         error
		wantStatus      int
		wantCode        string
	}{
		{name: "missing session without CSRF", authenticateErr: auth.ErrUnauthenticated, wantStatus: http.StatusUnauthorized, wantCode: "auth.session_required"},
		{name: "missing session with CSRF", csrfToken: "csrf-ok", authenticateErr: auth.ErrUnauthenticated, wantStatus: http.StatusUnauthorized, wantCode: "auth.session_required"},
		{name: "expired session", cookieValue: "expired-session", csrfToken: "csrf-ok", authenticateErr: auth.ErrUnauthenticated, wantStatus: http.StatusUnauthorized, wantCode: "auth.session_required"},
		{name: "revoked session", cookieValue: "revoked-session", csrfToken: "csrf-ok", authenticateErr: auth.ErrUnauthenticated, wantStatus: http.StatusUnauthorized, wantCode: "auth.session_required"},
		{name: "valid session without CSRF", cookieValue: "active-session", csrfErr: auth.ErrInvalidCSRF, wantStatus: http.StatusForbidden, wantCode: "csrf.required"},
		{name: "valid session with invalid CSRF", cookieValue: "active-session", csrfToken: "csrf-wrong", csrfErr: auth.ErrInvalidCSRF, wantStatus: http.StatusForbidden, wantCode: "csrf.invalid"},
		{name: "authentication infrastructure error", cookieValue: "active-session", csrfToken: "csrf-ok", authenticateErr: infrastructureErr, wantStatus: http.StatusInternalServerError, wantCode: "server.error"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			currentUser := fakeImportAuthForTest("user-1")
			currentUser.currentUserErr = tt.authenticateErr
			currentUser.validateErr = tt.csrfErr
			nextCalled := false
			handler := (Handlers{currentUser: currentUser}).CSRFMiddleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				nextCalled = true
			}))
			req := httptest.NewRequest(http.MethodPost, "/projects/project-1/books", nil)
			if tt.cookieValue != "" {
				req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: tt.cookieValue})
			}
			if tt.csrfToken != "" {
				req.Header.Set("X-CSRF-Token", tt.csrfToken)
			}
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body: %s", rec.Code, tt.wantStatus, rec.Body.String())
			}
			var problem struct {
				Code string `json:"code"`
			}
			if err := json.NewDecoder(rec.Body).Decode(&problem); err != nil {
				t.Fatalf("decode problem response: %v", err)
			}
			if problem.Code != tt.wantCode {
				t.Fatalf("problem code = %q, want %q", problem.Code, tt.wantCode)
			}
			if nextCalled {
				t.Fatal("next handler was called after authentication rejection")
			}
		})
	}
}

func TestLibraryHandlerCreatesBook(t *testing.T) {
	store := NewMemoryStoreForTest()
	store.projectRoles[roleKey("user-1", "project-1")] = projects.RoleEditor
	handler := newImportHandlerForTest(NewService(store, StaticIDsForTest(), StaticClockForTest()), fakeImportAuthForTest("user-1"))
	req := newAuthenticatedImportRequestForTest(t, http.MethodPost, "/projects/project-1/books", `{"title":"Smoke Book"}`)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", importAllowedOriginForTest)
	req.Header.Set("X-CSRF-Token", "csrf-for-test")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusCreated, rec.Body.String())
	}
	var body struct {
		ID           string `json:"id"`
		ProjectID    string `json:"projectId"`
		Title        string `json:"title"`
		Order        int    `json:"order"`
		Status       string `json:"status"`
		ChapterCount int    `json:"chapterCount"`
		WordCount    int    `json:"wordCount"`
		Indexing     struct {
			Status string `json:"status"`
		} `json:"indexing"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode book response: %v", err)
	}
	if body.ID != "book_generated_1" || body.ProjectID != "project-1" || body.Title != "Smoke Book" || body.Order != 1 || body.Status != string(BookStatusDraft) {
		t.Fatalf("book response = %#v, want created draft book", body)
	}
	if body.ChapterCount != 0 || body.WordCount != 0 {
		t.Fatalf("created book summary = chapterCount %d wordCount %d, want zero counts", body.ChapterCount, body.WordCount)
	}
}

func TestLibraryHandlerCreateAndUpdateBookHonorAfterBookId(t *testing.T) {
	store := NewMemoryStoreForTest()
	store.SeedBook("project-1", "book-1")
	store.SeedBook("project-1", "book-2")
	store.projectRoles[roleKey("user-1", "project-1")] = projects.RoleEditor
	handler := newImportHandlerForTest(NewService(store, StaticIDsForTest(), StaticClockForTest()), fakeImportAuthForTest("user-1"))

	createReq := newAuthenticatedImportRequestForTest(t, http.MethodPost, "/projects/project-1/books", `{"title":"Inserted","afterBookId":"book-1"}`)
	createReq.Header.Set("Content-Type", "application/json")
	createReq.Header.Set("Origin", importAllowedOriginForTest)
	createReq.Header.Set("X-CSRF-Token", "csrf-for-test")
	createRec := httptest.NewRecorder()
	handler.ServeHTTP(createRec, createReq)

	if createRec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want %d; body: %s", createRec.Code, http.StatusCreated, createRec.Body.String())
	}
	var created struct {
		ID    string `json:"id"`
		Order int    `json:"order"`
	}
	if err := json.NewDecoder(createRec.Body).Decode(&created); err != nil {
		t.Fatalf("decode create book response: %v", err)
	}
	if created.ID != "book_generated_1" || created.Order != 2 {
		t.Fatalf("created book = %#v, want generated book at order 2", created)
	}
	assertBookOrderForTest(t, store, "project-1", []string{"book-1", "book_generated_1", "book-2"})

	patchReq := newAuthenticatedImportRequestForTest(t, http.MethodPatch, "/books/book-2", `{"afterBookId":null}`)
	patchReq.Header.Set("Content-Type", "application/json")
	patchReq.Header.Set("Origin", importAllowedOriginForTest)
	patchReq.Header.Set("X-CSRF-Token", "csrf-for-test")
	patchRec := httptest.NewRecorder()
	handler.ServeHTTP(patchRec, patchReq)

	if patchRec.Code != http.StatusOK {
		t.Fatalf("patch status = %d, want %d; body: %s", patchRec.Code, http.StatusOK, patchRec.Body.String())
	}
	var patched struct {
		ID    string `json:"id"`
		Order int    `json:"order"`
	}
	if err := json.NewDecoder(patchRec.Body).Decode(&patched); err != nil {
		t.Fatalf("decode patch book response: %v", err)
	}
	if patched.ID != "book-2" || patched.Order != 1 {
		t.Fatalf("patched book = %#v, want book-2 moved to order 1", patched)
	}
	assertBookOrderForTest(t, store, "project-1", []string{"book-2", "book-1", "book_generated_1"})
}

func TestLibraryHandlerBookResponsesIncludeChapterCountAndWordCount(t *testing.T) {
	store := NewMemoryStoreForTest()
	store.SeedChapter("project-1", "book-1", []Paragraph{{ID: "paragraph-1", Markdown: "Alpha beta"}})
	store.SeedChapter("project-1", "book-1", []Paragraph{{ID: "paragraph-2", Markdown: "Gamma delta epsilon"}})
	store.projectRoles[roleKey("user-1", "project-1")] = projects.RoleEditor
	handler := newImportHandlerForTest(NewService(store, StaticIDsForTest(), StaticClockForTest()), fakeImportAuthForTest("user-1"))

	getReq := newAuthenticatedImportRequestForTest(t, http.MethodGet, "/books/book-1", "")
	getRec := httptest.NewRecorder()
	handler.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("get book status = %d, want %d; body: %s", getRec.Code, http.StatusOK, getRec.Body.String())
	}
	var got struct {
		ChapterCount int `json:"chapterCount"`
		WordCount    int `json:"wordCount"`
	}
	if err := json.NewDecoder(getRec.Body).Decode(&got); err != nil {
		t.Fatalf("decode get book response: %v", err)
	}
	if got.ChapterCount != 2 || got.WordCount != 5 {
		t.Fatalf("get book counts = chapterCount %d wordCount %d, want 2/5", got.ChapterCount, got.WordCount)
	}

	listReq := newAuthenticatedImportRequestForTest(t, http.MethodGet, "/projects/project-1/books", "")
	listRec := httptest.NewRecorder()
	handler.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list books status = %d, want %d; body: %s", listRec.Code, http.StatusOK, listRec.Body.String())
	}
	var listed struct {
		Data []struct {
			ID           string `json:"id"`
			ChapterCount int    `json:"chapterCount"`
			WordCount    int    `json:"wordCount"`
		} `json:"data"`
	}
	if err := json.NewDecoder(listRec.Body).Decode(&listed); err != nil {
		t.Fatalf("decode list books response: %v", err)
	}
	if len(listed.Data) != 1 || listed.Data[0].ID != "book-1" || listed.Data[0].ChapterCount != 2 || listed.Data[0].WordCount != 5 {
		t.Fatalf("list book counts = %#v, want book-1 with counts 2/5", listed.Data)
	}

	patchReq := newAuthenticatedImportRequestForTest(t, http.MethodPatch, "/books/book-1", `{"title":"Counted Book"}`)
	patchReq.Header.Set("Content-Type", "application/json")
	patchReq.Header.Set("Origin", importAllowedOriginForTest)
	patchReq.Header.Set("X-CSRF-Token", "csrf-for-test")
	patchRec := httptest.NewRecorder()
	handler.ServeHTTP(patchRec, patchReq)
	if patchRec.Code != http.StatusOK {
		t.Fatalf("patch book status = %d, want %d; body: %s", patchRec.Code, http.StatusOK, patchRec.Body.String())
	}
	var patched struct {
		ChapterCount int `json:"chapterCount"`
		WordCount    int `json:"wordCount"`
	}
	if err := json.NewDecoder(patchRec.Body).Decode(&patched); err != nil {
		t.Fatalf("decode patch book response: %v", err)
	}
	if patched.ChapterCount != 2 || patched.WordCount != 5 {
		t.Fatalf("patch book counts = chapterCount %d wordCount %d, want 2/5", patched.ChapterCount, patched.WordCount)
	}
}

func TestBookResponseFromBookAlwaysSerializesCanonicalCoverURL(t *testing.T) {
	coverURL := "/api/v1/books/book-1/cover/3"
	for name, book := range map[string]Book{
		"missing": {ID: "book-1"},
		"active":  {ID: "book-1", CoverURL: &coverURL},
	} {
		t.Run(name, func(t *testing.T) {
			encoded, err := json.Marshal(BookResponseFromBook(book))
			if err != nil {
				t.Fatal(err)
			}
			var response map[string]json.RawMessage
			if err := json.Unmarshal(encoded, &response); err != nil {
				t.Fatal(err)
			}
			want := "null"
			if book.CoverURL != nil {
				want = `"` + coverURL + `"`
			}
			if got := string(response["coverUrl"]); got != want {
				t.Fatalf("coverUrl = %s, want %s; response=%s", got, want, encoded)
			}
		})
	}
}

func TestChapterResponseReadingTimeUsesWordCount(t *testing.T) {
	tests := []struct {
		wordCount int
		want      int
	}{
		{wordCount: 0, want: 0},
		{wordCount: 1, want: 1},
		{wordCount: 220, want: 1},
		{wordCount: 221, want: 2},
	}

	for _, tt := range tests {
		t.Run(strconv.Itoa(tt.wordCount)+" words", func(t *testing.T) {
			response := chapterResponseFromContent(ChapterContent{WordCount: tt.wordCount})
			if response.Navigation.ReadingTimeMinutes != tt.want {
				t.Fatalf("reading time = %d, want %d", response.Navigation.ReadingTimeMinutes, tt.want)
			}
		})
	}
}

func TestLibraryHandlerCreateBookRejectsInvalidAfterBookId(t *testing.T) {
	store := NewMemoryStoreForTest()
	store.projectRoles[roleKey("user-1", "project-1")] = projects.RoleEditor
	handler := newImportHandlerForTest(NewService(store, StaticIDsForTest(), StaticClockForTest()), fakeImportAuthForTest("user-1"))
	req := newAuthenticatedImportRequestForTest(t, http.MethodPost, "/projects/project-1/books", `{"title":"Bad Anchor","afterBookId":"missing-book"}`)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", importAllowedOriginForTest)
	req.Header.Set("X-CSRF-Token", "csrf-for-test")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assertImportProblemForTest(t, rec, http.StatusNotFound, "not_found")
	if len(store.books) != 0 {
		t.Fatalf("invalid anchor persisted %d books", len(store.books))
	}
}

func TestLibraryHandlerUpdateBookRejectsInvalidAfterBookId(t *testing.T) {
	store := NewMemoryStoreForTest()
	store.SeedBook("project-1", "book-1")
	store.projectRoles[roleKey("user-1", "project-1")] = projects.RoleEditor
	handler := newImportHandlerForTest(NewService(store, StaticIDsForTest(), StaticClockForTest()), fakeImportAuthForTest("user-1"))
	req := newAuthenticatedImportRequestForTest(t, http.MethodPatch, "/books/book-1", `{"afterBookId":"missing-book"}`)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", importAllowedOriginForTest)
	req.Header.Set("X-CSRF-Token", "csrf-for-test")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assertImportProblemForTest(t, rec, http.StatusNotFound, "not_found")
	assertBookOrderForTest(t, store, "project-1", []string{"book-1"})
}

func TestLibraryHandlerGetsChapterWithBookNavigation(t *testing.T) {
	store := NewMemoryStoreForTest()
	firstChapterID := store.SeedChapter("project-1", "book-1", []Paragraph{{ID: "paragraph-existing", Markdown: "Existing chapter"}})
	secondChapterID := store.SeedChapter("project-1", "book-1", []Paragraph{{ID: "paragraph-smoke", Markdown: "Searchable smoke marker"}})
	store.projectRoles[roleKey("user-1", "project-1")] = projects.RoleEditor
	handler := newImportHandlerForTest(NewService(store, StaticIDsForTest(), StaticClockForTest()), fakeImportAuthForTest("user-1"))

	getReq := newAuthenticatedImportRequestForTest(t, http.MethodGet, "/chapters/"+secondChapterID, "")
	getRec := httptest.NewRecorder()

	handler.ServeHTTP(getRec, getReq)

	if getRec.Code != http.StatusOK {
		t.Fatalf("get status = %d, want %d; body: %s", getRec.Code, http.StatusOK, getRec.Body.String())
	}
	var got struct {
		ID         string                    `json:"id"`
		BookID     string                    `json:"bookId"`
		Navigation chapterNavigationResponse `json:"navigation"`
		Paragraphs []struct {
			Text string `json:"text"`
		} `json:"paragraphs"`
	}
	if err := json.NewDecoder(getRec.Body).Decode(&got); err != nil {
		t.Fatalf("decode get chapter response: %v", err)
	}
	if got.ID != secondChapterID || got.BookID != "book-1" || len(got.Paragraphs) != 1 || got.Paragraphs[0].Text != "Searchable smoke marker" {
		t.Fatalf("chapter response = %#v, want seeded chapter content", got)
	}
	if got.Navigation.Position != 2 || got.Navigation.Total != 2 || got.Navigation.Previous == nil || got.Navigation.Previous.ID != firstChapterID || got.Navigation.Next != nil {
		t.Fatalf("chapter navigation = %#v, want second-chapter navigation", got.Navigation)
	}
}

func TestLibraryHandlerSelectsExactChapterVariantAndConcealsDraftFromViewer(t *testing.T) {
	store := NewMemoryStoreForTest()
	chapterID := store.SeedChapter("project-1", "book-1", []Paragraph{{ID: "paragraph-1", Markdown: "Published snapshot"}})
	store.projectRoles[roleKey("user-1", "project-1")] = projects.RoleEditor
	service := NewService(store, StaticIDsForTest(), StaticClockForTest())
	store.SeedPublishedRevision(chapterID)
	store.SeedDraftRevision(chapterID, []Paragraph{{ID: "paragraph-1", Order: 1, Kind: ParagraphKindParagraph, Markdown: "Draft snapshot"}})

	handler := newImportHandlerForTest(service, fakeImportAuthForTest("user-1"))
	type chapterVariantResponse struct {
		ContentVariant ParagraphVariant `json:"contentVariant"`
		Revision       int64            `json:"revision"`
		DraftRevision  int64            `json:"draftRevision"`
		Paragraphs     []struct {
			Text string `json:"text"`
		} `json:"paragraphs"`
	}
	read := func(variant string) (int, chapterVariantResponse) {
		t.Helper()
		req := newAuthenticatedImportRequestForTest(t, http.MethodGet, "/chapters/"+chapterID+"?contentVariant="+variant, "")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		var body chapterVariantResponse
		if rec.Code == http.StatusOK {
			if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
				t.Fatalf("decode %s chapter: %v", variant, err)
			}
		}
		return rec.Code, body
	}

	publishedStatus, published := read("published")
	draftStatus, draft := read("draft")
	if publishedStatus != http.StatusOK || published.ContentVariant != ParagraphVariantPublished ||
		published.Revision != 1 || published.DraftRevision != 2 || len(published.Paragraphs) != 1 || published.Paragraphs[0].Text != "Published snapshot" {
		t.Fatalf("published response = status %d, body %#v", publishedStatus, published)
	}
	if draftStatus != http.StatusOK || draft.ContentVariant != ParagraphVariantDraft ||
		draft.Revision != 2 || draft.DraftRevision != 2 || len(draft.Paragraphs) != 1 || draft.Paragraphs[0].Text != "Draft snapshot" {
		t.Fatalf("draft response = status %d, body %#v", draftStatus, draft)
	}

	store.projectRoles[roleKey("user-1", "project-1")] = projects.RoleViewer
	if status, _ := read("draft"); status != http.StatusNotFound {
		t.Fatalf("viewer draft status = %d, want %d", status, http.StatusNotFound)
	}
	if status, body := read("published"); status != http.StatusOK || body.ContentVariant != ParagraphVariantPublished || body.Revision != 1 {
		t.Fatalf("viewer published response = status %d, body %#v", status, body)
	}
}

// A page of chapters names the next one, and its cursor reads it (specs/2026-11-07-chapter-list-paging.md, P1.4).
func TestLibraryHandlerPagesChaptersByCursor(t *testing.T) {
	store := NewMemoryStoreForTest()
	firstID := store.SeedChapter("project-1", "book-1", []Paragraph{{ID: "paragraph-1", Markdown: "First"}})
	secondID := store.SeedChapter("project-1", "book-1", []Paragraph{{ID: "paragraph-2", Markdown: "Second"}})
	store.projectRoles[roleKey("user-1", "project-1")] = projects.RoleViewer
	handler := newImportHandlerForTest(NewService(store, StaticIDsForTest(), StaticClockForTest()), fakeImportAuthForTest("user-1"))
	read := func(target string) (int, []string, pageMeta) {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, newAuthenticatedImportRequestForTest(t, http.MethodGet, target, ""))
		var body struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
			Meta pageMeta `json:"meta"`
		}
		_ = json.NewDecoder(rec.Body).Decode(&body)
		ids := make([]string, 0, len(body.Data))
		for _, item := range body.Data {
			ids = append(ids, item.ID)
		}
		return rec.Code, ids, body.Meta
	}

	status, ids, meta := read("/books/book-1/chapters?limit=1")
	if status != http.StatusOK || len(ids) != 1 || ids[0] != firstID || !meta.HasMore || meta.NextCursor == nil {
		t.Fatalf("first page = %d %v %#v, want the first chapter and a cursor", status, ids, meta)
	}
	status, ids, meta = read("/books/book-1/chapters?limit=1&cursor=" + url.QueryEscape(*meta.NextCursor))
	if status != http.StatusOK || len(ids) != 1 || ids[0] != secondID || meta.HasMore || meta.NextCursor != nil {
		t.Fatalf("second page = %d %v %#v, want the last chapter without a cursor", status, ids, meta)
	}
	if status, _, _ := read("/books/book-1/chapters?cursor=broken"); status != http.StatusBadRequest {
		t.Fatalf("broken cursor status = %d, want %d", status, http.StatusBadRequest)
	}
}

func TestLibraryHandlerListsChaptersForBook(t *testing.T) {
	store := NewMemoryStoreForTest()
	firstID := store.SeedChapter("project-1", "book-1", []Paragraph{{ID: "paragraph-1", Markdown: "First chapter text"}})
	secondID := store.SeedChapter("project-1", "book-1", []Paragraph{{ID: "paragraph-2", Markdown: "Second chapter has words"}})
	store.projectRoles[roleKey("user-1", "project-1")] = projects.RoleViewer
	handler := newImportHandlerForTest(NewService(store, StaticIDsForTest(), StaticClockForTest()), fakeImportAuthForTest("user-1"))
	req := newAuthenticatedImportRequestForTest(t, http.MethodGet, "/books/book-1/chapters", "")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var body struct {
		Data []struct {
			ID            string        `json:"id"`
			BookID        string        `json:"bookId"`
			Title         string        `json:"title"`
			Order         int           `json:"order"`
			DisplayNumber string        `json:"displayNumber"`
			Status        ChapterStatus `json:"status"`
			WordCount     int           `json:"wordCount"`
			HasDraft      bool          `json:"hasDraft"`
		} `json:"data"`
		Meta pageMeta `json:"meta"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode chapter list response: %v", err)
	}
	if len(body.Data) != 2 {
		t.Fatalf("chapter list count = %d, want 2: %#v", len(body.Data), body.Data)
	}
	if body.Data[0].ID != firstID || body.Data[1].ID != secondID {
		t.Fatalf("chapter order = %#v, want %s then %s", body.Data, firstID, secondID)
	}
	if body.Data[0].BookID != "book-1" || body.Data[0].DisplayNumber != "1" || body.Data[0].WordCount != 3 || !body.Data[0].HasDraft {
		t.Fatalf("first chapter summary = %#v, want contract fields", body.Data[0])
	}
	if body.Meta.HasMore || body.Meta.NextCursor != nil {
		t.Fatalf("chapter list meta = %#v, want no next page", body.Meta)
	}
}

func TestLibraryHandlerBookRoutesGetUpdateAndDelete(t *testing.T) {
	store := NewMemoryStoreForTest()
	store.SeedBook("project-1", "book-1")
	store.projectRoles[roleKey("user-1", "project-1")] = projects.RoleEditor
	handler := newImportHandlerForTest(NewService(store, StaticIDsForTest(), StaticClockForTest()), fakeImportAuthForTest("user-1"))

	getReq := newAuthenticatedImportRequestForTest(t, http.MethodGet, "/books/book-1", "")
	getRec := httptest.NewRecorder()
	handler.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("get book status = %d, want %d; body: %s", getRec.Code, http.StatusOK, getRec.Body.String())
	}

	patchReq := newAuthenticatedImportRequestForTest(t, http.MethodPatch, "/books/book-1", `{"title":"Updated Book","subtitle":"Revised","coverColor":"#123ABC"}`)
	patchReq.Header.Set("Content-Type", "application/json")
	patchReq.Header.Set("Origin", importAllowedOriginForTest)
	patchReq.Header.Set("X-CSRF-Token", "csrf-for-test")
	patchRec := httptest.NewRecorder()
	handler.ServeHTTP(patchRec, patchReq)
	if patchRec.Code != http.StatusOK {
		t.Fatalf("patch book status = %d, want %d; body: %s", patchRec.Code, http.StatusOK, patchRec.Body.String())
	}
	var patched struct {
		ID         string  `json:"id"`
		Title      string  `json:"title"`
		Subtitle   *string `json:"subtitle"`
		CoverColor *string `json:"coverColor"`
	}
	if err := json.NewDecoder(patchRec.Body).Decode(&patched); err != nil {
		t.Fatalf("decode patch book response: %v", err)
	}
	if patched.ID != "book-1" || patched.Title != "Updated Book" || patched.Subtitle == nil || *patched.Subtitle != "Revised" || patched.CoverColor == nil || *patched.CoverColor != "#123ABC" {
		t.Fatalf("patched book response = %#v, want updated metadata", patched)
	}

	deleteReq := newAuthenticatedImportRequestForTest(t, http.MethodDelete, "/books/book-1", "")
	deleteReq.Header.Set("Origin", importAllowedOriginForTest)
	deleteReq.Header.Set("X-CSRF-Token", "csrf-for-test")
	deleteRec := httptest.NewRecorder()
	handler.ServeHTTP(deleteRec, deleteReq)
	if deleteRec.Code != http.StatusNoContent {
		t.Fatalf("delete book status = %d, want %d; body: %s", deleteRec.Code, http.StatusNoContent, deleteRec.Body.String())
	}
}

func TestLibraryHandlerListsDeletedBooksAndRestoresWithCSRF(t *testing.T) {
	store := NewMemoryStoreForTest()
	store.SeedBook("project-1", "book-1")
	store.projectRoles[roleKey("user-1", "project-1")] = projects.RoleEditor
	deletedAt := staticTestTime()
	store.deletedBooks["book-1"] = struct{}{}
	store.deletedBookAt["book-1"] = deletedAt
	authService := fakeImportAuthForTest("user-1")
	handler := newImportHandlerForTest(NewService(store, StaticIDsForTest(), StaticClockForTest()), authService)

	listReq := newAuthenticatedImportRequestForTest(t, http.MethodGet, "/projects/project-1/books?state=deleted", "")
	listRec := httptest.NewRecorder()
	handler.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list deleted books status = %d, want 200; body: %s", listRec.Code, listRec.Body.String())
	}
	payload := listRec.Body.Bytes()
	var exactListBody struct {
		Data []map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(payload, &exactListBody); err != nil {
		t.Fatal(err)
	}
	assertDeletedSourceRowKeysForTest(t, exactListBody.Data, []string{"id", "title", "deletedAt", "restoreUntil"})

	var listBody struct {
		Data []struct {
			ID           string  `json:"id"`
			DeletedAt    *string `json:"deletedAt"`
			RestoreUntil *string `json:"restoreUntil"`
		} `json:"data"`
	}
	if err := json.Unmarshal(payload, &listBody); err != nil {
		t.Fatal(err)
	}
	if len(listBody.Data) != 1 || listBody.Data[0].ID != "book-1" || listBody.Data[0].DeletedAt == nil ||
		*listBody.Data[0].DeletedAt != "2026-06-24T12:00:00.000Z" || listBody.Data[0].RestoreUntil == nil ||
		*listBody.Data[0].RestoreUntil != "2026-07-01T12:00:00.000Z" {
		t.Fatalf("deleted book response = %#v", listBody.Data)
	}

	invalidReq := newAuthenticatedImportRequestForTest(t, http.MethodGet, "/projects/project-1/books?state=archived", "")
	invalidRec := httptest.NewRecorder()
	handler.ServeHTTP(invalidRec, invalidReq)
	if invalidRec.Code != http.StatusBadRequest {
		t.Fatalf("invalid state status = %d, want 400; body: %s", invalidRec.Code, invalidRec.Body.String())
	}

	missingCSRFReq := newAuthenticatedImportRequestForTest(t, http.MethodPost, "/books/book-1/restore", "")
	missingCSRFReq.Header.Set("Origin", importAllowedOriginForTest)
	missingCSRFRec := httptest.NewRecorder()
	authService.validateErr = auth.ErrInvalidCSRF
	handler.ServeHTTP(missingCSRFRec, missingCSRFReq)
	if missingCSRFRec.Code != http.StatusForbidden {
		t.Fatalf("restore without CSRF status = %d, want 403; body: %s", missingCSRFRec.Code, missingCSRFRec.Body.String())
	}
	authService.validateErr = nil

	restoreReq := newAuthenticatedImportRequestForTest(t, http.MethodPost, "/books/book-1/restore", "")
	restoreReq.Header.Set("Origin", importAllowedOriginForTest)
	restoreReq.Header.Set("X-CSRF-Token", "csrf-for-test")
	restoreRec := httptest.NewRecorder()
	handler.ServeHTTP(restoreRec, restoreReq)
	if restoreRec.Code != http.StatusOK {
		t.Fatalf("restore book status = %d, want 200; body: %s", restoreRec.Code, restoreRec.Body.String())
	}
	var restored bookResponse
	if err := json.NewDecoder(restoreRec.Body).Decode(&restored); err != nil {
		t.Fatal(err)
	}
	if restored.ID != "book-1" || restored.DeletedAt != nil || restored.RestoreUntil != nil {
		t.Fatalf("restored book response = %#v", restored)
	}
}

func TestLibraryHandlerDeletesChapter(t *testing.T) {
	store := NewMemoryStoreForTest()
	chapterID := store.SeedChapter("project-1", "book-1", []Paragraph{{ID: "paragraph-1", Markdown: "Old draft"}})
	store.SeedChapter("project-1", "book-1", []Paragraph{{ID: "paragraph-2", Markdown: "Next draft"}})
	store.projectRoles[roleKey("user-1", "project-1")] = projects.RoleEditor
	handler := newImportHandlerForTest(NewService(store, StaticIDsForTest(), StaticClockForTest()), fakeImportAuthForTest("user-1"))

	deleteReq := newAuthenticatedImportRequestForTest(t, http.MethodDelete, "/chapters/"+chapterID, "")
	deleteReq.Header.Set("Origin", importAllowedOriginForTest)
	deleteReq.Header.Set("X-CSRF-Token", "csrf-for-test")
	deleteRec := httptest.NewRecorder()
	handler.ServeHTTP(deleteRec, deleteReq)
	if deleteRec.Code != http.StatusNoContent {
		t.Fatalf("delete chapter status = %d, want %d; body: %s", deleteRec.Code, http.StatusNoContent, deleteRec.Body.String())
	}
}

func TestLibraryHandlerListsDeletedChaptersAndRestoresWithCSRF(t *testing.T) {
	store := NewMemoryStoreForTest()
	chapterID := store.SeedChapter("project-1", "book-1", nil)
	store.projectRoles[roleKey("user-1", "project-1")] = projects.RoleEditor
	deletedAt := staticTestTime()
	store.deletedChapters[chapterID] = struct{}{}
	store.deletedChapterAt[chapterID] = deletedAt
	authService := fakeImportAuthForTest("user-1")
	handler := newImportHandlerForTest(NewService(store, StaticIDsForTest(), StaticClockForTest()), authService)

	listReq := newAuthenticatedImportRequestForTest(t, http.MethodGet, "/books/book-1/chapters?state=deleted", "")
	listRec := httptest.NewRecorder()
	handler.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list deleted chapters status = %d, want 200; body: %s", listRec.Code, listRec.Body.String())
	}
	payload := listRec.Body.Bytes()
	var exactListBody struct {
		Data []map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(payload, &exactListBody); err != nil {
		t.Fatal(err)
	}
	assertDeletedSourceRowKeysForTest(t, exactListBody.Data, []string{"id", "title", "deletedAt", "restoreUntil"})

	var listBody struct {
		Data []struct {
			ID           string  `json:"id"`
			DeletedAt    *string `json:"deletedAt"`
			RestoreUntil *string `json:"restoreUntil"`
		} `json:"data"`
	}
	if err := json.Unmarshal(payload, &listBody); err != nil {
		t.Fatal(err)
	}
	if len(listBody.Data) != 1 || listBody.Data[0].ID != chapterID || listBody.Data[0].DeletedAt == nil ||
		*listBody.Data[0].DeletedAt != "2026-06-24T12:00:00.000Z" || listBody.Data[0].RestoreUntil == nil ||
		*listBody.Data[0].RestoreUntil != "2026-07-01T12:00:00.000Z" {
		t.Fatalf("deleted chapter response = %#v", listBody.Data)
	}

	missingCSRFReq := newAuthenticatedImportRequestForTest(t, http.MethodPost, "/chapters/"+chapterID+"/restore", "")
	missingCSRFReq.Header.Set("Origin", importAllowedOriginForTest)
	missingCSRFRec := httptest.NewRecorder()
	authService.validateErr = auth.ErrInvalidCSRF
	handler.ServeHTTP(missingCSRFRec, missingCSRFReq)
	if missingCSRFRec.Code != http.StatusForbidden {
		t.Fatalf("restore chapter without CSRF status = %d, want 403; body: %s", missingCSRFRec.Code, missingCSRFRec.Body.String())
	}
	authService.validateErr = nil

	restoreReq := newAuthenticatedImportRequestForTest(t, http.MethodPost, "/chapters/"+chapterID+"/restore", "")
	restoreReq.Header.Set("Origin", importAllowedOriginForTest)
	restoreReq.Header.Set("X-CSRF-Token", "csrf-for-test")
	restoreRec := httptest.NewRecorder()
	handler.ServeHTTP(restoreRec, restoreReq)
	if restoreRec.Code != http.StatusOK {
		t.Fatalf("restore chapter status = %d, want 200; body: %s", restoreRec.Code, restoreRec.Body.String())
	}
	var restored struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(restoreRec.Body).Decode(&restored); err != nil {
		t.Fatal(err)
	}
	if restored.ID != chapterID {
		t.Fatalf("restored chapter = %#v", restored)
	}
}

func TestLibraryRestoreHandlersPreserveAuthorizationLifecycleOrder(t *testing.T) {
	tests := []struct {
		name      string
		role      projects.Role
		status    int
		code      string
		lifecycle projectaccess.DeletedSourceLifecycle
		chapter   bool
	}{
		{name: "book non-member concealed", status: http.StatusNotFound, code: "not_found", lifecycle: projectaccess.DeletedSourceExpired},
		{name: "book viewer forbidden before expiry", role: projects.RoleViewer, status: http.StatusForbidden, code: "forbidden", lifecycle: projectaccess.DeletedSourceExpired},
		{name: "book editor sees expiry conflict", role: projects.RoleEditor, status: http.StatusConflict, code: "conflict", lifecycle: projectaccess.DeletedSourceExpired},
		{name: "chapter non-member concealed", status: http.StatusNotFound, code: "not_found", lifecycle: projectaccess.DeletedSourceParentDeleted, chapter: true},
		{name: "chapter viewer forbidden before parent state", role: projects.RoleViewer, status: http.StatusForbidden, code: "forbidden", lifecycle: projectaccess.DeletedSourceParentDeleted, chapter: true},
		{name: "chapter editor sees parent conflict", role: projects.RoleEditor, status: http.StatusConflict, code: "conflict", lifecycle: projectaccess.DeletedSourceParentDeleted, chapter: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewMemoryStoreForTest()
			route := "/books/book-1/restore"
			if tt.chapter {
				chapterID := store.SeedChapter("project-1", "book-1", nil)
				store.deletedChapters[chapterID] = struct{}{}
				store.deletedChapterLifecycle[chapterID] = tt.lifecycle
				route = "/chapters/" + chapterID + "/restore"
			} else {
				store.SeedBook("project-1", "book-1")
				store.deletedBooks["book-1"] = struct{}{}
				store.deletedBookLifecycle["book-1"] = tt.lifecycle
			}
			if tt.role != "" {
				store.projectRoles[roleKey("user-1", "project-1")] = tt.role
			}
			handler := newImportHandlerForTest(NewService(store, StaticIDsForTest(), StaticClockForTest()), fakeImportAuthForTest("user-1"))
			request := newAuthenticatedImportRequestForTest(t, http.MethodPost, route, "")
			request.Header.Set("Origin", importAllowedOriginForTest)
			request.Header.Set("X-CSRF-Token", "csrf-for-test")
			recorder := httptest.NewRecorder()

			handler.ServeHTTP(recorder, request)

			assertImportProblemForTest(t, recorder, tt.status, tt.code)
			if store.lastRestoreBookInput != nil || store.lastRestoreChapterInput != nil {
				t.Fatalf("restore mutation reached store after denial: book=%#v chapter=%#v", store.lastRestoreBookInput, store.lastRestoreChapterInput)
			}
		})
	}
}

func assertDeletedSourceRowKeysForTest(t *testing.T, rows []map[string]json.RawMessage, wantKeys []string) {
	t.Helper()
	if len(rows) != 1 {
		t.Fatalf("deleted response rows = %d, want 1", len(rows))
	}
	if len(rows[0]) != len(wantKeys) {
		t.Fatalf("deleted response keys = %v, want %v", rows[0], wantKeys)
	}
	for _, key := range wantKeys {
		if _, ok := rows[0][key]; !ok {
			t.Fatalf("deleted response missing key %q: %v", key, rows[0])
		}
	}
}

func assertBookOrderForTest(t *testing.T, store *memoryStoreForTest, projectID string, wantIDs []string) {
	t.Helper()
	books, err := store.ListBooks(t.Context(), ListBooksStoreInput{ProjectID: projectID, Limit: 100})
	if err != nil {
		t.Fatalf("ListBooks() error = %v", err)
	}
	if len(books) != len(wantIDs) {
		t.Fatalf("book count = %d, want %d: %#v", len(books), len(wantIDs), books)
	}
	for index, wantID := range wantIDs {
		if books[index].ID != wantID || books[index].Order != index+1 {
			t.Fatalf("books[%d] = %s order %d, want %s order %d; all books: %#v", index, books[index].ID, books[index].Order, wantID, index+1, books)
		}
	}
}
