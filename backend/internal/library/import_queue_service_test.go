package library

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"strings"
	"testing"

	"canonkeeper/backend/internal/auth"
	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/projects"
)

func TestUploadImportQueueItemPersistsDeterministicPreflightFailure(t *testing.T) {
	service, store := newImportQueueServiceForTest("user-1", projects.RoleEditor)
	queue := createImportQueueForTest(t, service)

	item, err := service.UploadImportQueueItem(context.Background(), UploadImportQueueItemInput{
		ProjectID: "project-1", QueueID: queue.ID, ItemID: queue.Items[0].ID, ActorUserID: "user-1",
		FileName: "volume-4.epub", MIMEType: "application/epub+zip", Content: []byte("not-a-zip"),
		IdempotencyKey: "idem-queue-item-4", SessionWriteProof: validImportQueueWriteProofForTest(),
	})
	if err != nil {
		t.Fatalf("UploadImportQueueItem() error = %v", err)
	}
	if item.PreflightStatus != ImportQueueItemInvalid {
		t.Fatalf("PreflightStatus = %q, want %q", item.PreflightStatus, ImportQueueItemInvalid)
	}
	var preflightError struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(item.PreflightErrorJSON, &preflightError); err != nil {
		t.Fatalf("decode preflight error: %v", err)
	}
	if preflightError.Code != "invalid_epub_container" {
		t.Fatalf("preflight code = %q, want invalid_epub_container", preflightError.Code)
	}
	if item.ImportJobID != "" {
		t.Fatalf("ImportJobID = %q, want empty", item.ImportJobID)
	}
	if item.StorageKey == "" || item.SizeBytes != int64(len("not-a-zip")) {
		t.Fatalf("invalid upload was not persisted: %#v", item)
	}
	if len(store.importJobs) != 0 {
		t.Fatalf("invalid preflight created jobs = %d", len(store.importJobs))
	}
}

func TestUploadImportQueueItemAcceptsProductionParsers(t *testing.T) {
	document := `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>Imported</w:t></w:r></w:p></w:body></w:document>`
	tests := []struct {
		name     string
		fileName string
		mimeType string
		content  []byte
	}{
		{name: "fb2", fileName: "volume.fb2", mimeType: "application/x-fictionbook+xml", content: readFileForImportQueueTest(t, "testdata/simple.fb2")},
		{name: "epub", fileName: "volume.epub", mimeType: "application/epub+zip", content: readFileForImportQueueTest(t, "testdata/simple.epub")},
		{name: "docx", fileName: "volume.docx", mimeType: "application/vnd.openxmlformats-officedocument.wordprocessingml.document", content: buildDOCXForTest(t, docxEntryForTest{name: "word/document.xml", body: document})},
		// specs/2026-11-10-book-file-kinds.md K1, K6.2: the new kinds pass the same preflight; a text named by nothing too.
		{name: "txt", fileName: "volume.txt", mimeType: "", content: []byte(textBookForTest)},
		{name: "md", fileName: "volume.md", mimeType: "text/markdown", content: []byte("# Глава 1\n\nКорабль *вошёл* в гавань.\n")},
		{name: "rtf", fileName: "volume.rtf", mimeType: "text/rtf", content: []byte(`{\rtf1\ansi Imported\par}`)},
		{name: "odt", fileName: "volume.odt", mimeType: "application/vnd.oasis.opendocument.text", content: odtForTest(t, "", `<text:p>Imported</text:p>`, "", "")},
		{name: "fb2.zip", fileName: "volume.fb2.zip", mimeType: "application/x-zip-compressed", content: zipForTest(t, zipEntryForTest{name: "volume.fb2", body: readFileForImportQueueTest(t, "testdata/simple.fb2")})},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service, _ := newImportQueueServiceForTest("user-1", projects.RoleEditor)
			queue := createImportQueueForTest(t, service)
			item, err := service.UploadImportQueueItem(context.Background(), UploadImportQueueItemInput{
				ProjectID: "project-1", QueueID: queue.ID, ItemID: queue.Items[0].ID, ActorUserID: "user-1",
				FileName: test.fileName, MIMEType: test.mimeType, Content: test.content,
				IdempotencyKey: "idem-queue-" + test.name, SessionWriteProof: validImportQueueWriteProofForTest(),
			})
			if err != nil {
				t.Fatalf("UploadImportQueueItem() error = %v", err)
			}
			if item.PreflightStatus != ImportQueueItemReady || len(item.PreflightErrorJSON) != 0 {
				t.Fatalf("preflight = %q/%s, want ready without error", item.PreflightStatus, item.PreflightErrorJSON)
			}
			if item.SHA256 != sha256Hex(test.content) || item.SizeBytes != int64(len(test.content)) {
				t.Fatalf("uploaded identity = %q/%d", item.SHA256, item.SizeBytes)
			}
		})
	}
}

func TestUploadImportQueueItemPreflightClassifiesDeterministicProblems(t *testing.T) {
	tests := []struct {
		name     string
		fileName string
		mimeType string
		content  string
		code     string
	}{
		{name: "mime mismatch", fileName: "volume.fb2", mimeType: "application/epub+zip", content: "<FictionBook/>", code: "import.content_mismatch"},
		{name: "empty usable content", fileName: "volume.fb2", mimeType: "application/x-fictionbook+xml", content: "<FictionBook></FictionBook>", code: "empty_import_content"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service, _ := newImportQueueServiceForTest("user-1", projects.RoleEditor)
			queue := createImportQueueForTest(t, service)
			item, err := service.UploadImportQueueItem(context.Background(), UploadImportQueueItemInput{
				ProjectID: "project-1", QueueID: queue.ID, ItemID: queue.Items[0].ID, ActorUserID: "user-1",
				FileName: test.fileName, MIMEType: test.mimeType, Content: []byte(test.content),
				IdempotencyKey: "idem-queue-" + test.name, SessionWriteProof: validImportQueueWriteProofForTest(),
			})
			if err != nil {
				t.Fatalf("UploadImportQueueItem() error = %v", err)
			}
			if item.PreflightStatus != ImportQueueItemInvalid || !strings.Contains(string(item.PreflightErrorJSON), test.code) {
				t.Fatalf("preflight = %q/%s, want invalid %q", item.PreflightStatus, item.PreflightErrorJSON, test.code)
			}
		})
	}

	service, _ := newImportQueueServiceForTest("user-1", projects.RoleEditor)
	queue := createImportQueueForTest(t, service)
	_, err := service.UploadImportQueueItem(context.Background(), UploadImportQueueItemInput{
		ProjectID: "project-1", QueueID: queue.ID, ItemID: queue.Items[0].ID, ActorUserID: "user-1",
		FileName: "large.epub", MIMEType: "application/epub+zip", Content: make([]byte, MaxImportFileSizeBytes+1),
		IdempotencyKey: "idem-queue-oversize", SessionWriteProof: validImportQueueWriteProofForTest(),
	})
	if !errors.Is(err, ErrImportFileTooLarge) {
		t.Fatalf("oversize error = %v, want %v", err, ErrImportFileTooLarge)
	}
}

func TestImportQueueServiceIdempotencyAndVersion(t *testing.T) {
	service, store := newImportQueueServiceForTest("user-1", projects.RoleEditor)
	input := CreateImportQueueInput{
		ProjectID: "project-1", ActorUserID: "user-1", IdempotencyKey: "idem-queue-create-1",
		RequestMethod: http.MethodPost, RequestPath: "/api/v1/projects/project-1/import-queues",
		SessionWriteProof: validImportQueueWriteProofForTest(),
		Items:             []CreateImportQueueItemInput{{ClientItemID: "client-1", Position: 1, SourceFileName: "volume-4.fb2"}},
	}
	first, err := service.CreateImportQueue(context.Background(), input)
	if err != nil {
		t.Fatalf("CreateImportQueue() error = %v", err)
	}
	replay, err := service.CreateImportQueue(context.Background(), input)
	if err != nil || replay.ID != first.ID {
		t.Fatalf("CreateImportQueue(replay) = (%#v, %v), want original", replay, err)
	}
	input.Items[0].SourceFileName = "changed.fb2"
	if _, err := service.CreateImportQueue(context.Background(), input); !errors.Is(err, ErrConflict) {
		t.Fatalf("changed idempotent payload error = %v, want %v", err, ErrConflict)
	}
	if len(store.queues) != 1 {
		t.Fatalf("idempotent create persisted queues = %d, want 1", len(store.queues))
	}

	queue, err := service.GetImportQueue(context.Background(), GetImportQueueInput{ProjectID: "project-1", QueueID: first.ID, ActorUserID: "user-1"})
	if err != nil {
		t.Fatalf("GetImportQueue() error = %v", err)
	}
	upload := UploadImportQueueItemInput{
		ProjectID: "project-1", QueueID: queue.ID, ItemID: queue.Items[0].ID, ActorUserID: "user-1",
		FileName: "volume-4.fb2", MIMEType: "application/x-fictionbook+xml", Content: readFileForImportQueueTest(t, "testdata/simple.fb2"),
		IdempotencyKey: "idem-queue-upload-1", SessionWriteProof: validImportQueueWriteProofForTest(),
	}
	uploaded, err := service.UploadImportQueueItem(context.Background(), upload)
	if err != nil {
		t.Fatalf("UploadImportQueueItem() error = %v", err)
	}
	uploadReplay, err := service.UploadImportQueueItem(context.Background(), upload)
	if err != nil || uploadReplay.ID != uploaded.ID || uploadReplay.QueueVersion != uploaded.QueueVersion {
		t.Fatalf("upload replay = (%#v, %v), want original", uploadReplay, err)
	}
	if store.putImportQueueItemCalls != 1 {
		t.Fatalf("upload replay persisted %d times, want 1", store.putImportQueueItemCalls)
	}
	restartedService := NewService(store, StaticIDsForTest(), StaticClockForTest())
	restartedUpload, err := restartedService.UploadImportQueueItem(context.Background(), upload)
	if err != nil || restartedUpload.ID != uploaded.ID || restartedUpload.QueueVersion != uploaded.QueueVersion {
		t.Fatalf("upload replay after service restart = (%#v, %v), want original", restartedUpload, err)
	}
	upload.Content = []byte("<FictionBook>changed</FictionBook>")
	if _, err := service.UploadImportQueueItem(context.Background(), upload); !errors.Is(err, ErrConflict) {
		t.Fatalf("changed upload idempotency error = %v, want %v", err, ErrConflict)
	}
}

// specs/2026-11-11-ux7-upload.md UP3.2: in a running queue the file of a volume that did not read is «Заменить файл» —
// the same operation, checked as any file, admitted with its request; «Пропустить» takes the volume out. A reader does
// neither.
func TestARunningQueueTakesANewFileOrASkipOfAVolumeThatDidNotRead(t *testing.T) {
	service, store := newImportQueueServiceForTest("user-1", projects.RoleEditor)
	queue := createImportQueueForTest(t, service)
	running := store.queues[queue.ID]
	running.Status = ImportQueueRunning
	running.Items[0].ImportJobID, running.Items[0].ImportJobStatus = "job-1", string(jobs.StatusFailed)

	replaced, err := service.UploadImportQueueItem(context.Background(), UploadImportQueueItemInput{
		ProjectID: "project-1", QueueID: queue.ID, ItemID: queue.Items[0].ID, ActorUserID: "user-1",
		FileName: "volume-4-fixed.fb2", MIMEType: "application/x-fictionbook+xml", Content: readFileForImportQueueTest(t, "testdata/simple.fb2"),
		IdempotencyKey: "idem-queue-replace-1", RequestMethod: http.MethodPost,
		RequestPath:       "/projects/project-1/import-queues/" + queue.ID + "/items",
		SessionWriteProof: validImportQueueWriteProofForTest(),
	})
	if err != nil {
		t.Fatalf("UploadImportQueueItem(running) error = %v", err)
	}
	if store.lastReplaceInput == nil || store.putImportQueueItemCalls != 0 ||
		store.lastReplaceInput.PreflightStatus != ImportQueueItemReady || store.lastReplaceInput.RequestFingerprint == "" ||
		replaced.ImportJobStatus != string(jobs.StatusQueued) || replaced.SourceFileName != "volume-4-fixed.fb2" {
		t.Fatalf("replacement = %#v with store input %#v", replaced, store.lastReplaceInput)
	}

	running.Items[0].ImportJobStatus = string(jobs.StatusFailed)
	skipped, err := service.SkipImportQueueItem(context.Background(), SkipImportQueueItemInput{
		ProjectID: "project-1", QueueID: queue.ID, ItemID: queue.Items[0].ID, ActorUserID: "user-1",
		SessionWriteProof: validImportQueueWriteProofForTest(),
	})
	if err != nil || skipped.Items[0].SkippedAt == nil {
		t.Fatalf("SkipImportQueueItem() = %#v, %v", skipped, err)
	}

	reader, _ := newImportQueueServiceForTest("reader", projects.RoleViewer)
	if _, err := reader.SkipImportQueueItem(context.Background(), SkipImportQueueItemInput{
		ProjectID: "project-1", QueueID: queue.ID, ItemID: queue.Items[0].ID, ActorUserID: "reader",
		SessionWriteProof: validImportQueueWriteProofForTest(),
	}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("skip by a reader = %v, want ErrForbidden", err)
	}
}

func TestImportQueueHTTPSurfaceRequiresAuthAndCSRF(t *testing.T) {
	tests := []struct {
		name   string
		role   projects.Role
		user   string
		status int
	}{
		{name: "viewer", role: projects.RoleViewer, user: "viewer", status: http.StatusForbidden},
		{name: "non member", role: projects.RoleViewer, user: "non-member", status: http.StatusForbidden},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service, _ := newImportQueueServiceForTest(test.user, test.role)
			handler := newImportHandlerForTest(service, fakeImportAuthForTest(test.user))
			req := newImportQueueJSONRequestForTest(t, http.MethodPost, "/projects/project-1/import-queues", `{"items":[{"clientItemId":"client-1","position":1,"sourceFileName":"volume-4.fb2"}]}`)
			req.Header.Set("Origin", importAllowedOriginForTest)
			req.Header.Set("X-CSRF-Token", "csrf-for-test")
			req.Header.Set("Idempotency-Key", "idem-queue-"+test.name)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != test.status {
				t.Fatalf("status = %d, want %d; body: %s", rec.Code, test.status, rec.Body.String())
			}
		})
	}

	service, _ := newImportQueueServiceForTest("user-1", projects.RoleEditor)
	authService := fakeImportAuthForTest("user-1")
	authService.validateErr = auth.ErrInvalidCSRF
	handler := newImportHandlerForTest(service, authService)
	req := newImportQueueJSONRequestForTest(t, http.MethodPost, "/projects/project-1/import-queues", `{"items":[{"clientItemId":"client-1","position":1,"sourceFileName":"volume-4.fb2"}]}`)
	req.Header.Set("Origin", importAllowedOriginForTest)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("missing CSRF status = %d, want %d; body: %s", rec.Code, http.StatusForbidden, rec.Body.String())
	}
}

func TestImportQueueHTTPRoutesCreateListGetUploadStartSkipAndCancel(t *testing.T) {
	service, store := newImportQueueServiceForTest("user-1", projects.RoleEditor)
	handler := newImportHandlerForTest(service, fakeImportAuthForTest("user-1"))

	createRec := serveImportQueueJSONForTest(t, handler, http.MethodPost, "/projects/project-1/import-queues", `{"items":[{"clientItemId":"client-1","position":1,"sourceFileName":"volume-4.fb2"}]}`, "idem-queue-http-create")
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want %d; body: %s", createRec.Code, http.StatusCreated, createRec.Body.String())
	}
	queue := decodeImportQueueResponseForTest(t, createRec)
	if queue.Status != "uploading" || queue.Version != 1 || len(queue.Items) != 1 {
		t.Fatalf("created queue = %#v", queue)
	}
	if createRec.Header().Get("Location") != "/api/v1/projects/project-1/import-queues/"+queue.ID {
		t.Fatalf("Location = %q", createRec.Header().Get("Location"))
	}

	listRec := serveImportQueueJSONForTest(t, handler, http.MethodGet, "/projects/project-1/import-queues", "", "")
	if listRec.Code != http.StatusOK || !strings.Contains(listRec.Body.String(), queue.ID) {
		t.Fatalf("list status/body = %d/%s", listRec.Code, listRec.Body.String())
	}
	getRec := serveImportQueueJSONForTest(t, handler, http.MethodGet, "/projects/project-1/import-queues/"+queue.ID, "", "")
	if getRec.Code != http.StatusOK || getRec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("get status/cache = %d/%q", getRec.Code, getRec.Header().Get("Cache-Control"))
	}

	uploadRec := serveImportQueueUploadForTest(t, handler, queue.Items[0].ID, "volume-4.fb2", "application/x-fictionbook+xml", readFileForImportQueueTest(t, "testdata/simple.fb2"), "idem-queue-http-upload")
	if uploadRec.Code != http.StatusOK {
		t.Fatalf("upload status = %d, want %d; body: %s", uploadRec.Code, http.StatusOK, uploadRec.Body.String())
	}
	uploaded := decodeImportQueueResponseForTest(t, uploadRec)
	if uploaded.Status != "draft" || uploaded.Version != 2 || uploaded.Items[0].PreflightStatus != "ready" {
		t.Fatalf("uploaded queue = %#v", uploaded)
	}

	// The order of the volumes is the order of the rows: there is no save of numbers before the start (UP2.3).
	if saveRec := serveImportQueueJSONForTest(t, handler, http.MethodPatch, "/projects/project-1/import-queues/"+queue.ID, `{"expectedVersion":2,"items":[]}`, ""); saveRec.Code != http.StatusMethodNotAllowed && saveRec.Code != http.StatusNotFound {
		t.Fatalf("save of numbers status = %d, want no such operation", saveRec.Code)
	}

	startRec := serveImportQueueJSONForTest(t, handler, http.MethodPost, "/projects/project-1/import-queues/"+queue.ID+"/start", `{"expectedVersion":2}`, "idem-queue-http-start")
	if startRec.Code != http.StatusAccepted {
		t.Fatalf("start status = %d, want %d; body: %s", startRec.Code, http.StatusAccepted, startRec.Body.String())
	}
	started := decodeImportQueueResponseForTest(t, startRec)
	if started.Status != "running" || started.Version != 3 {
		t.Fatalf("started queue = %#v", started)
	}
	if store.lastStartInput == nil ||
		store.lastStartInput.ProjectID != "project-1" ||
		store.lastStartInput.QueueID != queue.ID ||
		store.lastStartInput.ActorUserID != "user-1" ||
		store.lastStartInput.ExpectedVersion != 2 ||
		store.lastStartInput.IdempotencyKey != "idem-queue-http-start" ||
		store.lastStartInput.RequestMethod != http.MethodPost ||
		store.lastStartInput.RequestPath != "/projects/project-1/import-queues/"+queue.ID+"/start" ||
		!store.lastStartInput.SessionWriteProof.Valid() ||
		strings.TrimSpace(store.lastStartInput.RequestFingerprint) == "" {
		t.Fatalf("atomic start input = %#v", store.lastStartInput)
	}

	store.queues[queue.ID].Items[0].ImportJobStatus = string(jobs.StatusFailed)
	skipRec := serveImportQueueJSONForTest(t, handler, http.MethodDelete, "/projects/project-1/import-queues/"+queue.ID+"/items/"+queue.Items[0].ID, "", "")
	if skipRec.Code != http.StatusOK || !strings.Contains(skipRec.Body.String(), `"processingStatus":"skipped"`) {
		t.Fatalf("skip status/body = %d/%s", skipRec.Code, skipRec.Body.String())
	}

	cancelRec := serveImportQueueJSONForTest(t, handler, http.MethodDelete, "/projects/project-1/import-queues/"+queue.ID, "", "")
	if cancelRec.Code != http.StatusOK || !strings.Contains(cancelRec.Body.String(), `"status":"canceled"`) {
		t.Fatalf("cancel status/body = %d/%s", cancelRec.Code, cancelRec.Body.String())
	}
}

func TestImportQueueProjectionMarksOnlyCurrentUnfinishedItemImporting(t *testing.T) {
	queue := ImportQueue{
		ID: "queue-1", ProjectID: "project-1", Status: ImportQueueRunning,
		Items: []ImportQueueItem{
			{ID: "item-1", Position: 1, ImportJobID: "job-1", ImportJobStatus: string(jobs.StatusRunning), PreflightStatus: ImportQueueItemReady},
			{ID: "item-2", Position: 2, ImportJobID: "job-2", ImportJobStatus: string(jobs.StatusQueued), PreflightStatus: ImportQueueItemReady},
		},
	}
	response, err := importQueueResponseFor(queue, nil)
	if err != nil {
		t.Fatal(err)
	}
	if response.CurrentItemID == nil || *response.CurrentItemID != "item-1" {
		t.Fatalf("current item = %#v, want item-1", response.CurrentItemID)
	}
	if response.Items[0].ProcessingStatus != "importing" || response.Items[1].ProcessingStatus != "queued" {
		t.Fatalf("processing statuses = %s/%s, want importing/queued", response.Items[0].ProcessingStatus, response.Items[1].ProcessingStatus)
	}
}

func TestImportQueueProjectionKeepsDraftBookCurrentUntilReady(t *testing.T) {
	queue := ImportQueue{
		ID: "queue-1", ProjectID: "project-1", Status: ImportQueueRunning,
		Items: []ImportQueueItem{
			{ID: "item-1", Position: 1, ImportJobID: "job-1", ImportJobStatus: string(jobs.StatusSucceeded), BookID: "book-1", PreflightStatus: ImportQueueItemReady},
			{ID: "item-2", Position: 2, ImportJobID: "job-2", ImportJobStatus: string(jobs.StatusQueued), PreflightStatus: ImportQueueItemReady},
		},
	}

	response, err := importQueueResponseFor(queue, map[string]BookStatus{"book-1": BookStatusDraft})
	if err != nil {
		t.Fatal(err)
	}
	if response.CurrentItemID == nil || *response.CurrentItemID != "item-1" {
		t.Fatalf("current item = %#v, want item-1", response.CurrentItemID)
	}
	if response.Items[0].ProcessingStatus != "indexing" || response.Items[1].ProcessingStatus != "queued" {
		t.Fatalf("processing statuses = %s/%s, want indexing/queued", response.Items[0].ProcessingStatus, response.Items[1].ProcessingStatus)
	}
}

func TestImportQueueProjectionKeepsReadyBooksSucceededAndFailedCurrentFailed(t *testing.T) {
	base := func(status ImportQueueStatus) ImportQueue {
		return ImportQueue{
			ID: "queue-1", ProjectID: "project-1", Status: status,
			Items: []ImportQueueItem{
				{ID: "item-1", Position: 1, ImportJobID: "job-1", ImportJobStatus: string(jobs.StatusSucceeded), BookID: "book-1", PreflightStatus: ImportQueueItemReady},
				{ID: "item-2", Position: 2, ImportJobID: "job-2", ImportJobStatus: string(jobs.StatusSucceeded), BookID: "book-2", PreflightStatus: ImportQueueItemReady},
			},
		}
	}

	ready, err := importQueueResponseFor(base(ImportQueueRunning), map[string]BookStatus{
		"book-1": BookStatusReady, "book-2": BookStatusReady,
	})
	if err != nil {
		t.Fatal(err)
	}
	if ready.CurrentItemID != nil || ready.Items[0].ProcessingStatus != "succeeded" || ready.Items[1].ProcessingStatus != "succeeded" {
		t.Fatalf("ready projection = %#v", ready)
	}

	failedQueue := base(ImportQueueFailed)
	failedQueue.Items[0].ImportJobStatus = string(jobs.StatusFailed)
	failedQueue.Items[0].ImportJobErrorJSON, err = jobs.EncodeError(jobs.Error{Code: "worker.handler_failed", Message: "handler failed"})
	if err != nil {
		t.Fatal(err)
	}
	failed, err := importQueueResponseFor(failedQueue, map[string]BookStatus{
		"book-1": BookStatusDraft, "book-2": BookStatusReady,
	})
	if err != nil {
		t.Fatal(err)
	}
	if failed.CurrentItemID == nil || *failed.CurrentItemID != "item-1" ||
		failed.Items[0].ProcessingStatus != "failed" || failed.Items[1].ProcessingStatus != "succeeded" {
		t.Fatalf("failed projection = %#v", failed)
	}
}

// UP1.2, UP3.2: a file that did not pass the check is a volume that waits for the author, named by its reason.
func TestImportQueueProjectionShowsAFileThatDidNotPassAsWaiting(t *testing.T) {
	response, err := importQueueResponseFor(ImportQueue{
		ID: "queue-1", ProjectID: "project-1", Status: ImportQueueRunning, Items: []ImportQueueItem{{
			ID: "item-1", PreflightStatus: ImportQueueItemInvalid,
			PreflightErrorJSON: []byte(`{"code":"invalid_epub_container","message":"EPUB container is invalid."}`),
		}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	item := response.Items[0]
	if item.ProcessingStatus != "failed" || item.FailedStep == nil || *item.FailedStep != "preflight" ||
		item.PreflightError == nil || item.PreflightError.Code != "invalid_epub_container" {
		t.Fatalf("projected item = %#v, want a waiting volume with its reason", item)
	}
}

func TestImportQueueProjectionUsesExactJobRetryCount(t *testing.T) {
	tests := []struct {
		name string
		item ImportQueueItem
	}{
		{
			name: "import",
			item: ImportQueueItem{ID: "item-1", ImportJobID: "import-1", ImportJobStatus: string(jobs.StatusQueued), ImportJobRetryCount: 1},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			test.item.PreflightStatus = ImportQueueItemReady
			response, err := importQueueResponseFor(ImportQueue{
				ID: "queue-1", ProjectID: "project-1", Status: ImportQueueRunning, Items: []ImportQueueItem{test.item},
			}, nil)
			if err != nil {
				t.Fatal(err)
			}
			if got := response.Items[0].ProcessingStatus; got != "retrying" {
				t.Fatalf("processing status = %q, want retrying", got)
			}
		})
	}
}

// An expired import Job carries no error by design; the queue lists the volume as failed
// instead of failing the whole list.
func TestImportQueueProjectionShowsExpiredImportAsFailed(t *testing.T) {
	response, err := importQueueResponseFor(ImportQueue{
		ID: "queue-1", ProjectID: "project-1", Status: ImportQueueFailed, Items: []ImportQueueItem{{
			ID: "item-1", PreflightStatus: ImportQueueItemReady, ImportJobID: "import-1", ImportJobStatus: string(jobs.StatusExpired),
		}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	item := response.Items[0]
	if item.ProcessingStatus != "failed" || item.FailureClass == nil || *item.FailureClass != "infrastructure" {
		t.Fatalf("expired item = %#v, want a failed volume the author can import again", item)
	}
}

// An imported volume stays "indexing" until its draft text index makes the book ready; cards
// do not hold the queue (R2.4).
func TestImportQueueProjectionShowsIndexingUntilBookReady(t *testing.T) {
	queue := ImportQueue{
		ID: "queue-1", ProjectID: "project-1", Status: ImportQueueRunning,
		Items: []ImportQueueItem{
			{
				ID: "item-1", Position: 1, BookID: "book-1", PreflightStatus: ImportQueueItemReady,
				ImportJobID: "import-1", ImportJobStatus: string(jobs.StatusSucceeded),
			},
			{
				ID: "item-2", Position: 2, PreflightStatus: ImportQueueItemReady,
				ImportJobID: "import-2", ImportJobStatus: string(jobs.StatusQueued),
			},
		},
	}
	response, err := importQueueResponseFor(queue, map[string]BookStatus{"book-1": BookStatusDraft})
	if err != nil {
		t.Fatal(err)
	}
	if response.CurrentItemID == nil || *response.CurrentItemID != "item-1" ||
		response.Items[0].ProcessingStatus != "indexing" || response.Items[1].ProcessingStatus != "queued" ||
		response.Items[0].Links.Status == nil || !strings.Contains(*response.Items[0].Links.Status, "bookId=book-1") {
		t.Fatalf("indexing projection = %#v", response)
	}
	ready, err := importQueueResponseFor(queue, map[string]BookStatus{"book-1": BookStatusReady})
	if err != nil {
		t.Fatal(err)
	}
	if ready.Items[0].ProcessingStatus != "succeeded" {
		t.Fatalf("ready projection = %#v", ready.Items[0])
	}
}

func TestImportQueueProjectionShowsCancelingUntilExactJobTerminal(t *testing.T) {
	queue := ImportQueue{
		ID: "queue-1", ProjectID: "project-1", Status: ImportQueueCanceled,
		Items: []ImportQueueItem{{
			ID: "item-1", Position: 1, PreflightStatus: ImportQueueItemReady,
			ImportJobID: "import-1", ImportJobStatus: string(jobs.StatusCanceling),
		}},
	}
	stopping, err := importQueueResponseFor(queue, nil)
	if err != nil {
		t.Fatal(err)
	}
	if stopping.CurrentItemID == nil || *stopping.CurrentItemID != "item-1" || stopping.Items[0].ProcessingStatus != "canceling" {
		t.Fatalf("canceling projection = %#v", stopping)
	}
	queue.Items[0].ImportJobStatus = string(jobs.StatusCanceled)
	terminal, err := importQueueResponseFor(queue, nil)
	if err != nil {
		t.Fatal(err)
	}
	if terminal.Items[0].ProcessingStatus != "canceled" {
		t.Fatalf("terminal canceled projection = %#v", terminal)
	}
}

func TestImportQueueProjectionUsesExactJobFailure(t *testing.T) {
	for _, test := range []struct {
		jobError     jobs.Error
		failureClass string
	}{
		{jobError: jobs.Error{Code: jobs.RetryExhaustedCode, Message: jobs.RetryExhaustedMessage}, failureClass: "infrastructure"},
		{jobError: jobs.Error{Code: "unexpected_deterministic_failure", Message: "contract defect"}, failureClass: "unexpected_deterministic_failure"},
	} {
		raw, err := jobs.EncodeError(test.jobError)
		if err != nil {
			t.Fatal(err)
		}
		queue := ImportQueue{
			ID: "queue-1", ProjectID: "project-1", Status: ImportQueueFailed,
			Items: []ImportQueueItem{{
				ID: "item-1", Position: 1, PreflightStatus: ImportQueueItemReady,
				ImportJobID: "import-1", ImportJobStatus: string(jobs.StatusFailed), ImportJobErrorJSON: raw,
			}},
		}
		response, err := importQueueResponseFor(queue, nil)
		if err != nil {
			t.Fatal(err)
		}
		if response.Items[0].FailureClass == nil || *response.Items[0].FailureClass != test.failureClass ||
			response.Items[0].FailedStep == nil || *response.Items[0].FailedStep != "import" {
			t.Fatalf("import failure projection = %#v", response.Items[0])
		}
	}
}

func TestImportQueueResponsesLoadLinkedBookStatuses(t *testing.T) {
	service, store := newImportQueueServiceForTest("user-1", projects.RoleEditor)
	store.memoryStoreForTest.SeedBook("project-1", "book-1")
	handler := NewHandlers(service, fakeImportAuthForTest("user-1"), []string{importAllowedOriginForTest})
	queue := ImportQueue{ID: "queue-1", ProjectID: "project-1", Items: []ImportQueueItem{
		{ID: "item-1", BookID: "book-1"},
	}}

	statuses, err := handler.importQueueBookStatuses(context.Background(), Actor{UserID: "user-1"}, []ImportQueue{queue})
	if err != nil {
		t.Fatal(err)
	}
	if statuses["book-1"] != BookStatusDraft {
		t.Fatalf("book statuses = %#v, want draft book-1", statuses)
	}
}

func newImportQueueServiceForTest(userID string, role projects.Role) (*Service, *importQueueMemoryStoreForTest) {
	memory := NewMemoryStoreForTest()
	memory.projectRoles[roleKey(userID, "project-1")] = role
	store := newImportQueueMemoryStoreForTest(memory)
	return NewService(store, StaticIDsForTest(), StaticClockForTest()), store
}

func createImportQueueForTest(t *testing.T, service *Service) ImportQueue {
	t.Helper()
	queue, err := service.CreateImportQueue(context.Background(), CreateImportQueueInput{
		ProjectID: "project-1", ActorUserID: "user-1", IdempotencyKey: "idem-queue-create",
		RequestMethod: http.MethodPost, RequestPath: "/api/v1/projects/project-1/import-queues",
		SessionWriteProof: validImportQueueWriteProofForTest(),
		Items:             []CreateImportQueueItemInput{{ClientItemID: "client-1", Position: 1, SourceFileName: "volume-4.fb2"}},
	})
	if err != nil {
		t.Fatalf("CreateImportQueue() error = %v", err)
	}
	return queue
}

func validImportQueueWriteProofForTest() auth.SessionWriteProof {
	return auth.SessionWriteProof{SessionHash: "session-hash", CSRFSecretHash: "csrf-hash"}
}

func readFileForImportQueueTest(t *testing.T, path string) []byte {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return content
}

func newImportQueueJSONRequestForTest(t *testing.T, method, target, body string) *http.Request {
	t.Helper()
	return newAuthenticatedImportRequestForTest(t, method, target, body)
}

func serveImportQueueJSONForTest(t *testing.T, handler http.Handler, method, target, body, idempotencyKey string) *httptest.ResponseRecorder {
	t.Helper()
	contentType := ""
	if body != "" {
		contentType = "application/json"
	}
	req := newAuthenticatedImportRequestWithBodyForTest(method, target, strings.NewReader(body))
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Origin", importAllowedOriginForTest)
	if method != http.MethodGet {
		req.Header.Set("X-CSRF-Token", "csrf-for-test")
	}
	if idempotencyKey != "" {
		req.Header.Set("Idempotency-Key", idempotencyKey)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func serveImportQueueUploadForTest(t *testing.T, handler http.Handler, itemID, fileName, mimeType string, content []byte, idempotencyKey string) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("itemId", itemID); err != nil {
		t.Fatalf("write itemId: %v", err)
	}
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename="%s"`, fileName))
	header.Set("Content-Type", mimeType)
	part, err := writer.CreatePart(header)
	if err != nil {
		t.Fatalf("create file part: %v", err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatalf("write file part: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	req := newAuthenticatedImportRequestWithBodyForTest(http.MethodPost, "/projects/project-1/import-queues/queue-1/items", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Origin", importAllowedOriginForTest)
	req.Header.Set("X-CSRF-Token", "csrf-for-test")
	req.Header.Set("Idempotency-Key", idempotencyKey)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

type importQueueResponseForTest struct {
	ID      string `json:"id"`
	Status  string `json:"status"`
	Version int64  `json:"version"`
	Items   []struct {
		ID              string `json:"id"`
		PreflightStatus string `json:"preflightStatus"`
	} `json:"items"`
}

func decodeImportQueueResponseForTest(t *testing.T, rec *httptest.ResponseRecorder) importQueueResponseForTest {
	t.Helper()
	var response importQueueResponseForTest
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode queue response: %v", err)
	}
	return response
}
