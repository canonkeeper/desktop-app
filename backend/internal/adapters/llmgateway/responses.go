// SPDX-License-Identifier: AGPL-3.0-only

package llmgateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/url"
	"time"

	"canonkeeper/backend/internal/adapters/modelapi"
	"canonkeeper/backend/internal/domain/aisettings"
	"canonkeeper/backend/internal/domain/appwork"
	"canonkeeper/backend/internal/domain/coresteps"
	"canonkeeper/backend/internal/domain/disclosure"
	"canonkeeper/backend/internal/domain/modeljournal"

	"github.com/canonkeeper/desktop-app/translate"
)

// maxBody bounds a request: Codex sends the whole thread every time, and the module takes up to 64 MiB.
const maxBody = 64 << 20

// codeNotConnected refuses a request while the helper is not connected (GW1.6): no provider is asked, and the refusal goes
// with 400 — the status Codex 0.167.2 does not repeat; a 403 it repeats for about three minutes
// (specs/2026-11-13-desk-account-ai.md Decision log). The client of the core repeats neither.
const codeNotConnected = "helper.not_connected"

// dropped are the fields of a request the provider does not need (GW1.4): the state of the client, its cache and who it is.
var dropped = []string{"client_metadata", "prompt_cache_key", "prompt_cache_retention", "safety_identifier", "user", "metadata"}

// codeUnbound refuses a request of Codex whose thread is in no turn, whatever the source (specs/2026-11-14-desk-journal.md
// JR1.2): the conversation would go without a record. It goes with appwork.Lasting, the status Codex 0.167.2 does not
// repeat.
const codeUnbound = "turn.unbound"

// role is who asks: Codex of the chat or the core reading the books.
type role int

const (
	roleChat role = iota
	roleCards
)

// call is what the gateway knows of one request before the provider.
type call struct {
	role    role
	purpose string
	jobID   string
	stream  bool
	body    []byte
	size    int64
	// threadID is the thread of a request of Codex; checkID the number of the check of a probe; named what a call of the
	// core gives (disclosure.Header) — unnamed when it named nothing or a value of another form (JR1.4).
	threadID string
	checkID  string
	named    disclosure.Named
	unnamed  bool
	// last is the last attempt of the client of the app (modelapi.LastAttemptHeader): no connection on it pauses the
	// reading (specs/2026-11-13-desk-consent.md CN3.4).
	last bool
	// own says the Job of the request took «Свой ИИ» at its start: it goes there whatever the source of the settings became
	// (specs/2026-11-13-desk-account-ai.md AI1.4).
	own bool
	// checked marks the answer once the check of the reading let the request go, before its first byte
	// (modelapi.CheckedHeader; specs/2026-11-15-desk-reading-no-answer.md NA1.1).
	checked func()
}

// errNotConnected is a request while the helper is not connected; errNotObject a body that is not a JSON object.
var (
	errNotConnected = errors.New("llmgateway: the helper is not connected")
	errNotObject    = errors.New("llmgateway: the body is not an object")
	// errJournal is a sending whose record did not lie, or a gateway without its journal (JR1.1): nothing is sent.
	errJournal = errors.New("llmgateway: the record of the sending did not lie")
)

// responses takes a request of Codex or of the core to the provider of the author (GW1.2–GW1.7).
func (gateway *Gateway) responses(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBody+1))
	if err != nil {
		writeError(w, http.StatusBadRequest, translate.ErrorCodeInvalidRequest, "the request could not be read")
		return
	}
	if len(body) > maxBody {
		writeError(w, http.StatusRequestEntityTooLarge, translate.ErrorCodeRequestTooLarge, "the request is too large")
		return
	}
	request, refusal := gateway.read(r, body)
	if refusal != "" {
		writeError(w, http.StatusBadRequest, translate.ErrorCodeInvalidRequest, refusal)
		return
	}
	switch {
	case request.role == roleChat && request.jobID == "":
		// JR1.2: a request of Codex outside a turn goes nowhere, whatever the source, before any place or record. The log
		// names the event alone: no thread, no body.
		gateway.logger.Print(`{"event":"gateway_unbound"}`)
		writeError(w, appwork.Lasting, codeUnbound, "the thread of the request is in no turn")
		return
	case request.role == roleCards && request.unnamed:
		// JR1.4: a call of the core that named nothing it gives is not sent.
		writeError(w, appwork.Lasting, disclosure.CodeUnnamed, "the call names nothing it gives")
		return
	}
	// specs/2026-11-13-desk-account-ai.md AI1.4: a request of a Job goes to the source its Job took at its start; the helper
	// of the account has its own way to the site.
	settings := gateway.settings.AI()
	source, work := gateway.sourceOf(r.Context(), request, settings)
	if source == aisettings.SourceAccount {
		gateway.viaAccount(w, r, request, work, settings)
		return
	}
	request.own = source == aisettings.SourceOwn
	request.checked = func() { w.Header().Set(modelapi.CheckedHeader, "1") }
	if !request.stream {
		result, err := gateway.send(r.Context(), request, nil)
		if err != nil {
			gateway.heardJournal(request, err)
			answerFailure(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(result.Response)
		return
	}
	// The events of the provider go to the client as they come; a failure before the first event is answered with its
	// status, after it the stream ends without its terminal and the client repeats as it does after a break. A provider of
	// Chat Completions that refused before its answer is said by the module as response.failed before any other event: it
	// is a failure before the first event too, answered with its status and Retry-After (GW1.5).
	flusher, _ := w.(http.Flusher)
	started := false
	_, err = gateway.send(r.Context(), request, func(event translate.Event) error {
		if !started && event.Type == "response.failed" {
			return nil
		}
		if !started {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.WriteHeader(http.StatusOK)
			started = true
		}
		var frame bytes.Buffer
		frame.WriteString("event: " + event.Type + "\n")
		for _, line := range bytes.Split(event.Data, []byte("\n")) {
			frame.WriteString("data: ")
			frame.Write(line)
			frame.WriteByte('\n')
		}
		frame.WriteByte('\n')
		if _, err := w.Write(frame.Bytes()); err != nil {
			return err
		}
		if flusher != nil {
			flusher.Flush()
		}
		return nil
	})
	if err != nil && !started {
		gateway.heardJournal(request, err)
		answerFailure(w, err)
	}
}

// heardJournal tells the turn of a request of Codex the journal refused why it fails (JR1.6): the port of the reason of a
// turn of the build — the count of the work of the app.
func (gateway *Gateway) heardJournal(request call, err error) {
	if !errors.Is(err, errJournal) || request.role != roleChat || request.jobID == "" {
		return
	}
	if box := gateway.account.Load(); box != nil {
		box.account.Refused(request.jobID, request.purpose, appwork.Answer{Status: appwork.Lasting, Reason: appwork.ReasonJournal})
	}
}

// send is the one way a request goes to the provider of the author, for Codex, the core and the probes of «Проверить
// связь» alike (GW1.2–GW1.7; specs/2026-11-13-desk-probes.md PB2.2): the settings and the key read now, the rule «помощник
// подключён», the model of the role, a place among «Запросов одновременно», the protocol, the profile in the body, the
// module, and the row of the journal once the provider was asked. A stream hands its events to emit.
func (gateway *Gateway) send(ctx context.Context, request call, emit func(translate.Event) error) (translate.Result, error) {
	settings := gateway.settings.AI()
	if request.own {
		settings.Source = aisettings.SourceOwn
	}
	key, held := gateway.settings.Key()
	if !settings.Connected(aisettings.Held{Key: held}) {
		return translate.Result{}, errNotConnected
	}
	own := *settings.Own
	if own.Preset == aisettings.PresetOllama {
		// Ollama of the computer of the author takes no key: a key the author kept for another variant does not go there.
		key = ""
	}
	model := own.ChatModel
	if request.role == roleCards {
		model = own.CardsModel
	}
	entry := modeljournal.Request{
		Purpose: request.purpose, JobID: request.jobID, Profile: string(aisettings.SourceOwn),
		ProviderHost: hostOf(own.BaseURL), Model: model, RequestBytes: request.size,
	}
	lane := laneOf(request)
	if err := gateway.gate.acquire(ctx, lane); err != nil {
		return translate.Result{}, err
	}
	defer gateway.gate.release(lane)
	// specs/2026-11-13-desk-consent.md CN3.3: a request of the reading goes to the provider only once the check of the
	// consent let it go, with its reserve, after its place and before the provider — before «Определить» too: a request of the
	// reading the check did not let go asks the provider nothing (specs/2026-11-15-desk-reading-no-answer.md NA1.1).
	ticket, err := gateway.admit(ctx, request, entry, settings)
	if err != nil {
		return translate.Result{}, err
	}
	if ticket != nil && request.checked != nil {
		// The client of the app learns that a provider that does not answer this request pauses the reading here.
		request.checked()
	}
	// «Определить» is a sending of its own (JR1.7): its record lies before its request — the number of the check it runs in,
	// or a number of its own inside a work —, its row after.
	check := request.checkID
	protocol, err := gateway.protocols.resolve(ctx, own, key, func(bytes int64) (string, error) {
		if check == "" {
			check = modeljournal.NewID()
		}
		return gateway.write(ctx, modeljournal.Sending{
			Purpose: modeljournal.PurposeProbe, CheckID: check, Profile: entry.Profile, Host: entry.ProviderHost, Bytes: bytes,
			Named: disclosure.Named{Protocol: true},
		})
	}, func(probe modeljournal.Request) {
		probe.CheckID, probe.Profile, probe.ProviderHost, probe.Model = check, entry.Profile, entry.ProviderHost, model
		gateway.record(ctx, probe)
	})
	if err != nil {
		if ticket != nil {
			// «Определить» did not pass, so the request went nowhere and took nothing: its reserve goes without a row or a count
			// (JR1.1) — «Определить» has its own record and row —, and its failure is heard as a refusal of the provider before
			// its answer, with the rule of the last attempt (NA1.1); a request the client or the Job canceled is no refusal, nor is
			// a record that did not lie.
			ticket.Release(context.WithoutCancel(ctx))
			if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, errJournal) {
				ticket.Failed(context.WithoutCancel(ctx), failureOf(err), request.last)
			}
		}
		return translate.Result{}, err
	}
	target := translate.Target{BaseURL: own.BaseURL, Credential: key, Model: model, Capabilities: capabilities(protocol, own)}
	rewritten, err := rewrite(request.body, own)
	if err != nil {
		if ticket != nil {
			ticket.Release(context.WithoutCancel(ctx))
		}
		return translate.Result{}, errNotObject
	}
	// JR1.1: the record of the sending lies right before its body goes — the body as it goes to the provider, in its protocol
	// after the translation (its size known before the first byte), to whom it goes, what of the saga it gives —, or nothing
	// goes. A request the module refused before its body went has no record either.
	sending := func(bytes int) error {
		id, err := gateway.write(ctx, modeljournal.Sending{
			JobID: request.jobID, ThreadID: request.threadID, CheckID: request.checkID, Purpose: request.purpose,
			Profile: entry.Profile, Host: entry.ProviderHost, Bytes: int64(bytes), Named: request.named,
		})
		entry.ID, entry.CheckID, entry.At = id, request.checkID, time.Now()
		return err
	}
	var result translate.Result
	var seen characters
	if request.stream {
		result, err = gateway.translator.OpenStream(ctx, target, translate.Request{Body: rewritten, Sending: sending}, func(event translate.Event) error {
			seen.event(event)
			if emit == nil {
				return nil
			}
			return emit(withStopReason(event, seen.began))
		})
	} else {
		result, err = gateway.translator.Execute(ctx, target, translate.Request{Body: rewritten, Sending: sending})
		seen.ofResponse(result.Response)
	}
	if entry.ID == "" {
		// Nothing went — the record did not lie, or the module refused the request before its body: no row and no count, the
		// reserve of the check is released (JR1.1); a refusal of the module is still heard as the provider's would be, a
		// record that did not lie pauses nothing.
		if ticket != nil {
			ticket.Release(context.WithoutCancel(ctx))
			if err != nil && !errors.Is(err, errJournal) && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
				ticket.Failed(context.WithoutCancel(ctx), failureOf(err), request.last)
			}
		}
		return result, err
	}
	entry.Duration = time.Since(entry.At)
	entry.Outcome = outcomeOf(ctx, result, err)
	entry.Failure = failureClass(entry.Outcome, err, seen)
	entry.OutputCharacters = seen.count
	if result.UsageKnown {
		usage := result.Usage
		entry.Tokens = &modeljournal.Tokens{Input: tokens(usage.InputTokens), CachedInput: tokens(usage.CachedTokens), Output: tokens(usage.OutputTokens)}
		entry.Cost = costOf(own, entry.Tokens)
	}
	if ticket == nil {
		gateway.record(ctx, entry)
		return result, err
	}
	// The count of the reading and the row of the journal are one write; a refusal of the provider that stops the reading
	// pauses it at once (CN3.3, CN3.4).
	ticket.Settle(context.WithoutCancel(ctx), entry, seen.began)
	// The client of the core may leave as soon as it read the failure in the stream: the refusal is heard all the same; a
	// request the client or the Job canceled is no refusal. A provider of Responses that ended its stream with
	// response.failed is heard by that event; a failure after the provider began its answer is no answer, the connection
	// torn mid-answer included (specs/2026-11-15-desk-reading-no-answer.md NA1.1).
	switch {
	case err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded):
		ticket.Failed(context.WithoutCancel(ctx), seen.of(failureOf(err)), request.last)
	case err == nil && seen.failure != "":
		ticket.Failed(context.WithoutCancel(ctx), seen.of(seen.failure), request.last)
	}
	return result, err
}

// answerFailure answers a request that failed before any event: without the helper 400 helper.not_connected (GW1.6), a
// body that is no object 400, otherwise as the provider or the module failed.
func answerFailure(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errNotConnected):
		writeError(w, http.StatusBadRequest, codeNotConnected, "the helper is not connected")
	case errors.Is(err, errNotObject):
		writeError(w, http.StatusBadRequest, translate.ErrorCodeInvalidRequest, "the request is not a JSON object")
	case errors.Is(err, errJournal):
		// JR1.1, JR1.6: neither Codex nor the client of the core repeats it; the client of the app hands it typed.
		writeError(w, appwork.Lasting, disclosure.CodeUnavailable, "the record of the sending could not be written")
	case errors.As(err, new(*readingRefused)):
		// The check did not let the request go (CN3.3): the client of the app does not repeat it.
		var refused *readingRefused
		errors.As(err, &refused)
		writeReason(w, http.StatusConflict, codeReadingPaused, refused.reason, "the reading of the books is paused")
	default:
		writeFailure(w, err)
	}
}

// PurposeHeader is the header a request could name its purpose with; the gateway names it itself and refuses it.
const PurposeHeader = "X-CanonKeeper-Purpose"

// read names the role, the purpose and the Job of a request (GW1.2, GW1.7): the alias of the model names the role; a
// request of Codex is the chat and its Job is the one the turn put for its thread, a call of the core is the step whose
// words it gives — other words are refused — and its Job comes in modelapi.JobHeader. The purpose is the gateway's: a
// request that names one by a header or a field is refused — the probe of «Проверить связь» goes only from the backend
// itself (specs/2026-11-13-desk-probes.md PB2.2).
func (gateway *Gateway) read(r *http.Request, body []byte) (call, string) {
	var head struct {
		Model        string           `json:"model"`
		Stream       bool             `json:"stream"`
		Instructions string           `json:"instructions"`
		Purpose      *json.RawMessage `json:"purpose"`
	}
	if json.Unmarshal(body, &head) != nil {
		return call{}, "the request is not a JSON object of Responses"
	}
	if head.Purpose != nil || len(r.Header.Values(PurposeHeader)) > 0 {
		return call{}, "the purpose of a request is the gateway's"
	}
	request := call{stream: head.Stream, body: body, size: int64(len(body)), last: r.Header.Get(modelapi.LastAttemptHeader) != ""}
	values := r.Header.Values(disclosure.Header)
	switch head.Model {
	case ChatModel:
		if len(values) > 0 {
			// What a request of Codex gives its tools named in their thread (JR1.4).
			return call{}, "what a request of Codex gives is named by its turn"
		}
		request.role, request.purpose = roleChat, modeljournal.PurposeChat
		request.threadID = r.Header.Get("thread-id")
		request.jobID = gateway.threads.Job(request.threadID)
	case CardsModel:
		step, ok := coresteps.Of(head.Instructions)
		if !ok {
			return call{}, "the words of the request are no step of the core"
		}
		request.role, request.purpose = roleCards, string(step.Name)
		request.jobID = r.Header.Get(modelapi.JobHeader)
		named, err := disclosure.Parse(r.Header.Get(disclosure.Header))
		request.named, request.unnamed = named, len(values) != 1 || err != nil || named.Empty()
	default:
		return call{}, "the model is neither " + ChatModel + " nor " + CardsModel
	}
	return request, ""
}

// capabilities is the profile of the provider: what the author chose and the quirks of the servers of Chat Completions.
func capabilities(protocol translate.Protocol, own aisettings.Own) translate.Capabilities {
	capabilities := translate.Capabilities{
		Protocol: protocol, Tools: true, ParallelToolCalls: true, ImageInput: true,
		Reasoning: own.Advanced.Reasoning != aisettings.ReasoningNone,
	}
	if protocol == translate.ProtocolChatCompletions {
		capabilities.ThinkTags, capabilities.AllowNoSSEDone, capabilities.AllowMissingSSEContentType = true, true, true
	}
	return capabilities
}

// rewrite puts the profile of the author into the body (GW1.2, GW1.4): the fields the provider does not need go, the effort
// is the author's («Нет» — no reasoning at all, which the module also takes off with the capabilities), and the answer is
// bounded by his limit. The model is put by the module.
func rewrite(body []byte, own aisettings.Own) ([]byte, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(body, &object); err != nil || object == nil {
		return nil, errors.New("llmgateway: the body is not an object")
	}
	for _, field := range dropped {
		delete(object, field)
	}
	if own.Advanced.Reasoning == aisettings.ReasoningNone {
		delete(object, "reasoning")
	} else {
		var reasoning map[string]json.RawMessage
		if json.Unmarshal(object["reasoning"], &reasoning) != nil || reasoning == nil {
			reasoning = map[string]json.RawMessage{}
		}
		reasoning["effort"], _ = json.Marshal(string(own.Advanced.Reasoning))
		object["reasoning"], _ = json.Marshal(reasoning)
	}
	object["max_output_tokens"], _ = json.Marshal(own.Advanced.MaxOutput)
	return json.Marshal(object)
}

// writeFailure answers a failure that came before any event (GW1.5): a refusal of the provider with its status and its
// Retry-After — the gateway repeats nothing itself — a request the module refused with 400, a request too large with
// 413, a provider that could not be reached with 502. A client that left gets nothing.
func writeFailure(w http.ResponseWriter, err error) {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return
	}
	var failure *translate.Error
	if !errors.As(err, &failure) {
		writeError(w, http.StatusBadGateway, translate.ErrorCodeUpstreamError, "the provider could not be asked")
		return
	}
	status := http.StatusBadGateway
	switch {
	case failure.Status != 0:
		status = failure.Status
		if failure.RetryAfter != "" {
			w.Header().Set("Retry-After", failure.RetryAfter)
		}
	case failure.Code == translate.ErrorCodeRequestTooLarge:
		status = http.StatusRequestEntityTooLarge
	case failure.Code == translate.ErrorCodeInvalidRequest || failure.Code == translate.ErrorCodeUnsupportedCapability:
		status = http.StatusBadRequest
	}
	// The refusal names its stop of the reading by the one table of the domain (specs/2026-11-13-desk-consent.md CN3.4):
	// the client of the app does not repeat the funds, the key and the model, and stops at no connection after its repeats.
	writeReason(w, status, failure.Code, stopReason(failureOf(err)), failure.Message)
}

// stopReason is the word of the reason of a refusal the client of the app does not repeat, or none: a stop of the reading,
// or refused — a refusal of the class FailureRefused, which the same settings get again
// (specs/2026-11-15-desk-reading-rest.md RR1.1).
func stopReason(failure aisettings.Failure) string {
	switch failure {
	case aisettings.FailureFunds:
		return "funds"
	case aisettings.FailureKey:
		return "key"
	case aisettings.FailureModel:
		return "model"
	case aisettings.FailureUnreachable:
		return "connection"
	case aisettings.FailureRefused:
		return reasonRefused
	default:
		return ""
	}
}

// reasonRefused names a refusal of the provider of the class FailureRefused (RR1.1): the client of the app hands it as
// sagamemory.ErrRefused.
const reasonRefused = "refused"

// outcomeOf is how the request ended for the journal.
func outcomeOf(ctx context.Context, result translate.Result, err error) modeljournal.Outcome {
	switch {
	case ctx.Err() != nil || errors.Is(err, context.Canceled):
		return modeljournal.OutcomeCanceled
	case result.IncompleteReason != "":
		return modeljournal.OutcomeIncomplete
	case err != nil || result.Failed:
		return modeljournal.OutcomeFailed
	default:
		return modeljournal.OutcomeCompleted
	}
}

// write lies the record of a sending with a number of its own and the time of now (JR1.1); a gateway without its journal,
// or a record that did not lie, sends nothing. A client that left does not take the record with it, and the log hears only
// that it failed — never what it named.
func (gateway *Gateway) write(ctx context.Context, sending modeljournal.Sending) (string, error) {
	box := gateway.journal.Load()
	if box == nil {
		return "", errJournal
	}
	sending.ID, sending.At = modeljournal.NewID(), time.Now()
	if err := box.journal.Send(context.WithoutCancel(ctx), sending); err != nil {
		gateway.logger.Printf(`{"event":"gateway_record_failed","purpose":%q}`, sending.Purpose)
		return "", errJournal
	}
	return sending.ID, nil
}

// failureClass is how a failed request failed, for the words of the journal (specs/2026-11-14-desk-journal.md JR2.4): its
// answer broke off once it began, no answer came for the connection, or the provider refused it before its answer.
func failureClass(outcome modeljournal.Outcome, err error, seen characters) string {
	switch {
	case outcome != modeljournal.OutcomeFailed:
		return ""
	case seen.began:
		return modeljournal.FailureTorn
	case err != nil && failureOf(err) == aisettings.FailureUnreachable:
		return modeljournal.FailureConnection
	default:
		return modeljournal.FailureRefused
	}
}

// record writes the journal; a client that left does not take the row with it.
func (gateway *Gateway) record(ctx context.Context, request modeljournal.Request) {
	box := gateway.journal.Load()
	if box == nil {
		return
	}
	if request.At.IsZero() {
		request.At = time.Now()
	}
	if err := box.journal.Record(context.WithoutCancel(ctx), request); err != nil {
		gateway.logger.Print(`{"event":"gateway_journal_failed"}`)
	}
}

// tokens is a count of the provider for the journal, which keeps a bigint.
func tokens(count uint64) int64 {
	return int64(min(count, math.MaxInt64))
}

// hostOf is the host of the provider for the journal: its name and port, never a path or a key.
func hostOf(baseURL string) string {
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Host == "" {
		return "unknown"
	}
	return parsed.Host
}
