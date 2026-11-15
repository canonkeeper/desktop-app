// SPDX-License-Identifier: AGPL-3.0-only

package readingconsent

import (
	"slices"
	"testing"
)

// specs/2026-11-13-desk-account-ai.md AI3.4: the limit whose part of the reading it lets through is the smaller ends it first;
// both enough — none.
func TestTheLimitThatLetsLessThroughEndsTheReadingFirst(t *testing.T) {
	cases := []struct {
		characters, work int64
		chars, works     Limit
		want             string
	}{
		{100_000, 600_000, Limit{Used: 0, Limit: 1_000_000}, Limit{Used: 0, Limit: 8_000_000}, ""},
		{100_000, 600_000, Limit{Used: 950_000, Limit: 1_000_000}, Limit{Used: 0, Limit: 8_000_000}, EndsCharacters},
		{100_000, 600_000, Limit{Used: 0, Limit: 1_000_000}, Limit{Used: 7_700_000, Limit: 8_000_000}, EndsWork},
		{100_000, 600_000, Limit{Used: 980_000, Limit: 1_000_000}, Limit{Used: 7_700_000, Limit: 8_000_000}, EndsCharacters},
		{100_000, 600_000, Limit{Used: 900_000, Limit: 1_000_000}, Limit{Used: 7_880_000, Limit: 8_000_000}, EndsWork},
		{0, 0, Limit{Used: 1_000_000, Limit: 1_000_000}, Limit{}, ""},
	}
	for _, example := range cases {
		if got := AccountReadingOf(example.characters, example.work, example.chars, example.works, true).EndsFirst; got != example.want {
			t.Errorf("%+v: ends first %q, want %q", example, got, example.want)
		}
	}
	if unknown := AccountReadingOf(100_000, 600_000, Limit{}, Limit{}, false); unknown.LimitsTold || unknown.EndsFirst != "" || unknown.Characters != 100_000 {
		t.Fatalf("an estimate without the limits of the site = %+v", unknown)
	}
}

// specs/2026-11-13-desk-account-ai.md AI3.6: the pauses of the helper of the account go on by themselves by one rule — none
// while the site does not answer the check; no connection and the reading of the site unavailable whenever it does; the
// limits of the tier only at their check and with room; an app the site did not take only at the start of a new version;
// the pause of the author never.
func TestThePausesOfTheAccountGoOnByOneRule(t *testing.T) {
	cases := []struct {
		name  string
		check Resumption
		want  []Pause
	}{
		{"the site does not answer", Resumption{NewVersion: true, Limits: true, Characters: true, Work: true}, nil},
		{"a check of ten minutes", Resumption{Reaches: true, Characters: true, Work: true}, []Pause{PauseConnection, PauseUnavailable}},
		{"a start of the same version, no room", Resumption{Reaches: true, Limits: true}, []Pause{PauseConnection, PauseUnavailable}},
		{"the hour with room for the characters", Resumption{Reaches: true, Limits: true, Characters: true}, []Pause{PauseConnection, PauseUnavailable, PauseCharacters}},
		{"the hour with room for the work", Resumption{Reaches: true, Limits: true, Work: true}, []Pause{PauseConnection, PauseUnavailable, PauseReadingFence}},
		{"the start of a new version", Resumption{NewVersion: true, Reaches: true, Limits: true}, []Pause{PauseConnection, PauseUnavailable, PauseUnfit}},
	}
	for _, testCase := range cases {
		got := DueOfAccount(testCase.check)
		if !slices.Equal(got, testCase.want) || slices.Contains(got, PauseAuthor) {
			t.Errorf("%s: %v; want %v", testCase.name, got, testCase.want)
		}
	}
}
