// SPDX-License-Identifier: AGPL-3.0-only

package obsidian

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"canonkeeper/backend/internal/domain/authormaterials"
	domain "canonkeeper/backend/internal/domain/obsidian"
	service "canonkeeper/backend/internal/services/obsidian"

	"github.com/go-chi/chi/v5"
)

// The bounds of a request of a preview: a choice of a vault is a few folders; the folders and the notes left out may be
// many more.
const (
	maxPreviewBody    = 1 << 20
	maxChosenFolders  = 200
	maxLeftOut        = 2000
	maxRequestPathLen = 4096
)

type previewRequest struct {
	Books     []string `json:"books"`
	Materials []struct {
		Path string `json:"path"`
		Kind string `json:"kind"`
	} `json:"materials"`
	Exclude []string `json:"exclude"`
}

var errPreviewRequest = errors.New("obsidian: not a request of a preview")

// preview is previewObsidianImport (OB4): the choice in the body, never in the address — a path of a vault goes into no
// log of a request (OB0.4).
func (handlers Handlers) preview(response http.ResponseWriter, request *http.Request) {
	user, ok := handlers.writer(response, request)
	if !ok {
		return
	}
	choice, err := readPreview(request.Body)
	if err != nil {
		writeProblem(response, http.StatusBadRequest, "Bad Request", "validation failed", "request.validation", nil)
		return
	}
	preview, err := handlers.Preview.Execute(request.Context(), service.PreviewInput{
		ActorUserID: user.ID, ProjectID: chi.URLParam(request, "projectId"), Choice: choice,
	})
	if err != nil {
		writeError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, previewBodyOf(preview))
}

func readPreview(body io.Reader) (domain.Choice, error) {
	decoder := json.NewDecoder(io.LimitReader(body, maxPreviewBody))
	decoder.DisallowUnknownFields()
	var input previewRequest
	if err := decoder.Decode(&input); err != nil || decoder.More() {
		return domain.Choice{}, errPreviewRequest
	}
	if len(input.Books)+len(input.Materials) == 0 || len(input.Books) > maxChosenFolders || len(input.Materials) > maxChosenFolders ||
		len(input.Exclude) > maxLeftOut {
		return domain.Choice{}, errPreviewRequest
	}
	choice := domain.Choice{Books: input.Books, Exclude: input.Exclude}
	for _, material := range input.Materials {
		choice.Materials = append(choice.Materials, domain.MaterialFolder{Path: material.Path, Kind: authormaterials.Kind(material.Kind)})
	}
	for _, folder := range append(append(append([]string{}, input.Books...), input.Exclude...), materialPaths(choice)...) {
		if len(folder) > maxRequestPathLen {
			return domain.Choice{}, errPreviewRequest
		}
	}
	return choice, nil
}

func materialPaths(choice domain.Choice) []string {
	paths := make([]string, 0, len(choice.Materials))
	for _, material := range choice.Materials {
		paths = append(paths, material.Path)
	}
	return paths
}

type previewBody struct {
	Books     []bookPreviewBody `json:"books"`
	Materials materialsBody     `json:"materials"`
	Reading   readingBody       `json:"reading"`
}

type bookPreviewBody struct {
	Folder      string          `json:"folder"`
	Title       string          `json:"title"`
	Notes       []noteBody      `json:"notes"`
	Chapters    []chapterBody   `json:"chapters"`
	Characters  int             `json:"characters"`
	Refusal     string          `json:"refusal,omitempty"`
	Warnings    []warningBody   `json:"warnings"`
	Dropped     droppedBody     `json:"dropped"`
	Skipped     []skipBody      `json:"skipped"`
	State       folderStateBody `json:"state"`
	Fingerprint string          `json:"fingerprint"`
}

type noteBody struct {
	Path     string   `json:"path"`
	Title    string   `json:"title"`
	OrderBy  string   `json:"orderBy"`
	Chapters []string `json:"chapters"`
	InNote   bool     `json:"inNote"`
}

type chapterBody struct {
	Title      string `json:"title"`
	Characters int    `json:"characters"`
}

type warningBody struct {
	Code  string   `json:"code"`
	Notes []string `json:"notes"`
}

type droppedBody struct {
	Footnotes        int `json:"footnotes"`
	Embeds           int `json:"embeds"`
	Comments         int `json:"comments"`
	UnpairedComments int `json:"unpairedComments"`
	PluginBlocks     int `json:"pluginBlocks"`
	Tables           int `json:"tables"`
	FrontKeys        int `json:"frontKeys"`
}

type folderStateBody struct {
	Kind    string `json:"kind"`
	BookID  string `json:"bookId,omitempty"`
	Title   string `json:"title,omitempty"`
	Changed bool   `json:"changed"`
	InTrash bool   `json:"inTrash"`
	QueueID string `json:"queueId,omitempty"`
}

type materialsBody struct {
	Records []recordBody      `json:"records"`
	Skipped []skipBody        `json:"skipped"`
	Folders []folderPrintBody `json:"folders"`
}

// folderPrintBody is a folder of materials with the fingerprint of its records, as a load of the records names it
// (specs/2026-11-15-desk-obsidian-notes.md ON1.1).
type folderPrintBody struct {
	Folder      string `json:"folder"`
	Fingerprint string `json:"fingerprint"`
}

type recordBody struct {
	Path       string   `json:"path"`
	Folder     string   `json:"folder"`
	Name       string   `json:"name"`
	Kind       string   `json:"kind"`
	State      string   `json:"state"`
	Changed    bool     `json:"changed"`
	BookName   string   `json:"bookName,omitempty"`
	Aliases    []string `json:"aliases"`
	Properties int      `json:"properties"`
	Warnings   []string `json:"warnings"`
	Cut        int      `json:"cut"`
	Left       int      `json:"left"`
	// Seen is what the author sees of the state: a load names it back (specs/2026-11-15-desk-obsidian-notes.md ON1.3).
	Seen string `json:"seen"`
	// Replaceable says, for other_source, that a decision may replace the record that keeps the name: not when another note
	// of the load gets it (ON1.4).
	Replaceable bool `json:"replaceable"`
	// HeldBy is, for other_source, the note of the load that has the name — gets it, or keeps it by its record —; none when a
	// record no note of the load owns keeps it.
	HeldBy string `json:"heldBy,omitempty"`
}

type readingBody struct {
	NewChapters   int    `json:"newChapters"`
	NewCharacters int    `json:"newCharacters"`
	Helper        string `json:"helper"`
	Consent       bool   `json:"consent"`
}

func previewBodyOf(preview service.Preview) previewBody {
	body := previewBody{
		Books: []bookPreviewBody{},
		Materials: materialsBody{
			Records: []recordBody{}, Skipped: skippedBody(preview.Materials.Skipped), Folders: []folderPrintBody{},
		},
		Reading: readingBody{
			NewChapters: preview.NewChapters, NewCharacters: preview.NewCharacters, Helper: string(preview.Reading.Helper),
			Consent: preview.Reading.Consent,
		},
	}
	for _, book := range preview.Books {
		body.Books = append(body.Books, bookPreviewBodyOf(book))
	}
	for _, record := range preview.Materials.Records {
		item := recordBody{
			Path: record.Path, Folder: record.Folder, Name: record.Draft.Name, Kind: string(record.Draft.Kind), State: record.State,
			Changed: record.Changed, BookName: record.BookName, Aliases: []string{}, Properties: len(record.Draft.Properties),
			Warnings: []string{}, Cut: record.Cut, Left: record.Left, Seen: record.Seen,
			Replaceable: record.State == domain.StateOtherSource && record.Target != "", HeldBy: record.HeldBy,
		}
		item.Aliases = append(item.Aliases, record.Aliases...)
		for _, warning := range record.Warnings {
			item.Warnings = append(item.Warnings, string(warning))
		}
		body.Materials.Records = append(body.Materials.Records, item)
	}
	for _, folder := range preview.Materials.Folders {
		body.Materials.Folders = append(body.Materials.Folders, folderPrintBody(folder))
	}
	return body
}

// bookPreviewBodyOf is a folder of a book as a preview reads it: a preview of a load and of an update
// (specs/2026-11-14-desk-obsidian-books.md OK2.1).
func bookPreviewBodyOf(book service.BookPreview) bookPreviewBody {
	item := bookPreviewBody{
		Folder: book.Folder, Title: book.Title, Notes: []noteBody{}, Chapters: []chapterBody{}, Characters: book.Characters,
		Refusal: book.Refusal, Warnings: []warningBody{}, Skipped: skippedBody(book.Skipped), Fingerprint: book.Fingerprint,
		Dropped: droppedBody{
			Footnotes: book.Dropped.Footnotes, Embeds: book.Dropped.Embeds, Comments: book.Dropped.Comments,
			UnpairedComments: book.Dropped.UnpairedComments, PluginBlocks: book.Dropped.PluginBlocks, Tables: book.Dropped.Tables,
			FrontKeys: book.Dropped.FrontKeys,
		},
		State: folderStateBody{Kind: "new"},
	}
	switch {
	case book.State.Linked:
		item.State = folderStateBody{Kind: "linked", BookID: book.State.BookID, Title: book.State.Title, Changed: book.State.Changed, InTrash: book.State.InTrash}
	case book.State.Queued:
		// specs/2026-11-14-desk-obsidian-books.md OK0.3: the volume of the folder is in work of its queue.
		item.State = folderStateBody{Kind: "queued", QueueID: book.State.QueueID}
	}
	for _, note := range book.Notes {
		chapters := note.Chapters
		if chapters == nil {
			chapters = []string{}
		}
		item.Notes = append(item.Notes, noteBody{Path: note.Path, Title: note.Title, OrderBy: string(note.OrderBy), Chapters: chapters, InNote: note.InNote})
	}
	for _, chapter := range book.Chapters {
		item.Chapters = append(item.Chapters, chapterBody{Title: chapter.Title, Characters: chapter.Characters})
	}
	for _, warning := range book.Warnings {
		notes := warning.Notes
		if notes == nil {
			notes = []string{}
		}
		item.Warnings = append(item.Warnings, warningBody{Code: string(warning.Code), Notes: notes})
	}
	return item
}
