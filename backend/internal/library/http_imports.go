package library

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
)

func (h Handlers) GetImportConstraints(w http.ResponseWriter, r *http.Request) {
	actorUserID, ok := h.actorUserID(w, r)
	if !ok {
		return
	}
	constraints, err := h.service.ImportConstraints(r.Context(), Actor{UserID: actorUserID}, chi.URLParam(r, "projectId"))
	if err != nil {
		writeImportServiceError(w, r, err)
		return
	}
	writeImportConstraintsResponse(w, http.StatusOK, constraints)
}

func (h Handlers) ImportBookFile(w http.ResponseWriter, r *http.Request) {
	actorUserID, ok := h.actorUserID(w, r)
	if !ok {
		return
	}
	actor := Actor{UserID: actorUserID}
	projectID := chi.URLParam(r, "projectId")
	if err := h.service.AuthorizeImportMutation(r.Context(), actor, projectID); err != nil {
		writeImportServiceError(w, r, err)
		return
	}
	input, err := importJobInputFromRequest(w, r)
	if err != nil {
		writeImportServiceError(w, r, err)
		return
	}
	input.SessionWriteProof = h.sessionWriteProof(r)
	job, err := h.service.CreateImportJob(r.Context(), actor, projectID, input)
	if err != nil {
		writeImportServiceError(w, r, err)
		return
	}
	if err := writeJobStartResponse(w, r.Context(), http.StatusAccepted, job); err != nil {
		writeImportServiceError(w, r, err)
	}
}

const maxMultipartImportBodyBytes = MaxImportFileSizeBytes + 1024*1024

type importConstraintsResponse struct {
	AllowedExtensions []string `json:"allowedExtensions"`
	AllowedMIMETypes  []string `json:"allowedMimeTypes"`
	MaxFileSizeBytes  int64    `json:"maxFileSizeBytes"`
	NextBookOrder     int      `json:"nextBookOrder"`
}

func writeImportConstraintsResponse(w http.ResponseWriter, status int, constraints ImportConstraints) {
	w.Header().Set("Cache-Control", "no-store")
	writeImportJSON(w, status, importConstraintsResponse{
		AllowedExtensions: constraints.AllowedExtensions,
		AllowedMIMETypes:  constraints.AllowedMIMETypes,
		MaxFileSizeBytes:  constraints.MaxFileSizeBytes,
		NextBookOrder:     constraints.NextBookOrder,
	})
}

func importJobInputFromRequest(w http.ResponseWriter, r *http.Request) (CreateImportJobInput, error) {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || !strings.EqualFold(mediaType, "multipart/form-data") {
		return CreateImportJobInput{}, ErrImportUnsupportedMediaType
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxMultipartImportBodyBytes)
	reader, err := r.MultipartReader()
	if err != nil {
		if isMaxBytesError(err) {
			return CreateImportJobInput{}, ErrImportFileTooLarge
		}
		return CreateImportJobInput{}, ErrValidation
	}
	input := CreateImportJobInput{
		IdempotencyKey: r.Header.Get("Idempotency-Key"),
		RequestMethod:  r.Method,
		RequestPath:    r.URL.Path,
	}
	fileParts := 0
	metadataSourceFileName := ""
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			if isMaxBytesError(err) {
				return CreateImportJobInput{}, ErrImportFileTooLarge
			}
			return CreateImportJobInput{}, ErrValidation
		}
		if err := applyImportPart(&input, part, &fileParts, &metadataSourceFileName); err != nil {
			_ = part.Close()
			return CreateImportJobInput{}, err
		}
		_ = part.Close()
	}
	if metadataSourceFileName != "" {
		input.SourceFileName = metadataSourceFileName
	}
	if fileParts != 1 {
		return CreateImportJobInput{}, ErrValidation
	}
	return input, nil
}

func applyImportPart(input *CreateImportJobInput, part *multipart.Part, fileParts *int, metadataSourceFileName *string) error {
	switch part.FormName() {
	case "file":
		*fileParts = *fileParts + 1
		if *fileParts > 1 {
			return ErrValidation
		}
		fileBytes, err := importFilePartBytes(part)
		if err != nil {
			return err
		}
		input.SourceFileName = part.FileName()
		input.MIMEType = part.Header.Get("Content-Type")
		input.SizeBytes = int64(len(fileBytes))
		input.FileBytes = fileBytes
	case "metadata.title":
		title, err := importFormValue(part)
		if err != nil {
			return err
		}
		input.Title = &title
	case "metadata.sourceFileName":
		sourceFileName, err := importFormValue(part)
		if err != nil {
			return err
		}
		applyImportMetadataSourceFileName(metadataSourceFileName, sourceFileName)
	case "metadata.expectedBookOrder":
		expectedBookOrder, err := importExpectedBookOrder(part)
		if err != nil {
			return err
		}
		input.ExpectedBookOrder = expectedBookOrder
	case "metadata":
		var metadata importMetadataPart
		if err := importJSONPart(part, &metadata); err != nil {
			return err
		}
		if metadata.Title != nil {
			input.Title = metadata.Title
		}
		if metadata.SourceFileName != nil {
			applyImportMetadataSourceFileName(metadataSourceFileName, *metadata.SourceFileName)
		}
		if len(metadata.ExpectedBookOrder) > 0 {
			var expectedBookOrder int
			if json.Unmarshal(metadata.ExpectedBookOrder, &expectedBookOrder) != nil || expectedBookOrder < 1 {
				return ErrValidation
			}
			input.ExpectedBookOrder = expectedBookOrder
		}
	case "options.importMode":
		importMode, err := importFormValue(part)
		if err != nil {
			return err
		}
		input.ImportMode = ImportMode(strings.TrimSpace(importMode))
	case "options.bookId":
		bookID, err := importFormValue(part)
		if err != nil {
			return err
		}
		input.BookID = strings.TrimSpace(bookID)
	case "options":
		var options importOptionsPart
		if err := importJSONPart(part, &options); err != nil {
			return err
		}
		if options.ImportMode != nil {
			input.ImportMode = *options.ImportMode
		}
		if options.BookID != nil {
			input.BookID = strings.TrimSpace(*options.BookID)
		}
	default:
		return ErrValidation
	}
	return nil
}

func applyImportMetadataSourceFileName(target *string, value string) {
	if sourceFileName := strings.TrimSpace(value); sourceFileName != "" {
		*target = sourceFileName
	}
}

type importMetadataPart struct {
	Title             *string         `json:"title"`
	SourceFileName    *string         `json:"sourceFileName"`
	ExpectedBookOrder json.RawMessage `json:"expectedBookOrder"`
}

type importOptionsPart struct {
	ImportMode *ImportMode `json:"importMode"`
	BookID     *string     `json:"bookId"`
}

func importExpectedBookOrder(part *multipart.Part) (int, error) {
	raw, err := importFormValue(part)
	if err != nil {
		return 0, err
	}
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || value < 1 {
		return 0, ErrValidation
	}
	return value, nil
}

func importFilePartBytes(part *multipart.Part) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(part, MaxImportFileSizeBytes+1))
	if err != nil {
		if isMaxBytesError(err) {
			return nil, ErrImportFileTooLarge
		}
		return nil, ErrValidation
	}
	if int64(len(data)) > MaxImportFileSizeBytes {
		return nil, ErrImportFileTooLarge
	}
	return data, nil
}

func importFormValue(part *multipart.Part) (string, error) {
	data, err := io.ReadAll(io.LimitReader(part, 4097))
	if err != nil {
		if isMaxBytesError(err) {
			return "", ErrImportFileTooLarge
		}
		return "", ErrValidation
	}
	if len(data) > 4096 {
		return "", ErrValidation
	}
	return string(data), nil
}

func importJSONPart(part *multipart.Part, target any) error {
	value, err := importFormValue(part)
	if err != nil {
		return err
	}
	if !strings.HasPrefix(strings.TrimSpace(value), "{") {
		return ErrValidation
	}
	decoder := json.NewDecoder(strings.NewReader(value))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return ErrValidation
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return ErrValidation
	}
	return nil
}

func isMaxBytesError(err error) bool {
	var maxBytesErr *http.MaxBytesError
	return errors.As(err, &maxBytesErr)
}
