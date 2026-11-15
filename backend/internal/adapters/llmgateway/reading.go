// SPDX-License-Identifier: AGPL-3.0-only

package llmgateway

import (
	"context"
	"encoding/json"
	"regexp"
	"strconv"
	"unicode/utf8"

	"canonkeeper/backend/internal/domain/aisettings"
	"canonkeeper/backend/internal/domain/coresteps"
	"canonkeeper/backend/internal/domain/modeljournal"

	"github.com/canonkeeper/desktop-app/translate"
)

// Reading is the check of the reading of the books (specs/2026-11-13-desk-consent.md CN3.3): the gateway asks it for every
// request of a reading purpose — the cards, their passes, the names and the steps of the layer — after the request took its
// place and before the provider, every repeat of the client included. The consent of the app answers: a request let go
// holds its reserve until the gateway settles it; one refused names why and goes nowhere.
type Reading interface {
	Admit(ctx context.Context, request ReadingRequest) (Ticket, string, error)
}

// ReadingRequest is what the check knows of a request: its Job and purpose, the provider and the model it goes to, its
// size and the settings of this minute.
type ReadingRequest struct {
	JobID, Purpose string
	Host, Model    string
	RequestBytes   int64
	Settings       aisettings.Settings
}

// Ticket is a request of the reading let go.
type Ticket interface {
	// Settle replaces the reserve of the request by what it cost and writes the count of the reading with its row of the
	// journal at once; answered says the provider began its answer — a request it refused before took nothing.
	Settle(ctx context.Context, entry modeljournal.Request, answered bool)
	// Failed hears how the provider refused it: the funds, the key and the model pause the reading at once, no connection
	// only on the last attempt of the client.
	Failed(ctx context.Context, failure aisettings.Failure, last bool)
	// Release lets the reserve of a request that never went go, without a row of the journal and without a count: its
	// record did not lie, or it was refused between the check and the sending (specs/2026-11-14-desk-journal.md JR1.1).
	// It pauses nothing.
	Release(ctx context.Context)
	// Stop hears a refusal of the site of the account that stops the reading (specs/2026-11-13-desk-account-ai.md AI3.5):
	// the characters of the tier, the fence of the work of the reading, an app the site does not take, the reading of the site
	// unavailable — the reason of the pause of the same consent.
	Stop(ctx context.Context, reason string)
}

// codeReadingPaused refuses a request of the reading the check did not let go; the client of the app does not repeat it.
const codeReadingPaused = "reading.paused"

// readingRefused is a request of the reading the check did not let go, with why.
type readingRefused struct{ reason string }

func (refused *readingRefused) Error() string {
	return "llmgateway: the reading is paused: " + refused.reason
}

// readingPurposes are the steps of the core that read the books of a saga for its consent.
var readingPurposes = map[string]bool{
	string(coresteps.Card): true, string(coresteps.CardPass): true, string(coresteps.Names): true,
	string(coresteps.LayerBook): true, string(coresteps.LayerReduce): true, string(coresteps.LayerVerify): true,
	string(coresteps.LayerGap): true,
}

// UseReading makes the gateway check every request of the reading once the consent of the app is composed.
func (gateway *Gateway) UseReading(reading Reading) {
	gateway.reading.Store(&readingBox{reading: reading})
}

type readingBox struct{ reading Reading }

// admit asks the check about a request of a reading purpose; any other goes unasked.
func (gateway *Gateway) admit(ctx context.Context, request call, entry modeljournal.Request, settings aisettings.Settings) (Ticket, error) {
	if !readingPurposes[request.purpose] {
		return nil, nil
	}
	box := gateway.reading.Load()
	if box == nil {
		return nil, nil
	}
	ticket, refusal, err := box.reading.Admit(ctx, ReadingRequest{
		JobID: request.jobID, Purpose: request.purpose, Host: entry.ProviderHost, Model: entry.Model,
		RequestBytes: request.size, Settings: settings,
	})
	switch {
	case err != nil:
		return nil, err
	case ticket == nil:
		if refusal == "" {
			refusal = "consent"
		}
		return nil, &readingRefused{reason: refusal}
	default:
		return ticket, nil
	}
}

// characters counts the characters of the answer that go by, its reasoning included: the output of a request without usage
// is counted by them (CN3.3). began is whether the provider began its answer: any event but a failure. failure is the kind
// of a response.failed that went by (specs/2026-11-15-desk-reading-no-answer.md NA1.1): a provider of Responses ends its
// stream with it, and the module hands it as an end, not as an error.
type characters struct {
	count   int64
	began   bool
	failure aisettings.Failure
}

func (counted *characters) event(event translate.Event) {
	if event.Type != "response.failed" && event.Type != "error" {
		counted.began = true
	}
	if event.Type == "response.failed" {
		counted.failure, _ = failureOfEvent(event.Data)
	}
	switch event.Type {
	case "response.output_text.delta", "response.reasoning_text.delta", "response.reasoning_summary_text.delta",
		"response.function_call_arguments.delta":
		var delta struct {
			Delta string `json:"delta"`
		}
		if json.Unmarshal(event.Data, &delta) == nil {
			counted.count += int64(utf8.RuneCountInString(delta.Delta))
		}
	}
}

// of is the kind of a failure of the request as the check hears it: once the provider began its answer, a failure
// mid-answer (aisettings.Failure.MidAnswer; specs/2026-11-15-desk-reading-no-answer.md NA1.1).
func (counted *characters) of(failure aisettings.Failure) aisettings.Failure {
	if counted.began {
		return failure.MidAnswer()
	}
	return failure
}

// ofResponse counts the text of a whole answer of a request that did not stream.
func (counted *characters) ofResponse(response []byte) {
	counted.began = len(response) > 0
	var terminal struct {
		Output []item `json:"output"`
	}
	if json.Unmarshal(response, &terminal) != nil {
		return
	}
	for _, output := range terminal.Output {
		counted.count += int64(utf8.RuneCountInString(output.Arguments))
		for _, part := range output.Content {
			counted.count += int64(utf8.RuneCountInString(part.Text))
		}
	}
}

// costOf is what a request with the usage of the provider cost by the prices of this minute; nil without them.
func costOf(own aisettings.Own, tokens *modeljournal.Tokens) *modeljournal.Cost {
	prices, ok := own.Advanced.Prices()
	if !ok || tokens == nil {
		return nil
	}
	micros := prices.Cost(aisettings.Tokens{Input: tokens.Input, Cached: tokens.CachedInput, Output: tokens.Output})
	return &modeljournal.Cost{Micros: micros, Currency: string(prices.Currency)}
}

// httpStatus is the status of the refusal of a provider the module of the protocols writes into the message of a failed
// stream: «upstream code (HTTP 402): …».
var httpStatus = regexp.MustCompile(`\(HTTP (\d{3})\)`)

// withStopReason names in a response.failed event the reason its refusal stops the reading with, by the one table of the
// domain (specs/2026-11-13-desk-consent.md CN3.4), or refused (specs/2026-11-15-desk-reading-rest.md RR1.1): a provider of
// Chat Completions says its refusal in the stream, and the client of the app reads the reason there as it reads it in the
// body of a refusal before the stream. Every other event, and a failure the client may repeat, goes as it came. A failure
// after the provider began its answer (began) is of its kind mid-answer (specs/2026-11-15-desk-reading-no-answer.md NA1.1):
// a stream torn by the connection, or a refusal said mid-answer, names no reason, and the client repeats it as no answer.
func withStopReason(event translate.Event, began bool) translate.Event {
	if event.Type != "response.failed" {
		return event
	}
	failure, named := failureOfEvent(event.Data)
	if began {
		failure = failure.MidAnswer()
	}
	reason := stopReason(failure)
	if !named || reason == "" {
		return event
	}
	var failed map[string]json.RawMessage
	if json.Unmarshal(event.Data, &failed) != nil {
		return event
	}
	var response map[string]json.RawMessage
	if json.Unmarshal(failed["response"], &response) != nil {
		return event
	}
	var described map[string]any
	if json.Unmarshal(response["error"], &described) != nil {
		return event
	}
	described["reason"] = reason
	response["error"], _ = json.Marshal(described)
	failed["response"], _ = json.Marshal(response)
	data, err := json.Marshal(failed)
	if err != nil {
		return event
	}
	event.Data = data
	return event
}

// failureOfEvent is the kind of the refusal a response.failed event names by its error — its code, and the status of the
// provider the module of the protocols writes into its message — by the one table of the domain (CN3.4); an event that names
// no error is a failure of the provider all the same, and named is false.
func failureOfEvent(data []byte) (failure aisettings.Failure, named bool) {
	var failed struct {
		Response struct {
			Error *struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		} `json:"response"`
	}
	if json.Unmarshal(data, &failed) != nil || failed.Response.Error == nil {
		return aisettings.FailureOther, false
	}
	status := 0
	if match := httpStatus.FindStringSubmatch(failed.Response.Error.Message); match != nil {
		status, _ = strconv.Atoi(match[1])
	}
	code := failed.Response.Error.Code
	return aisettings.Classify(status, code, failed.Response.Error.Message, code == translate.ErrorCodeConnection), true
}
