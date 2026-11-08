package quota

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"canonkeeper/backend/internal/auth"
	domain "canonkeeper/backend/internal/domain/quota"

	"github.com/go-chi/chi/v5"
)

type fakeUser struct{ err error }

func (user fakeUser) CurrentUser(context.Context, string) (auth.User, error) {
	return auth.User{ID: "user-a"}, user.err
}

type fakeAccount struct{ userID string }

func (account *fakeAccount) Execute(_ context.Context, userID string) (domain.Account, error) {
	account.userID = userID
	return domain.Account{
		Limits: domain.Defaults(), Day: time.Date(2026, 10, 28, 0, 0, 0, 0, time.UTC),
		ResetsAt: time.Date(2026, 10, 28, 21, 0, 0, 0, time.UTC), ChatTurns: 24, ActiveTurns: 1, Projects: 1, Images: 3,
		Sources:       domain.Sources{Books: 13, Chapters: 641, Characters: 6534115},
		OwnedProjects: []domain.ProjectSources{{ProjectID: "project-a", Sources: domain.Sources{Books: 13, Chapters: 641, Characters: 6534115}}},
	}, nil
}

// R5.1: the writer reads only their own quota, never cached; a visitor without a session gets 401.
func TestGetAccountQuota(t *testing.T) {
	account := &fakeAccount{}
	router := chi.NewRouter()
	Handlers{AccountQuota: account, Auth: fakeUser{}}.Mount(router)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/account/quota", nil))
	var body accountQuota
	if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-store" || json.Unmarshal(rec.Body.Bytes(), &body) != nil {
		t.Fatalf("status = %d headers = %v body = %s", rec.Code, rec.Header(), rec.Body.String())
	}
	if account.userID != "user-a" || body.Day != "2026-10-28" || body.ResetsAt != "2026-10-28T21:00:00Z" ||
		body.ChatTurns != (counter{Used: 24, Limit: 30}) || body.Images != (counter{Used: 3, Limit: 30}) || body.Account.Characters != (counter{Used: 6534115, Limit: 25_000_000}) ||
		len(body.OwnedProjects) != 1 || body.OwnedProjects[0].Sources.Characters != (counter{Used: 6534115, Limit: 15_000_000}) {
		t.Fatalf("body = %+v", body)
	}

	router = chi.NewRouter()
	Handlers{AccountQuota: account, Auth: fakeUser{err: auth.ErrUnauthenticated}}.Mount(router)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/account/quota", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous status = %d", rec.Code)
	}
}
