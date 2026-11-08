// Package indexing holds the vocabulary of the indexing status: the text version a status is
// scoped to and its errors.
package indexing

import "errors"

var (
	ErrConflict   = errors.New("indexing: conflict")
	ErrNotFound   = errors.New("indexing: not found")
	ErrDenied     = errors.New("indexing: denied")
	ErrValidation = errors.New("indexing: validation failed")
)

type SourceView string

const (
	SourceDraft     SourceView = "draft"
	SourcePublished SourceView = "published"
)
