package researchchat

import (
	"encoding/json"
	"io"
	"strings"
)

// Assignment is the application-owned input stored in the native chat turn.
type Assignment struct {
	Question        string             `json:"question"`
	ProjectRef      string             `json:"projectRef"`
	BookRef         string             `json:"bookRef,omitempty"`
	SourceView      string             `json:"sourceView"`
	Scope           Scope              `json:"scope"`
	SelectedContext []ContextSelection `json:"selectedContext,omitempty"`
	// AttachedImages are the numbers of the gallery images the author attached to the question
	// (specs/2026-10-29-saga-illustrations.md R15.9); the model looks at them with saga.image.
	AttachedImages []int `json:"attachedImages,omitempty"`
	// AttachedPhotos say what the anchors of the question mean (R15.19): «фото 1 — изображение 81
	// (подпись)», one per attached image in order.
	AttachedPhotos []string `json:"attachedPhotos,omitempty"`
	// ChosenReferences are only in the assignments of old threads, which the strict decoding of the timeline still
	// reads: a turn of the chat no longer draws or takes chosen images (specs/2026-11-02-image-workshop.md IW7.1).
	// They were the images the author chose for the drawings of the turn
	// (specs/2026-10-29-saga-illustrations.md R25.1): the backend passes exactly them, whatever the model names.
	ChosenReferences []int `json:"chosenReferences,omitempty"`
	// Correction marks what the author sent into a turn already running (specs/2026-10-30-chat-steering.md
	// R1.4): Question is then the text of the correction, and the question of the turn stays its first
	// user item. FirstPhoto is the anchor number of the first attached image of a correction: the
	// anchors go on through the turn (R4.2).
	Correction bool `json:"correction,omitempty"`
	FirstPhoto int  `json:"firstPhoto,omitempty"`
	// WorkshopLines are what the workshop of a helper adds for the model beside the question — the
	// state of the drawing or the draft (specs/2026-11-03-workshop-helpers-steering.md S2.1). They are not the
	// author's words: the timeline never shows them.
	WorkshopLines []string `json:"workshop,omitempty"`
	// Attachment is the header of the chapter of the author attached to the question
	// (specs/2026-11-07-chat-attach-chapter.md CA1.4): its number in the chat, title, paragraphs and words, signed. Its
	// text is never here: the model reads it with saga.attachment.
	Attachment *AssignmentAttachment `json:"attachment,omitempty"`
}

// PublicQuestion is what the timeline shows of a question or a correction: its text and the images
// attached to it, with the anchor number of the first of them on a correction, and the chapter attached to a question.
type PublicQuestion struct {
	Text       string
	Images     []int
	Correction bool
	FirstPhoto int
	Attachment *PublicAttachment
}

// PublicAssignmentQuestion exposes only the actor's question and the images attached to it, never
// the wrapper or selected context. Other native inputs are not public chat questions.
func PublicAssignmentQuestion(input string) (PublicQuestion, bool) {
	decoder := json.NewDecoder(strings.NewReader(input))
	decoder.DisallowUnknownFields()
	var assignment Assignment
	if decoder.Decode(&assignment) != nil {
		return PublicQuestion{}, false
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF || strings.TrimSpace(assignment.Question) == "" ||
		assignment.ProjectRef != "project" || (assignment.SourceView != "draft" && assignment.SourceView != "published") ||
		!ValidAttachedImages(assignment.AttachedImages) {
		return PublicQuestion{}, false
	}
	// A chapter is attached to a question only: a correction carries none (CA1.5).
	if assignment.Attachment != nil && (assignment.Correction || !assignment.Attachment.valid()) {
		return PublicQuestion{}, false
	}
	if assignment.Scope == ScopeProject && assignment.BookRef == "" || assignment.Scope == ScopeBook && assignment.BookRef == "book" {
		question := PublicQuestion{
			Text: assignment.Question, Images: assignment.AttachedImages,
			Correction: assignment.Correction, FirstPhoto: assignment.FirstPhoto,
		}
		if header := assignment.Attachment; header != nil {
			question.Attachment = &PublicAttachment{Number: header.Number, Title: header.Title, Words: header.Words}
		}
		return question, true
	}
	return PublicQuestion{}, false
}

// MaxAttachedImages bounds the images of one question (R15.9, R20.1).
const MaxAttachedImages = 8

// MaxFirstPhoto bounds the anchor a correction may start from: four images on the question and on
// each of MaxSteersPerTurn corrections (specs/2026-10-30-chat-steering.md R1.8, R4.2).
const MaxFirstPhoto = MaxAttachedImages*(MaxSteersPerTurn+1) + 1

// MaxSteersPerTurn bounds the corrections one author sends into one turn (R1.8): a question must not
// feed a turn without end.
const MaxSteersPerTurn = 20

// ValidAttachedImages accepts at most MaxAttachedImages distinct image numbers.
func ValidAttachedImages(numbers []int) bool {
	if len(numbers) > MaxAttachedImages {
		return false
	}
	seen := make(map[int]struct{}, len(numbers))
	for _, number := range numbers {
		if _, duplicate := seen[number]; duplicate || number < 1 {
			return false
		}
		seen[number] = struct{}{}
	}
	return true
}
