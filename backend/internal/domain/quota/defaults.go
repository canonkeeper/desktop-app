package quota

import (
	"math"
	"time"
)

// Limits by default (specs/2026-10-29-admin-panel.md UC7): the administrator sets a platform value
// of a limit for every writer without his own, and the trial drawings of an account without its
// right to illustrations yet; the values of the code stay as the original ones.

// KeyTrialDrawings is a platform default only: the trial drawings an account gets with its right
// to illustrations (specs/2026-10-29-saga-illustrations.md R11.1). A writer has no own value of it.
const KeyTrialDrawings Key = "trial_drawings"

// TrialDrawings is how many drawings an account may try once (specs/2026-10-29-saga-illustrations.md R11.1) while the
// administrator set no trial for new accounts (specs/2026-10-29-admin-panel.md R7.3): the original value of the code.
const TrialDrawings = 5

// DefaultKeys lists the platform defaults in the order of the tab «Лимиты»: every writer limit, then
// the trial drawings.
func DefaultKeys() []Key {
	return append(Keys(), KeyTrialDrawings)
}

// ParseDefaultKey accepts a writer limit or the trial drawings.
func ParseDefaultKey(value string) (Key, bool) {
	for _, key := range DefaultKeys() {
		if string(key) == value {
			return key, true
		}
	}
	return "", false
}

// Max is the largest value the column of the key keeps: bigint for the characters, integer
// otherwise (R7.5).
func (key Key) Max() int64 {
	if key == KeyProjectCharacters || key == KeyAccountCharacters {
		return math.MaxInt64
	}
	return math.MaxInt32
}

// CheckOverrides refuses a change of a writer's own limits that is empty, names an unknown or
// repeated key or a value below 0 or above what the key keeps (writer-quotas R6.2).
func CheckOverrides(changes []OverrideChange) error {
	return checkChanges(changes, ParseKey)
}

// CheckDefaults is CheckOverrides for the platform defaults, whose keys also hold the trial
// drawings (admin-panel R7.5).
func CheckDefaults(changes []OverrideChange) error {
	return checkChanges(changes, ParseDefaultKey)
}

func checkChanges(changes []OverrideChange, parse func(string) (Key, bool)) error {
	if len(changes) == 0 {
		return ErrInvalidOverride
	}
	seen := make(map[Key]bool, len(changes))
	for _, change := range changes {
		if _, known := parse(string(change.Key)); !known || seen[change.Key] ||
			change.Value != nil && (*change.Value < 0 || *change.Value > change.Key.Max()) {
			return ErrInvalidOverride
		}
		seen[change.Key] = true
	}
	return nil
}

// Settings are the limits one source sets; a key without a value keeps the next source.
type Settings map[Key]int64

// Resolve is the limits in force for a writer (R7.2): his own value of a limit, else the platform
// default, else the value of the code (Defaults). A key Limits does not hold is ignored.
func Resolve(own, platform Settings) Limits {
	limits := Defaults()
	for _, key := range Keys() {
		if value, set := platform[key]; set {
			limits = limits.With(key, value)
		}
		if value, set := own[key]; set {
			limits = limits.With(key, value)
		}
	}
	return limits
}

// PlatformDefaults are what the administrator set (R7.1): the values, the moment of the last change
// and the administrator who made it; nil and empty while nothing was ever set.
type PlatformDefaults struct {
	Values    Settings
	UpdatedAt *time.Time
	UpdatedBy string
}

// Default is one platform default as the tab «Лимиты» shows it (R7.4): the value of the code and
// the administrator's, nil when he set none.
type Default struct {
	Key      Key
	Code     int64
	Platform *int64
}

// Effective is the default in force: the platform value, else the code's.
func (value Default) Effective() int64 {
	if value.Platform != nil {
		return *value.Platform
	}
	return value.Code
}

// DefaultList is the tab «Лимиты» (R7.4): every platform default in the order of DefaultKeys and the
// moment of the last change, nil while nothing was ever set.
type DefaultList struct {
	Items     []Default
	UpdatedAt *time.Time
}

// DefaultChanged is one changed platform default (R7.7): the platform's value before and after —
// nil is the code's — and the value of the code.
type DefaultChanged struct {
	OverrideChanged
	Code int64
}
