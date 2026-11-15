// SPDX-License-Identifier: AGPL-3.0-only

// Package llmgateway is the local gateway of «Свой ИИ» of the desktop app (specs/2026-11-13-desk-gateway.md GW1): Codex
// of the chat and the calls of the core speak Responses to 127.0.0.1 with the token of the run and a model alias of their
// role, and the gateway puts the profile of the author in — his address, model, key, effort and limits, read on every
// request — and speaks to his provider in its own protocol through the module translate. It answers the request with the
// events of Responses and writes the journal of the request, never its text.
package llmgateway

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"canonkeeper/backend/internal/domain/aisettings"
	"canonkeeper/backend/internal/domain/modeljournal"

	"github.com/canonkeeper/desktop-app/translate"
)

// The aliases of the roles (DA4): Codex of the chat asks for the model of the chat, the core for the model of the reading
// of the books. They never change; the gateway maps them to the models the author named.
const (
	ChatModel  = "canonkeeper-chat"
	CardsModel = "canonkeeper-cards"
)

// path is the one route of the gateway.
const path = "/v1/responses"

// Settings are what the author set for «Свой ИИ» and the key the backend holds, read on every request (GW1.2).
type Settings interface {
	AI() aisettings.Settings
	Key() (string, bool)
}

// Journal is the journal of what went out (specs/2026-11-14-desk-journal.md JR1.1; GW1.7): Send writes the record of a
// sending before its body goes — a record that does not lie sends nothing —, Record the row of its request once it ended,
// with the number of the record; a row that fails fails nothing: the request is answered.
type Journal interface {
	Send(ctx context.Context, sending modeljournal.Sending) error
	Record(ctx context.Context, request modeljournal.Request) error
}

// Gateway is the listener of the run on 127.0.0.1 and what it serves.
type Gateway struct {
	listener   net.Listener
	server     *http.Server
	host       string
	token      string
	settings   Settings
	journal    atomic.Pointer[journalBox]
	reading    atomic.Pointer[readingBox]
	account    atomic.Pointer[accountBox]
	translator *translate.Translator
	threads    *Threads
	gate       *gate
	protocols  *protocols
	logger     *log.Logger
}

type journalBox struct{ journal Journal }

// Open listens on a free port of 127.0.0.1 with a new token of 32 random bytes (GW1.1) and serves until Close. The token
// lives only in memory: the caller hands it to the app-server and the client of the core by value.
func Open(settings Settings, logger *log.Logger) (*Gateway, error) {
	if settings == nil || logger == nil {
		return nil, errors.New("llmgateway: settings and a logger are required")
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil, err
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	// A provider may think for long before its first event, and a stream may last as long as the answer: the client
	// has no timeout, the context of the request ends it.
	client := &http.Client{Transport: &http.Transport{
		Proxy: http.ProxyFromEnvironment, ForceAttemptHTTP2: true, MaxIdleConns: 64, IdleConnTimeout: 90 * time.Second,
		TLSHandshakeTimeout: 15 * time.Second, ResponseHeaderTimeout: 10 * time.Minute,
	}}
	gateway := &Gateway{
		listener: listener, host: listener.Addr().String(), token: hex.EncodeToString(secret), settings: settings,
		translator: translate.New(client), threads: NewThreads(), logger: logger,
	}
	gateway.gate = newGate(gateway.parallel, gateway.readingPlaces)
	gateway.protocols = newProtocols(client)
	gateway.server = &http.Server{Handler: gateway, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		if err := gateway.server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Print(`{"event":"gateway_stopped"}`)
		}
	}()
	return gateway, nil
}

// Port is the port of the listener.
func (gateway *Gateway) Port() int { return gateway.listener.Addr().(*net.TCPAddr).Port }

// BaseURL is the address Codex and the client of the core call: http://127.0.0.1:G/v1.
func (gateway *Gateway) BaseURL() string { return "http://" + gateway.host + "/v1" }

// Token is the token of the run; it goes only by value into the environment of the app-server and the client of the core.
func (gateway *Gateway) Token() string { return gateway.token }

// Threads is the registry «нить → Job» the turn of the chat fills (GW2.4).
func (gateway *Gateway) Threads() *Threads { return gateway.threads }

// UseJournal makes the gateway write the journal once the database of the run is open; until then nothing is sent
// (JR1.1).
func (gateway *Gateway) UseJournal(journal Journal) {
	gateway.journal.Store(&journalBox{journal: journal})
}

// Close stops the listener and the requests it serves.
func (gateway *Gateway) Close() error {
	err := gateway.server.Close()
	_ = gateway.translator.Close()
	return err
}

// parallel is «Запросов одновременно» of the author, read whenever a place is taken or freed (GW1.5); the helper of the
// account has the two places the site keeps for a device (specs/2026-11-13-desk-account-ai.md AI3.3).
func (gateway *Gateway) parallel() int {
	settings := gateway.settings.AI()
	if settings.Source == aisettings.SourceAccount {
		return accountPlaces
	}
	if own := settings.Own; own != nil && own.Advanced.Parallel > 0 {
		return own.Advanced.Parallel
	}
	return 1
}

// readingPlaces bounds the places the reading of the books holds: one of the two of the helper of the account, so the other
// is always free for a turn and its scan (AI3.3); «Свой ИИ» has no bound of its own.
func (gateway *Gateway) readingPlaces() int {
	if gateway.settings.AI().Source == aisettings.SourceAccount {
		return accountReadingPlaces
	}
	return 0
}

// ServeHTTP keeps the gateway to its clients (GW1.1): a Host other than 127.0.0.1:G is 421, a request of a page (with an
// Origin) 403, one without the token 401, anything but POST /v1/responses 404.
func (gateway *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Host != gateway.host:
		writeError(w, http.StatusMisdirectedRequest, "misdirected_request", "the gateway answers only its own address")
	case r.Header.Get("Origin") != "":
		writeError(w, http.StatusForbidden, "forbidden", "the gateway takes no request of a page")
	case !gateway.authorized(r.Header.Get("Authorization")):
		writeError(w, http.StatusUnauthorized, "unauthorized", "the token of the run is required")
	case r.Method != http.MethodPost || r.URL.Path != path:
		writeError(w, http.StatusNotFound, "not_found", "the gateway serves only POST "+path)
	default:
		gateway.responses(w, r)
	}
}

func (gateway *Gateway) authorized(header string) bool {
	presented, ok := strings.CutPrefix(header, "Bearer ")
	return ok && subtle.ConstantTimeCompare([]byte(presented), []byte(gateway.token)) == 1
}

// writeError answers in the shape of an error of Responses: Codex and the client of the core read its code and message.
func writeError(w http.ResponseWriter, status int, code, message string) {
	writeReason(w, status, code, "", message)
}

// writeReason is writeError with the reason of a stop of the reading the client of the app reads
// (specs/2026-11-13-desk-consent.md CN3.4); none is left out.
func writeReason(w http.ResponseWriter, status int, code, reason, message string) {
	type failure struct {
		Type    string `json:"type"`
		Code    string `json:"code"`
		Reason  string `json:"reason,omitempty"`
		Message string `json:"message"`
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(struct {
		Error failure `json:"error"`
	}{failure{Type: code, Code: code, Reason: reason, Message: message}})
}
