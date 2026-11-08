package governance

import (
	"encoding/json"
	"testing"
	"time"
)

func TestSafeFieldWrappersAreBounded(t *testing.T) {
	if _, err := NewRequestID("01234567-89ab-4cde-8fab-0123456789ab"); err != nil {
		t.Fatalf("NewRequestID() error = %v", err)
	}
	for _, value := range []string{
		"", " contains-space", string(make([]byte, 129)),
		"not-a-request-id", "still-not-a-request-id",
		"РУКОПИСЬБЕЗПРОБЕЛОВ", "01234567-89AB-4CDE-8FAB-0123456789AB",
	} {
		if _, err := NewRequestID(value); err == nil {
			t.Fatalf("NewRequestID(%q) error = nil", value)
		}
	}
	if _, err := NewDurationMillis(24*time.Hour + time.Millisecond); err == nil {
		t.Fatal("unbounded DurationMillis accepted")
	}
	if _, err := NewDurationMillis(5 * time.Second); err != nil {
		t.Fatalf("NewDurationMillis() error = %v", err)
	}
}

func TestRequestErrorCodeAcceptsOnlyStaticStageCodes(t *testing.T) {
	for _, value := range []string{"timeline.history_list_failed", "runtime.unavailable", "a.b2.c_3"} {
		if cause, err := NewRequestErrorCode(value); err != nil || string(cause) != value {
			t.Fatalf("NewRequestErrorCode(%q) = %q, %v", value, cause, err)
		}
	}
	for _, value := range []string{
		"", "unavailable", "timeline.", ".timeline", "timeline..read", "timeline.Read", "timeline.2read",
		"timeline.read failed", "timeline.read-failed", "timeline.ошибка", "timeline." + string(make([]byte, 64)),
	} {
		if _, err := NewRequestErrorCode(value); err == nil {
			t.Fatalf("NewRequestErrorCode(%q) error = nil", value)
		}
	}
}

func TestDigestJSONRoundTripIsClosed(t *testing.T) {
	want := Digest{1, 2, 3}
	raw, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	var got Digest
	if err := json.Unmarshal(raw, &got); err != nil || got != want {
		t.Fatalf("digest = %x, err = %v", got, err)
	}
	for _, invalid := range []string{`null`, `"short"`, `[]`} {
		if err := json.Unmarshal([]byte(invalid), &got); err == nil {
			t.Fatalf("invalid digest accepted: %s", invalid)
		}
	}
}
