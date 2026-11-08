package library

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path"
	"slices"
	"strings"

	"canonkeeper/backend/internal/auth"
)

type CreateImportQueueInput struct {
	ProjectID         string
	ActorUserID       string
	IdempotencyKey    string
	RequestMethod     string
	RequestPath       string
	SessionWriteProof auth.SessionWriteProof
	Items             []CreateImportQueueItemInput
}

type CreateImportQueueItemInput struct {
	ClientItemID      string
	Position          int
	SourceFileName    string
	DetectedBookOrder int
}

type GetImportQueueInput struct {
	ProjectID   string
	QueueID     string
	ActorUserID string
}

type ListImportQueuesInput struct {
	ProjectID   string
	ActorUserID string
	Limit       int
}

type UploadImportQueueItemInput struct {
	ProjectID         string
	QueueID           string
	ItemID            string
	ActorUserID       string
	FileName          string
	MIMEType          string
	Content           []byte
	IdempotencyKey    string
	SessionWriteProof auth.SessionWriteProof
}

type SaveImportQueueDraftInput struct {
	ProjectID         string
	QueueID           string
	ActorUserID       string
	ExpectedVersion   int64
	SessionWriteProof auth.SessionWriteProof
	Items             []SaveImportQueueItemInput
}

type SaveImportQueueItemInput = SaveImportQueueItemStoreInput

type StartImportQueueInput struct {
	ProjectID         string
	QueueID           string
	ActorUserID       string
	ExpectedVersion   int64
	IdempotencyKey    string
	RequestMethod     string
	RequestPath       string
	SessionWriteProof auth.SessionWriteProof
}

type CancelImportQueueInput struct {
	ProjectID         string
	QueueID           string
	ActorUserID       string
	SessionWriteProof auth.SessionWriteProof
}

type ImportQueueProjectionStore interface {
	CreateImportQueueDraftForActor(context.Context, CreateImportQueueDraftStoreInput) (ImportQueue, error)
	GetImportQueueForActor(context.Context, GetImportQueueStoreInput) (ImportQueue, error)
	ListImportQueuesForActor(context.Context, ListImportQueuesStoreInput) ([]ImportQueue, error)
	PutImportQueueItemForActor(context.Context, PutImportQueueItemStoreInput) (ImportQueueItem, error)
	SaveImportQueueDraftForActor(context.Context, SaveImportQueueDraftStoreInput) (ImportQueue, error)
	CancelImportQueueForActor(context.Context, CancelImportQueueStoreInput) (ImportQueue, error)
}

func (s *Service) CreateImportQueue(ctx context.Context, input CreateImportQueueInput) (ImportQueue, error) {
	if err := s.requireImportQueueStore(); err != nil {
		return ImportQueue{}, err
	}
	if err := requireImportMutation(ctx, s.store, Actor{UserID: input.ActorUserID}, input.ProjectID); err != nil {
		return ImportQueue{}, err
	}
	if !input.SessionWriteProof.Valid() {
		return ImportQueue{}, ErrForbidden
	}
	idempotencyKey, err := validatedImportIdempotencyKey(input.IdempotencyKey)
	if err != nil {
		return ImportQueue{}, err
	}
	if strings.ToUpper(strings.TrimSpace(input.RequestMethod)) != http.MethodPost || strings.TrimSpace(input.RequestPath) == "" {
		return ImportQueue{}, ErrValidation
	}
	if len(input.Items) == 0 {
		return ImportQueue{}, ErrValidation
	}
	positions := make([]int, 0, len(input.Items))
	clientIDs := make(map[string]struct{}, len(input.Items))
	for _, item := range input.Items {
		if item.ClientItemID == "" || item.SourceFileName == "" || item.Position < 1 || item.DetectedBookOrder < 0 {
			return ImportQueue{}, ErrValidation
		}
		if _, exists := clientIDs[item.ClientItemID]; exists {
			return ImportQueue{}, ErrValidation
		}
		clientIDs[item.ClientItemID] = struct{}{}
		positions = append(positions, item.Position)
	}
	slices.Sort(positions)
	for index, position := range positions {
		if position != index+1 {
			return ImportQueue{}, ErrValidation
		}
	}
	fingerprint, err := importQueueFingerprint(input.Items)
	if err != nil {
		return ImportQueue{}, err
	}
	return s.importQueues.CreateImportQueueDraftForActor(ctx, CreateImportQueueDraftStoreInput{
		ProjectID: input.ProjectID, ActorUserID: input.ActorUserID,
		IdempotencyKey: idempotencyKey, RequestFingerprint: fingerprint,
		Items: createImportQueueStoreItems(input.Items),
	})
}

func (s *Service) ListImportQueues(ctx context.Context, input ListImportQueuesInput) ([]ImportQueue, error) {
	if err := s.requireImportQueueStore(); err != nil {
		return nil, err
	}
	if err := requireImportRead(ctx, s.store, Actor{UserID: input.ActorUserID}, input.ProjectID); err != nil {
		return nil, err
	}
	return s.importQueues.ListImportQueuesForActor(ctx, ListImportQueuesStoreInput{
		ProjectID: input.ProjectID, ActorUserID: input.ActorUserID, Limit: input.Limit,
	})
}

func (s *Service) GetImportQueue(ctx context.Context, input GetImportQueueInput) (ImportQueue, error) {
	if err := s.requireImportQueueStore(); err != nil {
		return ImportQueue{}, err
	}
	if err := requireImportRead(ctx, s.store, Actor{UserID: input.ActorUserID}, input.ProjectID); err != nil {
		return ImportQueue{}, err
	}
	return s.importQueues.GetImportQueueForActor(ctx, GetImportQueueStoreInput(input))
}

func (s *Service) UploadImportQueueItem(ctx context.Context, input UploadImportQueueItemInput) (ImportQueueItem, error) {
	if err := s.requireImportQueueStore(); err != nil {
		return ImportQueueItem{}, err
	}
	if err := requireImportMutation(ctx, s.store, Actor{UserID: input.ActorUserID}, input.ProjectID); err != nil {
		return ImportQueueItem{}, err
	}
	if !input.SessionWriteProof.Valid() {
		return ImportQueueItem{}, ErrForbidden
	}
	idempotencyKey, err := validatedImportIdempotencyKey(input.IdempotencyKey)
	if err != nil {
		return ImportQueueItem{}, err
	}
	queue, err := s.GetImportQueue(ctx, GetImportQueueInput{ProjectID: input.ProjectID, QueueID: input.QueueID, ActorUserID: input.ActorUserID})
	if err != nil {
		return ImportQueueItem{}, err
	}
	if queue.Status != ImportQueueUploading && queue.Status != ImportQueueDraft {
		return ImportQueueItem{}, ErrConflict
	}
	item, found := findImportQueueItem(queue, input.ItemID)
	if !found {
		return ImportQueueItem{}, ErrNotFound
	}
	content := slices.Clone(input.Content)
	fileSHA256 := sha256Hex(content)
	replayKey := strings.Join([]string{input.ActorUserID, input.ProjectID, input.QueueID, input.ItemID, idempotencyKey}, "\x00")
	storageKey := "import/queue/" + sha256Hex([]byte(replayKey))
	sourceFileName := sanitizedImportSourceFileName(input.FileName)
	mimeType, _ := validatedImportMIMEType(input.MIMEType)
	if item.StorageKey == storageKey {
		if item.SHA256 == fileSHA256 && item.SizeBytes == int64(len(content)) &&
			item.MIMEType == mimeType && item.SourceFileName == sourceFileName {
			return item, nil
		}
		return ImportQueueItem{}, ErrConflict
	}

	preflightStatus, preflightError, err := preflightImportQueueItem(input.FileName, input.MIMEType, content, fileSHA256)
	if err != nil {
		return ImportQueueItem{}, err
	}
	item, err = s.importQueues.PutImportQueueItemForActor(ctx, PutImportQueueItemStoreInput{
		ProjectID: input.ProjectID, QueueID: input.QueueID, ItemID: input.ItemID, ActorUserID: input.ActorUserID,
		ExpectedVersion: queue.Version, StorageKey: storageKey, SourceFileName: sourceFileName,
		MIMEType: mimeType, SizeBytes: int64(len(content)), SHA256: fileSHA256, FileBytes: content,
		PreflightStatus: preflightStatus, PreflightErrorJSON: preflightError,
		DetectedBookOrder: item.DetectedBookOrder, ConfirmedBookOrder: item.ConfirmedBookOrder,
	})
	if err != nil {
		return ImportQueueItem{}, err
	}
	return item, nil
}

func (s *Service) SaveImportQueueDraft(ctx context.Context, input SaveImportQueueDraftInput) (ImportQueue, error) {
	if err := s.requireImportQueueStore(); err != nil {
		return ImportQueue{}, err
	}
	if err := requireImportMutation(ctx, s.store, Actor{UserID: input.ActorUserID}, input.ProjectID); err != nil {
		return ImportQueue{}, err
	}
	if !input.SessionWriteProof.Valid() {
		return ImportQueue{}, ErrForbidden
	}
	queue, err := s.GetImportQueue(ctx, GetImportQueueInput{ProjectID: input.ProjectID, QueueID: input.QueueID, ActorUserID: input.ActorUserID})
	if err != nil {
		return ImportQueue{}, err
	}
	nextOrder, err := s.store.NextBookOrder(ctx, input.ProjectID)
	if err != nil {
		return ImportQueue{}, err
	}
	if err := validateImportQueueSave(queue, input, nextOrder); err != nil {
		return ImportQueue{}, err
	}
	return s.importQueues.SaveImportQueueDraftForActor(ctx, SaveImportQueueDraftStoreInput{
		ProjectID: input.ProjectID, QueueID: input.QueueID, ActorUserID: input.ActorUserID,
		ExpectedVersion: input.ExpectedVersion, Items: input.Items,
	})
}

func (s *Service) StartImportQueue(ctx context.Context, input StartImportQueueInput) (ImportQueue, error) {
	if err := requireImportMutation(ctx, s.store, Actor{UserID: input.ActorUserID}, input.ProjectID); err != nil {
		return ImportQueue{}, err
	}
	if !input.SessionWriteProof.Valid() {
		return ImportQueue{}, ErrForbidden
	}
	idempotencyKey, err := validatedImportIdempotencyKey(input.IdempotencyKey)
	if err != nil {
		return ImportQueue{}, err
	}
	if input.ExpectedVersion < 1 {
		return ImportQueue{}, ErrValidation
	}
	if s.startImportQueues == nil {
		return ImportQueue{}, ErrValidation
	}
	return s.startImportQueues.StartImportQueueForActor(ctx, StartImportQueueStoreInput{
		ProjectID: input.ProjectID, QueueID: input.QueueID, ActorUserID: input.ActorUserID,
		ExpectedVersion: input.ExpectedVersion, IdempotencyKey: idempotencyKey,
		RequestMethod: strings.ToUpper(strings.TrimSpace(input.RequestMethod)), RequestPath: strings.TrimSpace(input.RequestPath),
		RequestFingerprint: sha256Hex([]byte(strings.Join([]string{
			input.ProjectID, input.QueueID, fmt.Sprintf("%d", input.ExpectedVersion),
		}, "\x00"))),
		SessionWriteProof: input.SessionWriteProof,
	})
}

func (s *Service) CancelImportQueue(ctx context.Context, input CancelImportQueueInput) (ImportQueue, error) {
	if err := s.requireImportQueueStore(); err != nil {
		return ImportQueue{}, err
	}
	if err := requireImportMutation(ctx, s.store, Actor{UserID: input.ActorUserID}, input.ProjectID); err != nil {
		return ImportQueue{}, err
	}
	if !input.SessionWriteProof.Valid() {
		return ImportQueue{}, ErrForbidden
	}
	queue, err := s.GetImportQueue(ctx, GetImportQueueInput{ProjectID: input.ProjectID, QueueID: input.QueueID, ActorUserID: input.ActorUserID})
	if err != nil {
		return ImportQueue{}, err
	}
	if queue.Status == ImportQueueSucceeded || queue.Status == ImportQueueCanceled || queue.Status == ImportQueueExpired {
		return ImportQueue{}, ErrConflict
	}
	return s.importQueues.CancelImportQueueForActor(ctx, CancelImportQueueStoreInput{
		ProjectID: input.ProjectID, QueueID: input.QueueID, ActorUserID: input.ActorUserID,
		ExpectedVersion: queue.Version,
	})
}

func (s *Service) requireImportQueueStore() error {
	if s.importQueues == nil {
		return ErrValidation
	}
	return nil
}

func createImportQueueStoreItems(items []CreateImportQueueItemInput) []CreateImportQueueItemStoreInput {
	result := make([]CreateImportQueueItemStoreInput, 0, len(items))
	for _, item := range items {
		result = append(result, CreateImportQueueItemStoreInput{
			ClientItemID: item.ClientItemID, Position: item.Position,
			SourceFileName: item.SourceFileName, DetectedBookOrder: item.DetectedBookOrder,
		})
	}
	return result
}

func importQueueFingerprint(items []CreateImportQueueItemInput) (string, error) {
	encoded, err := json.Marshal(items)
	if err != nil {
		return "", ErrValidation
	}
	return sha256Hex(encoded), nil
}

func findImportQueueItem(queue ImportQueue, itemID string) (ImportQueueItem, bool) {
	for _, item := range queue.Items {
		if item.ID == itemID {
			return item, true
		}
	}
	return ImportQueueItem{}, false
}

func preflightImportQueueItem(fileName, mimeType string, content []byte, fileSHA256 string) (string, []byte, error) {
	sourceFileName := sanitizedImportSourceFileName(fileName)
	if sourceFileName == "" {
		return "", nil, ErrValidation
	}
	if int64(len(content)) > MaxImportFileSizeBytes {
		return "", nil, ErrImportFileTooLarge
	}
	normalizedMIMEType, err := validatedImportMIMEType(mimeType)
	if err != nil {
		return importQueueInvalidPreflight("import.unsupported_media_type", "Import file type is not supported.")
	}
	extension := strings.ToLower(path.Ext(sourceFileName))
	if !allowedImportExtensionSet[extension] {
		return importQueueInvalidPreflight("import.unsupported_media_type", "Import file type is not supported.")
	}
	if err := validateImportExtensionMIMEContent(extension, normalizedMIMEType, content); err != nil {
		if extension == ".epub" {
			return importQueueInvalidPreflight("invalid_epub_container", "EPUB container is invalid.")
		}
		return importQueueInvalidPreflight("import.content_mismatch", "Import file content does not match its declared type.")
	}
	if int64(len(content)) == 0 {
		return importQueueInvalidPreflight("empty_import_content", "Import file has no usable content.")
	}
	metadata := ImportFileMetadata{
		SourceFileName: sourceFileName, Extension: extension, MIMEType: normalizedMIMEType,
		SizeBytes: int64(len(content)), FileSHA256: fileSHA256, ImportMode: ImportModeNewBook,
	}
	if err := verifyImportFileMetadata(content, metadata); err != nil {
		return importQueueInvalidPreflight("import.content_mismatch", "Import file identity is invalid.")
	}
	if _, err := readImportBook(content, metadata); err != nil {
		failure := importParseFailure(err, extension)
		return importQueueInvalidPreflight(failure.Code, failure.Message)
	}
	return ImportQueueItemReady, nil, nil
}

func importQueueInvalidPreflight(code, message string) (string, []byte, error) {
	encoded, err := json.Marshal(struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}{Code: code, Message: message})
	if err != nil {
		return "", nil, ErrValidation
	}
	return ImportQueueItemInvalid, encoded, nil
}

func validateImportQueueSave(queue ImportQueue, input SaveImportQueueDraftInput, nextOrder int) error {
	if queue.Status != ImportQueueUploading && queue.Status != ImportQueueDraft {
		return ErrConflict
	}
	if input.ExpectedVersion != queue.Version || len(input.Items) != len(queue.Items) {
		return ErrConflict
	}
	itemIDs := make(map[string]struct{}, len(queue.Items))
	for _, item := range queue.Items {
		if item.PreflightStatus != ImportQueueItemReady || item.StorageKey == "" {
			return ErrConflict
		}
		itemIDs[item.ID] = struct{}{}
	}
	positions := make([]int, 0, len(input.Items))
	orders := make([]int, 0, len(input.Items))
	for _, item := range input.Items {
		if _, exists := itemIDs[item.ItemID]; !exists || item.Position < 1 || item.ConfirmedBookOrder < 1 {
			return ErrValidation
		}
		delete(itemIDs, item.ItemID)
		positions = append(positions, item.Position)
		orders = append(orders, item.ConfirmedBookOrder)
	}
	if len(itemIDs) != 0 {
		return ErrValidation
	}
	slices.Sort(positions)
	slices.Sort(orders)
	for index, position := range positions {
		if position != index+1 || orders[index] != nextOrder+index {
			return ErrValidation
		}
	}
	return nil
}
