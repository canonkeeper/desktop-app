// SPDX-License-Identifier: AGPL-3.0-only

package obsidian

import (
	"errors"
	"fmt"
	"testing"

	service "canonkeeper/backend/internal/services/obsidian"

	"github.com/jackc/pgx/v5/pgconn"
)

// specs/2026-11-15-desk-obsidian-notes.md ON1.5: a deadlock or a failure of serialization the database ends the transaction of
// a load with — as «Сохранить» of a parse of materials that writes the same records in another order may — is a conflict, a
// refusal the same key repeats, as a name another record took meanwhile; the transaction is rolled back, so no answer of the
// key is kept. Another failure of the database is no conflict.
func TestAFailureOfTheDatabaseALoadMayRepeatIsAConflict(t *testing.T) {
	for _, code := range []string{"40P01", "40001", "23505"} {
		if err := writeError(fmt.Errorf("the transaction: %w", &pgconn.PgError{Code: code})); !errors.Is(err, service.ErrConflict) {
			t.Errorf("%s = %v, want a conflict", code, err)
		}
	}
	if err := writeError(&pgconn.PgError{Code: "57014"}); errors.Is(err, service.ErrConflict) {
		t.Fatalf("a canceled statement = %v", err)
	}
}
