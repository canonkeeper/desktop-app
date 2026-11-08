package indexing

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"canonkeeper/backend/internal/auth"
	"canonkeeper/backend/internal/domain/canon"
	indexingdomain "canonkeeper/backend/internal/domain/indexing"
	indexingservice "canonkeeper/backend/internal/services/indexing"

	"github.com/go-chi/chi/v5"
)

func TestStatusHandlerForwardsAuthenticatedBookScope(t *testing.T) {
	port := &fakeStatusPort{}
	service := indexingservice.NewService(port)
	handler := NewStatusHandler(service, fakeCurrentUser{})
	router := chi.NewRouter()
	handler.Mount(router)
	request := httptest.NewRequest(http.MethodGet, "/projects/project/indexing-status?bookId=book&sourceView=draft", nil)
	response := httptest.NewRecorder()

	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if port.input.ActorUserID != "actor" || port.input.ProjectID != "project" ||
		port.input.BookID != "book" || port.input.SourceView != indexingdomain.SourceDraft {
		t.Fatalf("input=%+v", port.input)
	}
}

func TestStatusHandlerPreservesUnscopedPublicContract(t *testing.T) {
	port := &fakeStatusPort{}
	service := indexingservice.NewService(port)
	handler := NewStatusHandler(service, fakeCurrentUser{})
	router := chi.NewRouter()
	handler.Mount(router)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/projects/project/indexing-status", nil))

	if response.Code != http.StatusOK || port.input.BookID != "" || port.input.SourceView != "" ||
		response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("status=%d headers=%v input=%+v body=%s", response.Code, response.Header(), port.input, response.Body.String())
	}
	for _, field := range [][]byte{[]byte(`"published"`), []byte(`"steps"`)} {
		if !bytes.Contains(response.Body.Bytes(), field) {
			t.Fatalf("missing %s: %s", field, response.Body.String())
		}
	}
}

func TestStatusHandlerDoesNotReintroduceViewerDraft(t *testing.T) {
	port := &fakeStatusPort{response: canon.IndexingStatusResponse{
		Published: canon.IndexingViewEnvelope{Exists: true}, Draft: nil, Steps: []canon.IndexingStep{},
	}}
	service := indexingservice.NewService(port)
	handler := NewStatusHandler(service, fakeCurrentUser{})
	router := chi.NewRouter()
	handler.Mount(router)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/projects/project/indexing-status", nil))

	if response.Code != http.StatusOK || bytes.Contains(response.Body.Bytes(), []byte(`"draft"`)) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

type fakeCurrentUser struct{}

func (fakeCurrentUser) CurrentUser(context.Context, string) (auth.User, error) {
	return auth.User{ID: "actor"}, nil
}

type fakeStatusPort struct {
	input    indexingservice.GetStatusInput
	response canon.IndexingStatusResponse
}

func (port *fakeStatusPort) GetStatus(_ context.Context, input indexingservice.GetStatusInput) (canon.IndexingStatusResponse, error) {
	port.input = input
	if port.response.Steps == nil {
		port.response = canon.IndexingStatusResponse{
			Published: canon.IndexingViewEnvelope{Exists: true}, Steps: []canon.IndexingStep{},
		}
	}
	return port.response, nil
}
