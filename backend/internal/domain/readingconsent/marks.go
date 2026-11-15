// SPDX-License-Identifier: AGPL-3.0-only

package readingconsent

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"canonkeeper/backend/internal/domain/aisettings"
)

// HelperOf names the helper the reading of the settings goes to (specs/2026-11-15-desk-reading-rest.md RR1.4): the source,
// and of «Свой ИИ» also the host of the provider, the model that reads the books and the limit of an answer — the helper the
// marks of a failure keep, as a SHA-256 of them. A mark acts only for the same helper: another model or another limit of an
// answer is a word of the author, and the reading tries once more. "" — no helper.
func HelperOf(settings aisettings.Settings) string {
	var named string
	switch {
	case settings.Source == aisettings.SourceAccount:
		named = string(SourceAccount)
	case settings.Own != nil:
		own := settings.Own
		named = fmt.Sprintf("%s\n%s\n%s\n%d", SourceOwn, own.Host(), own.CardsModel, own.Advanced.MaxOutput)
	default:
		return ""
	}
	sum := sha256.Sum256([]byte(named))
	return hex.EncodeToString(sum[:])
}

// Begins says a change of the consent began a new count (CN3.2; RR1.7): no consent acted for the source and the host the
// change was given for. «Повторить» and a new count forget the marks of the saga.
func Begins(current *Consent, next Consent) bool {
	return !current.Acts(next.Source, next.Host)
}
