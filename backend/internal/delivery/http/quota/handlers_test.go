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
		ImageUploads: 2, ChapterDrafts: 5, DrawingTurns: 7, DraftTurns: 11, DraftRewrites: 13,
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

	// specs/2026-11-10-ux2-tariff-account.md TA2.3: every counter of a day a refusal can meet stands in the answer, read
	// by the same fence as the limits — the page «Тариф» counts nothing itself.
	if body.ImageUploads != (counter{Used: 2, Limit: 30}) || body.ChapterDrafts != (counter{Used: 5, Limit: 20}) ||
		body.DrawingTurns != (counter{Used: 7, Limit: 60}) || body.DraftTurns != (counter{Used: 11, Limit: 60}) ||
		body.DraftRewrites != (counter{Used: 13, Limit: 200}) {
		t.Fatalf("the counters of the day = %+v", body)
	}

	router = chi.NewRouter()
	Handlers{AccountQuota: account, Auth: fakeUser{err: auth.ErrUnauthenticated}}.Mount(router)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/account/quota", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous status = %d", rec.Code)
	}
}

type accountFunc func() domain.Account

func (answer accountFunc) Execute(context.Context, string) (domain.Account, error) {
	return answer(), nil
}

// specs/2026-11-15-desk-turns-here.md TH2.1, TH2.4: «разом» carries who holds its places only when the build tells them — the
// app with the helper of the account —; without them the counter is used and limit, as it was.
func TestTheActiveTurnsNameTheirPlacesOnlyWhenTold(t *testing.T) {
	read := func(account domain.Account) map[string]json.RawMessage {
		t.Helper()
		router := chi.NewRouter()
		Handlers{AccountQuota: accountFunc(func() domain.Account { return account }), Auth: fakeUser{}}.Mount(router)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/account/quota", nil))
		var body struct {
			ActiveTurns map[string]json.RawMessage `json:"activeTurns"`
		}
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &body) != nil {
			t.Fatalf("status = %d body = %s", rec.Code, rec.Body)
		}
		return body.ActiveTurns
	}
	account := domain.Account{Limits: domain.Defaults(), ActiveTurns: 3}
	if site := read(account); len(site) != 2 || string(site["used"]) != "3" || site["places"] != nil {
		t.Fatalf("«разом» without places = %v", site)
	}
	account.Places = &domain.ActivePlaces{Answering: 1, Held: 1, Elsewhere: 1}
	if app := read(account); len(app) != 3 || string(app["places"]) != `{"answering":1,"held":1,"elsewhere":1}` {
		t.Fatalf("«разом» with places = %v", app)
	}
}

// specs/2026-11-18-free-tier.md FT1.5: the answer says whether a book of the writer loads again and how many books ever came
// into his sagas — a book loaded once says false, the code and the desktop true.
func TestTheAnswerSaysWhetherABookLoadsAgainAndTheBooksLoaded(t *testing.T) {
	for _, reloads := range []int64{1, 0} {
		account := accountFunc(func() domain.Account {
			return domain.Account{Limits: domain.Defaults().With(domain.KeyBookReloads, reloads), BooksLoaded: 3,
				Sources: domain.Sources{Books: 1}}
		})
		router := chi.NewRouter()
		Handlers{AccountQuota: account, Auth: fakeUser{}}.Mount(router)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/account/quota", nil))
		var body map[string]json.RawMessage
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &body) != nil {
			t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
		}
		want := `true`
		if reloads == 0 {
			want = `false`
		}
		if string(body["bookReloads"]) != want || string(body["booksLoaded"]) != "3" {
			t.Fatalf("book_reloads %d: bookReloads %s, booksLoaded %s", reloads, body["bookReloads"], body["booksLoaded"])
		}
	}
}
