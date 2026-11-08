package sagadraft

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	domain "canonkeeper/backend/internal/domain/sagadraft"
)

// The versions of the text of a draft (specs/2026-11-06-writing-agent.md, WA7.5, WA8). The JSON shapes are the
// ChapterDraftVersion* schemas of contracts/openapi.json.

type versionSummaryView struct {
	Number      int       `json:"number"`
	Reason      string    `json:"reason"`
	Words       int       `json:"words"`
	AuthorWords int       `json:"authorWords"`
	Plan        *int      `json:"plan,omitempty"`
	CreatedAt   time.Time `json:"createdAt"`
}

type versionView struct {
	versionSummaryView
	Text    string                `json:"text"`
	Pack    string                `json:"pack,omitempty"`
	Support []domain.SceneSupport `json:"support"`
}

func versionSummaryDTO(version domain.Version) versionSummaryView {
	view := versionSummaryView{Number: version.Number, Reason: string(version.Reason), Words: version.Words, AuthorWords: version.AuthorWords, CreatedAt: version.CreatedAt}
	if version.PlanNumber > 0 {
		plan := version.PlanNumber
		view.Plan = &plan
	}
	return view
}

func versionNumber(r *http.Request) (int, bool) {
	number, err := strconv.Atoi(strings.TrimSpace(chi.URLParam(r, "number")))
	return number, err == nil && number >= 1
}

func (handlers Handlers) listVersions(w http.ResponseWriter, r *http.Request) {
	actor, ok := handlers.reader(w, r)
	if !ok {
		return
	}
	versions, err := handlers.useCases.ListVersions.Execute(r.Context(), actor, DraftID(r))
	if err != nil {
		WriteError(w, r.Context(), err)
		return
	}
	view := struct {
		Versions []versionSummaryView `json:"versions"`
	}{Versions: make([]versionSummaryView, len(versions))}
	for i, version := range versions {
		view.Versions[i] = versionSummaryDTO(version)
	}
	WriteJSON(w, http.StatusOK, view)
}

func (handlers Handlers) getVersion(w http.ResponseWriter, r *http.Request) {
	actor, ok := handlers.reader(w, r)
	if !ok {
		return
	}
	number, valid := versionNumber(r)
	if !valid {
		WriteError(w, r.Context(), domain.ErrInvalid)
		return
	}
	version, err := handlers.useCases.GetVersion.Execute(r.Context(), actor, DraftID(r), number)
	if err != nil {
		WriteError(w, r.Context(), err)
		return
	}
	view := versionView{versionSummaryView: versionSummaryDTO(version), Text: version.Text, Pack: version.PackID, Support: version.Support}
	if view.Support == nil {
		view.Support = []domain.SceneSupport{}
	}
	WriteJSON(w, http.StatusOK, view)
}

type textPlaceBody struct {
	Quote string `json:"quote"`
	From  string `json:"from"`
	To    string `json:"to"`
}

func (handlers Handlers) takeVersion(w http.ResponseWriter, r *http.Request) {
	actor, ok := handlers.writer(w, r)
	if !ok {
		return
	}
	number, valid := versionNumber(r)
	var body struct {
		ExpectedRevision int64          `json:"expectedRevision"`
		Whole            bool           `json:"whole"`
		From             string         `json:"from"`
		To               string         `json:"to"`
		Place            *textPlaceBody `json:"place"`
		After            bool           `json:"after"`
	}
	if err := ReadBody(w, r, &body); err != nil || !valid {
		WriteError(w, r.Context(), domain.ErrInvalid)
		return
	}
	take := domain.Take{Whole: body.Whole, From: body.From, To: body.To, After: body.After}
	if body.Place != nil {
		take.Place = &domain.Scene{Place: body.Place.Quote, From: body.Place.From, To: body.Place.To}
	}
	draft, err := handlers.useCases.TakeVersion.Execute(r.Context(), actor, DraftID(r), number, body.ExpectedRevision, take)
	respondDraft(w, r, http.StatusOK, draft, err)
}
