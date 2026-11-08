package researchchat

import "errors"

// ToolOutput is a tool answer the model reads, with an optional JPEG image it sees.
type ToolOutput struct {
	Text  string
	Image []byte
}

// ToolOutcome is a typed answer of a tool that did not do what was asked; the model reads the message and acts on
// it in the same turn.
type ToolOutcome struct {
	Type    string
	Message string
	Next    []string
}

func (outcome *ToolOutcome) Error() string { return outcome.Type + ": " + outcome.Message }

// AsToolOutcome finds a typed tool outcome in an error chain.
func AsToolOutcome(err error) (*ToolOutcome, bool) {
	var outcome *ToolOutcome
	ok := errors.As(err, &outcome)
	return outcome, ok
}

// SentTurn is what the author sent to start a turn, as a tool of a helper reads it: his words and whether a button of
// the page sent them (specs/2026-11-04-writing-plain-path.md P5.6).
type SentTurn struct {
	Question string
	Button   bool
}
