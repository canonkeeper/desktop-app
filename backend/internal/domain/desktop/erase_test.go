// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import "testing"

// specs/2026-11-13-desk-erase.md ER1.1, ER1.2: the word of the erasure counts whatever its case and the spaces at its
// ends; any other word, an empty one or the word inside a phrase does not.
func TestTheWordOfTheErasureIsWrittenWhateverItsCase(t *testing.T) {
	for _, word := range []string{"стереть", "СТЕРЕТЬ", "Стереть", "  стереть\t", " СтЕрЕтЬ\n"} {
		if !EraseConfirmed(word) {
			t.Errorf("%q is not the word", word)
		}
	}
	for _, word := range []string{"", " ", "стереть всё", "стерет", "стереть.", "erase", "с тереть"} {
		if EraseConfirmed(word) {
			t.Errorf("%q is the word", word)
		}
	}
}
