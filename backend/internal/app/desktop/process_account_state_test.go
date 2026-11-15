// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"canonkeeper/backend/internal/app/desktop/desktoptest"
	accountdomain "canonkeeper/backend/internal/domain/accountlink"
	domain "canonkeeper/backend/internal/domain/desktop"
)

// newPassMark is the pass of a second link of the same app.
var newPassMark = "ckd_" + padded("pass-mark-of-a-new-link-")

const (
	accountPath = "/api/v1/app/account"
	devicePath  = "/api/v1/app/device"
	tokenPath   = "/api/v1/app/link/token"
)

// linkedRecords are the records of a shell that keeps the pass of the stub: the app starts linked.
func linkedRecords() map[string]string { return map[string]string{domain.SecretAccountPass: passMark} }

// answering is the stub answering path its own way and every other path as the site.
func answering(path string, answer func(w http.ResponseWriter, r *http.Request)) func(http.ResponseWriter, *http.Request, heard) {
	return func(w http.ResponseWriter, r *http.Request, request heard) {
		if request.path == path {
			answer(w, r)
			return
		}
		theSite(w, r, request)
	}
}

// silentUntilLeft is a site that does not answer: it holds the request until the app leaves it.
func silentUntilLeft(_ http.ResponseWriter, r *http.Request) { <-r.Context().Done() }

// refusedPass is the one refusal of a pass of the site.
func refusedPass(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.Header().Set("WWW-Authenticate", "Bearer")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = io.WriteString(w, `{"code":"app.pass_invalid","status":401}`)
}

// fresh is getAccount with fresh=true: the page asks the site now.
func (run *linked) fresh(t *testing.T) (accountState, []byte) {
	t.Helper()
	return run.account(t, "/api/v1/desktop/account?fresh=true")
}

// asked are the changes of the record of the pass the backend asked the shell for, since the first of them.
func (run *linked) asked(from int) []desktoptest.Message {
	var changes []desktoptest.Message
	for _, message := range run.shell.Asked()[from:] {
		if message.ID == domain.SecretAccountPass {
			changes = append(changes, message)
		}
	}
	return changes
}

// awaitRemoval waits for the backend to ask the shell to remove the record of the pass.
func (run *linked) awaitRemoval(t *testing.T, from int) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		for _, change := range run.asked(from) {
			if change.Type == "secret-delete" {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("the backend did not ask the shell to remove the record of the pass")
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// books is a saga of the app with a book whose text is indexed: what the author reads and finds without the site.
type books struct {
	project, chapter string
}

// saga makes the saga «Северный предел» with its book and waits until its text is found.
func (run *linked) saga(t *testing.T) books {
	t.Helper()
	window := run.window
	response, body := window.Do(http.MethodPost, "/api/v1/projects", window.CSRF(), map[string]string{"title": "Северный предел"})
	var project struct {
		ID string `json:"id"`
	}
	if response.StatusCode != http.StatusCreated || json.Unmarshal(body, &project) != nil {
		t.Fatalf("createProject = %d %s", response.StatusCode, body)
	}
	var file bytes.Buffer
	form := multipart.NewWriter(&file)
	part, _ := form.CreateFormFile("file", "Застава у перевала.md")
	_, _ = io.WriteString(part, northernBook)
	_ = form.Close()
	request, _ := http.NewRequest(http.MethodPost, window.Origin+"/api/v1/projects/"+project.ID+"/imports", &file)
	request.Header.Set("Content-Type", form.FormDataContentType())
	request.Header.Set("X-CSRF-Token", window.CSRF())
	request.Header.Set("Idempotency-Key", "import-of-the-northern-book")
	if response, body := window.Send(request); response.StatusCode != http.StatusAccepted {
		t.Fatalf("importBookFile = %d %s", response.StatusCode, body)
	}
	saga := books{project: project.ID}
	deadline := time.Now().Add(90 * time.Second)
	for !saga.found(t, window) {
		if time.Now().After(deadline) {
			t.Fatal("the book was not found in 90 s")
		}
		time.Sleep(200 * time.Millisecond)
	}
	var shelf struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if response, body := window.Do(http.MethodGet, "/api/v1/projects/"+project.ID+"/books", "", nil); response.StatusCode != http.StatusOK ||
		json.Unmarshal(body, &shelf) != nil || len(shelf.Data) != 1 {
		t.Fatalf("listBooks = %d %s", response.StatusCode, body)
	}
	var chapters struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if response, body := window.Do(http.MethodGet, "/api/v1/books/"+shelf.Data[0].ID+"/chapters", "", nil); response.StatusCode != http.StatusOK ||
		json.Unmarshal(body, &chapters) != nil || len(chapters.Data) != 2 {
		t.Fatalf("listChapters = %d %s", response.StatusCode, body)
	}
	saga.chapter = chapters.Data[0].ID
	return saga
}

// found says whether the search of the saga finds the second chapter.
func (saga books) found(t *testing.T, window *desktoptest.Window) bool {
	t.Helper()
	response, body := window.Do(http.MethodPost, "/api/v1/projects/"+saga.project+"/search", window.CSRF(), map[string]string{
		"query": "перевалу", "scope": "all", "sourceView": "published",
	})
	return response.StatusCode == http.StatusOK && strings.Contains(string(body), "Дозорные")
}

// work fails the test when the author cannot find and read his book: the link changes nothing of it (AC2.2).
func (saga books) work(t *testing.T, window *desktoptest.Window, state string) {
	t.Helper()
	if !saga.found(t, window) {
		t.Fatalf("%s: the search of the saga does not find the book", state)
	}
	if response, body := window.Do(http.MethodGet, "/api/v1/chapters/"+saga.chapter, "", nil); response.StatusCode != http.StatusOK ||
		!strings.Contains(string(body), "сигнальные огни") {
		t.Fatalf("%s: the chapter = %d", state, response.StatusCode)
	}
}

// specs/2026-11-13-desk-account.md AC2.1: a linked app tells the name, the address hidden after its first letter, the
// tier with its end, the questions of the day and the reading of 30 days the site told, the name of the computer the site
// sees, where the pass is kept and the pages of the site of the build. The site is asked once a minute and when the page
// asks; a site silent past its term leaves what it told with «нет связи»; after a restart without the site the app is
// linked and knows no name.
func TestALinkedAppTellsItsAccountAsTheSiteToldIt(t *testing.T) {
	resources := desktoptest.Resources(t)
	site := newSiteOfTheTest(t)
	binary := site.program(t)
	data := filepath.Join(t.TempDir(), "data")
	run := site.launch(t, binary, resources, data, linkedRecords(), desktoptest.KeepAll)
	host, _ := os.Hostname()
	state, body := run.account(t, "/api/v1/desktop/account")
	if state.State != "linked" || state.Account == nil || state.Account.Name != "Северный" || state.Account.Email != "a…@example.test" ||
		state.Account.Tier.Code != "writer" || state.Account.Tier.Title != "Писатель" || !bytes.Contains(body, []byte(`"until":"2026-12-13T00:00:00.000Z"`)) ||
		state.Account.Questions == nil || state.Account.Questions.Used != 3 || state.Account.Questions.Limit != 20 ||
		state.Account.Reading.Used != 3200000 || state.Account.Reading.Limit != 40000000 || !bytes.Contains(body, []byte(`"at":"`)) ||
		state.Unreachable || state.Computer != accountdomain.ComputerName(host) || state.PassStore != "file" ||
		state.Pages["tariffs"] != site.url+"/tariffs" || state.Pages["apps"] != site.url+"/account#apps" || bytes.Contains(body, []byte(passMark)) {
		t.Fatalf("a linked app = %s", body)
	}
	if reads := site.heardOf(accountPath); len(reads) != 1 || reads[0].authorization != "Bearer "+passMark {
		t.Fatalf("the site heard %d reads of the account", len(reads))
	}
	if run.account(t, "/api/v1/desktop/account"); len(site.heardOf(accountPath)) != 1 {
		t.Fatal("a second read in the minute asked the site")
	}
	if run.fresh(t); len(site.heardOf(accountPath)) != 2 {
		t.Fatal("the page asked and the site was not asked")
	}

	// The term of 5 s itself is TestTheSiteIsWaitedForFiveSeconds of services/accountlink; here the process waits for it
	// and does not hang.
	site.set(answering(accountPath, silentUntilLeft))
	began := time.Now()
	silent, _ := run.fresh(t)
	if waited := time.Since(began); waited < 4*time.Second || waited > 20*time.Second {
		t.Fatalf("a silent site was waited for %s", waited)
	}
	if silent.State != "linked" || !silent.Unreachable || silent.Account == nil || silent.Account.Name != "Северный" ||
		silent.Account.Questions == nil || silent.Account.Questions.Limit != 20 {
		t.Fatalf("a silent site = %+v", silent)
	}

	// A restart without the site: the pass says the app is linked, and nothing of the account is known.
	run.process.End()
	records := run.shell.Records()
	site.server.Close()
	again := site.launch(t, binary, resources, data, records, desktoptest.KeepAll)
	if state, body := again.account(t, "/api/v1/desktop/account"); state.State != "linked" || state.Account != nil || !state.Unreachable {
		t.Fatalf("after a restart without the site = %s", body)
	}
}

// AC2.2, AC2.3: the answer of the site says the state. 429 with its Retry-After, 500, a broken connection and any other
// answer are «нет связи» and keep the pass — 429 keeps the app from the site until its Retry-After —; app.pass_invalid
// breaks the link: the record of the pass goes through the shell and no request carries the pass any more. The author
// finds and reads his book in every state.
func TestTheAnswerOfTheSiteSaysTheStateOfTheLink(t *testing.T) {
	resources := desktoptest.Resources(t)
	site := newSiteOfTheTest(t)
	run := site.launch(t, site.program(t), resources, filepath.Join(t.TempDir(), "data"), linkedRecords(), desktoptest.KeepAll)
	saga := run.saga(t)
	if state, _ := run.account(t, "/api/v1/desktop/account"); state.State != "linked" || state.Account == nil {
		t.Fatalf("linked = %+v", state)
	}
	saga.work(t, run.window, "linked")

	for _, row := range []struct {
		name   string
		answer func(w http.ResponseWriter, r *http.Request)
	}{
		{"429", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Retry-After", "2")
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(w, `{"code":"rate_limit.exceeded","status":429}`)
		}},
		{"500", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusInternalServerError) }},
		{"broken", func(w http.ResponseWriter, _ *http.Request) {
			connection, _, _ := w.(http.Hijacker).Hijack()
			_ = connection.Close()
		}},
		{"another answer", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusForbidden)
			_, _ = io.WriteString(w, `{"code":"app.upgrade_required","status":403}`)
		}},
	} {
		site.set(answering(accountPath, row.answer))
		asked := len(run.shell.Asked())
		before := len(site.heardOf(accountPath))
		state, body := run.fresh(t)
		// A broken connection that was kept alive is asked once more by the transport of Go, as any idempotent request.
		if state.State != "linked" || !state.Unreachable || state.Account == nil || state.Account.Name != "Северный" ||
			len(site.heardOf(accountPath)) <= before {
			t.Fatalf("%s: %s", row.name, body)
		}
		if run.shell.Records()[domain.SecretAccountPass] != passMark || len(run.asked(asked)) != 0 {
			t.Fatalf("%s: the record of the pass changed", row.name)
		}
		if row.name == "429" {
			// Until its Retry-After the site is not asked, even when the page asks: a read that reaches it comes 2 s after
			// the 429 or later, however slow the machine is.
			refused := site.heardOf(accountPath)
			if state, _ := run.fresh(t); !state.Unreachable {
				t.Fatal("429: the site is not unreachable until its Retry-After")
			}
			for _, request := range site.heardOf(accountPath)[len(refused):] {
				if request.at.Sub(refused[len(refused)-1].at) < 2*time.Second {
					t.Fatal("429: the site was asked before its Retry-After")
				}
			}
			time.Sleep(2100 * time.Millisecond)
		}
		saga.work(t, run.window, row.name)
	}

	site.set(answering(accountPath, refusedPass))
	asked := len(run.shell.Asked())
	state, body := run.fresh(t)
	if state.State != "broken" || state.Account != nil || bytes.Contains(body, []byte(passMark)) {
		t.Fatalf("a refused pass = %s", body)
	}
	run.awaitRemoval(t, asked)
	if _, kept := run.shell.Records()[domain.SecretAccountPass]; kept {
		t.Fatal("the shell keeps the record of a refused pass")
	}
	requests := site.all()
	if state, _ := run.fresh(t); state.State != "broken" || site.all() != requests {
		t.Fatalf("after the refusal = %+v; the site heard %d more", state, site.all()-requests)
	}
	saga.work(t, run.window, "broken")
	if query := run.start(t); query.Get("state") == "" {
		t.Fatal("a broken link does not start again")
	}
}

// AC2.3: a refusal that comes to the pass of an unlinked link does not touch the new link made since; a record the shell
// did not remove is not used in this run, and the next start removes it first and is not linked.
func TestARefusalOfAnOldPassLeavesTheNewLinkAndALeftRecordGoesAtTheNextStart(t *testing.T) {
	resources := desktoptest.Resources(t)
	site := newSiteOfTheTest(t)
	binary := site.program(t)
	data := filepath.Join(t.TempDir(), "data")
	run := site.launch(t, binary, resources, data, linkedRecords(), desktoptest.KeepAll)

	// The page asks for the account; the site holds the answer for the old pass and refuses it once the app linked anew.
	release := make(chan struct{})
	site.set(func(w http.ResponseWriter, r *http.Request, request heard) {
		switch {
		case request.path == accountPath && request.authorization == "Bearer "+passMark:
			<-release
			refusedPass(w, r)
		case request.path == tokenPath:
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"pass":"`+newPassMark+`","account":{"name":"Северный","email":"a…@example.test",
				"tier":{"code":"writer","title":"Писатель","until":null},"reading":{"used":0,"limit":40000000}}}`)
		default:
			theSite(w, r, request)
		}
	})
	held := make(chan accountState, 1)
	go func() {
		state, _ := run.fresh(t)
		held <- state
	}()
	deadline := time.Now().Add(10 * time.Second)
	for len(site.heardOf(accountPath)) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the read of the account did not reach the site")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if response, body := run.window.Do(http.MethodDelete, "/api/v1/desktop/account", run.window.CSRF(), nil); response.StatusCode != http.StatusOK ||
		!bytes.Contains(body, []byte(`"siteUnlinked":true`)) || !bytes.Contains(body, []byte(`"passRemoved":true`)) {
		t.Fatalf("unlinkAccount = %d %s", response.StatusCode, body)
	}
	query := run.start(t)
	if status, _, _ := run.back(t, http.MethodGet, returnQuery(codeMark, query.Get("state")), nil); status != http.StatusOK {
		t.Fatalf("the new link = %d", status)
	}
	asked := len(run.shell.Asked())
	close(release)
	select {
	case state := <-held:
		if state.State != "linked" {
			t.Fatalf("the refusal of the old pass answered %+v", state)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the read with the old pass did not end")
	}
	if state, _ := run.fresh(t); state.State != "linked" || state.Unreachable || state.Account == nil {
		t.Fatalf("the new link after the refusal of the old pass = %+v", state)
	}
	if run.shell.Records()[domain.SecretAccountPass] != newPassMark || len(run.asked(asked)) != 0 {
		t.Fatal("the refusal of the old pass touched the record of the new one")
	}
	if reads := site.heardOf(accountPath); reads[len(reads)-1].authorization != "Bearer "+newPassMark {
		t.Fatal("the app reads the account with the old pass")
	}

	// The shell does not remove the record: the app unlinks in this run all the same, and the next start removes it.
	run.shell.Answer(func(message desktoptest.Message) desktoptest.Reply {
		if message.Type == "secret-delete" {
			return desktoptest.Refuse
		}
		return desktoptest.Keep
	})
	if response, body := run.window.Do(http.MethodDelete, "/api/v1/desktop/account", run.window.CSRF(), nil); response.StatusCode != http.StatusOK ||
		!bytes.Contains(body, []byte(`"siteUnlinked":true`)) || !bytes.Contains(body, []byte(`"passRemoved":false`)) {
		t.Fatalf("unlinkAccount with a shell that kept the record = %d %s", response.StatusCode, body)
	}
	requests := site.all()
	if state, _ := run.fresh(t); state.State != "none" || site.all() != requests {
		t.Fatalf("after unlinking = %+v; the site heard %d more", state, site.all()-requests)
	}
	records := run.shell.Records()
	if records[domain.SecretAccountPass] != newPassMark {
		t.Fatal("the shell of the test removed the record it refused to remove")
	}
	run.process.End()
	settings, err := os.ReadFile(DataDir{Root: data}.Settings())
	if err != nil || bytes.Contains(settings, []byte(newPassMark)) || !bytes.Contains(settings, []byte(`"account"`)) {
		t.Fatalf("settings.json does not mark the record: %v", err)
	}

	again := site.launch(t, binary, resources, data, records, desktoptest.KeepAll)
	again.awaitRemoval(t, 0)
	if _, kept := again.shell.Records()[domain.SecretAccountPass]; kept {
		t.Fatal("the next start did not remove the record")
	}
	if state, _ := again.fresh(t); state.State != "none" || site.all() != requests {
		t.Fatalf("the next start = %+v; the site heard %d more", state, site.all()-requests)
	}
	deadline = time.Now().Add(10 * time.Second)
	for {
		settings, err := os.ReadFile(DataDir{Root: data}.Settings())
		if err == nil && !bytes.Contains(settings, []byte(`"account"`)) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the mark of the removed record stays")
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// AC2.4, AC2.5: unlinking tells a site that answers — unlinkApp with the pass — and removes the record; a silent site is
// not told, the record goes all the same and the answer says so. «Стереть все данные» asks the site to unlink and erases
// when the site is silent, within its term.
func TestUnlinkingAndErasingTellTheSiteWhenItAnswers(t *testing.T) {
	resources := desktoptest.Resources(t)
	site := newSiteOfTheTest(t)
	run := site.launch(t, site.program(t), resources, filepath.Join(t.TempDir(), "data"), linkedRecords(), desktoptest.KeepAll)
	unlink := func() []byte {
		t.Helper()
		response, body := run.window.Do(http.MethodDelete, "/api/v1/desktop/account", run.window.CSRF(), nil)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("unlinkAccount = %d %s", response.StatusCode, body)
		}
		return body
	}
	relink := func() string {
		t.Helper()
		query := run.start(t)
		if status, _, _ := run.back(t, http.MethodGet, returnQuery(codeMark, query.Get("state")), nil); status != http.StatusOK {
			t.Fatalf("the link = %d", status)
		}
		return query.Get("state")
	}

	if body := unlink(); !bytes.Contains(body, []byte(`"siteUnlinked":true`)) || !bytes.Contains(body, []byte(`"passRemoved":true`)) {
		t.Fatalf("unlinking with the site = %s", body)
	}
	if told := site.heardOf(devicePath); len(told) != 1 || told[0].method != http.MethodDelete || told[0].authorization != "Bearer "+passMark {
		t.Fatalf("the site heard %d unlinkings", len(told))
	}
	if _, kept := run.shell.Records()[domain.SecretAccountPass]; kept {
		t.Fatal("the record stays after unlinking")
	}
	if state, _ := run.account(t, "/api/v1/desktop/account"); state.State != "none" {
		t.Fatalf("after unlinking = %+v", state)
	}

	relink()
	site.set(answering(devicePath, silentUntilLeft))
	began := time.Now()
	if body := unlink(); !bytes.Contains(body, []byte(`"siteUnlinked":false`)) || !bytes.Contains(body, []byte(`"passRemoved":true`)) {
		t.Fatalf("unlinking without the site = %s", body)
	}
	if waited := time.Since(began); waited > 20*time.Second {
		t.Fatalf("unlinking waited %s for a silent site", waited)
	}
	if _, kept := run.shell.Records()[domain.SecretAccountPass]; kept {
		t.Fatal("the record stays after unlinking without the site")
	}
	if state, _ := run.account(t, "/api/v1/desktop/account"); state.State != "none" {
		t.Fatalf("after unlinking without the site = %+v", state)
	}

	site.set(theSite)
	theMarksAreNowhere(t, run, relink())
	site.set(answering(devicePath, silentUntilLeft))
	unlinkings := len(site.heardOf(devicePath))
	began = time.Now()
	if response, body := run.window.Do(http.MethodPost, "/api/v1/desktop/erase", run.window.CSRF(), map[string]string{"confirm": "стереть"}); response.StatusCode != http.StatusAccepted {
		t.Fatalf("eraseDesktopData = %d %s", response.StatusCode, body)
	}
	if waited := time.Since(began); waited > 20*time.Second {
		t.Fatalf("the erasure waited %s for a silent site", waited)
	}
	if _, asked := run.shell.Await("erase", 10*time.Second); !asked {
		t.Fatal("the shell was not asked to erase")
	}
	if told := site.heardOf(devicePath)[unlinkings:]; len(told) != 1 || told[0].authorization != "Bearer "+passMark {
		t.Fatalf("the erasure asked the site %d times", len(told))
	}
}
