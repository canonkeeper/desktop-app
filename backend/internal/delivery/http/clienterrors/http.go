// Package clienterrors принимает отчёты браузера об ошибках без содержимого
// (specs/2026-10-28-reliability.md, UC5) и пишет их в журнал backend.
package clienterrors

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"regexp"

	"canonkeeper/backend/internal/httpapi"
	"canonkeeper/backend/internal/observability"

	"github.com/go-chi/chi/v5"
)

const (
	maxReportBytes = 2 << 10
	maxFrames      = 5
)

var (
	namePattern  = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.]{0,59}$`)
	buildPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,80}$`)
	framePattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,80}:[0-9]{1,7}:[0-9]{1,7}$`)
	kinds        = set("render", "error", "rejection", "chunk")
	surfaces     = set("admin", "auth", "chat", "compare", "drawing", "gallery", "home", "illustrations", "legal", "manuscript", "memory", "notes", "promo", "promo-link", "saga", "unknown", "writing")
	browsers     = set("chrome", "edge", "firefox", "safari", "yandex", "other")

	errInvalidReport = errors.New("client error report: invalid")
)

// Handler пишет одну строку client_error на отчёт. Поля проходят строгие шаблоны контракта,
// поэтому в журнал не попадают текст ошибки, адрес страницы и текст пользователя.
type Handler struct {
	logger      *log.Logger
	currentUser func(*http.Request) string
}

// NewHandler принимает журнал и id вошедшего пользователя запроса ("" для анонимного).
func NewHandler(logger *log.Logger, currentUser func(*http.Request) string) Handler {
	if logger == nil || currentUser == nil {
		panic("client errors: logger and current user are required")
	}
	return Handler{logger: logger, currentUser: currentUser}
}

// Mount монтирует reportClientError за строгим Origin и переданной проверкой CSRF.
func (handler Handler) Mount(router chi.Router, csrf func(http.Handler) http.Handler, allowedOrigins ...string) {
	router.With(httpapi.StrictOriginMiddleware(allowedOrigins...), csrf).Post("/client-errors", handler.report)
}

type report struct {
	Kind    string   `json:"kind"`
	Name    string   `json:"name"`
	Surface string   `json:"surface"`
	Build   string   `json:"build"`
	Frames  []string `json:"frames"`
	Browser string   `json:"browser"`
	Mobile  *bool    `json:"mobile"`
}

type logEntry struct {
	Event     string   `json:"event"`
	RequestID string   `json:"requestId,omitempty"`
	UserID    string   `json:"userId,omitempty"`
	Kind      string   `json:"kind"`
	Name      string   `json:"name"`
	Surface   string   `json:"surface"`
	Build     string   `json:"build"`
	Frames    []string `json:"frames,omitempty"`
	Browser   string   `json:"browser"`
	Mobile    bool     `json:"mobile"`
}

func (handler Handler) report(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	requestID, _ := observability.RequestIDFromContext(r.Context())
	value, err := decodeReport(http.MaxBytesReader(w, r.Body, maxReportBytes))
	if err != nil {
		httpapi.WriteProblem(w, string(requestID), http.StatusBadRequest, "Bad Request", "invalid client error report", "client_error.invalid")
		return
	}
	line, _ := json.Marshal(logEntry{
		Event: "client_error", RequestID: string(requestID), UserID: handler.currentUser(r),
		Kind: value.Kind, Name: value.Name, Surface: value.Surface, Build: value.Build,
		Frames: value.Frames, Browser: value.Browser, Mobile: *value.Mobile,
	})
	handler.logger.Print(string(line))
	w.WriteHeader(http.StatusNoContent)
}

func decodeReport(body io.Reader) (report, error) {
	decoder := json.NewDecoder(body)
	decoder.DisallowUnknownFields()
	var value report
	if err := decoder.Decode(&value); err != nil {
		return report{}, errInvalidReport
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return report{}, errInvalidReport
	}
	if !kinds[value.Kind] || !namePattern.MatchString(value.Name) || !surfaces[value.Surface] ||
		!buildPattern.MatchString(value.Build) || !browsers[value.Browser] || value.Mobile == nil ||
		len(value.Frames) > maxFrames {
		return report{}, errInvalidReport
	}
	for _, frame := range value.Frames {
		if !framePattern.MatchString(frame) {
			return report{}, errInvalidReport
		}
	}
	return value, nil
}

func set(values ...string) map[string]bool {
	result := make(map[string]bool, len(values))
	for _, value := range values {
		result[value] = true
	}
	return result
}
