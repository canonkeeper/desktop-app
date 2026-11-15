// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"reflect"
	"testing"
	"time"

	"canonkeeper/backend/internal/domain/aisettings"
)

// specs/2026-11-13-desk-probes.md PB2.5: the last check lives in the section ai of settings.json beside the settings — a
// write of the settings keeps it, a write of the check keeps the settings — and the next start reads it back; a check that
// is broken is none and the settings stay.
func TestTheLastCheckIsKeptInTheSectionOfTheAI(t *testing.T) {
	data := DataDir{Root: t.TempDir()}
	writeFile(t, data.Settings(), `{"port": 21000, "shell": {"window": [800, 600]}}`)
	saved, err := loadSettings(data.Settings(), settingsNow)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := saved.Report(); ok {
		t.Fatal("a check before any")
	}
	gateway, chat, cards := "https://gateway.example.test/v1", "north-chat", "north-cards"
	settings, err := aisettings.Draft{Source: "own", Own: &aisettings.OwnDraft{Preset: "custom", BaseURL: &gateway, ChatModel: &chat, CardsModel: &cards}}.Settings()
	if err != nil {
		t.Fatal(err)
	}
	if err := saved.KeepAI(settings); err != nil {
		t.Fatal(err)
	}
	calibration := 312.4
	report := aisettings.Report{
		At: time.Date(2026, 11, 13, 9, 30, 0, 0, time.UTC), Checked: aisettings.CheckedOf(*settings.Own), Calibration: &calibration,
		Results: []aisettings.Result{
			{Probe: aisettings.ProbeShort, Role: aisettings.RoleChat, Model: chat, Passed: true},
			{Probe: aisettings.ProbeTool, Role: aisettings.RoleChat, Model: chat, Reason: aisettings.ReasonNoTools},
		},
	}
	if err := saved.KeepReport(report); err != nil {
		t.Fatal(err)
	}
	if err := saved.KeepAI(settings); err != nil {
		t.Fatal(err)
	}
	values := readSettingsFile(t, data.Settings())
	if compact(t, values["port"]) != "21000" || compact(t, values["shell"]) != `{"window":[800,600]}` {
		t.Fatalf("settings.json after the check = %v", values)
	}
	again, err := loadSettings(data.Settings(), settingsNow)
	if err != nil || again.AI().Own == nil || again.AI().Own.CardsModel != cards {
		t.Fatalf("the settings after the check = %+v, %v", again.AI(), err)
	}
	kept, ok := again.Report()
	if !ok || !reflect.DeepEqual(kept, report) {
		t.Fatalf("the next start reads %+v", kept)
	}
	if err := again.KeepAI(aisettings.None()); err != nil {
		t.Fatal(err)
	}
	if _, ok := again.Report(); !ok {
		t.Fatal("a write of the settings dropped the check")
	}

	for name, check := range map[string]string{
		"not an object":   `"done"`,
		"a probe unknown": `{"at":"2026-11-13T09:30:00Z","chatModel":"a","cardsModel":"a","probes":[{"probe":"guess","role":"chat","model":"a","passed":true}]}`,
		"a reason unknown": `{"at":"2026-11-13T09:30:00Z","chatModel":"a","cardsModel":"a",` +
			`"probes":[{"probe":"short","role":"chat","model":"a","passed":false,"reason":"words of the provider"}]}`,
		"no time": `{"chatModel":"a","cardsModel":"a","probes":[]}`,
	} {
		t.Run(name, func(t *testing.T) {
			data := DataDir{Root: t.TempDir()}
			writeFile(t, data.Settings(), `{"port": 21000, "ai": {"source": "own", "own": {"preset": "ollama", "chatModel": "a", "cardsModel": "a"}, "check": `+check+`}}`)
			saved, err := loadSettings(data.Settings(), settingsNow)
			if err != nil || saved.AI().Own == nil || saved.AI().Own.ChatModel != "a" {
				t.Fatalf("the settings beside a broken check = %+v, %v", saved.AI(), err)
			}
			if _, ok := saved.Report(); ok {
				t.Fatal("a broken check was read")
			}
		})
	}
}
