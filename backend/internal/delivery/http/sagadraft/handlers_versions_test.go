package sagadraft

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	domain "canonkeeper/backend/internal/domain/sagadraft"
	service "canonkeeper/backend/internal/services/sagadraft"
)

type versionsStub struct {
	number int
	take   *domain.Take
}

func (stub *versionsStub) ChapterDraftVersions(context.Context, service.Actor, string) ([]domain.Version, error) {
	return []domain.Version{
		{Number: 2, Reason: domain.VersionWritten, Words: 900, AuthorWords: 100, PlanNumber: 3, CreatedAt: time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC)},
		{Number: 1, Reason: domain.VersionBeforeWrite, Words: 100, AuthorWords: 100, CreatedAt: time.Date(2026, 11, 6, 9, 0, 0, 0, time.UTC)},
	}, nil
}

func (stub *versionsStub) ChapterDraftVersion(_ context.Context, _ service.Actor, _ string, number int) (domain.Version, error) {
	stub.number = number
	return domain.Version{Number: number, Reason: domain.VersionWritten, Text: "⟦Кира ушла.⟧", Words: 2, PlanNumber: 3, PackID: "96000000-0000-4000-8000-0000000000aa",
		Support: []domain.SceneSupport{{Scene: 1, From: "Кира ушла.", To: "Кира ушла."}}}, nil
}

func (stub *versionsStub) TakeChapterDraftVersion(_ context.Context, _ service.Write, _ string, number int, _ int64, take domain.Take) (domain.Draft, error) {
	stub.number, stub.take = number, &take
	return domain.Draft{ID: testDraft, Revision: 4}, nil
}

func versionsRouter(versions *versionsStub) http.Handler {
	mux := chi.NewRouter()
	NewHandlers(UseCases{
		ListVersions: service.ListVersions{Store: versions}, GetVersion: service.GetVersion{Store: versions}, TakeVersion: service.TakeVersion{Store: versions},
	}, userStub{}, []string{"http://app.test"}).Mount(mux)
	return mux
}

// specs/2026-11-06-writing-agent.md WA7.5: a member lists the versions without their text and reads one whole; an editor
// takes one back whole or by a piece into a place of the text, behind the write proof.
func TestTheVersionsOfTheTextAreReadAndTakenBack(t *testing.T) {
	versions := &versionsStub{}
	handler := versionsRouter(versions)
	listed := call(t, handler, http.MethodGet, base+"/"+testDraft+"/versions", "", nil)
	var list struct {
		Versions []map[string]any `json:"versions"`
	}
	if listed.Code != http.StatusOK || json.Unmarshal(listed.Body.Bytes(), &list) != nil || len(list.Versions) != 2 || list.Versions[0]["plan"] != 3.0 ||
		list.Versions[0]["text"] != nil || list.Versions[1]["plan"] != nil || list.Versions[1]["reason"] != "before_write" {
		t.Fatalf("list = %d %s", listed.Code, listed.Body)
	}
	read := call(t, handler, http.MethodGet, base+"/"+testDraft+"/versions/2", "", nil)
	var version struct {
		Text    string `json:"text"`
		Pack    string `json:"pack"`
		Support []struct {
			Scene int `json:"scene"`
		} `json:"support"`
	}
	if read.Code != http.StatusOK || json.Unmarshal(read.Body.Bytes(), &version) != nil || version.Text != "⟦Кира ушла.⟧" || version.Pack == "" || len(version.Support) != 1 || versions.number != 2 {
		t.Fatalf("version = %d %s", read.Code, read.Body)
	}
	if bad := call(t, handler, http.MethodGet, base+"/"+testDraft+"/versions/zero", "", nil); bad.Code != http.StatusBadRequest {
		t.Fatalf("a version that is no number = %d", bad.Code)
	}
	taken := call(t, handler, http.MethodPost, base+"/"+testDraft+"/versions/1/take",
		`{"expectedRevision":3,"from":"Кира ушла.","to":"Кира ушла.","place":{"quote":"Ольм ждал."},"after":true}`, nil)
	if taken.Code != http.StatusOK || versions.number != 1 || versions.take == nil || versions.take.Place == nil || versions.take.Place.Place != "Ольм ждал." || !versions.take.After {
		t.Fatalf("take = %d %s, %+v", taken.Code, taken.Body, versions.take)
	}
	if noCSRF := call(t, handler, http.MethodPost, base+"/"+testDraft+"/versions/1/take", `{"expectedRevision":3,"whole":true}`, map[string]string{"X-CSRF-Token": ""}); noCSRF.Code != http.StatusForbidden {
		t.Fatalf("a take without the write proof = %d", noCSRF.Code)
	}
	if unknown := call(t, handler, http.MethodPost, base+"/"+testDraft+"/versions/1/take", `{"expectedRevision":3,"all":true}`, nil); unknown.Code != http.StatusBadRequest {
		t.Fatalf("an unknown field = %d", unknown.Code)
	}
}
