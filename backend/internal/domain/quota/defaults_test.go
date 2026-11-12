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
		nil,
		Settings{KeyChatTurnsPerDay: 40, KeyActiveTurns: 4, KeyAccountCharacters: 30_000_000},
	)
	want := Defaults()
	want.ChatTurnsPerDay, want.Projects, want.ActiveTurns, want.AccountCharacters = 60, 0, 4, 30_000_000
	if limits != want {
		t.Fatalf("Resolve = %+v, want %+v", limits, want)
	}
	if Resolve(nil, nil, nil) != Defaults() {
		t.Fatal("no own, no plan and no platform value must be the code's defaults")
	}
}

// specs/2026-11-08-subscription-tiers.md TR2.2: the writer's plan stands between his own limit and the platform's; a
// key the plan does not set goes on to the platform and the code.
func TestResolvePutsThePlanBetweenOwnAndPlatform(t *testing.T) {
	own := Settings{KeyChatTurnsPerDay: 60}
	plan := Settings{KeyChatTurnsPerDay: 20, KeyAccountBooks: 12, KeyProjects: 3}
	platform := Settings{KeyAccountBooks: 50, KeyProjects: 7, KeyActiveTurns: 4}
	limits := Resolve(own, plan, platform)
	want := Defaults()
	want.ChatTurnsPerDay, want.AccountBooks, want.Projects, want.ActiveTurns = 60, 12, 3, 4
	if limits != want {
		t.Fatalf("Resolve = %+v, want %+v", limits, want)
	}
	sources := LimitSources(own, plan)
	if len(sources) != len(Keys()) {
		t.Fatalf("LimitSources names %d keys, want every key", len(sources))
	}
	for key, want := range map[Key]Source{
		KeyChatTurnsPerDay: SourceOwn, KeyAccountBooks: SourcePlan, KeyProjects: SourcePlan,
		KeyActiveTurns: SourceDefault, KeyImagesPerDay: SourceDefault,
	} {
		if sources[key] != want {
			t.Fatalf("source of %s = %q, want %q", key, sources[key], want)
		}
	}
}

// TR2.5: a write names the measures of text it grows; the others it leaves to the checks of their own writes.
func TestMeasuresNameWhatAWriteGrows(t *testing.T) {
	edit := MeasureCharacters
	chapter := MeasureChapters | MeasureCharacters
	book := MeasureBooks | MeasureChapters | MeasureCharacters
	if edit.Has(MeasureBooks) || edit.Has(MeasureChapters) || !edit.Has(MeasureCharacters) {
		t.Fatalf("an edit grows only characters: %b", edit)
	}
	if chapter.Has(MeasureBooks) || !chapter.Has(MeasureChapters) || !chapter.Has(MeasureCharacters) {
		t.Fatalf("a chapter grows chapters and characters: %b", chapter)
	}
	if !book.Has(MeasureBooks) || !book.Has(MeasureChapters) || !book.Has(MeasureCharacters) {
		t.Fatalf("a book grows every measure: %b", book)
	}
}

// R7.4: a default in force is the platform value, else the code's; the trial drawings are no default since they are the
// tier's (specs/2026-11-08-subscription-tiers.md TR6.6).
func TestDefaultEffectiveAndNoTrialDrawings(t *testing.T) {
	platform := number(12)
	if (Default{Key: KeyProjects, Code: 3}).Effective() != 3 || (Default{Key: KeyProjects, Code: 3, Platform: platform}).Effective() != 12 {
		t.Fatal("Effective is the platform value, else the code's")
	}
	if _, ok := ParseKey("trial_drawings"); ok {
		t.Fatal("trial_drawings is a limit again")
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
	for name, refused := range map[string][]OverrideChange{
		"no change":       nil,
		"unknown key":     {{Key: "chat_turns", Value: number(5)}},
		"negative":        {{Key: KeyProjects, Value: number(-1)}},
		"over integer":    {{Key: KeyProjects, Value: number(math.MaxInt32 + 1)}},
		"images over int": {{Key: KeyImagesPerDay, Value: number(math.MaxInt32 + 1)}},
		"repeated key":    {{Key: KeyProjects, Value: number(5)}, {Key: KeyProjects}},
		"trial drawings":  {{Key: "trial_drawings", Value: number(5)}},
	} {
		if err := CheckOverrides(refused); !errors.Is(err, ErrInvalidOverride) {
			t.Fatalf("CheckOverrides %s: err = %v, want ErrInvalidOverride", name, err)
		}
	}
}
