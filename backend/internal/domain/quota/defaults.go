package quota

import (
	"math"
	"time"
)

// Limits by default (specs/2026-10-29-admin-panel.md UC7): the administrator sets a platform value
// of a limit for every writer without his own; the values of the code stay as the original ones. The keys
// are the writer limits, in the order of the tab «Лимиты»: the trial drawings of an account are its tier's
// (specs/2026-11-08-subscription-tiers.md TR6.6).

// Max is the largest value the column of the key keeps: bigint for the characters, integer
// otherwise (R7.5).
func (key Key) Max() int64 {
	if key == KeyProjectCharacters || key == KeyAccountCharacters {
		return math.MaxInt64
	}
	return math.MaxInt32
}

// CheckOverrides refuses a change of a writer's own limits or of the platform defaults that is empty,
// names an unknown or repeated key or a value below 0 or above what the key keeps (writer-quotas R6.2,
// admin-panel R7.5).
func CheckOverrides(changes []OverrideChange) error {
	if len(changes) == 0 {
		return ErrInvalidOverride
	}
	seen := make(map[Key]bool, len(changes))
	for _, change := range changes {
		if _, known := ParseKey(string(change.Key)); !known || seen[change.Key] ||
			change.Value != nil && (*change.Value < 0 || *change.Value > change.Key.Max()) {
			return ErrInvalidOverride
		}
		seen[change.Key] = true
	}
	return nil
}

// Settings are the limits one source sets; a key without a value keeps the next source.
type Settings map[Key]int64

// Resolve is the limits in force for a writer (R7.2; specs/2026-11-08-subscription-tiers.md TR2.2): his own value of a
// limit, else the value of his plan, else the platform default, else the value of the code (Defaults). The open core
// knows only that a writer may have a plan; what sets it is the build's. A key Limits does not hold is ignored.
func Resolve(own, plan, platform Settings) Limits {
	limits := Defaults()
	for _, key := range Keys() {
		for _, layer := range []Settings{platform, plan, own} {
			if value, set := layer[key]; set {
				limits = limits.With(key, value)
			}
		}
	}
	return limits
}

// Source is where a limit in force comes from (TR2.6): the writer's own value, his plan, or the default — the
// platform's or the code's.
type Source string

const (
	SourceOwn     Source = "own"
	SourcePlan    Source = "plan"
	SourceDefault Source = "default"
)

// LimitSources names the source of every limit by the same order as Resolve.
func LimitSources(own, plan Settings) map[Key]Source {
	sources := make(map[Key]Source, len(Keys()))
	for _, key := range Keys() {
		_, mine := own[key]
		_, planned := plan[key]
		switch {
		case mine:
			sources[key] = SourceOwn
		case planned:
			sources[key] = SourcePlan
		default:
			sources[key] = SourceDefault
		}
	}
	return sources
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

// DefaultList is the tab «Лимиты» (R7.4): every platform default in the order of Keys and the
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
