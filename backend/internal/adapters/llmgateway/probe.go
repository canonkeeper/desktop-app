// SPDX-License-Identifier: AGPL-3.0-only

package llmgateway

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"

	"canonkeeper/backend/internal/domain/aisettings"
	"canonkeeper/backend/internal/domain/modeljournal"

	"github.com/canonkeeper/desktop-app/translate"
)

// Probe asks the provider of the author one probe of «Проверить связь» (specs/2026-11-13-desk-probes.md PB2.2) the way the
// work goes — the alias of its role, the settings and the key read now, a place among «Запросов одновременно», the
// protocol, the module — with the made-up words of the domain alone and the purpose probe, which no request from outside
// can name. The answer is read from the stream and never kept; a failure of the provider is its kind, never its words. A
// long answer is read as far as the step says is enough and its request is closed then (PB2.3 (4)): its row of the journal
// is canceled, without the usage the provider did not send. The error is the end of ctx: the check stopped.
// Every probe of one check carries its number (specs/2026-11-14-desk-journal.md JR1.7): its record and row are of one check.
func (gateway *Gateway) Probe(ctx context.Context, check string, step aisettings.Step) (aisettings.Answer, error) {
	role := roleChat
	if step.Role == aisettings.RoleBooks {
		role = roleCards
	}
	body := probeRequest(role, aisettings.QuestionOf(step.Probe))
	request, closeRequest := context.WithCancel(ctx)
	defer closeRequest()
	reading := reading{enough: step.Enough, close: closeRequest}
	result, err := gateway.send(request, call{role: role, purpose: modeljournal.PurposeProbe, stream: true, body: body, size: int64(len(body)), checkID: check}, reading.event)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return aisettings.Answer{}, ctxErr
	}
	if reading.full {
		return reading.answer(translate.Result{}), nil
	}
	if err != nil {
		return aisettings.Answer{Failure: failureOf(err)}, nil
	}
	return reading.answer(result), nil
}

// probeRequest is the request of a probe as Codex or the core would send it: the alias of the role, the words of the probe,
// a stream; the function of the probe when it offers one.
func probeRequest(role role, question aisettings.Question) []byte {
	model := ChatModel
	if role == roleCards {
		model = CardsModel
	}
	type content struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	type message struct {
		Type    string    `json:"type"`
		Role    string    `json:"role"`
		Content []content `json:"content"`
	}
	type function struct {
		Type        string          `json:"type"`
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Parameters  json.RawMessage `json:"parameters"`
		Strict      bool            `json:"strict"`
	}
	request := struct {
		Model        string     `json:"model"`
		Instructions string     `json:"instructions"`
		Input        []message  `json:"input"`
		Tools        []function `json:"tools,omitempty"`
		ToolChoice   string     `json:"tool_choice,omitempty"`
		Stream       bool       `json:"stream"`
		Store        bool       `json:"store"`
	}{
		Model: model, Instructions: question.Instructions, Stream: true,
		Input: []message{{Type: "message", Role: "user", Content: []content{{Type: "input_text", Text: question.Input}}}},
	}
	if tool := question.Tool; tool != nil {
		request.Tools = []function{{Type: "function", Name: tool.Name, Description: tool.Description, Parameters: json.RawMessage(tool.Parameters)}}
		request.ToolChoice = "auto"
	}
	encoded, _ := json.Marshal(request)
	return encoded
}

// item is an item of the output of Responses: a message with its text or the call of a function.
type item struct {
	Type      string `json:"type"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
	Content   []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
}

// reading gathers the answer of a probe from the events of its stream; once its text has enough characters it closes the
// request and the stream ends (full).
type reading struct {
	text       strings.Builder
	characters int
	calls      []aisettings.Call
	enough     int
	close      context.CancelFunc
	full       bool
}

func (reading *reading) event(event translate.Event) error {
	switch event.Type {
	case "response.output_text.delta":
		var delta struct {
			Delta string `json:"delta"`
		}
		if json.Unmarshal(event.Data, &delta) == nil {
			reading.text.WriteString(delta.Delta)
			reading.characters += utf8.RuneCountInString(delta.Delta)
		}
		if reading.enough > 0 && reading.characters >= reading.enough {
			reading.full = true
			reading.close()
			return context.Canceled
		}
	case "response.output_item.done":
		var done struct {
			Item item `json:"item"`
		}
		if json.Unmarshal(event.Data, &done) == nil && done.Item.Type == "function_call" {
			reading.calls = append(reading.calls, aisettings.Call{Name: done.Item.Name, Arguments: done.Item.Arguments})
		}
	}
	return nil
}

// answer is the answer the stream gave; the terminal response holds the output a provider did not say in events.
func (reading *reading) answer(result translate.Result) aisettings.Answer {
	answer := aisettings.Answer{Text: reading.text.String(), Calls: reading.calls, Cut: result.IncompleteReason == "max_output_tokens"}
	if result.Failed && result.IncompleteReason == "" {
		return aisettings.Answer{Failure: aisettings.FailureOther}
	}
	var terminal struct {
		Output []item `json:"output"`
	}
	if json.Unmarshal(result.Response, &terminal) == nil {
		var text strings.Builder
		var calls []aisettings.Call
		for _, output := range terminal.Output {
			switch output.Type {
			case "message":
				for _, part := range output.Content {
					if part.Type == "output_text" {
						text.WriteString(part.Text)
					}
				}
			case "function_call":
				calls = append(calls, aisettings.Call{Name: output.Name, Arguments: output.Arguments})
			}
		}
		if answer.Text == "" {
			answer.Text = text.String()
		}
		if len(answer.Calls) == 0 {
			answer.Calls = calls
		}
	}
	if result.UsageKnown {
		answer.Usage = &aisettings.Usage{Input: tokens(result.Usage.InputTokens), Output: tokens(result.Usage.OutputTokens)}
		if result.Usage.ReasoningTokensKnown {
			answer.Usage.Reasoning = tokens(result.Usage.ReasoningTokens)
		}
	}
	return answer
}

// failureOf is the kind of a failure of the provider or of the module by the one table of the domain (aisettings.Classify).
func failureOf(err error) aisettings.Failure {
	var failure *translate.Error
	if !errors.As(err, &failure) {
		return aisettings.FailureOther
	}
	return aisettings.Classify(failure.Status, failure.Code, failure.Message, failure.Code == translate.ErrorCodeConnection)
}

// waits says the provider asks to wait (PB2.4): the one rule of the probes and of the list of the models.
func waits(status int, code string) bool {
	return aisettings.Waits(status, code)
}
