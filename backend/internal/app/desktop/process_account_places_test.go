// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"fmt"
	"net/http"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"canonkeeper/backend/internal/app/desktop/desktoptest"
)

// activePlaces is who holds the places of «разом» as getAccountQuota of the app tells it.
type activePlaces struct {
	Answering int64 `json:"answering"`
	Held      int64 `json:"held"`
	Elsewhere int64 `json:"elsewhere"`
}

// atOnce is «разом» of getAccountQuota of the app.
type atOnce struct {
	Used   int64         `json:"used"`
	Limit  int64         `json:"limit"`
	Places *activePlaces `json:"places"`
}

// specs/2026-11-15-desk-turns-here.md TH2.1–TH2.3: the app linked with a stub of the site and the helper of the account chosen.
// While a turn of a saga runs at the site, «разом» of getAccountQuota says the place is this app's answer; once it ended and the
// site still counts its permit, the place frees itself; a turn the site counts and this app does not have is the site's or
// another app's. A site that does not name the places of this app — an older one — leaves the object out, and one with fields
// the app does not know is read; «Свой ИИ» tells no places. A question the site refused «разом» makes the next read of the
// quota ask the site again, though what it told is younger than a minute.
func TestWhoHoldsThePlacesAtOnceIsToldBySiteAndApp(t *testing.T) {
	resources := desktoptest.Resources(t)
	site := newAccountAISite(t)
	var mu sync.Mutex
	turns := `{"used":0,"limit":2,"thisApp":0}`
	told := func(activeTurns string) {
		mu.Lock()
		defer mu.Unlock()
		turns = activeTurns
	}
	site.set(func(w http.ResponseWriter, r *http.Request, request heard) {
		if request.method != http.MethodGet || request.path != accountPath {
			site.serve(w, r, request)
			return
		}
		mu.Lock()
		activeTurns := turns
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"name":"Северный","email":"a…@example.test","tier":{"code":"writer","title":"Писатель","until":"2026-12-13T00:00:00Z"},
			"helper":{"chatModel":"site-chat","cardsModel":"site-cards","contextWindow":400000,"compactLimit":300000},
			"quota":{"day":"2026-11-13","resetsAt":"2026-11-13T21:00:00Z","chatTurns":{"used":1,"limit":20},"activeTurns":%s,
			"imports":{"used":0,"limit":5},"reading":{"used":3200000,"limit":40000000},"readingWork":{"used":100,"limit":320000000}}}`, activeTurns)
	})
	data := filepath.Join(t.TempDir(), "data")
	run := site.launchAccount(t, site.accountProgram(t), resources, data)
	_, chatID := run.chat(t, "Северный предел")
	read := func(fresh bool) atOnce {
		t.Helper()
		if fresh {
			run.window.read("/api/v1/desktop/account?fresh=true", nil)
		}
		var counts struct {
			ActiveTurns atOnce `json:"activeTurns"`
		}
		run.window.read("/api/v1/account/quota", &counts)
		return counts.ActiveTurns
	}
	expect := func(what string, want activePlaces) {
		t.Helper()
		if got := read(true); got.Places == nil || *got.Places != want {
			t.Fatalf("%s: «разом» = %+v (places %+v), want places %+v", what, got, got.Places, want)
		}
	}

	told(`{"used":1,"limit":2,"thisApp":1}`)
	arrived, release := site.holdTurns(t)
	turn := run.asked(t, chatID, questionMark, "question-of-the-places")
	arrived(1)
	expect("a turn of this app runs", activePlaces{Answering: 1})
	release()
	if status := run.window.jobEnded(turn, 3*time.Minute); status != "succeeded" {
		t.Fatalf("the turn = %s", status)
	}
	expect("the turn ended here, the site still counts its permit", activePlaces{Held: 1})
	told(`{"used":1,"limit":2,"thisApp":0}`)
	expect("a turn the site counts and this app does not have", activePlaces{Elsewhere: 1})
	told(`{"used":2,"limit":2,"thisApp":1,"sites":{"north":1}}`)
	expect("an answer with a field the app does not know", activePlaces{Held: 1, Elsewhere: 1})
	told(`{"used":1,"limit":2}`)
	if got := read(true); got.Places != nil || got.Used != 1 || got.Limit != 2 {
		t.Fatalf("a site that does not name the places of this app: «разом» = %+v (places %+v)", got, got.Places)
	}

	// «Разом» refused by the site: the next read asks the site, though what it told is fresh — the numbers are not older than
	// the refusal.
	told(`{"used":1,"limit":2,"thisApp":0}`)
	read(true)
	told(`{"used":2,"limit":2,"thisApp":0}`)
	site.mu.Lock()
	site.refuseNew = &siteRefusal{status: http.StatusTooManyRequests, code: "quota.active_turns"}
	site.mu.Unlock()
	before := len(site.heardOf(accountPath))
	if _, status, body := run.window.ask(chatID, "Кто зажёг огни?", "question-refused-at-once"); status != http.StatusTooManyRequests || !containsCode(body, "quota.active_turns") {
		t.Fatalf("a question «разом» = %d %s", status, body)
	}
	if got := read(false); got.Places == nil || *got.Places != (activePlaces{Elsewhere: 2}) {
		t.Fatalf("«разом» after the refusal = %+v (places %+v)", got, got.Places)
	}
	if asked := len(site.heardOf(accountPath)) - before; asked < 1 {
		t.Fatal("the read after a refusal «разом» did not ask the site")
	}

	// «Свой ИИ»: the counters of the computer, no places.
	own := map[string]any{"source": "own", "own": map[string]any{
		"preset": "custom", "baseUrl": "http://127.0.0.1:9/v1", "protocol": "chat", "chatModel": "north-chat", "cardsModel": "north-cards",
	}}
	if response, body := run.window.do(http.MethodPut, "/api/v1/desktop/ai", run.window.csrf(), own); response.StatusCode != http.StatusOK {
		t.Fatalf("updateAiSettings own = %d %s", response.StatusCode, body)
	}
	if got := read(true); got.Places != nil {
		t.Fatalf("«Свой ИИ» tells the places of «разом»: %+v", got.Places)
	}
}
