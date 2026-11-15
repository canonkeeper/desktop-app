package quota

import "testing"

// specs/2026-11-13-desk-account-ai.md AI5.3: an honest reading of the books spends about six tokens for a character of the
// text by the measure of production, so the fence of the reading of the apps is eight tokens for a character of the tier
// by default — a value the platform sets in «Лимиты» still wins.
func TestTheFenceOfTheReadingOfTheAppsIsEightTokensForACharacter(t *testing.T) {
	if got := Defaults().AppReadingTokensPerCharacter; got != 8 {
		t.Fatalf("app_reading_tokens_per_character by default = %d, want 8", got)
	}
	platform := Settings{KeyAppReadingTokensPerCharacter: 3}
	if got := Resolve(nil, nil, platform).AppReadingTokensPerCharacter; got != 3 {
		t.Fatalf("the value of «Лимиты» = %d, want 3", got)
	}
}
