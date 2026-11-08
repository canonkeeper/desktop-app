// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"context"
	"time"

	"canonkeeper/backend/internal/auth"
)

// sessionLifetime bounds the row of the session of the window; the cookie has no term and lives as long as the window,
// and the next start revokes the session anyway (DA3.3).
const sessionLifetime = 365 * 24 * time.Hour

// OpenCodes spends the one-time code of the window (domain/desktop.OpenCodes).
type OpenCodes interface {
	Use(code string) error
}

// Sessions open a session of a known user and read its user.
type Sessions interface {
	CreateSession(ctx context.Context, session auth.NewSession) (auth.Session, string, error)
	CurrentUser(ctx context.Context, cookieValue string) (auth.User, error)
}

// OpenedSession is the session of the window with its first CSRF token and its user.
type OpenedSession struct {
	Session   auth.Session
	CSRFToken string
	User      auth.User
}

// OpenSession is openDesktopSession (DA3.3): a one-time code of the shell becomes a session of the local author, opened
// by the standard session of the core. An unknown, used or late code is domain/desktop.ErrInvalidOpenCode.
type OpenSession struct {
	Codes         OpenCodes
	Sessions      Sessions
	LocalAuthorID string
	Now           func() time.Time
}

// Execute exchanges the code for the session (DA3, step «POST /api/v1/desktop/session»).
func (useCase OpenSession) Execute(ctx context.Context, code string) (OpenedSession, error) {
	if err := useCase.Codes.Use(code); err != nil {
		return OpenedSession{}, err
	}
	session, csrf, err := useCase.Sessions.CreateSession(ctx, auth.NewSession{
		UserID: useCase.LocalAuthorID, ExpiresAt: useCase.Now().Add(sessionLifetime),
	})
	if err != nil {
		return OpenedSession{}, err
	}
	user, err := useCase.Sessions.CurrentUser(ctx, session.CookieValue)
	if err != nil {
		return OpenedSession{}, err
	}
	return OpenedSession{Session: session, CSRFToken: csrf, User: user}, nil
}
