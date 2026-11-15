// SPDX-License-Identifier: AGPL-3.0-only

package obsidian

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"canonkeeper/backend/internal/auth"
	domain "canonkeeper/backend/internal/domain/obsidian"
	service "canonkeeper/backend/internal/services/obsidian"
)

// materials answers a load of records by the saga it is asked of and keeps what it was asked.
type materials struct{}

var askedMaterials service.MaterialsInput

func (materials) Execute(_ context.Context, input service.MaterialsInput) (service.MaterialsLoaded, error) {
	askedMaterials = input
	switch input.ProjectID {
	case "changed":
		return service.MaterialsLoaded{}, service.FolderChanged{Folders: []string{"Персонажи"}}
	case "large":
		return service.MaterialsLoaded{}, domain.TooManyRecords{Records: 12, Saga: domain.MaxRecords + 3}
	case "reused":
		return service.MaterialsLoaded{}, service.ErrReplayConflict
	case "slow":
		return service.MaterialsLoaded{}, service.ErrSlow
	case "conflict":
		return service.MaterialsLoaded{}, service.ErrConflict
	case "unlinked":
		return service.MaterialsLoaded{}, service.ErrNotLinked
	case "session":
		return service.MaterialsLoaded{}, auth.ErrUnauthenticated
	case "unprinted":
		return service.MaterialsLoaded{}, service.ErrMaterialsFingerprint
	case "unseen":
		return service.MaterialsLoaded{}, service.ErrMaterialsSeen
	}
	loaded := service.MaterialsLoaded{Skipped: domain.Skipped{domain.SkipNoName: 2}}
	loaded.Add(domain.OutcomeCreated, "Ива")
	loaded.Add(domain.OutcomeKeptTaken, "Перевал")
	return loaded, nil
}

const seen = "0123456789abcdef"

func materialsBodyWith(records string) string {
	return `{"books":["Книга 1"],"materials":[{"path":"Персонажи","kind":"character","fingerprint":"` + fingerprint + `"}],` +
		`"exclude":["Персонажи/Черновик.md"],"records":[` + records + `],"replace":["other_source"]}`
}

func materialsOf(project, body string, change func(*http.Request)) *httptest.ResponseRecorder {
	return askWith(http.MethodPost, "/projects/"+project+"/obsidian/materials", body, func(request *http.Request) {
		request.Header.Set("Idempotency-Key", "materials-key-1")
		if change != nil {
			change(request)
		}
	})
}

// specs/2026-11-15-desk-obsidian-notes.md ON1.1, ON1.4, ON1.7, ON1.8: importObsidianMaterials takes the choice, the
// fingerprint of each folder of materials, what the author saw of each record and his decisions with one Idempotency-Key
// and the proof of the session; it answers the groups of the records with their names and says each refusal with what the
// page needs.
func TestTheLoadOfTheRecordsOfTheNotes(t *testing.T) {
	recorder := materialsOf("saga", materialsBodyWith(`{"path":"Персонажи/Ива.md","seen":"`+seen+`"},{"path":"Места/Перевал.md","seen":"`+seen+`","decision":"keep"}`), nil)
	var body struct {
		Created struct {
			Count int      `json:"count"`
			Names []string `json:"names"`
		} `json:"created"`
		Updated struct {
			Names []string `json:"names"`
		} `json:"updated"`
		Kept struct {
			Taken struct {
				Count int      `json:"count"`
				Names []string `json:"names"`
			} `json:"taken"`
		} `json:"kept"`
		Skipped []map[string]any `json:"skipped"`
	}
	if recorder.Code != http.StatusOK || json.Unmarshal(recorder.Body.Bytes(), &body) != nil || body.Created.Count != 1 ||
		body.Created.Names[0] != "Ива" || body.Updated.Names == nil || body.Kept.Taken.Names[0] != "Перевал" || len(body.Skipped) != 1 {
		t.Fatalf("the load = %d %s", recorder.Code, recorder.Body)
	}
	if askedMaterials.Key != "materials-key-1" || askedMaterials.Fingerprints["Персонажи"] != fingerprint || askedMaterials.Seen["Персонажи/Ива.md"] != seen ||
		askedMaterials.Decisions.Notes["Места/Перевал.md"] != domain.DecisionKeep || askedMaterials.Decisions.States[domain.StateOtherSource] != domain.DecisionReplace ||
		askedMaterials.Choice.Books[0] != "Книга 1" || askedMaterials.Choice.Exclude[0] != "Персонажи/Черновик.md" {
		t.Fatalf("the use case was asked %+v", askedMaterials)
	}
	for name, change := range map[string]func(*http.Request){
		"no key":         func(request *http.Request) { request.Header.Del("Idempotency-Key") },
		"a query":        func(request *http.Request) { request.URL.RawQuery = "x=1" },
		"no write token": func(request *http.Request) { request.Header.Del("X-CSRF-Token") },
		"another origin": func(request *http.Request) { request.Header.Set("Origin", "http://other.example.test") },
	} {
		if recorder := materialsOf("saga", materialsBodyWith(""), change); recorder.Code < 400 || recorder.Code >= 500 {
			t.Errorf("%s = %d %s", name, recorder.Code, recorder.Body)
		}
	}
	for name, body := range map[string]string{
		"no folder of materials": `{"materials":[]}`,
		"a bad fingerprint":      `{"materials":[{"path":"Персонажи","kind":"character","fingerprint":"ABC"}]}`,
		"a bad seen":             materialsBodyWith(`{"path":"Персонажи/Ива.md","seen":"ABC"}`),
		"a note twice":           materialsBodyWith(`{"path":"Персонажи/Ива.md","seen":"` + seen + `"},{"path":"Персонажи/Ива.md","seen":"` + seen + `"}`),
		"a bad decision":         materialsBodyWith(`{"path":"Персонажи/Ива.md","seen":"` + seen + `","decision":"merge"}`),
		"a rule for a new name":  strings.Replace(materialsBodyWith(""), `"replace":["other_source"]`, `"replace":["new"]`, 1),
		"an unknown field":       strings.Replace(materialsBodyWith(""), `"records"`, `"more":1,"records"`, 1),
	} {
		if recorder := materialsOf("saga", body, nil); recorder.Code != http.StatusBadRequest {
			t.Errorf("%s = %d %s", name, recorder.Code, recorder.Body)
		}
	}
	for project, want := range map[string]struct {
		status int
		code   string
		field  string
	}{
		"changed":   {http.StatusConflict, domain.CodeFolderChanged, `"folders":["Персонажи"]`},
		"large":     {http.StatusUnprocessableEntity, domain.CodeTooLarge, `"records":12,"sagaRecords":5003,"limitRecords":5000`},
		"reused":    {http.StatusConflict, "idempotency.conflict", ""},
		"slow":      {http.StatusServiceUnavailable, domain.CodeSlow, ""},
		"conflict":  {http.StatusConflict, "conflict", ""},
		"unlinked":  {http.StatusConflict, domain.CodeNotLinked, ""},
		"session":   {http.StatusUnauthorized, "auth.session_required", ""},
		"unprinted": {http.StatusBadRequest, "request.validation", ""},
		"unseen":    {http.StatusBadRequest, "request.validation", ""},
	} {
		recorder := materialsOf(project, materialsBodyWith(""), nil)
		if recorder.Code != want.status || !strings.Contains(recorder.Body.String(), `"code":"`+want.code+`"`) ||
			!strings.Contains(recorder.Body.String(), want.field) {
			t.Errorf("%s = %d %s", project, recorder.Code, recorder.Body)
		}
	}
}
