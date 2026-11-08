package jobs

import (
	"strings"
	"unicode/utf8"

	"canonkeeper/backend/internal/governance"
)

type Result interface {
	jobResult()
	Kind() Kind
}

type ImportJobResult struct {
	Type           Kind   `json:"type"`
	BookID         string `json:"bookId"`
	ChapterCount   int    `json:"chapterCount"`
	ParagraphCount int    `json:"paragraphCount"`
	ImportMode     string `json:"importMode"`
	SourceFileName string `json:"sourceFileName"`
	// An update_book or update_preview import counts how the chapters of the file met the book; new_book has
	// none. ChangedChapters are chapters with other words; Punctuation and Renamed came with the plan of
	// specs/2026-10-27-update-book-from-file.md (UC3, UC4) and are absent from older results.
	UnchangedChapters   *int `json:"unchangedChapters,omitempty"`
	ChangedChapters     *int `json:"changedChapters,omitempty"`
	AddedChapters       *int `json:"addedChapters,omitempty"`
	RemovedChapters     *int `json:"removedChapters,omitempty"`
	PunctuationChapters *int `json:"punctuationChapters,omitempty"`
	RenamedChapters     *int `json:"renamedChapters,omitempty"`
	// Preview is what an update_preview import would do (R6.2); only its creator reads its places (R6.3).
	Preview *ImportPreview `json:"preview,omitempty"`
}

// The kinds of a chapter in a preview (R6.2).
const (
	ImportPreviewWords       = "words"
	ImportPreviewPunctuation = "punctuation"
	ImportPreviewRenamed     = "renamed"
	ImportPreviewAdded       = "added"
	ImportPreviewRemoved     = "removed"
)

// Bounds of a preview: places of at most this many chapters are kept, the list itself is cut beyond its
// limit and MoreChapters counts the rest.
const (
	MaxImportPreviewChapters    = 300
	MaxImportPreviewPlaceRunes  = 160
	maxImportPreviewPlaces      = 2
	MaxImportPreviewTitleLength = 180
)

// ImportPreview lists the chapters an update would change: how many need a new card, whether the threads of
// the saga would be rebuilt, the chapters with a change in the order of the book after the update and the
// removed ones after them.
type ImportPreview struct {
	NewCards     int                    `json:"newCards"`
	Threads      bool                   `json:"threads"`
	Chapters     []ImportPreviewChapter `json:"chapters"`
	MoreChapters int                    `json:"moreChapters,omitempty"`
}

// ImportPreviewChapter is one changed chapter: its place after the update (a removed one, its place now), its
// title, the title it had when renamed, the kind of change and up to two places «было → стало».
type ImportPreviewChapter struct {
	Order         int                  `json:"order"`
	Title         string               `json:"title"`
	PreviousTitle string               `json:"previousTitle,omitempty"`
	Change        string               `json:"change"`
	Places        []ImportPreviewPlace `json:"places,omitempty"`
}

// ImportPreviewPlace is a short place of a changed chapter before and after the update.
type ImportPreviewPlace struct {
	Before string `json:"before"`
	After  string `json:"after"`
}

func (r ImportJobResult) validCounters() bool {
	update := r.ImportMode == "update_book" || r.ImportMode == "update_preview"
	for _, counter := range []*int{r.UnchangedChapters, r.ChangedChapters, r.AddedChapters, r.RemovedChapters} {
		if (counter == nil) == update || counter != nil && *counter < 0 {
			return false
		}
	}
	for _, counter := range []*int{r.PunctuationChapters, r.RenamedChapters} {
		if counter != nil && (!update || *counter < 0) {
			return false
		}
	}
	if (r.Preview != nil) != (r.ImportMode == "update_preview") || r.Preview != nil && !r.Preview.valid() {
		return false
	}
	return r.ImportMode == "new_book" || update
}

func (preview ImportPreview) valid() bool {
	if preview.NewCards < 0 || preview.MoreChapters < 0 || len(preview.Chapters) > MaxImportPreviewChapters {
		return false
	}
	for _, chapter := range preview.Chapters {
		switch chapter.Change {
		case ImportPreviewWords, ImportPreviewPunctuation, ImportPreviewRenamed, ImportPreviewAdded, ImportPreviewRemoved:
		default:
			return false
		}
		if chapter.Order < 1 || !boundedRunes(chapter.Title, MaxImportPreviewTitleLength) || len(chapter.Places) > maxImportPreviewPlaces ||
			chapter.PreviousTitle != "" && !boundedRunes(chapter.PreviousTitle, MaxImportPreviewTitleLength) {
			return false
		}
		for _, place := range chapter.Places {
			if !utf8.ValidString(place.Before) || !utf8.ValidString(place.After) ||
				utf8.RuneCountInString(place.Before) > MaxImportPreviewPlaceRunes || utf8.RuneCountInString(place.After) > MaxImportPreviewPlaceRunes {
				return false
			}
		}
	}
	return true
}

func boundedRunes(value string, maxRunes int) bool {
	return strings.TrimSpace(value) != "" && utf8.ValidString(value) && utf8.RuneCountInString(value) <= maxRunes
}

// WithoutPlaces is the result as a member other than its creator reads it: the places «было → стало» quote the
// file of the author who loaded it (R6.3).
func (r ImportJobResult) WithoutPlaces() ImportJobResult {
	if r.Preview == nil {
		return r
	}
	preview := *r.Preview
	preview.Chapters = make([]ImportPreviewChapter, len(r.Preview.Chapters))
	for i, chapter := range r.Preview.Chapters {
		chapter.Places = nil
		preview.Chapters[i] = chapter
	}
	r.Preview = &preview
	return r
}

func (ImportJobResult) jobResult()   {}
func (r ImportJobResult) Kind() Kind { return r.Type }

type CanonTextIndexResult struct {
	Type         Kind                  `json:"type"`
	SourceView   governance.SourceView `json:"sourceView"`
	TextCoverage TextCoverage          `json:"textCoverage"`
}

func (CanonTextIndexResult) jobResult()   {}
func (r CanonTextIndexResult) Kind() Kind { return r.Type }

// ChapterCardsResult counts the chapters of the text version and the cards this run built.
type ChapterCardsResult struct {
	Type       Kind                  `json:"type"`
	SourceView governance.SourceView `json:"sourceView"`
	Chapters   int                   `json:"chapters"`
	Built      int                   `json:"built"`
}

func (ChapterCardsResult) jobResult()   {}
func (r ChapterCardsResult) Kind() Kind { return r.Type }

// SagaLayerResult counts what a layer build holds: the chapters it read, the threads of its registry,
// the books it asked the model about and the records that kept their words because their checks failed.
type SagaLayerResult struct {
	Type       Kind                  `json:"type"`
	SourceView governance.SourceView `json:"sourceView"`
	Chapters   int                   `json:"chapters"`
	Threads    int                   `json:"threads"`
	BooksBuilt int                   `json:"booksBuilt"`
	Unverified int                   `json:"unverified"`
}

func (SagaLayerResult) jobResult()   {}
func (r SagaLayerResult) Kind() Kind { return r.Type }

// ChapterDraftSource names a chapter text whose paragraphs entered a call of a chapter_draft Job.
type ChapterDraftSource struct {
	ChapterID string `json:"chapterId"`
	Revision  int64  `json:"revision"`
}

// ChapterDraftResult is what a chapter_draft Job of the writing agent has made while it runs, so a retry goes on from it
// (specs/2026-11-06-writing-agent.md, WA5): its phase, how many of its steps are done of how many, the scenes written so
// far — a retry goes on from the first one missing —, the pack it writes by and the versions it kept. Chapters names
// what the calls read. The text is in the draft, and the row of a finished Job keeps only Sealed. Only the backend reads
// it: Public shows the rest.
type ChapterDraftResult struct {
	Type           Kind                 `json:"type"`
	Mode           string               `json:"mode"`
	Phase          string               `json:"phase,omitempty"`
	Total          int                  `json:"total,omitempty"`
	Chapters       []ChapterDraftSource `json:"chapters,omitempty"`
	Done           int                  `json:"done,omitempty"`
	SceneRuns      []ChapterDraftRun    `json:"sceneRuns,omitempty"`
	Pack           string               `json:"pack,omitempty"`
	BeforeVersion  int                  `json:"beforeVersion,omitempty"`
	WrittenVersion int                  `json:"writtenVersion,omitempty"`
	// Findings are the corrections a proofreading found in the pieces it read, kept while it runs so a retry goes on
	// from the first piece not read (specs/2026-11-07-writing-proofread.md, PR3.9); Sealed drops them.
	Findings []ChapterDraftFinding `json:"findings,omitempty"`
}

// ChapterDraftFinding is one correction of a proofreading: the fragment of the working text, what stands there
// instead, the category and why, and where the fragment stands.
type ChapterDraftFinding struct {
	Quote       string `json:"quote"`
	Replacement string `json:"replacement"`
	Category    string `json:"category"`
	Problem     string `json:"problem,omitempty"`
	Start       int    `json:"start"`
	End         int    `json:"end"`
}

// ChapterDraftRun is one scene the agent wrote: its number, its words, how many calls it took, and its first and
// last phrases in the working text.
type ChapterDraftRun struct {
	Scene    int    `json:"scene"`
	Words    int    `json:"words"`
	Attempts int    `json:"attempts"`
	From     string `json:"from,omitempty"`
	To       string `json:"to,omitempty"`
	// Text is the scene as written while the Job runs, so a retry joins the same scenes; Sealed drops it.
	Text string `json:"text,omitempty"`
}

// The phases of a Job of the writing agent: the pack, the scenes, the notes of a selection or of seams, the check, the
// proofreading.
const (
	ChapterDraftPhasePack   = "pack"
	ChapterDraftPhaseScenes = "scenes"
	ChapterDraftPhaseNotes  = "notes"
	ChapterDraftPhaseCheck  = "check"
	// ChapterDraftPhaseProofread is a proofreading reading its pieces: Done of Total.
	ChapterDraftPhaseProofread = "proofread"
)

// Sealed is what stays in the row of a finished Job: nothing of the text. Deleting a draft leaves the same in the rows
// of its runs (the query EraseChapterDraftJobText).
func (r ChapterDraftResult) Sealed() ChapterDraftResult {
	return ChapterDraftResult{Type: r.Type, Mode: r.Mode, Total: r.Total, Chapters: r.Chapters, Done: r.Done, Pack: r.Pack,
		BeforeVersion: r.BeforeVersion, WrittenVersion: r.WrittenVersion}
}

func (ChapterDraftResult) jobResult()   {}
func (r ChapterDraftResult) Kind() Kind { return r.Type }

// Public is the result in the job API: the kind of work, not the text.
func (r ChapterDraftResult) Public() ChapterDraftResult {
	return ChapterDraftResult{Type: r.Type, Mode: r.Mode}
}

// AuthorMaterialsImportResult is how many candidates an import found; they are in the row of the import.
type AuthorMaterialsImportResult struct {
	Type       Kind `json:"type"`
	Candidates int  `json:"candidates"`
}

func (AuthorMaterialsImportResult) jobResult()   {}
func (r AuthorMaterialsImportResult) Kind() Kind { return r.Type }

type ResearchChatTurnResult struct {
	Type     Kind   `json:"type"`
	ChatID   string `json:"chatId"`
	TurnID   string `json:"turnId"`
	Response string `json:"response"`
}

func (ResearchChatTurnResult) jobResult()   {}
func (r ResearchChatTurnResult) Kind() Kind { return r.Type }

// IllustrationResult names the drawing the Job made ready.
type IllustrationResult struct {
	Type        Kind `json:"type"`
	ImageNumber int  `json:"imageNumber"`
}

func (IllustrationResult) jobResult()   {}
func (r IllustrationResult) Kind() Kind { return r.Type }
