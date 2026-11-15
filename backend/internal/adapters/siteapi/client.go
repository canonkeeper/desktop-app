// SPDX-License-Identifier: AGPL-3.0-only

// Package siteapi is the one client of the app to the site CanonKeeper (specs/2026-11-13-desk-account.md AC1.4;
// specs/2026-11-12-desktop-target.md DT11.2): the operations of the app under /api/v1/app/ of the address of the build
// (contracts/openapi.app.json). Only https, TLS 1.2 or later over the roots of the system, never without the check of
// the certificate; no redirection is followed — a 3xx is no connection —; no cookie, no Origin and no query. The terms
// are those of the connection, of TLS and of the headers of the answer, and the whole term of a short operation; a body
// of a stream has none. The client is the one holder of the pass of the device in a request: a new link holds a new
// pass of its own generation, and app.pass_invalid drops the pass only when it came to the pass held (AC2.3); 429 is no
// connection until its Retry-After. Nothing of a code, a verifier or a pass goes into a log or an error.
package siteapi

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	domain "canonkeeper/backend/internal/domain/accountlink"
)

const (
	dialTime    = 10 * time.Second
	tlsTime     = 10 * time.Second
	headersTime = 15 * time.Second
	// shortTime is the whole term of an operation of the link: the exchange, the account and the unlinking.
	shortTime = 20 * time.Second
	// waitWithout is how long a 429 without a readable Retry-After keeps the client from the site.
	waitWithout = time.Minute
	// maxAnswer bounds the answer of a short operation.
	maxAnswer = 1 << 20
	// passMark begins every pass of a device (the site's applink.PassMark).
	passMark = "ckd_"
)

var errSite = errors.New("siteapi: the address of the site is https://host")

// Client is the client of the app to the site of its build.
type Client struct {
	site string
	http *http.Client
	// long is the client of the stream of a turn and of a step: the same roots and rules, without a term of the headers —
	// the site answers them after the call of the model (specs/2026-11-13-desk-account-ai.md AI3.1).
	long *http.Client
	now  func() time.Time

	mu         sync.Mutex
	pass       string
	generation uint64
	notBefore  time.Time
	dropped    func()
}

// New is the client to the site of the address of the build: https://host, nothing else.
func New(site string) (*Client, error) {
	parsed, err := url.Parse(site)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" ||
		parsed.ForceQuery || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return nil, errSite
	}
	return newClient(strings.TrimRight(site, "/"), transport(nil), time.Now), nil
}

// transport is the transport of the client: roots are the roots of the system unless a test of this package names its
// own.
func transport(roots *x509.CertPool) *http.Transport {
	return &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: dialTime, KeepAlive: 30 * time.Second}).DialContext,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots},
		TLSHandshakeTimeout:   tlsTime,
		ResponseHeaderTimeout: headersTime,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          4,
		IdleConnTimeout:       90 * time.Second,
	}
}

func newClient(site string, transport *http.Transport, now func() time.Time) *Client {
	long := transport.Clone()
	long.ResponseHeaderTimeout = 0
	// The stream of a turn goes as the provider sends it: no compression of the answer the client would undo.
	long.DisableCompression = true
	noRedirect := func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{
		site: site, now: now,
		http: &http.Client{Transport: transport, CheckRedirect: noRedirect},
		long: &http.Client{Transport: long, CheckRedirect: noRedirect},
	}
}

// headersTime sets the term of the headers of the short operations; a test of this package shortens it.
func (client *Client) headersTime(term time.Duration) {
	client.http.Transport.(*http.Transport).ResponseHeaderTimeout = term
}

// Hold makes pass the pass of the link, a generation of its own.
func (client *Client) Hold(pass string) {
	client.mu.Lock()
	defer client.mu.Unlock()
	client.pass = pass
	client.generation++
}

// Held says whether the client holds a pass.
func (client *Client) Held() bool {
	client.mu.Lock()
	defer client.mu.Unlock()
	return client.pass != ""
}

// held is the pass and its generation at the moment.
func (client *Client) held() (string, uint64) {
	client.mu.Lock()
	defer client.mu.Unlock()
	return client.pass, client.generation
}

// drop forgets the pass of the generation, and only it; it reports whether it was the pass held.
func (client *Client) drop(generation uint64) bool {
	client.mu.Lock()
	defer client.mu.Unlock()
	if client.generation != generation || client.pass == "" {
		return false
	}
	client.pass = ""
	return true
}

// Exchange changes a code and its verifier for the pass of a new device (exchangeAppLinkCode): the refusal of the site
// is domain.ErrLinkInvalid, an answer that may have been lost — a broken connection, a term, 5xx, an answer that is not
// a pass — domain.ErrAnswerLost, any other answer domain.ErrUnreachable. The pass is not held: the link holds it once
// the shell kept it.
func (client *Client) Exchange(ctx context.Context, exchange domain.Exchange) (string, domain.Account, error) {
	body, _ := json.Marshal(map[string]string{
		"code": exchange.Code, "verifier": exchange.Verifier, "name": exchange.Name, "appVersion": exchange.AppVersion,
	})
	var answer struct {
		Pass    string      `json:"pass"`
		Account accountJSON `json:"account"`
	}
	status, code, err := client.call(ctx, http.MethodPost, "/api/v1/app/link/token", "", body, &answer)
	var later retryAfter
	switch {
	case errors.As(err, &later):
		return "", domain.Account{}, domain.ErrUnreachable
	case err != nil:
		return "", domain.Account{}, domain.ErrAnswerLost
	case status == http.StatusOK:
		if rest, ok := strings.CutPrefix(answer.Pass, passMark); !ok || len(rest) != 43 {
			return "", domain.Account{}, domain.ErrAnswerLost
		}
		return answer.Pass, answer.Account.domain(), nil
	case status == http.StatusBadRequest && code == "app.link_invalid":
		return "", domain.Account{}, domain.ErrLinkInvalid
	case status >= 500:
		return "", domain.Account{}, domain.ErrAnswerLost
	}
	return "", domain.Account{}, domain.ErrUnreachable
}

// Account reads the account of the pass held (getAppAccount): domain.ErrPassInvalid when the site refused it — the
// pass is dropped —, domain.ErrPassReplaced when it refused a pass held before, domain.ErrNotHeld without a request
// when no pass is held, domain.ErrUnreachable for anything else, without a request until a Retry-After passed.
func (client *Client) Account(ctx context.Context) (domain.Account, error) {
	pass, generation := client.held()
	if pass == "" {
		return domain.Account{}, domain.ErrNotHeld
	}
	client.mu.Lock()
	waiting := client.now().Before(client.notBefore)
	client.mu.Unlock()
	if waiting {
		return domain.Account{}, domain.ErrUnreachable
	}
	var answer struct {
		accountJSON
		Quota struct {
			Day       string      `json:"day"`
			ResetsAt  *time.Time  `json:"resetsAt"`
			ChatTurns counterJSON `json:"chatTurns"`
			// ActiveTurns names the places of this app among them since specs/2026-11-15-desk-turns-here.md TH1.1; an older
			// site does not.
			ActiveTurns struct {
				counterJSON
				ThisApp *int64 `json:"thisApp"`
			} `json:"activeTurns"`
			Imports     counterJSON `json:"imports"`
			Reading     counterJSON `json:"reading"`
			ReadingWork counterJSON `json:"readingWork"`
		} `json:"quota"`
	}
	status, code, err := client.call(ctx, http.MethodGet, "/api/v1/app/account", pass, nil, &answer)
	if err != nil {
		var later retryAfter
		if errors.As(err, &later) {
			client.mu.Lock()
			client.notBefore = client.now().Add(later.wait)
			client.mu.Unlock()
		}
		return domain.Account{}, domain.ErrUnreachable
	}
	switch {
	case status == http.StatusOK:
		account := answer.accountJSON.domain()
		account.Questions, account.Reading, account.Quota = answer.Quota.ChatTurns.domain(), answer.Quota.Reading.domain(), true
		account.ActiveTurns, account.Imports, account.Work = answer.Quota.ActiveTurns.domain(), answer.Quota.Imports.domain(), answer.Quota.ReadingWork.domain()
		account.ThisApp = answer.Quota.ActiveTurns.ThisApp
		account.Day, account.ResetsAt = answer.Quota.Day, answer.Quota.ResetsAt
		return account, nil
	case status == http.StatusUnauthorized && code == "app.pass_invalid":
		if client.drop(generation) {
			return domain.Account{}, domain.ErrPassInvalid
		}
		return domain.Account{}, domain.ErrPassReplaced
	}
	return domain.Account{}, domain.ErrUnreachable
}

// Unlink stops using the pass held at once and tells the site (unlinkApp); it reports whether the site heard it — 204.
func (client *Client) Unlink(ctx context.Context) bool {
	pass, generation := client.held()
	if pass == "" {
		return false
	}
	client.drop(generation)
	status, _, err := client.call(ctx, http.MethodDelete, "/api/v1/app/device", pass, nil, nil)
	return err == nil && status == http.StatusNoContent
}

// retryAfter is a 429 and how long the site asked to wait.
type retryAfter struct{ wait time.Duration }

func (later retryAfter) Error() string { return "siteapi: the site asked to wait" }

// call sends one request of a short operation and reads its answer: its status, the code of a problem and, for 200,
// the JSON into answer. A transport failure, a term and an answer that cannot be read are an error; 429 is retryAfter.
func (client *Client) call(ctx context.Context, method, path, pass string, body []byte, answer any) (int, string, error) {
	ctx, cancel := context.WithTimeout(ctx, shortTime)
	defer cancel()
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	request, err := http.NewRequestWithContext(ctx, method, client.site+path, reader)
	if err != nil {
		return 0, "", err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "CanonKeeper")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if pass != "" {
		request.Header.Set("Authorization", "Bearer "+pass)
	}
	response, err := client.http.Do(request)
	if err != nil {
		return 0, "", errors.New("siteapi: no answer")
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxAnswer))
	if err != nil {
		return 0, "", errors.New("siteapi: the answer broke")
	}
	var problem struct {
		Code string `json:"code"`
	}
	if response.StatusCode >= 400 {
		_ = json.Unmarshal(raw, &problem)
	}
	switch {
	case response.StatusCode == http.StatusTooManyRequests && (strings.HasPrefix(problem.Code, "quota.") || problem.Code == "ai.busy"):
		// A refusal of the tier and ai.busy answer their operation and keep the client from nothing
		// (specs/2026-11-13-desk-account-ai.md AI3.3).
		return response.StatusCode, problem.Code, nil
	case response.StatusCode == http.StatusTooManyRequests:
		return response.StatusCode, "", retryAfter{wait: waitOf(response.Header.Get("Retry-After"), client.now())}
	case response.StatusCode == http.StatusOK && answer != nil:
		if json.Unmarshal(raw, answer) != nil {
			return 0, "", errors.New("siteapi: the answer is not JSON")
		}
	case response.StatusCode >= 400:
		return response.StatusCode, problem.Code, nil
	}
	return response.StatusCode, "", nil
}

// waitOf is the wait of a Retry-After in seconds or as a date; without one, a minute.
func waitOf(header string, now time.Time) time.Duration {
	if seconds, err := strconv.Atoi(strings.TrimSpace(header)); err == nil && seconds >= 0 {
		return time.Duration(seconds) * time.Second
	}
	if at, err := http.ParseTime(header); err == nil && at.After(now) {
		return at.Sub(now)
	}
	return waitWithout
}

type tierJSON struct {
	Code  string     `json:"code"`
	Title string     `json:"title"`
	Until *time.Time `json:"until"`
}

type counterJSON struct {
	Used  int64 `json:"used"`
	Limit int64 `json:"limit"`
}

func (counter counterJSON) domain() domain.Counter {
	return domain.Counter{Used: counter.Used, Limit: counter.Limit}
}

// accountJSON is AppAccount of the contract; a field this app does not know is skipped (DT12.3).
type accountJSON struct {
	Name        string      `json:"name"`
	Email       string      `json:"email"`
	Tier        tierJSON    `json:"tier"`
	Reading     counterJSON `json:"reading"`
	Helper      helperJSON  `json:"helper"`
	ReadingWork counterJSON `json:"readingWork"`
}

// helperJSON is AppHelper of the contract (specs/2026-11-13-desk-account-ai.md AI5.1).
type helperJSON struct {
	ChatModel     string `json:"chatModel"`
	CardsModel    string `json:"cardsModel"`
	ContextWindow int64  `json:"contextWindow"`
	CompactLimit  int64  `json:"compactLimit"`
}

func (account accountJSON) domain() domain.Account {
	return domain.Account{
		Name: account.Name, Email: account.Email,
		Tier:    domain.Tier{Code: account.Tier.Code, Title: account.Tier.Title, Until: account.Tier.Until},
		Reading: account.Reading.domain(),
		Helper: domain.Helper{
			ChatModel: account.Helper.ChatModel, CardsModel: account.Helper.CardsModel,
			ContextWindow: account.Helper.ContextWindow, CompactLimit: account.Helper.CompactLimit,
		},
		Work: account.ReadingWork.domain(),
	}
}
