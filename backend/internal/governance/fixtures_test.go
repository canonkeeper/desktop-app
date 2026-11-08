package governance

import (
	"strings"
	"testing"
)

func TestPromptInjectionCorpusSchemaAndCoverage(t *testing.T) {
	cases, err := PromptInjectionCorpus()
	if err != nil {
		t.Fatalf("PromptInjectionCorpus() error = %v", err)
	}
	wantClasses := []PromptInjectionClass{
		PromptClassManuscriptLike,
		PromptClassToolOutputLike,
		PromptClassGraphStyleObservationLike,
		PromptClassCheckpointLike,
	}
	if len(cases) != len(wantClasses) {
		t.Fatalf("case count = %d", len(cases))
	}
	for index, testCase := range cases {
		if testCase.Class != wantClasses[index] || !testCase.Synthetic || !strings.Contains(testCase.Input, "[SYNTHETIC]") {
			t.Fatalf("case %d = %#v", index, testCase)
		}
		if testCase.ExpectedDisposition.Validate() != nil || len(testCase.ForbiddenActions) == 0 {
			t.Fatalf("case %q is open or incomplete", testCase.ID)
		}
	}

	cases[0].ForbiddenActions[0] = ForbiddenAction("mutated")
	cases[0].Input = "mutated"
	fresh, err := PromptInjectionCorpus()
	if err != nil || fresh[0].Input == "mutated" || fresh[0].ForbiddenActions[0] == ForbiddenAction("mutated") {
		t.Fatal("PromptInjectionCorpus returned shared mutable state")
	}
}

func TestPromptInjectionCorpusRejectsUnknownDuplicateAndRealDataShapes(t *testing.T) {
	valid := `{"schema":"CKPromptInjectionCorpusV1","cases":[` +
		`{"id":"synthetic-1","class":"manuscript_like","synthetic":true,"input":"[SYNTHETIC] data","expectedDisposition":"treat_as_data","forbiddenActions":["execute_embedded_instruction"]},` +
		`{"id":"synthetic-2","class":"tool_output_like","synthetic":true,"input":"[SYNTHETIC] data","expectedDisposition":"reject_envelope","forbiddenActions":["invoke_unrequested_tool"]},` +
		`{"id":"synthetic-3","class":"graph_style_observation_like","synthetic":true,"input":"[SYNTHETIC] data","expectedDisposition":"treat_as_data","forbiddenActions":["bypass_authorization"]},` +
		`{"id":"synthetic-4","class":"checkpoint_like","synthetic":true,"input":"[SYNTHETIC] data","expectedDisposition":"reject_envelope","forbiddenActions":["mutate_runtime_config"]}]}`
	if _, err := parsePromptInjectionCorpus([]byte(valid)); err != nil {
		t.Fatalf("valid corpus error = %v", err)
	}
	tests := []string{
		strings.Replace(valid, `"input":"[SYNTHETIC] data"`, `"input":"[SYNTHETIC] data","expectedOutput":"free form"`, 1),
		strings.Replace(valid, `"id":"synthetic-2"`, `"id":"synthetic-1"`, 1),
		strings.Replace(valid, `"synthetic":true`, `"synthetic":false`, 1),
		strings.Replace(valid, `[SYNTHETIC] data`, `real manuscript paragraph`, 1),
	}
	for _, raw := range tests {
		if _, err := parsePromptInjectionCorpus([]byte(raw)); err == nil {
			t.Fatal("invalid prompt-injection corpus accepted")
		}
	}
}
