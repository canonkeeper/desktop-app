package observability

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// SEC-17 (specs/2026-10-28-security-hardening.md, R4.1, R4.2): a request line names the signed-in
// user or the client address its handlers reported; an invalid value is not written.
func TestRequestLoggerWritesTheReportedUserAndClient(t *testing.T) {
	const userID = "0b4c2f2e-2d6c-4f43-9f5d-8b8f3c1a2e11"
	cases := []struct {
		name   string
		report func(*http.Request)
		want   map[string]any
	}{
		{name: "signed in", report: func(r *http.Request) { ReportRequestUser(r.Context(), userID) }, want: map[string]any{"userId": userID}},
		{name: "anonymous", report: func(r *http.Request) { ReportRequestClient(r.Context(), "203.0.113.7") }, want: map[string]any{"client": "203.0.113.7"}},
		{name: "signed in now", report: func(r *http.Request) {
			ReportRequestClient(r.Context(), "2001:db8::/64")
			ReportRequestUser(r.Context(), userID)
		}, want: map[string]any{"client": "2001:db8::/64", "userId": userID}},
		{name: "invalid", report: func(r *http.Request) {
			ReportRequestUser(r.Context(), "user-1")
			ReportRequestClient(r.Context(), "writer@example.com")
		}, want: map[string]any{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var output bytes.Buffer
			handler := RequestLogger(&output, RequestLoggerOptions{
				RouteInfo: func(*http.Request) RouteInfo {
					return RouteInfo{OperationID: "loginUser", PathTemplate: "/auth/login"}
				},
			})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				tc.report(r)
				w.WriteHeader(http.StatusOK)
			}))
			handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/auth/login", nil))

			var entry map[string]any
			if err := json.Unmarshal(output.Bytes(), &entry); err != nil {
				t.Fatalf("log entry is not JSON: %v\n%s", err, output.String())
			}
			for _, key := range []string{"userId", "client"} {
				if entry[key] != tc.want[key] {
					t.Fatalf("%s = %v, want %v; entry=%v", key, entry[key], tc.want[key], entry)
				}
			}
		})
	}
}

// V0.11 (specs/2026-11-03-voice-assistant.md): the request line of a voice call, written when the call ends, carries the
// gateway's id of the call and the numbers of the quality of its sound — nothing else; an id that is not the gateway's
// writes neither.
func TestRequestLoggerWritesTheQualityOfAVoiceCall(t *testing.T) {
	quality := VoiceCallQuality{PacketsReceived: 7297, PacketsLost: 158, ConcealedSamples: 248120, TotalSamplesReceived: 7475520,
		JitterBufferDelay: 2742.7, JitterBufferEmittedCount: 6989760, BitrateKbps: 61.3, RoundTripTime: 0.116}
	for name, callID := range map[string]string{"call": "rtc_u1_abc", "not the gateway's": "203.0.113.7"} {
		t.Run(name, func(t *testing.T) {
			var output bytes.Buffer
			handler := RequestLogger(&output, RequestLoggerOptions{
				RouteInfo: func(*http.Request) RouteInfo {
					return RouteInfo{OperationID: "streamVoiceCall", PathTemplate: "/projects/{projectId}/voice/call"}
				},
			})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				ReportVoiceCall(r.Context(), callID, quality)
				w.WriteHeader(http.StatusOK)
			}))
			handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/projects/p/voice/call", nil))
			var entry map[string]any
			if err := json.Unmarshal(output.Bytes(), &entry); err != nil {
				t.Fatalf("log entry is not JSON: %v\n%s", err, output.String())
			}
			voice, written := entry["voiceCall"].(map[string]any)
			if callID != "rtc_u1_abc" {
				if written {
					t.Fatalf("voiceCall written for %q: %v", callID, entry)
				}
				return
			}
			want := map[string]any{"callId": "rtc_u1_abc", "packetsReceived": float64(7297), "packetsLost": float64(158),
				"concealedSamples": float64(248120), "totalSamplesReceived": float64(7475520), "jitterBufferDelay": 2742.7,
				"jitterBufferEmittedCount": float64(6989760), "bitrateKbps": 61.3, "roundTripTime": 0.116}
			if !written || len(voice) != len(want) {
				t.Fatalf("voiceCall = %v, want %v", entry["voiceCall"], want)
			}
			for key, value := range want {
				if voice[key] != value {
					t.Fatalf("voiceCall.%s = %v, want %v", key, voice[key], value)
				}
			}
		})
	}
}
