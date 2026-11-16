// SPDX-License-Identifier: AGPL-3.0-only

package llmgateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	accountdomain "canonkeeper/backend/internal/domain/accountlink"
	"canonkeeper/backend/internal/domain/aisettings"
	"canonkeeper/backend/internal/domain/appwork"
	"canonkeeper/backend/internal/domain/coresteps"
	"canonkeeper/backend/internal/domain/disclosure"
	"canonkeeper/backend/internal/domain/modeljournal"
)

// Account is the helper «Аккаунт CanonKeeper» as the gateway needs it (specs/2026-11-13-desk-account-ai.md AI1.4, AI2.3–AI2.6,
// AI3.1–AI3.3): the work of a Job a site counts and its permit — which the gateway only finds, never opens, except the permit
// of a reading, which spends no unit —, the client of the app to the site, and what it hears of the refusals. The build
// implements it over the count of the work and the one client of the app to the site.
type Account interface {
	// Linked says the app is linked: the helper of the account is connected (AI1.2).
	Linked() bool
	// Host is the host of the site, for the journal.
	Host() string
	// WorkOf is the work of a Job: the source it took at its start and the permit the process holds for it; false — the Job
	// took no source the app keeps (the reading of the books, a request of Codex outside a turn).
	WorkOf(ctx context.Context, jobID string) (JobWork, bool)
	// Materials is the permit of the upload of materials of a Job of the account, found again by the ref of its start; it
	// never opens a new one.
	Materials(ctx context.Context, jobID string) (string, error)
	// Reading is the permit of the reading of a Job; renew — the site ended the one held, the next number opens.
	Reading(ctx context.Context, jobID string, renew bool) (string, error)
	// Refused hears the refusal of the site a request of the Job got: the reason of its Job and the refusals that repeat.
	Refused(jobID, purpose string, answer appwork.Answer)
	// Accepted hears a request of the Job the site took: no reason stays.
	Accepted(jobID string)
	// Dropped hears a request of a turn that ended mid-stream: the site reads the rest without the app.
	Dropped(jobID string)
	// RefusedNow is the code of a refusal the app remembers for work of the purpose in the chat; "" lets it go (AI2.6).
	RefusedNow(purpose, chatID string) string
	// Responses is the stream of a turn at the site under its permit.
	Responses(ctx context.Context, permitID string, body []byte) (io.ReadCloser, error)
	// Step is a step of the site under the permit of its kind.
	Step(ctx context.Context, step, permitID string, body []byte) (StepAnswer, error)
}

// JobWork is the work of a Job: its source, kind, permit and chat.
type JobWork struct {
	Source   appwork.Source
	Kind     appwork.Kind
	PermitID string
	ChatID   string
}

// StepAnswer is the answer of a step of the site: the text whole, its model and its usage.
type StepAnswer struct {
	Text  string
	Model string
	Usage StepUsage
}

// StepUsage is the usage of a step as the site counted it.
type StepUsage struct {
	InputTokens, CachedInputTokens, OutputTokens, ReasoningOutputTokens int64
}

// UseAccount makes the gateway serve the helper of the account once the count of the work is composed.
func (gateway *Gateway) UseAccount(account Account) {
	gateway.account.Store(&accountBox{account: account})
}

type accountBox struct{ account Account }

// The places of the helper of the account (AI3.3): those the site keeps for an app; the reading holds all but one.
const (
	accountPlaces        = appwork.PlacesPerApp
	accountReadingPlaces = appwork.ReadingPlaces
)

// codeNoTurn refuses a request of Codex outside a turn of the account: it has no permit, and the gateway sends nothing to
// the site (AI1.4, AI2.3).
const codeNoTurn = "helper.no_turn"

// busyPause bounds the wait for a place of the site when its Retry-After says less.
const busyPause = time.Second

// sourceOf is the helper a request goes to (AI1.4): a request of a Job that took its source at its start goes there, whatever
// the settings became; the reading of the books goes by the settings — the consent named by its source keeps it from
// another one —, and so does a request of Codex outside a turn.
func (gateway *Gateway) sourceOf(ctx context.Context, request call, settings aisettings.Settings) (aisettings.Source, JobWork) {
	box := gateway.account.Load()
	if box == nil || request.jobID == "" || readingPurposes[request.purpose] {
		return settings.Source, JobWork{}
	}
	work, ok := box.account.WorkOf(ctx, request.jobID)
	switch {
	case !ok:
		return settings.Source, JobWork{}
	case work.Source == appwork.SourceAccount:
		return aisettings.SourceAccount, work
	default:
		return aisettings.SourceOwn, work
	}
}

// laneOfAccount is the lane of a request of the account at the gate (AI2.10, AI3.3): a turn and its scan go with the chat —
// by the Job of the turn rather than by the alias of the model —, the pass of a chapter before new cards.
func laneOfAccount(request call) lane {
	switch {
	case request.role == roleChat, request.purpose == string(coresteps.Scan):
		return laneChat
	case request.purpose == string(coresteps.CardPass):
		return lanePass
	default:
		return laneReading
	}
}

// viaAccount takes a request of Codex or of the core to the site of the account (AI2.4, AI3.1): a linked app, no refusal the
// app remembers, a place of the gate — at most one for the reading of the books —, the check of the consent for the reading,
// the permit of the Job, and the place of the site waited for while it says ai.busy (the named exception from «the gateway
// repeats nothing», AI2.5).
func (gateway *Gateway) viaAccount(w http.ResponseWriter, r *http.Request, request call, work JobWork, settings aisettings.Settings) {
	box := gateway.account.Load()
	if box == nil || !box.account.Linked() || !request.stream {
		// A status Codex does not repeat (appwork.Lasting): the link broke, nothing goes to the site, and a link that broke
		// mid-work is the reason of its Job (AI2.5).
		if box != nil && request.jobID != "" && !box.account.Linked() {
			box.account.Refused(request.jobID, request.purpose, appwork.Answer{Status: appwork.Lasting, Reason: appwork.ReasonUnlinked})
		}
		writeError(w, appwork.Lasting, codeNotConnected, "the helper is not connected")
		return
	}
	account := box.account
	ctx := r.Context()
	remembered := account.RefusedNow(request.purpose, work.ChatID)
	if remembered != "" && !readingPurposes[request.purpose] {
		gateway.refuseLocally(w, account, request, remembered)
		return
	}
	lane := laneOfAccount(request)
	if err := gateway.gate.acquire(ctx, lane); err != nil {
		return
	}
	defer gateway.gate.release(lane)
	entry := modeljournal.Request{
		Purpose: request.purpose, JobID: request.jobID, Profile: string(aisettings.SourceAccount), ProviderHost: account.Host(),
		Model: modelOfAccount(settings, request.role), RequestBytes: request.size,
	}
	ticket, err := gateway.admit(ctx, request, entry, settings)
	if err != nil {
		answerFailure(w, err)
		return
	}
	if remembered != "" {
		// A step of the reading the site does not take from this version pauses the consent as its first refusal did
		// (AI3.5): nothing goes to the site, and the reading the consent let go waits for the author.
		if stop := stopOfAccount(appwork.Refusal{Code: remembered}); stop != "" && ticket != nil {
			ticket.Stop(context.WithoutCancel(ctx), stop)
		}
		gateway.refuseLocally(w, account, request, remembered)
		return
	}
	permit, err := gateway.permitOf(ctx, account, request, work)
	if err != nil {
		if refusal, ok := appwork.AsRefusal(err); ok && refusal.Code == codeNoTurn {
			if ticket != nil {
				// Nothing went: the reserve goes without a row or a count (specs/2026-11-14-desk-journal.md JR1.1).
				ticket.Release(context.WithoutCancel(ctx))
			}
			gateway.refuseLocally(w, account, request, codeNoTurn)
			return
		}
		gateway.siteFailed(w, account, request, err, withAt(entry, ""), ticket)
		return
	}
	entry.At = time.Now()
	if request.role == roleChat {
		gateway.turnViaAccount(w, ctx, account, request, permit, entry)
		return
	}
	gateway.stepViaAccount(w, ctx, account, request, permit, entry, ticket)
}

func withAt(entry modeljournal.Request, outcome modeljournal.Outcome) modeljournal.Request {
	if entry.At.IsZero() {
		entry.At = time.Now()
	}
	if outcome != "" {
		entry.Outcome = outcome
	}
	return entry
}

// modelOfAccount is the model of the helper of the account the journal names: the site tells the names for the page.
func modelOfAccount(settings aisettings.Settings, role role) string {
	if helper := settings.Account; helper != nil {
		if role == roleChat && helper.ChatModel != "" {
			return helper.ChatModel
		}
		if role == roleCards && helper.CardsModel != "" {
			return helper.CardsModel
		}
	}
	return string(aisettings.SourceAccount)
}

// permitOf is the permit of the Job of a request (AI2.3, AI3.2): the turn's and its scan's — found, never opened —, the
// upload's found again by the ref of its start, the reading's opened at its first step. A request without one goes nowhere.
func (gateway *Gateway) permitOf(ctx context.Context, account Account, request call, work JobWork) (string, error) {
	noTurn := &appwork.Refusal{Status: appwork.Lasting, Code: codeNoTurn}
	switch {
	case request.jobID == "":
		return "", noTurn
	case readingPurposes[request.purpose]:
		return account.Reading(ctx, request.jobID, false)
	case request.purpose == string(coresteps.Materials):
		if work.PermitID != "" {
			return work.PermitID, nil
		}
		permit, err := account.Materials(ctx, request.jobID)
		if err == nil && permit == "" {
			return "", noTurn
		}
		return permit, err
	case work.PermitID == "":
		return "", noTurn
	default:
		return work.PermitID, nil
	}
}

// turnViaAccount sends a request of Codex to the stream of the site and hands the stream to Codex as it comes (AI2.4): the
// fields the site needs not go, no effort or bound of an answer is put, no header of Codex goes — the client of the app
// sends its own.
func (gateway *Gateway) turnViaAccount(w http.ResponseWriter, ctx context.Context, account Account, request call, permit string, entry modeljournal.Request) {
	body, err := withoutClientFields(request.body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "the request is not a JSON object")
		return
	}
	var stream io.ReadCloser
	for {
		// JR1.1: every passing of the body to the site lies its record first — a wait for a place of the site included.
		if entry.ID, err = gateway.recordOfAccount(ctx, account, request, len(body)); err != nil {
			gateway.journalRefused(w, account, request)
			return
		}
		entry.At = time.Now()
		stream, err = account.Responses(ctx, permit, body)
		if wait, busy := busyWait(err); busy {
			gateway.refusedByThePassing(ctx, entry, err)
			if !sleep(ctx, wait) {
				return
			}
			continue
		}
		break
	}
	if err != nil {
		gateway.siteFailed(w, account, request, err, entry, nil)
		return
	}
	defer stream.Close()
	account.Accepted(request.jobID)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)
	var seen streamSeen
	buffer := make([]byte, 32<<10)
	left := false
	for {
		read, readErr := stream.Read(buffer)
		if read > 0 {
			seen.write(buffer[:read])
			if _, writeErr := w.Write(buffer[:read]); writeErr != nil {
				left = true
				break
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		if readErr != nil {
			break
		}
	}
	entry.Duration, entry.OutputCharacters, entry.Tokens = time.Since(entry.At), seen.characters, seen.tokens
	switch {
	case seen.ended && !left:
		entry.Outcome = modeljournal.OutcomeCompleted
		if seen.incomplete {
			entry.Outcome = modeljournal.OutcomeIncomplete
		}
	default:
		// Codex left or the stream broke mid-answer: the site reads the rest without the app, and a late answer may take a
		// returned unit back (AI2.9).
		account.Dropped(request.jobID)
		entry.Outcome, entry.Failure = modeljournal.OutcomeFailed, modeljournal.FailureTorn
		if left || ctx.Err() != nil {
			entry.Outcome, entry.Failure = modeljournal.OutcomeCanceled, ""
		}
	}
	gateway.record(ctx, entry)
}

// stepViaAccount runs a call of the core as a step of the site (AI3.1, AI3.2) and answers the client of the core with a
// stream of Responses built from the answer whole, with the usage of the site; a permit of the reading the site ended
// mid-Job is replaced once by the next number and the step goes again.
func (gateway *Gateway) stepViaAccount(w http.ResponseWriter, ctx context.Context, account Account, request call, permit string, entry modeljournal.Request, ticket Ticket) {
	body, err := stepCall(request.body)
	if err != nil {
		if ticket != nil {
			// Nothing went: the reserve goes without a row or a count (JR1.1).
			ticket.Release(context.WithoutCancel(ctx))
		}
		writeError(w, http.StatusBadRequest, "invalid_request", "the call of the core has no words and input")
		return
	}
	renewed := false
	var answer StepAnswer
	for {
		// JR1.1: every passing of the body to the site lies its record first — a wait for a place of the site and the step
		// again under a new permit included.
		var id string
		if id, err = gateway.recordOfAccount(ctx, account, request, len(body)); err != nil {
			if ticket != nil {
				ticket.Release(context.WithoutCancel(ctx))
			}
			gateway.journalRefused(w, account, request)
			return
		}
		entry.ID, entry.At = id, time.Now()
		answer, err = account.Step(ctx, request.purpose, permit, body)
		if wait, busy := busyWait(err); busy {
			gateway.refusedByThePassing(ctx, entry, err)
			if !sleep(ctx, wait) {
				// The client left while the site was busy: the row of the passing lay, the reserve goes.
				entry.ID = ""
				break
			}
			continue
		}
		if refusal, ok := appwork.AsRefusal(err); ok && refusal.Code == appwork.CodePermitClosed && readingPurposes[request.purpose] && !renewed {
			renewed = true
			gateway.refusedByThePassing(ctx, entry, err)
			if permit, err = account.Reading(ctx, request.jobID, true); err == nil {
				continue
			}
			// The new permit did not open: the passing has its row, nothing more went.
			entry.ID = ""
		}
		break
	}
	if err == nil && ctx.Err() != nil {
		err = ctx.Err()
	}
	if err != nil {
		gateway.siteFailed(w, account, request, err, entry, ticket)
		return
	}
	account.Accepted(request.jobID)
	entry.Duration, entry.Outcome = time.Since(entry.At), modeljournal.OutcomeCompleted
	if answer.Model != "" {
		entry.Model = answer.Model
	}
	entry.Tokens = &modeljournal.Tokens{Input: answer.Usage.InputTokens, CachedInput: answer.Usage.CachedInputTokens, Output: answer.Usage.OutputTokens}
	entry.OutputCharacters = int64(len([]rune(answer.Text)))
	gateway.settleAccount(ctx, entry, ticket, true)
	writeStepStream(w, answer)
}

// recordOfAccount lies the record of a passing of the body to the site (JR1.1): to the server CanonKeeper, the bytes of the body
// as it goes before its compression, what of the saga it gives.
func (gateway *Gateway) recordOfAccount(ctx context.Context, account Account, request call, bytes int) (string, error) {
	return gateway.write(ctx, modeljournal.Sending{
		JobID: request.jobID, ThreadID: request.threadID, Purpose: request.purpose, Profile: string(aisettings.SourceAccount),
		Host: account.Host(), Bytes: int64(bytes), Named: request.named,
	})
}

// journalRefused answers a request whose record did not lie (JR1.1, JR1.6): nothing went to the site; Codex and the client of
// the core do not repeat it, and a turn fails with it.
func (gateway *Gateway) journalRefused(w http.ResponseWriter, account Account, request call) {
	if request.jobID != "" && request.role == roleChat {
		account.Refused(request.jobID, request.purpose, appwork.Answer{Status: appwork.Lasting, Reason: appwork.ReasonJournal})
	}
	writeError(w, appwork.Lasting, disclosure.CodeUnavailable, "the record of the sending could not be written")
}

// siteFailed answers a request the site refused or did not answer, by the one table of the gateway (AI2.5, AI3.5).
func (gateway *Gateway) siteFailed(w http.ResponseWriter, account Account, request call, err error, entry modeljournal.Request, ticket Ticket) {
	entry.Duration, entry.Outcome = time.Since(entry.At), modeljournal.OutcomeFailed
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		entry.Outcome = modeljournal.OutcomeCanceled
		gateway.settleAccount(context.Background(), entry, ticket, false)
		return
	}
	refusal, ok := appwork.AsRefusal(err)
	if !ok && (errors.Is(err, accountdomain.ErrPassInvalid) || errors.Is(err, accountdomain.ErrNotHeld) || errors.Is(err, accountdomain.ErrPassReplaced)) {
		refusal, ok = &appwork.Refusal{Status: http.StatusUnauthorized, Code: appwork.CodePassInvalid}, true
	}
	if !ok {
		// No answer of the site — a broken connection, its term, its 5xx: Codex and the client of the core repeat it, and
		// on the last attempt of the client the reading pauses at «связь».
		answer := appwork.Unreachable()
		if request.jobID != "" {
			account.Refused(request.jobID, request.purpose, answer)
		}
		entry.Failure = failureOfThePassing(err, modeljournal.FailureConnection)
		gateway.settleAccount(context.Background(), entry, ticket, false)
		if ticket != nil {
			ticket.Failed(context.Background(), aisettings.FailureUnreachable, request.last)
		}
		reason := ""
		if request.role == roleCards {
			reason = stopConnection
		}
		writeReason(w, answer.Status, codeUnreachable, reason, "the site could not be reached")
		return
	}
	answer := appwork.AnswerOf(*refusal)
	if request.jobID != "" {
		account.Refused(request.jobID, request.purpose, answer)
	}
	entry.RefusalCode, entry.Failure = modeljournal.RefusalCodeOf(refusal.Code), failureOfThePassing(err, modeljournal.FailureRefused)
	if entry.Failure == modeljournal.FailureUnsent {
		entry.RefusalCode = ""
	}
	gateway.settleAccount(context.Background(), entry, ticket, false)
	stop := ""
	if request.role == roleCards {
		stop = stopOfAccount(*refusal)
		if stop != "" && ticket != nil {
			ticket.Stop(context.Background(), stop)
		}
	}
	status, reason := answer.Status, stop
	if stop != "" {
		// A refusal that stops the reading is not repeated by the client of the core whatever its status.
		status = appwork.Lasting
	} else if request.role == roleCards && refusedByAccount(*refusal) {
		// A lasting refusal without a stop is refused (specs/2026-11-15-desk-reading-rest.md RR1.1): the client of the core
		// hands it as sagamemory.ErrRefused and does not repeat it.
		reason = reasonRefused
	}
	if refusal.RetryAfter > 0 && status != appwork.Lasting {
		w.Header().Set("Retry-After", strconv.Itoa(max(1, int((refusal.RetryAfter+time.Second-1)/time.Second))))
	}
	writeReason(w, status, refusal.Code, reason, "the site refused the request")
}

// failureOfThePassing is the class of a failed passing to the site for the journal: its body did not leave the app — the
// client holds no pass or waits out the site's Retry-After (JR2.4) — or the class the answer of the site gives.
func failureOfThePassing(err error, answered string) string {
	if errors.Is(err, accountdomain.ErrNotSent) {
		return modeljournal.FailureUnsent
	}
	return answered
}

// refusedByThePassing writes the row of a passing the site answered with a code before the gateway passed the body again —
// ai.busy, app.permit_closed (JR1.1: every passing with an answer has its row). The count of the reading is not touched: a
// refusal before the answer costs nothing, and the passing that ends the request settles it.
func (gateway *Gateway) refusedByThePassing(ctx context.Context, entry modeljournal.Request, err error) {
	refusal, ok := appwork.AsRefusal(err)
	if !ok {
		return
	}
	entry.Duration, entry.Outcome, entry.Failure = time.Since(entry.At), modeljournal.OutcomeFailed, modeljournal.FailureRefused
	entry.RefusalCode = modeljournal.RefusalCodeOf(refusal.Code)
	gateway.record(context.WithoutCancel(ctx), entry)
}

// refusedByAccount says a refusal of a step of the site repeats at the same request whoever asks (RR1.1): the provider of the
// site did not take it — a 4xx that is no wait — or it is too large for the site. A link that broke, a permit that ended and
// a fence of the day are no refusal of the request.
func refusedByAccount(refusal appwork.Refusal) bool {
	switch refusal.Code {
	case appwork.CodeTooLarge:
		return true
	case appwork.CodeProviderRefused:
		return refusal.Status >= 400 && refusal.Status < 500 && refusal.Status != http.StatusTooManyRequests
	default:
		return false
	}
}

// refuseLocally answers a refusal the app gives itself, before any place and without the site (AI1.4, AI2.6): a request of
// Codex outside a turn, a refusal the app remembers. Codex does not repeat it; nothing goes to the site and no row is
// written — nothing was asked.
func (gateway *Gateway) refuseLocally(w http.ResponseWriter, account Account, request call, code string) {
	answer := appwork.AnswerOf(appwork.Refusal{Status: appwork.Lasting, Code: code})
	if code == codeNoTurn {
		answer = appwork.Answer{Status: appwork.Lasting, Reason: appwork.ReasonPermitEnded}
	}
	if request.jobID != "" {
		account.Refused(request.jobID, request.purpose, answer)
	}
	stop := ""
	if request.role == roleCards {
		stop = stopOfAccount(appwork.Refusal{Code: code})
	}
	writeReason(w, appwork.Lasting, code, stop, "the helper of the account refused the request")
}

// settleAccount writes the row of the journal of a request of the account: through the check of the reading for a request
// it let go — the count of the consent with it —, else alone.
func (gateway *Gateway) settleAccount(ctx context.Context, entry modeljournal.Request, ticket Ticket, answered bool) {
	if entry.ID == "" {
		// No record lay, so nothing went — the permit was not found or opened: no row and no count, the reserve of the check
		// goes (specs/2026-11-14-desk-journal.md JR1.1).
		if ticket != nil {
			ticket.Release(context.WithoutCancel(ctx))
		}
		return
	}
	if entry.Duration == 0 {
		entry.Duration = time.Since(entry.At)
	}
	if ticket != nil {
		ticket.Settle(context.WithoutCancel(ctx), entry, answered)
		return
	}
	gateway.record(ctx, entry)
}

// codeUnreachable is the code of no answer of the site, and stopConnection the stop of the reading it makes on the last
// attempt of the client.
const (
	codeUnreachable = "ai.account_unreachable"
	stopConnection  = "connection"
)

// stopOfAccount is the stop of the reading a refusal of the site makes (AI3.5), by its code: the characters of the tier, the
// fence of the work of the reading, an app the site does not take, the reading of the site unavailable — as is a refusal of
// its provider of a state that passes, which a repeat cures, by the one rule of both helpers (aisettings.Passes;
// specs/2026-11-15-desk-reading-queue.md RQ3.1).
func stopOfAccount(refusal appwork.Refusal) string {
	switch refusal.Code {
	case appwork.CodeAccountCharacters:
		return "characters"
	case appwork.CodeReadingFence:
		return "reading_fence"
	case appwork.CodeStepInstruction, appwork.CodeStepUnknown, appwork.CodeValidation, appwork.CodeUnsupported, appwork.CodePermitKind, appwork.CodeAppUnfit:
		return "unfit"
	case appwork.CodeProviderFailed, appwork.CodeUnavailable:
		return "unavailable"
	case appwork.CodeProviderRefused:
		if aisettings.Passes(refusal.Status) {
			return "unavailable"
		}
		return ""
	default:
		return ""
	}
}

// busyWait says the site asked to wait for its place, and how long.
func busyWait(err error) (time.Duration, bool) {
	refusal, ok := appwork.AsRefusal(err)
	if !ok || refusal.Code != appwork.CodeBusy {
		return 0, false
	}
	return max(refusal.RetryAfter, busyPause), true
}

func sleep(ctx context.Context, wait time.Duration) bool {
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// withoutClientFields drops what the site needs not — the state of the client, its cache and who it is (GW1.4) — and puts
// nothing of its own: the site sets the model, the effort and the stream (AI2.4).
func withoutClientFields(body []byte) ([]byte, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(body, &object); err != nil || object == nil {
		return nil, errNotObject
	}
	for _, field := range dropped {
		delete(object, field)
	}
	return json.Marshal(object)
}

// stepCall is the body of a step from a call of the core: its words and the text of its one input.
func stepCall(body []byte) ([]byte, error) {
	var request struct {
		Instructions string          `json:"instructions"`
		Input        json.RawMessage `json:"input"`
	}
	if err := json.Unmarshal(body, &request); err != nil || request.Instructions == "" {
		return nil, errors.New("llmgateway: no words")
	}
	input, ok := inputText(request.Input)
	if !ok {
		return nil, errors.New("llmgateway: no input")
	}
	return json.Marshal(struct {
		Instruction string `json:"instruction"`
		Input       string `json:"input"`
	}{request.Instructions, input})
}

// inputText is the text of the input of a call of the core: a string, or the texts of its messages.
func inputText(raw json.RawMessage) (string, bool) {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text, true
	}
	var messages []struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if json.Unmarshal(raw, &messages) != nil || len(messages) == 0 {
		return "", false
	}
	var joined strings.Builder
	for _, message := range messages {
		for _, part := range message.Content {
			joined.WriteString(part.Text)
		}
	}
	return joined.String(), true
}

// writeStepStream answers the client of the core with the stream of Responses it reads (AI3.1): the text, then the end of the
// answer with the usage of the site.
func writeStepStream(w http.ResponseWriter, answer StepAnswer) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	write := func(kind string, event any) {
		data, _ := json.Marshal(event)
		_, _ = io.WriteString(w, "event: "+kind+"\ndata: "+string(data)+"\n\n")
	}
	write("response.output_text.delta", map[string]any{"type": "response.output_text.delta", "delta": answer.Text})
	usage := map[string]any{
		"input_tokens": answer.Usage.InputTokens, "input_tokens_details": map[string]any{"cached_tokens": answer.Usage.CachedInputTokens},
		"output_tokens": answer.Usage.OutputTokens, "output_tokens_details": map[string]any{"reasoning_tokens": answer.Usage.ReasoningOutputTokens},
	}
	write("response.completed", map[string]any{"type": "response.completed", "response": map[string]any{"status": "completed", "usage": usage}})
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
}

// streamSeen reads what the journal needs of a stream of the site as it goes by: whether the answer ended, its usage and
// the characters of its text — never the text itself.
type streamSeen struct {
	pending    []byte
	ended      bool
	incomplete bool
	tokens     *modeljournal.Tokens
	characters int64
}

func (seen *streamSeen) write(chunk []byte) {
	seen.pending = append(seen.pending, chunk...)
	for {
		end := bytes.IndexByte(seen.pending, '\n')
		if end < 0 {
			if len(seen.pending) > maxBody {
				seen.pending = nil
			}
			return
		}
		line := seen.pending[:end]
		seen.pending = seen.pending[end+1:]
		data, ok := bytes.CutPrefix(bytes.TrimRight(line, "\r"), []byte("data:"))
		if !ok {
			continue
		}
		seen.event(bytes.TrimSpace(data))
	}
}

func (seen *streamSeen) event(data []byte) {
	var event struct {
		Type     string `json:"type"`
		Delta    string `json:"delta"`
		Response struct {
			Usage *responseUsage `json:"usage"`
		} `json:"response"`
	}
	if json.Unmarshal(data, &event) != nil {
		return
	}
	switch event.Type {
	case "response.output_text.delta", "response.reasoning_text.delta", "response.reasoning_summary_text.delta",
		"response.function_call_arguments.delta":
		seen.characters += int64(len([]rune(event.Delta)))
	case "response.completed", "response.incomplete":
		seen.ended, seen.incomplete = true, event.Type == "response.incomplete"
		if usage := event.Response.Usage; usage != nil {
			seen.tokens = &modeljournal.Tokens{Input: usage.InputTokens, CachedInput: usage.InputTokensDetails.CachedTokens, Output: usage.OutputTokens}
		}
	}
}

// responseUsage is the usage of the end of an answer of Responses.
type responseUsage struct {
	InputTokens        int64 `json:"input_tokens"`
	InputTokensDetails struct {
		CachedTokens int64 `json:"cached_tokens"`
	} `json:"input_tokens_details"`
	OutputTokens int64 `json:"output_tokens"`
}
