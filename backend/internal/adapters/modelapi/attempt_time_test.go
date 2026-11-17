package modelapi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"canonkeeper/backend/internal/domain/sagamemory"
)

// slowServer answers each request by the next of its steps: "headers" hangs before the headers of the answer, "body" hangs
// while the answer is read, "503" refuses, "close" drops the connection, "ok" answers; the last step stays. It keeps the header of the last attempt.
type slowServer struct {
	mu    sync.Mutex
	steps []string
	asked []string
}

func (slow *slowServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// The body read, the server hears the client go and ends the context of the request.
	_, _ = io.Copy(io.Discard, r.Body)
	slow.mu.Lock()
	step := slow.steps[min(len(slow.asked), len(slow.steps)-1)]
	slow.asked = append(slow.asked, r.Header.Get(LastAttemptHeader))
	slow.mu.Unlock()
	switch step {
	case "headers":
		<-r.Context().Done()
	case "body":
		fmt.Fprint(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"нача\"}\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	case "503":
		http.Error(w, "overloaded", http.StatusServiceUnavailable)
	case "close":
		connection, _, _ := w.(http.Hijacker).Hijack()
		_ = connection.Close()
	default:
		fmt.Fprint(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}\n\ndata: {\"type\":\"response.completed\"}\n\n")
	}
}

func (slow *slowServer) requests() []string {
	slow.mu.Lock()
	defer slow.mu.Unlock()
	return append([]string(nil), slow.asked...)
}

// specs/2026-11-17-long-chapters.md LC2.1: an attempt of the client of the site its own time cut — before the headers or while
// the answer was read — is repeated once; the second such attempt ends the call with the error of the time, without new
// attempts. The other repeats are as before, and so is a call whose own deadline comes first.
func TestAnAttemptOfTheSiteItsTimeCutIsRepeatedOnce(t *testing.T) {
	site := func(server *slowServer) *Client {
		listener := httptest.NewServer(server)
		t.Cleanup(listener.Close)
		client, err := New(Config{BaseURL: listener.URL, Model: "m"})
		if err != nil {
			t.Fatal(err)
		}
		if client.attemptTime != 45*time.Minute {
			t.Fatalf("the attempt of the site lasts %s", client.attemptTime)
		}
		client.attemptTime, client.pause = 50*time.Millisecond, func(int) time.Duration { return 0 }
		return client
	}
	for _, steps := range [][]string{{"headers"}, {"body"}, {"headers", "body"}} {
		server := &slowServer{steps: steps}
		_, _, err := site(server).Background().Complete(t.Context(), "инструкция", "вход")
		if !errors.Is(err, sagamemory.ErrAttemptTimeout) || len(server.requests()) != 2 {
			t.Fatalf("%v: %v after %d requests", steps, err, len(server.requests()))
		}
	}
	server := &slowServer{steps: []string{"body", "ok"}}
	if text, _, err := site(server).Background().Complete(t.Context(), "инструкция", "вход"); err != nil || text != "ok" || len(server.requests()) != 2 {
		t.Fatalf("a repeat after the time = %q, %v after %d requests", text, err, len(server.requests()))
	}
	server = &slowServer{steps: []string{"503", "headers", "503", "headers", "ok"}}
	if _, _, err := site(server).Background().Complete(t.Context(), "инструкция", "вход"); !errors.Is(err, sagamemory.ErrAttemptTimeout) || len(server.requests()) != 4 {
		t.Fatalf("refusals between the times = %v after %d requests", err, len(server.requests()))
	}
	for _, step := range []string{"503", "close"} {
		server = &slowServer{steps: []string{step}}
		if _, _, err := site(server).Background().Complete(t.Context(), "инструкция", "вход"); err == nil || errors.Is(err, sagamemory.ErrAttemptTimeout) || len(server.requests()) != attempts {
			t.Fatalf("%s = %v after %d requests", step, err, len(server.requests()))
		}
	}
	server = &slowServer{steps: []string{"headers"}}
	client := site(server)
	client.attemptTime = time.Hour
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	if _, _, err := client.Interactive().Complete(ctx, "инструкция", "вход"); errors.Is(err, sagamemory.ErrAttemptTimeout) || len(server.requests()) != 1 {
		t.Fatalf("a call shorter than its attempt = %v after %d requests", err, len(server.requests()))
	}
}

// LC2.1: an attempt of the client of the app lasts 30 minutes, and one its time cut is repeated as before — six attempts, the
// last marked for the gateway, and then no connection is the stop «связь».
func TestAnAttemptOfTheAppItsTimeCutIsRepeatedAsBefore(t *testing.T) {
	server := &slowServer{steps: []string{"headers"}}
	listener := httptest.NewServer(server)
	defer listener.Close()
	app, err := New(Config{BaseURL: listener.URL + "/v1", Key: "key-test-gateway-0009", Model: "canonkeeper-cards", CarryJob: true, Stops: true})
	if err != nil {
		t.Fatal(err)
	}
	if app.attemptTime != 30*time.Minute {
		t.Fatalf("the attempt of the app lasts %s", app.attemptTime)
	}
	app.attemptTime, app.pause = 50*time.Millisecond, func(int) time.Duration { return 0 }
	_, _, err = app.Background().Complete(t.Context(), "инструкция", "вход")
	if stop, ok := sagamemory.StopOf(err); !ok || stop != sagamemory.StopConnection {
		t.Fatalf("six attempts of the app cut by their time = %v", err)
	}
	if asked := server.requests(); len(asked) != attempts || asked[attempts-1] != "1" || asked[0] != "" || asked[attempts-2] != "" {
		t.Fatalf("the attempts of the app said %q", asked)
	}
}

// LC2.1: a round of a turn with tools of the site (the assistant of the voice) holds the time of its attempt too: the time
// cut before the headers or while the answer is read is repeated once, and the second ends the turn with the error of the
// time.
func TestARoundOfATurnHoldsTheTimeOfItsAttempt(t *testing.T) {
	for _, steps := range [][]string{{"headers"}, {"body"}} {
		server := &slowServer{steps: steps}
		listener := httptest.NewServer(server)
		client, err := New(Config{BaseURL: listener.URL, Model: "m", VoiceBrain: "brain"})
		if err != nil {
			t.Fatal(err)
		}
		client.attemptTime, client.pause = 50*time.Millisecond, func(int) time.Duration { return 0 }
		_, _, err = client.Background().Act(t.Context(), "инструкция", "вход", nil, nil)
		listener.Close()
		if !errors.Is(err, sagamemory.ErrAttemptTimeout) || len(server.requests()) != 2 {
			t.Fatalf("%v: a turn = %v after %d requests", steps, err, len(server.requests()))
		}
	}
}
