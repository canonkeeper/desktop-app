package agent

import "errors"

var ErrRuntimeProcessExited = errors.New("agent runtime process exited")

var (
	// ErrTurnNotActive: the native runtime has no such active turn to send input into.
	ErrTurnNotActive = errors.New("agent runtime: the turn is not active")
	// ErrTurnNotSteerable: the turn is active but takes no input now, such as while it compacts.
	ErrTurnNotSteerable = errors.New("agent runtime: the active turn takes no input now")
)
