// SPDX-License-Identifier: AGPL-3.0-only

package readingconsent

// Limit is a limit of the tier of the account of the site for the reading over 30 days (specs/2026-11-13-desk-account-ai.md
// AI3.4, AI3.7): what is used and the limit.
type Limit struct {
	Used, Limit int64
}

// Left is what the limit still lets through; never below zero.
func (limit Limit) Left() int64 { return max(limit.Limit-limit.Used, 0) }

// What ends the reading of the helper of the account first.
const (
	EndsCharacters = "characters"
	EndsWork       = "work"
)

// AccountReading is what the estimate of the helper of the account says instead of money (AI3.4): the characters the reading
// of the chapters without a card takes — the characters of the inputs of their card steps, which the site counts —, the work
// of the reading in tokens by the formula, both limits of the tier for 30 days, and which of them ends the reading first —
// none when both let it through. LimitsTold is false while the site has not told the limits yet: then neither limit nor
// what ends first is known.
type AccountReading struct {
	Characters      int64
	Work            int64
	LimitsTold      bool
	CharactersLimit Limit
	WorkLimit       Limit
	EndsFirst       string
}

// AccountReadingOf is the estimate of the helper of the account: the limit whose part of the reading it lets through is the
// smaller ends first; without the limits of the site nothing is said of them.
func AccountReadingOf(characters, work int64, charactersLimit, workLimit Limit, told bool) AccountReading {
	if !told {
		return AccountReading{Characters: characters, Work: work}
	}
	reading := AccountReading{Characters: characters, Work: work, LimitsTold: true, CharactersLimit: charactersLimit, WorkLimit: workLimit}
	short := func(left, needed int64) (float64, bool) {
		if needed <= 0 || left >= needed {
			return 1, false
		}
		return float64(left) / float64(needed), true
	}
	byCharacters, charactersShort := short(charactersLimit.Left(), characters)
	byWork, workShort := short(workLimit.Left(), work)
	switch {
	case charactersShort && (!workShort || byCharacters <= byWork):
		reading.EndsFirst = EndsCharacters
	case workShort:
		reading.EndsFirst = EndsWork
	}
	return reading
}

// Resumption is what the app knows at a check of the pauses of the helper of the account (AI3.6): whether this is the start
// of a new version, whether the check of the limits of the tier is due (at the start, then once an hour), whether the site
// answered the check now, and whether it told room in the characters of the tier and in the work of the reading.
type Resumption struct {
	NewVersion bool
	Limits     bool
	Reaches    bool
	Characters bool
	Work       bool
}

// DueOfAccount is the one rule of which pauses of the reading of the helper of the account go on by themselves at a check
// (AI3.6): no connection and the reading of the site unavailable — once the site answered the check; the characters of the
// tier and the fence of the work of the reading — at the check of the limits, when the site told room; an app the site did
// not take — only at the start of a new version, which the site answered. The pause of the author never goes on by itself.
func DueOfAccount(check Resumption) []Pause {
	if !check.Reaches {
		return nil
	}
	due := []Pause{PauseConnection, PauseUnavailable}
	if check.NewVersion {
		due = append(due, PauseUnfit)
	}
	if check.Limits && check.Characters {
		due = append(due, PauseCharacters)
	}
	if check.Limits && check.Work {
		due = append(due, PauseReadingFence)
	}
	return due
}
