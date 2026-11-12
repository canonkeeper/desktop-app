package jobs

import (
	"canonkeeper/backend/internal/governance"
)

type Subject interface {
	jobSubject()
	Kind() Kind
	Project() string
}

type ImportSubject struct {
	Type              Kind   `json:"type"`
	ID                string `json:"id"`
	ProjectID         string `json:"projectId"`
	ExpectedBookOrder int    `json:"expectedBookOrder,omitempty"`
	// BookID is the book an update_book or update_preview import reads; it has no expected order.
	BookID string `json:"bookId,omitempty"`
	// Preview marks an update_preview import: it writes nothing, and the shelf does not show it as work on the
	// book (specs/2026-10-27-update-book-from-file.md, R6.7).
	Preview           bool   `json:"preview,omitempty"`
	ImportQueueID     string `json:"importQueueId,omitempty"`
	ImportQueueItemID string `json:"importQueueItemId,omitempty"`
}

func (ImportSubject) jobSubject()       {}
func (s ImportSubject) Kind() Kind      { return s.Type }
func (s ImportSubject) Project() string { return s.ProjectID }

type CanonTextIndexSubject struct {
	Type            Kind                  `json:"type"`
	ProjectID       string                `json:"projectId"`
	SourceView      governance.SourceView `json:"sourceView"`
	CatalogRevision uint64                `json:"catalogRevision"`
}

func (CanonTextIndexSubject) jobSubject()       {}
func (s CanonTextIndexSubject) Kind() Kind      { return s.Type }
func (s CanonTextIndexSubject) Project() string { return s.ProjectID }

// ChapterCardsSubject is the cards Job of one project text version (UC2).
type ChapterCardsSubject struct {
	Type       Kind                  `json:"type"`
	ProjectID  string                `json:"projectId"`
	SourceView governance.SourceView `json:"sourceView"`
}

func (ChapterCardsSubject) jobSubject()       {}
func (s ChapterCardsSubject) Kind() Kind      { return s.Type }
func (s ChapterCardsSubject) Project() string { return s.ProjectID }

// SagaLayerSubject is the thread layer Job of one project, built from the cards of a text version
// (specs/2026-11-01-writer-assistant-index.md, UC2).
type SagaLayerSubject struct {
	Type       Kind                  `json:"type"`
	ProjectID  string                `json:"projectId"`
	SourceView governance.SourceView `json:"sourceView"`
}

func (SagaLayerSubject) jobSubject()       {}
func (s SagaLayerSubject) Kind() Kind      { return s.Type }
func (s SagaLayerSubject) Project() string { return s.ProjectID }

// ChapterDraftWrite is the earlier writing of a chapter from the author's draft (specs/2026-11-02-writer-workshop.md, W3),
// removed by specs/2026-11-06-writing-agent.md S8. Its rows stay in the history of the project, sealed (Erased): nothing
// starts or runs this mode.
const ChapterDraftWrite = "write"

// The modes of the writing agent (specs/2026-11-06-writing-agent.md, WA5, WA6, WA7): the scenes of an approved plan,
// 2–3 variants of a selection, a check of the seams of a text taken from a version, a check of the chapter, and the
// proofreading of the chapter (specs/2026-11-07-writing-proofread.md, PR3).
const (
	ChapterDraftScenes    = "scenes"
	ChapterDraftSelection = "selection"
	ChapterDraftSeams     = "seams"
	ChapterDraftCheck     = "check"
	ChapterDraftProofread = "proofread"
)

// ChapterDraftPlace names a place of the working text: an exact quote, or the first and last phrases of a long one.
type ChapterDraftPlace struct {
	Quote string `json:"quote,omitempty"`
	From  string `json:"from,omitempty"`
	To    string `json:"to,omitempty"`
}

// The tail of a run of scenes after its place: kept after the new scenes, or replaced by them (WA5.7).
const (
	ChapterDraftTailKeep    = "keep"
	ChapterDraftTailReplace = "replace"
)

// ChapterDraftSubject is the Job of the writing agent over a draft of the section «Письмо»
// (specs/2026-11-06-writing-agent.md, WA5–WA7): the revision it starts on, the approved version of the plan and its
// scenes, where the scenes go and what of the text after that place stays, the selection with the author's words about
// it, and the seams of a text taken from a version as offsets in the working text. The place and the words are the
// author's; they never leave the backend: Public shows the rest. BookID and AfterChapterID say where the chapter goes:
// what comes after that place is not read.
//
// A sealed subject (Erased) keeps nothing of the author's text, only who and where: so does every Job of a deleted
// draft (the query EraseChapterDraftJobText) and every Job of the earlier writing (mode write) since the migration of S8.
type ChapterDraftSubject struct {
	Type           Kind               `json:"type"`
	ProjectID      string             `json:"projectId"`
	DraftID        string             `json:"draftId,omitempty"`
	Mode           string             `json:"mode"`
	BookID         *string            `json:"bookId,omitempty"`
	AfterChapterID *string            `json:"afterChapterId,omitempty"`
	Revision       int64              `json:"revision,omitempty"`
	PlanNumber     int                `json:"planNumber,omitempty"`
	Scenes         []int              `json:"scenes,omitempty"`
	From           *ChapterDraftPlace `json:"from,omitempty"`
	Tail           string             `json:"tail,omitempty"`
	Place          *ChapterDraftPlace `json:"place,omitempty"`
	Instruction    string             `json:"instruction,omitempty"`
	Seams          []int              `json:"seams,omitempty"`
	// Release are lines of the author the scenes of this run need not keep word for word: «Писать сцену без этой фразы»
	// after a writing that kept losing them (specs/2026-11-11-ux9-writing-a.md WA2.4). Text of the author: erased with it.
	Release []string `json:"release,omitempty"`
	Erased  bool     `json:"erased,omitempty"`
}

func (ChapterDraftSubject) jobSubject()       {}
func (s ChapterDraftSubject) Kind() Kind      { return s.Type }
func (s ChapterDraftSubject) Project() string { return s.ProjectID }

// Public is the subject in the job API: the kind of work, not the text or the draft.
func (s ChapterDraftSubject) Public() ChapterDraftSubject {
	return ChapterDraftSubject{Type: s.Type, ProjectID: s.ProjectID, Mode: s.Mode}
}

// AuthorMaterialsImportSubject is the Job mapping the sections of a file of the author. It names his import and holds
// nothing of the file: the text lives in the row of the import and goes when he answers it (specs/2026-11-04-saga-map.md, R9.2).
type AuthorMaterialsImportSubject struct {
	Type      Kind   `json:"type"`
	ProjectID string `json:"projectId"`
	ImportID  string `json:"importId"`
}

func (AuthorMaterialsImportSubject) jobSubject()       {}
func (s AuthorMaterialsImportSubject) Kind() Kind      { return s.Type }
func (s AuthorMaterialsImportSubject) Project() string { return s.ProjectID }

type ResearchChatTurnSubject struct {
	Type       Kind                  `json:"type"`
	ProjectID  string                `json:"projectId"`
	ChatID     string                `json:"chatId"`
	TurnID     string                `json:"turnId"`
	SourceView governance.SourceView `json:"sourceView"`
	Scope      string                `json:"scope"`
	BookID     *string               `json:"bookId,omitempty"`
}

func (ResearchChatTurnSubject) jobSubject()       {}
func (s ResearchChatTurnSubject) Kind() Kind      { return s.Type }
func (s ResearchChatTurnSubject) Project() string { return s.ProjectID }

// IllustrationSubject is the Job drawing one image of the saga gallery
// (specs/2026-10-29-saga-illustrations.md R1.3), or one layer of a region edit of it (LayerID,
// R8.15).
type IllustrationSubject struct {
	Type      Kind   `json:"type"`
	ProjectID string `json:"projectId"`
	ImageID   string `json:"imageId"`
	LayerID   string `json:"layerId,omitempty"`
}

func (IllustrationSubject) jobSubject()       {}
func (s IllustrationSubject) Kind() Kind      { return s.Type }
func (s IllustrationSubject) Project() string { return s.ProjectID }
