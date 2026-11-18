package sagadraft

import "time"

// Trial is what a draft says to the writer who reads it when «Письмо» of his tier is a trial
// (specs/2026-11-18-free-tier.md FT2.4): the kind of the trial in the draft — before, chapter, proofread or none —, the end
// of its day, nil but for a trial chapter or proofreading, and the works the model may start in the draft now — scenes,
// check, rewrite, proofread, turn. The build with tiers decides it; a build without them, and a tier with a window, say
// nothing.
type Trial struct {
	Kind   string
	Until  *time.Time
	Allows []string
}
