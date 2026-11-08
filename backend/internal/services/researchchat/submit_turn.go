package researchchat

import (
	"context"
	"encoding/json"
	"strings"
	"time"
	"unicode/utf8"

	"canonkeeper/backend/internal/auth"
	"canonkeeper/backend/internal/domain/agent"
	"canonkeeper/backend/internal/domain/chapterfile"
	researchdomain "canonkeeper/backend/internal/domain/researchchat"
	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/jobs"
)

type SubmitTurnInput struct {
	Question        string
	ContextLocators []governance.CanonicalChapterSourceLocatorV1
	// Chapter is the chapter of the author attached to the question (specs/2026-11-07-chat-attach-chapter.md CA2): a
	// file or a pasted text, read by the one reader of a chapter file.
	Chapter           *ChapterInput
	IdempotencyKey    string
	RequestMethod     string
	RequestPath       string
	SessionWriteProof auth.SessionWriteProof
}

// ChapterInput is the chapter a question carries: a file — its name and bytes — or a pasted text.
type ChapterInput struct {
	FileName string
	Content  []byte
	Text     string
	HasText  bool
}

// read is the step «разобрать главу вопроса» (CA2): exactly a file or a text, through chapterfile, into the attachment.
func (chapter ChapterInput) read() (researchdomain.Attachment, error) {
	var (
		document chapterfile.Document
		err      error
	)
	switch {
	case chapter.HasText && chapter.FileName == "" && chapter.Content == nil:
		document, err = chapterfile.ParseText(chapter.Text)
	case !chapter.HasText && strings.TrimSpace(chapter.FileName) != "":
		document, err = chapterfile.Parse(chapter.FileName, chapter.Content)
	default:
		return researchdomain.Attachment{}, researchdomain.ErrValidation
	}
	if err != nil {
		return researchdomain.Attachment{}, err
	}
	return researchdomain.NewAttachment(document, chapter.FileName), nil
}

// fingerprint is the hash of the attachment in the hash of the request: another chapter under the same key is another
// request (CA2.5).
func fingerprint(attachment *researchdomain.Attachment) string {
	if attachment == nil {
		return ""
	}
	encoded, _ := json.Marshal(struct {
		Title      string   `json:"title"`
		FileName   string   `json:"fileName"`
		Format     string   `json:"format"`
		Paragraphs []string `json:"paragraphs"`
	}{attachment.Title, attachment.FileName, attachment.Format, attachment.Paragraphs})
	return hashText(string(encoded))
}

type SubmitTurnCommand struct {
	ActorUserID string
	Session     researchdomain.Session
	Question    string
	// WorkshopLines go to the model beside the question and are kept with it (specs/2026-11-03-workshop-helpers-steering.md S2.3).
	WorkshopLines []string
	// Button says a button of the workshop opened the turn, not the author's own words.
	Button bool
	// DrawAfter says «Нарисовать» sent the turn: when it ends the bundle of its kind starts the drawing
	// (specs/2026-11-05-drawing-one-screen.md D3.7).
	DrawAfter       bool
	ContextLocators []governance.CanonicalChapterSourceLocatorV1
	Images          []int
	// Attachment is the chapter of the author attached to the question (specs/2026-11-07-chat-attach-chapter.md CA2):
	// the repository numbers it in its chat and keeps it with the turn.
	Attachment         *researchdomain.Attachment
	SessionWriteProof  auth.SessionWriteProof
	ScopeHash          string
	OperationID        string
	IdempotencyKeyHash string
	RequestHash        string
	CreatedAt          time.Time
}

type SubmitTurnRepository interface {
	GetSession(context.Context, researchdomain.Actor, string) (researchdomain.Session, error)
	SubmitTurn(context.Context, SubmitTurnCommand) (jobs.Job, error)
}

type SubmitTurn struct {
	repository SubmitTurnRepository
	clock      Clock
}

func NewSubmitTurn(repository SubmitTurnRepository, clock Clock) *SubmitTurn {
	if repository == nil || clock == nil {
		panic("research chat: submit-turn dependencies are required")
	}
	return &SubmitTurn{repository: repository, clock: clock}
}

func (useCase *SubmitTurn) Execute(ctx context.Context, actor researchdomain.Actor, chatID string, input SubmitTurnInput) (jobs.Job, error) {
	actor.UserID = strings.TrimSpace(actor.UserID)
	chatID = strings.TrimSpace(chatID)
	input.Question = strings.TrimSpace(input.Question)
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	input.RequestMethod = strings.ToUpper(strings.TrimSpace(input.RequestMethod))
	input.RequestPath = strings.TrimSpace(input.RequestPath)
	if ctx == nil || actor.UserID == "" || chatID == "" || input.RequestMethod == "" || input.RequestPath == "" ||
		!validIdempotencyKey(input.IdempotencyKey) || !input.SessionWriteProof.Valid() ||
		!validQuestion(input.Question) || !validContextLocators(input.ContextLocators) {
		return jobs.Job{}, researchdomain.ErrValidation
	}
	session, err := useCase.repository.GetSession(ctx, actor, chatID)
	if err != nil {
		return jobs.Job{}, err
	}
	// The chat posts into its chats only: the session of a helper of a drawing takes the turns of the workshop
	// (specs/2026-11-02-image-workshop.md IW4.2).
	if session.Scope != researchdomain.ScopeProject || session.BookID != "" || session.Kind != agent.SessionResearch {
		return jobs.Job{}, researchdomain.ErrNotFound
	}
	if session.AgentGeneration != researchdomain.CurrentAgentGeneration {
		return jobs.Job{}, researchdomain.ErrOutdatedAgent
	}
	var attachment *researchdomain.Attachment
	if input.Chapter != nil {
		read, err := input.Chapter.read()
		if err != nil {
			return jobs.Job{}, err
		}
		attachment = &read
	}
	for _, locator := range input.ContextLocators {
		if locator.ProjectID != session.ProjectID || string(locator.SourceView) != session.SourceView {
			return jobs.Job{}, researchdomain.ErrValidation
		}
	}
	request, err := json.Marshal(struct {
		ChatID          string                                       `json:"chatId"`
		LifecycleEpoch  int64                                        `json:"lifecycleEpoch"`
		Question        string                                       `json:"question"`
		ContextLocators []governance.CanonicalChapterSourceLocatorV1 `json:"contextLocators"`
		Attachment      string                                       `json:"attachment,omitempty"`
	}{chatID, session.LifecycleEpoch, input.Question, input.ContextLocators, fingerprint(attachment)})
	if err != nil {
		return jobs.Job{}, err
	}
	return useCase.repository.SubmitTurn(ctx, SubmitTurnCommand{
		ActorUserID: actor.UserID, Session: session, Question: input.Question,
		ContextLocators: append([]governance.CanonicalChapterSourceLocatorV1(nil), input.ContextLocators...),
		Attachment:      attachment, SessionWriteProof: input.SessionWriteProof,
		ScopeHash:   requestScopeHash(actor.UserID, input.RequestMethod, input.RequestPath),
		OperationID: submitTurnOperation, IdempotencyKeyHash: hashText(input.IdempotencyKey),
		RequestHash: hashText(string(request)), CreatedAt: useCase.clock.Now(),
	})
}

func validQuestion(value string) bool {
	return utf8.ValidString(value) && strings.TrimSpace(value) != "" && utf8.RuneCountInString(value) <= 8000
}

func validContextLocators(locators []governance.CanonicalChapterSourceLocatorV1) bool {
	if len(locators) > 20 {
		return false
	}
	seen := make(map[string]struct{}, len(locators))
	for _, locator := range locators {
		if locator.Validate() != nil {
			return false
		}
		encoded, err := json.Marshal(locator)
		if err != nil {
			return false
		}
		key := string(encoded)
		if _, duplicate := seen[key]; duplicate {
			return false
		}
		seen[key] = struct{}{}
	}
	return true
}
