// SPDX-License-Identifier: AGPL-3.0-only

package aisettings

import (
	"net/http"
	"slices"
	"strings"
)

// Reason is why the provider or the model failed the author (specs/2026-11-13-desk-probes.md PB2.4): the closed list the
// window and the consent read, each with its words and advice on the page. No text of the provider is in it.
type Reason string

const (
	// ReasonUnreachable — «провайдер не ответил»: no connection, no answer.
	ReasonUnreachable Reason = "unreachable"
	// ReasonKeyRefused — «ключ не подошёл».
	ReasonKeyRefused Reason = "key_refused"
	// ReasonNoModel — «модели с таким именем нет».
	ReasonNoModel Reason = "no_model"
	// ReasonNoTools — «модель не вызывает инструменты».
	ReasonNoTools Reason = "no_tools"
	// ReasonStartOnly — «модель видит только начало длинного текста».
	ReasonStartOnly Reason = "start_only"
	// ReasonCutAnswer — «модель обрывает длинный ответ».
	ReasonCutAnswer Reason = "cut_answer"
	// ReasonRateLimited — «провайдер просит подождать»: his limit of requests.
	ReasonRateLimited Reason = "rate_limited"
	// ReasonOther — anything else the provider did.
	ReasonOther Reason = "other"
)

// Failure is how a request to the provider failed, as the gateway saw it, before anyone asks what it means for a probe.
type Failure string

const (
	FailureNone Failure = ""
	// FailureUnreachable: the provider could not be reached or did not answer in time.
	FailureUnreachable Failure = "unreachable"
	// FailureKey: the provider refused the key (401, 403).
	FailureKey Failure = "key"
	// FailureModel: the provider has no model of the name.
	FailureModel Failure = "model"
	// FailureRate: the provider asks to wait (429).
	FailureRate Failure = "rate"
	// FailureFunds: the provider refused for want of money on the account of the author — 402, or an exhausted quota
	// (specs/2026-11-13-desk-consent.md CN3.4).
	FailureFunds Failure = "funds"
	// FailureRefused: the provider refused the request itself (another 4xx).
	FailureRefused Failure = "refused"
	// FailureOther: the provider failed (5xx), broke the stream or answered what no client reads.
	FailureOther Failure = "other"
)

// Reason is what a failure of a request tells the author whatever was asked; a probe may tell more (Judge).
func (failure Failure) Reason() Reason {
	switch failure {
	case FailureUnreachable:
		return ReasonUnreachable
	case FailureKey:
		return ReasonKeyRefused
	case FailureModel:
		return ReasonNoModel
	case FailureRate:
		return ReasonRateLimited
	default:
		return ReasonOther
	}
}

// ModelsOutcome is what the provider said when asked for its models.
type ModelsOutcome string

const (
	// ModelsListed: the provider gave its models.
	ModelsListed ModelsOutcome = "listed"
	// ModelsUnlisted: the provider gives no list; the author writes the name of the model himself.
	ModelsUnlisted ModelsOutcome = "unlisted"
	// ModelsFailed: the provider could not be asked; Reason says why.
	ModelsFailed ModelsOutcome = "failed"
)

// MaxModels is the most names a list keeps (PB1.1).
const MaxModels = 500

// Models is the list of the models of the provider of the author (PB1.1): the names, that it gives none, or why it
// failed. It is asked for every time and kept nowhere.
type Models struct {
	Outcome ModelsOutcome
	Names   []string
	Reason  Reason
}

// ListedModels keeps of the names a provider gave those the author could choose — names of a model by the rule of a
// write — each once, by the alphabet, at most MaxModels.
func ListedModels(names []string) Models {
	kept := make([]string, 0, len(names))
	for _, name := range names {
		if valid, ok := model(&name); ok && valid != "" {
			kept = append(kept, valid)
		}
	}
	slices.SortFunc(kept, func(a, b string) int {
		if order := strings.Compare(strings.ToLower(a), strings.ToLower(b)); order != 0 {
			return order
		}
		return strings.Compare(a, b)
	})
	kept = slices.Compact(kept)
	if len(kept) > MaxModels {
		kept = kept[:MaxModels]
	}
	return Models{Outcome: ModelsListed, Names: kept}
}

// codeInsufficientQuota is the code the module of the protocols gives every spelling of an exhausted quota.
const codeInsufficientQuota = "insufficient_quota"

// Waits says the provider asks to wait («провайдер просит подождать», PB2.4): a 429 that is no exhausted quota, which
// waiting does not help. A model request has its code from the module of the protocols, which names every spelling of an
// exhausted quota insufficient_quota; the list of the models has the code of the provider's refusal.
func Waits(status int, code string) bool {
	return status == http.StatusTooManyRequests && code != codeInsufficientQuota
}

// Classify is the kind of a refusal of the provider by its status, its code and, to tell a model it does not have from
// another refusal, its words (PB2.4; specs/2026-11-13-desk-consent.md CN3.4) — the one table of the probes and of the
// reading of the books: no connection — unreachable; 401 and 403 — the key; 402 and an exhausted quota — the funds; a 429
// to wait — rate; no such model — the model; a refusal of a state that passes — other; another 4xx — refused, a refusal the
// same request repeated gets again (specs/2026-11-15-desk-reading-no-answer.md NA2.1); the rest — other.
func Classify(status int, code, message string, connection bool) Failure {
	switch {
	case connection:
		return FailureUnreachable
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return FailureKey
	case status == http.StatusPaymentRequired || code == codeInsufficientQuota:
		return FailureFunds
	case Waits(status, code):
		return FailureRate
	case status == http.StatusNotFound && namesModel(code, message, false), status >= 400 && status < 500 && namesModel(code, message, true):
		return FailureModel
	case status == http.StatusNotFound || status == http.StatusMethodNotAllowed || status == http.StatusTooManyRequests:
		return FailureOther
	case Passes(status):
		return FailureOther
	case status >= 400 && status < 500:
		return FailureRefused
	default:
		return FailureOther
	}
}

// Passes says a refusal is of a state that passes, which the same request repeated may not get again: the request timed out
// (408), met a conflict (409) or a lock (423), or came too early (425) (specs/2026-11-15-desk-reading-no-answer.md NA2.1). It
// is the one rule of what a repeat cures for both helpers: the helper of the account takes such a refusal of the provider of
// its site as the failure of the reading there, not as a refusal of the request (specs/2026-11-15-desk-reading-queue.md
// RQ3.1). The client of the app repeats the same statuses (modelapi; its test holds it to this rule).
func Passes(status int) bool {
	switch status {
	case http.StatusRequestTimeout, http.StatusConflict, http.StatusLocked, http.StatusTooEarly:
		return true
	default:
		return false
	}
}

// MidAnswer is the kind of a failure that came once the provider began its answer
// (specs/2026-11-15-desk-reading-no-answer.md NA1.1): the provider was reached and took the request, so a stream torn by the
// connection mid-answer is no answer, not «нет связи», and a refusal said mid-answer is no answer either — the client has its
// answer begun and cannot be told the refusal; the funds, the key, the model and a wait stay what they are.
func (failure Failure) MidAnswer() Failure {
	switch failure {
	case FailureUnreachable, FailureRefused:
		return FailureOther
	default:
		return failure
	}
}

// namesModel says the refusal is about the model: a 404 that names a model, or another refusal that says it has none.
func namesModel(code, message string, missing bool) bool {
	words := strings.ToLower(code + " " + message)
	if !strings.Contains(words, "model") {
		return false
	}
	if !missing {
		return true
	}
	for _, phrase := range []string{"not found", "not_found", "not exist", "does not exist", "unknown", "no such", "invalid model", "invalid_model"} {
		if strings.Contains(words, phrase) {
			return true
		}
	}
	return false
}
