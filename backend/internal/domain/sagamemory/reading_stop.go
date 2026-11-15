package sagamemory

import "errors"

// Stop is why a call of the model of the reading of the books was refused for good (specs/2026-11-13-desk-consent.md CN3.4):
// the provider has no money of the author, takes no key or has no such model, the provider could not be reached after the
// repeats of a request, or the reading of the saga is paused. The client of the app hands it typed, and the builders of the
// cards and of the layer stop handing out their work. The list is open for the reasons of another source of the AI.
type Stop string

const (
	StopFunds      Stop = "funds"
	StopKey        Stop = "key"
	StopModel      Stop = "model"
	StopConnection Stop = "connection"
	// StopNoAnswer: the provider did not answer a request through the repeats of the client of the app — a wait, a 5xx, a
	// stream torn mid-answer — and the gateway paused the reading on its last attempt
	// (specs/2026-11-15-desk-reading-no-answer.md NA1.1).
	StopNoAnswer Stop = "no_answer"
	// StopPaused: the gateway did not let the request go — the reading of the saga is paused, the sum is spent, no consent
	// acts — or the sample of its Job is over.
	StopPaused Stop = "paused"
	// The stops of the helper of the account (specs/2026-11-13-desk-account-ai.md AI3.5), by the code of its site: the
	// characters of the tier are not enough, the work of the reading of the tier is over, the site does not take this app,
	// the reading of the site is unavailable.
	StopCharacters   Stop = "characters"
	StopReadingFence Stop = "reading_fence"
	StopUnfit        Stop = "unfit"
	StopUnavailable  Stop = "unavailable"
)

// Typed says the stop is one a client hands typed and never repeats: every stop but no connection, which stops only after
// the repeats of a request.
func (stop Stop) Typed() bool {
	switch stop {
	case StopFunds, StopKey, StopModel, StopCharacters, StopReadingFence, StopUnfit, StopUnavailable:
		return true
	default:
		return false
	}
}

// ErrAnswerCut is an answer of the model cut short (response.incomplete — its limit or another reason of the provider): the
// answer was paid and gave no card. The client of the model hands it typed; a chapter whose attempts end with it does not
// read (specs/2026-11-13-desk-consent.md CN3.6). A request that failed — refused or torn — is not one.
var ErrAnswerCut = errors.New("saga memory: the answer of the model was cut short")

// ErrRefused is a request of the reading refused for good by whom the reading goes to — the provider of «Свой ИИ» did not take
// it (the window of the model, a request too large) or the site of the account refused it without a stop
// (specs/2026-11-15-desk-reading-rest.md RR1.1): at the same settings it is refused again. The client of the app hands it
// typed only when its gateway named it so; it is no stop of the reading.
var ErrRefused = errors.New("saga memory: the request of the reading was refused")

// ErrNoAnswer is a request of the reading nobody answered through the last attempt of the client of the app — a 429 or a 5xx,
// a stream torn mid-answer, a gateway that could not ask the provider (RR1.1).
var ErrNoAnswer = errors.New("saga memory: the request of the reading got no answer")

// The reasons a step of the reading did not come together once the model was asked (RR1.1): the answer was cut short, it
// did not read, the request was refused, nobody answered it.
const (
	UnreadCutAnswer  = "cut_answer"
	UnreadUnreadable = "unreadable"
	UnreadRefused    = "refused"
	UnreadNoAnswer   = "no_answer"
)

// UnreadOf is the reason a failed call of the model names (RR1.1), or "": a stop, a cancel and every error the client did not
// type name none. An answer that came and does not read is the caller's stage (UnreadUnreadable).
func UnreadOf(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, ErrAnswerCut):
		return UnreadCutAnswer
	case errors.Is(err, ErrRefused):
		return UnreadRefused
	case errors.Is(err, ErrNoAnswer):
		return UnreadNoAnswer
	default:
		return ""
	}
}

// Stopped is a refusal of a call of the model the caller does not repeat.
type Stopped struct {
	Stop Stop
}

func (stopped *Stopped) Error() string {
	return "saga memory: the reading stopped: " + string(stopped.Stop)
}

// StopOf is the stop of an error, if it is one.
func StopOf(err error) (Stop, bool) {
	var stopped *Stopped
	if errors.As(err, &stopped) {
		return stopped.Stop, true
	}
	return "", false
}

// Code is the code a Job of the reading ends with when it stopped (reading.funds, reading.key, reading.model,
// reading.connection, reading.no_answer, reading.characters, reading.reading_fence, reading.unfit, reading.unavailable,
// reading.paused).
func (stop Stop) Code() string {
	switch stop {
	case StopFunds, StopKey, StopModel, StopConnection, StopNoAnswer, StopCharacters, StopReadingFence, StopUnfit, StopUnavailable:
		return "reading." + string(stop)
	default:
		return "reading.paused"
	}
}
