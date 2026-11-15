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

	"canonkeeper/backend/internal/auth"
	domain "canonkeeper/backend/internal/domain/aisettings"
	desktopdomain "canonkeeper/backend/internal/domain/desktop"
	"canonkeeper/backend/internal/httpapi"
	"canonkeeper/backend/internal/observability"
	service "canonkeeper/backend/internal/services/aisettings"
)

// AI are the use cases of «Свой ИИ» (specs/2026-11-13-desk-secrets.md SE2): the window reads and replaces the settings,
// hands over the key and has it removed.
type AI struct {
	Read interface {
		Execute(context.Context) (service.State, error)
	}
	Update interface {
		Execute(context.Context, domain.Draft) (service.State, error)
	}
	SetKey interface {
		Execute(context.Context, string) error
	}
	DeleteKey interface{ Execute(context.Context) error }
	// Models lists the models of the provider (specs/2026-11-13-desk-probes.md PB1); Check is «Проверить связь» (PB2).
	Models interface {
		Execute(context.Context) (domain.Models, error)
	}
	Check Checks
}

type advancedJSON struct {
	ContextWindow *int         `json:"contextWindow"`
	MaxOutput     *int         `json:"maxOutput"`
	Reasoning     *string      `json:"reasoning"`
	Parallel      *int         `json:"parallel"`
	PriceIn       *json.Number `json:"priceInPerMillion"`
	PriceOut      *json.Number `json:"priceOutPerMillion"`
	// Currency and PriceCache left out of a write keep what the settings had (specs/2026-11-13-desk-consent.md CN1.1);
	// null clears the price of the cache.
	Currency   *string    `json:"currency,omitempty"`
	PriceCache cachePrice `json:"priceCachedInPerMillion"`
}

// cachePrice is priceCachedInPerMillion: in a write, left out keeps the price, null clears it; an answer says the price or
// null.
type cachePrice struct {
	given bool
	price *json.Number
}

func (cache *cachePrice) UnmarshalJSON(raw []byte) error {
	cache.given = true
	if string(raw) == "null" {
		cache.price = nil
		return nil
	}
	var number json.Number
	if err := json.Unmarshal(raw, &number); err != nil {
		return err
	}
	cache.price = &number
	return nil
}

func (cache cachePrice) MarshalJSON() ([]byte, error) {
	if cache.price == nil {
		return []byte("null"), nil
	}
	return json.Marshal(cache.price)
}

type ownJSON struct {
	Preset     string       `json:"preset"`
	BaseURL    *string      `json:"baseUrl,omitempty"`
	Protocol   *string      `json:"protocol,omitempty"`
	ChatModel  *string      `json:"chatModel,omitempty"`
	CardsModel *string      `json:"cardsModel,omitempty"`
	Advanced   advancedJSON `json:"advanced"`
}

// settingsJSON is AiSettings of the contract, and the body of updateAiSettings, whose fields the author may leave out.
type settingsJSON struct {
	Source string   `json:"source"`
	Own    *ownJSON `json:"own"`
}

type stateJSON struct {
	Settings           settingsJSON `json:"settings"`
	KeySaved           bool         `json:"keySaved"`
	SecretsStore       string       `json:"secretsStore"`
	AppliesAfterAnswer bool         `json:"appliesAfterAnswer"`
	Check              *reportJSON  `json:"check"`
	// MoneyStops — the sagas a change of the currency or the removal of the prices would pause
	// (specs/2026-11-13-desk-consent-web.md RW3.1).
	MoneyStops int `json:"moneyStopSagas"`
	// Account is the helper of the account as the site told it last, for the page (specs/2026-11-13-desk-account-ai.md
	// AI1.1); none until the site told it.
	Account *accountHelperJSON `json:"account"`
}

// accountHelperJSON is AiAccountHelper of the contract.
type accountHelperJSON struct {
	ChatModel     string `json:"chatModel"`
	CardsModel    string `json:"cardsModel"`
	ContextWindow int    `json:"contextWindow"`
	CompactLimit  int    `json:"compactLimit"`
}

type keyJSON struct {
	Key string `json:"key"`
}

type validationIssue struct {
	Path    string `json:"path"`
	Message string `json:"message"`
	Code    string `json:"code"`
}

type validationProblem struct {
	httpapi.Problem
	Errors []validationIssue `json:"errors"`
}

// The codes of the refusals of «Свой ИИ».
const (
	codeSettingsInvalid = "ai.settings_invalid"
	codeKeyInvalid      = "ai.key_invalid"
	codeKeyNotSaved     = "ai.key_not_saved"
	// codeAccountUnlinked and codeAccountUnreachable refuse the choice of the helper of the account by an app that is not
	// linked or whose site did not answer (specs/2026-11-13-desk-account-ai.md AI1.1).
	codeAccountUnlinked    = "helper.account_unlinked"
	codeAccountUnreachable = "ai.account_unreachable"
)

// aiSettings is getAiSettings (SE2.4): the settings, whether the key is held and where the shell keeps the secrets — the
// key is not in the answer.
func (handlers Handlers) aiSettings(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !handlers.signedIn(w, r) {
		return
	}
	state, err := handlers.AI.Read.Execute(r.Context())
	if err != nil {
		unexpected(w, r, err)
		return
	}
	writeState(w, state)
}

// updateAiSettings replaces the settings whole (SE2.3); a field that is not valid is refused by its JSON pointer and
// changes nothing.
func (handlers Handlers) updateAiSettings(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !handlers.signedIn(w, r) {
		return
	}
	var request settingsJSON
	if field, ok := decode(r, &request); !ok {
		refuse(w, codeSettingsInvalid, field)
		return
	}
	state, err := handlers.AI.Update.Execute(r.Context(), draftOf(request))
	var invalid *domain.FieldError
	if errors.As(err, &invalid) {
		writeInvalid(w, codeSettingsInvalid, "/"+strings.ReplaceAll(invalid.Field, ".", "/"))
		return
	}
	switch {
	case errors.Is(err, service.ErrAccountUnlinked):
		httpapi.WriteProblem(w, "", http.StatusConflict, "Conflict", "the app is not linked with an account", codeAccountUnlinked)
		return
	case errors.Is(err, service.ErrAccountUnreachable):
		httpapi.WriteProblem(w, "", http.StatusServiceUnavailable, "Service Unavailable", "the site did not answer", codeAccountUnreachable)
		return
	case err != nil:
		unexpected(w, r, err)
		return
	}
	writeState(w, state)
}

// setAiKey hands the key to the shell and answers once the shell kept it (SE2.5); the key is in no answer, log or error.
func (handlers Handlers) setAiKey(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !handlers.signedIn(w, r) {
		return
	}
	var request keyJSON
	if field, ok := decode(r, &request); !ok {
		refuse(w, codeKeyInvalid, field)
		return
	}
	handlers.keyAnswer(w, r, handlers.AI.SetKey.Execute(r.Context(), request.Key))
}

// deleteAiKey has the shell remove the key.
func (handlers Handlers) deleteAiKey(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !handlers.signedIn(w, r) {
		return
	}
	handlers.keyAnswer(w, r, handlers.AI.DeleteKey.Execute(r.Context()))
}

func (handlers Handlers) keyAnswer(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case err == nil:
		w.WriteHeader(http.StatusNoContent)
	case errors.Is(err, domain.ErrKeyInvalid):
		writeInvalid(w, codeKeyInvalid, "/key")
	case errors.Is(err, desktopdomain.ErrSecretNotKept):
		httpapi.WriteProblem(w, "", http.StatusServiceUnavailable, "Service Unavailable", "the key was not saved", codeKeyNotSaved)
	default:
		unexpected(w, r, err)
	}
}

// signedIn answers 401 without the session of the window.
func (handlers Handlers) signedIn(w http.ResponseWriter, r *http.Request) bool {
	_, err := handlers.Sessions.CurrentUser(r.Context(), auth.SessionCookieValue(r))
	if errors.Is(err, auth.ErrUnauthenticated) {
		httpapi.WriteProblem(w, "", http.StatusUnauthorized, "Unauthorized", "authentication required", "auth.session_required")
		return false
	}
	if err != nil {
		unexpected(w, r, err)
		return false
	}
	return true
}

// decode reads one JSON object without unknown fields; a value of a wrong type names its field.
func decode(r *http.Request, target any) (string, bool) {
	raw, err := io.ReadAll(httpapi.JSONBody(r))
	if err != nil {
		return "", false
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	err = decoder.Decode(target)
	var wrongType *json.UnmarshalTypeError
	if errors.As(err, &wrongType) && wrongType.Field != "" {
		return "/" + strings.ReplaceAll(wrongType.Field, ".", "/"), false
	}
	if err != nil || decoder.More() {
		return "", false
	}
	return "", true
}

// refuse is a body that is not the object of the operation: by its field and the code of the operation when a value had
// a wrong type. Nothing of the body is in the answer.
func refuse(w http.ResponseWriter, code, field string) {
	if field != "" {
		writeInvalid(w, code, field)
		return
	}
	httpapi.WriteProblem(w, "", http.StatusBadRequest, "Bad Request", "invalid request body", "validation.failed")
}

func writeInvalid(w http.ResponseWriter, code, path string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(validationProblem{
		Problem: httpapi.Problem{
			Type: "about:blank", Title: "Bad Request", Status: http.StatusBadRequest, Detail: "a field is not valid", Code: code,
			RequestID: w.Header().Get(observability.RequestIDHeader),
		},
		Errors: []validationIssue{{Path: path, Message: "not valid", Code: code}},
	})
}

func draftOf(request settingsJSON) domain.Draft {
	draft := domain.Draft{Source: request.Source}
	if own := request.Own; own != nil {
		draft.Own = &domain.OwnDraft{
			Preset: own.Preset, BaseURL: own.BaseURL, Protocol: own.Protocol, ChatModel: own.ChatModel, CardsModel: own.CardsModel,
			Advanced: domain.AdvancedDraft{
				ContextWindow: own.Advanced.ContextWindow, MaxOutput: own.Advanced.MaxOutput, Reasoning: own.Advanced.Reasoning,
				Parallel: own.Advanced.Parallel, PriceIn: (*string)(own.Advanced.PriceIn), PriceOut: (*string)(own.Advanced.PriceOut),
				Currency: own.Advanced.Currency, PriceCache: (*string)(own.Advanced.PriceCache.price),
				ClearPriceCache: own.Advanced.PriceCache.given && own.Advanced.PriceCache.price == nil,
			},
		}
	}
	return draft
}

func writeState(w http.ResponseWriter, state service.State) {
	response := stateJSON{
		Settings: settingsJSON{Source: string(state.Settings.Source)}, KeySaved: state.KeySaved, SecretsStore: string(state.SecretsStore),
		AppliesAfterAnswer: state.AppliesAfterAnswer, Check: reportOf(state.Check), MoneyStops: state.MoneyStops,
	}
	if account := state.Settings.Account; account != nil {
		response.Account = &accountHelperJSON{
			ChatModel: account.ChatModel, CardsModel: account.CardsModel, ContextWindow: account.ContextWindow, CompactLimit: account.CompactLimit,
		}
	}
	if own := state.Settings.Own; own != nil {
		protocol, reasoning, currency := string(own.Protocol), string(own.Advanced.Reasoning), string(own.Advanced.Currency)
		response.Settings.Own = &ownJSON{
			Preset: string(own.Preset), BaseURL: &own.BaseURL, Protocol: &protocol, ChatModel: &own.ChatModel, CardsModel: &own.CardsModel,
			Advanced: advancedJSON{
				ContextWindow: &own.Advanced.ContextWindow, MaxOutput: &own.Advanced.MaxOutput, Reasoning: &reasoning,
				Parallel: &own.Advanced.Parallel, PriceIn: priceJSON(own.Advanced.PriceIn), PriceOut: priceJSON(own.Advanced.PriceOut),
				Currency: &currency, PriceCache: cachePrice{price: priceJSON(own.Advanced.PriceCache)},
			},
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(response)
}

func priceJSON(price *domain.Price) *json.Number {
	if price == nil {
		return nil
	}
	number := json.Number(price.String())
	return &number
}
