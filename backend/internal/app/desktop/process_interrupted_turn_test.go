// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"canonkeeper/backend/internal/app/desktop/desktoptest"
)

// questionsAfterACrash waits for the turns a crash left to end in the next run and fails the test unless each ended
// model.session_ended and its question tells returned.
func questionsAfterACrash(t *testing.T, run accountRun, returned string, turns ...string) {
	t.Helper()
	for _, turn := range turns {
		status := run.window.jobEnded(turn, 2*time.Minute)
		if _, code, told := run.jobOf(turn); status != "failed" || code != "model.session_ended" || told != returned {
			t.Fatalf("the turn %s a crash left = %s %q, its question came back %q (want %q)", turn, status, code, told, returned)
		}
	}
}

// specs/2026-11-15-desk-interrupted-turn.md IT1.1, IT1.2 (the helper of the account; two questions run at the site and a
// third waits in the queue when the app is killed; the next start comes after the lease of the site — six minutes — ended
// the permit of the waiting question unused): the next start revokes the sessions of the local author, so every question the
// crash left ends model.session_ended at once — the one that ran and the one that waited —, Codex sends the site nothing
// more and the site opens no new permit, though the permit of the waiting question went by unused: each permit of the run
// before is asked by the ref of its row, spending nothing, and closed failed; every question is pending until the word of
// the site (the word itself is asked six minutes after the start — services/appwork TestAnInterruptedTurnAsksNoNewPermitAtAnyAge).
func TestAQuestionACrashLeftEndsAndOpensNoPermit(t *testing.T) {
	resources := desktoptest.Resources(t)
	site := newAccountAISite(t)
	data := filepath.Join(t.TempDir(), "data")
	binary := site.accountProgram(t)
	run := site.launchAccount(t, binary, resources, data)
	saga, first := run.chat(t, "Северный предел")
	second, third := run.anotherChat(t, saga.project, "chat-second"), run.anotherChat(t, saga.project, "chat-third")

	arrived, release := site.holdTurns(t)
	one := run.asked(t, first, "Кто зажёг огни?", "question-one")
	two := run.asked(t, second, "Где стоит застава?", "question-two")
	arrived(2)
	three := run.asked(t, third, questionMark, "question-three")
	awaitTrue(t, "the permit of the waiting question", time.Minute, func() bool { return site.opened("chat_turn") == 3 })
	if err := run.process.Command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = run.process.Wait(time.Minute)
	release()
	// Six minutes later: the lease of the site ended the permit of the waiting question unused, its unit came back.
	if expired := site.expireUnused(); expired != 1 {
		t.Fatalf("the lease ended %d permits unused", expired)
	}
	site.mu.Lock()
	refs := map[string]bool{}
	for _, permit := range site.permits {
		refs[permit.ref] = true
	}
	requests := len(site.turns)
	site.mu.Unlock()
	opensBefore, closesBefore := len(site.heardOf("/api/v1/app/permits")), len(site.closed())

	again := site.launchAccount(t, binary, resources, data)
	for _, turn := range []string{one, two, three} {
		again.window.jobEnded(turn, 2*time.Minute)
	}
	if site.opened("chat_turn") != 3 {
		t.Fatalf("the site opened %d permits of turns; want the three of the run before and none new", site.opened("chat_turn"))
	}
	opens := site.heardOf("/api/v1/app/permits")[opensBefore:]
	for _, open := range opens {
		if !refs[open.body["ref"]] {
			t.Fatal("a question the crash left asked a permit by a ref of no permit of the run before")
		}
	}
	closes := site.closed()[closesBefore:]
	if len(opens) != 3 || len(closes) != 3 {
		t.Fatalf("after the crash the site was asked %d permits by their refs and closed %v", len(opens), closes)
	}
	for _, close := range closes {
		if close != "chat_turn failed" {
			t.Fatalf("a permit of the run before closed %q", close)
		}
	}
	site.mu.Lock()
	heard := len(site.turns)
	site.mu.Unlock()
	if heard != requests {
		t.Fatalf("Codex sent the site %d requests after the crash", heard-requests)
	}
	questionsAfterACrash(t, again, "pending", one, two, three)
}

// IT1.1 («Свой ИИ»): a question the crash left mid-answer ends with the same code; nothing is counted — the question tells
// none —, the site hears nothing and the provider no request more.
func TestAQuestionOfTheOwnAIACrashLeftEnds(t *testing.T) {
	resources := desktoptest.Resources(t)
	site := newAccountAISite(t)
	provider := &providerOfTheTest{}
	stub := httptest.NewServer(provider)
	defer stub.Close()
	data := filepath.Join(t.TempDir(), "data")
	binary := site.accountProgram(t)
	run := site.launchAccount(t, binary, resources, data)
	_, chatID := run.chat(t, "Северный предел")
	own := map[string]any{"source": "own", "own": map[string]any{
		"preset": "custom", "baseUrl": stub.URL + "/v1", "protocol": "chat", "chatModel": "north-chat", "cardsModel": "north-cards",
	}}
	if response, body := run.window.do(http.MethodPut, "/api/v1/desktop/ai", run.window.csrf(), own); response.StatusCode != http.StatusOK {
		t.Fatalf("updateAiSettings own = %d %s", response.StatusCode, body)
	}
	turn := run.asked(t, chatID, longQuestion, "question-of-the-own")
	provider.holding(t, 3*time.Minute)
	if err := run.process.Command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = run.process.Wait(time.Minute)
	chats := func() int {
		count := 0
		for _, request := range provider.seen() {
			if request.model == "north-chat" {
				count++
			}
		}
		return count
	}
	asked := chats()

	again := site.launchAccount(t, binary, resources, data)
	questionsAfterACrash(t, again, "none", turn)
	if chats() != asked || site.opened("chat_turn") != 0 || len(site.closed()) != 0 {
		t.Fatalf("after the crash the provider was asked %d times more, the site opened %d permits and closed %v",
			chats()-asked, site.opened("chat_turn"), site.closed())
	}
}
