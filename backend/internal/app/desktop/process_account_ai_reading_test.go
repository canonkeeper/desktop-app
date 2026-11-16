// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"canonkeeper/backend/internal/domain/coresteps"
)

// checksOfThePauses waits for count more checks of the pauses of the reading of the account — each is a fresh read of the
// account at the site, the event the test waits for instead of a pause by the clock.
func (site *accountAISite) checksOfThePauses(t *testing.T, count int) {
	t.Helper()
	before := len(site.heardOf(accountPath))
	awaitTrue(t, "the checks of the pauses", 2*time.Minute, func() bool { return len(site.heardOf(accountPath)) >= before+count })
}

// cardJobs counts the cards Jobs of a saga.
func cardJobs(t *testing.T, data, projectID string) int {
	t.Helper()
	connection := database(t, data)
	defer func() { _ = connection.Close(context.Background()) }()
	var count int
	if err := connection.QueryRow(context.Background(),
		`SELECT count(*) FROM jobs WHERE project_id = $1 AND kind = 'chapter_cards'`, projectID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

// specs/2026-11-13-desk-account-ai.md AI3.5, AI3.6 (an app the site does not take — the pause «приложение не подходит серверу»,
// the catch-up at the start sends no step; no connection goes on only once the site answers its check): a step of the card the
// site refuses app.step_instruction pauses the consent at unfit; a restart of the same version and the checks of the pauses
// after it send no step, though the site would take one now — only the start of a new version tries again, and the reading
// goes on. With the site out of reach a new book pauses the reading at «связь»; the checks while the site does not answer
// queue no Job; once it answers, the reading goes on by itself.
func TestTheReadingOfTheAccountGoesOnByItsRule(t *testing.T) {
	resources := processResources(t)
	site := newAccountAISite(t)
	data := filepath.Join(t.TempDir(), "data")
	binary := site.accountProgram(t)
	run := site.launchAccount(t, binary, resources, data)
	saga, _ := run.chat(t, "Северный предел")
	awaitTrue(t, "the wait for the consent", 2*time.Minute, func() bool {
		status := saga.status()
		return status.Preparation != nil && status.Preparation.Waiting == "consent"
	})
	site.mu.Lock()
	site.stepRefusal[string(coresteps.Card)] = &siteRefusal{status: http.StatusBadRequest, code: "app.step_instruction"}
	site.mu.Unlock()
	saga.setConsent(map[string]any{"state": "allowed"})
	if paused := saga.pausedAt(3 * time.Minute); paused.Pause == nil || *paused.Pause != "unfit" {
		t.Fatalf("the pause = %+v%s", paused, saga.journey())
	}
	cards := site.stepsOf(coresteps.Card)

	// A restart of the same version: the site would take the step now, but the pause of an app it did not take waits for a
	// new version.
	run.process.End()
	site.mu.Lock()
	delete(site.stepRefusal, string(coresteps.Card))
	site.mu.Unlock()
	again := site.launchAccount(t, binary, resources, data)
	saga.client = again.window
	// A pause that waits for a new version gives the checks of the pauses nothing to ask the site (AI3.6): no event comes, so
	// the test lets five checks of the program (resumeEvery 2s) pass.
	time.Sleep(5 * 2 * time.Second)
	if status := saga.status(); status.Reading == nil || status.Reading.Pause == nil || *status.Reading.Pause != "unfit" || site.stepsOf(coresteps.Card) != cards {
		t.Fatalf("after a restart of the same version: %+v, cards %d → %d%s", status.Reading, cards, site.stepsOf(coresteps.Card), saga.journey())
	}

	// The start of a new version tries again: the reading goes on.
	again.process.End()
	next := program(t, programVariant{
		name: "account-ai-next-" + strings.ReplaceAll(strings.TrimPrefix(site.url, "https://"), ":", "-"), version: "1.3.1", site: site.url,
		retryPause: "10ms", resumeEvery: "2s",
	})
	updated := site.launchAccountOf(t, next, "1.3.1", resources, data)
	saga.client = updated.window
	saga.readThrough(3 * time.Minute)
	if site.stepsOf(coresteps.Card) <= cards {
		t.Fatalf("the new version read no card%s", saga.journey())
	}

	// No connection: the reading pauses at «связь», and the checks while the site does not answer queue no Job.
	site.mu.Lock()
	site.down = true
	site.mu.Unlock()
	updated.window.importFileAs(saga.project, "Второй том.md", bookOf("Глава 3. Ледяной перевал", "Глава 4. Застава"), "import-of-the-second-book")
	if paused := saga.pausedAt(3 * time.Minute); paused.Pause == nil || *paused.Pause != "connection" {
		t.Fatalf("the pause without the site = %+v%s", paused, saga.journey())
	}
	jobs := cardJobs(t, data, saga.project)
	site.checksOfThePauses(t, 3)
	if after := cardJobs(t, data, saga.project); after != jobs {
		t.Fatalf("the checks without the site queued %d cards Jobs%s", after-jobs, saga.journey())
	}
	site.mu.Lock()
	site.down = false
	site.mu.Unlock()
	saga.readThrough(3 * time.Minute)
}

// specs/2026-11-13-desk-account-ai.md AI1.2, AI3.4 with GW4.1 («подключение ставит догон карточек сразу»): the reading of the
// account goes while the link breaks — the site refuses the pass at a step of the card, the Job of the cards ends, the consent
// stays given, the helper is not connected —; the author links the app again, the helper of the site the same: the reading is
// queued again at once, without a restart of the app, and reads every chapter.
func TestTheReadingOfTheAccountGoesOnOnceTheAppIsLinkedAgain(t *testing.T) {
	resources := processResources(t)
	site := newAccountAISite(t)
	data := filepath.Join(t.TempDir(), "data")
	run := site.launchAccount(t, site.accountProgram(t), resources, data)
	saga, _ := run.chat(t, "Северный предел")
	awaitTrue(t, "the wait for the consent", 2*time.Minute, func() bool {
		status := saga.status()
		return status.Preparation != nil && status.Preparation.Waiting == "consent"
	})
	site.mu.Lock()
	site.stepRefusal[string(coresteps.Card)] = &siteRefusal{status: http.StatusUnauthorized, code: "app.pass_invalid"}
	site.mu.Unlock()
	saga.setConsent(map[string]any{"state": "allowed"})
	linkState := func() string {
		var state struct {
			State string `json:"state"`
		}
		run.window.read("/api/v1/desktop/account", &state)
		return state.State
	}
	awaitTrue(t, "the broken link", 2*time.Minute, func() bool { return linkState() == "broken" })
	connection := database(t, data)
	awaitTrue(t, "the end of the Job of the cards", 2*time.Minute, func() bool {
		var live int
		_ = connection.QueryRow(context.Background(), `SELECT count(*) FROM jobs WHERE project_id = $1 AND kind = 'chapter_cards'
			AND status IN ('queued', 'running', 'canceling')`, saga.project).Scan(&live)
		return live == 0
	})
	if status := saga.status(); status.Preparation == nil || status.Preparation.Waiting != "helper" || saga.consent().State != "allowed" {
		t.Fatalf("after the broken link: %+v, the consent %+v%s", status.Preparation, saga.consent(), saga.journey())
	}
	cards := site.stepsOf(coresteps.Card)

	// «Связать снова»: a new link with the same helper of the site.
	site.mu.Lock()
	delete(site.stepRefusal, string(coresteps.Card))
	site.mu.Unlock()
	response, body := run.window.do(http.MethodPost, "/api/v1/desktop/account/link", run.window.csrf(), nil)
	var waiting struct {
		Link *struct {
			Address string `json:"address"`
		} `json:"link"`
	}
	if response.StatusCode != http.StatusOK || json.Unmarshal(body, &waiting) != nil || waiting.Link == nil {
		t.Fatalf("startAccountLink = %d %s", response.StatusCode, body)
	}
	address, err := url.Parse(waiting.Link.Address)
	if err != nil {
		t.Fatal(err)
	}
	back, err := http.Get(run.window.origin + "/desktop/link/callback?" + returnQuery(codeMark, address.Query().Get("state")))
	if err != nil || back.StatusCode != http.StatusOK {
		t.Fatalf("the return of the link = %v %v", back, err)
	}
	_ = back.Body.Close()
	awaitTrue(t, "the link made again", time.Minute, func() bool { return linkState() == "linked" })
	saga.readThrough(2 * time.Minute)
	if site.stepsOf(coresteps.Card) <= cards {
		t.Fatalf("no card was read after the link was made again%s", saga.journey())
	}
}

// specs/2026-11-13-desk-account-ai.md AI3.5, AI3.6, AI3.7 (the refusals of the site to the reading): ai.provider_failed — the
// site already repeated it — pauses the reading «чтение на сервере недоступно» at the first refusal, one request and not one
// for each chapter; the pause goes on by itself at the check once the site answers, and ai.reading_fence then pauses it at
// the fence of the work of the reading with both limits of the tier in the status.
func TestTheRefusalsOfTheSitePauseTheReadingOfTheAccount(t *testing.T) {
	resources := processResources(t)
	site := newAccountAISite(t)
	data := filepath.Join(t.TempDir(), "data")
	run := site.launchAccount(t, site.accountProgram(t), resources, data)
	saga, _ := run.chat(t, "Северный предел")
	awaitTrue(t, "the wait for the consent", 2*time.Minute, func() bool {
		status := saga.status()
		return status.Preparation != nil && status.Preparation.Waiting == "consent"
	})
	site.mu.Lock()
	site.stepRefusal[string(coresteps.Card)] = &siteRefusal{status: http.StatusBadGateway, code: "ai.provider_failed"}
	site.mu.Unlock()
	saga.setConsent(map[string]any{"state": "allowed"})
	if paused := saga.pausedAt(3 * time.Minute); paused.Pause == nil || *paused.Pause != "unavailable" || site.stepsOf(coresteps.Card) != 1 {
		t.Fatalf("the pause of a failed provider = %+v after %d cards%s", paused, site.stepsOf(coresteps.Card), saga.journey())
	}

	site.mu.Lock()
	site.stepRefusal[string(coresteps.Card)] = &siteRefusal{status: http.StatusForbidden, code: "ai.reading_fence"}
	site.mu.Unlock()
	awaitTrue(t, "the pause at the fence of the reading", 3*time.Minute, func() bool {
		consent := saga.consent()
		return consent.Pause != nil && *consent.Pause == "reading_fence"
	})
	if cards := site.stepsOf(coresteps.Card); cards != 2 {
		t.Fatalf("the fence of the reading after %d cards%s", cards, saga.journey())
	}
	var status struct {
		Reading *struct {
			Pause  *string `json:"pause"`
			Limits *struct {
				Characters struct{ Used, Limit int64 }
				Work       struct{ Used, Limit int64 }
			} `json:"limits"`
		} `json:"reading"`
	}
	saga.read("/api/v1/projects/"+saga.project+"/indexing-status", &status)
	if status.Reading == nil || status.Reading.Pause == nil || *status.Reading.Pause != "reading_fence" || status.Reading.Limits == nil ||
		status.Reading.Limits.Characters.Limit != 40000000 || status.Reading.Limits.Work.Limit != 320000000 {
		t.Fatalf("the status of the fence = %+v", status.Reading)
	}
}
