package quota

import (
	"errors"
	"time"
)

// WorkKind is the work of the writer a site counts (specs/2026-11-13-desk-account-ai.md AI2.1, AI3.8).
type WorkKind string

const (
	// WorkTurn is a turn of the chat: a question of the day and a place of «разом».
	WorkTurn WorkKind = "chat_turn"
	// WorkMaterials is an upload of the author's materials read by the model: an upload of the day.
	WorkMaterials WorkKind = "materials"
)

// Work is a turn or an upload of materials as the transaction of its start names it to the counter of a site: its Job — the
// counter derives its ref from it —, its writer, the chat of a turn and the moment.
type Work struct {
	Kind   WorkKind
	JobID  string
	UserID string
	ChatID string
	At     time.Time
}

// Steer is a correction of a running turn as the counter of a site sees it: the Job of the turn and the key of the repeat
// of the correction (AI2.8).
type Steer struct {
	JobID string
	// Key is the key of the repeat of the correction — its scope, operation and key — the same for every sending of it,
	// whether the transaction of an earlier one committed or not (specs/2026-11-13-desk-account-ai.md AI2.2).
	Key string
	At  time.Time
}

// ErrCounterUnlinked is work a counter cannot take any more: the app is not linked with the account of its source (AI2.1).
var ErrCounterUnlinked = errors.New("quota: the counter of the work is not linked")

// CounterUnreachable is a counter that did not answer twice (AI2.1): the work did not start. MayReturn says the site may
// have counted the unit before its answer was lost — it gives it back itself once the lease of the permit ends.
type CounterUnreachable struct {
	MayReturn bool
}

func (unreachable *CounterUnreachable) Error() string {
	return "quota: the counter of the work did not answer"
}

// CounterUnreachableCode is the code of the refusal of a counter that did not answer.
const CounterUnreachableCode = "ai.account_unreachable"

// AsCounterUnreachable is the unreachable counter an error carries.
func AsCounterUnreachable(err error) (*CounterUnreachable, bool) {
	var unreachable *CounterUnreachable
	if errors.As(err, &unreachable) {
		return unreachable, true
	}
	return nil, false
}

// KeyOfCode is the key of a code of a refusal of a limit, as the site sends it (quota.chat_turns_daily → chat_turns_per_day).
func KeyOfCode(code string) (Key, bool) {
	for _, key := range append(Keys(), KeyActiveTurns) {
		if Code(key) == code {
			return key, true
		}
	}
	return "", false
}

// CounterRefusal is a refusal a counter remembers and gives at once, before any permit (AI2.6): the tokens of the day of the
// work of the app are over until the Moscow midnight, the site does not take this version of the app until its restart,
// or the thread of a chat is too large for the site.
type CounterRefusal struct {
	Code string
}

func (refusal *CounterRefusal) Error() string {
	return "quota: the counter refused the work: " + refusal.Code
}

// CounterDayFenceCode is the refusal of the fence of the day of the work of the app the counter remembers until the Moscow
// midnight (AI2.6): the code of the site.
const CounterDayFenceCode = "ai.daily_fence"

// CounterUnfitCode is the code of the work of a kind the site does not take from this version of the app (AI2.6).
const CounterUnfitCode = "ai.app_unfit"

// AsCounterRefusal is the refusal of a counter an error carries.
func AsCounterRefusal(err error) (*CounterRefusal, bool) {
	var refusal *CounterRefusal
	if errors.As(err, &refusal) {
		return refusal, true
	}
	return nil, false
}
