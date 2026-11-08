package library

import (
	"crypto/sha256"
	"errors"
	"time"
)

const (
	MaxCoverBytes  int64 = 5 << 20
	MinCoverWidth        = 600
	MinCoverHeight       = 900
)

var (
	ErrNotFound              = errors.New("library: not found")
	ErrForbidden             = errors.New("library: forbidden")
	ErrValidation            = errors.New("library: validation failed")
	ErrConflict              = errors.New("library: conflict")
	ErrUnavailable           = errors.New("library: unavailable")
	ErrCoverTooLarge         = errors.New("library: cover is too large")
	ErrCoverTooSmall         = errors.New("library: cover dimensions are too small")
	ErrUnsupportedCoverMedia = errors.New("library: unsupported cover media")
)

type Actor struct {
	UserID string
}

type CoverState string

const (
	CoverStaging    CoverState = "staging"
	CoverActive     CoverState = "active"
	CoverSuperseded CoverState = "superseded"
	CoverRemoved    CoverState = "removed"
)

type CoverArtifact struct {
	ID                      string
	ProjectID               string
	BookID                  string
	BookLifecycleGeneration int64
	Version                 int64
	StorageKey              string
	MIMEType                string
	ByteSize                int64
	Width                   int
	Height                  int
	ContentSHA256           [sha256.Size]byte
	State                   CoverState
	CreatedAt               time.Time
	ActivatedAt             *time.Time
	SupersededAt            *time.Time
	RemovedAt               *time.Time
	ObjectDeletedAt         *time.Time
}

type CoverActivation struct {
	Current    CoverArtifact
	Superseded *CoverArtifact
}

type CoverLookup struct {
	Actor   Actor
	BookID  string
	Version int64
}

type RemoveCoverInput struct {
	Actor  Actor
	BookID string
	At     time.Time
}

type ActivateCoverInput struct {
	Actor         Actor
	ArtifactID    string
	BookID        string
	StorageKey    string
	MIMEType      string
	ByteSize      int64
	Width         int
	Height        int
	ContentSHA256 [sha256.Size]byte
	At            time.Time
}

func (input ActivateCoverInput) Validate() error {
	if input.Actor.UserID == "" || input.ArtifactID == "" || input.BookID == "" || input.StorageKey == "" ||
		input.ByteSize < 1 || input.ByteSize > MaxCoverBytes || input.Width < MinCoverWidth || input.Height < MinCoverHeight ||
		input.ContentSHA256 == ([sha256.Size]byte{}) || input.At.IsZero() {
		return ErrValidation
	}
	switch input.MIMEType {
	case "image/jpeg", "image/png", "image/webp":
		return nil
	default:
		return ErrValidation
	}
}
