package library

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"strings"
	"testing"

	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/jobworker"

	"golang.org/x/text/encoding/charmap"
)

func TestImportWorkerParsesFB2PersistsBookAndSafeResult(t *testing.T) {
	storageKey := "import/file.fb2"
	metadataTitle := "Requested Import Title"
	request := importRequestForWorkerTest("job-1", storageKey, &metadataTitle)
	request.Metadata.InitialPublication = InitialPublicationPublishImported
	loader := &fakeImportLoader{
		requests: map[string]ImportJob{
			"job-1": request,
		},
		files: map[string][]byte{
			storageKey: smallFB2ForImportWorkerTest(),
		},
	}
	store := newLibraryJobStoreForTest(importJobForWorkerTest("job-1"))
	var finalizedInput FinalizeImportStoreInput
	service := &fakeImportLibraryService{onFinalize: func(input FinalizeImportStoreInput) {
		finalizedInput = input
		store.forceStatusForTest(input.JobID, jobs.StatusSucceeded)
	}}
	worker := jobworker.NewWorker(store, []jobworker.Handler{NewImportHandler(loader, service)}, log.New(io.Discard, "", 0))

	ran, err := worker.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if !ran {
		t.Fatal("RunOnce() ran = false, want true")
	}
	if loader.loadedJobID != "job-1" {
		t.Fatalf("loaded job ID = %q, want job-1", loader.loadedJobID)
	}
	if loader.loadedStorageKey != storageKey {
		t.Fatalf("loaded storage key = %q, want %q", loader.loadedStorageKey, storageKey)
	}
	if loader.loadedProjectID != "project-1" {
		t.Fatalf("loaded project ID = %q, want project-1", loader.loadedProjectID)
	}
	if loader.loadedClaim != store.jobs["job-1"].WorkerClaim {
		t.Fatalf("loaded claim = %#v, want %#v", loader.loadedClaim, store.jobs["job-1"].WorkerClaim)
	}
	if len(service.books) != 1 {
		t.Fatalf("created books = %d, want 1", len(service.books))
	}
	bookCall := service.books[0]
	if bookCall.actor.UserID != "actor-1" || bookCall.projectID != "project-1" {
		t.Fatalf("CreateBook actor/project = %#v/%q, want import request actor/project", bookCall.actor, bookCall.projectID)
	}
	if bookCall.input.Title != metadataTitle {
		t.Fatalf("CreateBook title = %q, want metadata title", bookCall.input.Title)
	}
	if len(service.chapters) != 2 {
		t.Fatalf("created chapters = %d, want 2", len(service.chapters))
	}
	if got := importWorkerParagraphCountForTest(service.chapters); got != 3 {
		t.Fatalf("created paragraph count = %d, want 3", got)
	}
	if len(service.progressPhases) != 2 || service.progressPhases[0] != jobs.ImportPhaseParsing ||
		service.progressPhases[1] != jobs.ImportPhaseSaving || len(service.savingChapterCounts) != 1 ||
		service.savingChapterCounts[0] != 2 {
		t.Fatalf("import progress = %v/%v, want parsing then saving(2)", service.progressPhases, service.savingChapterCounts)
	}
	for _, chapterCall := range service.chapters {
		if chapterCall.actor.UserID != "actor-1" || chapterCall.bookID != "book-1" {
			t.Fatalf("CreateChapter actor/book = %#v/%q, want import actor/book", chapterCall.actor, chapterCall.bookID)
		}
		for i, paragraph := range chapterCall.paragraphs {
			if paragraph.Kind != ParagraphKindParagraph {
				t.Fatalf("paragraph kind = %q, want paragraph", paragraph.Kind)
			}
			if paragraph.Order != i+1 {
				t.Fatalf("paragraph order = %d, want %d", paragraph.Order, i+1)
			}
			if paragraph.Text == nil || paragraph.Markdown != *paragraph.Text {
				t.Fatal("paragraph markdown/text were not persisted through draft input")
			}
		}
	}
	if finalizedInput.InitialPublication != InitialPublicationPublishImported {
		t.Fatalf("finalized initial publication = %q, want publish_imported", finalizedInput.InitialPublication)
	}
}

func TestParseImportBookSupportsDOCX(t *testing.T) {
	document := `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>Word paragraph</w:t></w:r></w:p></w:body></w:document>`
	data := buildDOCXForTest(t, docxEntryForTest{name: "word/document.xml", body: document})

	parsed, err := parseImportBook(data, ".DOCX")
	if err != nil {
		t.Fatalf("parseImportBook(.DOCX) error = %v", err)
	}
	if len(parsed.Chapters) != 1 || len(parsed.Chapters[0].Paragraphs) != 1 || parsed.Chapters[0].Paragraphs[0] != "Word paragraph" {
		t.Fatalf("parseImportBook(.DOCX) = %#v", parsed)
	}
}

func TestImportExpectedOrderConflictIsImmediatelyTerminal(t *testing.T) {
	assertImportOrderConflictIsImmediatelyTerminal(t, ErrImportExpectedOrderConflict, "import_expected_order_conflict")
}

func TestImportSequenceConflictIsImmediatelyTerminal(t *testing.T) {
	assertImportOrderConflictIsImmediatelyTerminal(t, ErrImportSequenceConflict, "import_sequence_conflict")
}

func assertImportOrderConflictIsImmediatelyTerminal(t *testing.T, conflict error, code string) {
	t.Helper()
	storageKey := "import/terminal-order-conflict.fb2"
	store := newLibraryJobStoreForTest(importJobForWorkerTest("job-1"))
	loader := &fakeImportLoader{
		requests: map[string]ImportJob{"job-1": importRequestForWorkerTest("job-1", storageKey, nil)},
		files:    map[string][]byte{storageKey: smallFB2ForImportWorkerTest()},
	}
	service := &fakeImportLibraryService{err: conflict}
	service.onTerminal = func(input FinalizeImportTerminalForClaimInput) {
		store.forceStatusForTest(input.Claim.JobID, input.Status)
	}
	worker := jobworker.NewWorker(store, []jobworker.Handler{NewImportHandler(loader, service)}, log.New(io.Discard, "", 0))

	ran, err := worker.RunOnce(context.Background())
	if err != nil || !ran {
		t.Fatalf("RunOnce() = (%v, %v), want (true, nil)", ran, err)
	}
	if len(service.terminalInputs) != 1 {
		t.Fatalf("terminal inputs = %#v, want one", service.terminalInputs)
	}
	terminal := service.terminalInputs[0]
	if terminal.Status != jobs.StatusFailed || terminal.Error == nil || terminal.Error.Code != code {
		t.Fatalf("terminal input = %#v, want failed %q", terminal, code)
	}
	if store.jobs["job-1"].Status != jobs.StatusFailed {
		t.Fatalf("job status = %q, want failed", store.jobs["job-1"].Status)
	}
}

// TestImportWorkerFailsBooksOverTheLimitsWithTheirCodes (R2.1): a new book and an update of a book
// over the limits fail with the code of the exceeded limit and write nothing.
func TestImportWorkerFailsBooksOverTheLimitsWithTheirCodes(t *testing.T) {
	titles := make([]string, maxImportedChapters+1)
	for index := range titles {
		titles[index] = fmt.Sprintf("Глава %d", index+1)
	}
	tooManyChapters := fb2WithChapterTitlesForImportWorkerTest(titles...)
	tooLarge := []byte("<FictionBook><body><section><p>" + strings.Repeat("я", maxImportedTextRunes+1) + "</p></section></body></FictionBook>")
	for _, test := range []struct {
		name   string
		data   []byte
		update bool
		code   string
	}{
		{name: "too many chapters", data: tooManyChapters, code: "import.too_many_chapters"},
		{name: "too many chapters on update", data: tooManyChapters, update: true, code: "import.too_many_chapters"},
		{name: "too large", data: tooLarge, code: "import.too_large"},
		{name: "too large on update", data: tooLarge, update: true, code: "import.too_large"},
	} {
		t.Run(test.name, func(t *testing.T) {
			storageKey := "import/over-limits.fb2"
			request := importRequestForWorkerTest("job-1", storageKey, nil)
			request.Metadata.SizeBytes = int64(len(test.data))
			request.Metadata.FileSHA256 = sha256HexForImportWorkerTest(test.data)
			if test.update {
				request.Metadata.ImportMode = ImportModeUpdateBook
				request.Metadata.TargetBookID = "book-9"
				request.Subject.BookID = "book-9"
			}
			loader := &fakeImportLoader{
				requests: map[string]ImportJob{"job-1": request},
				files:    map[string][]byte{storageKey: test.data},
			}
			store := newLibraryJobStoreForTest(importJobForWorkerTest("job-1"))
			service := &fakeImportLibraryService{}
			service.onTerminal = func(input FinalizeImportTerminalForClaimInput) {
				store.forceStatusForTest(input.Claim.JobID, input.Status)
			}
			worker := jobworker.NewWorker(store, []jobworker.Handler{NewImportHandler(loader, service)}, log.New(io.Discard, "", 0))

			if ran, err := worker.RunOnce(context.Background()); err != nil || !ran {
				t.Fatalf("RunOnce() = (%v, %v), want (true, nil)", ran, err)
			}
			if len(service.books) != 0 || len(service.chapters) != 0 || len(service.savingChapterCounts) != 0 {
				t.Fatalf("service wrote %d books/%d chapters, want none over the limits", len(service.books), len(service.chapters))
			}
			if len(service.terminalInputs) != 1 {
				t.Fatalf("terminal inputs = %#v, want one", service.terminalInputs)
			}
			terminal := service.terminalInputs[0]
			if terminal.Status != jobs.StatusFailed || terminal.Error == nil || terminal.Error.Code != test.code {
				t.Fatalf("terminal input = %#v, want failed %q", terminal, test.code)
			}
			if _, err := jobs.EncodeError(*terminal.Error); err != nil {
				t.Fatalf("job error %#v is not storable: %v", terminal.Error, err)
			}
		})
	}
}

// TestImportWorkerNamesWhyAFileCannotBeRead (UC3.3, UC3.4, R3.4.1): a new book and an update read
// a windows-1251 FB2 as a book, and a file they cannot read fails with the code of the reason —
// the one the queue preflight gives — instead of the generic worker failure.
func TestImportWorkerNamesWhyAFileCannotBeRead(t *testing.T) {
	cp1251 := fb2InEncodingForTest(t, cyrillicFB2ForTest, "windows-1251", charmap.Windows1251)
	unknownEncoding := []byte(strings.Replace(cyrillicFB2ForTest, `encoding="utf-8"`, `encoding="x-unknown"`, 1))
	broken := []byte(`<?xml version="1.0" encoding="utf-8"?><FictionBook><body><section><p>Обрыв`)
	empty := []byte(`<?xml version="1.0" encoding="utf-8"?><FictionBook><body><section><title><p>Только заголовок</p></title></section></body></FictionBook>`)
	brokenDOCX := []byte("PK\x03\x04 not a zip archive")
	for _, test := range []struct {
		name      string
		data      []byte
		extension string
		update    bool
		code      string
	}{
		{name: "windows-1251", data: cp1251, extension: ".fb2"},
		{name: "windows-1251 on update", data: cp1251, extension: ".fb2", update: true},
		{name: "unknown encoding", data: unknownEncoding, extension: ".fb2", code: "import.unsupported_encoding"},
		{name: "unknown encoding on update", data: unknownEncoding, extension: ".fb2", update: true, code: "import.unsupported_encoding"},
		{name: "broken markup", data: broken, extension: ".fb2", code: "invalid_fb2_document"},
		{name: "no chapter text", data: empty, extension: ".fb2", code: "empty_import_content"},
		{name: "broken docx", data: brokenDOCX, extension: ".docx", code: "invalid_docx_document"},
	} {
		t.Run(test.name, func(t *testing.T) {
			storageKey := "import/unreadable" + test.extension
			request := importRequestForWorkerTest("job-1", storageKey, nil)
			request.Metadata.SourceFileName = "book" + test.extension
			request.Metadata.Extension = test.extension
			request.Metadata.SizeBytes = int64(len(test.data))
			request.Metadata.FileSHA256 = sha256HexForImportWorkerTest(test.data)
			if test.update {
				request.Metadata.ImportMode = ImportModeUpdateBook
				request.Metadata.TargetBookID = "book-9"
				request.Subject.BookID = "book-9"
			}
			loader := &fakeImportLoader{
				requests: map[string]ImportJob{"job-1": request},
				files:    map[string][]byte{storageKey: test.data},
			}
			store := newLibraryJobStoreForTest(importJobForWorkerTest("job-1"))
			service := &fakeImportLibraryService{}
			service.onFinalize = func(input FinalizeImportStoreInput) {
				store.forceStatusForTest(input.JobID, jobs.StatusSucceeded)
			}
			service.onTerminal = func(input FinalizeImportTerminalForClaimInput) {
				store.forceStatusForTest(input.Claim.JobID, input.Status)
			}
			worker := jobworker.NewWorker(store, []jobworker.Handler{NewImportHandler(loader, service)}, log.New(io.Discard, "", 0))

			if ran, err := worker.RunOnce(context.Background()); err != nil || !ran {
				t.Fatalf("RunOnce() = (%v, %v), want (true, nil)", ran, err)
			}
			if test.code == "" {
				if len(service.terminalInputs) != 0 || len(service.books) != 1 || service.books[0].input.Title != "Пламя Севера" {
					t.Fatalf("import = books %#v, terminal %#v, want the windows-1251 book", service.books, service.terminalInputs)
				}
				if len(service.chapters) != 2 || service.chapters[1].title != "Глава вторая. Огонь" ||
					service.chapters[1].paragraphs[0].Markdown != "Костёр горел до утра." {
					t.Fatalf("chapters = %#v, want the Cyrillic chapters", service.chapters)
				}
				return
			}
			if len(service.books) != 0 || len(service.chapters) != 0 {
				t.Fatalf("service wrote %d books/%d chapters, want none", len(service.books), len(service.chapters))
			}
			if len(service.terminalInputs) != 1 {
				t.Fatalf("terminal inputs = %#v, want one", service.terminalInputs)
			}
			terminal := service.terminalInputs[0]
			if terminal.Status != jobs.StatusFailed || terminal.Error == nil || terminal.Error.Code != test.code {
				t.Fatalf("terminal input = %#v, want failed %q", terminal, test.code)
			}
			if strings.Contains(terminal.Error.Message, "x-unknown") || strings.Contains(terminal.Error.Message, "Обрыв") {
				t.Fatalf("job error message %q carries file content", terminal.Error.Message)
			}
		})
	}
}

func TestImportWorkerCancellationBeforePersistDoesNotCreateBookOrChapter(t *testing.T) {
	storageKey := "import/canceled-before-persist.fb2"
	store := newLibraryJobStoreForTest(importJobForWorkerTest("job-1"))
	loader := &fakeImportLoader{
		requests: map[string]ImportJob{
			"job-1": importRequestForWorkerTest("job-1", storageKey, nil),
		},
		files: map[string][]byte{
			storageKey: smallFB2ForImportWorkerTest(),
		},
		statuses: []string{string(jobs.StatusRunning), string(jobs.StatusCanceling)},
		onLoadJob: func(call int) {
			if call == 2 {
				store.forceStatusForTest("job-1", jobs.StatusCanceling)
			}
		},
	}
	service := &fakeImportLibraryService{}
	service.onTerminal = func(input FinalizeImportTerminalForClaimInput) {
		store.forceStatusForTest(input.Claim.JobID, input.Status)
	}
	worker := jobworker.NewWorker(store, []jobworker.Handler{NewImportHandler(loader, service)}, log.New(io.Discard, "", 0))

	ran, err := worker.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if !ran {
		t.Fatal("RunOnce() ran = false, want true")
	}
	if loader.loadJobCalls != 2 {
		t.Fatalf("import job load calls = %d, want initial load and pre-persist cancellation check", loader.loadJobCalls)
	}
	if len(service.books) != 0 || len(service.chapters) != 0 {
		t.Fatalf("service calls = %d books/%d chapters, want none for canceled import", len(service.books), len(service.chapters))
	}
	if store.jobs["job-1"].Status != jobs.StatusCanceled {
		t.Fatalf("job status = %q, want %q", store.jobs["job-1"].Status, jobs.StatusCanceled)
	}
}

func TestImportWorkerCancellationWinsBeforeAtomicFinalizationWithoutPartialBook(t *testing.T) {
	storageKey := "import/canceled-after-book.fb2"
	store := newLibraryJobStoreForTest(importJobForWorkerTest("job-1"))
	loader := &fakeImportLoader{
		requests: map[string]ImportJob{
			"job-1": importRequestForWorkerTest("job-1", storageKey, nil),
		},
		files: map[string][]byte{
			storageKey: smallFB2ForImportWorkerTest(),
		},
		statuses: []string{string(jobs.StatusRunning), string(jobs.StatusRunning), string(jobs.StatusCanceling)},
	}
	service := &fakeImportLibraryService{
		onCreateBook: func() {
			store.forceStatusForTest("job-1", jobs.StatusCanceling)
		},
		err: errImportCanceled,
	}
	service.onTerminal = func(input FinalizeImportTerminalForClaimInput) {
		store.forceStatusForTest(input.Claim.JobID, input.Status)
	}
	worker := jobworker.NewWorker(store, []jobworker.Handler{NewImportHandler(loader, service)}, log.New(io.Discard, "", 0))

	ran, err := worker.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if !ran {
		t.Fatal("RunOnce() ran = false, want true")
	}
	if store.jobs["job-1"].Status != jobs.StatusCanceled {
		t.Fatalf("job status = %q, want %q", store.jobs["job-1"].Status, jobs.StatusCanceled)
	}
	if store.jobs["job-1"].Result != nil {
		t.Fatalf("canceled import result = %#v, want nil", store.jobs["job-1"].Result)
	}
	if len(service.books) != 0 || len(service.chapters) != 0 {
		t.Fatalf("atomic finalization persisted %d books/%d chapters after cancellation", len(service.books), len(service.chapters))
	}
}

// The worker rewrites the book the update request names (specs/2026-10-27-update-book-from-file.md).
func TestImportWorkerPassesTheUpdatedBookToFinalization(t *testing.T) {
	storageKey := "import/update.fb2"
	request := importRequestForWorkerTest("job-1", storageKey, nil)
	request.Metadata.ImportMode = ImportModeUpdateBook
	request.Metadata.TargetBookID = "book-9"
	request.Subject.BookID = "book-9"
	request.Metadata.InitialPublication = InitialPublicationPublishImported
	loader := &fakeImportLoader{
		requests: map[string]ImportJob{"job-1": request},
		files:    map[string][]byte{storageKey: smallFB2ForImportWorkerTest()},
	}
	store := newLibraryJobStoreForTest(importJobForWorkerTest("job-1"))
	var finalizedInput FinalizeImportStoreInput
	service := &fakeImportLibraryService{onFinalize: func(input FinalizeImportStoreInput) {
		finalizedInput = input
		store.forceStatusForTest(input.JobID, jobs.StatusSucceeded)
	}}
	worker := jobworker.NewWorker(store, []jobworker.Handler{NewImportHandler(loader, service)}, log.New(io.Discard, "", 0))

	if ran, err := worker.RunOnce(context.Background()); err != nil || !ran {
		t.Fatalf("RunOnce() = %v, %v", ran, err)
	}
	if finalizedInput.ImportMode != ImportModeUpdateBook || finalizedInput.BookID != "book-9" {
		t.Fatalf("finalized input = %q of %q, want an update of book-9", finalizedInput.ImportMode, finalizedInput.BookID)
	}
}

func TestImportWorkerRejectsAppendModeBeforeServiceWrites(t *testing.T) {
	storageKey := "import/append.fb2"
	data := smallFB2ForImportWorkerTest()
	request := importRequestForWorkerTest("job-1", storageKey, nil)
	request.Metadata.ImportMode = ImportMode("append")
	request.Metadata.SizeBytes = int64(len(data))
	request.Metadata.FileSHA256 = sha256HexForImportWorkerTest(data)
	loader := &fakeImportLoader{
		requests: map[string]ImportJob{"job-1": request},
		files:    map[string][]byte{storageKey: data},
	}
	service := &fakeImportLibraryService{}
	store := newLibraryJobStoreForTest(importJobForWorkerTest("job-1"))
	service.onTerminal = func(input FinalizeImportTerminalForClaimInput) {
		store.forceStatusForTest(input.Claim.JobID, input.Status)
	}
	worker := jobworker.NewWorker(store, []jobworker.Handler{NewImportHandler(loader, service)}, log.New(io.Discard, "", 0))

	ran, err := worker.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if !ran {
		t.Fatal("RunOnce() ran = false, want true")
	}
	if len(service.books) != 0 || len(service.chapters) != 0 {
		t.Fatalf("service calls = %d books/%d chapters, want none for append import", len(service.books), len(service.chapters))
	}
	if len(service.terminalInputs) != 1 || service.terminalInputs[0].Status != jobs.StatusFailed {
		t.Fatalf("terminal inputs = %#v, want one atomic failed terminal", service.terminalInputs)
	}
}

func TestImportWorkerRejectsInvalidLaterChapterBeforeServiceWrites(t *testing.T) {
	storageKey := "import/invalid-later-chapter.fb2"
	data := fb2WithChapterTitlesForImportWorkerTest("Valid Chapter", strings.Repeat("A", 181))
	request := importRequestForWorkerTest("job-1", storageKey, nil)
	request.Metadata.SizeBytes = int64(len(data))
	request.Metadata.FileSHA256 = sha256HexForImportWorkerTest(data)
	loader := &fakeImportLoader{
		requests: map[string]ImportJob{"job-1": request},
		files:    map[string][]byte{storageKey: data},
	}
	service := &fakeImportLibraryService{}
	store := newLibraryJobStoreForTest(importJobForWorkerTest("job-1"))
	service.onTerminal = func(input FinalizeImportTerminalForClaimInput) {
		store.forceStatusForTest(input.Claim.JobID, input.Status)
	}
	worker := jobworker.NewWorker(store, []jobworker.Handler{NewImportHandler(loader, service)}, log.New(io.Discard, "", 0))

	ran, err := worker.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if !ran {
		t.Fatal("RunOnce() ran = false, want true")
	}
	if len(service.books) != 0 || len(service.chapters) != 0 {
		t.Fatalf("service calls = %d books/%d chapters, want none after import preflight rejection", len(service.books), len(service.chapters))
	}
	if len(service.terminalInputs) != 1 || service.terminalInputs[0].Status != jobs.StatusFailed {
		t.Fatalf("terminal inputs = %#v, want one atomic failed terminal", service.terminalInputs)
	}
}

func TestImportWorkerRejectsUploadMetadataMismatchBeforeServiceWrites(t *testing.T) {
	data := smallFB2ForImportWorkerTest()
	for _, tc := range []struct {
		name   string
		mutate func(*ImportFileMetadata)
	}{
		{
			name: "size",
			mutate: func(metadata *ImportFileMetadata) {
				metadata.SizeBytes = int64(len(data)) + 1
			},
		},
		{
			name: "sha256",
			mutate: func(metadata *ImportFileMetadata) {
				metadata.FileSHA256 = strings.Repeat("0", 64)
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			storageKey := "import/mismatch-" + tc.name + ".fb2"
			request := importRequestForWorkerTest("job-1", storageKey, nil)
			request.Metadata.SizeBytes = int64(len(data))
			request.Metadata.FileSHA256 = sha256HexForImportWorkerTest(data)
			tc.mutate(&request.Metadata)
			loader := &fakeImportLoader{
				requests: map[string]ImportJob{"job-1": request},
				files:    map[string][]byte{storageKey: data},
			}
			service := &fakeImportLibraryService{}
			store := newLibraryJobStoreForTest(importJobForWorkerTest("job-1"))
			service.onTerminal = func(input FinalizeImportTerminalForClaimInput) {
				store.forceStatusForTest(input.Claim.JobID, input.Status)
			}
			worker := jobworker.NewWorker(store, []jobworker.Handler{NewImportHandler(loader, service)}, log.New(io.Discard, "", 0))

			ran, err := worker.RunOnce(context.Background())
			if err != nil {
				t.Fatalf("RunOnce() error = %v", err)
			}
			if !ran {
				t.Fatal("RunOnce() ran = false, want true")
			}
			if len(service.books) != 0 || len(service.chapters) != 0 {
				t.Fatalf("service calls = %d books/%d chapters, want none after metadata mismatch", len(service.books), len(service.chapters))
			}
			if len(service.terminalInputs) != 1 || service.terminalInputs[0].Status != jobs.StatusFailed {
				t.Fatalf("terminal inputs = %#v, want one atomic failed terminal", service.terminalInputs)
			}
		})
	}
}

func TestImportWorkerFailsGenericallyForInvalidContent(t *testing.T) {
	storageKey := "import/bad.fb2"
	loader := &fakeImportLoader{
		requests: map[string]ImportJob{
			"job-1": importRequestForWorkerTest("job-1", storageKey, nil),
		},
		files: map[string][]byte{
			storageKey: []byte("SHOULD_NOT_LEAK"),
		},
	}
	service := &fakeImportLibraryService{}
	store := newLibraryJobStoreForTest(importJobForWorkerTest("job-1"))
	service.onTerminal = func(input FinalizeImportTerminalForClaimInput) {
		store.forceStatusForTest(input.Claim.JobID, input.Status)
	}
	worker := jobworker.NewWorker(store, []jobworker.Handler{NewImportHandler(loader, service)}, log.New(io.Discard, "", 0))

	ran, err := worker.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if !ran {
		t.Fatal("RunOnce() ran = false, want true")
	}
	if len(service.books) != 0 || len(service.chapters) != 0 {
		t.Fatalf("service calls = %d books/%d chapters, want none after invalid import", len(service.books), len(service.chapters))
	}
	if len(service.terminalInputs) != 1 || service.terminalInputs[0].Status != jobs.StatusFailed {
		t.Fatalf("terminal inputs = %#v, want one failed terminalization", service.terminalInputs)
	}
	terminalError := service.terminalInputs[0].Error
	if terminalError == nil || terminalError.Code != "worker.handler_failed" {
		t.Fatalf("failed error = %#v, want generic worker error", terminalError)
	}
	if strings.Contains(fmt.Sprint(terminalError), "SHOULD_NOT_LEAK") {
		t.Fatal("failed worker error leaked import content")
	}
}

func TestImportHandlerPreservesRetryExhaustedFailureForCommittedTerminal(t *testing.T) {
	service := &fakeImportLibraryService{}
	handler := NewImportHandler(&fakeImportLoader{}, service)
	job := importJobForWorkerTest("job-1")
	job.Status = jobs.StatusRunning
	job.WorkerClaim = jobs.WorkerClaim{ProjectID: job.ProjectID, JobID: job.ID, ClaimIncarnation: 3}
	job.Error = &jobs.Error{Code: jobs.RetryExhaustedCode, Message: jobs.RetryExhaustedMessage}

	_, err := handler.HandleFailedResult(context.Background(), job)
	if err != nil {
		t.Fatalf("HandleFailedResult() error = %v", err)
	}
	if len(service.terminalInputs) != 1 || service.terminalInputs[0].Error == nil ||
		service.terminalInputs[0].Error.Code != jobs.RetryExhaustedCode {
		t.Fatalf("terminal inputs = %#v, want retry exhausted error preserved", service.terminalInputs)
	}
}

func TestImportWorkerExpiresStagingWhenClaimBoundBodyHasExpired(t *testing.T) {
	storageKey := "import/expired.fb2"
	loader := &fakeImportLoader{
		requests: map[string]ImportJob{
			"job-1": importRequestForWorkerTest("job-1", storageKey, nil),
		},
		bodyErr: ErrImportUploadExpired,
	}
	service := &fakeImportLibraryService{}
	store := newLibraryJobStoreForTest(importJobForWorkerTest("job-1"))
	service.onExpire = func(claim jobs.WorkerClaim) {
		store.forceStatusForTest(claim.JobID, jobs.StatusExpired)
	}
	worker := jobworker.NewWorker(store, []jobworker.Handler{NewImportHandler(loader, service)}, log.New(io.Discard, "", 0))

	ran, err := worker.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if !ran {
		t.Fatal("RunOnce() ran = false, want true")
	}
	if len(service.expiryClaims) != 1 || service.expiryClaims[0] != store.jobs["job-1"].WorkerClaim {
		t.Fatalf("expiry claims = %#v, want exact current claim", service.expiryClaims)
	}
	if len(service.terminalInputs) != 0 {
		t.Fatalf("terminal inputs = %#v, want dedicated expiry finalization", service.terminalInputs)
	}
	if store.jobs["job-1"].Status != jobs.StatusExpired {
		t.Fatalf("job status = %q, want expired", store.jobs["job-1"].Status)
	}
}

type fakeImportLoader struct {
	requests         map[string]ImportJob
	files            map[string][]byte
	statuses         []string
	loadedJobID      string
	loadJobCalls     int
	loadedProjectID  string
	loadedStorageKey string
	loadedClaim      jobs.WorkerClaim
	onLoadJob        func(int)
	err              error
	bodyErr          error
}

func (l *fakeImportLoader) GetImportJobForExecution(ctx context.Context, projectID, jobID string) (ImportJob, error) {
	if err := ctx.Err(); err != nil {
		return ImportJob{}, err
	}
	l.loadJobCalls++
	if l.onLoadJob != nil {
		l.onLoadJob(l.loadJobCalls)
	}
	l.loadedJobID = jobID
	l.loadedProjectID = projectID
	if l.err != nil {
		return ImportJob{}, l.err
	}
	request, ok := l.requests[jobID]
	if !ok {
		return ImportJob{}, errors.New("not found")
	}
	if l.loadJobCalls <= len(l.statuses) {
		request.Status = l.statuses[l.loadJobCalls-1]
	}
	return request, nil
}

func (l *fakeImportLoader) GetImportFile(ctx context.Context, projectID, storageKey string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	l.loadedProjectID = projectID
	l.loadedStorageKey = storageKey
	if l.err != nil {
		return nil, l.err
	}
	data, ok := l.files[storageKey]
	if !ok {
		return nil, errors.New("not found")
	}
	return append([]byte(nil), data...), nil
}

func (l *fakeImportLoader) LoadImportBodyForClaim(ctx context.Context, claim jobs.WorkerClaim, storageKey string) ([]byte, error) {
	l.loadedClaim = claim
	if l.bodyErr != nil {
		return nil, l.bodyErr
	}
	return l.GetImportFile(ctx, claim.ProjectID, storageKey)
}

type createImportBookCall struct {
	actor     Actor
	projectID string
	input     BookInput
}

type createImportChapterCall struct {
	actor      Actor
	bookID     string
	title      string
	paragraphs []ParagraphInput
}

type fakeImportLibraryService struct {
	books               []createImportBookCall
	chapters            []createImportChapterCall
	deletedBooks        []string
	onCreateBook        func()
	onFinalize          func(FinalizeImportStoreInput)
	onTerminal          func(FinalizeImportTerminalForClaimInput)
	onExpire            func(jobs.WorkerClaim)
	terminalInputs      []FinalizeImportTerminalForClaimInput
	expiryClaims        []jobs.WorkerClaim
	progressPhases      []jobs.ImportPhase
	savingChapterCounts []int
	err                 error
}

func (s *fakeImportLibraryService) MarkImportParsingForClaim(ctx context.Context, _ jobs.WorkerClaim) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.progressPhases = append(s.progressPhases, jobs.ImportPhaseParsing)
	return nil
}

func (s *fakeImportLibraryService) MarkImportSavingForClaim(ctx context.Context, _ jobs.WorkerClaim, chapters int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.progressPhases = append(s.progressPhases, jobs.ImportPhaseSaving)
	s.savingChapterCounts = append(s.savingChapterCounts, chapters)
	return nil
}

func (s *fakeImportLibraryService) FinalizeImport(ctx context.Context, input FinalizeImportStoreInput) (FinalizedImport, error) {
	if err := ctx.Err(); err != nil {
		return FinalizedImport{}, err
	}
	if s.onCreateBook != nil {
		s.onCreateBook()
	}
	if s.err != nil {
		return FinalizedImport{}, s.err
	}
	s.books = append(s.books, createImportBookCall{
		actor: Actor{UserID: input.ActorUserID}, projectID: input.ProjectID,
		input: BookInput{Title: input.BookTitle},
	})
	paragraphCount := 0
	for _, chapter := range input.Chapters {
		s.chapters = append(s.chapters, createImportChapterCall{
			actor: Actor{UserID: input.ActorUserID}, bookID: "book-1",
			title: chapter.Title, paragraphs: chapter.Paragraphs,
		})
		paragraphCount += len(chapter.Paragraphs)
	}
	if s.onFinalize != nil {
		s.onFinalize(input)
	}
	return FinalizedImport{
		BookID: "book-1", ChapterCount: len(input.Chapters), ParagraphCount: paragraphCount,
		ImportMode: input.ImportMode, SourceFileName: input.SourceFileName,
	}, nil
}

func (s *fakeImportLibraryService) FinalizeImportTerminalForClaim(ctx context.Context, input FinalizeImportTerminalForClaimInput) (jobs.Status, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	s.terminalInputs = append(s.terminalInputs, input)
	if s.onTerminal != nil {
		s.onTerminal(input)
	}
	if s.err != nil && !errors.Is(s.err, errImportCanceled) {
		return "", s.err
	}
	return input.Status, nil
}

func (s *fakeImportLibraryService) ExpireImportStagingForClaim(ctx context.Context, claim jobs.WorkerClaim) (jobs.Status, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	s.expiryClaims = append(s.expiryClaims, claim)
	if s.onExpire != nil {
		s.onExpire(claim)
	}
	if s.err != nil {
		return "", s.err
	}
	return jobs.StatusExpired, nil
}

func (s *fakeImportLibraryService) CreateBook(ctx context.Context, actor Actor, projectID string, input BookInput) (Book, error) {
	if err := ctx.Err(); err != nil {
		return Book{}, err
	}
	if s.err != nil {
		return Book{}, s.err
	}
	s.books = append(s.books, createImportBookCall{actor: actor, projectID: projectID, input: input})
	return Book{ID: "book-1", ProjectID: projectID, Title: input.Title}, nil
}

func (s *fakeImportLibraryService) DeleteBook(ctx context.Context, _ Actor, bookID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.err != nil {
		return s.err
	}
	s.deletedBooks = append(s.deletedBooks, bookID)
	return nil
}

func importJobForWorkerTest(id string) jobs.Job {
	return jobs.Job{
		ID:          id,
		ProjectID:   "project-1",
		Kind:        jobs.KindImport,
		Status:      jobs.StatusQueued,
		Subject:     jobs.ImportSubject{Type: jobs.KindImport, ID: "project-1", ProjectID: "project-1"},
		CanCancel:   true,
		WorkerClaim: jobs.WorkerClaim{ProjectID: "project-1", JobID: id, ClaimIncarnation: 1},
		CreatedAt:   libraryJobTestNow(),
		UpdatedAt:   libraryJobTestNow(),
	}
}

func importRequestForWorkerTest(id, storageKey string, title *string) ImportJob {
	return ImportJob{
		ID:          id,
		ProjectID:   "project-1",
		ActorUserID: "actor-1",
		Kind:        string(jobs.KindImport),
		Status:      string(jobs.StatusQueued),
		Subject: ImportJobSubject{
			Type:      "import",
			ID:        "project-1",
			ProjectID: "project-1",
		},
		Metadata: ImportFileMetadata{
			StorageKey:     storageKey,
			SourceFileName: "uploaded.fb2",
			Extension:      ".fb2",
			MIMEType:       "application/xml",
			SizeBytes:      int64(len(smallFB2ForImportWorkerTest())),
			FileSHA256:     sha256HexForImportWorkerTest(smallFB2ForImportWorkerTest()),
			ImportMode:     ImportModeNewBook,
			Title:          title,
		},
	}
}

func smallFB2ForImportWorkerTest() []byte {
	return []byte(`<?xml version="1.0" encoding="utf-8"?>
<FictionBook>
  <description><title-info><book-title>Parsed Title</book-title></title-info></description>
  <body>
    <section>
      <title><p>Opening</p></title>
      <p>First paragraph SHOULD_NOT_LEAK</p>
      <p>Second paragraph</p>
      <section>
        <title><p>Nested</p></title>
        <p>Nested paragraph</p>
      </section>
    </section>
  </body>
</FictionBook>`)
}

func fb2WithChapterTitlesForImportWorkerTest(titles ...string) []byte {
	var builder strings.Builder
	builder.WriteString(`<?xml version="1.0" encoding="utf-8"?><FictionBook><body>`)
	for _, title := range titles {
		builder.WriteString(`<section><title><p>`)
		builder.WriteString(title)
		builder.WriteString(`</p></title><p>paragraph</p></section>`)
	}
	builder.WriteString(`</body></FictionBook>`)
	return []byte(builder.String())
}

func sha256HexForImportWorkerTest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func importWorkerParagraphCountForTest(chapters []createImportChapterCall) int {
	total := 0
	for _, chapter := range chapters {
		total += len(chapter.paragraphs)
	}
	return total
}
