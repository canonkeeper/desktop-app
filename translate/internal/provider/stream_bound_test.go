// SPDX-License-Identifier: MIT

package provider

import (
	"context"
	"errors"
	"net/http"
	"testing"
)

// NET-18: a long healthy response with more than 100 000 small events is not
// cut off after execution; streams are bounded by size and time.
func TestLongResponseWithManySmallEventsCompletes(t *testing.T) {
	const events = 150000
	adapter := New(&http.Client{Transport: memoryStreamTransport{benchmarkSSEStream(events)}})
	defer adapter.Close()
	deltas := 0
	result, err := adapter.OpenStream(context.Background(), testTarget("https://upstream.invalid", responsesCapabilities()), Request{Body: []byte(`{"model":"m","input":"hi","stream":true}`)}, func(e Event) error {
		if e.Type == "response.output_text.delta" {
			deltas++
		}
		return nil
	})
	if err != nil || !result.UsageKnown || deltas != events {
		t.Fatalf("long response cut off: deltas=%d %+v %v", deltas, result, err)
	}
}

type endlessSSELine struct{ read int }

func (r *endlessSSELine) Read(dst []byte) (int, error) {
	for i := range dst {
		dst[i] = 'a'
	}
	r.read += len(dst)
	return len(dst), nil
}

func TestSSELineBoundBeforeUnboundedAllocation(t *testing.T) {
	reader := new(endlessSSELine)
	err := readSSE(reader, func(string, []byte) error { return nil })
	var failure *Error
	if !errors.As(err, &failure) || failure.Code != ErrorCodeInvalidStream || reader.read > maxSSEEventBytes+64<<10 {
		t.Fatalf("unbounded SSE read: bytes=%d error=%v", reader.read, err)
	}
}
