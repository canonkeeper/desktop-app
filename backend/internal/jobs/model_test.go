package jobs

import (
	"encoding"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"canonkeeper/backend/internal/auth"
	"canonkeeper/backend/internal/governance"
)

func TestModelBindingValidateClosedOriginAndKindCombinations(t *testing.T) {
	valid := []ModelBinding{
		{JobID: "job", ProjectID: "project", Kind: KindResearchChatTurn, Origin: ModelOriginUser, ActorUserID: "actor", InitiatingAuthSessionID: "session", InputContractVersion: 2},
	}
	for _, binding := range valid {
		if err := binding.Validate(); err != nil {
			t.Fatalf("valid binding %#v: %v", binding, err)
		}
	}

	invalid := []ModelBinding{
		{},
		{JobID: "job", ProjectID: "project", Kind: KindImport, Origin: ModelOriginUser, ActorUserID: "actor", InitiatingAuthSessionID: "session", InputContractVersion: 1},
		{JobID: "job", ProjectID: "project", Kind: KindResearchChatTurn, Origin: ModelOrigin("service"), InputContractVersion: 1},
		{JobID: "job", ProjectID: "project", Kind: KindChapterCards, Origin: ModelOriginUser, ActorUserID: "actor", InitiatingAuthSessionID: "session", InputContractVersion: 1},
		{JobID: "job", ProjectID: "project", Kind: KindResearchChatTurn, Origin: ModelOriginUser, ActorUserID: "actor", InputContractVersion: 1},
		{JobID: "job", ProjectID: "project", Kind: KindResearchChatTurn, Origin: ModelOriginUser, ActorUserID: "actor", InitiatingAuthSessionID: "session"},
	}
	for _, binding := range invalid {
		if err := binding.Validate(); err == nil {
			t.Fatalf("invalid binding accepted: %#v", binding)
		}
	}
}

func TestModelBindingHasNoTextOrLogSerializationHelpers(t *testing.T) {
	for _, value := range []any{ModelBinding{}, CreateModelJobInput{}} {
		if _, ok := value.(fmt.Stringer); ok {
			t.Fatalf("%T implements fmt.Stringer", value)
		}
		if _, ok := value.(encoding.TextMarshaler); ok {
			t.Fatalf("%T implements encoding.TextMarshaler", value)
		}
	}
}

func TestModelPolicyForAcceptedCombinations(t *testing.T) {
	for _, test := range []struct {
		name       string
		subject    Subject
		origin     ModelOrigin
		queueClass QueueClass
	}{
		{name: "research chat", subject: validResearchChatSubject(), origin: ModelOriginUser, queueClass: QueueClassInteractive},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := validCreateModelJobInput(test.subject, test.origin)
			policy, err := ModelPolicyFor(input)
			if err != nil {
				t.Fatal(err)
			}
			want := ModelPolicy{QueueClass: test.queueClass, MaxAttempts: 3}
			if policy != want {
				t.Fatalf("ModelPolicyFor() = %#v, want %#v", policy, want)
			}
		})
	}
}

func TestModelPolicyForRejectsInvalidStarts(t *testing.T) {
	validUser := validCreateModelJobInput(validResearchChatSubject(), ModelOriginUser)
	foreignProject := "10000000-0000-4000-8000-000000000099"

	for _, test := range []struct {
		name  string
		input CreateModelJobInput
	}{
		{name: "import", input: validCreateModelJobInput(ImportSubject{Type: KindImport, ID: testProjectID, ProjectID: testProjectID}, ModelOriginUser)},
		{name: "canon text indexing", input: validCreateModelJobInput(CanonTextIndexSubject{Type: KindCanonTextIndex, ProjectID: testProjectID, SourceView: governance.SourceViewDraft, CatalogRevision: 1}, ModelOriginUser)},
		{name: "chapter cards", input: validCreateModelJobInput(ChapterCardsSubject{Type: KindChapterCards, ProjectID: testProjectID, SourceView: governance.SourceViewDraft}, ModelOriginUser)},
		{name: "service research chat", input: validCreateModelJobInput(validResearchChatSubject(), ModelOrigin("service"))},
		{name: "unknown origin", input: func() CreateModelJobInput { input := validUser; input.Origin = ModelOrigin("unknown"); return input }()},
		{name: "missing user actor", input: func() CreateModelJobInput { input := validUser; input.ActorUserID = ""; return input }()},
		{name: "missing user proof", input: func() CreateModelJobInput {
			input := validUser
			input.SessionProof = auth.SessionWriteProof{}
			return input
		}()},
		{name: "partial user proof", input: func() CreateModelJobInput { input := validUser; input.SessionProof.CSRFSecretHash = ""; return input }()},
		{name: "missing job id", input: func() CreateModelJobInput { input := validUser; input.JobID = ""; return input }()},
		{name: "missing project id", input: func() CreateModelJobInput { input := validUser; input.ProjectID = ""; return input }()},
		{name: "missing subject", input: func() CreateModelJobInput { input := validUser; input.Subject = nil; return input }()},
		{name: "zero contract version", input: func() CreateModelJobInput { input := validUser; input.InputContractVersion = 0; return input }()},
		{name: "subject project mismatch", input: func() CreateModelJobInput { input := validUser; input.ProjectID = foreignProject; return input }()},
		{name: "subject kind mismatch", input: func() CreateModelJobInput {
			input := validUser
			subject := validResearchChatSubject()
			subject.Type = KindChapterCards
			input.Subject = subject
			return input
		}()},
		{name: "missing idempotency record", input: func() CreateModelJobInput { input := validUser; input.Idempotency.RecordID = ""; return input }()},
		{name: "missing idempotency scope", input: func() CreateModelJobInput { input := validUser; input.Idempotency.ScopeHash = ""; return input }()},
		{name: "missing idempotency operation", input: func() CreateModelJobInput { input := validUser; input.Idempotency.OperationID = ""; return input }()},
		{name: "missing idempotency key", input: func() CreateModelJobInput {
			input := validUser
			input.Idempotency.IdempotencyKeyHash = ""
			return input
		}()},
		{name: "missing idempotency request", input: func() CreateModelJobInput { input := validUser; input.Idempotency.RequestHash = ""; return input }()},
		{name: "missing idempotency status", input: func() CreateModelJobInput { input := validUser; input.Idempotency.StatusCode = 0; return input }()},
		{name: "missing creation time", input: func() CreateModelJobInput { input := validUser; input.CreatedAt = time.Time{}; return input }()},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := ModelPolicyFor(test.input); err == nil {
				t.Fatal("ModelPolicyFor accepted invalid start")
			}
		})
	}
}

func TestCreateModelJobInputIsBackendOnlyAndHasNoPolicyOverrides(t *testing.T) {
	typeOfInput := reflect.TypeOf(CreateModelJobInput{})
	fieldNames := make([]string, 0, typeOfInput.NumField())
	for i := 0; i < typeOfInput.NumField(); i++ {
		field := typeOfInput.Field(i)
		fieldNames = append(fieldNames, field.Name)
		if field.Tag.Get("json") != "-" {
			t.Fatalf("CreateModelJobInput.%s json tag = %q, want -", field.Name, field.Tag.Get("json"))
		}
	}
	wantFieldNames := []string{"JobID", "ProjectID", "Subject", "Origin", "ActorUserID", "SessionProof", "InputContractVersion", "Idempotency", "CreatedAt"}
	if !reflect.DeepEqual(fieldNames, wantFieldNames) {
		t.Fatalf("CreateModelJobInput fields = %v, want %v", fieldNames, wantFieldNames)
	}
	for _, forbidden := range []string{
		"InitiatingAuthSessionID", "QueueClass", "Priority", "MaxAttempts", "RetryDelay", "CanCancel",
		"Progress", "Result", "Error", "Route", "Handler", "Runtime", "Prompt", "Body", "Owner",
	} {
		if _, ok := typeOfInput.FieldByName(forbidden); ok {
			t.Fatalf("CreateModelJobInput exposes forbidden field %s", forbidden)
		}
	}
	raw, err := json.Marshal(validCreateModelJobInput(validResearchChatSubject(), ModelOriginUser))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "{}" {
		t.Fatalf("CreateModelJobInput JSON = %s, want {}", raw)
	}
	policyType := reflect.TypeOf(ModelPolicy{})
	if policyType.NumField() != 2 || policyType.Field(0).Name != "QueueClass" || policyType.Field(1).Name != "MaxAttempts" {
		t.Fatalf("ModelPolicy shape = %v, want QueueClass and MaxAttempts only", policyType)
	}
}

func validCreateModelJobInput(subject Subject, origin ModelOrigin) CreateModelJobInput {
	input := CreateModelJobInput{
		JobID:                "10000000-0000-4000-8000-000000000010",
		ProjectID:            testProjectID,
		Subject:              subject,
		Origin:               origin,
		InputContractVersion: 1,
		Idempotency: IdempotentJobStoreInput{
			RecordID:           "10000000-0000-4000-8000-000000000011",
			ScopeHash:          "scope",
			OperationID:        "operation",
			IdempotencyKeyHash: "key",
			RequestHash:        "request",
			StatusCode:         200,
		},
		CreatedAt: time.Date(2026, time.July, 21, 12, 0, 0, 0, time.UTC),
	}
	if origin == ModelOriginUser {
		input.ActorUserID = "10000000-0000-4000-8000-000000000012"
		input.SessionProof = auth.SessionWriteProof{SessionHash: "session", CSRFSecretHash: "csrf"}
	}
	return input
}

func validResearchChatSubject() ResearchChatTurnSubject {
	return ResearchChatTurnSubject{Type: KindResearchChatTurn, ProjectID: testProjectID, ChatID: testChatID, TurnID: testTurnID, SourceView: governance.SourceViewDraft, Scope: "project"}
}

func TestModelClaimValidation(t *testing.T) {
	workerClaim := WorkerClaim{
		ProjectID:        testProjectID,
		JobID:            "10000000-0000-4000-8000-000000000010",
		ClaimIncarnation: 1,
	}
	valid := ModelClaim{
		Claim: workerClaim, Kind: KindResearchChatTurn, Subject: validResearchChatSubject(),
		Origin: ModelOriginUser, ActorUserID: "actor", InitiatingAuthSessionID: "session", InputContractVersion: 2,
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid claim %#v: %v", valid, err)
	}
	invalid := []ModelClaim{
		{},
		{Claim: workerClaim, Kind: KindResearchChatTurn, Origin: ModelOriginUser, ActorUserID: "actor", InitiatingAuthSessionID: "session", InputContractVersion: 1},
		{Claim: workerClaim, Kind: KindResearchChatTurn, Subject: validResearchChatSubject(), Origin: ModelOrigin("service"), InputContractVersion: 1},
		{Claim: workerClaim, Kind: KindResearchChatTurn, Subject: validResearchChatSubject(), Origin: ModelOriginUser, ActorUserID: "actor", InputContractVersion: 1},
		{Claim: workerClaim, Kind: KindResearchChatTurn, Subject: validResearchChatSubject(), Origin: ModelOriginUser, ActorUserID: "actor", InitiatingAuthSessionID: "session"},
		{Claim: WorkerClaim{ProjectID: testProjectID, JobID: workerClaim.JobID}, Kind: KindResearchChatTurn, Subject: validResearchChatSubject(), Origin: ModelOriginUser, ActorUserID: "actor", InitiatingAuthSessionID: "session", InputContractVersion: 1},
	}
	for _, claim := range invalid {
		if err := claim.Validate(); !errors.Is(err, ErrValidation) {
			t.Fatalf("invalid claim %#v Validate() error = %v, want ErrValidation", claim, err)
		}
	}
}

func TestModelClaimIsBackendOnly(t *testing.T) {
	raw, err := json.Marshal(ModelClaim{})
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "{}" {
		t.Fatalf("ModelClaim JSON = %s, want {}", raw)
	}
	if _, ok := any(ModelClaim{}).(fmt.Stringer); ok {
		t.Fatal("ModelClaim implements fmt.Stringer")
	}
	if _, ok := any(ModelClaim{}).(encoding.TextMarshaler); ok {
		t.Fatal("ModelClaim implements encoding.TextMarshaler")
	}
}
