// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"bytes"
	"encoding/json"
	"encoding/pem"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"canonkeeper/backend/internal/app/desktop/desktoptest"
	accountdomain "canonkeeper/backend/internal/domain/accountlink"
	domain "canonkeeper/backend/internal/domain/desktop"
)

// The marks of the tests of the link: a pass the stub of the site gives, a code it returns with. The tests look for the
// marks, never print a value.
var (
	passMark = "ckd_" + padded("pass-mark-of-the-account-test-")
	codeMark = padded("code-mark-of-the-account-test-")
)

// padded is a mark of the 43 signs of base64url a code and a pass have.
func padded(mark string) string { return mark + strings.Repeat("0", 43-len(mark)) }

// versionOfTheLink is the version the programs of the link carry: a build without one cannot be linked.
const versionOfTheLink = "1.3.0"

// heard is a request the stub of the site heard, and when.
type heard struct {
	method, path, query, authorization, origin, cookie string
	body                                               map[string]string
	at                                                 time.Time
	// header and raw are the headers and the body as they came (specs/2026-11-13-desk-account-ai.md AI2.4).
	header http.Header
	raw    []byte
}

// siteOfTheTest is a stub of the site over TLS on a port known before the program is built with its address
// (-X main.siteURL): the process trusts its certificate by the file SSL_CERT_FILE names, as the computer of the author
// trusts the certificate of the site. Its answers are the test's to change.
type siteOfTheTest struct {
	server   *httptest.Server
	url      string
	certFile string
	mu       sync.Mutex
	requests []heard
	answer   func(w http.ResponseWriter, r *http.Request, request heard)
}

func newSiteOfTheTest(t *testing.T) *siteOfTheTest {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	site := &siteOfTheTest{answer: theSite}
	site.server = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		request := heard{
			method: r.Method, path: r.URL.Path, query: r.URL.RawQuery, authorization: r.Header.Get("Authorization"),
			origin: r.Header.Get("Origin"), cookie: r.Header.Get("Cookie"), at: time.Now(), header: r.Header.Clone(), raw: raw,
		}
		_ = json.Unmarshal(raw, &request.body)
		site.mu.Lock()
		site.requests = append(site.requests, request)
		answer := site.answer
		site.mu.Unlock()
		answer(w, r, request)
	}))
	_ = site.server.Listener.Close()
	site.server.Listener = listener
	site.server.StartTLS()
	t.Cleanup(site.server.Close)
	site.url = "https://" + listener.Addr().String()
	site.certFile = filepath.Join(t.TempDir(), "site.pem")
	certificate := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: site.server.Certificate().Raw})
	if err := os.WriteFile(site.certFile, certificate, 0o600); err != nil {
		t.Fatal(err)
	}
	return site
}

// theSite answers as the site answers a good pass and a good code.
func theSite(w http.ResponseWriter, _ *http.Request, request heard) {
	w.Header().Set("Content-Type", "application/json")
	switch request.method + " " + request.path {
	case "POST /api/v1/app/link/token":
		_, _ = io.WriteString(w, `{"pass":"`+passMark+`","account":{"name":"Северный","email":"a…@example.test",
			"tier":{"code":"writer","title":"Писатель","until":"2026-12-13T00:00:00Z"},"reading":{"used":3200000,"limit":40000000}}}`)
	case "GET /api/v1/app/account":
		_, _ = io.WriteString(w, `{"name":"Северный","email":"a…@example.test","tier":{"code":"writer","title":"Писатель","until":"2026-12-13T00:00:00Z"},
			"quota":{"day":"2026-11-13","resetsAt":"2026-11-13T21:00:00Z","chatTurns":{"used":3,"limit":20},"activeTurns":{"used":0,"limit":1},
			"imports":{"used":0,"limit":5},"reading":{"used":3200000,"limit":40000000}},"newer":{"field":1}}`)
	case "DELETE /api/v1/app/device":
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (site *siteOfTheTest) set(answer func(w http.ResponseWriter, r *http.Request, request heard)) {
	site.mu.Lock()
	defer site.mu.Unlock()
	site.answer = answer
}

// heardOf are the requests the stub heard to a path.
func (site *siteOfTheTest) heardOf(path string) []heard {
	site.mu.Lock()
	defer site.mu.Unlock()
	var of []heard
	for _, request := range site.requests {
		if request.path == path {
			of = append(of, request)
		}
	}
	return of
}

func (site *siteOfTheTest) all() int {
	site.mu.Lock()
	defer site.mu.Unlock()
	return len(site.requests)
}

// program is cmd/desktop built with the version of the link and the address of the stub.
func (site *siteOfTheTest) program(t *testing.T) string {
	t.Helper()
	name := "site-" + strings.ReplaceAll(strings.TrimPrefix(site.url, "https://"), ":", "-")
	return program(t, programVariant{name: name, version: versionOfTheLink, site: site.url})
}

// linked is a run of the program of the stub as the shell runs it: the backend, the shell of the test and the window.
type linked struct {
	process *desktoptest.Process
	ready   desktoptest.Message
	shell   *desktoptest.Shell
	window  *desktoptest.Window
	data    string
}

// launch starts the program on a data directory with the records the shell hands over, the shell answering as reply,
// and opens the window. The environment names the certificate of the stub and an address of another site the build does
// not take.
func (site *siteOfTheTest) launch(t *testing.T, binary, resources, data string, records map[string]string, reply func(desktoptest.Message) desktoptest.Reply) *linked {
	t.Helper()
	launch := desktoptest.Launch{Data: data, Resources: resources, Environment: []string{
		"SSL_CERT_FILE=" + site.certFile, "CANONKEEPER_SITE_URL=https://other.example.test", "CANONKEEPER_SITE=https://other.example.test",
	}}
	process := desktoptest.Begin(t, binary, launch, desktoptest.StartOf(launch, versionOfTheLink, records))
	ready := process.Ready(2 * time.Minute)
	if ready.Type != "ready" || ready.Port == 0 || ready.Code == "" {
		t.Fatalf("the backend answered %s %s instead of ready", ready.Type, ready.Reason)
	}
	shell := process.Serve(records, reply)
	return &linked{process: process, ready: ready, shell: shell, window: desktoptest.OpenWindow(t, ready.Port, ready.Code), data: data}
}

// accountState is getAccount.
type accountState struct {
	State string `json:"state"`
	Link  *struct {
		Address   string `json:"address"`
		ExpiresAt string `json:"expiresAt"`
	} `json:"link"`
	Failure *string `json:"failure"`
	Account *struct {
		Name      string `json:"name"`
		Email     string `json:"email"`
		Tier      struct{ Code, Title string }
		Questions *struct{ Used, Limit int64 } `json:"questions"`
		Reading   struct{ Used, Limit int64 }  `json:"reading"`
	} `json:"account"`
	Unreachable bool              `json:"unreachable"`
	Computer    string            `json:"computer"`
	PassStore   string            `json:"passStore"`
	Pages       map[string]string `json:"pages"`
}

func (run *linked) account(t *testing.T, path string) (accountState, []byte) {
	t.Helper()
	response, body := run.window.Do(http.MethodGet, path, "", nil)
	var state accountState
	if response.StatusCode != http.StatusOK || json.Unmarshal(body, &state) != nil {
		t.Fatalf("getAccount = %d %s", response.StatusCode, body)
	}
	return state, body
}

// start is startAccountLink with the query of its address.
func (run *linked) start(t *testing.T) url.Values {
	t.Helper()
	response, body := run.window.Do(http.MethodPost, "/api/v1/desktop/account/link", run.window.CSRF(), nil)
	var state accountState
	if response.StatusCode != http.StatusOK || json.Unmarshal(body, &state) != nil || state.State != "waiting" || state.Link == nil {
		t.Fatalf("startAccountLink = %d %s", response.StatusCode, body)
	}
	address, err := url.Parse(state.Link.Address)
	if err != nil {
		t.Fatal(err)
	}
	return address.Query()
}

// back is the return of the browser to the window, as the page /link of the site sends it.
func (run *linked) back(t *testing.T, method, query string, client *http.Client) (int, string, http.Header) {
	t.Helper()
	request, _ := http.NewRequest(method, "http://127.0.0.1:"+strconv.Itoa(run.ready.Port)+"/desktop/link/callback?"+query, nil)
	if client == nil {
		client = &http.Client{Timeout: desktoptest.Waited(time.Minute)}
	}
	response, err := client.Do(request)
	if err != nil {
		return 0, "", nil
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	return response.StatusCode, string(body), response.Header
}

func returnQuery(code, state string) string {
	return url.Values{"code": {code}, "state": {state}}.Encode()
}

// specs/2026-11-13-desk-account.md AC1.1, AC1.3, AC1.4: the backend of the app with a stub of the site over TLS. The
// link waits with the address of /link of the address of the build — the challenge S256 of the verifier, a state of 43
// signs and the port of the window —; the return with its state changes the code for the pass, the shell keeps it as
// account:pass, getAccount says linked and the shell is asked to raise the window. The stub got the verifier, the name of
// the computer and the version without a cookie, an Origin or a query; an address of another site in the environment
// changed nothing.
func TestTheAppLinksWithTheAccountThroughTheBrowser(t *testing.T) {
	resources := processResources(t)
	site := newSiteOfTheTest(t)
	run := site.launch(t, site.program(t), resources, filepath.Join(t.TempDir(), "data"), nil, desktoptest.KeepAll)
	first, _ := run.account(t, "/api/v1/desktop/account")
	host, _ := os.Hostname()
	if first.State != "none" || first.Computer != accountdomain.ComputerName(host) || first.Pages["apps"] != site.url+"/account#apps" ||
		first.Pages["tariffs"] != site.url+"/tariffs" || first.PassStore != "file" {
		t.Fatalf("the first state = %+v", first)
	}
	query := run.start(t)
	waiting, _ := run.account(t, "/api/v1/desktop/account")
	if waiting.State != "waiting" || waiting.Link == nil || !strings.HasPrefix(waiting.Link.Address, site.url+"/link?") ||
		!strings.Contains(waiting.Link.Address, query.Get("state")) {
		t.Fatalf("the waiting = %+v", waiting)
	}
	if len(query.Get("state")) != 43 || len(query.Get("challenge")) != 43 || query.Get("port") != strconv.Itoa(run.ready.Port) ||
		query.Get("platform") != runtime.GOOS || len(query) != 4 {
		t.Fatalf("the address asks %v", query)
	}
	if status, body, _ := run.back(t, http.MethodGet, returnQuery(codeMark, query.Get("state")), nil); status != http.StatusOK ||
		!strings.Contains(body, "Готово. Вернитесь в окно CanonKeeper — эту вкладку можно закрыть") {
		t.Fatalf("the return = %d %s", status, body)
	}
	exchanges := site.heardOf("/api/v1/app/link/token")
	if len(exchanges) != 1 {
		t.Fatalf("the site heard %d exchanges", len(exchanges))
	}
	exchange := exchanges[0]
	if exchange.method != http.MethodPost || exchange.query != "" || exchange.origin != "" || exchange.cookie != "" || exchange.authorization != "" ||
		exchange.body["code"] != codeMark || accountdomain.Challenge(exchange.body["verifier"]) != query.Get("challenge") ||
		exchange.body["name"] != accountdomain.ComputerName(host) || exchange.body["appVersion"] != versionOfTheLink {
		t.Fatalf("the exchange heard %s %s, its body is not the link's", exchange.method, exchange.path)
	}
	if records := run.shell.Records(); records[domain.SecretAccountPass] != passMark {
		t.Fatal("the shell does not keep the pass")
	}
	if _, raised := run.shell.Await("focus", 10*time.Second); !raised {
		t.Fatal("the window was not raised")
	}
	state, body := run.account(t, "/api/v1/desktop/account")
	if state.State != "linked" || state.Account == nil || state.Account.Name != "Северный" || state.Account.Email != "a…@example.test" ||
		state.Account.Questions == nil || state.Account.Questions.Limit != 20 || state.Account.Reading.Limit != 40000000 || bytes.Contains(body, []byte(passMark)) {
		t.Fatalf("linked = %s", body)
	}
	account := site.heardOf("/api/v1/app/account")
	if len(account) != 1 || account[0].authorization != "Bearer "+passMark || account[0].query != "" || account[0].origin != "" || account[0].cookie != "" {
		t.Fatalf("the site heard %d reads of the account", len(account))
	}
	if response, body := run.window.Do(http.MethodPost, "/api/v1/desktop/account/link", run.window.CSRF(), nil); response.StatusCode != http.StatusConflict ||
		!containsCode(body, "account.link_unavailable") {
		t.Fatalf("a start of a linked app = %d %s", response.StatusCode, body)
	}
	theMarksAreNowhere(t, run, query.Get("state"))
}

// theMarksAreNowhere fails the test when the pass, the code or the state of the test is in the log of the process, a
// file of the data directory or the arguments and the environment of the processes of the app (AC1.2, AC1.5). The
// address of /link with the state goes to the browser, which is not a process of the app.
func theMarksAreNowhere(t *testing.T, run *linked, state string) {
	t.Helper()
	marks := [][]byte{[]byte(passMark), []byte(codeMark), []byte(state)}
	for _, mark := range marks {
		if bytes.Contains(run.process.Stderr.Bytes(), mark) {
			t.Error("the output of the backend holds a mark")
		}
	}
	_ = filepath.WalkDir(run.data, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || !entry.Type().IsRegular() {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		for _, mark := range marks {
			if bytes.Contains(raw, mark) {
				t.Errorf("%s holds a mark", path)
			}
		}
		return nil
	})
	if runtime.GOOS != "linux" {
		return
	}
	for _, member := range family(t, run.process.Command.Process.Pid, run.data) {
		for _, file := range []string{"cmdline", "environ"} {
			raw, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(member), file))
			if err != nil {
				continue
			}
			for _, mark := range marks {
				if bytes.Contains(raw, mark) {
					t.Errorf("/proc/%d/%s holds a mark", member, file)
				}
			}
		}
	}
}

// AC1.2: the return is the one route without the session of the window. Another Host is 421 before it; another method,
// a code of another shape, another, canceled or spent state is 403 with the page of the refusal and asks the site
// nothing; a wrong state does not spend the waiting; two returns at once with the right state make one exchange. The
// pages are not kept, have no script and no style and repeat nothing of the request.
func TestTheReturnOfTheLinkIsRefusedOrDoneOnce(t *testing.T) {
	resources := processResources(t)
	site := newSiteOfTheTest(t)
	run := site.launch(t, site.program(t), resources, filepath.Join(t.TempDir(), "data"), nil, desktoptest.KeepAll)
	query := run.start(t)
	state := query.Get("state")
	stranger, _ := http.NewRequest(http.MethodGet, "http://127.0.0.1:"+strconv.Itoa(run.ready.Port)+"/desktop/link/callback?"+returnQuery(codeMark, state), nil)
	stranger.Host = "evil.example"
	if response, err := (&http.Client{Timeout: desktoptest.Waited(10 * time.Second)}).Do(stranger); err != nil || response.StatusCode != http.StatusMisdirectedRequest {
		t.Fatalf("another Host = %v, %v", response, err)
	} else {
		_ = response.Body.Close()
	}
	refused := "Эта ссылка связи уже не действует. Вернитесь в CanonKeeper и свяжите приложение ещё раз"
	for _, request := range []struct{ method, query string }{
		{http.MethodPost, returnQuery(codeMark, state)},
		{http.MethodGet, returnQuery("short-code", state)},
		{http.MethodGet, returnQuery(codeMark, padded("state-of-another-"))},
		{http.MethodGet, returnQuery(codeMark, state) + "&extra=1"},
		{http.MethodGet, "code=" + codeMark},
	} {
		status, body, headers := run.back(t, request.method, request.query, nil)
		if status != http.StatusForbidden || (request.method == http.MethodGet && !strings.Contains(body, refused)) ||
			headers.Get("Cache-Control") != "no-store" || strings.Contains(body, "<script") || strings.Contains(body, "style") ||
			strings.Contains(body, codeMark) || strings.Contains(body, "short-code") || strings.Contains(body, "state-of-another") || strings.Contains(body, "extra") {
			t.Fatalf("%s %s = %d %s", request.method, request.query, status, body)
		}
	}
	if site.all() != 0 {
		t.Fatalf("a refused return asked the site %d times", site.all())
	}
	release := make(chan struct{})
	site.set(func(w http.ResponseWriter, r *http.Request, request heard) {
		if request.path == "/api/v1/app/link/token" {
			<-release
		}
		theSite(w, r, request)
	})
	var group sync.WaitGroup
	statuses := make(chan int, 4)
	for range 4 {
		group.Add(1)
		go func() {
			defer group.Done()
			status, _, _ := run.back(t, http.MethodGet, returnQuery(codeMark, state), nil)
			statuses <- status
		}()
	}
	time.Sleep(time.Second)
	close(release)
	group.Wait()
	close(statuses)
	var done, forbidden int
	for status := range statuses {
		switch status {
		case http.StatusOK:
			done++
		case http.StatusForbidden:
			forbidden++
		}
	}
	if done != 1 || forbidden != 3 || len(site.heardOf("/api/v1/app/link/token")) != 1 {
		t.Fatalf("four returns at once: %d done, %d refused, %d exchanges", done, forbidden, len(site.heardOf("/api/v1/app/link/token")))
	}
	if status, body, _ := run.back(t, http.MethodGet, returnQuery(codeMark, state), nil); status != http.StatusForbidden || !strings.Contains(body, refused) {
		t.Fatalf("the spent state = %d %s", status, body)
	}

	// A canceled waiting is refused; unlinked first, the app starts again.
	if response, _ := run.window.Do(http.MethodDelete, "/api/v1/desktop/account", run.window.CSRF(), nil); response.StatusCode != http.StatusOK {
		t.Fatalf("unlinkAccount = %d", response.StatusCode)
	}
	again := run.start(t)
	if response, _ := run.window.Do(http.MethodDelete, "/api/v1/desktop/account/link", run.window.CSRF(), nil); response.StatusCode != http.StatusNoContent {
		t.Fatalf("cancelAccountLink = %d", response.StatusCode)
	}
	before := len(site.heardOf("/api/v1/app/link/token"))
	if status, _, _ := run.back(t, http.MethodGet, returnQuery(codeMark, again.Get("state")), nil); status != http.StatusForbidden ||
		len(site.heardOf("/api/v1/app/link/token")) != before {
		t.Fatalf("a canceled state = %d", status)
	}
}

// AC1.3: the outcomes of the exchange. The refusal of the site is asked once; a connection broken after the site made the
// device is asked once more with the same code and verifier; a pass the shell refused or did not keep in its term is
// unlinked on the site with itself and its record removed, and the link failed for the keyring; a browser that left
// before the end does not stop the exchange.
func TestTheOutcomesOfAReturn(t *testing.T) {
	resources := processResources(t)
	site := newSiteOfTheTest(t)
	binary := site.program(t)
	run := site.launch(t, binary, resources, filepath.Join(t.TempDir(), "data"), nil, desktoptest.KeepAll)
	failedWords := "Связать не получилось. Вернитесь в CanonKeeper и свяжите приложение ещё раз"

	site.set(func(w http.ResponseWriter, r *http.Request, request heard) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"code":"app.link_invalid","status":400}`)
	})
	query := run.start(t)
	if status, body, _ := run.back(t, http.MethodGet, returnQuery(codeMark, query.Get("state")), nil); status != http.StatusBadGateway ||
		!strings.Contains(body, failedWords) || len(site.heardOf("/api/v1/app/link/token")) != 1 {
		t.Fatalf("a refused code = %d %s after %d exchanges", status, body, len(site.heardOf("/api/v1/app/link/token")))
	}
	if state, _ := run.account(t, "/api/v1/desktop/account"); state.State != "failed" || state.Failure == nil || *state.Failure != "exchange" {
		t.Fatalf("after a refused code = %+v", state)
	}

	exchanges := len(site.heardOf("/api/v1/app/link/token"))
	site.set(func(w http.ResponseWriter, r *http.Request, request heard) {
		if request.path == "/api/v1/app/link/token" && len(site.heardOf(request.path)) == exchanges+1 {
			connection, _, _ := w.(http.Hijacker).Hijack()
			_ = connection.Close()
			return
		}
		theSite(w, r, request)
	})
	query = run.start(t)
	if status, _, _ := run.back(t, http.MethodGet, returnQuery(codeMark, query.Get("state")), nil); status != http.StatusOK {
		t.Fatalf("a lost answer = %d", status)
	}
	repeated := site.heardOf("/api/v1/app/link/token")[exchanges:]
	if len(repeated) != 2 || repeated[0].body["code"] != repeated[1].body["code"] || repeated[0].body["verifier"] != repeated[1].body["verifier"] {
		t.Fatalf("a lost answer was asked %d times", len(repeated))
	}
	if response, _ := run.window.Do(http.MethodDelete, "/api/v1/desktop/account", run.window.CSRF(), nil); response.StatusCode != http.StatusOK {
		t.Fatalf("unlinkAccount = %d", response.StatusCode)
	}

	// The page names no store: where the link was not kept the app says by passStore (specs/2026-11-15-desk-account-link-web.md AW1.6).
	notSaved := "Связь не сохранилась на этом компьютере. Вернитесь в CanonKeeper и свяжите приложение ещё раз"
	for name, reply := range map[string]desktoptest.Reply{"refused": desktoptest.Refuse, "silent": desktoptest.Silent} {
		site.set(theSite)
		run.shell.Answer(func(message desktoptest.Message) desktoptest.Reply {
			if message.ID == domain.SecretAccountPass {
				return reply
			}
			return desktoptest.Keep
		})
		unlinks := len(site.heardOf("/api/v1/app/device"))
		asked := len(run.shell.Asked())
		query = run.start(t)
		status, body, _ := run.back(t, http.MethodGet, returnQuery(codeMark, query.Get("state")), nil)
		if status != http.StatusServiceUnavailable || !strings.Contains(body, notSaved) {
			t.Fatalf("%s: the return = %d %s", name, status, body)
		}
		unlinked := site.heardOf("/api/v1/app/device")[unlinks:]
		if len(unlinked) != 1 || unlinked[0].authorization != "Bearer "+passMark || unlinked[0].method != http.MethodDelete {
			t.Fatalf("%s: the site heard %d unlinkings", name, len(unlinked))
		}
		changes := run.shell.Asked()[asked:]
		if len(changes) != 2 || changes[0].Type != "secret-save" || changes[1].Type != "secret-delete" || changes[1].ID != domain.SecretAccountPass {
			t.Fatalf("%s: the shell was asked %d changes", name, len(changes))
		}
		if state, _ := run.account(t, "/api/v1/desktop/account"); state.State != "failed" || state.Failure == nil || *state.Failure != "keyring" {
			t.Fatalf("%s: after the pass not kept = %+v", name, state)
		}
	}

	// The browser leaves before the end: the exchange goes on and the app is linked.
	run.shell.Answer(desktoptest.KeepAll)
	site.set(func(w http.ResponseWriter, r *http.Request, request heard) {
		if request.path == "/api/v1/app/link/token" {
			time.Sleep(2 * time.Second)
		}
		theSite(w, r, request)
	})
	query = run.start(t)
	if status, _, _ := run.back(t, http.MethodGet, returnQuery(codeMark, query.Get("state")), &http.Client{Timeout: 300 * time.Millisecond}); status != 0 {
		t.Fatalf("the impatient browser got %d", status)
	}
	deadline := desktoptest.Deadline(30 * time.Second)
	for {
		if state, _ := run.account(t, "/api/v1/desktop/account"); state.State == "linked" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the exchange stopped with the browser")
		}
		time.Sleep(200 * time.Millisecond)
	}
	if run.shell.Records()[domain.SecretAccountPass] != passMark {
		t.Fatal("the shell does not keep the pass of a return the browser left")
	}
}

// AC1.1: a build the site cannot return to — without a version, or with the window on a port out of 20000–29999 —
// starts no link and opens no address.
func TestABuildTheSiteCannotReturnToStartsNoLink(t *testing.T) {
	binary, resources := desktopBinary(t)
	process := startBackend(t, binary, resources, filepath.Join(t.TempDir(), "data"), nil)
	window := desktoptest.OpenWindow(t, process.ready.Port, process.ready.Code)
	if response, body := window.Do(http.MethodPost, "/api/v1/desktop/account/link", window.CSRF(), nil); response.StatusCode != http.StatusConflict ||
		!containsCode(body, "account.link_unsupported") || bytes.Contains(body, []byte("/link?")) {
		t.Fatalf("a start of a build without a version = %d %s", response.StatusCode, body)
	}
	process.stop()

	site := newSiteOfTheTest(t)
	data := filepath.Join(t.TempDir(), "data")
	if err := os.MkdirAll(data, 0o700); err != nil {
		t.Fatal(err)
	}
	free, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := free.Addr().(*net.TCPAddr).Port
	_ = free.Close()
	if port >= accountdomain.MinPort && port <= accountdomain.MaxPort {
		t.Skipf("the free port %d is in the range of the site", port)
	}
	writeFile(t, DataDir{Root: data}.Settings(), `{"port": `+strconv.Itoa(port)+`}`)
	run := site.launch(t, site.program(t), resources, data, nil, desktoptest.KeepAll)
	if run.ready.Port != port {
		t.Skipf("the window took %d, not the remembered %d", run.ready.Port, port)
	}
	if response, body := run.window.Do(http.MethodPost, "/api/v1/desktop/account/link", run.window.CSRF(), nil); response.StatusCode != http.StatusConflict ||
		!containsCode(body, "account.link_unsupported") {
		t.Fatalf("a start of a window on %d = %d %s", port, response.StatusCode, body)
	}
	if site.all() != 0 {
		t.Fatal("a build that cannot be linked asked the site")
	}
}
