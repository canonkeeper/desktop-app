package authormaterials

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
	"unicode/utf16"

	"canonkeeper/backend/internal/auth"
	domain "canonkeeper/backend/internal/domain/authormaterials"
	service "canonkeeper/backend/internal/services/authormaterials"

	"github.com/go-chi/chi/v5"
)

const (
	testProject = "90000000-0000-4000-8000-000000000001"
	testImport  = "90000000-0000-4000-8000-000000000002"
	testRecord  = "90000000-0000-4000-8000-000000000003"
	testOrigin  = "http://localhost:3000"
)

type userStub struct{}

func (userStub) CurrentUser(_ context.Context, cookie string) (auth.User, error) {
	if cookie == "" {
		return auth.User{}, auth.ErrUnauthenticated
	}
	return auth.User{ID: "user-1"}, nil
}

func (userStub) AuthenticateWrite(_ context.Context, cookie, csrf string) (auth.User, auth.SessionWriteProof, error) {
	if cookie == "" {
		return auth.User{}, auth.SessionWriteProof{}, auth.ErrUnauthenticated
	}
	if csrf != "token" {
		return auth.User{}, auth.SessionWriteProof{}, auth.ErrInvalidCSRF
	}
	return auth.User{ID: "user-1"}, auth.SessionWriteProof{SessionHash: "session", CSRFSecretHash: "csrf"}, nil
}

type storeStub struct {
	started *service.StartCommand
	choices []domain.Choice
	err     error
}

func (stub *storeStub) StartAuthorMaterialsImport(_ context.Context, command service.StartCommand) (domain.Import, error) {
	stub.started = &command
	return domain.Import{ID: testImport, JobID: testRecord, JobStatus: "queued", SourceName: command.SourceName, SourceKind: command.SourceKind, CreatedAt: time.Unix(1, 0)}, stub.err
}

func (stub *storeStub) ConfirmAuthorMaterialsImport(_ context.Context, command service.ConfirmCommand) (int, error) {
	stub.choices = command.Choices
	return len(command.Choices), stub.err
}

func (stub *storeStub) ListAuthorMaterials(context.Context, service.Actor, domain.Filter) (domain.Page, error) {
	return domain.Page{Records: []domain.Record{{ID: testRecord, Draft: domain.Draft{Name: "Ужас", Kind: domain.KindSkill}, Revision: 1}}, Total: 1}, stub.err
}

func (stub *storeStub) AuthorMaterialsImport(context.Context, service.Actor) (*domain.Import, map[string]bool, error) {
	return nil, nil, stub.err
}

func router(stub *storeStub) http.Handler {
	routes := chi.NewRouter()
	NewHandlers(UseCases{
		List:      service.List{Store: stub},
		Start:     service.Start{Store: stub, Now: time.Now},
		GetImport: service.GetImport{Store: stub},
		Confirm:   service.Confirm{Store: stub},
	}, userStub{}, []string{testOrigin}).Mount(routes)
	return routes
}

func request(method, path string, body *bytes.Buffer, contentType string) *http.Request {
	if body == nil {
		body = &bytes.Buffer{}
	}
	result := httptest.NewRequest(method, path, body)
	result.Header.Set("Origin", testOrigin)
	result.Header.Set("X-CSRF-Token", "token")
	result.Header.Set("Idempotency-Key", "key-00000001")
	result.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: "cookie"})
	if contentType != "" {
		result.Header.Set("Content-Type", contentType)
	}
	return result
}

func upload(t *testing.T, name string, content []byte) (*bytes.Buffer, string) {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return &body, writer.FormDataContentType()
}

func code(t *testing.T, response *httptest.ResponseRecorder) string {
	t.Helper()
	var problem struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil {
		t.Fatalf("%d: %s", response.Code, response.Body.String())
	}
	return problem.Code
}

// specs/2026-11-04-saga-map.md R9.2, R9.3: a file is read at the door and only what it holds goes further.
func TestStartReadsTheFileAndKeepsNoFile(t *testing.T) {
	content, err := os.ReadFile("../../../domain/authormaterials/testdata/materials.xlsx")
	if err != nil {
		t.Fatal(err)
	}
	stub := &storeStub{}
	body, contentType := upload(t, `C:\папка\навыки.xlsx`, content)
	response := httptest.NewRecorder()
	router(stub).ServeHTTP(response, request(http.MethodPost, "/projects/"+testProject+"/author-material-imports", body, contentType))
	if response.Code != http.StatusAccepted {
		t.Fatalf("%d: %s", response.Code, response.Body.String())
	}
	if stub.started == nil || stub.started.SourceName != "навыки.xlsx" || stub.started.SourceKind != domain.SourceXLSX {
		t.Fatalf("started = %+v", stub.started)
	}
	if !strings.Contains(string(stub.started.Parsed), "омут снов") || bytes.Contains(stub.started.Parsed, []byte("PK")) {
		t.Fatalf("what is kept: %.200s", stub.started.Parsed)
	}
	var answer struct {
		ID         string `json:"id"`
		JobStatus  string `json:"jobStatus"`
		Candidates *[]any `json:"candidates"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &answer); err != nil || answer.ID != testImport || answer.JobStatus != "queued" || answer.Candidates != nil {
		t.Fatalf("answer = %s, %v", response.Body.String(), err)
	}
}

func TestStartSaysWhatIsWrongWithAFile(t *testing.T) {
	for name, testCase := range map[string]struct {
		file    string
		content []byte
		status  int
		code    string
	}{
		"bytes of nothing known": {"навыки.xlsx", []byte{0x00, 0x01, 0xfe, 0x00}, http.StatusUnprocessableEntity, "author_materials.unreadable"},
		"an empty file":          {"навыки.xlsx", nil, http.StatusUnprocessableEntity, "author_materials.empty"},
		"over the limit":         {"навыки.xlsx", make([]byte, domain.MaxFileBytes+1), http.StatusRequestEntityTooLarge, "author_materials.too_large"},
		"under a password":       {"навыки.xlsx", compound("EncryptedPackage"), http.StatusUnprocessableEntity, "author_materials.protected"},
		"an old workbook":        {"навыки.xls", compound("Workbook"), http.StatusUnprocessableEntity, "author_materials.old_format"},
		"a LibreOffice sheet":    {"навыки.ods", zipOf(t, "mimetype", "application/vnd.oasis.opendocument.spreadsheet"), http.StatusUnprocessableEntity, "author_materials.open_document"},
		"a PDF":                  {"заметки.pdf", []byte("%PDF-1.7\n"), http.StatusUnprocessableEntity, "author_materials.pdf"},
		"a picture":              {"карта.docx", []byte("\x89PNG\r\n\x1a\n"), http.StatusUnprocessableEntity, "author_materials.picture"},
		"a CSV":                  {"навыки.csv", []byte("Ворон;персонаж\n"), http.StatusUnprocessableEntity, "author_materials.plain_text"},
	} {
		t.Run(name, func(t *testing.T) {
			body, contentType := upload(t, testCase.file, testCase.content)
			response := httptest.NewRecorder()
			router(&storeStub{}).ServeHTTP(response, request(http.MethodPost, "/projects/"+testProject+"/author-material-imports", body, contentType))
			if response.Code != testCase.status || code(t, response) != testCase.code {
				t.Fatalf("%d: %s", response.Code, response.Body.String())
			}
		})
	}
	stub := &storeStub{err: domain.ErrImportBusy}
	body, contentType := upload(t, "навыки.xlsx", mustRead(t))
	response := httptest.NewRecorder()
	router(stub).ServeHTTP(response, request(http.MethodPost, "/projects/"+testProject+"/author-material-imports", body, contentType))
	if response.Code != http.StatusConflict || code(t, response) != "author_materials.import_busy" {
		t.Fatalf("%d: %s", response.Code, response.Body.String())
	}
}

func mustRead(t *testing.T) []byte {
	t.Helper()
	content, err := os.ReadFile("../../../domain/authormaterials/testdata/materials.xlsx")
	if err != nil {
		t.Fatal(err)
	}
	return content
}

// compound is the start of a compound file of the old Office formats with a stream of the name.
func compound(stream string) []byte {
	data := append([]byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1}, make([]byte, 504)...)
	for _, unit := range utf16.Encode([]rune(stream)) {
		data = append(data, byte(unit), byte(unit>>8))
	}
	return data
}

func zipOf(t *testing.T, name, body string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	entry, err := writer.Create(name)
	if err == nil {
		_, err = entry.Write([]byte(body))
	}
	if err != nil || writer.Close() != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

// A workbook is read as one whatever its name says (R9.2).
func TestStartReadsAWorkbookByWhatItHolds(t *testing.T) {
	stub := &storeStub{}
	body, contentType := upload(t, "навыки.docx", mustRead(t))
	response := httptest.NewRecorder()
	router(stub).ServeHTTP(response, request(http.MethodPost, "/projects/"+testProject+"/author-material-imports", body, contentType))
	if response.Code != http.StatusAccepted || stub.started == nil || stub.started.SourceKind != domain.SourceXLSX || stub.started.SourceName != "навыки.docx" {
		t.Fatalf("%d: %s", response.Code, response.Body.String())
	}
}

func TestStartNeedsItsKeyAndItsToken(t *testing.T) {
	body, contentType := upload(t, "навыки.xlsx", mustRead(t))
	withoutKey := request(http.MethodPost, "/projects/"+testProject+"/author-material-imports", body, contentType)
	withoutKey.Header.Del("Idempotency-Key")
	response := httptest.NewRecorder()
	router(&storeStub{}).ServeHTTP(response, withoutKey)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("without a key: %d", response.Code)
	}
	body, contentType = upload(t, "навыки.xlsx", mustRead(t))
	withoutToken := request(http.MethodPost, "/projects/"+testProject+"/author-material-imports", body, contentType)
	withoutToken.Header.Set("X-CSRF-Token", "wrong")
	response = httptest.NewRecorder()
	router(&storeStub{}).ServeHTTP(response, withoutToken)
	if response.Code != http.StatusForbidden {
		t.Fatalf("without a token: %d", response.Code)
	}
}

func TestConfirmTakesTheChoicesOfTheAuthor(t *testing.T) {
	stub := &storeStub{}
	body := bytes.NewBufferString(`{"items":[{"index":0},{"index":2,"name":"Ёж","kind":"skill","properties":[{"key":"Ранг","value":"1"}]}]}`)
	response := httptest.NewRecorder()
	router(stub).ServeHTTP(response, request(http.MethodPost, "/projects/"+testProject+"/author-material-imports/"+testImport+"/confirm", body, "application/json"))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"saved":2`) {
		t.Fatalf("%d: %s", response.Code, response.Body.String())
	}
	second := stub.choices[1]
	if second.Index != 2 || *second.Name != "Ёж" || *second.Kind != domain.KindSkill || (*second.Properties)[0].Key != "Ранг" || second.Description != nil {
		t.Fatalf("choices = %+v", stub.choices)
	}
	response = httptest.NewRecorder()
	router(stub).ServeHTTP(response, request(http.MethodPost, "/projects/"+testProject+"/author-material-imports/"+testImport+"/confirm", bytes.NewBufferString(`{"items":[],"extra":1}`), "application/json"))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("an unknown field: %d", response.Code)
	}
}

func TestListAndCurrentAnswerTheContract(t *testing.T) {
	response := httptest.NewRecorder()
	router(&storeStub{}).ServeHTTP(response, request(http.MethodGet, "/projects/"+testProject+"/author-materials?q=%D1%83%D0%B6", nil, ""))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"properties":[]`) || !strings.Contains(response.Body.String(), `"totalCount":1`) {
		t.Fatalf("%d: %s", response.Code, response.Body.String())
	}
	response = httptest.NewRecorder()
	router(&storeStub{}).ServeHTTP(response, request(http.MethodGet, "/projects/"+testProject+"/author-materials?nothing=1", nil, ""))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("an unknown filter: %d", response.Code)
	}
	response = httptest.NewRecorder()
	router(&storeStub{}).ServeHTTP(response, request(http.MethodGet, "/projects/"+testProject+"/author-material-imports/current", nil, ""))
	if response.Code != http.StatusOK || strings.TrimSpace(response.Body.String()) != `{"import":null}` {
		t.Fatalf("%d: %s", response.Code, response.Body.String())
	}
	response = httptest.NewRecorder()
	router(&storeStub{err: errors.New("database down")}).ServeHTTP(response, request(http.MethodGet, "/projects/"+testProject+"/author-material-imports/current", nil, ""))
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("a failure: %d", response.Code)
	}
}
