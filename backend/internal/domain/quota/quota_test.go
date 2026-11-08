package quota

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

// R1.2: days are Moscow calendar days; counters reset at 00:00 MSK.
func TestMoscowDayAndMidnight(t *testing.T) {
	beforeMidnight := time.Date(2026, 10, 28, 20, 59, 0, 0, time.UTC) // 23:59 MSK
	afterMidnight := time.Date(2026, 10, 28, 21, 1, 0, 0, time.UTC)   // 00:01 MSK next day
	if got := MoscowDay(beforeMidnight); !got.Equal(time.Date(2026, 10, 28, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("MoscowDay(23:59 MSK) = %v", got)
	}
	if got := MoscowDay(afterMidnight); !got.Equal(time.Date(2026, 10, 29, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("MoscowDay(00:01 MSK) = %v", got)
	}
	if got := NextMoscowMidnight(beforeMidnight); !got.Equal(time.Date(2026, 10, 28, 21, 0, 0, 0, time.UTC)) {
		t.Fatalf("NextMoscowMidnight(23:59 MSK) = %v", got)
	}
	if got := NextMoscowMidnight(afterMidnight); !got.Equal(time.Date(2026, 10, 29, 21, 0, 0, 0, time.UTC)) {
		t.Fatalf("NextMoscowMidnight(00:01 MSK) = %v", got)
	}
}

// R1.6: problem codes of daily limits name what they count; the rest repeat the key.
func TestCodesAndKeys(t *testing.T) {
	want := map[Key]string{
		KeyChatTurnsPerDay: "quota.chat_turns_daily", KeyImportsPerDay: "quota.imports_daily",
		KeyActiveTurns: "quota.active_turns", KeyProjects: "quota.projects",
		KeyProjectBooks: "quota.project_books", KeyAccountCharacters: "quota.account_characters",
	}
	for key, code := range want {
		if Code(key) != code {
			t.Fatalf("Code(%s) = %s, want %s", key, Code(key), code)
		}
	}
	defaults := Defaults()
	for _, key := range Keys() {
		if parsed, ok := ParseKey(string(key)); !ok || parsed != key || defaults.Value(key) <= 0 {
			t.Fatalf("key %s: parsed %q %v, default %d", key, parsed, ok, defaults.Value(key))
		}
		if defaults.With(key, 7).Value(key) != 7 {
			t.Fatalf("With(%s) did not change the value", key)
		}
	}
	if _, ok := ParseKey("chat_turns"); ok {
		t.Fatal("unknown key parsed")
	}
	wrapped := fmt.Errorf("wrapped: %w", DailyExceeded(KeyChatTurnsPerDay, 30, time.Now()))
	exceeded, ok := AsExceeded(wrapped)
	if !ok || exceeded.Code() != "quota.chat_turns_daily" || exceeded.ResetsAt == nil {
		t.Fatalf("AsExceeded = %#v %v", exceeded, ok)
	}
	if _, ok := AsExceeded(errors.New("other")); ok {
		t.Fatal("plain error read as quota refusal")
	}
}
