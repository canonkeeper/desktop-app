// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"canonkeeper/backend/internal/auth"
	domain "canonkeeper/backend/internal/domain/readingconsent"
	"canonkeeper/backend/internal/httpapi"
	service "canonkeeper/backend/internal/services/readingconsent"

	"github.com/go-chi/chi/v5"
)

// Reading are the use cases of the reading of the books by «Свой ИИ» (specs/2026-11-13-desk-consent.md): the estimate, the
// consent of a saga and the sample of three chapters, each for the owner of the saga alone.
type Reading struct {
	Estimate interface {
		Execute(ctx context.Context, actorID, projectID string) (service.Estimate, error)
	}
	Consent interface {
		Execute(ctx context.Context, actorID, projectID string) (service.ConsentView, error)
	}
	SetConsent interface {
		Execute(ctx context.Context, actorID, projectID string, change domain.Change) (service.ConsentView, error)
	}
	// Unit is the unit of a sum in the settings now: a saga without a consent counts in it.
	Unit func() domain.Unit
	// Sample is the sample of three chapters (CN2).
	Sample interface {
		Start(ctx context.Context, actorID, projectID, key string) (service.Sample, error)
		Get(ctx context.Context, actorID, projectID string) (service.Sample, error)
		Stop(ctx context.Context, actorID, projectID string) error
	}
}

type tokensAmountJSON struct {
	InputTokens  int64  `json:"inputTokens"`
	OutputTokens int64  `json:"outputTokens"`
	Amount       *int64 `json:"amount"`
}

// estimateJSON is ReadingEstimate of the contract.
type estimateJSON struct {
	Chapters       int              `json:"chapters"`
	InputTokens    int64            `json:"inputTokens"`
	OutputTokens   int64            `json:"outputTokens"`
	Seconds        int              `json:"seconds"`
	Amount         *int64           `json:"amount"`
	Unit           string           `json:"unit"`
	LayerShare     float64          `json:"layerShare"`
	Host           string           `json:"host"`
	Local          bool             `json:"local"`
	Kind           string           `json:"kind"`
	Assumptions    []string         `json:"assumptions"`
	SuggestedStop  int64            `json:"suggestedStop"`
	SampleChapters int              `json:"sampleChapters"`
	ChapterEdit    tokensAmountJSON `json:"chapterEdit"`
	NamesWait      bool             `json:"namesWait"`
	ThreadsWait    bool             `json:"threadsWait"`
	// Account is the estimate of the helper of the account (specs/2026-11-13-desk-account-ai.md AI3.4); absent for «Свой ИИ».
	Account *accountEstimateJSON `json:"account,omitempty"`
}

// accountEstimateJSON is ReadingAccountEstimate of the contract.
type accountEstimateJSON struct {
	Characters int64 `json:"characters"`
	Work       int64 `json:"work"`
	// CharactersLimit and WorkLimit are null while the site has not told the limits of the tier.
	CharactersLimit *limitJSON `json:"charactersLimit"`
	WorkLimit       *limitJSON `json:"workLimit"`
	EndsFirst       *string    `json:"endsFirst"`
}

// limitJSON is ReadingLimit of the contract.
type limitJSON struct {
	Used  int64 `json:"used"`
	Limit int64 `json:"limit"`
}

// readingEstimate is getReadingEstimate (CN1.3): what reading the rest of the saga will take; the model is not called.
func (handlers Handlers) readingEstimate(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	actor, ok := handlers.author(w, r)
	if !ok {
		return
	}
	estimate, err := handlers.Reading.Estimate.Execute(r.Context(), actor, chi.URLParam(r, "projectId"))
	if err != nil {
		writeReadingError(w, r, err)
		return
	}
	answer := estimateJSON{
		Chapters: estimate.Chapters, InputTokens: estimate.Input, OutputTokens: estimate.Output, Seconds: estimate.Seconds,
		Amount: estimate.Amount, Unit: string(estimate.Unit), LayerShare: estimate.LayerShare, Host: estimate.Host, Local: estimate.Local,
		Kind: string(estimate.Kind), Assumptions: []string{}, SuggestedStop: estimate.SuggestedStop, SampleChapters: estimate.SampleChapters,
		ChapterEdit: tokensAmountJSON{InputTokens: estimate.ChapterEdit.Input, OutputTokens: estimate.ChapterEdit.Output, Amount: estimate.ChapterEdit.Amount},
		NamesWait:   estimate.NamesWait, ThreadsWait: estimate.ThreadsWait,
	}
	for _, assumption := range estimate.Assumptions {
		answer.Assumptions = append(answer.Assumptions, string(assumption))
	}
	if account := estimate.Account; account != nil {
		answer.Account = &accountEstimateJSON{Characters: account.Characters, Work: account.Work}
		if account.LimitsTold {
			answer.Account.CharactersLimit = &limitJSON{Used: account.CharactersLimit.Used, Limit: account.CharactersLimit.Limit}
			answer.Account.WorkLimit = &limitJSON{Used: account.WorkLimit.Used, Limit: account.WorkLimit.Limit}
		}
		if account.EndsFirst != "" {
			ends := account.EndsFirst
			answer.Account.EndsFirst = &ends
		}
	}
	writeJSON(w, http.StatusOK, answer)
}

// author is the local author of the session of the window; without it 401.
func (handlers Handlers) author(w http.ResponseWriter, r *http.Request) (string, bool) {
	user, err := handlers.Sessions.CurrentUser(r.Context(), auth.SessionCookieValue(r))
	if errors.Is(err, auth.ErrUnauthenticated) || errors.Is(err, auth.ErrSessionNotFound) {
		httpapi.WriteProblem(w, "", http.StatusUnauthorized, "Unauthorized", "authentication required", "auth.session_required")
		return "", false
	}
	if err != nil {
		unexpected(w, r, err)
		return "", false
	}
	return user.ID, true
}

// writeReadingError answers a refusal of the reading: a saga that is not the author's is not found.
func writeReadingError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, service.ErrNotFound):
		httpapi.WriteProblem(w, "", http.StatusNotFound, "Not Found", "the saga was not found", "not_found")
	case errors.Is(err, service.ErrNotConnected):
		httpapi.WriteProblem(w, "", http.StatusConflict, "Conflict", "the helper is not connected", codeNotConnected)
	default:
		var field *service.FieldError
		if errors.As(err, &field) {
			writeInvalid(w, "reading.invalid", "/"+strings.ReplaceAll(field.Field, ".", "/"))
			return
		}
		var conflict *service.Conflict
		if errors.As(err, &conflict) {
			httpapi.WriteProblem(w, "", http.StatusConflict, "Conflict", "the reading cannot do it now", conflict.Code)
			return
		}
		unexpected(w, r, err)
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

type spentJSON struct {
	InputTokens       int64 `json:"inputTokens"`
	CachedInputTokens int64 `json:"cachedInputTokens"`
	OutputTokens      int64 `json:"outputTokens"`
	Amount            int64 `json:"amount"`
	Approximate       bool  `json:"approximate"`
}

// consentJSON is ReadingConsent of the contract.
type consentJSON struct {
	State string `json:"state"`
	// Source is who reads the books: own or account; null without a helper.
	Source *string   `json:"source"`
	Reason *string   `json:"reason"`
	Host   *string   `json:"host"`
	Pause  *string   `json:"pause"`
	Unit   string    `json:"unit"`
	StopAt *int64    `json:"stopAt"`
	Step   *int64    `json:"step"`
	Since  *string   `json:"since"`
	Spent  spentJSON `json:"spent"`
}

func consentOf(view service.ConsentView, unit domain.Unit) consentJSON {
	answer := consentJSON{State: "none", Unit: string(unit)}
	if view.Source != "" {
		source := string(view.Source)
		answer.Source = &source
	}
	if view.Reason != "" {
		answer.Reason = &view.Reason
	}
	consent := view.Consent
	if consent == nil {
		return answer
	}
	host, since := consent.Host, consent.Since.UTC().Format(time.RFC3339Nano)
	answer.State, answer.Host, answer.Unit, answer.StopAt, answer.Step, answer.Since = string(consent.State), &host, string(consent.Unit), consent.StopAt, consent.Step, &since
	if consent.Pause != "" {
		pause := string(consent.Pause)
		answer.Pause = &pause
	}
	answer.Spent = spentJSON{
		InputTokens: consent.Spent.Tokens.Input, CachedInputTokens: consent.Spent.Tokens.Cached, OutputTokens: consent.Spent.Tokens.Output,
		Amount: consent.Count(), Approximate: consent.Spent.Estimated,
	}
	return answer
}

// readingConsent is getReadingConsent (CN3.2).
func (handlers Handlers) readingConsent(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	actor, ok := handlers.author(w, r)
	if !ok {
		return
	}
	view, err := handlers.Reading.Consent.Execute(r.Context(), actor, chi.URLParam(r, "projectId"))
	if err != nil {
		writeReadingError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, consentOf(view, handlers.Reading.Unit()))
}

// consentChangeJSON is ReadingConsentChange of the contract: stopAt left out keeps the sum, null takes it away.
type consentChangeJSON struct {
	State       string          `json:"state"`
	StopAt      json.RawMessage `json:"stopAt"`
	ContinueBy  *int64          `json:"continueBy"`
	RetryUnread bool            `json:"retryUnread"`
}

// setReadingConsent is setReadingConsent (CN3.2): the state and the sum of the consent of the saga.
func (handlers Handlers) setReadingConsent(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	actor, ok := handlers.author(w, r)
	if !ok {
		return
	}
	raw, err := io.ReadAll(httpapi.JSONBody(r))
	var request consentChangeJSON
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err != nil || decoder.Decode(&request) != nil || decoder.More() {
		httpapi.WriteProblem(w, "", http.StatusBadRequest, "Bad Request", "invalid request body", "validation.failed")
		return
	}
	change := domain.Change{State: domain.State(request.State), ContinueBy: request.ContinueBy, RetryUnread: request.RetryUnread}
	switch stop := strings.TrimSpace(string(request.StopAt)); {
	case stop == "":
	case stop == "null":
		change.NoStop = true
	default:
		var amount int64
		if json.Unmarshal(request.StopAt, &amount) != nil {
			writeInvalid(w, "reading.invalid", "/stopAt")
			return
		}
		change.StopAt = &amount
	}
	view, err := handlers.Reading.SetConsent.Execute(r.Context(), actor, chi.URLParam(r, "projectId"), change)
	if err != nil {
		writeReadingError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, consentOf(view, handlers.Reading.Unit()))
}

type sampleCardJSON struct {
	ChapterID string   `json:"chapterId"`
	Place     string   `json:"place"`
	Summary   string   `json:"summary"`
	Events    []string `json:"events"`
	People    []string `json:"people"`
}

type sampleSpentJSON struct {
	InputTokens  int64  `json:"inputTokens"`
	OutputTokens int64  `json:"outputTokens"`
	Amount       *int64 `json:"amount"`
	Unit         string `json:"unit"`
	Approximate  bool   `json:"approximate"`
}

// sampleJSON is ReadingSample of the contract.
type sampleJSON struct {
	State    string           `json:"state"`
	Failure  *string          `json:"failure"`
	Done     int              `json:"done"`
	Total    int              `json:"total"`
	Chapters []sampleCardJSON `json:"chapters"`
	Seconds  int              `json:"seconds"`
	Spent    sampleSpentJSON  `json:"spent"`
}

func sampleOf(sample service.Sample) sampleJSON {
	answer := sampleJSON{
		State: sample.State, Done: sample.Done, Total: sample.Total, Chapters: []sampleCardJSON{}, Seconds: sample.Seconds,
		Spent: sampleSpentJSON{
			InputTokens: sample.Tokens.Input, OutputTokens: sample.Tokens.Output, Amount: sample.Amount, Unit: string(sample.Unit),
			Approximate: sample.Approximate,
		},
	}
	if sample.Failure != "" {
		failure := sample.Failure
		answer.Failure = &failure
	}
	for _, card := range sample.Cards {
		shown := sampleCardJSON{ChapterID: card.ChapterID, Place: card.Place, Summary: card.Summary, Events: card.Events, People: card.People}
		if shown.Events == nil {
			shown.Events = []string{}
		}
		if shown.People == nil {
			shown.People = []string{}
		}
		answer.Chapters = append(answer.Chapters, shown)
	}
	return answer
}

// startReadingSample is startReadingSample (CN2.1): the sample of three chapters under the key of the request.
func (handlers Handlers) startReadingSample(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	actor, ok := handlers.author(w, r)
	if !ok {
		return
	}
	keys := r.Header.Values("Idempotency-Key")
	if len(keys) != 1 || strings.TrimSpace(keys[0]) == "" || len(keys[0]) > 200 {
		httpapi.WriteProblem(w, "", http.StatusBadRequest, "Bad Request", "one Idempotency-Key is required", "validation.failed")
		return
	}
	sample, err := handlers.Reading.Sample.Start(r.Context(), actor, chi.URLParam(r, "projectId"), keys[0])
	if err != nil {
		writeReadingError(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, sampleOf(sample))
}

// readingSample is getReadingSample (CN2.4).
func (handlers Handlers) readingSample(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	actor, ok := handlers.author(w, r)
	if !ok {
		return
	}
	sample, err := handlers.Reading.Sample.Get(r.Context(), actor, chi.URLParam(r, "projectId"))
	if err != nil {
		writeReadingError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, sampleOf(sample))
}

// cancelReadingSample is cancelReadingSample (CN2.4): the sample of the saga stops; its cards stay.
func (handlers Handlers) cancelReadingSample(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	actor, ok := handlers.author(w, r)
	if !ok {
		return
	}
	if err := handlers.Reading.Sample.Stop(r.Context(), actor, chi.URLParam(r, "projectId")); err != nil {
		writeReadingError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
