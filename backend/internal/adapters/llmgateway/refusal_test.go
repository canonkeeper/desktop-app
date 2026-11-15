// SPDX-License-Identifier: AGPL-3.0-only

package llmgateway

import (
	"net/http"
	"strings"
	"testing"

	"canonkeeper/backend/internal/adapters/modelapi"
	"canonkeeper/backend/internal/domain/aisettings"
	"canonkeeper/backend/internal/domain/coresteps"

	"github.com/canonkeeper/desktop-app/translate"
)

// specs/2026-11-13-desk-consent.md CN3.4: a refusal of a provider of Chat Completions — which the module of the protocols
// says in the stream as response.failed — stops the reading as a refusal before the stream does: the check hears it with its
// kind, and the event names the reason of the stop for the client of the app.
func TestARefusalOfTheProviderNamesItsStop(t *testing.T) {
	for status, want := range map[int]struct {
		reason  string
		failure aisettings.Failure
	}{
		http.StatusPaymentRequired: {"funds", aisettings.FailureFunds}, http.StatusUnauthorized: {"key", aisettings.FailureKey},
	} {
		stand := openStand(t)
		stub := &provider{chat: true, status: status}
		server := startProvider(t, stub)
		stand.settings.set(t, custom(server, "chat"), "key-test-refusal-0001")
		reading := &readingOfTheTest{}
		stand.gateway.UseReading(reading)
		response, body := stand.post(t, coreBody(t, coresteps.Card), map[string]string{modelapi.JobHeader: "job-of-the-cards"})
		if !strings.Contains(body, `"reason":"`+want.reason+`"`) {
			t.Errorf("%d = %d %s", status, response.StatusCode, body)
		}
		if len(reading.failures) != 1 || reading.failures[0] != want.failure {
			t.Errorf("%d was heard as %v", status, reading.failures)
		}
		// A refusal before the answer took nothing: it is settled as not answered.
		if len(reading.answered) != 1 || reading.answered[0] {
			t.Errorf("%d was settled as answered: %v", status, reading.answered)
		}
	}
}

// GW1.5, specs/2026-11-13-desk-consent.md CN3.4: a provider of Chat Completions that refuses before its answer — the module
// says it as response.failed before response.created — is answered with its status and its Retry-After, as a provider of
// Responses is: the client waits the provider's wait at a 429 and repeats a 503 as a refusal, not as a paid answer that broke.
func TestARefusalOfAProviderOfChatCompletionsKeepsItsStatus(t *testing.T) {
	for _, refusal := range []struct {
		status int
		retry  string
	}{{http.StatusTooManyRequests, "17"}, {http.StatusServiceUnavailable, ""}} {
		stand := openStand(t)
		stub := &provider{chat: true, status: refusal.status, retry: refusal.retry}
		stand.settings.set(t, custom(startProvider(t, stub), "chat"), "key-test-refusal-0002")
		for _, body := range []string{chatBody(""), coreBody(t, coresteps.Card)} {
			response, answer := stand.post(t, body, map[string]string{modelapi.JobHeader: "job-of-the-cards"})
			if response.StatusCode != refusal.status || response.Header.Get("Retry-After") != refusal.retry ||
				strings.Contains(answer, "response.failed") {
				t.Errorf("a %d of a provider of Chat Completions = %d %q %s", refusal.status, response.StatusCode,
					response.Header.Get("Retry-After"), answer)
			}
		}
	}
}

// specs/2026-11-15-desk-reading-rest.md RR1.1: the gateway names refused only a refusal of the provider of the class
// FailureRefused — a 4xx that is no key, no funds and no model: the window of the model, a request too large — before the
// stream and in its response.failed; a wait, another refusal and a failure of the provider are named nothing.
func TestTheGatewayNamesRefusedOnlyForItsClass(t *testing.T) {
	for status, want := range map[int]string{
		http.StatusBadRequest: "refused", http.StatusRequestEntityTooLarge: "refused", http.StatusUnprocessableEntity: "refused",
		http.StatusNotFound: "", http.StatusTooManyRequests: "", http.StatusServiceUnavailable: "", http.StatusPaymentRequired: "funds",
	} {
		stand := openStand(t)
		stand.settings.set(t, custom(startProvider(t, &provider{chat: true, status: status}), "chat"), "key-test-refusal-0003")
		_, body := stand.post(t, coreBody(t, coresteps.Names), map[string]string{modelapi.JobHeader: "job-of-the-cards"})
		named := strings.Contains(body, `"reason":"refused"`)
		if want == "refused" && !named || want != "refused" && named || want == "" && strings.Contains(body, `"reason"`) {
			t.Errorf("%d = %s; want the reason %q", status, body, want)
		}
	}
	for status, want := range map[string]bool{"400": true, "413": true, "429": false, "500": false, "404": false} {
		event := withStopReason(translate.Event{Type: "response.failed", Data: []byte(
			`{"type":"response.failed","response":{"status":"failed","error":{"code":"upstream_error","message":"upstream (HTTP ` + status + `): no"}}}`)}, false)
		if named := strings.Contains(string(event.Data), `"reason":"refused"`); named != want {
			t.Errorf("response.failed of %s = %s", status, event.Data)
		}
	}
}
