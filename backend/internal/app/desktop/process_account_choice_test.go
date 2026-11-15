// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"canonkeeper/backend/internal/app/desktop/desktoptest"
)

// theSiteWithItsHelper answers as the site answers a good code: the account and the helper of the site for the app
// (specs/2026-11-13-desk-account-ai.md AI5.1).
func theSiteWithItsHelper(w http.ResponseWriter, r *http.Request, request heard) {
	if request.method == http.MethodPost && request.path == "/api/v1/app/link/token" {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"pass":"`+passMark+`","account":{"name":"Северный","email":"a…@example.test",
			"tier":{"code":"writer","title":"Писатель","until":null},"reading":{"used":3200000,"limit":40000000},
			"helper":{"chatModel":"site-chat","cardsModel":"site-cards","contextWindow":400000,"compactLimit":300000}}}`)
		return
	}
	theSite(w, r, request)
}

// aiSettingsOf is getAiSettings: the source and the profile of «Свой ИИ».
type aiSettingsOf struct {
	Settings struct {
		Source string          `json:"source"`
		Own    json.RawMessage `json:"own"`
	} `json:"settings"`
}

// specs/2026-11-15-desk-account-link-web.md AW3.2: who answers after a link is the server's decision by the intent of its
// start. «Свой ИИ» chosen, a link without the intent leaves it; a link started to choose the helper of the account switches
// the source to the account. The profile of «Свой ИИ» — the provider, the models, «Дополнительно», the prices — is the same
// after both.
func TestALinkChoosesTheHelperOfTheAccountByTheIntentOfItsStart(t *testing.T) {
	resources := desktoptest.Resources(t)
	site := newSiteOfTheTest(t)
	site.set(theSiteWithItsHelper)
	run := site.launch(t, site.program(t), resources, filepath.Join(t.TempDir(), "data"), nil, desktoptest.KeepAll)
	own := map[string]any{"source": "own", "own": map[string]any{
		"preset": "deepseek", "chatModel": "deepseek-chat", "cardsModel": "deepseek-reasoner",
		"advanced": map[string]any{"contextWindow": 64000, "maxOutput": 16000, "reasoning": "high", "parallel": 2,
			"priceInPerMillion": 0.27, "priceOutPerMillion": 1.1, "currency": "RUB"},
	}}
	if response, body := run.window.Do(http.MethodPut, "/api/v1/desktop/ai", run.window.CSRF(), own); response.StatusCode != http.StatusOK {
		t.Fatalf("updateAiSettings own = %d %s", response.StatusCode, body)
	}
	before := run.settings(t)

	for _, step := range []struct {
		intent bool
		source string
	}{{false, "own"}, {true, "account"}} {
		response, body := run.window.Do(http.MethodPost, "/api/v1/desktop/account/link", run.window.CSRF(), map[string]bool{"chooseHelper": step.intent})
		var waiting accountState
		if response.StatusCode != http.StatusOK || json.Unmarshal(body, &waiting) != nil || waiting.Link == nil {
			t.Fatalf("startAccountLink %v = %d %s", step.intent, response.StatusCode, body)
		}
		address, err := url.Parse(waiting.Link.Address)
		if err != nil || address.Query().Has("chooseHelper") || len(address.Query()) != 4 {
			t.Fatalf("the address of /link carries %v", address.Query())
		}
		if status, page, _ := run.back(t, http.MethodGet, returnQuery(codeMark, address.Query().Get("state")), nil); status != http.StatusOK {
			t.Fatalf("the return = %d %s", status, page)
		}
		awaitTrue(t, "the link made", time.Minute, func() bool {
			state, _ := run.account(t, "/api/v1/desktop/account")
			return state.State == "linked"
		})
		after := run.settings(t)
		if after.Settings.Source != step.source || string(after.Settings.Own) != string(before.Settings.Own) {
			t.Fatalf("intent %v: the source %q, the profile %s; before %s", step.intent, after.Settings.Source, after.Settings.Own, before.Settings.Own)
		}
		if response, body := run.window.Do(http.MethodDelete, "/api/v1/desktop/account", run.window.CSRF(), nil); response.StatusCode != http.StatusOK {
			t.Fatalf("unlinkAccount = %d %s", response.StatusCode, body)
		}
	}
}

// settings is getAiSettings of the window.
func (run *linked) settings(t *testing.T) aiSettingsOf {
	t.Helper()
	response, body := run.window.Do(http.MethodGet, "/api/v1/desktop/ai", "", nil)
	var settings aiSettingsOf
	if response.StatusCode != http.StatusOK || json.Unmarshal(body, &settings) != nil {
		t.Fatalf("getAiSettings = %d %s", response.StatusCode, body)
	}
	return settings
}
