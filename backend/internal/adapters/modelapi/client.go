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
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"canonkeeper/backend/internal/domain/disclosure"
	"canonkeeper/backend/internal/domain/quota"
	"canonkeeper/backend/internal/domain/sagamemory"
	"canonkeeper/backend/internal/jobs"

	"github.com/openai/codex/sdk/go/images"
)

// ErrNotConfigured is the answer of every call of a client without a base URL: no AI is connected yet (the desktop
// before the author connects one, specs/2026-11-07-desktop-app.md DA2.6), so no request is made.
var ErrNotConfigured = errors.New("model API is not configured")

// ErrIncomplete is an answer the model cut short (response.incomplete; specs/2026-11-13-desk-gateway.md GW3.3): the
// client repeats nothing — the caller decides, a chapter card goes into its own attempt.
var ErrIncomplete = errors.New("model API: the answer was cut short")

// IncompleteError is ErrIncomplete with why the answer was cut, such as max_output_tokens.
type IncompleteError struct {
	Reason string
}

func (cut *IncompleteError) Error() string { return ErrIncomplete.Error() + ": " + cut.Reason }

func (cut *IncompleteError) Unwrap() error { return ErrIncomplete }

// Is names a cut answer to the builders of the reading as the typed sagamemory.ErrAnswerCut
// (specs/2026-11-13-desk-consent.md CN3.6).
func (cut *IncompleteError) Is(target error) bool { return target == sagamemory.ErrAnswerCut }

// JobHeader names the Job of a call to the local gateway of the app (GW3.2); the gateway takes it off before the
// provider.
const JobHeader = "X-CanonKeeper-Job"

// LastAttemptHeader marks the last attempt of a call of the client of the app (specs/2026-11-13-desk-consent.md CN3.4): no
// connection on it is the pause «связь» of the reading; the gateway takes it off before the provider.
const LastAttemptHeader = "X-CanonKeeper-Last-Attempt"

// CheckedHeader marks the answer of the gateway of the app to a request of the reading the check of the consent let go
// (specs/2026-11-15-desk-reading-no-answer.md NA1.1): a provider that does not answer its last attempt pauses the reading at
// «провайдер не отвечает» there, and the client hands its caller the typed stop. The way of the helper of the account does
// not carry it.
const CheckedHeader = "X-CanonKeeper-Reading-Checked"

// attempts is how many HTTP attempts a call makes at most.
const attempts = 6

// maxRetryAfter bounds the wait a provider asks for before a retry.
const maxRetryAfter = 5 * time.Minute

type Config struct {
	BaseURL string
	KeyEnv  string // name of the environment variable holding the API key; empty for keyless relays
	// ReadingKeyEnv names the variable of the key of the reading of books — cards, names, the layer —, a fork of the key of
	// the site with a bound of its own in the pool of codex-lb (specs/2026-11-17-model-cache.md MC4.1); empty, KeyEnv.
	ReadingKeyEnv string
	// Key is the key by value, for the client of the app: the token of its local gateway, which never enters an
	// environment (specs/2026-11-13-desk-gateway.md GW3.1). Set, KeyEnv is not read.
	Key string
	// CarryJob names the Job of the context of a call in JobHeader (GW3.2): the gateway of the app journals it. The site
	// sends no such header.
	CarryJob bool
	// Stops makes the client of the app hand its caller a typed stop (sagamemory.Stopped) for a refusal the gateway names —
	// the funds, the key, the model, a refusal of the check of the reading — without repeating it, and no connection after
	// its last attempt (specs/2026-11-13-desk-consent.md CN3.4). The site keeps its repeats and its errors.
	Stops bool
	// RetryPause is the unit of the pause before a repeat (attempt²·RetryPause); zero is three seconds. Only the program of
	// the process tests shortens it.
	RetryPause time.Duration
	// WaitsPool makes the client of the site wait out a refusal of the pool of codex-lb for its own calls, outside the count
	// of the attempts, while the call lives (specs/2026-11-17-model-cache.md MC4.2); a client without it — of the steps of
	// apps — hands the refusal to its caller (PoolRefusal). Logger takes the line of each wait; nil keeps none.
	WaitsPool bool
	Logger    *log.Logger
	Model     string
	Effort    string
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
	// http sends the calls of the client, whose every attempt holds its own time (attemptTime); images sends the drawings
	// of the images route within the time of their own HTTP client.
	http        *http.Client
	images      *http.Client
	attemptTime time.Duration
	gate        *gate
	pause       func(attempt int) time.Duration
	// poolWait is how long a wait of the pool lasts for the wait it asks; only the tests shorten it.
	poolWait func(asked time.Duration) time.Duration
}

// New makes the client of the process; without a base URL it is a client of an AI not connected yet, whose every call
// answers ErrNotConfigured.
func New(config Config) (*Client, error) {
	if strings.TrimSpace(config.Model) == "" {
		return nil, ErrNotConfigured
	}
	unit := config.RetryPause
	if unit <= 0 {
		unit = 3 * time.Second
	}
	attemptTime := siteAttemptTime
	if config.CarryJob {
		attemptTime = appAttemptTime
	}
	return &Client{
		config: config, http: &http.Client{}, images: &http.Client{Timeout: 30 * time.Minute}, attemptTime: attemptTime, gate: newGate(config.Parallel),
		pause:    func(attempt int) time.Duration { return time.Duration(attempt*attempt) * unit },
		poolWait: func(asked time.Duration) time.Duration { return asked },
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
		BaseURL: client.config.BaseURL, KeyEnv: client.config.KeyEnv, Model: model, Quality: quality, HTTP: client.images,
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
	// keyEnv is the variable of the key of the port; empty, the key of the client.
	keyEnv string
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

// Reading is the port of the reading of books — the cards, the names and the thread layer (MC4.1): Background, with the key
// of the reading when one is named; the materials of the author stay on Background with the key of the site. The gate of
// the process is one for all the ports.
func (client *Client) Reading() Port {
	port := client.Background()
	port.keyEnv = client.config.ReadingKeyEnv
	return port
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
	var asked time.Duration
	unreachable, repeated, checked := false, false, false
	send := sending{keyEnv: port.keyEnv, thread: client.threadOf(ctx)}
	timeouts := 0
	for try := 0; try < attempts; try++ {
		if try > 0 {
			// The wait the provider asked for in Retry-After, or the client's own pause (GW3.3).
			wait := client.pause(try)
			if asked > 0 {
				wait = asked
			}
			select {
			case <-ctx.Done():
				return "", usage, ctx.Err()
			case <-time.After(wait):
			}
		}
		last := try == attempts-1
		text, spent, outcome, err := exchange(ctx, client, port.class, send.thread, func() (string, quota.ModelUsage, attempt, error) {
			if portions != nil {
				portions("")
			}
			return client.once(ctx, send, payload, portions, last)
		})
		usage = usage.Add(spent)
		if err == nil {
			return text, usage, nil
		}
		lastErr, asked, unreachable, repeated, checked = err, outcome.wait, outcome.unreachable, outcome.retry, outcome.checked
		if !outcome.retry || ctx.Err() != nil || client.timedOutTwice(outcome, &timeouts) {
			break
		}
	}
	if client.config.Stops && ctx.Err() == nil {
		switch {
		case unreachable:
			// The repeats of one request are over and the provider is still not reached: the reading pauses at «связь».
			return "", usage, &sagamemory.Stopped{Stop: sagamemory.StopConnection}
		case repeated && checked:
			// Nobody answered the last attempt of a request the check let go: the gateway paused the reading at «провайдер не
			// отвечает» on it (specs/2026-11-15-desk-reading-no-answer.md NA1.1).
			return "", usage, &sagamemory.Stopped{Stop: sagamemory.StopNoAnswer}
		case repeated:
			// The repeats are over and nobody answered (specs/2026-11-15-desk-reading-rest.md RR1.1).
			return "", usage, &noAnswer{err: lastErr}
		}
	}
	return "", usage, lastErr
}

// noAnswer is the last error of a request of the client of the app nobody answered through its repeats — the same words,
// typed as sagamemory.ErrNoAnswer (RR1.1).
type noAnswer struct{ err error }

func (silent *noAnswer) Error() string { return silent.err.Error() }

func (silent *noAnswer) Unwrap() []error { return []error{silent.err, sagamemory.ErrNoAnswer} }

// attempt is what an HTTP attempt says of the next one: whether to repeat it and how long the provider asked to wait.
type attempt struct {
	retry bool
	wait  time.Duration
	// timedOut: the own time of the attempt ended it, before the headers or while its answer was read (LC2.1).
	timedOut bool
	// unreachable: the gateway of the app could not reach the provider, or the client the gateway.
	unreachable bool
	// checked: the gateway let the request go under the check of the reading (CheckedHeader).
	checked bool
	// threadRefused: codex-lb refused the thread of the request, which lost its account (MC1.2).
	threadRefused bool
	// pool: the pool of requests of codex-lb refused the request (MC4.2).
	pool bool
}

// RetryAfter is the wait of a Retry-After of a refusal — seconds or a date — bounded by maxRetryAfter; none is zero.
func RetryAfter(header http.Header) time.Duration {
	value := strings.TrimSpace(header.Get("Retry-After"))
	if value == "" {
		return 0
	}
	var wait time.Duration
	if seconds, err := strconv.ParseUint(value, 10, 31); err == nil {
		wait = time.Duration(seconds) * time.Second
	} else if at, err := http.ParseTime(value); err == nil {
		wait = time.Until(at)
	}
	return min(max(wait, 0), maxRetryAfter)
}

// sending is what the HTTP requests of one call send besides the body: the variable of the key of its port — empty, the
// client's — and its thread.
type sending struct {
	keyEnv string
	thread *callThread
}

// authorize puts the key of a call: by value for the app, from the variable of the environment for the site — of the port,
// or of the client — and the thread of the call.
func (client *Client) authorize(req *http.Request, send sending, last bool) error {
	keyEnv := send.keyEnv
	if keyEnv == "" {
		keyEnv = client.config.KeyEnv
	}
	switch {
	case client.config.Key != "":
		req.Header.Set("Authorization", "Bearer "+client.config.Key)
	case keyEnv != "":
		key := os.Getenv(keyEnv)
		if key == "" {
			return fmt.Errorf("model API key variable %s is empty", keyEnv)
		}
		req.Header.Set("Authorization", "Bearer "+key)
	}
	send.thread.name(req.Header)
	if client.config.CarryJob {
		if job := jobs.JobIDOf(req.Context()); job != "" {
			req.Header.Set(JobHeader, job)
		}
		// What the call gives, named by its builder in its context (specs/2026-11-14-desk-journal.md JR1.4): the gateway of
		// the app writes it into the record of the sending and takes it off. A call that named nothing goes without it and is
		// refused there.
		if named, ok := disclosure.Of(req.Context()); ok {
			if value, err := named.Encode(); err == nil {
				req.Header.Set(disclosure.Header, value)
			}
		}
	}
	if client.config.Stops && last {
		req.Header.Set(LastAttemptHeader, "1")
	}
	return nil
}

// refused is a refusal of the provider of the site, read from the first 300 bytes of its body: a wait (429) and a failure
// (5xx) are repeated, and a refusal of the thread and of the pool are named (MC1.2, MC4.2). The excerpt goes into the error.
func refused(resp *http.Response) (attempt, error) {
	excerpt, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
	return attempt{
			retry: resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500, wait: RetryAfter(resp.Header),
			threadRefused: ThreadRefused(resp.StatusCode, excerpt), pool: PoolRefused(resp.StatusCode, excerpt),
		},
		fmt.Errorf("model API status %d: %s", resp.StatusCode, strings.TrimSpace(string(excerpt)))
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

func (client *Client) once(ctx context.Context, send sending, payload []byte, portions func(string), last bool) (string, quota.ModelUsage, attempt, error) {
	var usage quota.ModelUsage
	call := ctx
	ctx, cancel := client.attempt(ctx)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(client.config.BaseURL, "/")+"/responses", bytes.NewReader(payload))
	if err != nil {
		return "", usage, attempt{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	if err := client.authorize(req, send, last); err != nil {
		return "", usage, attempt{}, err
	}
	resp, err := client.http.Do(req)
	if err != nil {
		outcome := attempt{retry: call.Err() == nil, unreachable: client.config.Stops}
		return "", usage, outcome, client.timedOut(call, ctx, &outcome, err)
	}
	defer resp.Body.Close()
	checked := client.config.Stops && resp.Header.Get(CheckedHeader) != ""
	if resp.StatusCode != http.StatusOK && client.config.Stops {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		if code := journalCode(raw); code != "" {
			// The gateway did not send the call for its journal (specs/2026-11-14-desk-journal.md JR1.6): no stop of the
			// reading and no repeat — the Job repeats the step.
			return "", usage, attempt{}, &disclosure.JournalError{Code: code}
		}
		if stop, unreachable, refused := stopOf(raw); stop != nil {
			return "", usage, attempt{}, stop
		} else if refused {
			return "", usage, attempt{}, fmt.Errorf("model API status %d: %w", resp.StatusCode, sagamemory.ErrRefused)
		} else if unreachable {
			return "", usage, attempt{retry: true, unreachable: true, wait: RetryAfter(resp.Header)},
				fmt.Errorf("model API status %d: the provider could not be reached", resp.StatusCode)
		}
		return "", usage, attempt{retry: repeatsInApp(resp.StatusCode), wait: RetryAfter(resp.Header), checked: checked}, fmt.Errorf("model API status %d", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		outcome, err := refused(resp)
		return "", usage, outcome, err
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
					Code    string `json:"code"`
					Reason  string `json:"reason"`
					Message string `json:"message"`
				} `json:"error"`
				Usage             *responseUsage `json:"usage"`
				IncompleteDetails *struct {
					Reason string `json:"reason"`
				} `json:"incomplete_details"`
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
		case "response.completed", "response.incomplete":
			completed = true
			if spent := event.Response.Usage; spent != nil {
				usage.InputTokens, usage.CachedInputTokens = spent.InputTokens, spent.InputTokensDetails.CachedTokens
				usage.OutputTokens, usage.ReasoningOutputTokens = spent.OutputTokens, spent.OutputTokensDetails.ReasoningTokens
			}
			if event.Type == "response.incomplete" {
				cut := &IncompleteError{Reason: "unknown"}
				if details := event.Response.IncompleteDetails; details != nil && details.Reason != "" {
					cut.Reason = details.Reason
				}
				return "", usage, attempt{}, cut
			}
		case "response.failed", "error":
			message := "response failed"
			if failure := event.Response.Error; failure != nil {
				message = failure.Message
				// A provider of Chat Completions says its refusal in the stream: the gateway of the app names its stop there
				// (specs/2026-11-13-desk-consent.md CN3.4).
				if client.config.Stops {
					raw, _ := json.Marshal(gatewayRefusal{Error: struct {
						Code   string `json:"code"`
						Reason string `json:"reason"`
					}{Code: failure.Code, Reason: failure.Reason}})
					if stop, unreachable, refused := stopOf(raw); stop != nil {
						return "", usage, attempt{}, stop
					} else if refused {
						return "", usage, attempt{}, fmt.Errorf("%s: %w", message, sagamemory.ErrRefused)
					} else if unreachable {
						return "", usage, attempt{retry: true, unreachable: true}, errors.New(message)
					}
				}
			}
			return "", usage, attempt{retry: true, checked: checked}, errors.New(message)
		}
	}
	if err := scanner.Err(); err != nil {
		outcome := attempt{retry: true, checked: checked}
		return "", usage, outcome, client.timedOut(call, ctx, &outcome, err)
	}
	if !completed {
		outcome := attempt{retry: true, checked: checked}
		return "", usage, outcome, client.timedOut(call, ctx, &outcome, errors.New("model API stream ended before completion"))
	}
	return text.String(), usage, attempt{}, nil
}
