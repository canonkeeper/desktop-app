package notes

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"canonkeeper/backend/internal/auth"
	domain "canonkeeper/backend/internal/domain/notes"
	service "canonkeeper/backend/internal/services/notes"
	"github.com/go-chi/chi/v5"
)

const userID = "00000000-0000-4000-8000-000000000001"
const noteID = "00000000-0000-4000-8000-000000000002"

type testAuth struct {
	user  string
	calls int
}

func (a *testAuth) CurrentUser(context.Context, string) (auth.User, error) {
	if a.user == "" {
		return auth.User{}, auth.ErrUnauthenticated
	}
	return auth.User{ID: a.user}, nil
}
func (a *testAuth) AuthenticateWrite(ctx context.Context, cookie, csrf string) (auth.User, auth.SessionWriteProof, error) {
	a.calls++
	user, err := a.CurrentUser(ctx, cookie)
	if err != nil {
		return user, auth.SessionWriteProof{}, err
	}
	if csrf != "valid-token" {
		return user, auth.SessionWriteProof{}, auth.ErrInvalidCSRF
	}
	return user, auth.SessionWriteProof{SessionHash: "fixture", CSRFSecretHash: "fixture"}, nil
}

type testRepository struct {
	writes int
	body   string
}

func (repo *testRepository) List(context.Context, service.ListQuery) (service.ListResult, error) {
	return service.ListResult{}, nil
}
func (repo *testRepository) Get(_ context.Context, user, id string) (domain.Note, error) {
	if user != userID || id != noteID {
		return domain.Note{}, domain.ErrNotFound
	}
	return domain.Note{ID: id, Body: "owned", Revision: 1}, nil
}
func (repo *testRepository) Create(_ context.Context, c service.CreateCommand) (domain.Note, error) {
	repo.writes++
	repo.body = c.Body
	return domain.Note{ID: noteID, Body: c.Body, Source: c.Source, Revision: 1}, nil
}
func (repo *testRepository) Update(ctx context.Context, a service.Write, id string, i service.UpdateInput) (domain.Note, error) {
	if _, err := repo.Get(ctx, a.UserID, id); err != nil {
		return domain.Note{}, err
	}
	if i.ExpectedRevision != 1 {
		return domain.Note{}, domain.ErrConflict
	}
	repo.writes++
	value := domain.Note{ID: id, Body: "owned", Revision: 2}
	if i.Body != nil {
		value.Body = *i.Body
	}
	if i.Title != nil {
		value.Title = *i.Title
	}
	return value, nil
}
func (repo *testRepository) Delete(ctx context.Context, a service.Write, id string, revision int64) error {
	if _, err := repo.Get(ctx, a.UserID, id); err != nil {
		return err
	}
	if revision != 1 {
		return domain.ErrConflict
	}
	repo.writes++
	return nil
}
func (repo *testRepository) ReadSource(ctx context.Context, user, id string) (service.SourceContext, error) {
	_, err := repo.Get(ctx, user, id)
	return service.SourceContext{Resolution: domain.Resolution{State: "unavailable"}}, err
}
func TestHTTPNotesAuthCSRFStrictInputAndRevision(t *testing.T) {
	authn := &testAuth{user: userID}
	repo := &testRepository{}
	router := chi.NewRouter()
	(Handlers{List: service.List{Repository: repo}, Get: service.Get{Repository: repo}, Create: service.Create{Repository: repo},
		Update: service.Update{Repository: repo}, Delete: service.Delete{Repository: repo}, Resolve: service.Resolve{Repository: repo},
		Auth: authn, AllowedOrigins: []string{"http://localhost"}}).Mount(router)
	for _, test := range []struct {
		name, method, path, body, csrf, origin, user string
		status                                       int
	}{
		{"unauthenticated", "POST", "/notes/query", "{}", "", "http://localhost", "", 401},
		{"old route removed", "GET", "/notes", "", "", "", userID, 404},
		{"private query", "POST", "/notes/query", `{"query":"text"}`, "valid-token", "http://localhost", userID, 200},
		{"null query body", "POST", "/notes/query", "null", "valid-token", "http://localhost", userID, 400},
		{"null query field", "POST", "/notes/query", `{"query":null}`, "valid-token", "http://localhost", userID, 400},
		{"zero explicit limit", "POST", "/notes/query", `{"limit":0}`, "valid-token", "http://localhost", userID, 400},
		{"query in URL denied", "POST", "/notes/query?query=text", "{}", "valid-token", "http://localhost", userID, 400},
		{"book requires saga", "POST", "/notes/query", `{"bookId":"00000000-0000-4000-8000-000000000003"}`, "valid-token", "http://localhost", userID, 400},
		{"csrf required", "POST", "/personal-notes", `{"body":"copy"}`, "", "http://localhost", userID, 403},
		{"origin denied", "POST", "/personal-notes", `{"body":"copy"}`, "valid-token", "http://evil.test", userID, 403},
		{"unknown owner", "POST", "/personal-notes", `{"body":"copy","ownerUserId":"other"}`, "valid-token", "http://localhost", userID, 400},
		{"empty", "POST", "/personal-notes", `{"body":"  "}`, "valid-token", "http://localhost", userID, 400},
		{"null title", "POST", "/personal-notes", `{"body":"copy","title":null}`, "valid-token", "http://localhost", userID, 400},
		{"long title", "POST", "/personal-notes", `{"body":"copy","title":"` + strings.Repeat("Я", 201) + `"}`, "valid-token", "http://localhost", userID, 400},
		{"invalid UTF-8", "POST", "/personal-notes", "{\"body\":\"" + string([]byte{0xff}) + "\"}", "valid-token", "http://localhost", userID, 400},
		{"oversize", "POST", "/personal-notes", `{"body":"` + strings.Repeat("я", 100001) + `"}`, "valid-token", "http://localhost", userID, 400},
		{"create", "POST", "/personal-notes", `{"body":"<script>private copy</script>"}`, "valid-token", "http://localhost", userID, 201},
		{"immutable source", "PATCH", "/personal-notes/" + noteID, `{"body":"copy","expectedRevision":1,"source":null}`, "valid-token", "http://localhost", userID, 400},
		{"title-only update", "PATCH", "/personal-notes/" + noteID, `{"title":"Title","expectedRevision":1}`, "valid-token", "http://localhost", userID, 200},
		{"null patch", "PATCH", "/personal-notes/" + noteID, `{"title":null,"expectedRevision":1}`, "valid-token", "http://localhost", userID, 400},
		{"empty patch", "PATCH", "/personal-notes/" + noteID, `{"expectedRevision":1}`, "valid-token", "http://localhost", userID, 400},
		{"revision conflict", "PATCH", "/personal-notes/" + noteID, `{"body":"copy","expectedRevision":2}`, "valid-token", "http://localhost", userID, 409},
		{"foreign get", "GET", "/personal-notes/" + noteID, "", "", "", "00000000-0000-4000-8000-000000000099", 404},
		{"foreign source", "GET", "/personal-notes/" + noteID + "/source", "", "", "", "00000000-0000-4000-8000-000000000099", 404},
		{"unavailable source", "GET", "/personal-notes/" + noteID + "/source", "", "", "", userID, 200},
		{"delete needs revision", "DELETE", "/personal-notes/" + noteID, "", "valid-token", "http://localhost", userID, 400},
		{"delete", "DELETE", "/personal-notes/" + noteID + "?expectedRevision=1", "", "valid-token", "http://localhost", userID, 204},
	} {
		t.Run(test.name, func(t *testing.T) {
			authn.user = test.user
			request := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body))
			request.Header.Set("Origin", test.origin)
			request.Header.Set("X-CSRF-Token", test.csrf)
			request.Header.Set("Idempotency-Key", "request-key")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != test.status {
				t.Fatalf("status=%d want=%d body=%s", response.Code, test.status, response.Body.String())
			}
			if test.status == 200 && strings.HasSuffix(test.path, "/source") {
				var resolved domain.Resolution
				if json.Unmarshal(response.Body.Bytes(), &resolved) != nil || resolved.State != "unavailable" || resolved.Source != nil {
					t.Fatal("unavailable source leaked")
				}
			}
		})
	}
	if repo.writes != 3 || repo.body != "<script>private copy</script>" {
		t.Fatalf("unexpected writes=%d or altered body", repo.writes)
	}
}
