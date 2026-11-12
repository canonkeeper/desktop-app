package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"canonkeeper/backend/internal/domain/quota"
)

// specs/2026-11-09-demo-saga.md DS3.3: the refusal of the demo saga is its own answer — 409 demo_saga.read_only,
// without a limit and without Retry-After — through every handler that answers a refusal of the quota fence, and
// an error that is neither is not answered here.
func TestWriteQuotaProblemAnswersTheRefusalOfTheDemoSaga(t *testing.T) {
	rec := httptest.NewRecorder()
	if !WriteQuotaProblem(rec, fmt.Errorf("stage the import: %w", quota.ErrDemoSaga), time.Now()) {
		t.Fatal("the refusal of the demo saga was not answered")
	}
	var problem Problem
	if err := json.NewDecoder(rec.Body).Decode(&problem); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusConflict || problem.Code != "demo_saga.read_only" || problem.Status != http.StatusConflict {
		t.Fatalf("the refusal = %d %+v; want 409 demo_saga.read_only", rec.Code, problem)
	}
	if rec.Header().Get("Retry-After") != "" || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("the headers of the refusal = %v; want no Retry-After and no-store", rec.Header())
	}

	rec = httptest.NewRecorder()
	if WriteQuotaProblem(rec, errors.New("another failure"), time.Now()) || rec.Body.Len() != 0 {
		t.Fatalf("an error of another kind was answered: %s", rec.Body.String())
	}
}
