// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"canonkeeper/backend/internal/app/desktop/desktoptest"

	"github.com/jackc/pgx/v5"
)

// anotherChat opens one more chat of the saga.
func (run accountRun) anotherChat(t *testing.T, projectID, key string) string {
	t.Helper()
	response, body := run.window.post("/api/v1/projects/"+projectID+"/chats", map[string]string{"sourceView": "published", "scope": "project"}, key)
	var chat struct {
		ID string `json:"id"`
	}
	if response.StatusCode != http.StatusCreated || json.Unmarshal(body, &chat) != nil {
		t.Fatalf("createChatSession = %d %s", response.StatusCode, body)
	}
	return chat.ID
}

// holdTurns makes the stub hold every request of a turn until the function it answers lets them go; arrived waits for a
// count of held requests.
func (site *accountAISite) holdTurns(t *testing.T) (arrived func(count int), release func()) {
	t.Helper()
	site.mu.Lock()
	site.holdAll, site.arrivals = make(chan struct{}), make(chan string, 16)
	hold, arrivals := site.holdAll, site.arrivals
	site.mu.Unlock()
	arrived = func(count int) {
		t.Helper()
		for range count {
			select {
			case <-arrivals:
			case <-time.After(desktoptest.Waited(3 * time.Minute)):
				t.Fatal("a turn never reached the site")
			}
		}
	}
	release = func() {
		site.mu.Lock()
		site.holdAll = nil
		site.mu.Unlock()
		close(hold)
	}
	return arrived, release
}

// asked asks a question and fails the test unless the turn starts.
func (run accountRun) asked(t *testing.T, chatID, question, key string) string {
	t.Helper()
	turn, status, body := run.window.ask(chatID, question, key)
	if status != http.StatusAccepted {
		t.Fatalf("the question %q = %d %s", key, status, body)
	}
	return turn
}

// specs/2026-11-13-desk-account-ai.md AI2.3 (three turns at «разом» four, the first two longer than the lease): the app has two
// workers of the chat, so the third turn waits in the queue while two run at the site; the lease of the site ends its permit
// unused meanwhile — its unit comes back. Taken by a worker, the third turn asks its permit by the same ref, finds it gone by
// unused and opens the next number through the fence of the site: one permit more, the turn answers, one unit counted for it.
// When the site refuses the next number by the tier, the turn ends with the code of the tier before the model — not a failure
// of the worker — and its question did not count.
func TestATurnThatWaitedLongerThanTheLeaseTakesANewPermit(t *testing.T) {
	resources := processResources(t)
	site := newAccountAISite(t)
	data := filepath.Join(t.TempDir(), "data")
	run := site.launchAccount(t, site.accountProgram(t), resources, data)
	saga, first := run.chat(t, "Северный предел")
	second, third := run.anotherChat(t, saga.project, "chat-second"), run.anotherChat(t, saga.project, "chat-third")

	arrived, release := site.holdTurns(t)
	one := run.asked(t, first, "Кто зажёг огни?", "question-one")
	two := run.asked(t, second, "Где стоит застава?", "question-two")
	arrived(2)
	three := run.asked(t, third, questionMark, "question-three")
	awaitTrue(t, "the permit of the waiting turn", time.Minute, func() bool { return site.opened("chat_turn") == 3 })
	if expired := site.expireUnused(); expired != 1 {
		t.Fatalf("the lease ended %d permits unused", expired)
	}
	release()
	for _, turn := range []string{one, two, three} {
		if status := run.window.jobEnded(turn, 3*time.Minute); status != "succeeded" {
			_, code, _ := run.jobOf(turn)
			t.Fatalf("the turn %s = %s %s", turn, status, code)
		}
	}
	if site.opened("chat_turn") != 4 {
		t.Fatalf("the site opened %d permits of turns; want one more for the turn that waited", site.opened("chat_turn"))
	}
	if _, _, returned := run.settledJobOf(t, three); returned != "no" {
		t.Fatalf("the question of the turn that waited came back: %q", returned)
	}

	// The tier refuses the next number of a turn that waited: the code of the tier, before the model.
	arrived, release = site.holdTurns(t)
	four := run.asked(t, first, "А где Вера?", "question-four")
	five := run.asked(t, second, "Что за белый порт?", "question-five")
	arrived(2)
	six := run.asked(t, third, "Кто ждал на стене?", "question-six")
	awaitTrue(t, "the permit of the second waiting turn", time.Minute, func() bool { return site.opened("chat_turn") == 7 })
	if expired := site.expireUnused(); expired != 1 {
		t.Fatalf("the lease ended %d permits unused", expired)
	}
	site.mu.Lock()
	site.refuseNew = &siteRefusal{status: http.StatusTooManyRequests, code: "quota.chat_turns_daily", retry: "3600"}
	site.mu.Unlock()
	release()
	for _, turn := range []string{four, five} {
		if status := run.window.jobEnded(turn, 3*time.Minute); status != "succeeded" {
			t.Fatalf("the turn %s = %s", turn, status)
		}
	}
	if status := run.window.jobEnded(six, 3*time.Minute); status != "failed" {
		t.Fatalf("the turn refused by the tier = %s", status)
	}
	if _, code, returned := run.settledJobOf(t, six); code != "quota.chat_turns_daily" || returned != "yes" {
		t.Fatalf("the turn refused by the tier = %q, came back %q", code, returned)
	}
}

// AI2.2, AI2.3, AI2.9 (the kill of the process mid-turn and a new start — the same ref, one permit): the salt of the refs is
// kept in settings.json, so the turn a crash left, taken again by a worker of the next start, asks the site by the ref of
// its start and gets the same permit — no second one is opened or paid. The desktop build revokes the sessions of the local
// author at every start (services/desktop/local_author.go), so the turn ends model.session_ended before the model
// (specs/2026-11-15-desk-interrupted-turn.md IT1.1), and its permit is closed by that ref. The kill dropped a request of it
// mid-stream, which the new run does not know, so its question is pending, not yes.
func TestAKilledTurnAsksItsPermitByTheSameRef(t *testing.T) {
	resources := processResources(t)
	site := newAccountAISite(t)
	data := filepath.Join(t.TempDir(), "data")
	binary := site.accountProgram(t)
	run := site.launchAccount(t, binary, resources, data)
	_, chatID := run.chat(t, "Северный предел")
	site.mu.Lock()
	site.holdTurn, site.held = make(chan struct{}), make(chan struct{})
	held := site.held
	site.mu.Unlock()
	turn := run.asked(t, chatID, questionMark, "question-killed")
	select {
	case <-held:
	case <-time.After(desktoptest.Waited(3 * time.Minute)):
		t.Fatal("the turn never reached the site")
	}
	if err := run.process.Command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = run.process.Wait(time.Minute)
	site.mu.Lock()
	site.holdTurn, site.held = nil, nil
	site.mu.Unlock()
	before := len(site.heardOf("/api/v1/app/permits"))

	again := site.launchAccount(t, binary, resources, data)
	again.window.jobEnded(turn, 4*time.Minute)
	if _, code, returned := again.jobOf(turn); code != "model.session_ended" || returned != "pending" {
		t.Fatalf("the turn the crash left ended with %q, its question came back %q", code, returned)
	}
	opens := site.heardOf("/api/v1/app/permits")
	if site.opened("chat_turn") != 1 || len(opens) <= before {
		t.Fatalf("the site opened %d permits of turns; the turn taken again asked %d times", site.opened("chat_turn"), len(opens)-before)
	}
	for _, open := range opens[1:] {
		if open.body["ref"] != opens[0].body["ref"] {
			t.Fatal("the turn taken again asked by another ref")
		}
	}
}

// AI1.4, AI1.5 (one chat: a turn on «Свой ИИ», the source changed to the account mid-turn, the next turn through the site, then
// «Свой ИИ» again): the source a turn took at its start serves it to its end — the turn of «Свой ИИ» answers at its provider
// while the settings already name the account, and the site hears no request of it —; the next turn goes to the site under its
// own permit, and the one after it to the provider again; every turn of the one chat answers, and no permit is opened beside
// the one of the turn of the account.
func TestTheSourceOfATurnIsItsOwnInOneChat(t *testing.T) {
	resources := processResources(t)
	site := newAccountAISite(t)
	provider := &providerOfTheTest{}
	stub := httptest.NewServer(provider)
	defer stub.Close()
	data := filepath.Join(t.TempDir(), "data")
	run := site.launchAccount(t, site.accountProgram(t), resources, data)
	_, chatID := run.chat(t, "Северный предел")
	own := map[string]any{"source": "own", "own": map[string]any{
		"preset": "custom", "baseUrl": stub.URL + "/v1", "protocol": "chat", "chatModel": "north-chat", "cardsModel": "north-cards",
	}}
	choose := func(settings map[string]any) {
		t.Helper()
		if response, body := run.window.do(http.MethodPut, "/api/v1/desktop/ai", run.window.csrf(), settings); response.StatusCode != http.StatusOK {
			t.Fatalf("updateAiSettings %v = %d %s", settings["source"], response.StatusCode, body)
		}
	}
	chats := func() int {
		count := 0
		for _, request := range provider.seen() {
			if request.model == "north-chat" {
				count++
			}
		}
		return count
	}
	choose(own)
	first := run.asked(t, chatID, longQuestion, "question-of-the-own")
	provider.holding(t, 3*time.Minute)
	choose(map[string]any{"source": "account"})
	if status := run.window.jobEnded(first, 3*time.Minute); status != "succeeded" {
		t.Fatalf("the turn of «Свой ИИ» = %s", status)
	}
	site.mu.Lock()
	heardTurns := len(site.turns)
	site.mu.Unlock()
	if heardTurns != 0 || site.opened("chat_turn") != 0 || chats() == 0 {
		t.Fatalf("the turn of «Свой ИИ» went to the site: %d requests, %d permits", heardTurns, site.opened("chat_turn"))
	}
	ownRequests := chats()
	second := run.asked(t, chatID, "Где Вера?", "question-of-the-account")
	if status := run.window.jobEnded(second, 3*time.Minute); status != "succeeded" {
		t.Fatalf("the turn of the account = %s", status)
	}
	site.mu.Lock()
	heardTurns = len(site.turns)
	site.mu.Unlock()
	if heardTurns == 0 || site.opened("chat_turn") != 1 || chats() != ownRequests {
		t.Fatalf("the turn of the account: %d requests at the site, %d permits, the provider %d → %d", heardTurns, site.opened("chat_turn"), ownRequests, chats())
	}
	choose(own)
	third := run.asked(t, chatID, "Кто ждал на стене?", "question-of-the-own-again")
	if status := run.window.jobEnded(third, 3*time.Minute); status != "succeeded" {
		t.Fatalf("the turn of «Свой ИИ» again = %s", status)
	}
	if site.opened("chat_turn") != 1 || chats() == ownRequests {
		t.Fatalf("the turn of «Свой ИИ» again: %d permits, the provider %d → %d", site.opened("chat_turn"), ownRequests, chats())
	}
}

// AI4.1, AI4.2 (after the refusals of the table AI2.5 and broken connections: the marks of the pass, of the ids of the
// permits, of the refs and the words of the question nowhere): an open whose answer was lost and a close whose answer was
// lost, the lasting refusals of the table — the site without models, the permit ended, a thread too large, an app the site
// does not take —, a saga file of the saga and a copy before an update: the marks are not in the output of the backend, the
// files of its data directory, the homes of Codex, the saga file, the copy, the environment and the arguments of its
// processes, nor in any row of the database, asked by queries.
func TestNoMarkOfTheAccountLeavesTheApp(t *testing.T) {
	resources := processResources(t)
	site := newAccountAISite(t)
	data := filepath.Join(t.TempDir(), "data")
	run := site.launchAccount(t, site.accountProgram(t), resources, data)
	saga, chatID := run.chat(t, "Северный предел")

	site.mu.Lock()
	site.drops["open"], site.drops["close"] = 1, 1
	site.mu.Unlock()
	answered := run.asked(t, chatID, questionMark, "question-of-the-lost-answers")
	if status := run.window.jobEnded(answered, 3*time.Minute); status != "succeeded" || site.opened("chat_turn") != 1 {
		t.Fatalf("a question whose open lost its answer = %s, %d permits", status, site.opened("chat_turn"))
	}
	for _, refusal := range []struct {
		code, want string
		status     int
		chat       string
	}{
		{"ai.unavailable", "model.site_unavailable", http.StatusServiceUnavailable, chatID},
		{"app.permit_closed", "model.permit_ended", http.StatusConflict, chatID},
		{"app.request_too_large", "model.thread_too_large", http.StatusRequestEntityTooLarge, run.anotherChat(t, saga.project, "chat-too-large")},
		{"request.validation", "model.app_unfit", http.StatusBadRequest, chatID},
	} {
		site.mu.Lock()
		site.turnRefusal = &siteRefusal{status: refusal.status, code: refusal.code}
		site.mu.Unlock()
		turn := run.asked(t, refusal.chat, "Кто зажёг огни?", "question-of-"+refusal.code)
		if status := run.window.jobEnded(turn, 2*time.Minute); status != "failed" {
			t.Fatalf("%s: the turn = %s", refusal.code, status)
		}
		if _, code, _ := run.jobOf(turn); code != refusal.want {
			t.Fatalf("%s: the code of the turn = %q", refusal.code, code)
		}
	}
	if closes := site.closed(); len(closes) < 5 {
		t.Fatalf("the closes = %v", closes)
	}
	site.mu.Lock()
	marks := []string{permitMarkPrefix, passMark}
	for ref := range site.refs {
		marks = append(marks, ref)
	}
	site.mu.Unlock()

	// The saga file of the saga.
	response, archive := run.window.do(http.MethodGet, "/api/v1/projects/"+saga.project+"/saga-archive", "", nil)
	if response.StatusCode != http.StatusOK || len(archive) == 0 {
		t.Fatalf("getSagaArchive = %d", response.StatusCode)
	}
	if found := markIn(archive, marks); found {
		t.Error("the saga file holds a mark")
	}
	if entries, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive))); err == nil {
		for _, entry := range entries.File {
			reader, err := entry.Open()
			if err != nil {
				continue
			}
			raw, _ := io.ReadAll(reader)
			_ = reader.Close()
			if markIn(raw, marks) {
				t.Errorf("the record %s of the saga file holds a mark", entry.Name)
			}
		}
	} else {
		t.Fatalf("the saga file does not read: %v", err)
	}
	marksAreNowhere(t, run, marks, questionMark)

	// A copy before an update: a later program with one more migration copies the data first.
	run.process.End()
	later := program(t, programVariant{
		name: "account-ai-later-" + filepath.Base(t.TempDir()), ahead: 1, version: versionOfTheLink, site: site.url, retryPause: "10ms", resumeEvery: "2s",
	})
	updated := site.launchAccount(t, later, resources, data)
	copies := 0
	_ = filepath.WalkDir(filepath.Join(data, "backups"), func(path string, entry fs.DirEntry, err error) error {
		if err != nil || !entry.Type().IsRegular() {
			return nil
		}
		copies++
		if raw, err := os.ReadFile(path); err == nil && markIn(raw, marks) {
			t.Errorf("%s of the copy before the update holds a mark", path)
		}
		return nil
	})
	if copies == 0 {
		t.Fatal("the later program made no copy before its migration")
	}
	marksAreNowhere(t, updated, marks, questionMark)
}

// markIn says whether raw holds any of the marks; it never says which.
func markIn(raw []byte, marks []string) bool {
	for _, mark := range marks {
		if bytes.Contains(raw, []byte(mark)) {
			return true
		}
	}
	return false
}

// queryMarks counts the rows of every table of the database whose text holds a mark: the database is asked by queries, not
// by the files of its cluster — as its administrator, who reads every table; a table that does not answer fails the test.
func queryMarks(t *testing.T, data string, marks []string) int {
	t.Helper()
	cluster := runningCluster(t, data)
	connection, err := pgx.Connect(context.Background(), cluster.url(adminRole, cluster.secrets.Admin, databaseName, url.Values{}))
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close(context.Background())
	rows, err := connection.Query(context.Background(), `SELECT tablename FROM pg_tables WHERE schemaname = 'public'`)
	if err != nil {
		t.Fatal(err)
	}
	tables, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil || len(tables) == 0 {
		t.Fatalf("the tables of the database = %d, %v", len(tables), err)
	}
	patterns := make([]string, 0, len(marks))
	for _, mark := range marks {
		patterns = append(patterns, "%"+mark+"%")
	}
	found := 0
	for _, table := range tables {
		var count int
		if err := connection.QueryRow(context.Background(),
			`SELECT count(*) FROM public.`+quoteIdentifier(table)+` AS row_of WHERE row_of::text LIKE ANY ($1)`, patterns).Scan(&count); err != nil {
			t.Fatalf("the table %s was not asked: %v", table, err)
		}
		found += count
	}
	return found
}

func quoteIdentifier(name string) string {
	return `"` + string(bytes.ReplaceAll([]byte(name), []byte(`"`), []byte(`""`))) + `"`
}
