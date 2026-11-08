package governance_test

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"canonkeeper/backend/internal/domain/deletion"
	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/observability"
	"canonkeeper/backend/internal/search"
)

const redactionHarnessProjectID = "00000000-0000-4000-8000-000000009101"

//go:embed testdata/redaction_forbidden_strings.json
var forbiddenStringsJSON []byte

//go:embed testdata/prompt_injection_corpus.json
var promptInjectionCorpusJSON []byte

type promptInjectionCase struct {
	ID    string `json:"id"`
	Input string `json:"input"`
}

type promptInjectionCorpus struct {
	Cases []promptInjectionCase `json:"cases"`
}

type forbiddenStringsFile struct {
	Schema string                `json:"schema"`
	Cases  []forbiddenStringCase `json:"cases"`
}

type forbiddenStringCase struct {
	ID        string `json:"id"`
	Category  string `json:"category"`
	Synthetic bool   `json:"synthetic"`
	Value     string `json:"value"`
}

func TestRedactionHarnessKeepsCanariesOutOfOperationalOutputs(t *testing.T) {
	t.Parallel()

	for _, testCase := range forbiddenStringCases(t) {
		testCase := testCase
		t.Run(testCase.ID, func(t *testing.T) {
			t.Parallel()
			testRequestLogRedaction(t, testCase)
			testCanonicalSearchRedaction(t, testCase)
		})
	}
}

func TestPromptInjectionCorpusCannotMutatePhaseOneConfiguration(t *testing.T) {
	t.Parallel()

	cases := promptInjectionCases(t)
	surfaceBaseline := governance.AuthorizationSurfacePolicy()
	sourceTargets := deletion.SourceDeletionTargets()

	for _, testCase := range cases {
		testCase := testCase
		t.Run(testCase.ID, func(t *testing.T) {
			t.Parallel()
			candidateSurfaces := governance.AuthorizationSurfacePolicy()
			candidateSurfaces[0].Surface = governance.Surface(testCase.Input)
			if governance.SourceEntityKind(testCase.Input).Valid() {
				t.Fatalf("%s: embedded text changed source scope", testCase.ID)
			}
			candidateDeletionKind := governance.DeletionSelectionKind(testCase.Input)
			if candidateDeletionKind == governance.DeletionSelectionProject ||
				candidateDeletionKind == governance.DeletionSelectionBook ||
				candidateDeletionKind == governance.DeletionSelectionChapter {
				t.Fatalf("%s: embedded text selected a deletion scope", testCase.ID)
			}
			if !reflect.DeepEqual(governance.AuthorizationSurfacePolicy(), surfaceBaseline) {
				t.Fatalf("%s: authorization surface registry was mutated", testCase.ID)
			}
			if !reflect.DeepEqual(deletion.SourceDeletionTargets(), sourceTargets) {
				t.Fatalf("%s: deletion target order was mutated", testCase.ID)
			}
		})
	}
}

func promptInjectionCases(t *testing.T) []promptInjectionCase {
	t.Helper()
	var corpus promptInjectionCorpus
	if err := json.Unmarshal(promptInjectionCorpusJSON, &corpus); err != nil || len(corpus.Cases) == 0 {
		t.Fatal("prompt-injection corpus is invalid")
	}
	return corpus.Cases
}

func testRequestLogRedaction(t *testing.T, testCase forbiddenStringCase) {
	t.Helper()
	var output bytes.Buffer
	fixedNow := time.Date(2026, 7, 14, 1, 0, 0, 0, time.UTC)
	handler := observability.RequestLogger(&output, observability.RequestLoggerOptions{
		Now:          func() time.Time { return fixedNow },
		NewRequestID: func() string { return "00000000-0000-4000-8000-000000009105" },
		RouteInfo: func(*http.Request) observability.RouteInfo {
			return observability.RouteInfo{OperationID: "getProjectSupportSnapshot", PathTemplate: "/projects/{projectId}/support-snapshot"}
		},
	})(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = io.Copy(io.Discard, request.Body)
		writer.WriteHeader(http.StatusServiceUnavailable)
	}))
	request := httptest.NewRequest(http.MethodGet, "/api/v1/projects/synthetic?probe="+url.QueryEscape(testCase.Value), strings.NewReader(testCase.Value))
	request.Header.Set(observability.RequestIDHeader, testCase.Value)
	request.Header.Set("X-Synthetic-Canary", testCase.Value)
	handler.ServeHTTP(httptest.NewRecorder(), request)
	assertCanaryAbsent(t, testCase, "request log", output.Bytes())
}

func testCanonicalSearchRedaction(t *testing.T, testCase forbiddenStringCase) {
	t.Helper()
	request := search.SearchRequest{
		ProjectID:  redactionHarnessProjectID,
		Query:      testCase.Value,
		SourceView: governance.SourceViewDraft,
	}
	actor := search.Actor{UserID: "00000000-0000-4000-8000-000000009107"}

	for _, seam := range []struct {
		name       string
		storeError error
		want       error
	}{
		{name: "canonical database error", storeError: errors.New("synthetic database failure: " + testCase.Value)},
		{name: "canonical cancellation", storeError: fmt.Errorf("%s: %w", testCase.Value, context.Canceled), want: context.Canceled},
		{name: "canonical deadline", storeError: fmt.Errorf("%s: %w", testCase.Value, context.DeadlineExceeded), want: context.DeadlineExceeded},
	} {
		_, err := search.NewService(redactionCanonicalSearcher{err: seam.storeError}).Search(context.Background(), actor, request)
		if err == nil {
			t.Fatalf("%s: %s unexpectedly succeeded", testCase.ID, seam.name)
		}
		if seam.want != nil && err != seam.want {
			t.Fatalf("%s: %s error = %v, want standard %v", testCase.ID, seam.name, err, seam.want)
		}
		assertCanaryAbsent(t, testCase, seam.name, err)
	}

	_, authErr := search.NewService(redactionCanonicalSearcher{}).Search(
		context.Background(), search.Actor{UserID: testCase.Value}, request,
	)
	if authErr == nil {
		t.Fatalf("%s: canonical authorization unexpectedly succeeded", testCase.ID)
	}
	assertCanaryAbsent(t, testCase, "canonical authorization error", authErr)

	manuscript := search.LexicalSearchRow{
		ID: "00000000-0000-4000-8000-000000009108", ProjectID: redactionHarnessProjectID,
		Rank: 1, Content: testCase.Value,
	}
	_, manuscriptErr := search.NewService(redactionCanonicalSearcher{
		output: search.LexicalSearchOutput{Rows: []search.LexicalSearchRow{manuscript}},
	}).Search(context.Background(), actor, request)
	if manuscriptErr == nil {
		t.Fatalf("%s: corrupt canonical manuscript unexpectedly succeeded", testCase.ID)
	}
	assertCanaryAbsent(t, testCase, "canonical manuscript error", manuscriptErr)
}

func forbiddenStringCases(t *testing.T) []forbiddenStringCase {
	t.Helper()
	var file forbiddenStringsFile
	decoder := json.NewDecoder(bytes.NewReader(forbiddenStringsJSON))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&file); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
		t.Fatal("redaction corpus schema is invalid")
	}
	if file.Schema != "CKRedactionForbiddenStringsV1" || len(file.Cases) == 0 {
		t.Fatal("redaction corpus is empty or has an unknown schema")
	}
	seen := make(map[string]struct{}, len(file.Cases))
	for _, testCase := range file.Cases {
		if testCase.ID == "" || testCase.Category == "" || !testCase.Synthetic ||
			!strings.HasPrefix(testCase.Value, "[SYNTHETIC CANARY ") {
			t.Fatal("redaction corpus contains an invalid case")
		}
		if _, duplicate := seen[testCase.ID]; duplicate {
			t.Fatal("redaction corpus contains a duplicate case")
		}
		seen[testCase.ID] = struct{}{}
	}
	return append([]forbiddenStringCase(nil), file.Cases...)
}

func assertCanaryAbsent(t *testing.T, testCase forbiddenStringCase, seam string, outputs ...any) {
	t.Helper()
	forms := canaryForms(testCase.Value)
	for _, output := range outputs {
		var value string
		switch output := output.(type) {
		case nil:
			continue
		case []byte:
			value = string(output)
		case json.RawMessage:
			value = string(output)
		case string:
			value = output
		case error:
			value = output.Error()
		default:
			t.Fatalf("%s: %s has unsupported redaction output %T", testCase.ID, seam, output)
		}
		lower := strings.ToLower(value)
		normalized := strings.Join(strings.Fields(value), " ")
		for _, form := range forms {
			if form != "" && (strings.Contains(value, form) || strings.Contains(lower, strings.ToLower(form)) || strings.Contains(normalized, strings.Join(strings.Fields(form), " "))) {
				t.Fatalf("%s: %s leaked a forbidden value", testCase.ID, seam)
			}
		}
	}
}

func canaryForms(value string) []string {
	quoted, _ := json.Marshal(value)
	jsonEscaped := strings.TrimSuffix(strings.TrimPrefix(string(quoted), `"`), `"`)
	return []string{
		value,
		strings.Join(strings.Fields(value), " "),
		url.QueryEscape(value),
		url.PathEscape(value),
		html.EscapeString(value),
		jsonEscaped,
	}
}

type redactionCanonicalSearcher struct {
	output search.LexicalSearchOutput
	err    error
}

func (searcher redactionCanonicalSearcher) SearchLexical(context.Context, search.LexicalSearchInput) (search.LexicalSearchOutput, error) {
	return searcher.output, searcher.err
}

func (searcher redactionCanonicalSearcher) SearchExactPhrase(context.Context, search.ExactPhraseSearchInput) (search.ExactPhraseSearchOutput, error) {
	return search.ExactPhraseSearchOutput{}, searcher.err
}
