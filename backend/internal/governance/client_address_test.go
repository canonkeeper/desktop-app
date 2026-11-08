package governance

import (
	"math"
	"strings"
	"testing"
)

// SEC-17 (specs/2026-10-28-security-hardening.md, R4.2): the request log names a client by an
// address or an IPv6 network in canonical form and nothing else.
func TestClientAddressAcceptsOnlyAnAddressOrNetwork(t *testing.T) {
	for value, want := range map[string]string{
		"203.0.113.7":     "203.0.113.7",
		"2001:db8::/64":   "2001:db8::/64",
		"2001:0db8::1":    "2001:db8::1",
		"198.51.100.0/24": "198.51.100.0/24",
	} {
		got, err := NewClientAddress(value)
		if err != nil || string(got) != want {
			t.Fatalf("NewClientAddress(%q) = %q, %v; want %q", value, got, err, want)
		}
	}
	for _, value := range []string{"", "localhost", "203.0.113.7:443", "203.0.113.7\nforged", "writer@example.com"} {
		if got, err := NewClientAddress(value); err == nil {
			t.Fatalf("NewClientAddress(%q) = %q, want an error", value, got)
		}
	}
}

// V0.11 (specs/2026-11-03-voice-assistant.md): the request line of a voice call names it by the gateway's id and nothing
// else.
func TestVoiceCallIDAcceptsOnlyTheGatewaysID(t *testing.T) {
	for _, value := range []string{"rtc_abc123", "rtc_u1_Zx-9"} {
		if got, err := NewVoiceCallID(value); err != nil || string(got) != value {
			t.Fatalf("NewVoiceCallID(%q) = %q, %v", value, got, err)
		}
	}
	for _, value := range []string{"", "rtc_", "abc", "rtc_a b", "rtc_a\nforged", "rtc_" + strings.Repeat("a", 129), "203.0.113.7"} {
		if got, err := NewVoiceCallID(value); err == nil {
			t.Fatalf("NewVoiceCallID(%q) = %q, want an error", value, got)
		}
	}
}

// A measure in the request log is a finite number not below zero.
func TestMeasureIsFiniteAndNotNegative(t *testing.T) {
	for value, want := range map[float64]Measure{61.3: 61.3, 0: 0, -1: 0, math.Inf(1): 0, math.NaN(): 0} {
		if got := NewMeasure(value); got != want {
			t.Fatalf("NewMeasure(%v) = %v, want %v", value, got, want)
		}
	}
}
