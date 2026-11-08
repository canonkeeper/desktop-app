package canonstore

import (
	storedb "canonkeeper/backend/internal/store/db"
	governancestore "canonkeeper/backend/internal/store/governance"
)

type Store struct {
	*storedb.DB
	artifactSources *governancestore.Store
}

func New(db *storedb.DB) *Store {
	return &Store{
		DB: db, artifactSources: governancestore.New(db),
	}
}
