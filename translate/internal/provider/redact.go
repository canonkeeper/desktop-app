// SPDX-License-Identifier: MIT

package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/canonkeeper/desktop-app/translate/internal/wirejson"
)

// The text of a failure leaves the module without the provider's key, even
// when the provider echoes it, and at most maxRefusalBodyBytes long: the text
// of an Error, the ErrorMessage of a result and the error message of a failed
// response and of its response.failed event. The key is replaced before the
// text is cut, so no part of it stays at the cut. Nothing else is touched:
// the events and the answer come as the provider or the translation made
// them, so a placeholder key of a local server ("x", "ollama") cannot change
// them.

const redactedKey = "[provider key]"

// maxRefusalBodyBytes bounds the provider text an error carries.
const maxRefusalBodyBytes = 4 << 10

// failureText cleans the text of a failure of one call.
type failureText struct{ key string }

// text is s without the key, cut to its bound.
func (f failureText) text(s string) string {
	if f.key != "" {
		s = strings.ReplaceAll(s, f.key, redactedKey)
	}
	return truncateText(s, maxRefusalBodyBytes)
}

// outcome is the result and the error of a call as they leave the module.
func (f failureText) outcome(result Result, err error) (Result, error) {
	result.ErrorMessage = f.text(result.ErrorMessage)
	if result.Failed {
		result.Response = f.responseError(result.Response)
	}
	return result, f.error(err)
}

// error is a new Error with the texts of the module's one cleaned; a
// cancellation it came with stays visible to errors.Is. The error is built
// anew, so no text of the old one survives in a wrapper.
func (f failureText) error(err error) error {
	var failure *Error
	if !errors.As(err, &failure) {
		return err
	}
	clean := *failure
	clean.Message, clean.Body = f.text(failure.Message), f.text(failure.Body)
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded} {
		if errors.Is(err, cause) {
			return fmt.Errorf("%w: %w", cause, &clean)
		}
	}
	return &clean
}

// emit hands every event to the caller, a response.failed one with the error
// message of its response cleaned.
func (f failureText) emit(emit func(Event) error) func(Event) error {
	return func(event Event) error {
		if event.Type == "response.failed" {
			event.Data = withMember(event.Data, "response", f.responseError)
		}
		return emit(event)
	}
}

// responseError is a Responses or Chat object with the message of its error
// cleaned.
func (f failureText) responseError(object json.RawMessage) json.RawMessage {
	return withMember(object, "error", func(failure json.RawMessage) json.RawMessage {
		return withMember(failure, "message", func(message json.RawMessage) json.RawMessage {
			var text string
			if json.Unmarshal(message, &text) != nil {
				return message
			}
			if cleaned := f.text(text); cleaned != text {
				return mustJSON(cleaned)
			}
			return message
		})
	})
}

// withMember is the JSON object with its member name changed by change; it
// comes back as it was when it has no such member or change leaves it.
func withMember(object json.RawMessage, name string, change func(json.RawMessage) json.RawMessage) json.RawMessage {
	var fields map[string]json.RawMessage
	if json.Unmarshal(object, &fields) != nil || fields[name] == nil {
		return object
	}
	changed := change(fields[name])
	if bytes.Equal(changed, fields[name]) {
		return object
	}
	fields[name] = changed
	return wirejson.EncodeObject(fields)
}

// truncateText cuts text to at most limit bytes at a character boundary.
func truncateText(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut]
}
