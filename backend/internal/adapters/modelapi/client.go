// Package modelapi calls a Responses API endpoint directly for backend-owned model work
// (chapter cards, exhaustive scans, drawings of the saga gallery). It is a thin transport: prompts and parsing belong
// to the calling service.
package modelapi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"canonkeeper/backend/internal/domain/quota"

	"github.com/openai/codex/sdk/go/images"
)

// ErrNotConfigured is the answer of every call of a client without a base URL: no AI is connected yet (the desktop
// before the author connects one, specs/2026-11-07-desktop-app.md DA2.6), so no request is made.
var ErrNotConfigured = errors.New("model API is not configured")

type Config struct {
	BaseURL string
	KeyEnv  string // name of the environment variable holding the API key; empty for keyless relays
	Model   string
	Effort  string
	// CardsModel and CardsEffort are what chapter cards (the Background port) call; empty, they
	// are Model and Effort (specs/2026-10-29-chapter-cards-quality.md, UC3).
	CardsModel  string
	CardsEffort string
	// ImageModel and ImageQuality draw the saga gallery (specs/2026-10-29-saga-illustrations.md
	// R1.2); empty, drawings use Model and medium quality.
	ImageModel   string
	ImageQuality string
	// CheckModel looks at a drawing and checks it against the facts of the text (R12.2, R13.11);
	// empty, Model.
	CheckModel string
	// WritingModel, WritingEffort and WritingCheckModel are the writing agent (specs/2026-11-06-writing-agent.md, WA5.11):
	// the model that writes the scenes, at WritingEffort or high, and another model that checks them; the configuration
	// requires both.
	WritingModel      string
	WritingEffort     string
	WritingCheckModel string
	// Parallel is the number of model calls the whole process makes at once (model_api.parallel).
	Parallel int
	// VoiceBrain and VoiceBrainEffort are the assistant of the voice (specs/2026-11-03-voice-assistant.md V4.3).
	VoiceBrain       string
	VoiceBrainEffort string
}

// Client is the one transport of the process; callers use it through a Port of their class.
type Client struct {
	config Config
	http   *http.Client
	gate   *gate
	pause  func(attempt int) time.Duration
}

// New makes the client of the process; without a base URL it is a client of an AI not connected yet, whose every call
// answers ErrNotConfigured.
func New(config Config) (*Client, error) {
	if strings.TrimSpace(config.Model) == "" {
		return nil, ErrNotConfigured
	}
	return &Client{
		config: config, http: &http.Client{Timeout: 30 * time.Minute}, gate: newGate(config.Parallel),
		pause: func(attempt int) time.Duration { return time.Duration(attempt*attempt) * 3 * time.Second },
	}, nil
}

// Connected reports whether the client has a model API to call: the desktop has none until the author connects an AI
// (specs/2026-11-12-desk-window.md WN3.1).
func (client *Client) Connected() bool { return strings.TrimSpace(client.config.BaseURL) != "" }

// Images names the image model and quality: ImageModel, or the image model of the SDK
// (gpt-image-2) when none is set, and ImageQuality, or medium. The subscription picks quality and
// size itself; they are still asked for honestly (R13.2).
func (config Config) Images() (model, quality string) {
	model, quality = config.ImageModel, config.ImageQuality
	if model == "" {
		model = images.DefaultModel
	}
	if quality == "" {
		quality = "medium"
	}
	return model, quality
}

// ImagesRoute is what a port of the images routes needs of the client (the site's drawings,
// specs/2026-10-29-saga-illustrations.md UC13): the base address, the key environment variable, the image model and
// quality, the HTTP client and Hold, which takes a place of the gate before chapter cards — a writer waits for a
// drawing — and gives its release.
type ImagesRoute struct {
	BaseURL, KeyEnv, Model, Quality string
	HTTP                            *http.Client
	Hold                            func(context.Context) (func(), error)
}

// ImagesRoute is the images route of model_api.
func (client *Client) ImagesRoute() ImagesRoute {
	model, quality := client.config.Images()
	return ImagesRoute{
		BaseURL: client.config.BaseURL, KeyEnv: client.config.KeyEnv, Model: model, Quality: quality, HTTP: client.http,
		Hold: func(ctx context.Context) (func(), error) {
			if err := client.gate.acquire(ctx, Interactive); err != nil {
				return nil, err
			}
			return client.gate.release, nil
		},
	}
}

// Port is the client for one class of callers: the chat's scan is Interactive, chapter cards
// are Background (UC11); each class calls its own model.
type Port struct {
	client        *Client
	class         Class
	model, effort string
}

// Interactive is the port of calls a writer waits for during a chat turn.
func (client *Client) Interactive() Port {
	return Port{client: client, class: Interactive, model: client.config.Model, effort: client.config.Effort}
}

// Background is the port of calls nobody waits for at once: chapter cards, with the cards model.
func (client *Client) Background() Port {
	model, effort := client.config.Cards()
	return Port{client: client, class: Background, model: model, effort: effort}
}

// agentEffort is the effort the measurement wrote scenes with; checkEffort the one it checked them with.
const (
	agentEffort = "high"
	checkEffort = "medium"
)

// Prose is the port of the writing agent's scenes and rewrites (specs/2026-11-06-writing-agent.md, WA5.1, WA5.11): in the
// class of a call a person waits for, as the chat.
func (client *Client) Prose() Port {
	model, effort := client.config.Prose()
	return Port{client: client, class: Interactive, model: model, effort: effort}
}

// Proofing is the port of the check of the agent's text: another model than the one that wrote it (WA5.11).
func (client *Client) Proofing() Port {
	return Port{client: client, class: Interactive, model: client.config.WritingCheckModel, effort: checkEffort}
}

// ProseModel and ProofingModel name the models of the writing agent for its spend.
func (client *Client) ProseModel() string {
	model, _ := client.config.Prose()
	return model
}

func (client *Client) ProofingModel() string { return client.config.WritingCheckModel }

// Prose names the model and effort of the agent's scenes: WritingModel at WritingEffort, or high.
func (config Config) Prose() (model, effort string) {
	effort = agentEffort
	if config.WritingEffort != "" {
		effort = config.WritingEffort
	}
	return config.WritingModel, effort
}

// Voice is the port of the assistant of the voice: a person waits for it in a conversation (V4.3).
func (client *Client) Voice() Port {
	return Port{client: client, class: Interactive, model: client.config.VoiceBrain, effort: client.config.VoiceBrainEffort}
}

// Model names the model of the port.
func (port Port) Model() string { return port.model }

// CardsModel names the model chapter cards are written with.
func (client *Client) CardsModel() string {
	model, _ := client.config.Cards()
	return model
}

// CheckModel names the model drawings are checked with.
func (client *Client) CheckModel() string { return client.config.Check() }

// Check names the model of the drawing check: CheckModel, or Model when none is set.
func (config Config) Check() string {
	if config.CheckModel != "" {
		return config.CheckModel
	}
	return config.Model
}

// Cards names the model and effort chapter cards are written with: CardsModel, or Model when no
// cards model is set, with CardsEffort, or Effort when no cards effort is set.
func (config Config) Cards() (model, effort string) {
	model, effort = config.Model, config.Effort
	if config.CardsModel != "" {
		model = config.CardsModel
	}
	if config.CardsEffort != "" {
		effort = config.CardsEffort
	}
	return model, effort
}

type request struct {
	Model        string         `json:"model"`
	Instructions string         `json:"instructions"`
	Input        []inputMessage `json:"input"`
	Stream       bool           `json:"stream"`
	Store        bool           `json:"store"`
	Reasoning    map[string]any `json:"reasoning,omitempty"`
}

type inputMessage struct {
	Type    string         `json:"type"`
	Role    string         `json:"role"`
	Content []inputContent `json:"content"`
}

type inputContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// Complete sends instructions and one user input and returns the streamed output text and what
// the call cost: every HTTP attempt answered with 200 is a call, tokens come from the usage of
// response.completed (R7.1); usage is returned with an error too. Rate limits and server errors
// are retried with backoff. Every HTTP attempt holds a place of the process gate and frees it
// before the retry pause (R11.1).
func (port Port) Complete(ctx context.Context, instructions, input string) (string, quota.ModelUsage, error) {
	return port.Stream(ctx, instructions, input, nil)
}

// Stream is Complete that hands the answer to portions as it comes (specs/2026-11-12-ux10-scene-stream.md ST1.1): the text
// of the HTTP attempt that goes on so far after each delta, and "" at the start of each attempt — what an attempt that
// broke gave is gone. Without portions it is Complete.
func (port Port) Stream(ctx context.Context, instructions, input string, portions func(sofar string)) (string, quota.ModelUsage, error) {
	client := port.client
	if !client.Connected() {
		return "", quota.ModelUsage{}, ErrNotConfigured
	}
	body := request{
		Model: port.model, Instructions: instructions, Stream: true, Store: false,
		Input: []inputMessage{{Type: "message", Role: "user", Content: []inputContent{{Type: "input_text", Text: input}}}},
	}
	if port.effort != "" {
		body.Reasoning = map[string]any{"effort": port.effort}
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return "", quota.ModelUsage{}, err
	}
	var usage quota.ModelUsage
	var lastErr error
	for attempt := 0; attempt < 6; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return "", usage, ctx.Err()
			case <-time.After(client.pause(attempt)):
			}
		}
		if err := client.gate.acquire(ctx, port.class); err != nil {
			return "", usage, err
		}
		if portions != nil {
			portions("")
		}
		text, spent, retry, err := client.once(ctx, payload, portions)
		client.gate.release()
		usage = usage.Add(spent)
		if err == nil {
			return text, usage, nil
		}
		lastErr = err
		if !retry || ctx.Err() != nil {
			break
		}
	}
	return "", usage, lastErr
}

// responseUsage is the usage of response.completed in the Responses API.
type responseUsage struct {
	InputTokens        int64 `json:"input_tokens"`
	InputTokensDetails struct {
		CachedTokens int64 `json:"cached_tokens"`
	} `json:"input_tokens_details"`
	OutputTokens        int64 `json:"output_tokens"`
	OutputTokensDetails struct {
		ReasoningTokens int64 `json:"reasoning_tokens"`
	} `json:"output_tokens_details"`
}

func (client *Client) once(ctx context.Context, payload []byte, portions func(string)) (string, quota.ModelUsage, bool, error) {
	var usage quota.ModelUsage
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(client.config.BaseURL, "/")+"/responses", bytes.NewReader(payload))
	if err != nil {
		return "", usage, false, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	if client.config.KeyEnv != "" {
		key := os.Getenv(client.config.KeyEnv)
		if key == "" {
			return "", usage, false, fmt.Errorf("model API key variable %s is empty", client.config.KeyEnv)
		}
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := client.http.Do(req)
	if err != nil {
		return "", usage, ctx.Err() == nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		excerpt, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		retry := resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500
		return "", usage, retry, fmt.Errorf("model API status %d: %s", resp.StatusCode, strings.TrimSpace(string(excerpt)))
	}
	usage.Calls = 1
	var text strings.Builder
	completed := false
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 1<<20), 64<<20)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" || data == "[DONE]" {
			continue
		}
		var event struct {
			Type     string `json:"type"`
			Delta    string `json:"delta"`
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
		case "response.output_text.delta":
			text.WriteString(event.Delta)
			if portions != nil && event.Delta != "" {
				portions(text.String())
			}
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
			return "", usage, true, errors.New(message)
		}
	}
	if err := scanner.Err(); err != nil {
		return "", usage, true, err
	}
	if !completed {
		return "", usage, true, errors.New("model API stream ended before completion")
	}
	return text.String(), usage, false, nil
}
