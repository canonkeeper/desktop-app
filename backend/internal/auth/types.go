package auth

import "time"

type SessionWriteProof struct {
	SessionHash    string `json:"-"`
	CSRFSecretHash string `json:"-"`
}

func (proof SessionWriteProof) Valid() bool {
	return proof.SessionHash != "" && proof.CSRFSecretHash != ""
}

type User struct {
	ID            string
	Email         string
	EmailVerified bool
	DisplayName   string
	CreatedAt     time.Time
}

type Session struct {
	ID          string
	CookieValue string
	ExpiresAt   time.Time
	// Persistent sessions keep their cookie across browser restarts.
	Persistent bool
}

type CSRFToken struct {
	Token              string
	ExpiresAt          time.Time
	Rotation           string
	ClearSessionCookie bool
}
