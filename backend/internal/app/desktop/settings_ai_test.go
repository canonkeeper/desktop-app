// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"os"
	"runtime"
	"testing"

	"canonkeeper/backend/internal/domain/aisettings"
)

// specs/2026-11-13-desk-secrets.md SE2.3: the settings of «Свой ИИ» live in settings.json under ai; the owner of the file
// writes them whole beside the port and every key it does not know, and reads them back at the next start.
func TestTheSettingsOfTheAIAreKeptBesideThePort(t *testing.T) {
	data := DataDir{Root: t.TempDir()}
	writeFile(t, data.Settings(), `{"port": 21000, "shell": {"window": [800, 600]}}`)
	saved, err := loadSettings(data.Settings(), settingsNow)
	if err != nil || saved.AI().Source != aisettings.SourceNone || saved.AI().Own != nil {
		t.Fatalf("settings without ai = %+v, %v", saved.AI(), err)
	}
	gateway, chat, priceIn := "https://gateway.example/v1", "north-chat", "0.27"
	settings, err := aisettings.Draft{Source: "own", Own: &aisettings.OwnDraft{
		Preset: "custom", BaseURL: &gateway, ChatModel: &chat, Advanced: aisettings.AdvancedDraft{PriceIn: &priceIn},
	}}.Settings()
	if err != nil {
		t.Fatal(err)
	}
	if err := saved.KeepAI(settings); err != nil {
		t.Fatal(err)
	}
	values := readSettingsFile(t, data.Settings())
	if compact(t, values["port"]) != "21000" || compact(t, values["shell"]) != `{"window":[800,600]}` || len(values) != 3 {
		t.Fatalf("settings.json after the AI = %v", values)
	}
	// The currency is kept with the prices (specs/2026-11-13-desk-consent.md CN1.1); a file without it reads as dollars.
	want := `{"own":{"advanced":{"contextWindow":128000,"currency":"USD","maxOutput":16000,"parallel":4,"priceInPerMillion":0.27,` +
		`"priceOutPerMillion":null,"reasoning":"medium"},"baseUrl":"https://gateway.example/v1","cardsModel":"",` +
		`"chatModel":"north-chat","preset":"custom","protocol":"auto"},"source":"own"}`
	if got := compact(t, values["ai"]); got != want {
		t.Fatalf("ai = %s\nwant %s", got, want)
	}
	if info, err := os.Stat(data.Settings()); err != nil || runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("settings.json = %v, %v", info, err)
	}
	if entries, _ := os.ReadDir(data.Root); len(entries) != 1 {
		t.Fatalf("the data directory holds %v", entries)
	}
	if changed, err := saved.keep(23000); err != nil || !changed {
		t.Fatalf("a new port after the AI: changed %v, %v", changed, err)
	}
	again, err := loadSettings(data.Settings(), settingsNow)
	if err != nil || again.port != 23000 || again.AI().Own == nil || *again.AI().Own.Advanced.PriceIn != *settings.Own.Advanced.PriceIn ||
		again.AI().Own.BaseURL != gateway || again.AI().Own.ChatModel != chat || again.AI().Source != aisettings.SourceOwn {
		t.Fatalf("the next start reads %+v, port %d, %v", again.AI(), again.port, err)
	}
}

// SE2.3: a section ai that is broken or not valid does not stop the start: the source is none, the port is read, and the
// next write replaces the section.
func TestABrokenSectionOfTheAIMeansNone(t *testing.T) {
	for name, section := range map[string]string{
		"not an object":     `"own"`,
		"unknown source":    `{"source": "account"}`,
		"own without own":   `{"source": "own"}`,
		"a field not valid": `{"source": "own", "own": {"preset": "custom", "baseUrl": "ftp://gateway.example"}}`,
		"a wrong type":      `{"source": "own", "own": {"preset": "openai", "advanced": {"parallel": "4"}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			data := DataDir{Root: t.TempDir()}
			writeFile(t, data.Settings(), `{"port": 21000, "ai": `+section+`}`)
			saved, err := loadSettings(data.Settings(), settingsNow)
			if err != nil || saved.port != 21000 || saved.lost || saved.AI().Source != aisettings.SourceNone || saved.AI().Own != nil {
				t.Fatalf("a broken ai = %+v, port %d, lost %v, %v", saved.AI(), saved.port, saved.lost, err)
			}
			if err := saved.KeepAI(aisettings.None()); err != nil {
				t.Fatal(err)
			}
			if values := readSettingsFile(t, data.Settings()); compact(t, values["ai"]) != `{"own":null,"source":"none"}` || compact(t, values["port"]) != "21000" {
				t.Fatalf("settings.json after the write = %v", values)
			}
		})
	}
}
