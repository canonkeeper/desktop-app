// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"encoding/json"
	"time"

	"canonkeeper/backend/internal/domain/aisettings"
)

// aiSection is the section ai of settings.json: the settings of «Свой ИИ» and the last check of them
// (specs/2026-11-13-desk-probes.md PB2.5), written whole together.
type aiSection struct {
	aiFile
	Check *checkFile `json:"check,omitempty"`
}

// checkFile is the last check in the words of the contract: when, what it checked, the probes and the calibration — no
// text of a probe or of an answer.
type checkFile struct {
	At          *time.Time  `json:"at"`
	BaseURL     string      `json:"baseUrl"`
	Protocol    string      `json:"protocol"`
	ChatModel   string      `json:"chatModel"`
	CardsModel  string      `json:"cardsModel"`
	Probes      []probeFile `json:"probes"`
	Calibration *float64    `json:"calibration"`
	KeyChanged  bool        `json:"keyChanged"`
}

type probeFile struct {
	Probe  string `json:"probe"`
	Role   string `json:"role"`
	Model  string `json:"model"`
	Passed bool   `json:"passed"`
	Reason string `json:"reason,omitempty"`
}

// sectionOf is the section of the settings and the check.
func sectionOf(ai aisettings.Settings, check *aisettings.Report) aiSection {
	section := aiSection{aiFile: aiFileOf(ai)}
	if check != nil {
		at := check.At.UTC()
		file := &checkFile{
			At: &at, BaseURL: check.Checked.BaseURL, Protocol: string(check.Checked.Protocol), ChatModel: check.Checked.ChatModel,
			CardsModel: check.Checked.CardsModel, Calibration: check.Calibration, KeyChanged: check.KeyChanged, Probes: []probeFile{},
		}
		for _, result := range check.Results {
			file.Probes = append(file.Probes, probeFile{
				Probe: string(result.Probe), Role: string(result.Role), Model: result.Model, Passed: result.Passed, Reason: string(result.Reason),
			})
		}
		section.Check = file
	}
	return section
}

// checkOfFile reads the check of the section; one that is broken or names what the domain does not know is none, and the
// settings beside it stay.
func checkOfFile(section json.RawMessage) *aisettings.Report {
	var file struct {
		Check *checkFile `json:"check"`
	}
	if json.Unmarshal(section, &file) != nil || file.Check == nil || file.Check.At == nil {
		return nil
	}
	check := file.Check
	report := &aisettings.Report{
		At: check.At.UTC(), Calibration: check.Calibration, KeyChanged: check.KeyChanged,
		Checked: aisettings.Checked{
			BaseURL: check.BaseURL, Protocol: aisettings.Protocol(check.Protocol), ChatModel: check.ChatModel, CardsModel: check.CardsModel,
		},
	}
	for _, probe := range check.Probes {
		result := aisettings.Result{
			Probe: aisettings.Probe(probe.Probe), Role: aisettings.Role(probe.Role), Model: probe.Model, Passed: probe.Passed,
			Reason: aisettings.Reason(probe.Reason),
		}
		if !result.Probe.Known() || !result.Role.Known() || result.Passed != (result.Reason == "") || result.Reason != "" && !result.Reason.Known() {
			return nil
		}
		report.Results = append(report.Results, result)
	}
	return report
}

// Report is the last check settings.json keeps.
func (current *settings) Report() (aisettings.Report, bool) {
	current.mu.Lock()
	defer current.mu.Unlock()
	if current.check == nil {
		return aisettings.Report{}, false
	}
	return *current.check, true
}

// KeepReport replaces the last check; the settings beside it stay. A write that fails leaves the file and the check as
// they were.
func (current *settings) KeepReport(report aisettings.Report) error {
	current.mu.Lock()
	defer current.mu.Unlock()
	encoded, err := json.Marshal(sectionOf(current.ai, &report))
	if err != nil {
		return err
	}
	if err := current.write(settingsAI, encoded); err != nil {
		return err
	}
	current.check = &report
	return nil
}
