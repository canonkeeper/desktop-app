// SPDX-License-Identifier: AGPL-3.0-only

package coresteps

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// specs/2026-11-14-desk-journal.md JR1.4: every step of the core has the test of its builder that holds what it names; a new
// step without one fails here before its call is refused at the gateway.
func TestEveryStepOfTheCoreHasItsNaming(t *testing.T) {
	raw, err := os.ReadFile("testdata/named-steps.json")
	if err != nil {
		t.Fatal(err)
	}
	var table struct {
		Steps map[string]string `json:"steps"`
	}
	if err := json.Unmarshal(raw, &table); err != nil {
		t.Fatal(err)
	}
	if len(table.Steps) != len(All()) {
		t.Fatalf("the table names %d steps, the core has %d", len(table.Steps), len(All()))
	}
	for _, step := range All() {
		where := strings.Fields(table.Steps[string(step.Name)])
		if len(where) != 2 {
			t.Errorf("the step %s has no test of its naming", step.Name)
			continue
		}
		source, err := os.ReadFile(filepath.Join("..", "..", filepath.FromSlash(where[0])))
		if err != nil || !strings.Contains(string(source), "func "+where[1]+"(t *testing.T)") {
			t.Errorf("the step %s names %s, which has no %s", step.Name, where[0], where[1])
		}
	}
}
