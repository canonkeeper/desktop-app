// SPDX-License-Identifier: MIT

package provider

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestInvalidTerminalIsNotEmittedAsSuccessfulCompletion(t *testing.T) {
	for _, raw := range []string{
		`{"type":"response.completed","response":{"id":"r","status":"completed","usage":{"input_tokens":1}}}`,
		`{"type":"response.completed","response":{"id":"r","status":"failed","usage":{"input_tokens":1,"output_tokens":0}}}`,
		`{"type":"response.completed","response":{"status":"completed","usage":{"input_tokens":1,"output_tokens":0}}}`,
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: " + raw + "\n\n"))
		}))
		adapter := New(server.Client())
		emitted := 0
		_, err := adapter.OpenStream(context.Background(), Target{BaseURL: server.URL, Capabilities: responsesCapabilities()}, Request{Body: json.RawMessage(`{"model":"gpt-6-sol","input":"hello","stream":true}`)}, func(Event) error { emitted++; return nil })
		adapter.Close()
		server.Close()
		var failure *Error
		if !errors.As(err, &failure) || emitted != 0 {
			t.Fatalf("invalid terminal emitted: %s count=%d err=%v", raw, emitted, err)
		}
	}
}
