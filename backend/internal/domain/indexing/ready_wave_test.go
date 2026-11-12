package indexing

import (
	"testing"
	"time"
)

func TestRemainingSecondsNeedsThreeRecentCards(t *testing.T) {
	if _, ok := RemainingSeconds(2, 40*time.Second, 18); ok {
		t.Fatal("two cards gave an estimate")
	}
	if _, ok := RemainingSeconds(15, 240*time.Second, 0); ok {
		t.Fatal("nothing left gave an estimate")
	}
	if _, ok := RemainingSeconds(15, 0, 5); ok {
		t.Fatal("no time gave an estimate")
	}
}

func TestRemainingSecondsFollowsThePaceOfTheCards(t *testing.T) {
	seconds, ok := RemainingSeconds(15, 240*time.Second, 5)
	if !ok || seconds != 80 {
		t.Fatalf("15 cards in 240 s with 5 left = %d, %t, want 80", seconds, ok)
	}
	// A fraction of a second rounds up: the page never promises less than is left.
	seconds, ok = RemainingSeconds(3, 10*time.Second, 1)
	if !ok || seconds != 4 {
		t.Fatalf("3 cards in 10 s with 1 left = %d, %t, want 4", seconds, ok)
	}
}

func TestAWaveIsLongAfterTwoMinutes(t *testing.T) {
	started := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	if (ReadyWave{StartedAt: started, ReadyAt: started.Add(2 * time.Minute)}).Long() {
		t.Fatal("exactly two minutes is long")
	}
	if !(ReadyWave{StartedAt: started, ReadyAt: started.Add(2*time.Minute + time.Second)}).Long() {
		t.Fatal("two minutes and a second is not long")
	}
}

// FS5.2: the pace is measured over the wave so far, at most over its last window.
func TestThePaceIsMeasuredOverTheLastWindowOfTheWave(t *testing.T) {
	started := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	if span := PaceSpan(started, started.Add(90*time.Second)); span != 90*time.Second {
		t.Fatalf("a young wave is paced over %s", span)
	}
	if span := PaceSpan(started, started.Add(time.Hour)); span != PaceWindow {
		t.Fatalf("a long wave is paced over %s", span)
	}
	if span := PaceSpan(started, started.Add(-time.Second)); span != 0 {
		t.Fatalf("a wave that starts later is paced over %s", span)
	}
}
