// Package quota serves getAccountQuota: the signed-in writer's limits and use.
package quota

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"canonkeeper/backend/internal/auth"
	domain "canonkeeper/backend/internal/domain/quota"
	"canonkeeper/backend/internal/httpapi"

	"github.com/go-chi/chi/v5"
)

type CurrentUser interface {
	CurrentUser(context.Context, string) (auth.User, error)
}

type AccountQuota interface {
	Execute(context.Context, string) (domain.Account, error)
}

type Handlers struct {
	AccountQuota AccountQuota
	Auth         CurrentUser
}

func (handlers Handlers) Mount(router chi.Router) {
	router.Get("/account/quota", handlers.getAccountQuota)
}

type counter struct {
	Used  int64 `json:"used"`
	Limit int64 `json:"limit"`
}

type sources struct {
	Books      counter `json:"books"`
	Chapters   counter `json:"chapters"`
	Characters counter `json:"characters"`
}

type projectQuota struct {
	ProjectID string  `json:"projectId"`
	Sources   sources `json:"sources"`
}

type accountQuota struct {
	Day         string  `json:"day"`
	ResetsAt    string  `json:"resetsAt"`
	ChatTurns   counter `json:"chatTurns"`
	ActiveTurns counter `json:"activeTurns"`
	Imports     counter `json:"imports"`
	Images      counter `json:"images"`
	// The other counters of a day a refusal can meet (specs/2026-11-10-ux2-tariff-account.md TA2.3): the author's own
	// pictures, the scenes, checks and proofreadings of «Письмо», the messages of the helpers of «Рисование» and «Письмо»
	// and «Переписать».
	ImageUploads  counter        `json:"imageUploads"`
	ChapterDrafts counter        `json:"chapterDrafts"`
	DrawingTurns  counter        `json:"drawingTurns"`
	DraftTurns    counter        `json:"draftTurns"`
	DraftRewrites counter        `json:"draftRewrites"`
	Projects      counter        `json:"projects"`
	Account       sources        `json:"account"`
	OwnedProjects []projectQuota `json:"ownedProjects"`
}

func (handlers Handlers) getAccountQuota(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.URL.RawQuery != "" {
		httpapi.WriteProblem(w, "", http.StatusBadRequest, "Bad Request", "validation failed", "request.validation")
		return
	}
	user, err := handlers.Auth.CurrentUser(r.Context(), auth.SessionCookieValue(r))
	if err != nil {
		httpapi.WriteProblem(w, "", http.StatusUnauthorized, "Unauthorized", "authentication required", "auth.session_required")
		return
	}
	account, err := handlers.AccountQuota.Execute(r.Context(), user.ID)
	if errors.Is(err, domain.ErrNotFound) {
		httpapi.WriteProblem(w, "", http.StatusUnauthorized, "Unauthorized", "authentication required", "auth.session_required")
		return
	}
	if err != nil {
		httpapi.WriteProblem(w, "", http.StatusInternalServerError, "Internal Server Error", "unexpected server error", "server.error")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response(account))
}

func response(account domain.Account) accountQuota {
	limits := account.Limits
	owned := make([]projectQuota, 0, len(account.OwnedProjects))
	for _, project := range account.OwnedProjects {
		owned = append(owned, projectQuota{ProjectID: project.ProjectID, Sources: sourcesOf(project.Sources,
			limits.ProjectBooks, limits.ProjectChapters, limits.ProjectCharacters)})
	}
	return accountQuota{
		Day:           account.Day.Format(time.DateOnly),
		ResetsAt:      account.ResetsAt.UTC().Format(time.RFC3339),
		ChatTurns:     counter{Used: account.ChatTurns, Limit: limits.ChatTurnsPerDay},
		ActiveTurns:   counter{Used: account.ActiveTurns, Limit: limits.ActiveTurns},
		Imports:       counter{Used: account.Imports, Limit: limits.ImportsPerDay},
		Images:        counter{Used: account.Images, Limit: limits.ImagesPerDay},
		ImageUploads:  counter{Used: account.ImageUploads, Limit: limits.ImageUploadsPerDay},
		ChapterDrafts: counter{Used: account.ChapterDrafts, Limit: limits.ChapterDraftsPerDay},
		DrawingTurns:  counter{Used: account.DrawingTurns, Limit: limits.DrawingTurnsPerDay},
		DraftTurns:    counter{Used: account.DraftTurns, Limit: limits.DraftTurnsPerDay},
		DraftRewrites: counter{Used: account.DraftRewrites, Limit: limits.DraftRewritesPerDay},
		Projects:      counter{Used: account.Projects, Limit: limits.Projects},
		Account: sourcesOf(account.Sources,
			limits.AccountBooks, limits.AccountChapters, limits.AccountCharacters),
		OwnedProjects: owned,
	}
}

func sourcesOf(value domain.Sources, books, chapters, characters int64) sources {
	return sources{
		Books:      counter{Used: value.Books, Limit: books},
		Chapters:   counter{Used: value.Chapters, Limit: chapters},
		Characters: counter{Used: value.Characters, Limit: characters},
	}
}
