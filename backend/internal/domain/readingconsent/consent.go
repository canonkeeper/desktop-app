// SPDX-License-Identifier: AGPL-3.0-only

package readingconsent

import (
	"errors"
	"time"

	"canonkeeper/backend/internal/domain/aisettings"
)

// Source is whom the consent let the text of the books go to (CN3.1): «Свой ИИ» of the author, or the helper «Аккаунт
// CanonKeeper» — the site of his account (specs/2026-11-13-desk-account-ai.md AI3.4).
type Source string

const (
	// SourceOwn is «Свой ИИ»: the provider of the author, at the host the consent names.
	SourceOwn Source = "own"
	// SourceAccount is the helper of the account: the site at the host the consent names; no money and no stop sum — the
	// tier of the site bounds the reading.
	SourceAccount Source = "account"
)

// State is whether the reading goes.
type State string

const (
	StateAllowed State = "allowed"
	StatePaused  State = "paused"
)

// Pause is why the reading stands (CN3.4, CN3.8): the list is open for the reasons of the source «Аккаунт».
type Pause string

const (
	// PauseAuthor — the author asked for it.
	PauseAuthor Pause = "author"
	// PauseSum — the next request would have gone over the sum the author named.
	PauseSum Pause = "sum"
	// PauseFunds — the provider refused: no money on the account (402 or an exhausted quota).
	PauseFunds Pause = "funds"
	// PauseKey — the provider refused the key (401, 403).
	PauseKey Pause = "key"
	// PauseModel — the provider has no model of the name.
	PauseModel Pause = "model"
	// PauseConnection — the provider could not be reached after the repeats of a request.
	PauseConnection Pause = "connection"
	// PauseNoAnswer — the provider did not answer a request through the repeats of the client: a 5xx, a wait it asked for, a
	// stream torn mid-answer (specs/2026-11-15-desk-reading-no-answer.md NA1.1).
	PauseNoAnswer Pause = "no_answer"
	// PausePrices — the currency or the prices changed while the sum is in money: the author names the sum again.
	PausePrices Pause = "prices"
	// The reasons of the helper of the account (specs/2026-11-13-desk-account-ai.md AI3.5), by the code of the answer of the
	// site: the characters of the tier for 30 days are not enough (quota.account_characters); the fence of the work of the
	// reading for 30 days (ai.reading_fence); the site does not take this app (app.step_instruction, app.step_unknown); the
	// reading of the site is unavailable (ai.provider_failed, ai.unavailable). No connection and the author's pause are the
	// same as those of «Свой ИИ».
	PauseCharacters   Pause = "characters"
	PauseReadingFence Pause = "reading_fence"
	PauseUnfit        Pause = "unfit"
	PauseUnavailable  Pause = "unavailable"
)

// Valid says the reason is one of the list.
func (pause Pause) Valid() bool {
	switch pause {
	case PauseAuthor, PauseSum, PauseFunds, PauseKey, PauseModel, PauseConnection, PauseNoAnswer, PausePrices,
		PauseCharacters, PauseReadingFence, PauseUnfit, PauseUnavailable:
		return true
	default:
		return false
	}
}

// PauseOfAccount is the pause a refusal of the site of the account puts on the reading (AI3.5): the stop the gateway named
// for it. Any other stops nothing.
func PauseOfAccount(stop string) (Pause, bool) {
	switch pause := Pause(stop); pause {
	case PauseCharacters, PauseReadingFence, PauseUnfit, PauseUnavailable:
		return pause, true
	default:
		return "", false
	}
}

// Spent is the count of a consent (CN3.1): the tokens in, cached and out, the money in millionths of the currency of the
// consent, and whether an estimate is in it — a request the provider sent no usage for.
type Spent struct {
	Tokens    aisettings.Tokens
	Micros    int64
	Estimated bool
}

// Add counts one request: its tokens, and its cost when it is in the currency of the consent.
func (spent Spent) Add(tokens aisettings.Tokens, cost *Cost, unit Unit, estimated bool) Spent {
	spent.Tokens = spent.Tokens.Add(tokens)
	if cost != nil && Unit(cost.Currency) == unit {
		spent.Micros += cost.Micros
	}
	spent.Estimated = spent.Estimated || estimated
	return spent
}

// Cost is what a request cost in a currency.
type Cost struct {
	Micros   int64
	Currency aisettings.Currency
}

// Consent is the consent of the author to the reading of the books of a saga (CN3.1): for which source and which host,
// allowed or paused with its reason, the stop sum in its unit — none is no stop — and the step «ещё на X», the start of
// the count, the last continuation and the count.
type Consent struct {
	Source    Source
	Host      string
	State     State
	Pause     Pause
	Unit      Unit
	StopAt    *int64
	Step      *int64
	Since     time.Time
	ResumedAt time.Time
	Spent     Spent
}

// Acts says the consent holds for the source and the host of the settings now (CN3.1): a consent given for another host —
// or one written before the consents had a source — waits for a new one, and no request of the reading goes to the new host.
func (consent *Consent) Acts(source Source, host string) bool {
	return consent != nil && consent.Source != "" && consent.Source == source && consent.Host == host
}

// Count is what was spent in the unit of the consent: tokens in and out, or millionths of its currency.
func (consent Consent) Count() int64 {
	if consent.Unit == UnitTokens {
		return consent.Spent.Tokens.Sum()
	}
	return consent.Spent.Micros
}

// In is the consent counted in the unit of the settings now (CN3.1, CN3.4): a consent without a sum follows the unit of the
// settings — its count of money starts again in a new currency or goes without prices, the tokens kept; a sum stays in its
// unit (PricesChanged says when one in money waits for a sum named again).
func (consent Consent) In(unit Unit) Consent {
	if consent.StopAt == nil && consent.Unit != unit {
		consent.Unit, consent.Spent.Micros = unit, 0
	}
	return consent
}

// PricesChanged says the sum of the consent is in money the settings no longer price in its currency — the currency
// changed or the prices went (CN3.4): the reading waits for a sum named again. A sum in tokens holds whatever the prices.
func (consent Consent) PricesChanged(unit Unit) bool {
	return consent.StopAt != nil && consent.Unit != UnitTokens && consent.Unit != unit
}

// StopsInMoney says the sum of the consent is in money of the currency the settings price in now (unit): a change of the
// currency or the removal of the prices would pause it at «цены изменились» (CN3.4; specs/2026-11-13-desk-consent-web.md
// RW3.1). A sum in tokens holds; a sum that already waits for a new one is not paused again.
func (consent Consent) StopsInMoney(unit Unit) bool {
	return consent.StopAt != nil && consent.Unit != UnitTokens && consent.Unit == unit
}

// Change is what setReadingConsent asks (CN3.2): the state, and at most one of a new stop sum, no stop at all, or «ещё на
// X» over what was spent; and whether the chapters that did not read are read again.
type Change struct {
	State       State
	StopAt      *int64
	NoStop      bool
	ContinueBy  *int64
	RetryUnread bool
}

var (
	// ErrNothingToPause is a pause of a saga without a consent that acts.
	ErrNothingToPause = errors.New("reading consent: no consent to pause")
	// ErrInvalidChange is a change that is not one: an unknown state, or two sums at once.
	ErrInvalidChange = errors.New("reading consent: the change is not valid")
)

// FieldError names the field of a change that is not valid by its name in the contract.
type FieldError struct {
	Field string
}

func (err *FieldError) Error() string {
	return "reading consent: the field " + err.Field + " is not valid"
}

// Set is the transition of a change (CN3.2). A new count — its start now, nothing spent — begins only when there was no
// consent that acts: a pause, a continuation, a new sum and a restart keep the count. «Ещё на X» is a sum of what was spent
// and X in the unit of the consent; a sum that is not above what was spent is refused by its field. A sum named is in the
// unit of the settings now. A consent without a sum follows that unit (In); a sum in tokens holds whatever the prices; a sum
// in money whose currency changed or whose prices went goes on only with a sum named again — «ещё на X» would read X in the
// old currency — and its count of money starts again in the new unit, the tokens kept.
func Set(current *Consent, change Change, now time.Time, source Source, host string, unit Unit) (Consent, error) {
	sums := 0
	for _, given := range []bool{change.StopAt != nil, change.NoStop, change.ContinueBy != nil} {
		if given {
			sums++
		}
	}
	if sums > 1 || change.State != StateAllowed && change.State != StatePaused {
		return Consent{}, ErrInvalidChange
	}
	if change.StopAt != nil && *change.StopAt <= 0 {
		return Consent{}, &FieldError{Field: "stopAt"}
	}
	if change.ContinueBy != nil && *change.ContinueBy <= 0 {
		return Consent{}, &FieldError{Field: "continueBy"}
	}
	var next Consent
	if current.Acts(source, host) {
		next = *current
	} else {
		if change.State == StatePaused {
			return Consent{}, ErrNothingToPause
		}
		next = Consent{Source: source, Host: host, Unit: unit, Since: now, ResumedAt: now}
	}
	next = next.In(unit)
	if next.PricesChanged(unit) {
		switch {
		case change.StopAt != nil || change.NoStop:
			// The count of money starts again in the new unit; the tokens are counted whatever the unit.
			next.Unit, next.Spent.Micros = unit, 0
		case change.ContinueBy != nil:
			return Consent{}, &FieldError{Field: "continueBy"}
		case change.State == StateAllowed:
			return Consent{}, &FieldError{Field: "stopAt"}
		}
	} else if change.StopAt != nil && next.Unit != unit {
		// A sum in tokens named again in money: the count of money starts in the unit of the settings.
		next.Unit, next.Spent.Micros = unit, 0
	}
	switch {
	case change.StopAt != nil:
		next.StopAt, next.Step = change.StopAt, change.StopAt
	case change.ContinueBy != nil:
		stop, step := next.Count()+*change.ContinueBy, *change.ContinueBy
		next.StopAt, next.Step = &stop, &step
	case change.NoStop:
		next.StopAt, next.Step = nil, nil
	}
	if next.StopAt != nil && *next.StopAt <= next.Count() && (change.StopAt != nil || change.State == StateAllowed) {
		return Consent{}, &FieldError{Field: "stopAt"}
	}
	if change.State == StatePaused {
		next.State, next.Pause = StatePaused, PauseAuthor
		return next, nil
	}
	if next.State != StateAllowed {
		next.ResumedAt = now
	}
	next.State, next.Pause = StateAllowed, ""
	return next, nil
}

// Decision is what the check of a request of the reading says (CN3.3).
type Decision int

const (
	// Go: the request goes to the provider with its reserve.
	Go Decision = iota
	// Refuse: no consent acts or it is paused; nothing changes.
	Refuse
	// Halt: the request would go over the sum, or the prices changed under a sum in money: the consent is paused with the
	// reason of Admit, in the same transaction.
	Halt
)

// Admit checks a request of the reading of a saga at the gateway, after its place and before the provider (CN3.3): the
// consent acts for the source and the host of the settings now and is allowed; a sum in money has the prices of its currency
// now; what was spent with the reserve of the requests of the saga that go and the reserve of this one is within the sum.
// Each reserve is in the unit of the consent.
func Admit(consent *Consent, source Source, host string, unit Unit, going, reserve int64) (Decision, Pause) {
	if !consent.Acts(source, host) || consent.State != StateAllowed {
		return Refuse, ""
	}
	if consent.StopAt == nil {
		return Go, ""
	}
	if consent.PricesChanged(unit) {
		return Halt, PausePrices
	}
	if consent.Count()+going+reserve > *consent.StopAt {
		return Halt, PauseSum
	}
	return Go, ""
}

// Reserve is what a request is counted at before its answer, in the unit of a consent (CN3.3): the input by the size of the
// request and the calibration, the output the limit of an answer of the role; its cost by the prices now.
func Reserve(requestBytes int64, maxOutput int, calibration float64, prices *aisettings.Prices, unit Unit) int64 {
	tokens := aisettings.Unseen(requestBytes, 0, calibration)
	tokens.Output = int64(max(maxOutput, 0))
	if unit == UnitTokens || prices == nil {
		return tokens.Sum()
	}
	return prices.Cost(tokens)
}

// PauseOf is the pause a refusal of the provider puts on the reading (CN3.4): the funds, the key and the model at once; no
// connection, and a wait or a failure of the provider — no answer (specs/2026-11-15-desk-reading-no-answer.md NA1.1) — only
// when the repeats of the request are over; a refusal of the request stops nothing: it marks its chapter (NA2.1).
func PauseOf(failure aisettings.Failure, last bool) (Pause, bool) {
	switch failure {
	case aisettings.FailureFunds:
		return PauseFunds, true
	case aisettings.FailureKey:
		return PauseKey, true
	case aisettings.FailureModel:
		return PauseModel, true
	case aisettings.FailureUnreachable:
		return PauseConnection, last
	case aisettings.FailureRate, aisettings.FailureOther:
		return PauseNoAnswer, last
	default:
		return "", false
	}
}
