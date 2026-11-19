// SPDX-License-Identifier: AGPL-3.0-only

package aisettings

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// localHostsTable is testdata/local-hosts.json, the one table of «этот компьютер» that the page reads too
// (src/editions/desktop/settings/helper/this-computer.test.ts).
type localHostsTable struct {
	Hosts []struct {
		Host  string `json:"host"`
		Local bool   `json:"local"`
		Why   string `json:"why"`
	} `json:"hosts"`
}

// specs/2026-11-19-desk-trust.md TW4.1: a host is this computer by the one table, and an address of «Свой ИИ» is local by its
// host; the variant Ollama is local whatever its address.
func TestTheHostOfThisComputerIsTheTables(t *testing.T) {
	raw, err := os.ReadFile("testdata/local-hosts.json")
	if err != nil {
		t.Fatal(err)
	}
	var table localHostsTable
	if err := json.Unmarshal(raw, &table); err != nil || len(table.Hosts) == 0 {
		t.Fatalf("testdata/local-hosts.json: %v", err)
	}
	for _, row := range table.Hosts {
		if got := LocalHost(row.Host); got != row.Local {
			t.Errorf("LocalHost(%q) = %v, want %v (%s)", row.Host, got, row.Local, row.Why)
		}
		if row.Host == "" {
			continue
		}
		host := row.Host
		if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
			host = "[" + host + "]"
		}
		if got := (Own{Preset: PresetCustom, BaseURL: "http://" + host + ":11434/v1"}).Local(); got != row.Local {
			t.Errorf("an address of %q is local %v, want %v (%s)", row.Host, got, row.Local, row.Why)
		}
	}
	if !(Own{Preset: PresetOllama, BaseURL: "http://192.168.1.10:11434/v1"}).Local() {
		t.Fatal("the variant Ollama is not local")
	}
}
