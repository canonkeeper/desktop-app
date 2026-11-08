package authstore

import (
	"context"
	"errors"

	"canonkeeper/backend/internal/auth"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// LockSessionWriteProof проверяет доказательство записи и удерживает session/user
// до завершения уже открытой транзакции.
func LockSessionWriteProof(ctx context.Context, tx pgx.Tx, actorUserID pgtype.UUID, proof auth.SessionWriteProof) (pgtype.UUID, error) {
	sessionID, _, err := lockSessionWriteProof(ctx, tx, actorUserID, proof)
	return sessionID, err
}

// LockVerifiedSessionWriteProof is LockSessionWriteProof for transactions that
// create model work: the actor's address must also be verified (UC1.4).
// Verification never goes back, so the check at creation covers the work's life.
func LockVerifiedSessionWriteProof(ctx context.Context, tx pgx.Tx, actorUserID pgtype.UUID, proof auth.SessionWriteProof) (pgtype.UUID, error) {
	sessionID, verified, err := lockSessionWriteProof(ctx, tx, actorUserID, proof)
	if err != nil {
		return pgtype.UUID{}, err
	}
	if !verified {
		return pgtype.UUID{}, auth.ErrEmailNotVerified
	}
	return sessionID, nil
}

func lockSessionWriteProof(ctx context.Context, tx pgx.Tx, actorUserID pgtype.UUID, proof auth.SessionWriteProof) (pgtype.UUID, bool, error) {
	if tx == nil || !actorUserID.Valid || !proof.Valid() {
		return pgtype.UUID{}, false, auth.ErrUnauthenticated
	}
	var sessionID pgtype.UUID
	var verified bool
	err := tx.QueryRow(ctx, `
		SELECT session.id, actor.email_verified_at IS NOT NULL
		FROM sessions AS session
		JOIN users AS actor ON actor.id = session.user_id
		WHERE session.user_id = $1
		  AND session.session_hash = $2
		  AND session.csrf_secret_hash = $3
		  AND session.revoked_at IS NULL
		  AND session.rotated_at IS NULL
		  AND session.expires_at > clock_timestamp()
		  AND actor.disabled_at IS NULL
		FOR SHARE OF session, actor
	`, actorUserID, proof.SessionHash, proof.CSRFSecretHash).Scan(&sessionID, &verified)
	if errors.Is(err, pgx.ErrNoRows) {
		return pgtype.UUID{}, false, auth.ErrUnauthenticated
	}
	return sessionID, verified, err
}
