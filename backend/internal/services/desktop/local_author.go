// SPDX-License-Identifier: AGPL-3.0-only

// Package desktop holds the use cases of the desktop build (specs/2026-11-07-desktop-app.md): the local author of the
// computer and the session of the window.
package desktop

import (
	"context"
	"errors"
	"time"

	"canonkeeper/backend/internal/auth"
	"canonkeeper/backend/internal/domain/quota"
)

// The local author (DA2.2): an address of the reserved domain .invalid — no mail goes anywhere, the build has no mail —
// verified, so the model work of the author is not closed to him.
const (
	LocalAuthorEmail = "local@desktop.invalid"
	LocalAuthorName  = "Автор"
)

// AuthorStore finds the local author by address and revokes the sessions of earlier runs.
type AuthorStore interface {
	UserIDByEmail(ctx context.Context, email string) (string, bool, error)
	RevokeUserSessions(ctx context.Context, userID string, at time.Time) error
}

// UserCreator creates a user without a password; only a session the build opens signs it in.
type UserCreator interface {
	CreateUser(ctx context.Context, user auth.NewUser) (auth.User, error)
}

// QuotaDefaults sets the platform's limits by default, the standard path of the administrator's tab «Лимиты»: a repeat of
// the same values writes nothing.
type QuotaDefaults interface {
	SetPlatformDefaults(ctx context.Context, adminID string, changes []quota.OverrideChange, at time.Time) ([]quota.OverrideChanged, error)
}

// PrepareLocalAuthor is the first step of every start (DA2.2, DA3.3): the local author — created by the first start
// without a question, found by every later one — the platform's limits at the largest value of every key, set by the
// author himself as the only person of the computer, and no session of an earlier run left.
type PrepareLocalAuthor struct {
	Authors  AuthorStore
	Users    UserCreator
	Defaults QuotaDefaults
	Now      func() time.Time
}

// Execute returns the id of the local author.
func (useCase PrepareLocalAuthor) Execute(ctx context.Context) (string, error) {
	userID, found, err := useCase.Authors.UserIDByEmail(ctx, LocalAuthorEmail)
	if err != nil {
		return "", err
	}
	if !found {
		user, err := useCase.Users.CreateUser(ctx, auth.NewUser{Email: LocalAuthorEmail, DisplayName: LocalAuthorName, EmailVerified: true})
		if err != nil {
			return "", err
		}
		userID = user.ID
	}
	if userID == "" {
		return "", errors.New("desktop: the local author has no id")
	}
	changes := make([]quota.OverrideChange, 0, len(quota.Keys()))
	for _, key := range quota.Keys() {
		largest := key.Max()
		changes = append(changes, quota.OverrideChange{Key: key, Value: &largest})
	}
	now := useCase.Now().UTC()
	if _, err := useCase.Defaults.SetPlatformDefaults(ctx, userID, changes, now); err != nil {
		return "", err
	}
	if err := useCase.Authors.RevokeUserSessions(ctx, userID, now); err != nil {
		return "", err
	}
	return userID, nil
}
