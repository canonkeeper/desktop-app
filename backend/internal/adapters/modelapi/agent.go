package modelapi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
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
		output, spent, err := port.round(ctx, payload)
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
func (port Port) round(ctx context.Context, payload []byte) ([]json.RawMessage, quota.ModelUsage, error) {
	client := port.client
	if !client.Connected() {
		return nil, quota.ModelUsage{}, ErrNotConfigured
	}
	var usage quota.ModelUsage
	var lastErr error
	for attempt := 0; attempt < 6; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, usage, ctx.Err()
			case <-time.After(client.pause(attempt)):
			}
		}
		if err := client.gate.acquire(ctx, port.class); err != nil {
			return nil, usage, err
		}
		output, spent, retry, err := client.items(ctx, payload)
		client.gate.release()
		usage = usage.Add(spent)
		if err == nil {
			return output, usage, nil
		}
		lastErr = err
		if !retry || ctx.Err() != nil {
			break
		}
	}
	return nil, usage, lastErr
}

// items reads the output items of a streamed response: each comes whole in response.output_item.done.
func (client *Client) items(ctx context.Context, payload []byte) ([]json.RawMessage, quota.ModelUsage, bool, error) {
	var usage quota.ModelUsage
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(client.config.BaseURL, "/")+"/responses", bytes.NewReader(payload))
	if err != nil {
		return nil, usage, false, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	if client.config.KeyEnv != "" {
		key := os.Getenv(client.config.KeyEnv)
		if key == "" {
			return nil, usage, false, fmt.Errorf("model API key variable %s is empty", client.config.KeyEnv)
		}
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := client.http.Do(req)
	if err != nil {
		return nil, usage, ctx.Err() == nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		excerpt, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		retry := resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500
		return nil, usage, retry, fmt.Errorf("model API status %d: %s", resp.StatusCode, strings.TrimSpace(string(excerpt)))
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
			return nil, usage, true, errors.New(message)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, usage, true, err
	}
	if !completed {
		return nil, usage, true, errors.New("model API stream ended before completion")
	}
	return output, usage, false, nil
}
