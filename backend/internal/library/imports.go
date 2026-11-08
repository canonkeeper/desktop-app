package library

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"hash"
	"mime"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	"canonkeeper/backend/internal/auth"
	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/projects"
)

const MaxImportFileSizeBytes int64 = 50 * 1024 * 1024

const ImportUploadRetention = 24 * time.Hour

var (
	ErrImportFileTooLarge          = errors.New("library: import file too large")
	ErrImportUnsupportedMediaType  = errors.New("library: unsupported import media type")
	ErrImportContentMismatch       = errors.New("library: import content mismatch")
	ErrImportSequenceConflict      = errors.New("library: import book sequence conflict")
	ErrImportExpectedOrderConflict = errors.New("library: import expected book order conflict")

	allowedImportExtensions = []string{".fb2", ".epub", ".docx"}
	allowedImportMIMETypes  = []string{
		"application/epub+zip",
		"application/x-fictionbook+xml",
		"text/xml",
		"application/xml",
		"application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	}
	allowedImportExtensionSet = stringSet(allowedImportExtensions)
	allowedImportMIMETypeSet  = stringSet(allowedImportMIMETypes)
)

const (
	minIdempotencyKeyLength = 8
	maxIdempotencyKeyLength = 128
)

const (
	ImportJobKind         = "import"
	ImportJobStatusQueued = "queued"
)

type ImportMode string

const (
	ImportModeNewBook ImportMode = "new_book"
	// ImportModeUpdateBook rewrites an existing book from a new file of the same volume
	// (specs/2026-10-27-update-book-from-file.md).
	ImportModeUpdateBook ImportMode = "update_book"
	// ImportModeUpdatePreview reads a new file of a book and tells what an update would change, writing
	// nothing into the book (specs/2026-10-27-update-book-from-file.md, UC6).
	ImportModeUpdatePreview ImportMode = "update_preview"
)

// UpdatesBook says whether the mode names an existing book: an update or its preview.
func (mode ImportMode) UpdatesBook() bool {
	return mode == ImportModeUpdateBook || mode == ImportModeUpdatePreview
}

type InitialPublication string

const (
	InitialPublicationDraftOnly       InitialPublication = "draft_only"
	InitialPublicationPublishImported InitialPublication = "publish_imported"
)

type ImportConstraints struct {
	AllowedExtensions []string
	AllowedMIMETypes  []string
	MaxFileSizeBytes  int64
	NextBookOrder     int
}

type CreateImportJobInput struct {
	SourceFileName string
	MIMEType       string
	SizeBytes      int64
	FileBytes      []byte
	IdempotencyKey string
	RequestMethod  string
	RequestPath    string
	ImportMode     ImportMode
	// BookID names the book an update_book import rewrites.
	BookID            string
	ExpectedBookOrder int
	Title             *string
	SessionWriteProof auth.SessionWriteProof
}

type ImportFileMetadata struct {
	StorageKey         string
	SourceFileName     string
	Extension          string
	MIMEType           string
	SizeBytes          int64
	FileSHA256         string
	IdempotencyKey     string
	ImportMode         ImportMode
	InitialPublication InitialPublication
	Title              *string
	// TargetBookID is the book an update_book import rewrites or an update_preview import reads.
	TargetBookID string
}

type ImportJobSubject struct {
	Type              string
	ID                string
	ProjectID         string
	ExpectedBookOrder int
	BookID            string
}

type ImportJob struct {
	ID                 string
	ProjectID          string
	ActorUserID        string
	Kind               string
	Status             string
	Progress           int
	Subject            ImportJobSubject
	CanCancel          bool
	ExpiresAt          *time.Time
	CreatedAt          time.Time
	UpdatedAt          time.Time
	Metadata           ImportFileMetadata
	RequestFingerprint string
}

type StoreImportFileInput struct {
	StorageKey string
	ProjectID  string
	MIMEType   string
	SizeBytes  int64
	SHA256     string
	Bytes      []byte
	CreatedAt  time.Time
	ExpiresAt  time.Time
}

type CreateImportJobStoreInput struct {
	ID                 string
	ProjectID          string
	ActorUserID        string
	IdempotencyKey     string
	RequestMethod      string
	RequestPath        string
	RequestFingerprint string
	Kind               string
	Status             string
	Progress           int
	Subject            ImportJobSubject
	CanCancel          bool
	CreatedAt          time.Time
	UpdatedAt          time.Time
	Metadata           ImportFileMetadata
}

type EnqueueImportJobInput struct {
	JobID      string
	ProjectID  string
	StorageKey string
	EnqueuedAt time.Time
}

type CreateImportStagingForActorInput struct {
	JobID              string
	ProjectID          string
	ActorUserID        string
	IdempotencyKey     string
	RequestMethod      string
	RequestPath        string
	RequestFingerprint string
	ExpectedBookOrder  int
	FileBytes          []byte
	Metadata           ImportFileMetadata
	SessionWriteProof  auth.SessionWriteProof
}

func (s *Service) ImportConstraints(ctx context.Context, actor Actor, projectID string) (ImportConstraints, error) {
	if err := requireImportRead(ctx, s.store, actor, projectID); err != nil {
		return ImportConstraints{}, err
	}
	nextBookOrder, err := s.store.NextBookOrder(ctx, projectID)
	if err != nil {
		return ImportConstraints{}, err
	}
	constraints := DefaultImportConstraints()
	constraints.NextBookOrder = nextBookOrder
	return constraints, nil
}

func (s *Service) AuthorizeImportMutation(ctx context.Context, actor Actor, projectID string) error {
	return requireImportMutation(ctx, s.store, actor, projectID)
}

func DefaultImportConstraints() ImportConstraints {
	return ImportConstraints{
		AllowedExtensions: slices.Clone(allowedImportExtensions),
		AllowedMIMETypes:  slices.Clone(allowedImportMIMETypes),
		MaxFileSizeBytes:  MaxImportFileSizeBytes,
	}
}

func (s *Service) CreateImportJob(ctx context.Context, actor Actor, projectID string, input CreateImportJobInput) (jobs.Job, error) {
	return createImportJob(ctx, s.store, s.ids, actor, projectID, input)
}

func createImportJob(ctx context.Context, store ImportStore, ids IDGenerator, actor Actor, projectID string, input CreateImportJobInput) (jobs.Job, error) {
	if err := requireImportMutation(ctx, store, actor, projectID); err != nil {
		return jobs.Job{}, err
	}
	if input.ExpectedBookOrder < 0 {
		return jobs.Job{}, ErrValidation
	}
	sourceFileName := sanitizedImportSourceFileName(input.SourceFileName)
	if sourceFileName == "" {
		return jobs.Job{}, ErrValidation
	}
	extension := strings.ToLower(path.Ext(sourceFileName))
	if !allowedImportExtensionSet[extension] {
		return jobs.Job{}, ErrImportUnsupportedMediaType
	}
	mimeType, err := validatedImportMIMEType(input.MIMEType)
	if err != nil {
		return jobs.Job{}, err
	}
	if input.SizeBytes == 0 {
		return jobs.Job{}, ErrImportNoContent
	}
	if input.SizeBytes < 0 {
		return jobs.Job{}, ErrValidation
	}
	if input.SizeBytes > MaxImportFileSizeBytes {
		return jobs.Job{}, ErrImportFileTooLarge
	}
	if int64(len(input.FileBytes)) != input.SizeBytes {
		return jobs.Job{}, ErrValidation
	}
	if err := validateImportExtensionMIMEContent(extension, mimeType, input.FileBytes); err != nil {
		return jobs.Job{}, err
	}
	idempotencyKey, err := validatedImportIdempotencyKey(input.IdempotencyKey)
	if err != nil {
		return jobs.Job{}, err
	}
	requestMethod := strings.ToUpper(strings.TrimSpace(input.RequestMethod))
	requestPath := strings.TrimSpace(input.RequestPath)
	if requestMethod == "" || requestPath == "" {
		return jobs.Job{}, ErrValidation
	}
	importMode, err := normalizedImportMode(input.ImportMode)
	if err != nil {
		return jobs.Job{}, err
	}
	bookID := strings.TrimSpace(input.BookID)
	// An update and its preview keep the book's title and place, so they name a book and nothing else.
	if importMode.UpdatesBook() != (bookID != "") ||
		importMode.UpdatesBook() && (input.ExpectedBookOrder > 0 || input.Title != nil) {
		return jobs.Job{}, ErrValidation
	}
	initialPublication, err := InitialPublicationForImportExtension(extension)
	if err != nil {
		return jobs.Job{}, err
	}
	title, err := validateOptionalText(input.Title, maxBookTitleLength)
	if err != nil {
		return jobs.Job{}, err
	}
	fileSHA256 := sha256Hex(input.FileBytes)
	requestFingerprint := importRequestFingerprint(sourceFileName, extension, mimeType, input.SizeBytes, fileSHA256, importMode, bookID, title, input.ExpectedBookOrder)

	jobID := ids.NewImportJobID()
	storageKey := ids.NewImportStorageKey()
	if jobID == "" || storageKey == "" {
		return jobs.Job{}, ErrValidation
	}
	stagingInput := CreateImportStagingForActorInput{
		JobID:              jobID,
		ProjectID:          projectID,
		ActorUserID:        actor.UserID,
		IdempotencyKey:     idempotencyKey,
		RequestMethod:      requestMethod,
		RequestPath:        requestPath,
		RequestFingerprint: requestFingerprint,
		ExpectedBookOrder:  input.ExpectedBookOrder,
		FileBytes:          slices.Clone(input.FileBytes),
		SessionWriteProof:  input.SessionWriteProof,
		Metadata: ImportFileMetadata{
			StorageKey:         storageKey,
			SourceFileName:     sourceFileName,
			Extension:          extension,
			MIMEType:           mimeType,
			SizeBytes:          input.SizeBytes,
			FileSHA256:         fileSHA256,
			IdempotencyKey:     idempotencyKey,
			ImportMode:         importMode,
			InitialPublication: initialPublication,
			Title:              title,
			TargetBookID:       bookID,
		},
	}
	job, err := store.CreateImportStagingForActor(ctx, stagingInput)
	if err != nil {
		return jobs.Job{}, err
	}
	return job, nil
}

func requireImportRead(ctx context.Context, store ImportStore, actor Actor, projectID string) error {
	role, err := activeImportProjectRole(ctx, store, actor, projectID)
	if err != nil {
		return err
	}
	if !projects.CanReadProject(role) {
		return ErrForbidden
	}
	return nil
}

func requireImportMutation(ctx context.Context, store ImportStore, actor Actor, projectID string) error {
	role, err := activeImportProjectRole(ctx, store, actor, projectID)
	if err != nil {
		return err
	}
	if !projects.CanMutateProject(role) {
		return ErrForbidden
	}
	return nil
}

func activeImportProjectRole(ctx context.Context, store ImportStore, actor Actor, projectID string) (projects.Role, error) {
	if actor.UserID == "" || projectID == "" {
		return "", ErrNotFound
	}
	return store.ActiveProjectRole(ctx, actor.UserID, projectID)
}

func sanitizedImportSourceFileName(fileName string) string {
	fileName = strings.TrimSpace(strings.ReplaceAll(fileName, "\\", "/"))
	if fileName == "" {
		return ""
	}
	base := strings.TrimSpace(path.Base(fileName))
	if base == "." || base == "/" {
		return ""
	}
	return base
}

func validatedImportMIMEType(value string) (string, error) {
	mediaType, _, err := mime.ParseMediaType(strings.TrimSpace(value))
	if err != nil {
		return "", ErrImportUnsupportedMediaType
	}
	mediaType = strings.ToLower(mediaType)
	if !allowedImportMIMETypeSet[mediaType] {
		return "", ErrImportUnsupportedMediaType
	}
	return mediaType, nil
}

func validatedImportIdempotencyKey(value string) (string, error) {
	value = strings.TrimSpace(value)
	if len(value) < minIdempotencyKeyLength || len(value) > maxIdempotencyKeyLength {
		return "", ErrValidation
	}
	return value, nil
}

func normalizedImportMode(value ImportMode) (ImportMode, error) {
	switch value {
	case "":
		return ImportModeNewBook, nil
	case ImportModeNewBook, ImportModeUpdateBook, ImportModeUpdatePreview:
		return value, nil
	default:
		return "", ErrValidation
	}
}

func InitialPublicationForImportExtension(extension string) (InitialPublication, error) {
	switch strings.ToLower(strings.TrimSpace(extension)) {
	case ".fb2", ".epub", ".docx":
		return InitialPublicationPublishImported, nil
	default:
		return "", ErrImportUnsupportedMediaType
	}
}

func validateImportExtensionMIMEContent(extension, mimeType string, data []byte) error {
	if !importMIMETypeMatchesExtension(extension, mimeType) {
		return ErrImportContentMismatch
	}
	switch extension {
	case ".epub", ".docx":
		if !isZipLike(data) {
			return ErrImportContentMismatch
		}
	case ".fb2":
		if !isXMLLike(data) {
			return ErrImportContentMismatch
		}
	default:
		return ErrValidation
	}
	return nil
}

func importMIMETypeMatchesExtension(extension, mimeType string) bool {
	switch extension {
	case ".epub":
		return mimeType == "application/epub+zip"
	case ".docx":
		return mimeType == "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	case ".fb2":
		return mimeType == "application/x-fictionbook+xml" || mimeType == "text/xml" || mimeType == "application/xml"
	default:
		return false
	}
}

func isZipLike(data []byte) bool {
	return bytes.HasPrefix(data, []byte("PK\x03\x04")) ||
		bytes.HasPrefix(data, []byte("PK\x05\x06")) ||
		bytes.HasPrefix(data, []byte("PK\x07\x08"))
}

func isXMLLike(data []byte) bool {
	data = bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})
	data = bytes.TrimSpace(data)
	if !bytes.HasPrefix(data, []byte("<")) {
		return false
	}
	if bytes.HasPrefix(data, []byte("<FictionBook")) {
		return true
	}
	sample := data
	if len(sample) > 4096 {
		sample = sample[:4096]
	}
	return bytes.Contains(sample, []byte("<FictionBook"))
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func importRequestFingerprint(sourceFileName, extension, mimeType string, sizeBytes int64, fileSHA256 string, importMode ImportMode, bookID string, title *string, expectedBookOrder int) string {
	h := sha256.New()
	writeFingerprintPart(h, sourceFileName)
	writeFingerprintPart(h, extension)
	writeFingerprintPart(h, mimeType)
	writeFingerprintPart(h, string(importMode))
	if importMode.UpdatesBook() {
		writeFingerprintPart(h, bookID)
	}
	writeFingerprintPart(h, fileSHA256)
	writeFingerprintPart(h, strconv.FormatInt(sizeBytes, 10))
	if expectedBookOrder > 0 {
		writeFingerprintPart(h, strconv.Itoa(expectedBookOrder))
	}
	if title == nil {
		writeFingerprintPart(h, "")
	} else {
		writeFingerprintPart(h, *title)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func writeFingerprintPart(h hash.Hash, value string) {
	_, _ = h.Write([]byte(value))
	_, _ = h.Write([]byte{0})
}

func stringSet(values []string) map[string]bool {
	result := make(map[string]bool, len(values))
	for _, value := range values {
		result[value] = true
	}
	return result
}
