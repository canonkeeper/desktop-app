package governance

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"io"
	"strings"
)

//go:embed testdata/prompt_injection_corpus.json
var promptInjectionCorpusJSON []byte

type PromptInjectionClass string

const (
	PromptClassManuscriptLike            PromptInjectionClass = "manuscript_like"
	PromptClassToolOutputLike            PromptInjectionClass = "tool_output_like"
	PromptClassGraphStyleObservationLike PromptInjectionClass = "graph_style_observation_like"
	PromptClassCheckpointLike            PromptInjectionClass = "checkpoint_like"
)

type PromptDisposition string

const (
	PromptTreatAsData    PromptDisposition = "treat_as_data"
	PromptRejectEnvelope PromptDisposition = "reject_envelope"
)

func (disposition PromptDisposition) Validate() error {
	if disposition != PromptTreatAsData && disposition != PromptRejectEnvelope {
		return errors.New("governance: unknown prompt disposition")
	}
	return nil
}

type ForbiddenAction string

const (
	ForbiddenExecuteEmbeddedInstruction   ForbiddenAction = "execute_embedded_instruction"
	ForbiddenMutateRuntimeConfig          ForbiddenAction = "mutate_runtime_config"
	ForbiddenInvokeUnrequestedTool        ForbiddenAction = "invoke_unrequested_tool"
	ForbiddenDiscloseGovernedData         ForbiddenAction = "disclose_governed_data"
	ForbiddenBypassAuthorization          ForbiddenAction = "bypass_authorization"
	ForbiddenPersistCheckpointInstruction ForbiddenAction = "persist_checkpoint_instruction"
)

func (action ForbiddenAction) valid() bool {
	switch action {
	case ForbiddenExecuteEmbeddedInstruction, ForbiddenMutateRuntimeConfig, ForbiddenInvokeUnrequestedTool,
		ForbiddenDiscloseGovernedData, ForbiddenBypassAuthorization, ForbiddenPersistCheckpointInstruction:
		return true
	default:
		return false
	}
}

type PromptInjectionCase struct {
	ID                  string               `json:"id"`
	Class               PromptInjectionClass `json:"class"`
	Synthetic           bool                 `json:"synthetic"`
	Input               string               `json:"input"`
	ExpectedDisposition PromptDisposition    `json:"expectedDisposition"`
	ForbiddenActions    []ForbiddenAction    `json:"forbiddenActions"`
}

type promptInjectionCorpusFile struct {
	Schema string                `json:"schema"`
	Cases  []PromptInjectionCase `json:"cases"`
}

func PromptInjectionCorpus() ([]PromptInjectionCase, error) {
	return parsePromptInjectionCorpus(promptInjectionCorpusJSON)
}

func parsePromptInjectionCorpus(raw []byte) ([]PromptInjectionCase, error) {
	var file promptInjectionCorpusFile
	if err := decodeFixture(raw, &file); err != nil || file.Schema != "CKPromptInjectionCorpusV1" {
		return nil, errors.New("governance: invalid prompt-injection corpus")
	}
	wantClasses := [...]PromptInjectionClass{
		PromptClassManuscriptLike,
		PromptClassToolOutputLike,
		PromptClassGraphStyleObservationLike,
		PromptClassCheckpointLike,
	}
	if len(file.Cases) != len(wantClasses) {
		return nil, errors.New("governance: incomplete prompt-injection corpus")
	}
	seenIDs := make(map[string]bool, len(file.Cases))
	for index := range file.Cases {
		testCase := &file.Cases[index]
		if testCase.Class != wantClasses[index] || seenIDs[testCase.ID] || !strings.HasPrefix(testCase.ID, "synthetic-") ||
			!testCase.Synthetic || !strings.Contains(testCase.Input, "[SYNTHETIC]") || len(testCase.Input) > 4096 ||
			testCase.ExpectedDisposition.Validate() != nil || len(testCase.ForbiddenActions) == 0 {
			return nil, errors.New("governance: invalid prompt-injection case")
		}
		seenIDs[testCase.ID] = true
		seenActions := make(map[ForbiddenAction]bool, len(testCase.ForbiddenActions))
		for _, action := range testCase.ForbiddenActions {
			if !action.valid() || seenActions[action] {
				return nil, errors.New("governance: invalid prompt-injection forbidden action")
			}
			seenActions[action] = true
		}
		testCase.ForbiddenActions = append([]ForbiddenAction(nil), testCase.ForbiddenActions...)
	}
	return file.Cases, nil
}

func decodeFixture(raw []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("governance: trailing fixture data")
	}
	return nil
}
