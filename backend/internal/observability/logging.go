package observability

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"time"

	"canonkeeper/backend/internal/governance"
)

const RequestIDHeader = "X-Request-ID"

type RouteInfo struct {
	OperationID  string
	PathTemplate string
	// PathValues — значения параметров шаблона пути по имени ({jobId} → значение из пути).
	// В журнал попадают только канонические UUID ресурсов.
	PathValues map[string]string
}

type RequestLoggerOptions struct {
	Now          func() time.Time
	NewRequestID func() string
	RouteInfo    func(*http.Request) RouteInfo
	// PanicResponse отвечает клиенту на панику обработчика, если ответ ещё не начат.
	PanicResponse func(http.ResponseWriter, governance.RequestID)
}

func RequestLogger(out io.Writer, options RequestLoggerOptions) func(http.Handler) http.Handler {
	if out == nil {
		out = io.Discard
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.NewRequestID == nil {
		options.NewRequestID = newSecureRequestID
	}

	logger := &requestLogger{
		out:           out,
		now:           options.Now,
		newRequestID:  options.NewRequestID,
		routeInfo:     options.RouteInfo,
		panicResponse: options.PanicResponse,
	}

	return logger.middleware
}

type requestLogger struct {
	out           io.Writer
	now           func() time.Time
	newRequestID  func() string
	routeInfo     func(*http.Request) RouteInfo
	panicResponse func(http.ResponseWriter, governance.RequestID)
	mu            sync.Mutex
}

func (logger *requestLogger) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := logger.now()
		stats := newResponseStats(w)
		requestID := logger.resolveRequestID(r.Header.Get(RequestIDHeader))
		errorReport := &requestReport{}
		ctx := ContextWithRequestID(r.Context(), requestID)
		ctx = context.WithValue(ctx, requestReportContextKey{}, errorReport)
		r = r.WithContext(ctx)
		w.Header().Set(RequestIDHeader, string(requestID))

		// Строка запроса пишется и после паники обработчика: паника становится ответом 500 с
		// requestId, а строка запроса — её значением и стеком; процесс продолжает работать.
		defer func() {
			recovered := recover()
			if recovered != nil && recovered != http.ErrAbortHandler {
				failure := RecoveredPanic(recovered)
				errorReport.class, errorReport.cause = ClassifyError(failure)
				errorReport.panic = failure
				if stats.status == 0 {
					logger.writePanicResponse(stats, requestID)
				}
			}
			logger.writeRequest(requestID, r, stats, errorReport, start)
			if recovered == http.ErrAbortHandler {
				panic(recovered)
			}
		}()
		next.ServeHTTP(stats.responseWriter(), r)
	})
}

func (logger *requestLogger) writePanicResponse(w http.ResponseWriter, requestID governance.RequestID) {
	if logger.panicResponse != nil {
		logger.panicResponse(w, requestID)
		return
	}
	w.WriteHeader(http.StatusInternalServerError)
}

func (logger *requestLogger) writeRequest(
	requestID governance.RequestID,
	r *http.Request,
	stats *responseStats,
	report *requestReport,
	start time.Time,
) {
	route := RouteInfo{}
	if logger.routeInfo != nil {
		route = logger.routeInfo(r)
	}
	if route.OperationID == "" && report.panic != nil {
		route = RouteInfo{OperationID: "unmatchedRoute", PathTemplate: "/"}
	}
	status, class, cause := stats.statusCode(), report.class, report.cause
	if clientDropped(r.Context(), status, report) {
		status, class, cause = statusClientClosedRequest, governance.RequestErrorContext, governance.RequestErrorCanceled
	}
	entry, err := newRequestLogEntry(requestID, r.Method, route, status, logger.now().Sub(start))
	if err != nil {
		return
	}
	entry.ErrorClass, entry.ErrorCause = class, cause
	entry.UserID, entry.Client, entry.VoiceCall = report.userID, report.client, report.voice
	if report.panic != nil {
		entry.Panic, entry.Stack = governance.PanicText(report.panic.Value), governance.PanicText(report.panic.Stack)
	}
	logger.write(entry)
}

// statusClientClosedRequest is the status of the line of a request its client dropped, as nginx writes it.
const statusClientClosedRequest = 499

// clientDropped says the request failed because its client left: the context of the request itself is cancelled — the
// server does that when the client closes the connection, and neither its graceful stop nor a timeout does — and the
// handler answered 5xx into the closed connection. Such a line is the client's 499, whatever error the cancelled read
// gave and whether the handler reported it (specs/2026-10-28-production-monitoring.md R5). A panic and a timeout the
// handler reported stay failures of the server; so does an error, a cancellation included, met while the client waits.
func clientDropped(request context.Context, status int, report *requestReport) bool {
	return status >= http.StatusInternalServerError && request.Err() == context.Canceled &&
		report.panic == nil && report.cause != governance.RequestErrorDeadlineExceeded
}

type requestIDContextKey struct{}

// ContextWithRequestID связывает с контекстом id запроса; так его видят ответы об ошибках и
// задания, созданные этим запросом.
func ContextWithRequestID(ctx context.Context, requestID governance.RequestID) context.Context {
	return context.WithValue(ctx, requestIDContextKey{}, requestID)
}

func RequestIDFromContext(ctx context.Context) (governance.RequestID, bool) {
	requestID, ok := ctx.Value(requestIDContextKey{}).(governance.RequestID)
	return requestID, ok && requestID.Validate() == nil
}

type requestReportContextKey struct{}

// requestReport is what the handlers of a request add to its log line: an unexpected error and who
// asked.
type requestReport struct {
	class  governance.RequestErrorClass
	cause  governance.RequestErrorCause
	panic  *PanicError
	userID governance.ResourceID
	client governance.ClientAddress
	voice  *voiceCallLogEntry
}

func reportOf(ctx context.Context) *requestReport {
	report, _ := ctx.Value(requestReportContextKey{}).(*requestReport)
	return report
}

// ReportRequestUser names in the request line the signed-in user the request acts for, or the one
// it has just signed in (SEC-17, specs/2026-10-28-security-hardening.md R4.1). A value that is not
// a canonical UUID is not written.
func ReportRequestUser(ctx context.Context, userID string) {
	if report := reportOf(ctx); report != nil && governance.ResourceID(userID).Validate() == nil {
		report.userID = governance.ResourceID(userID)
	}
}

// ReportRequestClient names in the request line the client address of an anonymous sign-in,
// registration or password reset: the address the rate limiter counts (SEC-17, R4.1). A value that
// is not an address or a network is not written.
func ReportRequestClient(ctx context.Context, address string) {
	if report := reportOf(ctx); report != nil {
		if client, err := governance.NewClientAddress(address); err == nil {
			report.client = client
		}
	}
}

// VoiceCallQuality is what the browser measured of the voice it heard in a call: the counters of the incoming audio of
// WebRTC (getStats), its median bitrate while the voice spoke and the round trip of the connection. Numbers only — no
// sound, no words (specs/2026-11-03-voice-assistant.md V0.11).
type VoiceCallQuality struct {
	PacketsReceived          int64
	PacketsLost              int64
	ConcealedSamples         int64
	TotalSamplesReceived     int64
	JitterBufferDelay        float64
	JitterBufferEmittedCount int64
	BitrateKbps              float64
	RoundTripTime            float64
}

// ReportVoiceCall writes into the request line of a voice call, the line written when the call ends, the gateway's id of
// the call and the quality of its sound. A call id that is not the gateway's is not written, and then neither is the
// quality.
func ReportVoiceCall(ctx context.Context, callID string, quality VoiceCallQuality) {
	report := reportOf(ctx)
	id, err := governance.NewVoiceCallID(callID)
	if report == nil || err != nil {
		return
	}
	// RTCP counts a duplicate against a loss, so packetsLost may go below zero: none were lost then.
	count := func(value int64) governance.Count { return governance.Count(max(value, 0)) }
	report.voice = &voiceCallLogEntry{
		CallID: id, PacketsReceived: count(quality.PacketsReceived), PacketsLost: count(quality.PacketsLost),
		ConcealedSamples: count(quality.ConcealedSamples), TotalSamplesReceived: count(quality.TotalSamplesReceived),
		JitterBufferDelay: governance.NewMeasure(quality.JitterBufferDelay), JitterBufferEmittedCount: count(quality.JitterBufferEmittedCount),
		BitrateKbps: governance.NewMeasure(quality.BitrateKbps), RoundTripTime: governance.NewMeasure(quality.RoundTripTime),
	}
}

func ReportUnexpectedError(ctx context.Context, err error) {
	report, ok := ctx.Value(requestReportContextKey{}).(*requestReport)
	if !ok || report == nil || err == nil {
		return
	}
	report.class, report.cause = ClassifyError(err)
}

func (logger *requestLogger) resolveRequestID(inbound string) governance.RequestID {
	if requestID, err := governance.NewRequestID(inbound); err == nil {
		return requestID
	}
	requestID, err := governance.NewRequestID(logger.newRequestID())
	if err != nil {
		panic("observability: request ID generator returned an invalid ID")
	}
	return requestID
}

func (logger *requestLogger) write(entry requestLogEntry) {
	logger.mu.Lock()
	defer logger.mu.Unlock()
	_ = json.NewEncoder(logger.out).Encode(entry)
}

// requestLogEntry — строка журнала одного запроса: только типизированные операционные поля,
// UUID ресурсов из шаблона пути, кто спрашивает (пользователь сессии или только что вошедший;
// у анонимного входа, регистрации и сброса пароля — адрес клиента, SEC-17), у звонка голоса —
// числа качества его звука (V0.11) и, после паники обработчика, её значение и стек — без запроса,
// тела, заголовков и текста ошибок.
type requestLogEntry struct {
	RequestID    governance.RequestID         `json:"requestId"`
	Method       governance.HTTPMethod        `json:"method"`
	OperationID  governance.RouteOperationID  `json:"operationId"`
	PathTemplate governance.RoutePathTemplate `json:"pathTemplate"`
	Status       governance.HTTPStatus        `json:"status"`
	Duration     governance.DurationMillis    `json:"durationMs"`
	ProjectID    governance.ResourceID        `json:"projectId,omitempty"`
	BookID       governance.ResourceID        `json:"bookId,omitempty"`
	ChapterID    governance.ResourceID        `json:"chapterId,omitempty"`
	ChatID       governance.ResourceID        `json:"chatId,omitempty"`
	JobID        governance.ResourceID        `json:"jobId,omitempty"`
	QueueID      governance.ResourceID        `json:"queueId,omitempty"`
	NoteID       governance.ResourceID        `json:"noteId,omitempty"`
	InvitationID governance.ResourceID        `json:"invitationId,omitempty"`
	MemberID     governance.ResourceID        `json:"memberId,omitempty"`
	UserID       governance.ResourceID        `json:"userId,omitempty"`
	Client       governance.ClientAddress     `json:"client,omitempty"`
	ErrorClass   governance.RequestErrorClass `json:"errorClass,omitempty"`
	ErrorCause   governance.RequestErrorCause `json:"errorCause,omitempty"`
	Panic        governance.PanicText         `json:"panic,omitempty"`
	Stack        governance.PanicText         `json:"stack,omitempty"`
	VoiceCall    *voiceCallLogEntry           `json:"voiceCall,omitempty"`
}

// voiceCallLogEntry is the quality of the sound of a voice call in its request line (V0.11): the gateway's id of the call
// and numbers the browser measured; JitterBufferDelay and RoundTripTime are in seconds, as WebRTC gives them.
type voiceCallLogEntry struct {
	CallID                   governance.VoiceCallID `json:"callId"`
	PacketsReceived          governance.Count       `json:"packetsReceived"`
	PacketsLost              governance.Count       `json:"packetsLost"`
	ConcealedSamples         governance.Count       `json:"concealedSamples"`
	TotalSamplesReceived     governance.Count       `json:"totalSamplesReceived"`
	JitterBufferDelay        governance.Measure     `json:"jitterBufferDelay"`
	JitterBufferEmittedCount governance.Count       `json:"jitterBufferEmittedCount"`
	BitrateKbps              governance.Measure     `json:"bitrateKbps"`
	RoundTripTime            governance.Measure     `json:"roundTripTime"`
}

// setResources переносит в строку журнала UUID ресурсов из параметров пути; значения, не
// являющиеся каноническим UUID, отбрасываются.
func (entry *requestLogEntry) setResources(values map[string]string) {
	for name, field := range map[string]*governance.ResourceID{
		"projectId": &entry.ProjectID, "bookId": &entry.BookID, "chapterId": &entry.ChapterID,
		"chatId": &entry.ChatID, "jobId": &entry.JobID, "queueId": &entry.QueueID,
		"noteId": &entry.NoteID, "invitationId": &entry.InvitationID, "memberId": &entry.MemberID,
	} {
		if id := governance.ResourceID(values[name]); id.Validate() == nil {
			*field = id
		}
	}
}

func newRequestLogEntry(
	requestID governance.RequestID,
	method string,
	route RouteInfo,
	status int,
	duration time.Duration,
) (requestLogEntry, error) {
	typedMethod, err := governance.NewHTTPMethod(method)
	if err != nil {
		return requestLogEntry{}, err
	}
	operationID, err := governance.NewRouteOperationID(route.OperationID)
	if err != nil {
		return requestLogEntry{}, err
	}
	pathTemplate, err := governance.NewRoutePathTemplate(route.PathTemplate)
	if err != nil {
		return requestLogEntry{}, err
	}
	typedStatus, err := governance.NewHTTPStatus(status)
	if err != nil {
		return requestLogEntry{}, err
	}
	durationMillis, err := governance.NewDurationMillis(duration)
	if err != nil {
		return requestLogEntry{}, err
	}
	entry := requestLogEntry{
		RequestID: requestID, Method: typedMethod, OperationID: operationID,
		PathTemplate: pathTemplate, Status: typedStatus, Duration: durationMillis,
	}
	entry.setResources(route.PathValues)
	return entry, nil
}

func newSecureRequestID() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		panic(err)
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	var encoded [36]byte
	hex.Encode(encoded[0:8], value[0:4])
	encoded[8] = '-'
	hex.Encode(encoded[9:13], value[4:6])
	encoded[13] = '-'
	hex.Encode(encoded[14:18], value[6:8])
	encoded[18] = '-'
	hex.Encode(encoded[19:23], value[8:10])
	encoded[23] = '-'
	hex.Encode(encoded[24:36], value[10:16])
	return string(encoded[:])
}

type responseStats struct {
	http.ResponseWriter
	status int
}

func newResponseStats(w http.ResponseWriter) *responseStats {
	return &responseStats{ResponseWriter: w}
}

func (stats *responseStats) responseWriter() http.ResponseWriter {
	if _, ok := stats.ResponseWriter.(http.Flusher); ok {
		return flushableResponseStats{responseStats: stats}
	}
	return stats
}

func (stats *responseStats) WriteHeader(status int) {
	if stats.status != 0 {
		return
	}
	stats.status = status
	stats.ResponseWriter.WriteHeader(status)
}

func (stats *responseStats) Write(body []byte) (int, error) {
	if stats.status == 0 {
		stats.WriteHeader(http.StatusOK)
	}
	return stats.ResponseWriter.Write(body)
}

func (stats *responseStats) Unwrap() http.ResponseWriter {
	return stats.ResponseWriter
}

func (stats *responseStats) statusCode() int {
	if stats.status == 0 {
		return http.StatusOK
	}
	return stats.status
}

type flushableResponseStats struct {
	*responseStats
}

func (stats flushableResponseStats) Flush() {
	if stats.status == 0 {
		stats.status = http.StatusOK
	}
	stats.ResponseWriter.(http.Flusher).Flush()
}
