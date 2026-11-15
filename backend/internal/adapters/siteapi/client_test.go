// SPDX-License-Identifier: AGPL-3.0-only

package siteapi

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	domain "canonkeeper/backend/internal/domain/accountlink"
)

const (
	passOfTheTest  = "ckd_" + "pass-of-the-test-north-00000000000000000000"
	otherPass      = "ckd_" + "pass-of-the-test-south-11111111111111111111"
	codeOfTheTest  = "code-of-the-test-north-00000000000000000000"
	verifierOfTest = "verifier-of-the-test-north-0000000000000000"
)

// stub is a site of the test over TLS: what it heard and what it answers.
type stub struct {
	server *httptest.Server
	mu     sync.Mutex
	heard  []*http.Request
	bodies []string
	answer func(w http.ResponseWriter, r *http.Request)
}

func newStub(t *testing.T, answer func(w http.ResponseWriter, r *http.Request)) *stub {
	t.Helper()
	site := &stub{answer: answer}
	site.server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		site.mu.Lock()
		site.heard = append(site.heard, r.Clone(context.Background()))
		site.bodies = append(site.bodies, string(body))
		answer := site.answer
		site.mu.Unlock()
		answer(w, r)
	}))
	t.Cleanup(site.server.Close)
	return site
}

func (site *stub) requests() []*http.Request {
	site.mu.Lock()
	defer site.mu.Unlock()
	return append([]*http.Request(nil), site.heard...)
}

func (site *stub) set(answer func(w http.ResponseWriter, r *http.Request)) {
	site.mu.Lock()
	defer site.mu.Unlock()
	site.answer = answer
}

// trusted is the client of the app over the stub, trusting its certificate as the computer of the author trusts the
// certificate of the site; everything else is the product's.
func trusted(site *stub, now func() time.Time) *Client {
	roots := x509.NewCertPool()
	roots.AddCert(site.server.Certificate())
	return newClient(site.server.URL, transport(roots), now)
}

func problem(status int, code string) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]any{"status": status, "code": code, "unknown": "field"})
	}
}

func token(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, `{"pass":"`+passOfTheTest+`","account":{"name":"Северный","email":"a…@example.test",
		"tier":{"code":"writer","title":"Писатель","until":"2026-12-13T00:00:00Z","later":1},
		"reading":{"used":3200000,"limit":40000000},"newField":{"x":1}}}`)
}

func account(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, `{"name":"Северный","email":"a…@example.test","tier":{"code":"free","title":"Бесплатно","until":null},
		"quota":{"day":"2026-11-13","resetsAt":"2026-11-13T21:00:00Z","chatTurns":{"used":3,"limit":20},
		"activeTurns":{"used":0,"limit":1},"imports":{"used":0,"limit":5},"reading":{"used":120,"limit":1000000}},
		"unknown":true}`)
}

// specs/2026-11-13-desk-account.md AC1.3, AC1.4: the exchange sends the code, the verifier, the name and the version to
// /api/v1/app/link/token of the address of the build, without a cookie, an Origin or a query, and reads an answer with
// fields it does not know.
func TestTheExchangeAsksTheSiteOfTheBuildAndReadsANewerAnswer(t *testing.T) {
	site := newStub(t, token)
	client := trusted(site, time.Now)
	pass, got, err := client.Exchange(context.Background(), domain.Exchange{Code: codeOfTheTest, Verifier: verifierOfTest, Name: "Рабочий стол", AppVersion: "1.3.0"})
	if err != nil || pass != passOfTheTest {
		t.Fatalf("Exchange = %v", err)
	}
	if got.Name != "Северный" || got.Email != "a…@example.test" || got.Tier.Code != "writer" || got.Tier.Until == nil ||
		got.Reading != (domain.Counter{Used: 3200000, Limit: 40000000}) || got.Quota {
		t.Fatalf("account = %+v", got)
	}
	heard := site.requests()
	if len(heard) != 1 {
		t.Fatalf("the site heard %d requests", len(heard))
	}
	request := heard[0]
	if request.Method != http.MethodPost || request.URL.Path != "/api/v1/app/link/token" || request.URL.RawQuery != "" ||
		request.Header.Get("Origin") != "" || request.Header.Get("Cookie") != "" || request.Header.Get("Authorization") != "" {
		t.Fatalf("the request = %s %s %v", request.Method, request.URL, request.Header)
	}
	var body map[string]string
	if json.Unmarshal([]byte(site.bodies[0]), &body) != nil || len(body) != 4 || body["code"] != codeOfTheTest ||
		body["verifier"] != verifierOfTest || body["name"] != "Рабочий стол" || body["appVersion"] != "1.3.0" {
		t.Fatalf("the body = %s", site.bodies[0])
	}
}

// AC1.3: app.link_invalid is a refusal; a broken connection and 5xx are an answer that may have been lost; any other
// answer is no connection.
func TestTheExchangeTellsARefusalFromALostAnswer(t *testing.T) {
	for name, case_ := range map[string]struct {
		answer func(w http.ResponseWriter, r *http.Request)
		want   error
		lost   bool
	}{
		"refused":       {answer: problem(http.StatusBadRequest, "app.link_invalid"), want: domain.ErrLinkInvalid},
		"other 400":     {answer: problem(http.StatusBadRequest, "request.validation"), want: domain.ErrUnreachable},
		"500":           {answer: problem(http.StatusInternalServerError, "server.error"), want: domain.ErrUnreachable, lost: true},
		"503":           {answer: problem(http.StatusServiceUnavailable, "x"), want: domain.ErrUnreachable, lost: true},
		"429":           {answer: problem(http.StatusTooManyRequests, "rate_limit.exceeded"), want: domain.ErrUnreachable},
		"broken answer": {answer: func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, `{"pass":`) }, want: domain.ErrUnreachable, lost: true},
		"no pass":       {answer: func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, `{"pass":"x"}`) }, want: domain.ErrUnreachable, lost: true},
		"connection broken": {answer: func(w http.ResponseWriter, _ *http.Request) {
			connection, _, _ := w.(http.Hijacker).Hijack()
			_ = connection.Close()
		}, want: domain.ErrUnreachable, lost: true},
	} {
		site := newStub(t, case_.answer)
		_, _, err := trusted(site, time.Now).Exchange(context.Background(), domain.Exchange{Code: codeOfTheTest, Verifier: verifierOfTest, Name: "n", AppVersion: "1"})
		if !errors.Is(err, case_.want) || errors.Is(err, domain.ErrAnswerLost) != case_.lost {
			t.Errorf("%s: %v", name, err)
		}
		if len(site.requests()) != 1 {
			t.Errorf("%s: the site heard %d requests", name, len(site.requests()))
		}
	}
}

// AC1.4: no redirection is followed — a 307 to the same host, which would repeat the body with the code and send the
// pass, is no connection with one request; a certificate the system does not trust is no connection.
func TestTheClientFollowsNoRedirectionAndTrustsOnlyTheSystem(t *testing.T) {
	site := newStub(t, nil)
	site.set(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://"+r.Host+"/api/v1/app/link/token", http.StatusTemporaryRedirect)
	})
	client := trusted(site, time.Now)
	if _, _, err := client.Exchange(context.Background(), domain.Exchange{Code: codeOfTheTest, Verifier: verifierOfTest, Name: "n", AppVersion: "1"}); !errors.Is(err, domain.ErrUnreachable) || errors.Is(err, domain.ErrAnswerLost) {
		t.Fatalf("Exchange after 307 = %v", err)
	}
	client.Hold(passOfTheTest)
	if _, err := client.Account(context.Background()); !errors.Is(err, domain.ErrUnreachable) {
		t.Fatalf("Account after 307 = %v", err)
	}
	if heard := site.requests(); len(heard) != 2 {
		t.Fatalf("the site heard %d requests", len(heard))
	}

	product, err := New(site.server.URL)
	if err != nil {
		t.Fatal(err)
	}
	site.set(token)
	if _, _, err := product.Exchange(context.Background(), domain.Exchange{Code: codeOfTheTest, Verifier: verifierOfTest, Name: "n", AppVersion: "1"}); !errors.Is(err, domain.ErrUnreachable) {
		t.Fatalf("an untrusted certificate = %v", err)
	}
	if len(site.requests()) != 2 {
		t.Fatal("a request went over an untrusted certificate")
	}
	settings := transport(nil).TLSClientConfig
	if settings.MinVersion != tls.VersionTLS12 || settings.InsecureSkipVerify || settings.RootCAs != nil {
		t.Fatalf("TLS = %+v", settings)
	}
	for _, address := range []string{"http://site.example.test", "https://", "https://u@site.example.test", "https://site.example.test/path", "https://site.example.test?x=1", "ftp://site.example.test"} {
		if _, err := New(address); err == nil {
			t.Errorf("New(%q) took it", address)
		}
	}
}

// AC2.1, AC2.2, AC2.3: the account is read with the pass in Authorization alone; app.pass_invalid of the pass held drops
// it at once, of a pass held before touches nothing; 429 is no connection until Retry-After, and no request goes before.
func TestThePassIsHeldOnceAndDropsOnlyByItsOwnRefusal(t *testing.T) {
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	var clock sync.Mutex
	at := func() time.Time { clock.Lock(); defer clock.Unlock(); return now }
	site := newStub(t, account)
	client := trusted(site, at)
	client.Hold(passOfTheTest)
	got, err := client.Account(context.Background())
	if err != nil || !got.Quota || got.Questions != (domain.Counter{Used: 3, Limit: 20}) || got.Reading != (domain.Counter{Used: 120, Limit: 1000000}) ||
		got.Tier.Until != nil || got.Email != "a…@example.test" {
		t.Fatalf("Account = %+v, %v", got, err)
	}
	request := site.requests()[0]
	if request.Method != http.MethodGet || request.URL.Path != "/api/v1/app/account" || request.URL.RawQuery != "" ||
		request.Header.Get("Authorization") != "Bearer "+passOfTheTest || request.Header.Get("Origin") != "" || request.Header.Get("Cookie") != "" {
		t.Fatalf("the request = %s %v", request.URL, request.Header)
	}

	site.set(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "120")
		problem(http.StatusTooManyRequests, "rate_limit.exceeded")(w, nil)
	})
	if _, err := client.Account(context.Background()); !errors.Is(err, domain.ErrUnreachable) || !client.Held() {
		t.Fatalf("Account after 429 = %v, held %v", err, client.Held())
	}
	site.set(account)
	if _, err := client.Account(context.Background()); !errors.Is(err, domain.ErrUnreachable) || len(site.requests()) != 2 {
		t.Fatalf("Account before Retry-After = %v after %d requests", err, len(site.requests()))
	}
	clock.Lock()
	now = now.Add(121 * time.Second)
	clock.Unlock()
	if _, err := client.Account(context.Background()); err != nil || len(site.requests()) != 3 {
		t.Fatalf("Account after Retry-After = %v after %d requests", err, len(site.requests()))
	}

	for name, answer := range map[string]func(http.ResponseWriter, *http.Request){
		"500":        problem(http.StatusInternalServerError, "server.error"),
		"403":        problem(http.StatusForbidden, "x"),
		"401 other":  problem(http.StatusUnauthorized, "auth.session_required"),
		"broken 200": func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, `{`) },
	} {
		site.set(answer)
		if _, err := client.Account(context.Background()); !errors.Is(err, domain.ErrUnreachable) || !client.Held() {
			t.Errorf("%s: %v, held %v", name, err, client.Held())
		}
	}

	// A refusal of the pass held before a new link comes back after it: the new pass stays.
	release := make(chan struct{})
	site.set(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer "+passOfTheTest {
			<-release
		}
		problem(http.StatusUnauthorized, "app.pass_invalid")(w, r)
	})
	old := make(chan error, 1)
	go func() { _, err := client.Account(context.Background()); old <- err }()
	for len(site.requests()) < 8 {
		time.Sleep(10 * time.Millisecond)
	}
	client.Hold(otherPass)
	close(release)
	if err := <-old; !errors.Is(err, domain.ErrPassReplaced) || !client.Held() {
		t.Fatalf("the old refusal = %v, held %v", err, client.Held())
	}
	if _, err := client.Account(context.Background()); !errors.Is(err, domain.ErrPassInvalid) || client.Held() {
		t.Fatalf("the refusal of the pass held = %v, held %v", err, client.Held())
	}
	// Without a pass the site is not asked, and nothing it did not say is a refusal: an unlinking of the author between the
	// state and the request does not break the link.
	before := len(site.requests())
	if _, err := client.Account(context.Background()); !errors.Is(err, domain.ErrNotHeld) || errors.Is(err, domain.ErrPassInvalid) ||
		len(site.requests()) != before {
		t.Fatalf("a read without a pass = %v after %d requests", err, len(site.requests())-before)
	}
}

// AC2.4, AC2.5: unlinking stops using the pass at once whatever the site answers, and says whether the site heard it.
func TestUnlinkingDropsThePassWhateverTheSiteSays(t *testing.T) {
	site := newStub(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	client := trusted(site, time.Now)
	client.Hold(passOfTheTest)
	if told := client.Unlink(context.Background()); !told || client.Held() {
		t.Fatalf("Unlink = %v, held %v", told, client.Held())
	}
	request := site.requests()[0]
	if request.Method != http.MethodDelete || request.URL.Path != "/api/v1/app/device" || request.Header.Get("Authorization") != "Bearer "+passOfTheTest {
		t.Fatalf("the request = %s %s", request.Method, request.URL)
	}
	if told := client.Unlink(context.Background()); told || len(site.requests()) != 1 {
		t.Fatal("unlinking without a pass went to the site")
	}
	site.set(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
		w.WriteHeader(http.StatusNoContent)
	})
	client.Hold(passOfTheTest)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if told := client.Unlink(ctx); told || client.Held() {
		t.Fatalf("Unlink of a silent site = %v, held %v", told, client.Held())
	}
	for _, answer := range []func(http.ResponseWriter, *http.Request){problem(http.StatusInternalServerError, "x"), problem(http.StatusUnauthorized, "app.pass_invalid")} {
		site.set(answer)
		client.Hold(passOfTheTest)
		if told := client.Unlink(context.Background()); told || client.Held() {
			t.Fatalf("Unlink = %v, held %v", told, client.Held())
		}
	}
	if strings.Contains(site.bodies[0], passOfTheTest) {
		t.Fatal("the pass went in a body")
	}
}

// specs/2026-11-15-desk-turns-here.md TH2.2: getAppAccount of a site that names the places of this app reads thisApp beside
// used and limit; a site without the field — an older one — reads without it and says so, not naught; fields the client does
// not know, in the counter and around it, take nothing away.
func TestTheAccountReadsThePlacesOfThisAppWhenTheSiteNamesThem(t *testing.T) {
	answer := func(activeTurns string) func(w http.ResponseWriter, _ *http.Request) {
		return func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"name":"Северный","email":"a…@example.test","tier":{"code":"writer","title":"Писатель","until":null},
				"quota":{"day":"2026-11-13","resetsAt":"2026-11-13T21:00:00Z","chatTurns":{"used":3,"limit":20},
				"activeTurns":`+activeTurns+`,"imports":{"used":0,"limit":5},"reading":{"used":120,"limit":1000000},"later":{"x":1}},
				"newer":[1,2]}`)
		}
	}
	site := newStub(t, answer(`{"used":2,"limit":4,"thisApp":1}`))
	client := trusted(site, time.Now)
	client.Hold(passOfTheTest)
	got, err := client.Account(context.Background())
	if err != nil || got.ActiveTurns != (domain.Counter{Used: 2, Limit: 4}) || got.ThisApp == nil || *got.ThisApp != 1 {
		t.Fatalf("an account with the places of this app = %+v (thisApp %v), %v", got, got.ThisApp, err)
	}
	for name, activeTurns := range map[string]string{
		"without the field":     `{"used":2,"limit":4}`,
		"with a field of later": `{"used":2,"limit":4,"elsewhere":{"sites":1}}`,
	} {
		site.set(answer(activeTurns))
		got, err := client.Account(context.Background())
		if err != nil || !got.Quota || got.ActiveTurns != (domain.Counter{Used: 2, Limit: 4}) || got.ThisApp != nil {
			t.Fatalf("%s: %+v (thisApp %v), %v", name, got, got.ThisApp, err)
		}
	}
}
