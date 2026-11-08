package modelapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"canonkeeper/backend/internal/domain/quota"
)

// visionCallTimeout bounds one look at a drawing (specs/2026-10-29-saga-illustrations.md R12.2).
const visionCallTimeout = 120 * time.Second

// visionEffort is the reasoning effort of the drawing check.
const visionEffort = "medium"

// VisionPort asks the check model to look at one image and answer in text, with no tools: the
// self-check of a drawing (R12.2, R13.11).
type VisionPort struct {
	client  *Client
	model   string
	timeout time.Duration
}

// Vision is the port of the drawing check; it calls the check model, as writers wait for it in a
// chat turn it takes a place of the gate before chapter cards.
func (client *Client) Vision() VisionPort {
	return VisionPort{client: client, model: client.config.Check(), timeout: visionCallTimeout}
}

type visionRequestBody struct {
	Model        string              `json:"model"`
	Instructions string              `json:"instructions"`
	Input        []imageInputMessage `json:"input"`
	Reasoning    map[string]any      `json:"reasoning"`
	Stream       bool                `json:"stream"`
	Store        bool                `json:"store"`
}

// Look sends the instructions, the text and then the image as a data URL, and returns the
// streamed text and the spend. It makes one HTTP attempt within 120 s: the caller decides whether
// to ask again.
func (port VisionPort) Look(ctx context.Context, instructions, prompt string, image []byte) (string, quota.ModelUsage, error) {
	if port.client == nil || !port.client.connected() || strings.TrimSpace(prompt) == "" || len(image) == 0 {
		return "", quota.ModelUsage{}, ErrNotConfigured
	}
	payload, err := json.Marshal(visionRequestBody{
		Model: port.model, Instructions: instructions, Stream: true, Store: false,
		Reasoning: map[string]any{"effort": visionEffort},
		Input: []imageInputMessage{{Type: "message", Role: "user", Content: []imageInputContent{
			{Type: "input_text", Text: prompt}, {Type: "input_image", ImageURL: dataURL(image)},
		}}},
	})
	if err != nil {
		return "", quota.ModelUsage{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, port.timeout)
	defer cancel()
	if err := port.client.gate.acquire(ctx, Interactive); err != nil {
		return "", quota.ModelUsage{}, err
	}
	defer port.client.gate.release()
	text, usage, _, err := port.client.once(ctx, payload)
	return text, usage, err
}

// dataURL embeds an image file in a Responses request by its own type.
func dataURL(image []byte) string {
	mime := http.DetectContentType(image)
	if mime != "image/png" && mime != "image/jpeg" && mime != "image/webp" {
		mime = "image/png"
	}
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(image)
}

// imageInputMessage and imageInputContent are a user message of the Responses API with text and
// images: the drawing check sends the render this way (vision.go).
type imageInputMessage struct {
	Type    string              `json:"type"`
	Role    string              `json:"role"`
	Content []imageInputContent `json:"content"`
}

type imageInputContent struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	ImageURL string `json:"image_url,omitempty"`
}
