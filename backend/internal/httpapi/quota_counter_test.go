package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"canonkeeper/backend/internal/domain/quota"
)

// specs/2026-11-13-desk-account-ai.md AI2.1, AI2.6: a refusal of the counter of a site at the start of a turn, a correction or
// an upload of materials reaches the page as a refusal of a limit — the counter not linked is «помощник не подключён», a
// counter that did not answer says whether the site may give the unit back itself, a refusal the app remembers keeps the code
// of the site, the fence of the day with its Retry-After until the Moscow midnight.
func TestARefusalOfTheCounterOfASiteIsARefusalOfALimit(t *testing.T) {
	now := time.Date(2026, 11, 13, 20, 0, 0, 0, time.UTC)
	cases := []struct {
		err       error
		status    int
		code      string
		mayReturn *bool
		retry     string
	}{
		{quota.ErrCounterUnlinked, http.StatusConflict, "helper.not_connected", nil, ""},
		{&quota.CounterUnreachable{MayReturn: true}, http.StatusServiceUnavailable, quota.CounterUnreachableCode, ptr(true), ""},
		{&quota.CounterUnreachable{}, http.StatusServiceUnavailable, quota.CounterUnreachableCode, ptr(false), ""},
		{&quota.CounterRefusal{Code: "ai.daily_fence"}, http.StatusTooManyRequests, "ai.daily_fence", nil, "3600"},
		{&quota.CounterRefusal{Code: quota.CounterUnfitCode}, http.StatusConflict, quota.CounterUnfitCode, nil, ""},
		{&quota.CounterRefusal{Code: "app.request_too_large"}, http.StatusConflict, "app.request_too_large", nil, ""},
	}
	for _, refusal := range cases {
		recorder := httptest.NewRecorder()
		if !WriteQuotaProblem(recorder, refusal.err, now) {
			t.Fatalf("%v is not a refusal of a limit", refusal.err)
		}
		var problem struct {
			Code      string `json:"code"`
			MayReturn *bool  `json:"mayReturn"`
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &problem); err != nil {
			t.Fatal(err)
		}
		if recorder.Code != refusal.status || problem.Code != refusal.code || recorder.Header().Get("Retry-After") != refusal.retry ||
			(refusal.mayReturn == nil) != (problem.MayReturn == nil) || refusal.mayReturn != nil && *refusal.mayReturn != *problem.MayReturn {
			t.Fatalf("%v = %d %s %q", refusal.err, recorder.Code, recorder.Body.String(), recorder.Header().Get("Retry-After"))
		}
	}
}

func ptr(value bool) *bool { return &value }
