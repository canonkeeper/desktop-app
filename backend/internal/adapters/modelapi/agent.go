package modelapi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"canonkeeper/backend/internal/domain/quota"
)

// Function is a tool the model may call in a turn of Act.
type Function struct {
	Name        string
	Description string
	Parameters  map[string]any
}

// Output is what a function answered: its text and the images the model sees after it.
type Output struct {
	Text   string
	Images []Image
}

// Image is an image for the model, with its media type.
type Image struct {
	MediaType string
	Data      []byte
}

// maxActRounds bounds the rounds of tool calls of one turn: a look through a gallery takes a dozen.
const maxActRounds = 16

// Act runs a turn of the model with tools (specs/2026-11-03-voice-assistant.md V4.3): every function the model calls
// goes to run and its output back to the model, until the model answers in text. The reasoning of a round goes back
// encrypted with the next one; nothing is stored by the API.
func (port Port) Act(ctx context.Context, instructions, input string, tools []Function,
	run func(ctx context.Context, callID, name, arguments string) (Output, error),
) (string, quota.ModelUsage, error) {
	items := []any{inputMessage{Type: "message", Role: "user", Content: []inputContent{{Type: "input_text", Text: input}}}}
	functions := make([]map[string]any, len(tools))
	for index, tool := range tools {
		functions[index] = map[string]any{"type": "function", "name": tool.Name, "description": tool.Description, "parameters": tool.Parameters}
	}
	var usage quota.ModelUsage
	// Every round and every attempt of the turn go with the thread of its context (specs/2026-11-17-model-cache.md MC1.1).
	send := sending{keyEnv: port.keyEnv, thread: port.client.threadOf(ctx)}
	for round := 0; round < maxActRounds; round++ {
		body := map[string]any{
			"model": port.model, "instructions": instructions, "input": items, "tools": functions, "tool_choice": "auto",
			"stream": true, "store": false, "include": []string{"reasoning.encrypted_content"},
		}
		if port.effort != "" {
			body["reasoning"] = map[string]any{"effort": port.effort}
		}
		payload, err := json.Marshal(body)
		if err != nil {
			return "", usage, err
		}
		output, spent, err := port.round(ctx, send, payload)
		usage = usage.Add(spent)
		if err != nil {
			return "", usage, err
		}
		var calls []functionCall
		var text strings.Builder
		for _, raw := range output {
			var item struct {
				Type      string `json:"type"`
				CallID    string `json:"call_id"`
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
				Content   []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
			}
			if json.Unmarshal(raw, &item) != nil {
				continue
			}
			items = append(items, raw)
			switch item.Type {
			case "function_call":
				calls = append(calls, functionCall{callID: item.CallID, name: item.Name, arguments: item.Arguments})
			case "message":
				for _, content := range item.Content {
					if content.Type == "output_text" {
						text.WriteString(content.Text)
					}
				}
			}
		}
		if len(calls) == 0 {
			return strings.TrimSpace(text.String()), usage, nil
		}
		for _, call := range calls {
			result, err := run(ctx, call.callID, call.name, call.arguments)
			if err != nil {
				return "", usage, err
			}
			items = append(items, map[string]any{"type": "function_call_output", "call_id": call.callID, "output": result.Text})
			if len(result.Images) > 0 {
				// A function's output is text; the images it shows come right after it, as the user's.
				content := []map[string]any{{"type": "input_text", "text": "Изображения из ответа " + call.name + ", по порядку:"}}
				for _, image := range result.Images {
					content = append(content, map[string]any{"type": "input_image",
						"image_url": "data:" + image.MediaType + ";base64," + base64.StdEncoding.EncodeToString(image.Data)})
				}
				items = append(items, map[string]any{"type": "message", "role": "user", "content": content})
			}
		}
	}
	return "", usage, errors.New("model API: the turn called tools without an end")
}

type functionCall struct{ callID, name, arguments string }

// round is one call of a turn with the retries and the gate of Complete; it answers the output items.
func (port Port) round(ctx context.Context, send sending, payload []byte) ([]json.RawMessage, quota.ModelUsage, error) {
	client := port.client
	if !client.Connected() {
		return nil, quota.ModelUsage{}, ErrNotConfigured
	}
	var usage quota.ModelUsage
	var lastErr error
	timeouts := 0
	for try := 0; try < attempts; try++ {
		if try > 0 {
			select {
			case <-ctx.Done():
				return nil, usage, ctx.Err()
			case <-time.After(client.pause(try)):
			}
		}
		output, spent, outcome, err := exchange(ctx, client, port.class, send.thread, func() ([]json.RawMessage, quota.ModelUsage, attempt, error) {
			return client.items(ctx, send, payload)
		})
		usage = usage.Add(spent)
		if err == nil {
			return output, usage, nil
		}
		lastErr = err
		if !outcome.retry || ctx.Err() != nil || client.timedOutTwice(outcome, &timeouts) {
			break
		}
	}
	return nil, usage, lastErr
}

// items reads the output items of a streamed response: each comes whole in response.output_item.done.
func (client *Client) items(ctx context.Context, send sending, payload []byte) ([]json.RawMessage, quota.ModelUsage, attempt, error) {
	var usage quota.ModelUsage
	call := ctx
	ctx, cancel := client.attempt(ctx)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(client.config.BaseURL, "/")+"/responses", bytes.NewReader(payload))
	if err != nil {
		return nil, usage, attempt{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	if err := client.authorize(req, send, false); err != nil {
		return nil, usage, attempt{}, err
	}
	resp, err := client.http.Do(req)
	if err != nil {
		outcome := attempt{retry: call.Err() == nil}
		return nil, usage, outcome, client.timedOut(call, ctx, &outcome, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		outcome, err := refused(resp)
		return nil, usage, outcome, err
	}
	usage.Calls = 1
	var output []json.RawMessage
	completed := false
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 1<<20), 64<<20)
	for scanner.Scan() {
		data := strings.TrimSpace(strings.TrimPrefix(scanner.Text(), "data:"))
		if !strings.HasPrefix(scanner.Text(), "data:") || data == "" || data == "[DONE]" {
			continue
		}
		var event struct {
			Type     string          `json:"type"`
			Item     json.RawMessage `json:"item"`
			Response struct {
				Error *struct {
					Message string `json:"message"`
				} `json:"error"`
				Usage *responseUsage `json:"usage"`
			} `json:"response"`
		}
		if json.Unmarshal([]byte(data), &event) != nil {
			continue
		}
		switch event.Type {
		case "response.output_item.done":
			output = append(output, event.Item)
		case "response.completed":
			completed = true
			if spent := event.Response.Usage; spent != nil {
				usage.InputTokens, usage.CachedInputTokens = spent.InputTokens, spent.InputTokensDetails.CachedTokens
				usage.OutputTokens, usage.ReasoningOutputTokens = spent.OutputTokens, spent.OutputTokensDetails.ReasoningTokens
			}
		case "response.failed", "error":
			message := "response failed"
			if event.Response.Error != nil {
				message = event.Response.Error.Message
			}
			return nil, usage, attempt{retry: true}, errors.New(message)
		}
	}
	if err := scanner.Err(); err != nil {
		outcome := attempt{retry: true}
		return nil, usage, outcome, client.timedOut(call, ctx, &outcome, err)
	}
	if !completed {
		outcome := attempt{retry: true}
		return nil, usage, outcome, client.timedOut(call, ctx, &outcome, errors.New("model API stream ended before completion"))
	}
	return output, usage, attempt{}, nil
}
