// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"canonkeeper/backend/internal/app/desktop/desktoptest"
	domain "canonkeeper/backend/internal/domain/desktop"
)

// The made-up names the provider of «Свой ИИ» and the site stand at in the test of the proxy; the proxy holds the tunnels to
// their stubs (specs/2026-11-14-desk-journal.md JR5.2).
const (
	providerName = "provider.example.test"
	siteName     = "site.example.test"
)

// proxyNames are the names of the environment that choose a proxy or its exceptions: the backend of the test inherits none
// of the machine's, in any case.
var proxyNames = []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY", "FTP_PROXY", "REQUEST_METHOD"}

// namedCertificate is a certificate of the names, which the stubs serve and the process trusts by SSL_CERT_FILE.
func namedCertificate(t *testing.T, names ...string) (tls.Certificate, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: names[0]}, DNSNames: names,
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour), IsCA: true, BasicConstraintsValid: true,
		KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "names.pem")
	if err := os.WriteFile(file, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, file
}

// namedStub is a stub over TLS with the certificate of the names.
func namedStub(t *testing.T, certificate tls.Certificate, handler http.Handler) *httptest.Server {
	t.Helper()
	server := httptest.NewUnstartedServer(handler)
	server.TLS = &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12}
	server.StartTLS()
	t.Cleanup(server.Close)
	return server
}

// recordingProxy is the proxy of the environment of the backend: it writes down every host it is asked to reach and holds
// the tunnels to the stubs of the made-up names; a name without a stub is refused.
type recordingProxy struct {
	mu      sync.Mutex
	reached []string
	routes  map[string]string
	open    []net.Conn
	address string
}

func newRecordingProxy(t *testing.T, routes map[string]string) *recordingProxy {
	t.Helper()
	proxy := &recordingProxy{routes: routes}
	server := httptest.NewServer(proxy)
	proxy.address = server.Listener.Addr().String()
	t.Cleanup(func() {
		proxy.mu.Lock()
		for _, connection := range proxy.open {
			_ = connection.Close()
		}
		proxy.mu.Unlock()
		server.Close()
	})
	return proxy
}

// withoutDefaultPort is a host as the comparison takes it: without the port of its scheme.
func withoutDefaultPort(host, port string) string {
	return strings.TrimSuffix(host, ":"+port)
}

func (proxy *recordingProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	target, port := r.Host, "443"
	if r.Method != http.MethodConnect {
		target, port = r.URL.Host, "80"
		if r.URL.Port() == "" {
			target += ":80"
		}
	}
	proxy.mu.Lock()
	proxy.reached = append(proxy.reached, withoutDefaultPort(target, port))
	route, known := proxy.routes[target]
	proxy.mu.Unlock()
	if !known {
		http.Error(w, "no route", http.StatusBadGateway)
		return
	}
	if r.Method != http.MethodConnect {
		outgoing := r.Clone(r.Context())
		outgoing.RequestURI, outgoing.URL.Scheme, outgoing.URL.Host = "", "http", route
		response, err := (&http.Transport{}).RoundTrip(outgoing)
		if err != nil {
			http.Error(w, "upstream", http.StatusBadGateway)
			return
		}
		defer response.Body.Close()
		for name, values := range response.Header {
			w.Header()[name] = values
		}
		w.WriteHeader(response.StatusCode)
		_, _ = io.Copy(w, response.Body)
		return
	}
	upstream, err := net.DialTimeout("tcp", route, 5*time.Second)
	if err != nil {
		http.Error(w, "upstream", http.StatusBadGateway)
		return
	}
	connection, buffered, err := w.(http.Hijacker).Hijack()
	if err != nil {
		_ = upstream.Close()
		return
	}
	proxy.mu.Lock()
	proxy.open = append(proxy.open, connection, upstream)
	proxy.mu.Unlock()
	_, _ = io.WriteString(connection, "HTTP/1.1 200 Connection Established\r\n\r\n")
	go func() {
		_, _ = io.Copy(upstream, buffered)
		_ = upstream.Close()
	}()
	go func() {
		_, _ = io.Copy(connection, upstream)
		_ = connection.Close()
	}()
}

// hosts are the hosts the proxy was asked to reach, each once.
func (proxy *recordingProxy) hosts() []string {
	proxy.mu.Lock()
	defer proxy.mu.Unlock()
	hosts := slices.Clone(proxy.reached)
	slices.Sort(hosts)
	return slices.Compact(hosts)
}

// journalHosts are the recipients the journal of the window names: the provider by its host as is.
func journalHosts(t *testing.T, window *client) []string {
	t.Helper()
	var page struct {
		Rows []struct {
			Recipients []string `json:"recipients"`
		} `json:"rows"`
	}
	window.read("/api/v1/desktop/log?zone=UTC", &page)
	var hosts []string
	for _, row := range page.Rows {
		hosts = append(hosts, row.Recipients...)
	}
	slices.Sort(hosts)
	return slices.Compact(hosts)
}

// networkHosts are the hosts of «Куда приложение ходит в сеть».
func networkHosts(t *testing.T, window *client) []string {
	t.Helper()
	var state struct {
		Network []struct {
			Host string `json:"host"`
		} `json:"network"`
	}
	window.read("/api/v1/desktop/state", &state)
	var hosts []string
	for _, use := range state.Network {
		hosts = append(hosts, use.Host)
	}
	return hosts
}

// reachedOnly fails the test unless the proxy reached exactly the hosts wanted, every host of the journal is a host the
// proxy reached and every host the proxy reached is named by the journal or by the list of the network.
func reachedOnly(t *testing.T, proxy *recordingProxy, window *client, wanted ...string) {
	t.Helper()
	reached, journal, network := proxy.hosts(), journalHosts(t, window), networkHosts(t, window)
	slices.Sort(wanted)
	if !slices.Equal(reached, wanted) {
		t.Fatalf("the proxy reached %v, want %v", reached, wanted)
	}
	for _, host := range journal {
		if !slices.Contains(reached, host) {
			t.Fatalf("the journal names %s, which the proxy never reached (reached %v)", host, reached)
		}
	}
	for _, host := range reached {
		if !slices.Contains(journal, host) && !slices.Contains(network, host) {
			t.Fatalf("the proxy reached %s, which neither the journal %v nor the network %v names", host, journal, network)
		}
	}
}

// specs/2026-11-14-desk-journal.md JR5.2: the built backend of the app with a recording proxy in its environment — and no
// proxy or exception of the machine —, the provider of «Свой ИИ» and the site at made-up names whose tunnels the proxy holds.
// Without the helper and the link the import of a book and the reading of a chapter make no connection; with «Свой ИИ» a
// question and the reading of the book reach only the host of the provider; with the link — only the host of the site
// besides. Every host of the journal is a host of the proxy, and every host of the proxy is named by the journal or by the
// list of the network.
func TestTheAppGoesOnlyWhereItsJournalAndItsNetworkSay(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the certificate of the stubs is trusted by SSL_CERT_FILE, which the roots of Linux read")
	}
	resources := desktoptest.Resources(t)
	certificate, certFile := namedCertificate(t, providerName, siteName)
	provider := &providerOfTheTest{}
	providerStub := namedStub(t, certificate, provider)
	siteStub := namedStub(t, certificate, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		request := heard{method: r.Method, path: r.URL.Path, at: time.Now(), header: r.Header.Clone(), raw: raw}
		_ = json.Unmarshal(raw, &request.body)
		theSite(w, r, request)
	}))
	proxy := newRecordingProxy(t, map[string]string{
		providerName + ":443": providerStub.Listener.Addr().String(),
		siteName + ":443":     siteStub.Listener.Addr().String(),
	})
	binary := program(t, programVariant{name: "proxy-site", version: versionOfTheLink, site: "https://" + siteName})
	data := filepath.Join(t.TempDir(), "data")
	launch := desktoptest.Launch{Data: data, Resources: resources, Unset: proxyNames, Environment: []string{
		"HTTPS_PROXY=http://" + proxy.address, "HTTP_PROXY=http://" + proxy.address, "SSL_CERT_FILE=" + certFile,
	}}
	process := desktoptest.Begin(t, binary, launch, desktoptest.StartOf(launch, versionOfTheLink, nil))
	ready := process.Ready(2 * time.Minute)
	if ready.Type != "ready" || ready.Port == 0 || ready.Code == "" {
		t.Fatalf("the backend answered %s %s instead of ready", ready.Type, ready.Reason)
	}
	shell := process.Serve(nil, desktoptest.KeepAll)
	window := &client{t: t, origin: "http://127.0.0.1:" + strconv.Itoa(ready.Port), http: &http.Client{Timeout: 60 * time.Second}}
	if response := window.open(ready.Code); response.StatusCode != http.StatusOK {
		t.Fatalf("open = %d", response.StatusCode)
	}
	if network := networkHosts(t, window); len(network) != 0 {
		t.Fatalf("the network without the helper and the link = %v", network)
	}

	// Without the helper and the link: the import of a book and the reading of a chapter make no connection.
	response, body := window.do(http.MethodPost, "/api/v1/projects", window.csrf(), map[string]string{"title": "Северный предел"})
	var project struct {
		ID string `json:"id"`
	}
	if response.StatusCode != http.StatusCreated || json.Unmarshal(body, &project) != nil {
		t.Fatalf("createProject = %d %s", response.StatusCode, body)
	}
	window.importFile(project.ID, "Застава у перевала.md", northernBook)
	indexed(t, window, project.ID)
	if waits, _ := window.waiting(project.ID); waits != "helper" {
		t.Fatalf("the reading without the helper waits for %q", waits)
	}
	readChapter(t, window, project.ID)
	window.read("/api/v1/desktop/account", nil)
	reachedOnly(t, proxy, window)

	// «Свой ИИ» at the made-up name of the provider: a question and the reading of the book reach its host alone.
	settings := map[string]any{"source": "own", "own": map[string]any{
		"preset": "custom", "baseUrl": "https://" + providerName + "/v1", "protocol": "chat", "chatModel": "north-chat", "cardsModel": "north-cards",
	}}
	if response, body := window.do(http.MethodPut, "/api/v1/desktop/ai", window.csrf(), settings); response.StatusCode != http.StatusOK {
		t.Fatalf("updateAiSettings = %d %s", response.StatusCode, body)
	}
	response, body = window.post("/api/v1/projects/"+project.ID+"/chats", map[string]string{"sourceView": "published", "scope": "project"}, "chat-of-the-proxy")
	var chat struct {
		ID string `json:"id"`
	}
	if response.StatusCode != http.StatusCreated || json.Unmarshal(body, &chat) != nil {
		t.Fatalf("createChatSession = %d %s", response.StatusCode, body)
	}
	turn, status, body := window.ask(chat.ID, "Где застава?", "question-of-the-proxy")
	if status != http.StatusAccepted || window.jobEnded(turn, 2*time.Minute) != "succeeded" {
		t.Fatalf("a question through the proxy = %d %s", status, body)
	}
	consentedAt := time.Now().Add(-time.Second)
	if response, body := window.do(http.MethodPut, "/api/v1/projects/"+project.ID+"/reading/consent", window.csrf(), map[string]any{"state": "allowed"}); response.StatusCode != http.StatusOK {
		t.Fatalf("setReadingConsent = %d %s", response.StatusCode, body)
	}
	if status, code := cardsJobEnded(t, data, project.ID, consentedAt, 2*time.Minute); status != "succeeded" {
		t.Fatalf("the cards Job through the proxy = %s %s", status, code)
	}
	readChapter(t, window, project.ID)
	if len(provider.seen()) == 0 {
		t.Fatal("the provider heard nothing")
	}
	reachedOnly(t, proxy, window, providerName)
	if journal := journalHosts(t, window); !slices.Equal(journal, []string{providerName}) {
		t.Fatalf("the journal names %v", journal)
	}
	journalHoldsNoWords(t, data, "Где застава", "сигнальные огни")

	// The link with the account: the host of the site besides, named by the list of the network.
	linkStart, body := window.do(http.MethodPost, "/api/v1/desktop/account/link", window.csrf(), nil)
	var waiting accountState
	if linkStart.StatusCode != http.StatusOK || json.Unmarshal(body, &waiting) != nil || waiting.Link == nil {
		t.Fatalf("startAccountLink = %d %s", linkStart.StatusCode, body)
	}
	state := strings.SplitN(strings.SplitN(waiting.Link.Address, "state=", 2)[1], "&", 2)[0]
	callback, err := http.Get(window.origin + "/desktop/link/callback?" + returnQuery(codeMark, state))
	if err != nil {
		t.Fatal(err)
	}
	_ = callback.Body.Close()
	if records := shell.Records(); callback.StatusCode != http.StatusOK || records[domain.SecretAccountPass] != passMark {
		t.Fatalf("the return of the link = %d", callback.StatusCode)
	}
	var account accountState
	window.read("/api/v1/desktop/account?fresh=true", &account)
	if account.State != "linked" {
		t.Fatalf("the account is %s", account.State)
	}
	reachedOnly(t, proxy, window, providerName, siteName)
	if network := networkHosts(t, window); !slices.Contains(network, siteName) || !slices.Contains(network, providerName) {
		t.Fatalf("the network of the linked app = %v", network)
	}
}

// readChapter reads the first chapter of the first book of a saga, as the reader of the window does.
func readChapter(t *testing.T, window *client, projectID string) {
	t.Helper()
	var books struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	window.read("/api/v1/projects/"+projectID+"/books", &books)
	if len(books.Data) == 0 {
		t.Fatal("no book")
	}
	var chapters struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	window.read("/api/v1/books/"+books.Data[0].ID+"/chapters", &chapters)
	if len(chapters.Data) == 0 {
		t.Fatal("no chapter")
	}
	window.read("/api/v1/chapters/"+chapters.Data[0].ID, nil)
}

// journalTables are the tables of the journal of what went and of its requests (specs/2026-11-14-desk-journal.md JR1.8).
var journalTables = []string{"model_disclosures", "model_disclosures_pending", "model_thread_recipients", "model_requests", "reading_sample_requests"}

// journalHoldsNoWords fails the test when a row of the journal holds a word of the author — the question asked or a line of
// the book —, while the book itself holds its line (the mark is real). The journal names places, sizes, recipients and
// outcomes alone (JR1.3).
func journalHoldsNoWords(t *testing.T, data string, question, line string) {
	t.Helper()
	connection := database(t, data)
	var inBook bool
	if err := connection.QueryRow(t.Context(), `SELECT EXISTS (SELECT 1 FROM chapter_paragraphs WHERE plain_text ILIKE '%' || $1 || '%')`, line).Scan(&inBook); err != nil || !inBook {
		t.Fatalf("the line of the book is not in the book: %v", err)
	}
	for _, table := range journalTables {
		var rows int
		var words bool
		if err := connection.QueryRow(t.Context(), `SELECT count(*), coalesce(bool_or(row_to_json(t)::text ILIKE '%' || $1 || '%' OR row_to_json(t)::text ILIKE '%' || $2 || '%'), false) FROM `+table+` t`,
			question, line).Scan(&rows, &words); err != nil {
			t.Fatal(err)
		}
		if words {
			t.Fatalf("a row of %s holds a word of the author", table)
		}
		t.Logf("%s: %d rows, no word of the author", table, rows)
	}
}

// indexed waits until the import of the northern book and the indexes of its text ended, nothing else working: a question
// before it is refused start_indexing.
func indexed(t *testing.T, window *client, projectID string) {
	t.Helper()
	for deadline := time.Now().Add(90 * time.Second); ; time.Sleep(200 * time.Millisecond) {
		var list struct {
			Data []struct {
				Kind   string `json:"kind"`
				Status string `json:"status"`
			} `json:"data"`
		}
		window.read("/api/v1/projects/"+projectID+"/jobs", &list)
		working, done := false, 0
		for _, job := range list.Data {
			working = working || job.Status == "queued" || job.Status == "running"
			if job.Kind == "canon_text_index" && job.Status == "succeeded" {
				done++
			}
		}
		if !working && done == 2 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the import and the text index did not end: %+v", list.Data)
		}
	}
}
