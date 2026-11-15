// SPDX-License-Identifier: AGPL-3.0-only

package llmgateway

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"canonkeeper/backend/internal/domain/aisettings"
)

// modelsTime bounds the question about the models of the provider; maxModelsBody bounds its answer, maxRefusalBody what
// is read of a refusal.
const (
	modelsTime     = 20 * time.Second
	maxModelsBody  = 4 << 20
	maxRefusalBody = 64 << 10
)

// Models asks the provider of the saved settings for its models (specs/2026-11-13-desk-probes.md PB1.1): GET {address}/models
// with the key of the author — a local server gets none — at that address alone, without following a redirect. The answer
// is the names, that the provider gives none (404, 405), or why it failed; never its words. Nothing is kept and nothing goes
// into the journal: no model is asked.
func (gateway *Gateway) Models(ctx context.Context) aisettings.Models {
	failed := func(reason aisettings.Reason) aisettings.Models {
		return aisettings.Models{Outcome: aisettings.ModelsFailed, Reason: reason}
	}
	own := gateway.settings.AI().Own
	if own == nil {
		return failed(aisettings.ReasonOther)
	}
	key, _ := gateway.settings.Key()
	if own.Preset == aisettings.PresetOllama {
		key = ""
	}
	ctx, cancel := context.WithTimeout(ctx, modelsTime)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(own.BaseURL, "/")+"/models", nil)
	if err != nil {
		return failed(aisettings.ReasonOther)
	}
	request.Header.Set("Accept", "application/json")
	if key != "" {
		request.Header.Set("Authorization", "Bearer "+key)
	}
	response, err := gateway.protocols.client.Do(request)
	if err != nil {
		return failed(aisettings.ReasonUnreachable)
	}
	defer response.Body.Close()
	switch status := response.StatusCode; {
	case status == http.StatusNotFound || status == http.StatusMethodNotAllowed:
		return aisettings.Models{Outcome: aisettings.ModelsUnlisted}
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return failed(aisettings.ReasonKeyRefused)
	case status == http.StatusTooManyRequests && waits(status, refusalCode(response.Body)):
		// An exhausted quota is no wait: it is refused below as another failure.
		return failed(aisettings.ReasonRateLimited)
	case status < 200 || status > 299:
		return failed(aisettings.ReasonOther)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxModelsBody+1))
	if errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil {
		return failed(aisettings.ReasonUnreachable)
	}
	if err != nil || len(raw) > maxModelsBody {
		return failed(aisettings.ReasonOther)
	}
	// OpenAI and the servers like it list data[].id; some name their models[] by id or name.
	var page struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
		Models []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"models"`
	}
	if json.Unmarshal(raw, &page) != nil || page.Data == nil && page.Models == nil {
		return failed(aisettings.ReasonOther)
	}
	names := make([]string, 0, len(page.Data)+len(page.Models))
	for _, entry := range page.Data {
		names = append(names, entry.ID)
	}
	for _, entry := range page.Models {
		name := entry.ID
		if name == "" {
			name = entry.Name
		}
		names = append(names, name)
	}
	return aisettings.ListedModels(names)
}

// refusalCode is the code of a refusal of the provider as OpenAI and the servers like it write it — error.code, else
// error.type; only its kind is read, never its words.
func refusalCode(body io.Reader) string {
	var refusal struct {
		Error struct {
			Code any    `json:"code"`
			Type string `json:"type"`
		} `json:"error"`
	}
	if json.NewDecoder(io.LimitReader(body, maxRefusalBody)).Decode(&refusal) != nil {
		return ""
	}
	if code, ok := refusal.Error.Code.(string); ok && code != "" {
		return code
	}
	return refusal.Error.Type
}
