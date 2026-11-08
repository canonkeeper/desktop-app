package observability

import (
	"bytes"
	"context"
	"encoding"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"canonkeeper/backend/internal/governance"
)

func TestRequestLoggerRedactsSensitiveRequestData(t *testing.T) {
	var output bytes.Buffer
	start := time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC)
	times := []time.Time{start, start.Add(15 * time.Millisecond)}
	now := func() time.Time {
		if len(times) == 0 {
			return start.Add(15 * time.Millisecond)
		}
		next := times[0]
		times = times[1:]
		return next
	}

	middleware := RequestLogger(&output, RequestLoggerOptions{
		Now: now,
		NewRequestID: func() string {
			return "01234567-89ab-4cde-8fab-0123456789ab"
		},
		RouteInfo: func(*http.Request) RouteInfo {
			return RouteInfo{
				OperationID:  "searchProject",
				PathTemplate: "/projects/{projectId}/search",
			}
		},
	})
	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID, ok := RequestIDFromContext(r.Context())
		if !ok || requestID != governance.RequestID("01234567-89ab-4cde-8fab-0123456789ab") {
			t.Fatalf("request context ID = %q, %v", requestID, ok)
		}
		if got := w.Header().Get(RequestIDHeader); got != string(requestID) {
			t.Fatalf("response request ID = %q, want %q", got, requestID)
		}
		w.WriteHeader(http.StatusAccepted)
	}))

	req := httptest.NewRequest(
		http.MethodPost,
		"/projects/project-1/search?q=QUERY_SECRET_SHOULD_NOT_APPEAR&token=QUERY_TOKEN_SHOULD_NOT_APPEAR",
		strings.NewReader(`{"password":"PASSWORD_SHOULD_NOT_APPEAR","accessToken":"BODY_TOKEN_SHOULD_NOT_APPEAR","prompt":"RAW_PROMPT_SHOULD_NOT_APPEAR"}`),
	)
	req.Header.Set("X-Request-ID", "SYNTHETIC_INVALID_REQUEST_ID_MUST_NOT_ESCAPE")
	req.Header.Set("Cookie", "ck_session=COOKIE_SECRET_SHOULD_NOT_APPEAR")
	req.Header.Set("Authorization", "Bearer AUTH_TOKEN_SHOULD_NOT_APPEAR")
	req.Header.Set("X-CSRF-Token", "CSRF_TOKEN_SHOULD_NOT_APPEAR")

	handler.ServeHTTP(httptest.NewRecorder(), req)

	var entry map[string]any
	if err := json.Unmarshal(output.Bytes(), &entry); err != nil {
		t.Fatalf("log entry is not JSON: %v\n%s", err, output.String())
	}

	want := map[string]any{
		"requestId":    "01234567-89ab-4cde-8fab-0123456789ab",
		"method":       http.MethodPost,
		"operationId":  "searchProject",
		"pathTemplate": "/projects/{projectId}/search",
		"status":       float64(http.StatusAccepted),
		"durationMs":   float64(15),
	}
	if len(entry) != len(want) {
		t.Fatalf("log fields = %v, want only %v", entry, want)
	}
	for key, wantValue := range want {
		if got := entry[key]; got != wantValue {
			t.Fatalf("log field %s = %v, want %v; entry=%v", key, got, wantValue, entry)
		}
	}

	rawOutput := output.String()
	for _, forbidden := range []string{
		"QUERY_SECRET_SHOULD_NOT_APPEAR",
		"QUERY_TOKEN_SHOULD_NOT_APPEAR",
		"PASSWORD_SHOULD_NOT_APPEAR",
		"BODY_TOKEN_SHOULD_NOT_APPEAR",
		"RAW_PROMPT_SHOULD_NOT_APPEAR",
		"COOKIE_SECRET_SHOULD_NOT_APPEAR",
		"AUTH_TOKEN_SHOULD_NOT_APPEAR",
		"CSRF_TOKEN_SHOULD_NOT_APPEAR",
		"password",
		"accessToken",
		"prompt",
		"Cookie",
		"Authorization",
		"X-CSRF-Token",
		"SYNTHETIC_INVALID_REQUEST_ID_MUST_NOT_ESCAPE",
	} {
		if strings.Contains(rawOutput, forbidden) {
			t.Fatalf("log output leaked %q: %s", forbidden, rawOutput)
		}
	}
}

func TestRequestLoggerRetainsOnlyCanonicalInboundRequestID(t *testing.T) {
	var output bytes.Buffer
	canonical := "01234567-89ab-4cde-8fab-0123456789ab"
	middleware := RequestLogger(&output, RequestLoggerOptions{
		Now: func() time.Time { return time.Unix(1, 0).UTC() },
		NewRequestID: func() string {
			t.Fatal("request ID generator called for a canonical inbound ID")
			return ""
		},
		RouteInfo: func(*http.Request) RouteInfo {
			return RouteInfo{OperationID: "getProject", PathTemplate: "/projects/{projectId}"}
		},
	})
	req := httptest.NewRequest(http.MethodGet, "/projects/ignored?secret=synthetic", nil)
	req.Header.Set(RequestIDHeader, canonical)
	recorder := httptest.NewRecorder()
	middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID, ok := RequestIDFromContext(r.Context())
		if !ok || string(requestID) != canonical {
			t.Fatalf("request context ID = %q, %v", requestID, ok)
		}
	})).ServeHTTP(recorder, req)
	if got := recorder.Header().Get(RequestIDHeader); got != canonical {
		t.Fatalf("response request ID = %q", got)
	}
	if strings.Contains(output.String(), "secret") || !strings.Contains(output.String(), canonical) {
		t.Fatalf("unexpected request log output")
	}
}

func TestRequestLogEntryUsesOnlyApprovedOperationalFields(t *testing.T) {
	if !safeRequestLogSchema(reflect.TypeOf(requestLogEntry{})) {
		t.Fatalf("request log schema %T is unsafe", requestLogEntry{})
	}
}

func TestRequestLoggerWritesResourceIDsOfPathTemplate(t *testing.T) {
	const (
		projectID = "11111111-1111-4111-8111-111111111111"
		chatID    = "22222222-2222-4222-8222-222222222222"
		jobID     = "33333333-3333-4333-8333-333333333333"
	)
	var output bytes.Buffer
	handler := RequestLogger(&output, RequestLoggerOptions{
		NewRequestID: func() string { return "01234567-89ab-4cde-8fab-0123456789ab" },
		RouteInfo: func(*http.Request) RouteInfo {
			return RouteInfo{
				OperationID:  "createChatTurn",
				PathTemplate: "/projects/{projectId}/chats/{chatId}/turns",
				PathValues: map[string]string{
					"projectId": projectID, "chatId": chatID, "jobId": jobID,
					"bookId": "NOT_A_UUID_SHOULD_NOT_APPEAR", "chapterKey": "481",
				},
			}
		},
	})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusAccepted) }))

	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/projects/x/chats/y/turns", nil))

	var entry map[string]any
	if err := json.Unmarshal(output.Bytes(), &entry); err != nil {
		t.Fatalf("log entry is not JSON: %v\n%s", err, output.String())
	}
	for key, want := range map[string]string{"projectId": projectID, "chatId": chatID, "jobId": jobID} {
		if entry[key] != want {
			t.Fatalf("log field %s = %v, want %s; entry=%v", key, entry[key], want, entry)
		}
	}
	for _, absent := range []string{"bookId", "chapterKey"} {
		if _, ok := entry[absent]; ok {
			t.Fatalf("log entry has %s: %v", absent, entry)
		}
	}
	if strings.Contains(output.String(), "NOT_A_UUID_SHOULD_NOT_APPEAR") || strings.Contains(output.String(), "481") {
		t.Fatalf("log entry leaked a non-UUID path value: %s", output.String())
	}
}

func TestRequestLoggerTurnsHandlerPanicIntoLoggedServerError(t *testing.T) {
	const requestID = "01234567-89ab-4cde-8fab-0123456789ab"
	var output bytes.Buffer
	panicked := true
	handler := RequestLogger(&output, RequestLoggerOptions{
		NewRequestID: func() string { return requestID },
		RouteInfo: func(*http.Request) RouteInfo {
			return RouteInfo{OperationID: "getJob", PathTemplate: "/jobs/{jobId}"}
		},
		PanicResponse: func(w http.ResponseWriter, gotRequestID governance.RequestID) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(string(gotRequestID)))
		},
	})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if panicked {
			panic("handler invariant broken")
		}
		w.WriteHeader(http.StatusOK)
	}))

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/jobs/x", nil))

	if recorder.Code != http.StatusInternalServerError || recorder.Body.String() != requestID {
		t.Fatalf("panic response = %d %q, want 500 with request ID", recorder.Code, recorder.Body.String())
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("log lines = %q, want one request line", lines)
	}
	var entry struct {
		RequestID  string `json:"requestId"`
		Status     int    `json:"status"`
		ErrorClass string `json:"errorClass"`
		ErrorCause string `json:"errorCause"`
		Panic      string `json:"panic"`
		Stack      string `json:"stack"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &entry); err != nil {
		t.Fatal(err)
	}
	if entry.RequestID != requestID || entry.Status != http.StatusInternalServerError ||
		entry.ErrorClass != "internal" || entry.ErrorCause != "panic" ||
		entry.Panic != "handler invariant broken" || !strings.Contains(entry.Stack, "goroutine") {
		t.Fatalf("request log after panic = %+v", entry)
	}

	panicked = false
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/jobs/x", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("request after panic = %d, want 200", recorder.Code)
	}
}

type unsafeCustomMarshaler string

func (value unsafeCustomMarshaler) MarshalJSON() ([]byte, error) {
	return json.Marshal(string(value))
}

func TestRequestLogSchemaGuardRejectsUnboundedAndBypassTypes(t *testing.T) {
	for name, typ := range map[string]reflect.Type{
		"string": reflect.TypeOf(struct {
			Value string `json:"value"`
		}{}),
		"bytes": reflect.TypeOf(struct {
			Value []byte `json:"value"`
		}{}),
		"raw json": reflect.TypeOf(struct {
			Value json.RawMessage `json:"value"`
		}{}),
		"map": reflect.TypeOf(struct {
			Value map[string]governance.HTTPStatus `json:"value"`
		}{}),
		"any": reflect.TypeOf(struct {
			Value any `json:"value"`
		}{}),
		"error": reflect.TypeOf(struct {
			Value error `json:"value"`
		}{}),
		"time": reflect.TypeOf(struct {
			Value time.Time `json:"value"`
		}{}),
		"custom marshaler": reflect.TypeOf(struct {
			Value unsafeCustomMarshaler `json:"value"`
		}{}),
		"untagged field": reflect.TypeOf(struct {
			Value governance.HTTPStatus
		}{}),
		"omitempty field": reflect.TypeOf(struct {
			Value governance.HTTPStatus `json:"value,omitempty"`
		}{}),
	} {
		if safeRequestLogSchema(typ) {
			t.Fatalf("%s field accepted", name)
		}
	}
}

// safeRequestLogSchema принимает только плоскую структуру с явными JSON-именами
// и закрытыми типизированными полями: строки, байты, карты, ошибки и свои
// сериализаторы в журнал запросов не попадают. omitempty допустим только у
// необязательных полей: id ресурса из пути, класса ошибки и текста паники.
func safeRequestLogSchema(typ reflect.Type) bool {
	approved := map[reflect.Type]bool{
		reflect.TypeOf(governance.RequestID("")):         true,
		reflect.TypeOf(governance.HTTPMethod("")):        true,
		reflect.TypeOf(governance.RouteOperationID("")):  true,
		reflect.TypeOf(governance.RoutePathTemplate("")): true,
		reflect.TypeOf(governance.HTTPStatus(0)):         true,
		reflect.TypeOf(governance.DurationMillis(0)):     true,
	}
	optional := map[reflect.Type]bool{
		reflect.TypeOf(governance.ResourceID("")):        true,
		reflect.TypeOf(governance.RequestErrorClass("")): true,
		reflect.TypeOf(governance.RequestErrorCause("")): true,
		reflect.TypeOf(governance.PanicText("")):         true,
		// The address of an anonymous sign-in, for abuse investigation within the 30 days the
		// privacy policy keeps request logs with IP addresses (SEC-17).
		reflect.TypeOf(governance.ClientAddress("")): true,
		// The quality of the sound of a voice call: the gateway's id of the call and measured numbers (V0.11).
		reflect.TypeOf((*voiceCallLogEntry)(nil)): safeVoiceCallLogSchema(reflect.TypeOf(voiceCallLogEntry{})),
	}
	jsonMarshaler := reflect.TypeOf((*json.Marshaler)(nil)).Elem()
	textMarshaler := reflect.TypeOf((*encoding.TextMarshaler)(nil)).Elem()
	if typ.Kind() != reflect.Struct || typ.Implements(jsonMarshaler) || reflect.PointerTo(typ).Implements(jsonMarshaler) ||
		typ.Implements(textMarshaler) || reflect.PointerTo(typ).Implements(textMarshaler) {
		return false
	}
	for index := 0; index < typ.NumField(); index++ {
		field := typ.Field(index)
		name, options, _ := strings.Cut(field.Tag.Get("json"), ",")
		if !field.IsExported() || field.Anonymous || name == "" || name == "-" {
			return false
		}
		switch options {
		case "":
			if !approved[field.Type] {
				return false
			}
		case "omitempty":
			if !optional[field.Type] {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// safeVoiceCallLogSchema takes the quality of a voice call in the request line: its call id, counts and measures, every
// field always written — no text, no address.
func safeVoiceCallLogSchema(typ reflect.Type) bool {
	approved := map[reflect.Type]bool{
		reflect.TypeOf(governance.VoiceCallID("")): true,
		reflect.TypeOf(governance.Count(0)):        true,
		reflect.TypeOf(governance.Measure(0)):      true,
	}
	for index := 0; index < typ.NumField(); index++ {
		field := typ.Field(index)
		name, options, _ := strings.Cut(field.Tag.Get("json"), ",")
		if !field.IsExported() || field.Anonymous || name == "" || name == "-" || options != "" || !approved[field.Type] {
			return false
		}
	}
	return true
}

func TestRequestLoggerReportsOnlySafeUnexpectedErrorClassification(t *testing.T) {
	const secret = "SHOULD_NOT_LEAK"
	tests := []struct {
		name      string
		err       error
		class     string
		cause     string
		logStatus int
	}{
		{name: "canceled", err: errors.Join(context.Canceled, errors.New(secret)), class: "context", cause: "canceled", logStatus: 499},
		{name: "deadline", err: errors.Join(context.DeadlineExceeded, errors.New(secret)), class: "context", cause: "deadline_exceeded", logStatus: 500},
		{name: "database", err: requestLogSQLStateError{state: "40P01", message: secret}, class: "database", cause: "40P01", logStatus: 500},
		{name: "invalid database code", err: requestLogSQLStateError{state: secret, message: secret}, class: "internal", cause: "unclassified", logStatus: 500},
		{name: "internal", err: errors.New(secret), class: "internal", cause: "unclassified", logStatus: 500},
		{
			name: "stage code", err: fmt.Errorf("read: %w", requestLogStageError{code: "timeline.history_list_failed", message: secret}),
			class: "internal", cause: "timeline.history_list_failed", logStatus: 500,
		},
		{name: "invalid stage code", err: requestLogStageError{code: secret, message: secret}, class: "internal", cause: "unclassified", logStatus: 500},
		{name: "free text stage code", err: requestLogStageError{code: "timeline.read " + secret, message: secret}, class: "internal", cause: "unclassified", logStatus: 500},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			handler := RequestLogger(&output, RequestLoggerOptions{
				NewRequestID: func() string { return "01234567-89ab-4cde-8fab-0123456789ab" },
				RouteInfo: func(*http.Request) RouteInfo {
					return RouteInfo{OperationID: "getIndexingStatus", PathTemplate: "/projects/{projectId}/indexing-status"}
				},
			})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				ReportUnexpectedError(r.Context(), test.err)
				http.Error(w, "unexpected server error", http.StatusInternalServerError)
			}))
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/projects/project-1/indexing-status", nil))

			if recorder.Code != http.StatusInternalServerError || recorder.Body.String() != "unexpected server error\n" {
				t.Fatalf("response = %d %q, want unchanged generic 500", recorder.Code, recorder.Body.String())
			}
			var entry struct {
				Status     int    `json:"status"`
				ErrorClass string `json:"errorClass"`
				ErrorCause string `json:"errorCause"`
			}
			if err := json.Unmarshal(output.Bytes(), &entry); err != nil {
				t.Fatal(err)
			}
			if entry.Status != test.logStatus || entry.ErrorClass != test.class || entry.ErrorCause != test.cause {
				t.Fatalf("safe error fields = %#v, want status=%d class=%q cause=%q", entry, test.logStatus, test.class, test.cause)
			}
			if strings.Contains(output.String(), secret) || strings.Contains(recorder.Body.String(), secret) {
				t.Fatalf("unexpected error leaked secret canary")
			}
		})
	}
}

type requestLogSQLStateError struct {
	state   string
	message string
}

func (err requestLogSQLStateError) Error() string    { return err.message }
func (err requestLogSQLStateError) SQLState() string { return err.state }

type requestLogStageError struct {
	code    string
	message string
}

func (err requestLogStageError) Error() string         { return err.message }
func (err requestLogStageError) SafeErrorCode() string { return err.code }

func TestRequestLoggerWritesPanicOfUnmatchedRoute(t *testing.T) {
	var output bytes.Buffer
	handler := RequestLogger(&output, RequestLoggerOptions{
		NewRequestID: func() string { return "01234567-89ab-4cde-8fab-0123456789ab" },
		RouteInfo:    func(*http.Request) RouteInfo { return RouteInfo{} },
	})(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("unrouted handler broke") }))
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/not-in-catalog", nil))

	if recorder.Code != http.StatusInternalServerError ||
		!strings.Contains(output.String(), `"operationId":"unmatchedRoute"`) ||
		!strings.Contains(output.String(), `"panic":"unrouted handler broke"`) {
		t.Fatalf("response %d, log %q: want a logged panic of an unmatched route", recorder.Code, output.String())
	}
}
