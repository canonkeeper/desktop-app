package library

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path"
	"strings"
	"unicode/utf8"

	"canonkeeper/backend/internal/domain/chapterfile"
	"canonkeeper/backend/internal/domain/quota"
	"canonkeeper/backend/internal/domain/textface"
	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/jobworker"
)

var (
	ErrInvalidImportJob = errors.New("library: invalid import job metadata")
	errImportCanceled   = errors.New("library: import canceled")
)

const (
	maxImportedBookTitleLength    = 180
	maxImportedChapterTitleLength = 180
)

type ImportJobLoader interface {
	GetImportJobForExecution(context.Context, string, string) (ImportJob, error)
	LoadImportBodyForClaim(context.Context, jobs.WorkerClaim, string) ([]byte, error)
}

type ImportLibraryService interface {
	MarkImportParsingForClaim(context.Context, jobs.WorkerClaim) error
	MarkImportSavingForClaim(context.Context, jobs.WorkerClaim, int) error
	FinalizeImport(context.Context, FinalizeImportStoreInput) (FinalizedImport, error)
	FinalizeImportTerminalForClaim(context.Context, FinalizeImportTerminalForClaimInput) (jobs.Status, error)
	ExpireImportStagingForClaim(context.Context, jobs.WorkerClaim) (jobs.Status, error)
}

type ImportHandler struct {
	loader  ImportJobLoader
	service ImportLibraryService
}

func NewImportHandler(loader ImportJobLoader, service ImportLibraryService) ImportHandler {
	if loader == nil {
		panic("library: ImportJobLoader is required")
	}
	if service == nil {
		panic("library: ImportLibraryService is required")
	}
	return ImportHandler{loader: loader, service: service}
}

func (ImportHandler) Kind() jobs.Kind {
	return jobs.KindImport
}

func (h ImportHandler) HandleCanceledResult(ctx context.Context, job jobs.Job) (jobworker.HandlerResult, error) {
	if err := validateImportJob(job); err != nil {
		return jobworker.HandlerResult{}, err
	}
	return h.finalizeTerminal(ctx, job, jobs.StatusCanceled)
}

func (h ImportHandler) HandleFailedResult(ctx context.Context, job jobs.Job) (jobworker.HandlerResult, error) {
	if err := validateImportJob(job); err != nil {
		return jobworker.HandlerResult{}, err
	}
	return h.finalizeTerminal(ctx, job, jobs.StatusFailed)
}

func (h ImportHandler) HandleResult(ctx context.Context, job jobs.Job) (jobworker.HandlerResult, error) {
	if err := ctx.Err(); err != nil {
		return jobworker.HandlerResult{}, err
	}
	if err := validateImportJob(job); err != nil {
		return jobworker.HandlerResult{}, err
	}
	request, err := h.loader.GetImportJobForExecution(ctx, job.ProjectID, job.ID)
	if err != nil {
		return jobworker.HandlerResult{}, jobworker.ErrRetryableHandler
	}
	if err := validateImportExecutionRequest(job, request); err != nil {
		return h.finalizeTerminal(ctx, job, jobs.StatusFailed)
	}
	if importJobCancellationRequested(request) {
		return h.finalizeTerminal(ctx, job, jobs.StatusCanceled)
	}
	data, err := h.loader.LoadImportBodyForClaim(ctx, job.WorkerClaim, request.Metadata.StorageKey)
	if errors.Is(err, ErrImportUploadExpired) {
		return h.expireStaging(ctx, job)
	}
	if err != nil {
		return jobworker.HandlerResult{}, jobworker.ErrRetryableHandler
	}
	if err := verifyImportFileMetadata(data, request.Metadata); err != nil {
		return h.finalizeTerminal(ctx, job, jobs.StatusFailed)
	}
	if err := h.service.MarkImportParsingForClaim(ctx, job.WorkerClaim); err != nil {
		return jobworker.HandlerResult{}, jobworker.ErrRetryableHandler
	}
	plan, err := readImportBook(data, request.Metadata)
	if err != nil {
		failure := importParseFailure(err, request.Metadata.Extension)
		job.Error = &failure
		return h.finalizeTerminal(ctx, job, jobs.StatusFailed)
	}
	result, err := h.persistImport(ctx, job, request, plan)
	if errors.Is(err, errImportCanceled) {
		return h.finalizeTerminal(ctx, job, jobs.StatusCanceled)
	}
	if errors.Is(err, ErrInvalidImportJob) || errors.Is(err, ErrValidation) {
		return h.finalizeTerminal(ctx, job, jobs.StatusFailed)
	}
	if errors.Is(err, ErrImportSequenceConflict) || errors.Is(err, ErrImportExpectedOrderConflict) {
		job.Error = importOrderConflictJobError(err)
		return h.finalizeTerminal(ctx, job, jobs.StatusFailed)
	}
	// A book over the owner's quota is not written; the Job fails with the quota code and a queue
	// stops as after any other failed volume (quota spec R3.3).
	if exceeded, ok := quota.AsExceeded(err); ok {
		job.Error = &jobs.Error{Code: exceeded.Code(), Message: "The book does not fit the writer quota."}
		return h.finalizeTerminal(ctx, job, jobs.StatusFailed)
	}
	if err != nil {
		return jobworker.HandlerResult{}, jobworker.ErrRetryableHandler
	}
	return result, nil
}

func importOrderConflictJobError(err error) *jobs.Error {
	if errors.Is(err, ErrImportSequenceConflict) {
		return &jobs.Error{Code: "import_sequence_conflict", Message: "Existing book order is not contiguous."}
	}
	return &jobs.Error{Code: "import_expected_order_conflict", Message: "Expected book order is stale."}
}

// importParseFailure is the refusal step of UC3.4 (specs/2026-10-28-writer-first-success.md): the
// queue preflight and the import Job of a new book or of «Обновить из файла» name a file they
// cannot read with the same code, so the author learns the reason (book limits, encoding, no
// chapter text, broken document) and job_finished names it too (R3.4.1, R3.4.2). The message is
// fixed: nothing of the file reaches the Job.
func importParseFailure(err error, extension string) jobs.Error {
	switch {
	case errors.Is(err, ErrImportTooManyChapters):
		return jobs.Error{Code: "import.too_many_chapters", Message: "The book has more than 2000 chapters."}
	case errors.Is(err, ErrImportTooLarge):
		return jobs.Error{Code: "import.too_large", Message: "The book text is longer than 10000000 characters."}
	case errors.Is(err, errImportUnsupportedEncoding):
		return jobs.Error{Code: "import.unsupported_encoding", Message: "The file declares an encoding that is not supported."}
	case errors.Is(err, ErrImportNoContent):
		return jobs.Error{Code: "empty_import_content", Message: "Import file has no usable content."}
	case errors.Is(err, errFB2ZipTooLarge):
		return jobs.Error{Code: "import.fb2zip_too_large", Message: fmt.Sprintf("The book in the archive is larger than %d bytes.", MaxImportFileSizeBytes)}
	case errors.Is(err, errFB2Document):
		return jobs.Error{Code: "invalid_fb2_document", Message: "Import document is invalid."}
	}
	switch extension = strings.ToLower(strings.TrimSpace(extension)); extension {
	case ".fb2", ".epub", ".docx", ".rtf", ".odt", ".fb2.zip":
		return jobs.Error{Code: "invalid_" + strings.ReplaceAll(strings.TrimPrefix(extension, "."), ".", "") + "_document", Message: "Import document is invalid."}
	default:
		return jobs.Error{Code: "import.unsupported_media_type", Message: "Import file type is not supported."}
	}
}

func (h ImportHandler) expireStaging(ctx context.Context, job jobs.Job) (jobworker.HandlerResult, error) {
	committedStatus, err := h.service.ExpireImportStagingForClaim(ctx, job.WorkerClaim)
	if err != nil {
		return jobworker.HandlerResult{}, jobworker.ErrRetryableHandler
	}
	if committedStatus != jobs.StatusExpired {
		return jobworker.HandlerResult{}, jobworker.ErrTerminalProtocolInvariant
	}
	return jobworker.HandlerResult{Status: committedStatus}, nil
}

func (h ImportHandler) finalizeTerminal(ctx context.Context, job jobs.Job, status jobs.Status) (jobworker.HandlerResult, error) {
	input := FinalizeImportTerminalForClaimInput{Claim: job.WorkerClaim, Status: status}
	if status == jobs.StatusFailed {
		input.Error = job.Error
		if input.Error == nil {
			input.Error = &jobs.Error{Code: jobworker.HandlerFailedCode, Message: jobworker.HandlerFailedMessage}
		}
	}
	committedStatus, err := h.service.FinalizeImportTerminalForClaim(ctx, input)
	if err != nil {
		return jobworker.HandlerResult{}, jobworker.ErrRetryableHandler
	}
	return jobworker.HandlerResult{Status: committedStatus}, nil
}

func validateImportJob(job jobs.Job) error {
	subject, ok := job.Subject.(jobs.ImportSubject)
	if strings.TrimSpace(job.ID) == "" ||
		strings.TrimSpace(job.ProjectID) == "" ||
		job.Kind != jobs.KindImport ||
		!ok || subject.Type != jobs.KindImport ||
		strings.TrimSpace(subject.ID) == "" ||
		subject.ID != job.ProjectID ||
		subject.ProjectID != job.ProjectID ||
		job.WorkerClaim.Validate() != nil ||
		job.WorkerClaim.ProjectID != job.ProjectID ||
		job.WorkerClaim.JobID != job.ID {
		return ErrInvalidImportJob
	}
	return nil
}

func validateImportExecutionRequest(job jobs.Job, request ImportJob) error {
	if strings.TrimSpace(request.ID) == "" ||
		request.ID != job.ID ||
		strings.TrimSpace(request.ProjectID) == "" ||
		request.ProjectID != job.ProjectID ||
		strings.TrimSpace(request.ActorUserID) == "" ||
		request.Kind != string(jobs.KindImport) ||
		request.Subject.Type != "import" ||
		request.Subject.ID != request.ProjectID ||
		request.Subject.ProjectID != request.ProjectID ||
		strings.TrimSpace(request.Metadata.StorageKey) == "" {
		return ErrInvalidImportJob
	}
	switch request.Metadata.ImportMode {
	case "", ImportModeNewBook:
		if request.Metadata.TargetBookID != "" {
			return ErrInvalidImportJob
		}
		return nil
	case ImportModeUpdateBook, ImportModeUpdatePreview:
		if request.Metadata.TargetBookID == "" || request.Subject.BookID != request.Metadata.TargetBookID {
			return ErrInvalidImportJob
		}
		return nil
	default:
		return ErrInvalidImportJob
	}
}

func verifyImportFileMetadata(data []byte, metadata ImportFileMetadata) error {
	if metadata.SizeBytes <= 0 || int64(len(data)) != metadata.SizeBytes {
		return ErrInvalidImportJob
	}
	if strings.TrimSpace(metadata.FileSHA256) == "" || !strings.EqualFold(strings.TrimSpace(metadata.FileSHA256), sha256Hex(data)) {
		return ErrInvalidImportJob
	}
	return nil
}

// readImportBook reads an uploaded file into the plan of the book it imports. The queue preflight
// and the import Job read a file this one way, so a file the preflight accepts imports and a file
// either refuses gets the same importParseFailure code.
func readImportBook(data []byte, metadata ImportFileMetadata) (importPlan, error) {
	parsed, err := parseImportBook(data, metadata.Extension)
	if err != nil {
		return importPlan{}, err
	}
	return buildImportPlan(metadata, parsed)
}

func parseImportBook(data []byte, extension string) (BookImport, error) {
	switch strings.ToLower(strings.TrimSpace(extension)) {
	case ".fb2":
		return ParseFB2(bytes.NewReader(data))
	case ".epub":
		reader := bytes.NewReader(data)
		return ParseEPUB(reader, int64(len(data)))
	case ".docx":
		reader := bytes.NewReader(data)
		return ParseDOCX(reader, int64(len(data)))
	case ".rtf":
		return ParseRTF(data)
	case ".odt":
		return ParseODT(bytes.NewReader(data), int64(len(data)))
	case ".fb2.zip":
		return ParseFB2Zip(bytes.NewReader(data), int64(len(data)))
	case ".txt":
		return ParseText(data, false)
	case ".md":
		return ParseText(data, true)
	default:
		return BookImport{}, ErrInvalidImportJob
	}
}

func (h ImportHandler) persistImport(ctx context.Context, job jobs.Job, request ImportJob, plan importPlan) (jobworker.HandlerResult, error) {
	if err := h.ensureImportNotCanceled(ctx, job); err != nil {
		return jobworker.HandlerResult{}, err
	}
	if err := h.service.MarkImportSavingForClaim(ctx, job.WorkerClaim, len(plan.Chapters)); err != nil {
		return jobworker.HandlerResult{}, err
	}
	chapters := make([]FinalizeImportChapterInput, 0, len(plan.Chapters))
	for _, chapter := range plan.Chapters {
		chapters = append(chapters, FinalizeImportChapterInput{
			Title:      chapter.Title,
			Paragraphs: chapter.Paragraphs,
		})
	}
	finalized, err := h.service.FinalizeImport(ctx, FinalizeImportStoreInput{
		JobID:                    job.ID,
		ProjectID:                request.ProjectID,
		ActorUserID:              request.ActorUserID,
		StorageKey:               request.Metadata.StorageKey,
		ExpectedClaimIncarnation: job.WorkerClaim.ClaimIncarnation,
		ExpectedBookOrder:        request.Subject.ExpectedBookOrder,
		BookTitle:                plan.BookTitle,
		BookID:                   request.Metadata.TargetBookID,
		Chapters:                 chapters,
		ImportMode:               importModeForResult(request.Metadata.ImportMode),
		InitialPublication:       request.Metadata.InitialPublication,
		SourceFileName:           request.Metadata.SourceFileName,
	})
	if err != nil {
		return jobworker.HandlerResult{}, err
	}
	_ = finalized
	return jobworker.HandlerResult{Status: jobs.StatusSucceeded}, nil
}

func (h ImportHandler) ensureImportNotCanceled(ctx context.Context, job jobs.Job) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	request, err := h.loader.GetImportJobForExecution(ctx, job.ProjectID, job.ID)
	if err != nil {
		return err
	}
	if err := validateImportExecutionRequest(job, request); err != nil {
		return err
	}
	if importJobCancellationRequested(request) {
		return errImportCanceled
	}
	return nil
}

func importJobCancellationRequested(request ImportJob) bool {
	switch jobs.Status(request.Status) {
	case jobs.StatusCanceling, jobs.StatusCanceled:
		return true
	default:
		return false
	}
}

type importPlan struct {
	BookTitle      string
	Chapters       []importPlanChapter
	ParagraphCount int
}

type importPlanChapter struct {
	Title      string
	Paragraphs []ParagraphInput
}

func buildImportPlan(metadata ImportFileMetadata, parsed BookImport) (importPlan, error) {
	bookTitle, err := validateImportedTitle(importBookTitle(metadata, parsed), maxImportedBookTitleLength)
	if err != nil {
		return importPlan{}, err
	}
	plan := importPlan{
		BookTitle: bookTitle,
		Chapters:  make([]importPlanChapter, 0, len(parsed.Chapters)),
	}
	for _, chapter := range parsed.Chapters {
		paragraphs := importParagraphInputs(chapter.Paragraphs)
		if len(paragraphs) == 0 {
			continue
		}
		chapterTitle := importChapterTitle(chapter.Title, len(plan.Chapters)+1)
		if parsed.Unsplit {
			chapterTitle = bookTitle
		}
		title, err := validateImportedTitle(chapterTitle, maxImportedChapterTitleLength)
		if err != nil {
			return importPlan{}, err
		}
		plan.Chapters = append(plan.Chapters, importPlanChapter{
			Title:      title,
			Paragraphs: paragraphs,
		})
		plan.ParagraphCount += len(paragraphs)
	}
	if len(plan.Chapters) == 0 || plan.ParagraphCount == 0 {
		return importPlan{}, ErrImportNoContent
	}
	return plan, nil
}

func validateImportedTitle(title string, maxLength int) (string, error) {
	title = strings.TrimSpace(title)
	if title == "" || utf8.RuneCountInString(title) > maxLength {
		return "", ErrInvalidImportJob
	}
	return title, nil
}

func importBookTitle(metadata ImportFileMetadata, parsed BookImport) string {
	if metadata.Title != nil {
		if title := strings.TrimSpace(*metadata.Title); title != "" {
			return title
		}
	}
	if title := strings.TrimSpace(parsed.Title); title != "" {
		return title
	}
	if title := importSourceFileTitle(metadata.SourceFileName); title != "" {
		return title
	}
	return "Imported Book"
}

func importSourceFileTitle(sourceFileName string) string {
	base := path.Base(strings.TrimSpace(strings.ReplaceAll(sourceFileName, "\\", "/")))
	if base == "." || base == "/" {
		return ""
	}
	return strings.TrimSpace(base[:len(base)-len(ImportFileKind(base))])
}

func importChapterTitle(title string, number int) string {
	if title := strings.TrimSpace(title); title != "" {
		return title
	}
	return chapterFallbackTitle(number)
}

// importParagraphInputs are the paragraphs of a chapter with words as the book keeps them
// (specs/2026-11-08-italic-bold-in-text.md TF4.1): markdown in the Markdown subset of their runs, the text their words.
func importParagraphInputs(values []chapterfile.Paragraph) []ParagraphInput {
	paragraphs := make([]ParagraphInput, 0, len(values))
	for _, value := range values {
		runs := textface.Trimmed(value.Runs)
		text := chapterfile.Paragraph{Runs: runs}.Text()
		if text == "" {
			continue
		}
		paragraphs = append(paragraphs, ParagraphInput{
			Kind:     ParagraphKindParagraph,
			Order:    len(paragraphs) + 1,
			Markdown: textface.Markdown(runs),
			Text:     &text,
		})
	}
	return paragraphs
}

func importModeForResult(mode ImportMode) ImportMode {
	if mode == "" {
		return ImportModeNewBook
	}
	return mode
}
