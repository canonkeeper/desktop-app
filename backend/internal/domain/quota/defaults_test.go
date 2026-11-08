package quota

import (
	"errors"
	"math"
	"testing"
)

func number(value int64) *int64 { return &value }

// specs/2026-10-29-admin-panel.md R7.2: a writer's own limit, else the platform default, else the
// value of the code.
func TestResolveTakesOwnThenPlatformThenCode(t *testing.T) {
	limits := Resolve(
		Settings{KeyChatTurnsPerDay: 60, KeyProjects: 0},
		Settings{KeyChatTurnsPerDay: 40, KeyActiveTurns: 4, KeyAccountCharacters: 30_000_000, KeyTrialDrawings: 9},
	)
	want := Defaults()
	want.ChatTurnsPerDay, want.Projects, want.ActiveTurns, want.AccountCharacters = 60, 0, 4, 30_000_000
	if limits != want {
		t.Fatalf("Resolve = %+v, want %+v", limits, want)
	}
	if Resolve(nil, nil) != Defaults() {
		t.Fatal("no own and no platform value must be the code's defaults")
	}
}

// R7.4, R7.1: the platform defaults are every writer limit in the order of Keys, then the trial
// drawings, which no writer has a value of.
func TestDefaultKeysAndTheirValue(t *testing.T) {
	keys := DefaultKeys()
	if len(keys) != len(Keys())+1 || keys[len(keys)-1] != KeyTrialDrawings {
		t.Fatalf("DefaultKeys = %v", keys)
	}
	for index, key := range Keys() {
		if keys[index] != key {
			t.Fatalf("DefaultKeys[%d] = %s, want %s", index, keys[index], key)
		}
	}
	if _, ok := ParseKey(string(KeyTrialDrawings)); ok {
		t.Fatal("trial_drawings is no writer limit")
	}
	if key, ok := ParseDefaultKey("trial_drawings"); !ok || key != KeyTrialDrawings {
		t.Fatalf("ParseDefaultKey(trial_drawings) = %q %v", key, ok)
	}
	if _, ok := ParseDefaultKey("chat_turns"); ok {
		t.Fatal("unknown default key parsed")
	}
	platform := number(12)
	if (Default{Key: KeyProjects, Code: 3}).Effective() != 3 || (Default{Key: KeyProjects, Code: 3, Platform: platform}).Effective() != 12 {
		t.Fatal("Effective is the platform value, else the code's")
	}
}

// R7.5 and writer-quotas R6.2: a change set is not empty, names a known key once and keeps a
// value from 0 to what the key's column holds; a refused set changes nothing.
func TestCheckChangesOfOverridesAndDefaults(t *testing.T) {
	valid := []OverrideChange{
		{Key: KeyChatTurnsPerDay, Value: number(60)}, {Key: KeyActiveTurns},
		{Key: KeyAccountCharacters, Value: number(math.MaxInt64)}, {Key: KeyProjects, Value: number(math.MaxInt32)},
	}
	if err := CheckOverrides(valid); err != nil {
		t.Fatalf("CheckOverrides(valid) = %v", err)
	}
	if err := CheckDefaults(append(valid, OverrideChange{Key: KeyTrialDrawings, Value: number(10)})); err != nil {
		t.Fatalf("CheckDefaults(valid) = %v", err)
	}
	for name, refused := range map[string][]OverrideChange{
		"no change":       nil,
		"unknown key":     {{Key: "chat_turns", Value: number(5)}},
		"negative":        {{Key: KeyProjects, Value: number(-1)}},
		"over integer":    {{Key: KeyProjects, Value: number(math.MaxInt32 + 1)}},
		"trial over int":  {{Key: KeyImagesPerDay, Value: number(math.MaxInt32 + 1)}},
		"repeated key":    {{Key: KeyProjects, Value: number(5)}, {Key: KeyProjects}},
		"trial is no own": {{Key: KeyTrialDrawings, Value: number(5)}},
	} {
		if err := CheckOverrides(refused); !errors.Is(err, ErrInvalidOverride) {
			t.Fatalf("CheckOverrides %s: err = %v, want ErrInvalidOverride", name, err)
		}
		if name == "trial is no own" {
			continue
		}
		if err := CheckDefaults(refused); !errors.Is(err, ErrInvalidOverride) {
			t.Fatalf("CheckDefaults %s: err = %v, want ErrInvalidOverride", name, err)
		}
	}
	if err := CheckDefaults([]OverrideChange{{Key: KeyTrialDrawings, Value: number(math.MaxInt32 + 1)}}); !errors.Is(err, ErrInvalidOverride) {
		t.Fatalf("CheckDefaults trial over integer: err = %v", err)
	}
}
