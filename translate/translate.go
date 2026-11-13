// SPDX-License-Identifier: MIT

// Package translate is the face of the module for the local gateway of the
// CanonKeeper desktop app. A Responses request goes to the author's provider
// in the provider's protocol, Responses as it is or Chat Completions, and comes
// back as Responses events and a result. How the request is translated lives
// in internal/ and is not part of the face.
package translate

import (
	"net/http"

	"github.com/canonkeeper/desktop-app/translate/internal/provider"
)

type (
	// Translator sends requests through one HTTP client; it is safe for
	// concurrent use. OpenStream and Execute are its two entries.
	Translator = provider.HTTPAdapter
	// Target is where one request goes: BaseURL (an http or https address of
	// the provider's API, local and private ones included), Credential (the
	// provider's key), Model (the provider's model, empty keeps the model of
	// the request), Headers (sent beside the module's own) and the profile.
	Target = provider.Target
	// Capabilities is the profile of the provider: its protocol, what it takes
	// and what quirks it has.
	Capabilities = provider.Capabilities
	// Protocol is the language of the provider.
	Protocol = provider.Protocol
	// Request is a Responses request body as the client sent it.
	Request = provider.Request
	// Event is a Responses event: its type and its JSON.
	Event = provider.Event
	// Result is the outcome of a request: the terminal response, the usage and
	// whether it is known, and how the response ended.
	Result = provider.Result
	// Usage counts the tokens of a response.
	Usage = provider.Usage
	// Error is a typed failure of the module; see the ErrorCode constants.
	Error = provider.Error
)

const (
	ProtocolResponses       = provider.ProtocolResponses
	ProtocolChatCompletions = provider.ProtocolChatCompletions
)

// Codes of Error. A refusal of the provider carries the provider's own code
// (or http_<status>) with Status, Body and RetryAfter; a cancelled call is an
// error for which errors.Is(err, context.Canceled) or
// errors.Is(err, context.DeadlineExceeded) holds.
const (
	ErrorCodeInvalidRequest        = provider.ErrorCodeInvalidRequest
	ErrorCodeRequestTooLarge       = provider.ErrorCodeRequestTooLarge
	ErrorCodeInvalidConfiguration  = provider.ErrorCodeInvalidConfiguration
	ErrorCodeUnsupportedCapability = provider.ErrorCodeUnsupportedCapability
	ErrorCodeUpstreamError         = provider.ErrorCodeUpstreamError
	ErrorCodeInsufficientQuota     = provider.ErrorCodeInsufficientQuota
	ErrorCodeRateLimited           = provider.ErrorCodeRateLimited
	ErrorCodeConnection            = provider.ErrorCodeConnection
	ErrorCodeStreamIncomplete      = provider.ErrorCodeStreamIncomplete
	ErrorCodeInvalidStream         = provider.ErrorCodeInvalidStream
	ErrorCodeInvalidToolCall       = provider.ErrorCodeInvalidToolCall
)

// New makes a Translator on client (http.DefaultClient's settings when nil).
// The translator never follows a redirect of the provider.
func New(client *http.Client) *Translator {
	return provider.New(client)
}
