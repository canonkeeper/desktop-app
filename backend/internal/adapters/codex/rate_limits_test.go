package codex

import (
	"testing"
	"time"

	"github.com/openai/codex/sdk/go/protocol"
)

// R9.1: the weekly window is the reading; without it the primary one is.
func TestLimitSampleReadsWeeklyWindow(t *testing.T) {
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	resets := now.Add(72 * time.Hour).Unix()
	sample, ok := limitSample(protocol.RateLimitSnapshot{
		LimitID:   protocol.Some("codex"),
		Primary:   protocol.Some(protocol.RateLimitWindow{UsedPercent: 12, WindowDurationMins: protocol.Some(int64(300))}),
		Secondary: protocol.Some(protocol.RateLimitWindow{UsedPercent: 50, WindowDurationMins: protocol.Some(int64(10080)), ResetsAt: protocol.Some(resets)}),
	}, now)
	if !ok || sample.LimitID != "codex" || sample.UsedPercent != 50 || sample.WindowMinutes != 10080 ||
		sample.ResetsAt == nil || sample.ResetsAt.Unix() != resets {
		t.Fatalf("sample = %+v, %v", sample, ok)
	}
	primary, ok := limitSample(protocol.RateLimitSnapshot{Primary: protocol.Some(protocol.RateLimitWindow{UsedPercent: 7})}, now)
	if !ok || primary.UsedPercent != 7 || primary.ResetsAt != nil {
		t.Fatalf("primary sample = %+v, %v", primary, ok)
	}
	if _, ok := limitSample(protocol.RateLimitSnapshot{}, now); ok {
		t.Fatal("a snapshot without windows was read")
	}
}
