// Package indexing reads the indexing status of a project from PostgreSQL.
package indexing

import (
	"fmt"

	indexingdomain "canonkeeper/backend/internal/domain/indexing"
	storedb "canonkeeper/backend/internal/store/db"
)

var (
	ErrInvalid  = fmt.Errorf("indexing repository: invalid request: %w", indexingdomain.ErrValidation)
	ErrConflict = fmt.Errorf("indexing repository: conflict: %w", indexingdomain.ErrConflict)
)

type Store struct {
	db *storedb.DB
}

func New(db *storedb.DB) *Store { return &Store{db: db} }
