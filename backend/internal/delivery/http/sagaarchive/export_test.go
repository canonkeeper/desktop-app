// SPDX-License-Identifier: AGPL-3.0-only

package sagaarchive

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"canonkeeper/backend/internal/adapters/tempfiles"
	"canonkeeper/backend/internal/auth"
	agentdomain "canonkeeper/backend/internal/domain/agent"
	timeline "canonkeeper/backend/internal/domain/agenttimeline"
	domain "canonkeeper/backend/internal/domain/sagaarchive"
	service "canonkeeper/backend/internal/services/sagaarchive"

	"github.com/go-chi/chi/v5"
)

// sessions names the author of a request by the value of its cookie.
type sessions struct{}

func (sessions) CurrentUser(_ context.Context, cookie string) (auth.User, error) {
	if cookie == "" {
		return auth.User{}, auth.ErrUnauthenticated
	}
	return auth.User{ID: cookie}, nil
}

func (sessions) AuthenticateWrite(ctx context.Context, cookie, csrf string) (auth.User, auth.SessionWriteProof, error) {
	user, err := sessions{}.CurrentUser(ctx, cookie)
	if err == nil && csrf != "token" {
		err = auth.ErrInvalidCSRF
	}
	return user, auth.SessionWriteProof{SessionHash: "session", CSRFSecretHash: "csrf"}, err
}

// saga is a saga of three members: the owner, an editor and nobody else.
type saga struct{ broken bool }

func (saga saga) Snapshot(_ context.Context, actor, _ string, read func(service.Snapshot) error) error {
	switch actor {
	case "owner", "other-owner":
		return read(saga)
	case "editor":
		return service.ErrForbidden
	}
	return service.ErrNotFound
}

func (saga) Saga() (domain.Saga, error) {
	return domain.Saga{Name: "Северный предел"}, nil
}
func (saga) Books() ([]service.SnapshotBook, error) {
	return nil, nil
}
func (saga) Chapters(func(domain.Chapter) error) error    { return nil }
func (saga) Cards(func(domain.Card) error) error          { return nil }
func (saga) Names() (*domain.Names, error)                { return nil, nil }
func (saga) Layer() (*service.Layer, error)               { return nil, nil }
func (saga) MapHidden() ([]domain.MapHidden, error)       { return nil, nil }
func (saga) Materials() ([]domain.Material, error)        { return nil, nil }
func (saga) Memory() ([]domain.Memory, error)             { return nil, nil }
func (saga) Notes(string) ([]service.SnapshotNote, error) { return nil, nil }
func (saga) Transcripts() ([]service.Transcript, error)   { return nil, nil }
func (saga saga) Chats(string) ([]service.ChatSource, error) {
	if saga.broken {
		return []service.ChatSource{{ID: "chat", Title: "Чат", ThreadID: "thread", CreatedAt: time.Now(), Turns: []service.TurnSource{{ProviderTurnID: "t", Mine: true}}}}, nil
	}
	return nil, nil
}
func (saga) LeftOut() (domain.LeftOut, error) { return domain.LeftOut{}, nil }

// threads that hold the reader until he lets them go; entered hears that a save reads one.
type threads struct{ hold, entered chan struct{} }

func (threads threads) ThreadItems(ctx context.Context, _ agentdomain.Flow, _ string) ([]timeline.RawItem, error) {
	select {
	case threads.entered <- struct{}{}:
	default:
	}
	select {
	case <-threads.hold:
	case <-ctx.Done():
	}
	return nil, errors.New("no thread")
}

type temp struct{ directory *tempfiles.Directory }

func (port temp) Create() (service.TempFile, error) { return port.directory.Create() }

func newServer(t *testing.T, snapshot service.SnapshotStore, hold, entered chan struct{}) *httptest.Server {
	t.Helper()
	directory, err := tempfiles.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	MountExport(router, &service.ExportSaga{
		Snapshot: snapshot, Threads: threads{hold: hold, entered: entered}, Temp: temp{directory}, Gate: service.NewGate(nil),
		CreatedBy: "canonkeeper.ru", Now: time.Now,
	}, sessions{})
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	return server
}

func get(t *testing.T, server *httptest.Server, author string) (*http.Response, []byte, error) {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, server.URL+"/projects/saga/saga-archive", nil)
	if err != nil {
		t.Fatal(err)
	}
	if author != "" {
		request.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: author})
	}
	response, err := server.Client().Do(request)
	if err != nil {
		return nil, nil, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	return response, body, err
}

// specs/2026-11-13-desk-sagafile.md SF2.1, SF2.4: someone outside the saga gets 404, an editor 403, the owner a zip the reader
// of the format takes, named by the saga; a second save of the author while his first runs is 429, another author's goes.
func TestTheSaveOfASagaAnswersItsOwnerOnly(t *testing.T) {
	hold := make(chan struct{})
	server := newServer(t, saga{}, hold, nil)
	for author, status := range map[string]int{"": http.StatusUnauthorized, "stranger": http.StatusNotFound, "editor": http.StatusForbidden} {
		if response, _, err := get(t, server, author); err != nil || response.StatusCode != status {
			t.Fatalf("a save by %q = %v, %v; want %d", author, response, err, status)
		}
	}
	response, body, err := get(t, server, "owner")
	if err != nil || response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != "application/zip" ||
		response.Header.Get("Cache-Control") != "no-store" || !strings.Contains(response.Header.Get("Content-Disposition"), ".cksaga") {
		t.Fatalf("the save of the owner = %+v, %v", response, err)
	}
	if _, err := domain.Read(bytes.NewReader(body), int64(len(body))); err != nil {
		t.Fatalf("the file of the save: %v", err)
	}

	entered := make(chan struct{}, 1)
	slow := newServer(t, saga{broken: true}, hold, entered)
	results := make(chan int, 2)
	save := func(author string) {
		response, _, err := get(t, slow, author)
		if err != nil {
			results <- 0
			return
		}
		results <- response.StatusCode
	}
	go save("owner")
	<-entered
	if response, _, err := get(t, slow, "owner"); err != nil || response.StatusCode != http.StatusTooManyRequests || response.Header.Get("Retry-After") == "" {
		t.Fatalf("a second save of the author while his first runs = %+v, %v; want 429 with Retry-After", response, err)
	}
	go save("other-owner")
	<-entered
	close(hold)
	for index := 0; index < 2; index++ {
		if status := <-results; status != http.StatusOK {
			t.Fatalf("a save while another author's runs = %d; want both to go", status)
		}
	}
}
