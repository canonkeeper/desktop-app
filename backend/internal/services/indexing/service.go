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
}

func NewService(getStatus GetStatusPort) *Service {
	if getStatus == nil {
		panic("indexing service: status port is required")
	}
	return &Service{getStatus: getStatus}
}
