// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// The limits of the window are the contract's (specs/2026-11-14-desk-journal.md, the third check C2): every operation of the
// contract of the app that names «N a minute» has that rule in the table of the build, the operations of the journal — ten.
func TestTheLimitsOfTheWindowAreTheContracts(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "contracts", "openapi.desktop.json"))
	if err != nil {
		t.Fatal(err)
	}
	var contract struct {
		Paths map[string]map[string]struct {
			OperationID string `json:"operationId"`
			Description string `json:"description"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(raw, &contract); err != nil {
		t.Fatal(err)
	}
	numbers := map[string]int{"five": 5, "ten": 10, "twenty": 20, "thirty": 30, "sixty": 60}
	limit := regexp.MustCompile(`(?i)\b(five|ten|twenty|thirty|sixty) a minute\b`)
	named := 0
	for _, operations := range contract.Paths {
		for _, operation := range operations {
			match := limit.FindStringSubmatch(operation.Description)
			if match == nil {
				continue
			}
			named++
			rule, ok := windowRateLimits[operation.OperationID]
			if want := numbers[strings.ToLower(match[1])]; !ok || rule.Limit != want || rule.Window != time.Minute {
				t.Errorf("%s: the contract says %d a minute, the build %+v", operation.OperationID, want, rule)
			}
		}
	}
	for _, operation := range []string{"clearSendLog", "getSendLogFile"} {
		if rule := windowRateLimits[operation]; rule.Limit != 10 || rule.Window != time.Minute {
			t.Errorf("%s = %+v", operation, rule)
		}
	}
	if named < 10 {
		t.Fatalf("the contract names %d limits", named)
	}
}
