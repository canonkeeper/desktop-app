// SPDX-License-Identifier: AGPL-3.0-only

package llmgateway

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"canonkeeper/backend/internal/domain/aisettings"
	"canonkeeper/backend/internal/domain/modeljournal"

	"github.com/canonkeeper/desktop-app/translate"
)

// probeTime bounds the probe of an address.
const probeTime = 30 * time.Second

// protocols is «Определить» (GW1.3): the protocol of an address of the author is found once, by a probe without his text
// — Responses first, Chat Completions when the provider has no such path — and kept until the address changes.
type protocols struct {
	mu     sync.Mutex
	client *http.Client
	known  map[string]translate.Protocol
}

func newProtocols(client *http.Client) *protocols {
	owned := *client
	owned.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &protocols{client: &owned, known: map[string]translate.Protocol{}}
}

// resolve is the protocol of the profile: the one the author or the variant chose, or the one the probe found; the probe
// goes into the journal — its record through sent before it goes, which names the number of its row, its row through probed
// after (specs/2026-11-14-desk-journal.md JR1.7). A record that did not lie sends no probe. A probe that could not reach the
// provider is not kept.
func (protocols *protocols) resolve(ctx context.Context, own aisettings.Own, key string, sent func(bytes int64) (string, error), probed func(modeljournal.Request)) (translate.Protocol, error) {
	switch own.Protocol {
	case aisettings.ProtocolResponses:
		return translate.ProtocolResponses, nil
	case aisettings.ProtocolChat:
		return translate.ProtocolChatCompletions, nil
	}
	protocols.mu.Lock()
	defer protocols.mu.Unlock()
	if protocol, ok := protocols.known[own.BaseURL]; ok {
		return protocol, nil
	}
	id, err := sent(int64(len(probeBody)))
	if err != nil {
		return "", err
	}
	started := time.Now()
	protocol, err := protocols.probe(ctx, own.BaseURL, key)
	outcome := modeljournal.OutcomeCompleted
	if err != nil {
		outcome = modeljournal.OutcomeFailed
	}
	probed(modeljournal.Request{ID: id, At: started, Purpose: modeljournal.PurposeProbe, RequestBytes: int64(len(probeBody)), Outcome: outcome, Duration: time.Since(started)})
	if err != nil {
		return "", err
	}
	protocols.known = map[string]translate.Protocol{own.BaseURL: protocol}
	return protocol, nil
}

// probeBody asks nothing of the model: a provider of Responses refuses it as a request, one without the path says so.
var probeBody = []byte(`{}`)

// probe asks the address for /responses with an empty request: «no such path» (404, 405) is Chat Completions, any
// other answer of the provider is Responses. A provider that fails or cannot be reached tells nothing, nor does one that
// refuses the key or the rate (401, 403, 429) before it looks at the path: its refusal goes to the client as it came.
func (protocols *protocols) probe(ctx context.Context, baseURL, key string) (translate.Protocol, error) {
	ctx, cancel := context.WithTimeout(ctx, probeTime)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSuffix(baseURL, "/")+"/responses", bytes.NewReader(probeBody))
	if err != nil {
		return "", &translate.Error{Code: translate.ErrorCodeInvalidConfiguration, Message: "the address of the provider is not valid"}
	}
	request.Header.Set("Content-Type", "application/json")
	if key != "" {
		request.Header.Set("Authorization", "Bearer "+key)
	}
	response, err := protocols.client.Do(request)
	if err != nil {
		if ctx.Err() != nil && errors.Is(err, context.Canceled) {
			return "", err
		}
		return "", &translate.Error{Code: translate.ErrorCodeConnection, Message: "the provider could not be reached"}
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
	_ = response.Body.Close()
	switch {
	case response.StatusCode == http.StatusNotFound || response.StatusCode == http.StatusMethodNotAllowed:
		return translate.ProtocolChatCompletions, nil
	case response.StatusCode >= 500:
		return "", &translate.Error{Code: translate.ErrorCodeUpstreamError, Status: response.StatusCode, Message: "the provider failed the probe of its protocol"}
	case response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden || response.StatusCode == http.StatusTooManyRequests:
		return "", &translate.Error{
			Code: fmt.Sprintf("http_%d", response.StatusCode), Status: response.StatusCode, RetryAfter: response.Header.Get("Retry-After"),
			Message: "the provider refused the probe of its protocol before its path",
		}
	default:
		return translate.ProtocolResponses, nil
	}
}
