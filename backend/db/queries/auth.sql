-- name: CreateUser :one
INSERT INTO users (
  id,
  email,
  display_name,
  password_hash,
  email_verified_at,
  terms_version,
  personal_data_consent_version,
  documents_accepted_at
) VALUES (
  $1,
  $2,
  $3,
  $4,
  $5,
  $6,
  $7,
  $8
)
RETURNING id, email, display_name, email_verified_at, disabled_at, created_at, updated_at, terms_version, personal_data_consent_version;

-- name: CreateSession :one
INSERT INTO sessions (
  id,
  user_id,
  session_hash,
  csrf_secret_hash,
  expires_at,
  created_at,
  persistent,
  impersonator_user_id
) VALUES (
  $1,
  $2,
  $3,
  $4,
  $5,
  $6,
  $7,
  sqlc.narg(impersonator_user_id)
)
RETURNING id, user_id, expires_at, created_at, persistent;

-- name: GetActiveSessionByHash :one
SELECT
  s.id AS session_id,
  s.user_id AS session_user_id,
  s.csrf_secret_hash AS session_csrf_secret_hash,
  s.expires_at AS session_expires_at,
  s.created_at AS session_created_at,
  s.persistent AS session_persistent,
  u.id AS user_id,
  u.email AS user_email,
  u.email_verified_at AS user_email_verified_at,
  u.display_name AS user_display_name,
  u.created_at AS user_created_at,
  u.terms_version AS user_terms_version,
  u.personal_data_consent_version AS user_personal_data_consent_version
FROM sessions s
JOIN users u ON u.id = s.user_id
WHERE s.session_hash = sqlc.arg(session_hash)
  AND s.revoked_at IS NULL
  AND s.expires_at > sqlc.arg(now)
  AND u.disabled_at IS NULL;

-- name: UpdateSessionCSRFByHash :execrows
UPDATE sessions AS s
SET csrf_secret_hash = sqlc.arg(csrf_secret_hash)
FROM users u
WHERE s.session_hash = sqlc.arg(session_hash)
  AND s.user_id = u.id
  AND s.revoked_at IS NULL
  AND s.expires_at > sqlc.arg(now)
  AND u.disabled_at IS NULL;

-- name: RevokeUserSessions :exec
UPDATE sessions
SET revoked_at = sqlc.arg(revoked_at)
WHERE user_id = sqlc.arg(user_id)
  AND revoked_at IS NULL;

-- name: GetUserIDByEmail :one
SELECT id
FROM users
WHERE email = sqlc.arg(email)
  AND disabled_at IS NULL;
