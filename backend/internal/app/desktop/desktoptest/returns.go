// SPDX-License-Identifier: AGPL-3.0-only

package desktoptest

// ReturnsOnClose is the rule of the site whether a close by the app gives the unit of a permit back
// (specs/2026-11-13-desk-permits.md PM2.1, PM2.4), as the stub of the site of the process tests answers it. The open code
// does not call the closed domain of the site, so the rule is kept as a table: the kinds with a unit closed failed, when the
// site saw no answer of the model under the permit — with requests or without (then on the word of the app). A closed test
// of the site holds the table equal to the rule of its domain on every case.
var ReturnsOnClose = []Close{
	{Kind: "chat_turn", Outcome: "failed"},
	{Kind: "materials", Outcome: "failed"},
}

// Close is a close of a permit by the app: its kind and the outcome the app closes it with.
type Close struct{ Kind, Outcome string }

// Returned says whether the site gives the unit of a permit of the kind back when the app closes it with the outcome, by
// whether the site saw an answer of the model under it.
func Returned(kind, outcome string, answered bool) bool {
	if answered {
		return false
	}
	for _, close := range ReturnsOnClose {
		if close.Kind == kind && close.Outcome == outcome {
			return true
		}
	}
	return false
}
