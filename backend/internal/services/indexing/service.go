// Package indexing reports how far a project text version is through import, text index and
// chapter cards (R5.3).
package indexing

import "errors"

var (
	ErrInvalid     = errors.New("indexing service: invalid request")
	ErrUnavailable = errors.New("indexing service: operation unavailable")
)

type Service struct {
	getStatus GetStatusPort
	letters   ReadyLetters
	helper    Helper
}

// WithLetters makes the status promise the letter «Сага готова» only to an author the build would write to
// (specs/2026-11-09-first-session.md, FS5.6); a build without letters promises none.
func (service *Service) WithLetters(letters ReadyLetters) *Service {
	service.letters = letters
	return service
}

// WithHelper makes the status say that the reading of a saga waits for the helper while the build has none connected
// (specs/2026-11-12-desk-window.md WN3.1); a build without the port — the site — always reads.
func (service *Service) WithHelper(helper Helper) *Service {
	service.helper = helper
	return service
}

func NewService(getStatus GetStatusPort) *Service {
	if getStatus == nil {
		panic("indexing service: status port is required")
	}
	return &Service{getStatus: getStatus}
}
