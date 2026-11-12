package library

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strings"
	"testing"
	"time"

	"canonkeeper/backend/internal/auth"
	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/observability"
	"canonkeeper/backend/internal/projects"
	"github.com/go-chi/chi/v5"
)

func TestImportConstraintsHandlerReturnsOpenAPIContract(t *testing.T) {
	store := NewMemoryStoreForTest()
	store.projectRoles[roleKey("user-1", "project-1")] = projects.RoleViewer
	handler := newImportHandlerForTest(NewService(store, StaticIDsForTest(), StaticClockForTest()), fakeImportAuthForTest("user-1"))
	req := newAuthenticatedImportRequestForTest(t, http.MethodGet, "/projects/project-1/import-constraints", "")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", rec.Header().Get("Cache-Control"))
	}
	var body struct {
		AllowedExtensions []string `json:"allowedExtensions"`
		AllowedMIMETypes  []string `json:"allowedMimeTypes"`
		MaxFileSizeBytes  int64    `json:"maxFileSizeBytes"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode constraints response: %v", err)
	}
	assertStringSliceEqualForTest(t, body.AllowedExtensions, []string{".fb2", ".epub", ".docx", ".txt", ".md", ".rtf", ".odt", ".fb2.zip"})
	assertStringSliceEqualForTest(t, body.AllowedMIMETypes, []string{
		"application/x-fictionbook+xml",
		"text/xml",
		"application/xml",
		"application/epub+zip",
		"application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		"text/plain",
		"text/markdown",
		"text/x-markdown",
		"application/rtf",
		"text/rtf",
		"application/vnd.oasis.opendocument.text",
		"application/zip",
		"application/x-zip-compressed",
	})
	if body.MaxFileSizeBytes != 52428800 {
		t.Fatalf("maxFileSizeBytes = %d, want 52428800", body.MaxFileSizeBytes)
	}
}

func TestCreateImportJobRejectsStaleExpectedBookOrder(t *testing.T) {
	const payload = "<FictionBook>stale expected order</FictionBook>"
	store := NewMemoryStoreForTest()
	store.projectRoles[roleKey("user-1", "project-1")] = projects.RoleEditor
	store.books["book-1"] = Book{ID: "book-1", ProjectID: "project-1", Order: 1}
	store.books["book-2"] = Book{ID: "book-2", ProjectID: "project-1", Order: 2}

	_, err := NewService(store, StaticIDsForTest(), StaticClockForTest()).CreateImportJob(
		context.Background(), Actor{UserID: "user-1"}, "project-1", CreateImportJobInput{
			SourceFileName: "story.fb2", MIMEType: "application/x-fictionbook+xml",
			SizeBytes: int64(len(payload)), FileBytes: []byte(payload),
			IdempotencyKey: "stale-expected-order", RequestMethod: http.MethodPost,
			RequestPath: "/projects/project-1/imports", ImportMode: ImportModeNewBook,
			ExpectedBookOrder: 4,
		},
	)
	if !errors.Is(err, ErrImportExpectedOrderConflict) {
		t.Fatalf("CreateImportJob error = %v, want ErrImportExpectedOrderConflict", err)
	}
	if len(store.importJobs) != 0 {
		t.Fatalf("stale expected order created jobs = %#v", store.importJobs)
	}
}

func TestImportReplayReturnsOriginalJobAfterOrderAdvances(t *testing.T) {
	const payload = "<FictionBook>idempotent expected order</FictionBook>"
	store := NewMemoryStoreForTest()
	store.projectRoles[roleKey("user-1", "project-1")] = projects.RoleEditor
	service := NewService(store, StaticIDsForTest(), StaticClockForTest())
	input := CreateImportJobInput{
		SourceFileName: "story.fb2", MIMEType: "application/x-fictionbook+xml",
		SizeBytes: int64(len(payload)), FileBytes: []byte(payload),
		IdempotencyKey: "replay-after-advance", RequestMethod: http.MethodPost,
		RequestPath: "/projects/project-1/imports", ImportMode: ImportModeNewBook,
		ExpectedBookOrder: 1,
	}
	created, err := service.CreateImportJob(context.Background(), Actor{UserID: "user-1"}, "project-1", input)
	if err != nil {
		t.Fatalf("CreateImportJob(create) error = %v", err)
	}
	store.books["book-1"] = Book{ID: "book-1", ProjectID: "project-1", Order: 1}

	replayed, err := service.CreateImportJob(context.Background(), Actor{UserID: "user-1"}, "project-1", input)
	if err != nil || replayed.ID != created.ID {
		t.Fatalf("CreateImportJob(replay) = (%#v, %v), want original job", replayed, err)
	}
	if len(store.importJobs) != 1 {
		t.Fatalf("replay created jobs = %#v", store.importJobs)
	}
}

func TestImportBookFileAcceptsDOCX(t *testing.T) {
	document := `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>Imported</w:t></w:r></w:p></w:body></w:document>`
	data := buildDOCXForTest(t, docxEntryForTest{name: "word/document.xml", body: document})
	store := NewMemoryStoreForTest()
	store.projectRoles[roleKey("user-1", "project-1")] = projects.RoleEditor
	handler := newImportHandlerForTest(NewService(store, StaticIDsForTest(), StaticClockForTest()), fakeImportAuthForTest("user-1"))
	req := newMultipartImportRequestForTest(
		t,
		"/projects/project-1/imports",
		"story.docx",
		"application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		string(data),
	)
	req.Header.Set("Origin", importAllowedOriginForTest)
	req.Header.Set("X-CSRF-Token", "csrf-for-test")
	req.Header.Set("Idempotency-Key", "import-docx-request-1")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusAccepted, rec.Body.String())
	}
	if len(store.importJobs) != 1 || len(store.importFiles) != 1 || len(store.enqueuedImports) != 1 {
		t.Fatalf("DOCX import persisted files/jobs/queue = %d/%d/%d, want 1/1/1", len(store.importFiles), len(store.importJobs), len(store.enqueuedImports))
	}
	if got := store.importJobs[0].Metadata.InitialPublication; got != InitialPublicationPublishImported {
		t.Fatalf("DOCX initial publication = %q, want %q", got, InitialPublicationPublishImported)
	}
}

func TestCreateImportJobFreezesServerOwnedPublicationByFormat(t *testing.T) {
	document := `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>Imported</w:t></w:r></w:p></w:body></w:document>`
	docx := buildDOCXForTest(t, docxEntryForTest{name: "word/document.xml", body: document})
	tests := []struct {
		name, fileName, mimeType string
		content                  []byte
		want                     InitialPublication
	}{
		{name: "fb2", fileName: "story.fb2", mimeType: "application/x-fictionbook+xml", content: []byte("<FictionBook>story</FictionBook>"), want: InitialPublicationPublishImported},
		{name: "epub", fileName: "story.epub", mimeType: "application/epub+zip", content: readFileForImportQueueTest(t, "testdata/simple.epub"), want: InitialPublicationPublishImported},
		{name: "docx", fileName: "story.docx", mimeType: "application/vnd.openxmlformats-officedocument.wordprocessingml.document", content: docx, want: InitialPublicationPublishImported},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := NewMemoryStoreForTest()
			store.projectRoles[roleKey("user-1", "project-1")] = projects.RoleEditor
			_, err := NewService(store, StaticIDsForTest(), StaticClockForTest()).CreateImportJob(
				context.Background(), Actor{UserID: "user-1"}, "project-1", CreateImportJobInput{
					SourceFileName: test.fileName, MIMEType: test.mimeType,
					SizeBytes: int64(len(test.content)), FileBytes: test.content,
					IdempotencyKey: "format-policy-" + test.name, RequestMethod: http.MethodPost,
					RequestPath: "/projects/project-1/imports", ImportMode: ImportModeNewBook,
				},
			)
			if err != nil {
				t.Fatalf("CreateImportJob() error = %v", err)
			}
			if got := store.importJobs[0].Metadata.InitialPublication; got != test.want {
				t.Fatalf("initial publication = %q, want %q", got, test.want)
			}
		})
	}
}

func TestInitialPublicationForImportExtension(t *testing.T) {
	for extension, want := range map[string]InitialPublication{
		".fb2":  InitialPublicationPublishImported,
		".EPUB": InitialPublicationPublishImported,
		".docx": InitialPublicationPublishImported,
	} {
		got, err := InitialPublicationForImportExtension(extension)
		if err != nil || got != want {
			t.Fatalf("InitialPublicationForImportExtension(%q) = %q, %v; want %q", extension, got, err, want)
		}
	}
	if _, err := InitialPublicationForImportExtension(".pdf"); !errors.Is(err, ErrImportUnsupportedMediaType) {
		t.Fatalf("unsupported extension error = %v, want ErrImportUnsupportedMediaType", err)
	}
}

func TestImportBookFileCreatesQueuedImportJob(t *testing.T) {
	const payload = "<FictionBook>RAW_IMPORT_BODY_SHOULD_NOT_BE_STORED</FictionBook>"
	store := NewMemoryStoreForTest()
	store.projectRoles[roleKey("user-1", "project-1")] = projects.RoleEditor
	handler := newImportHandlerForTest(NewService(store, StaticIDsForTest(), StaticClockForTest()), fakeImportAuthForTest("user-1"))
	req := newMultipartImportRequestForTest(t, "/projects/project-1/imports", "story.FB2", "application/x-fictionbook+xml", payload)
	req.Header.Set("Origin", importAllowedOriginForTest)
	req.Header.Set("X-CSRF-Token", "csrf-for-test")
	req.Header.Set("Idempotency-Key", "import-request-1")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusAccepted, rec.Body.String())
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", rec.Header().Get("Cache-Control"))
	}
	if rec.Header().Get("Location") != "/api/v1/jobs/job_generated_1" {
		t.Fatalf("Location = %q, want /api/v1/jobs/job_generated_1", rec.Header().Get("Location"))
	}
	if rec.Header().Get("Retry-After") != "1" {
		t.Fatalf("Retry-After = %q, want 1", rec.Header().Get("Retry-After"))
	}
	var response struct {
		JobID string `json:"jobId"`
		Job   struct {
			ID       string `json:"id"`
			Kind     string `json:"kind"`
			Status   string `json:"status"`
			Progress int    `json:"progress"`
			Subject  struct {
				Type      string `json:"type"`
				ID        string `json:"id"`
				ProjectID string `json:"projectId"`
			} `json:"subject"`
			CanCancel bool `json:"canCancel"`
			Links     struct {
				Self   string `json:"self"`
				Cancel string `json:"cancel"`
			} `json:"links"`
		} `json:"job"`
		Links struct {
			Job string `json:"job"`
		} `json:"links"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode import response: %v", err)
	}
	if response.JobID != "job_generated_1" || response.Job.ID != "job_generated_1" {
		t.Fatalf("job ids = %q/%q, want job_generated_1", response.JobID, response.Job.ID)
	}
	if response.Job.Kind != "import" || response.Job.Status != "queued" || response.Job.Progress != 0 {
		t.Fatalf("job state = kind %q status %q progress %d, want import/queued/0", response.Job.Kind, response.Job.Status, response.Job.Progress)
	}
	if response.Job.Subject.Type != "import" || response.Job.Subject.ID != "project-1" || response.Job.Subject.ProjectID != "project-1" {
		t.Fatalf("job subject = %#v, want import subject", response.Job.Subject)
	}
	if !response.Job.CanCancel {
		t.Fatal("job canCancel = false, want true")
	}
	assertStringEqualForTest(t, response.Job.Links.Self, "/api/v1/jobs/job_generated_1")
	assertStringEqualForTest(t, response.Job.Links.Cancel, "/api/v1/jobs/job_generated_1/cancel")
	assertStringEqualForTest(t, response.Links.Job, "/api/v1/jobs/job_generated_1")

	if len(store.importJobs) != 1 {
		t.Fatalf("created import jobs = %d, want 1", len(store.importJobs))
	}
	job := store.importJobs[0]
	if job.ID != "job_generated_1" || job.ProjectID != "project-1" || job.Kind != "import" || job.Status != "queued" {
		t.Fatalf("stored job = %#v, want queued import job for project-1", job)
	}
	if job.Metadata.StorageKey != "storage_generated_1" ||
		job.Metadata.SourceFileName != "story.FB2" ||
		job.Metadata.Extension != ".fb2" ||
		job.Metadata.MIMEType != "application/x-fictionbook+xml" ||
		job.Metadata.SizeBytes != int64(len(payload)) ||
		job.Metadata.IdempotencyKey != "import-request-1" ||
		job.Metadata.ImportMode != ImportModeNewBook {
		t.Fatalf("stored metadata = %#v, want uploaded file metadata", job.Metadata)
	}
	if job.Metadata.Title == nil || *job.Metadata.Title != "Imported title" {
		t.Fatalf("stored metadata title = %#v, want Imported title", job.Metadata.Title)
	}
	if strings.Contains(fmt.Sprintf("%#v", job.Metadata), payload) {
		t.Fatalf("stored metadata contains raw multipart body")
	}
	file, ok := store.importFiles["storage_generated_1"]
	if !ok {
		t.Fatalf("missing stored import file for storage key")
	}
	if string(file.Bytes) != payload {
		t.Fatalf("stored file bytes = %q, want original payload", string(file.Bytes))
	}
	if !file.ExpiresAt.Equal(staticTestTime().Add(ImportUploadRetention)) {
		t.Fatalf("stored file expires at = %s, want %s", file.ExpiresAt, staticTestTime().Add(ImportUploadRetention))
	}
	if len(store.enqueuedImports) != 1 {
		t.Fatalf("enqueued imports = %d, want 1", len(store.enqueuedImports))
	}
	if store.enqueuedImports[0].JobID != "job_generated_1" || store.enqueuedImports[0].StorageKey != "storage_generated_1" {
		t.Fatalf("enqueued import = %#v, want generated job and storage IDs", store.enqueuedImports[0])
	}
}

func TestImportBookFileAuthorizesBeforeReadingBody(t *testing.T) {
	tests := []struct {
		name        string
		actorUserID string
		role        projects.Role
		hasRole     bool
		contentType string
		contentSize int64
		wantStatus  int
		wantCode    string
	}{
		{
			name:        "viewer before non-multipart parsing",
			actorUserID: "viewer-1",
			role:        projects.RoleViewer,
			hasRole:     true,
			contentType: "application/json",
			contentSize: 16,
			wantStatus:  http.StatusForbidden,
			wantCode:    "forbidden",
		},
		{
			name:        "viewer before malformed multipart parsing",
			actorUserID: "viewer-1",
			role:        projects.RoleViewer,
			hasRole:     true,
			contentType: "multipart/form-data; boundary=broken",
			contentSize: 16,
			wantStatus:  http.StatusForbidden,
			wantCode:    "forbidden",
		},
		{
			name:        "non-member before oversized multipart parsing",
			actorUserID: "outsider-1",
			contentType: "multipart/form-data; boundary=oversized",
			contentSize: maxMultipartImportBodyBytes + 1,
			wantStatus:  http.StatusNotFound,
			wantCode:    "not_found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewMemoryStoreForTest()
			if tt.hasRole {
				store.projectRoles[roleKey(tt.actorUserID, "project-1")] = tt.role
			}
			handler := newImportHandlerForTest(NewService(store, StaticIDsForTest(), StaticClockForTest()), fakeImportAuthForTest(tt.actorUserID))
			body := &readCountingBodyForTest{}
			req := newAuthenticatedImportRequestWithBodyForTest(http.MethodPost, "/projects/project-1/imports", body)
			req.ContentLength = tt.contentSize
			req.Header.Set("Content-Type", tt.contentType)
			req.Header.Set("Origin", importAllowedOriginForTest)
			req.Header.Set("X-CSRF-Token", "csrf-for-test")
			req.Header.Set("Idempotency-Key", "import-request-1")
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			assertImportProblemForTest(t, rec, tt.wantStatus, tt.wantCode)
			if body.reads != 0 {
				t.Fatalf("request body reads = %d, want 0 before import authorization", body.reads)
			}
			if len(store.importFiles) != 0 || len(store.importJobs) != 0 || len(store.enqueuedImports) != 0 {
				t.Fatalf("unauthorized import persisted data: files=%d jobs=%d enqueued=%d", len(store.importFiles), len(store.importJobs), len(store.enqueuedImports))
			}
		})
	}
}

func TestImportBookFileAcceptsOpenAPIMultipartMetadataAndOptionsJSON(t *testing.T) {
	const payload = "<FictionBook>JSON fields</FictionBook>"
	store := NewMemoryStoreForTest()
	store.projectRoles[roleKey("user-1", "project-1")] = projects.RoleEditor
	handler := newImportHandlerForTest(NewService(store, StaticIDsForTest(), StaticClockForTest()), fakeImportAuthForTest("user-1"))
	req := newMultipartImportRequestWithFieldsForTest(t, "/projects/project-1/imports", "story.fb2", "application/x-fictionbook+xml", payload, map[string]string{
		"metadata": `{"title":"JSON title","sourceFileName":"json-source.fb2","expectedBookOrder":1}`,
		"options":  `{"importMode":"new_book"}`,
	})
	req.Header.Set("Origin", importAllowedOriginForTest)
	req.Header.Set("X-CSRF-Token", "csrf-for-test")
	req.Header.Set("Idempotency-Key", "import-request-json")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusAccepted, rec.Body.String())
	}
	if len(store.importJobs) != 1 {
		t.Fatalf("created import jobs = %d, want 1", len(store.importJobs))
	}
	metadata := store.importJobs[0].Metadata
	if metadata.Title == nil || *metadata.Title != "JSON title" {
		t.Fatalf("metadata title = %#v, want JSON title", metadata.Title)
	}
	if metadata.ImportMode != ImportModeNewBook {
		t.Fatalf("import mode = %q, want new_book", metadata.ImportMode)
	}
	if store.importJobs[0].Subject.ExpectedBookOrder != 1 {
		t.Fatalf("expected book order = %d, want 1", store.importJobs[0].Subject.ExpectedBookOrder)
	}
}

// An update and its preview name their book and nothing else (specs/2026-10-27-update-book-from-file.md, R1.1, R6.1).
func TestImportBookFileAcceptsUpdateOfANamedBook(t *testing.T) {
	const payload = "<FictionBook>update</FictionBook>"
	for _, tt := range []struct {
		name     string
		fields   map[string]string
		want     int
		wantKey  string
		wantMode ImportMode
	}{
		{name: "update names its book", fields: map[string]string{"options": `{"importMode":"update_book","bookId":"book-7"}`}, want: http.StatusAccepted, wantKey: "book-7"},
		{name: "preview names its book", fields: map[string]string{"options": `{"importMode":"update_preview","bookId":"book-7"}`}, want: http.StatusAccepted, wantKey: "book-7", wantMode: ImportModeUpdatePreview},
		{name: "preview without a book", fields: map[string]string{"options": `{"importMode":"update_preview"}`}, want: http.StatusBadRequest},
		{name: "preview with a title", fields: map[string]string{"metadata": `{"title":"Другое"}`, "options": `{"importMode":"update_preview","bookId":"book-7"}`}, want: http.StatusBadRequest},
		{name: "update without a book", fields: map[string]string{"options": `{"importMode":"update_book"}`}, want: http.StatusBadRequest},
		{name: "new book with a book", fields: map[string]string{"options": `{"importMode":"new_book","bookId":"book-7"}`}, want: http.StatusBadRequest},
		{name: "update with an order", fields: map[string]string{"metadata": `{"expectedBookOrder":2}`, "options": `{"importMode":"update_book","bookId":"book-7"}`}, want: http.StatusBadRequest},
	} {
		t.Run(tt.name, func(t *testing.T) {
			store := NewMemoryStoreForTest()
			store.projectRoles[roleKey("user-1", "project-1")] = projects.RoleEditor
			handler := newImportHandlerForTest(NewService(store, StaticIDsForTest(), StaticClockForTest()), fakeImportAuthForTest("user-1"))
			req := newMultipartImportRequestWithFieldsForTest(t, "/projects/project-1/imports", "story.fb2", "application/x-fictionbook+xml", payload, tt.fields)
			req.Header.Set("Origin", importAllowedOriginForTest)
			req.Header.Set("X-CSRF-Token", "csrf-for-test")
			req.Header.Set("Idempotency-Key", "import-request-update")
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code != tt.want {
				t.Fatalf("status = %d, want %d; body: %s", rec.Code, tt.want, rec.Body.String())
			}
			if tt.want != http.StatusAccepted {
				if len(store.importJobs) != 0 {
					t.Fatalf("rejected update persisted %d jobs", len(store.importJobs))
				}
				return
			}
			metadata := store.importJobs[0].Metadata
			wantMode := tt.wantMode
			if wantMode == "" {
				wantMode = ImportModeUpdateBook
			}
			if metadata.ImportMode != wantMode || metadata.TargetBookID != tt.wantKey {
				t.Fatalf("metadata = %+v, want %s of %s", metadata, wantMode, tt.wantKey)
			}
		})
	}
}

func TestImportBookFileRejectsAppendImportModeBeforePersistence(t *testing.T) {
	const payload = "<FictionBook>append unsupported</FictionBook>"
	store := NewMemoryStoreForTest()
	store.projectRoles[roleKey("user-1", "project-1")] = projects.RoleEditor
	handler := newImportHandlerForTest(NewService(store, StaticIDsForTest(), StaticClockForTest()), fakeImportAuthForTest("user-1"))
	req := newMultipartImportRequestWithFieldsForTest(t, "/projects/project-1/imports", "story.fb2", "application/x-fictionbook+xml", payload, map[string]string{
		"options": `{"importMode":"append"}`,
	})
	req.Header.Set("Origin", importAllowedOriginForTest)
	req.Header.Set("X-CSRF-Token", "csrf-for-test")
	req.Header.Set("Idempotency-Key", "import-request-append")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assertImportProblemForTest(t, rec, http.StatusBadRequest, "request.validation")
	if len(store.importFiles) != 0 || len(store.importJobs) != 0 || len(store.enqueuedImports) != 0 {
		t.Fatalf("append import mode persisted data: files=%d jobs=%d enqueued=%d", len(store.importFiles), len(store.importJobs), len(store.enqueuedImports))
	}
}

func TestImportBookFileUsesJSONMetadataSourceFileNameWhenMultipartFilenameIsGeneric(t *testing.T) {
	const payload = "<FictionBook>JSON source filename</FictionBook>"
	store := NewMemoryStoreForTest()
	store.projectRoles[roleKey("user-1", "project-1")] = projects.RoleEditor
	handler := newImportHandlerForTest(NewService(store, StaticIDsForTest(), StaticClockForTest()), fakeImportAuthForTest("user-1"))
	req := newMultipartImportRequestWithFieldsForTest(t, "/projects/project-1/imports", "upload", "application/x-fictionbook+xml", payload, map[string]string{
		"metadata": `{"sourceFileName":"json-source.fb2"}`,
	})
	req.Header.Set("Origin", importAllowedOriginForTest)
	req.Header.Set("X-CSRF-Token", "csrf-for-test")
	req.Header.Set("Idempotency-Key", "json-source-key")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusAccepted, rec.Body.String())
	}
	if len(store.importJobs) != 1 {
		t.Fatalf("created import jobs = %d, want 1", len(store.importJobs))
	}
	metadata := store.importJobs[0].Metadata
	if metadata.SourceFileName != "json-source.fb2" || metadata.Extension != ".fb2" {
		t.Fatalf("stored source metadata = %#v, want JSON source filename and .fb2 extension", metadata)
	}
}

func TestImportBookFileUsesDottedMetadataSourceFileNameWhenMultipartFilenameIsGeneric(t *testing.T) {
	const payload = "<FictionBook>dotted source filename</FictionBook>"
	store := NewMemoryStoreForTest()
	store.projectRoles[roleKey("user-1", "project-1")] = projects.RoleEditor
	handler := newImportHandlerForTest(NewService(store, StaticIDsForTest(), StaticClockForTest()), fakeImportAuthForTest("user-1"))
	req := newMultipartImportRequestWithFieldsForTest(t, "/projects/project-1/imports", "upload", "application/x-fictionbook+xml", payload, map[string]string{
		"metadata.sourceFileName": "dotted-source.fb2",
	})
	req.Header.Set("Origin", importAllowedOriginForTest)
	req.Header.Set("X-CSRF-Token", "csrf-for-test")
	req.Header.Set("Idempotency-Key", "dotted-source-key")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusAccepted, rec.Body.String())
	}
	if len(store.importJobs) != 1 {
		t.Fatalf("created import jobs = %d, want 1", len(store.importJobs))
	}
	metadata := store.importJobs[0].Metadata
	if metadata.SourceFileName != "dotted-source.fb2" || metadata.Extension != ".fb2" {
		t.Fatalf("stored source metadata = %#v, want dotted source filename and .fb2 extension", metadata)
	}
}

func TestImportBookFileMetadataSourceFileNameParticipatesInIdempotencyFingerprint(t *testing.T) {
	const payload = "<FictionBook>same bytes different source filename</FictionBook>"
	store := NewMemoryStoreForTest()
	store.projectRoles[roleKey("user-1", "project-1")] = projects.RoleEditor
	handler := newImportHandlerForTest(NewService(store, StaticIDsForTest(), StaticClockForTest()), fakeImportAuthForTest("user-1"))

	first := newMultipartImportRequestWithFieldsForTest(t, "/projects/project-1/imports", "upload", "application/x-fictionbook+xml", payload, map[string]string{
		"metadata": `{"sourceFileName":"first-source.fb2"}`,
	})
	first.Header.Set("Origin", importAllowedOriginForTest)
	first.Header.Set("X-CSRF-Token", "csrf-for-test")
	first.Header.Set("Idempotency-Key", "metadata-fingerprint-key")
	firstRec := httptest.NewRecorder()
	handler.ServeHTTP(firstRec, first)
	if firstRec.Code != http.StatusAccepted {
		t.Fatalf("first status = %d, want %d; body: %s", firstRec.Code, http.StatusAccepted, firstRec.Body.String())
	}

	second := newMultipartImportRequestWithFieldsForTest(t, "/projects/project-1/imports", "upload", "application/x-fictionbook+xml", payload, map[string]string{
		"metadata": `{"sourceFileName":"second-source.fb2"}`,
	})
	second.Header.Set("Origin", importAllowedOriginForTest)
	second.Header.Set("X-CSRF-Token", "csrf-for-test")
	second.Header.Set("Idempotency-Key", "metadata-fingerprint-key")
	secondRec := httptest.NewRecorder()
	handler.ServeHTTP(secondRec, second)

	assertImportProblemForTest(t, secondRec, http.StatusConflict, "conflict")
	if len(store.importFiles) != 1 || len(store.importJobs) != 1 || len(store.enqueuedImports) != 1 {
		t.Fatalf("metadata filename conflict mutated work: files=%d jobs=%d enqueued=%d", len(store.importFiles), len(store.importJobs), len(store.enqueuedImports))
	}
}

func TestImportBookFileRejectsMalformedOrUnknownMultipartJSON(t *testing.T) {
	tests := []struct {
		name   string
		fields map[string]string
	}{
		{
			name: "malformed metadata",
			fields: map[string]string{
				"metadata": `{"title":`,
			},
		},
		{
			name: "unknown metadata field",
			fields: map[string]string{
				"metadata": `{"title":"JSON title","extra":true}`,
			},
		},
		{
			name: "metadata is not object",
			fields: map[string]string{
				"metadata": `null`,
			},
		},
		{
			name: "malformed options",
			fields: map[string]string{
				"options": `{"importMode":`,
			},
		},
		{
			name: "unknown options field",
			fields: map[string]string{
				"options": `{"importMode":"append","extra":true}`,
			},
		},
		{
			name: "options is not object",
			fields: map[string]string{
				"options": `null`,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewMemoryStoreForTest()
			store.projectRoles[roleKey("user-1", "project-1")] = projects.RoleEditor
			handler := newImportHandlerForTest(NewService(store, StaticIDsForTest(), StaticClockForTest()), fakeImportAuthForTest("user-1"))
			req := newMultipartImportRequestWithFieldsForTest(t, "/projects/project-1/imports", "story.fb2", "application/x-fictionbook+xml", "<FictionBook/>", tt.fields)
			req.Header.Set("Origin", importAllowedOriginForTest)
			req.Header.Set("X-CSRF-Token", "csrf-for-test")
			req.Header.Set("Idempotency-Key", "import-request-json")
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			assertImportProblemForTest(t, rec, http.StatusBadRequest, "request.validation")
			if len(store.importFiles) != 0 || len(store.importJobs) != 0 || len(store.enqueuedImports) != 0 {
				t.Fatalf("invalid multipart JSON persisted data: files=%d jobs=%d enqueued=%d", len(store.importFiles), len(store.importJobs), len(store.enqueuedImports))
			}
		})
	}
}

func TestImportBookFileRejectsUnsupportedExtensionAsUnsupportedMediaType(t *testing.T) {
	store := NewMemoryStoreForTest()
	store.projectRoles[roleKey("user-1", "project-1")] = projects.RoleEditor
	handler := newImportHandlerForTest(NewService(store, StaticIDsForTest(), StaticClockForTest()), fakeImportAuthForTest("user-1"))
	req := newMultipartImportRequestForTest(t, "/projects/project-1/imports", "story.pdf", "application/x-fictionbook+xml", "<FictionBook/>")
	req.Header.Set("Origin", importAllowedOriginForTest)
	req.Header.Set("X-CSRF-Token", "csrf-for-test")
	req.Header.Set("Idempotency-Key", "import-request-1")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assertImportProblemForTest(t, rec, http.StatusUnsupportedMediaType, "import.unsupported_media_type")
	if len(store.importFiles) != 0 || len(store.importJobs) != 0 || len(store.enqueuedImports) != 0 {
		t.Fatalf("unsupported extension persisted data: files=%d jobs=%d enqueued=%d", len(store.importFiles), len(store.importJobs), len(store.enqueuedImports))
	}
}

func TestImportBookFileRejectsNonMultipartBodyAsUnsupportedMediaType(t *testing.T) {
	store := NewMemoryStoreForTest()
	store.projectRoles[roleKey("user-1", "project-1")] = projects.RoleEditor
	handler := newImportHandlerForTest(NewService(store, StaticIDsForTest(), StaticClockForTest()), fakeImportAuthForTest("user-1"))
	req := newAuthenticatedImportRequestForTest(t, http.MethodPost, "/projects/project-1/imports", `{"file":"story.fb2"}`)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", importAllowedOriginForTest)
	req.Header.Set("X-CSRF-Token", "csrf-for-test")
	req.Header.Set("Idempotency-Key", "import-request-1")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assertImportProblemForTest(t, rec, http.StatusUnsupportedMediaType, "import.unsupported_media_type")
	if len(store.importFiles) != 0 || len(store.importJobs) != 0 || len(store.enqueuedImports) != 0 {
		t.Fatalf("non-multipart body persisted data: files=%d jobs=%d enqueued=%d", len(store.importFiles), len(store.importJobs), len(store.enqueuedImports))
	}
}

func TestImportBookFileRejectsMalformedMultipartBodyAsBadRequest(t *testing.T) {
	store := NewMemoryStoreForTest()
	store.projectRoles[roleKey("user-1", "project-1")] = projects.RoleEditor
	handler := newImportHandlerForTest(NewService(store, StaticIDsForTest(), StaticClockForTest()), fakeImportAuthForTest("user-1"))
	req := newAuthenticatedImportRequestForTest(t, http.MethodPost, "/projects/project-1/imports", "not a multipart body")
	req.Header.Set("Content-Type", "multipart/form-data; boundary=broken")
	req.Header.Set("Origin", importAllowedOriginForTest)
	req.Header.Set("X-CSRF-Token", "csrf-for-test")
	req.Header.Set("Idempotency-Key", "import-request-1")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assertImportProblemForTest(t, rec, http.StatusBadRequest, "request.validation")
	if len(store.importFiles) != 0 || len(store.importJobs) != 0 || len(store.enqueuedImports) != 0 {
		t.Fatalf("malformed multipart persisted data: files=%d jobs=%d enqueued=%d", len(store.importFiles), len(store.importJobs), len(store.enqueuedImports))
	}
}

func TestImportBookFileRejectsContentMismatch(t *testing.T) {
	store := NewMemoryStoreForTest()
	store.projectRoles[roleKey("user-1", "project-1")] = projects.RoleEditor
	handler := newImportHandlerForTest(NewService(store, StaticIDsForTest(), StaticClockForTest()), fakeImportAuthForTest("user-1"))
	req := newMultipartImportRequestForTest(t, "/projects/project-1/imports", "story.epub", "application/epub+zip", "<FictionBook/>")
	req.Header.Set("Origin", importAllowedOriginForTest)
	req.Header.Set("X-CSRF-Token", "csrf-for-test")
	req.Header.Set("Idempotency-Key", "import-request-1")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assertImportProblemForTest(t, rec, http.StatusUnprocessableEntity, "import.content_mismatch")
	if len(store.importFiles) != 0 || len(store.importJobs) != 0 || len(store.enqueuedImports) != 0 {
		t.Fatalf("content mismatch persisted data: files=%d jobs=%d enqueued=%d", len(store.importFiles), len(store.importJobs), len(store.enqueuedImports))
	}
}

func TestDOCXImportRequiresDOCXMIMEAndZIPSignature(t *testing.T) {
	document := `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>Text</w:t></w:r></w:p></w:body></w:document>`
	data := buildDOCXForTest(t, docxEntryForTest{name: "word/document.xml", body: document})
	const mimeType = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"

	if err := validateImportExtensionMIMEContent(".docx", "application/epub+zip", data); !errors.Is(err, ErrImportContentMismatch) {
		t.Fatalf("DOCX with EPUB MIME error = %v, want ErrImportContentMismatch", err)
	}
	if err := validateImportExtensionMIMEContent(".docx", mimeType, []byte("not a ZIP archive")); !errors.Is(err, ErrImportContentMismatch) {
		t.Fatalf("DOCX without ZIP signature error = %v, want ErrImportContentMismatch", err)
	}
}

func TestImportBookFileRejectsFB2XMLWithoutFictionBook(t *testing.T) {
	store := NewMemoryStoreForTest()
	store.projectRoles[roleKey("user-1", "project-1")] = projects.RoleEditor
	handler := newImportHandlerForTest(NewService(store, StaticIDsForTest(), StaticClockForTest()), fakeImportAuthForTest("user-1"))
	req := newMultipartImportRequestForTest(t, "/projects/project-1/imports", "story.fb2", "application/xml", `<?xml version="1.0"?><not-fiction/>`)
	req.Header.Set("Origin", importAllowedOriginForTest)
	req.Header.Set("X-CSRF-Token", "csrf-for-test")
	req.Header.Set("Idempotency-Key", "import-request-1")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assertImportProblemForTest(t, rec, http.StatusUnprocessableEntity, "import.content_mismatch")
	if len(store.importFiles) != 0 || len(store.importJobs) != 0 || len(store.enqueuedImports) != 0 {
		t.Fatalf("non-FictionBook XML persisted data: files=%d jobs=%d enqueued=%d", len(store.importFiles), len(store.importJobs), len(store.enqueuedImports))
	}
}

func TestImportBookFileRejectsShortIdempotencyKeyBeforePersistence(t *testing.T) {
	store := NewMemoryStoreForTest()
	store.projectRoles[roleKey("user-1", "project-1")] = projects.RoleEditor
	handler := newImportHandlerForTest(NewService(store, StaticIDsForTest(), StaticClockForTest()), fakeImportAuthForTest("user-1"))
	req := newMultipartImportRequestForTest(t, "/projects/project-1/imports", "story.fb2", "application/x-fictionbook+xml", "<FictionBook/>")
	req.Header.Set("Origin", importAllowedOriginForTest)
	req.Header.Set("X-CSRF-Token", "csrf-for-test")
	req.Header.Set("Idempotency-Key", "short")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assertImportProblemForTest(t, rec, http.StatusBadRequest, "request.validation")
	if len(store.importFiles) != 0 || len(store.importJobs) != 0 || len(store.enqueuedImports) != 0 {
		t.Fatalf("short idempotency key persisted data: files=%d jobs=%d enqueued=%d", len(store.importFiles), len(store.importJobs), len(store.enqueuedImports))
	}
}

func TestImportBookFileAcceptsContractValidIdempotencyKeyCharacters(t *testing.T) {
	store := NewMemoryStoreForTest()
	store.projectRoles[roleKey("user-1", "project-1")] = projects.RoleEditor
	handler := newImportHandlerForTest(NewService(store, StaticIDsForTest(), StaticClockForTest()), fakeImportAuthForTest("user-1"))
	req := newMultipartImportRequestForTest(t, "/projects/project-1/imports", "story.fb2", "application/x-fictionbook+xml", "<FictionBook/>")
	req.Header.Set("Origin", importAllowedOriginForTest)
	req.Header.Set("X-CSRF-Token", "csrf-for-test")
	req.Header.Set("Idempotency-Key", "valid key/with spaces!")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusAccepted, rec.Body.String())
	}
	if len(store.importJobs) != 1 || len(store.enqueuedImports) != 1 {
		t.Fatalf("accepted idempotency key did not create/enqueue job: jobs=%d enqueued=%d", len(store.importJobs), len(store.enqueuedImports))
	}
}

func TestImportBookFileIdempotencyReplaysAndConflicts(t *testing.T) {
	const firstPayload = "<FictionBook>same</FictionBook>"
	store := NewMemoryStoreForTest()
	store.projectRoles[roleKey("user-1", "project-1")] = projects.RoleEditor
	handler := newImportHandlerForTest(NewService(store, StaticIDsForTest(), StaticClockForTest()), fakeImportAuthForTest("user-1"))

	first := serveImportForTest(t, handler, "import-request-1", firstPayload)
	second := serveImportForTest(t, handler, "import-request-1", firstPayload)

	if first.Code != http.StatusAccepted || second.Code != http.StatusAccepted {
		t.Fatalf("replay statuses = %d/%d, want 202/202", first.Code, second.Code)
	}
	firstJobID := decodeImportJobIDForTest(t, first.Body)
	secondJobID := decodeImportJobIDForTest(t, second.Body)
	if firstJobID != "job_generated_1" || secondJobID != "job_generated_1" {
		t.Fatalf("replay job IDs = %q/%q, want original job", firstJobID, secondJobID)
	}
	if len(store.importFiles) != 1 || len(store.importJobs) != 1 || len(store.enqueuedImports) != 1 {
		t.Fatalf("replay duplicated work: files=%d jobs=%d enqueued=%d", len(store.importFiles), len(store.importJobs), len(store.enqueuedImports))
	}

	conflict := serveImportForTest(t, handler, "import-request-1", "<FictionBook>changed</FictionBook>")
	assertImportProblemForTest(t, conflict, http.StatusConflict, "conflict")
	if len(store.importFiles) != 1 || len(store.importJobs) != 1 || len(store.enqueuedImports) != 1 {
		t.Fatalf("conflict mutated work: files=%d jobs=%d enqueued=%d", len(store.importFiles), len(store.importJobs), len(store.enqueuedImports))
	}
}

func TestCreateImportJobKeepsLegacyFingerprintWithServerOwnedPublication(t *testing.T) {
	const payload = "<FictionBook>fingerprint contract</FictionBook>"
	const draftOnlyFingerprint = "b38c48c07ba3c3049c80d7b44f24e2dd405ac16dbf7230915b88e49bb6d56ecf"
	store := NewMemoryStoreForTest()
	store.projectRoles[roleKey("user-1", "project-1")] = projects.RoleEditor
	_, err := NewService(store, StaticIDsForTest(), StaticClockForTest()).CreateImportJob(context.Background(), Actor{UserID: "user-1"}, "project-1", CreateImportJobInput{
		SourceFileName: "story.fb2",
		MIMEType:       "application/x-fictionbook+xml",
		SizeBytes:      int64(len(payload)),
		FileBytes:      []byte(payload),
		IdempotencyKey: "fingerprint-key",
		RequestMethod:  http.MethodPost,
		RequestPath:    "/projects/project-1/imports",
		ImportMode:     ImportModeNewBook,
	})
	if err != nil {
		t.Fatalf("CreateImportJob error = %v", err)
	}
	job := store.importJobs[0]
	if job.RequestFingerprint != draftOnlyFingerprint {
		t.Fatalf("fingerprint = %q, want legacy %q", job.RequestFingerprint, draftOnlyFingerprint)
	}
	if len(store.importJobs) != 1 || store.importJobs[0].Metadata.InitialPublication != InitialPublicationPublishImported {
		t.Fatalf("stored jobs = %#v, want server-owned publication", store.importJobs)
	}
}

func TestCreateImportJobReplaysLegacyDraftOnlyJob(t *testing.T) {
	const payload = "<FictionBook>fingerprint contract</FictionBook>"
	const draftOnlyFingerprint = "b38c48c07ba3c3049c80d7b44f24e2dd405ac16dbf7230915b88e49bb6d56ecf"
	store := NewMemoryStoreForTest()
	store.projectRoles[roleKey("user-1", "project-1")] = projects.RoleEditor
	key := importIdempotencyKeyForTest("user-1", http.MethodPost, "/projects/project-1/imports", "draft-fingerprint-key")
	store.importJobsByKey[key] = ImportJob{
		ID: "current-job", ProjectID: "project-1", Kind: "import", Status: "queued",
		RequestFingerprint: draftOnlyFingerprint,
		Metadata:           ImportFileMetadata{StorageKey: "current-storage", InitialPublication: InitialPublicationDraftOnly},
	}
	service := NewService(store, StaticIDsForTest(), StaticClockForTest())
	input := CreateImportJobInput{
		SourceFileName: "story.fb2", MIMEType: "application/x-fictionbook+xml", SizeBytes: int64(len(payload)), FileBytes: []byte(payload),
		IdempotencyKey: "draft-fingerprint-key", RequestMethod: http.MethodPost, RequestPath: "/projects/project-1/imports", ImportMode: ImportModeNewBook,
	}

	replayed, err := service.CreateImportJob(context.Background(), Actor{UserID: "user-1"}, "project-1", input)
	if err != nil || replayed.ID != "current-job" {
		t.Fatalf("current replay = (%#v, %v), want current-job", replayed, err)
	}
	if len(store.importFiles) != 0 || len(store.enqueuedImports) != 0 {
		t.Fatalf("current replay mutated source: files=%d enqueued=%d", len(store.importFiles), len(store.enqueuedImports))
	}

}

func TestImportBookFileReplayProjectsCancelingAndTerminalState(t *testing.T) {
	const payload = "<FictionBook>same</FictionBook>"
	const resultBookID = "10000000-0000-4000-8000-000000000002"
	const firstRequestID = "00000000-0000-4000-8000-000000005105"
	const replayRequestID = "00000000-0000-4000-8000-000000005106"
	for _, tc := range []struct {
		name         string
		storedStatus jobs.Status
		publicStatus jobs.PublicStatus
		progress     float64
		result       jobs.Result
		jobError     *jobs.Error
	}{
		{name: "canceling", storedStatus: jobs.StatusCanceling, publicStatus: jobs.PublicStatusCanceling, progress: 0.25},
		{
			name: "succeeded", storedStatus: jobs.StatusSucceeded, publicStatus: jobs.PublicStatusSucceeded, progress: 0.625,
			result: jobs.ImportJobResult{Type: jobs.KindImport, BookID: resultBookID, ChapterCount: 2, ParagraphCount: 3, ImportMode: "new_book", SourceFileName: "story.fb2"},
		},
		{
			name: "failed", storedStatus: jobs.StatusFailed, publicStatus: jobs.PublicStatusFailed, progress: 0.375,
			jobError: &jobs.Error{Code: "worker.handler_failed", Message: "The background job failed."},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := NewMemoryStoreForTest()
			store.projectRoles[roleKey("user-1", "project-1")] = projects.RoleEditor
			handler := newImportHandlerForTest(NewService(store, StaticIDsForTest(), StaticClockForTest()), fakeImportAuthForTest("user-1"))
			serve := func(requestID string) *httptest.ResponseRecorder {
				return serveImportForTest(t, observability.RequestLogger(io.Discard, observability.RequestLoggerOptions{
					NewRequestID: func() string { return requestID },
				})(handler), "import-replay-1", payload)
			}

			first := serve(firstRequestID)
			if first.Code != http.StatusAccepted {
				t.Fatalf("first status = %d, want %d; body: %s", first.Code, http.StatusAccepted, first.Body.String())
			}
			key := importIdempotencyKeyForTest("user-1", http.MethodPost, "/projects/project-1/imports", "import-replay-1")
			stored := store.importStartJobsByKey[key]
			stored.Status = tc.storedStatus
			stored.Progress = &tc.progress
			stored.Result = tc.result
			stored.Error = tc.jobError
			stored.CanCancel = true
			store.importStartJobsByKey[key] = stored

			replay := serve(replayRequestID)
			if replay.Code != http.StatusAccepted {
				t.Fatalf("replay status = %d, want %d; body: %s", replay.Code, http.StatusAccepted, replay.Body.String())
			}
			var response struct {
				Job struct {
					Status    string   `json:"status"`
					Progress  *float64 `json:"progress"`
					CanCancel bool     `json:"canCancel"`
					Result    *struct {
						Type           string `json:"type"`
						BookID         string `json:"bookId"`
						ChapterCount   int    `json:"chapterCount"`
						ParagraphCount int    `json:"paragraphCount"`
						ImportMode     string `json:"importMode"`
						SourceFileName string `json:"sourceFileName"`
					} `json:"result"`
					Error *struct {
						Code      string `json:"code"`
						RequestID string `json:"requestId"`
					} `json:"error"`
					Links struct {
						Cancel string `json:"cancel"`
					} `json:"links"`
				} `json:"job"`
			}
			if err := json.NewDecoder(replay.Body).Decode(&response); err != nil {
				t.Fatalf("decode replay response: %v", err)
			}
			if response.Job.Status != string(tc.publicStatus) {
				t.Fatalf("job status = %q, want %q", response.Job.Status, tc.publicStatus)
			}
			if response.Job.Progress == nil || *response.Job.Progress != tc.progress {
				t.Fatalf("job progress = %v, want %v", response.Job.Progress, tc.progress)
			}
			if response.Job.CanCancel || response.Job.Links.Cancel != "" {
				t.Fatalf("cancel projection = canCancel %v link %q, want false and absent", response.Job.CanCancel, response.Job.Links.Cancel)
			}
			if tc.result == nil {
				if response.Job.Result != nil {
					t.Fatalf("job result = %#v, want null", response.Job.Result)
				}
			} else if response.Job.Result == nil || response.Job.Result.Type != string(jobs.KindImport) ||
				response.Job.Result.BookID != resultBookID || response.Job.Result.ChapterCount != 2 ||
				response.Job.Result.ParagraphCount != 3 || response.Job.Result.ImportMode != "new_book" ||
				response.Job.Result.SourceFileName != "story.fb2" {
				t.Fatalf("job result = %#v, want typed current import result", response.Job.Result)
			}
			if tc.jobError == nil {
				if response.Job.Error != nil {
					t.Fatalf("job error = %#v, want null", response.Job.Error)
				}
			} else if response.Job.Error == nil || response.Job.Error.Code != tc.jobError.Code || response.Job.Error.RequestID != replayRequestID {
				t.Fatalf("job error = %#v, want exact code %q and current requestId %q", response.Job.Error, tc.jobError.Code, replayRequestID)
			}
			if !store.importStartJobsByKey[key].CanCancel {
				t.Fatal("replay projection mutated stored canCancel")
			}
		})
	}
}

func TestCreateImportJobReplaysAfterIdempotencyCreateConflict(t *testing.T) {
	const payload = "<FictionBook>race</FictionBook>"
	tests := []struct {
		name            string
		existingMatches bool
		wantErr         error
	}{
		{
			name:            "same request returns existing job",
			existingMatches: true,
		},
		{
			name:            "different request remains conflict",
			existingMatches: false,
			wantErr:         ErrConflict,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewMemoryStoreForTest()
			store.projectRoles[roleKey("user-1", "project-1")] = projects.RoleEditor
			store.importFiles["existing-storage"] = StoreImportFileInput{
				StorageKey: "existing-storage",
				ProjectID:  "project-1",
				Bytes:      []byte(payload),
			}
			store.createImportJobErr = ErrConflict
			store.createImportJobHook = func(input CreateImportJobStoreInput) {
				fingerprint := input.RequestFingerprint
				if !tt.existingMatches {
					fingerprint = "different-fingerprint"
				}
				metadata := input.Metadata
				metadata.StorageKey = "existing-storage"
				store.importJobsByKey[importIdempotencyKeyForTest(input.ActorUserID, input.RequestMethod, input.RequestPath, input.IdempotencyKey)] = ImportJob{
					ID:                 "existing-job",
					ProjectID:          input.ProjectID,
					Kind:               input.Kind,
					Status:             input.Status,
					Progress:           input.Progress,
					Subject:            input.Subject,
					CanCancel:          input.CanCancel,
					CreatedAt:          input.CreatedAt,
					UpdatedAt:          input.UpdatedAt,
					Metadata:           metadata,
					RequestFingerprint: fingerprint,
				}
			}
			service := NewService(store, StaticIDsForTest(), StaticClockForTest())

			job, err := service.CreateImportJob(context.Background(), Actor{UserID: "user-1"}, "project-1", CreateImportJobInput{
				SourceFileName: "story.fb2",
				MIMEType:       "application/x-fictionbook+xml",
				SizeBytes:      int64(len(payload)),
				FileBytes:      []byte(payload),
				IdempotencyKey: "race-key-1",
				RequestMethod:  http.MethodPost,
				RequestPath:    "/projects/project-1/imports",
				ImportMode:     ImportModeNewBook,
			})

			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("CreateImportJob error = %v, want %v", err, tt.wantErr)
			}
			if tt.wantErr == nil && job.ID != "existing-job" {
				t.Fatalf("job ID = %q, want existing-job", job.ID)
			}
			if store.importJobIdempotencyHits != 2 {
				t.Fatalf("idempotency lookups = %d, want precheck and conflict replay lookup", store.importJobIdempotencyHits)
			}
			if len(store.enqueuedImports) != 0 {
				t.Fatalf("conflict replay enqueued duplicate imports: %d", len(store.enqueuedImports))
			}
			if _, exists := store.importFiles["storage_generated_1"]; exists {
				t.Fatalf("conflict replay left generated upload storage_generated_1")
			}
			if _, exists := store.importFiles["existing-storage"]; !exists {
				t.Fatalf("conflict replay removed existing referenced upload")
			}
			if len(store.importFiles) != 1 {
				t.Fatalf("conflict replay import files = %d, want only original upload", len(store.importFiles))
			}
		})
	}
}

func TestCreateImportJobDoesNotPoisonIdempotencyAfterEnqueueFailure(t *testing.T) {
	const firstPayload = "<FictionBook>enqueue retry</FictionBook>"
	enqueueErr := errors.New("enqueue failed")
	store := NewMemoryStoreForTest()
	store.projectRoles[roleKey("user-1", "project-1")] = projects.RoleEditor
	store.enqueueImportJobErr = enqueueErr
	service := NewService(store, StaticIDsForTest(), StaticClockForTest())
	input := CreateImportJobInput{
		SourceFileName: "story.fb2",
		MIMEType:       "application/x-fictionbook+xml",
		SizeBytes:      int64(len(firstPayload)),
		FileBytes:      []byte(firstPayload),
		IdempotencyKey: "enqueue-key-1",
		RequestMethod:  http.MethodPost,
		RequestPath:    "/projects/project-1/imports",
		ImportMode:     ImportModeNewBook,
	}

	_, err := service.CreateImportJob(context.Background(), Actor{UserID: "user-1"}, "project-1", input)
	if !errors.Is(err, enqueueErr) {
		t.Fatalf("first CreateImportJob error = %v, want enqueue failure", err)
	}
	if len(store.enqueuedImports) != 0 {
		t.Fatalf("failed enqueue recorded imports: %d", len(store.enqueuedImports))
	}

	store.enqueueImportJobErr = nil
	job, err := service.CreateImportJob(context.Background(), Actor{UserID: "user-1"}, "project-1", input)
	if err != nil {
		t.Fatalf("retry CreateImportJob error = %v, want success", err)
	}
	if len(store.enqueuedImports) != 1 {
		t.Fatalf("retry enqueued imports = %d, want 1", len(store.enqueuedImports))
	}
	enqueued := store.enqueuedImports[0]
	if enqueued.JobID != job.ID {
		t.Fatalf("enqueued job ID = %q, want returned job %q", enqueued.JobID, job.ID)
	}
	if _, exists := store.importFiles[enqueued.StorageKey]; !exists {
		t.Fatalf("enqueued storage key %q is not stored", enqueued.StorageKey)
	}
	if len(store.importFiles) != 1 {
		t.Fatalf("stored import files after retry = %d, want only accepted upload", len(store.importFiles))
	}

	changedPayload := "<FictionBook>changed after retry</FictionBook>"
	input.SizeBytes = int64(len(changedPayload))
	input.FileBytes = []byte(changedPayload)
	_, err = service.CreateImportJob(context.Background(), Actor{UserID: "user-1"}, "project-1", input)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("changed payload CreateImportJob error = %v, want ErrConflict", err)
	}
	if len(store.enqueuedImports) != 1 || len(store.importFiles) != 1 {
		t.Fatalf("mismatch mutated accepted work: files=%d enqueued=%d", len(store.importFiles), len(store.enqueuedImports))
	}
	if _, exists := store.importFiles[enqueued.StorageKey]; !exists {
		t.Fatalf("mismatch removed accepted upload %q", enqueued.StorageKey)
	}
}

func TestCreateImportJobValidatesCommandInput(t *testing.T) {
	tests := []struct {
		name        string
		role        projects.Role
		fileName    string
		mimeType    string
		sizeBytes   int64
		idempotency string
		wantErr     error
	}{
		{
			name:        "viewer cannot import",
			role:        projects.RoleViewer,
			fileName:    "story.fb2",
			mimeType:    "application/x-fictionbook+xml",
			sizeBytes:   1,
			idempotency: "import-request-1",
			wantErr:     ErrForbidden,
		},
		{
			name:        "unsupported extension",
			role:        projects.RoleEditor,
			fileName:    "story.pdf",
			mimeType:    "application/x-fictionbook+xml",
			sizeBytes:   1,
			idempotency: "import-request-1",
			wantErr:     ErrImportUnsupportedMediaType,
		},
		{
			name:        "unsupported mime",
			role:        projects.RoleEditor,
			fileName:    "story.fb2",
			mimeType:    "application/pdf",
			sizeBytes:   1,
			idempotency: "import-request-1",
			wantErr:     ErrImportUnsupportedMediaType,
		},
		{
			name:        "file too large",
			role:        projects.RoleEditor,
			fileName:    "story.fb2",
			mimeType:    "application/x-fictionbook+xml",
			sizeBytes:   MaxImportFileSizeBytes + 1,
			idempotency: "import-request-1",
			wantErr:     ErrImportFileTooLarge,
		},
		{
			name:        "missing idempotency key",
			role:        projects.RoleEditor,
			fileName:    "story.fb2",
			mimeType:    "application/x-fictionbook+xml",
			sizeBytes:   1,
			idempotency: "",
			wantErr:     ErrValidation,
		},
		{
			name:        "empty file",
			role:        projects.RoleEditor,
			fileName:    "story.fb2",
			mimeType:    "application/x-fictionbook+xml",
			sizeBytes:   0,
			idempotency: "import-request-1",
			wantErr:     ErrImportNoContent,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewMemoryStoreForTest()
			store.projectRoles[roleKey("user-1", "project-1")] = tt.role
			service := NewService(store, StaticIDsForTest(), StaticClockForTest())

			_, err := service.CreateImportJob(context.Background(), Actor{UserID: "user-1"}, "project-1", CreateImportJobInput{
				SourceFileName: tt.fileName,
				MIMEType:       tt.mimeType,
				SizeBytes:      tt.sizeBytes,
				IdempotencyKey: tt.idempotency,
				ImportMode:     ImportModeNewBook,
			})

			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("CreateImportJob error = %v, want %v", err, tt.wantErr)
			}
			if len(store.importFiles) != 0 || len(store.importJobs) != 0 || len(store.enqueuedImports) != 0 {
				t.Fatalf("invalid command persisted import source: files=%d jobs=%d enqueued=%d", len(store.importFiles), len(store.importJobs), len(store.enqueuedImports))
			}
		})
	}
}

type fakeImportAuthService struct {
	user           auth.User
	currentUserErr error
	validateErr    error
}

func fakeImportAuthForTest(userID string) *fakeImportAuthService {
	return &fakeImportAuthService{
		user: auth.User{
			ID:            userID,
			Email:         userID + "@example.test",
			EmailVerified: true,
			DisplayName:   "Writer",
			CreatedAt:     time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC),
		},
	}
}

func (s *fakeImportAuthService) CurrentUser(context.Context, string) (auth.User, error) {
	if s.currentUserErr != nil {
		return auth.User{}, s.currentUserErr
	}
	return s.user, nil
}

func (s *fakeImportAuthService) ValidateCSRF(context.Context, string, string) error {
	return s.validateErr
}

func (s *fakeImportAuthService) AuthenticateWrite(context.Context, string, string) (auth.User, auth.SessionWriteProof, error) {
	if s.currentUserErr != nil {
		return auth.User{}, auth.SessionWriteProof{}, s.currentUserErr
	}
	if s.validateErr != nil {
		return auth.User{}, auth.SessionWriteProof{}, s.validateErr
	}
	return s.user, auth.SessionWriteProof{SessionHash: "session-hash", CSRFSecretHash: "csrf-hash"}, nil
}

const importAllowedOriginForTest = "https://app.example.test"

func newImportHandlerForTest(service *Service, authService *fakeImportAuthService) http.Handler {
	router := chi.NewRouter()
	NewHandlers(service, authService, []string{importAllowedOriginForTest}).Mount(router)
	return router
}

func newAuthenticatedImportRequestForTest(t *testing.T, method, target, body string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.AddCookie(&http.Cookie{
		Name:  auth.SessionCookieName,
		Value: "session-for-test",
	})
	return req
}

func newAuthenticatedImportRequestWithBodyForTest(method, target string, body io.Reader) *http.Request {
	req := httptest.NewRequest(method, target, body)
	req.AddCookie(&http.Cookie{
		Name:  auth.SessionCookieName,
		Value: "session-for-test",
	})
	return req
}

func newMultipartImportRequestForTest(t *testing.T, target, fileName, mimeType, payload string) *http.Request {
	t.Helper()
	return newMultipartImportRequestWithFieldsForTest(t, target, fileName, mimeType, payload, map[string]string{
		"metadata.title":     "Imported title",
		"options.importMode": string(ImportModeNewBook),
	})
}

func newMultipartImportRequestWithFieldsForTest(t *testing.T, target, fileName, mimeType, payload string, fields map[string]string) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	partHeader := make(textproto.MIMEHeader)
	partHeader.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename="%s"`, fileName))
	partHeader.Set("Content-Type", mimeType)
	part, err := writer.CreatePart(partHeader)
	if err != nil {
		t.Fatalf("create multipart file part: %v", err)
	}
	if _, err := io.WriteString(part, payload); err != nil {
		t.Fatalf("write multipart file part: %v", err)
	}
	for name, value := range fields {
		if err := writer.WriteField(name, value); err != nil {
			t.Fatalf("write multipart field %s: %v", name, err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	req := newAuthenticatedImportRequestForTest(t, http.MethodPost, target, body.String())
	req.Header.Set("Content-Type", writer.FormDataContentType())
	return req
}

func serveImportForTest(t *testing.T, handler http.Handler, idempotencyKey, payload string) *httptest.ResponseRecorder {
	t.Helper()
	req := newMultipartImportRequestForTest(t, "/projects/project-1/imports", "story.fb2", "application/x-fictionbook+xml", payload)
	req.Header.Set("Origin", importAllowedOriginForTest)
	req.Header.Set("X-CSRF-Token", "csrf-for-test")
	req.Header.Set("Idempotency-Key", idempotencyKey)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func decodeImportJobIDForTest(t *testing.T, body *bytes.Buffer) string {
	t.Helper()
	var response struct {
		JobID string `json:"jobId"`
	}
	if err := json.NewDecoder(body).Decode(&response); err != nil {
		t.Fatalf("decode import job response: %v", err)
	}
	return response.JobID
}

func assertImportProblemForTest(t *testing.T, rec *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, status, rec.Body.String())
	}
	var problem struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&problem); err != nil {
		t.Fatalf("decode problem response: %v", err)
	}
	if problem.Code != code {
		t.Fatalf("problem code = %q, want %q", problem.Code, code)
	}
}

func assertStringSliceEqualForTest(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("slice length = %d, want %d; got %#v", len(got), len(want), got)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("slice[%d] = %q, want %q; got %#v", index, got[index], want[index], got)
		}
	}
}

func assertStringEqualForTest(t *testing.T, got, want string) {
	t.Helper()
	if got != want {
		t.Fatalf("value = %q, want %q", got, want)
	}
}

type readCountingBodyForTest struct {
	reads int
}

func (b *readCountingBodyForTest) Read([]byte) (int, error) {
	b.reads++
	return 0, errors.New("body should not be read before import authorization")
}
