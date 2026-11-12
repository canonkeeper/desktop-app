package researchchat

import (
	"errors"
	"fmt"
	"time"

	"canonkeeper/backend/internal/domain/agent"
)

var (
	ErrNotFound         = errors.New("research chat: not found")
	ErrForbidden        = errors.New("research chat: forbidden")
	ErrValidation       = errors.New("research chat: validation failed")
	ErrConflict         = errors.New("research chat: conflict")
	ErrNativeIncomplete = errors.New("research chat: native turn did not complete")
	ErrStaleSource      = errors.New("research chat: source is stale")
	ErrTitleEmpty       = errors.New("research chat: title is empty")
	ErrTitleTooLong     = errors.New("research chat: title is too long")
	ErrOutdatedAgent    = errors.New("research chat: chat belongs to an outdated agent generation")
	// ErrNotEditor: a tool of a turn writes, and the turn's author is not an editor or above of its project.
	ErrNotEditor = errors.New("research chat: the turn's author is not an editor")
	// ErrTurnEnded: a correction came after the turn stopped working (specs/2026-10-30-chat-steering.md R2.1).
	ErrTurnEnded = errors.New("research chat: the turn has ended")
	// ErrSteerRetry: the turn works but takes no correction right now, such as while it compacts (R2.2).
	ErrSteerRetry = errors.New("research chat: the turn takes no correction right now")
	// ErrSteerLimit: the author has sent MaxSteersPerTurn corrections into this turn (R1.8).
	ErrSteerLimit = errors.New("research chat: too many corrections in one turn")
)

// ToolNamespace is the namespace of the saga memory tools the chat agent calls.
const ToolNamespace = "saga"

type Actor struct {
	UserID string
}

type Scope string

const (
	ScopeProject Scope = "project"
	ScopeBook    Scope = "book"
)

// CurrentAgentGeneration is the agent tool set new chats are created with: 2 is the saga
// memory set. Chats of an older generation are read-only (R3.6).
const CurrentAgentGeneration = 2

type Session struct {
	ID              string    `json:"id"`
	ProjectID       string    `json:"projectId"`
	BookID          string    `json:"bookId,omitempty"`
	SourceView      string    `json:"sourceView"`
	Scope           Scope     `json:"scope"`
	Title           string    `json:"title"`
	CreatedAt       time.Time `json:"createdAt"`
	UpdatedAt       time.Time `json:"updatedAt"`
	LatestTurnID    string    `json:"latestTurnId,omitempty"`
	LatestTurnJobID string    `json:"latestTurnJobId,omitempty"`
	// LatestTurnStatus is the Job status of the latest turn; the chat list marks the chats
	// whose turn is queued, running or canceling.
	LatestTurnStatus string `json:"latestTurnStatus,omitempty"`
	// LatestTurnAt is when the latest turn was asked: the list of chats stands by it and opens the latest conversation
	// (specs/2026-11-11-ux4-after-answer.md AA2.1, AA3.1).
	LatestTurnAt    *time.Time       `json:"latestTurnAt,omitempty"`
	SavedAssignment *SavedAssignment `json:"savedAssignment"`
	Outdated        bool             `json:"outdated"`
	// Kind is research for a chat, drawing for the session of a helper of the drawing workshop
	// (specs/2026-11-02-image-workshop.md IW4.2): the chat operations neither list nor post into the latter.
	Kind            string `json:"-"`
	AgentGeneration int    `json:"-"`
	LifecycleEpoch  int64  `json:"-"`
	RootThreadID    string `json:"-"`
}

// ChapterLocation is the chapter a citation key names in a chat's text version.
type ChapterLocation struct {
	ChapterKey string `json:"chapterKey"`
	BookID     string `json:"bookId"`
	ChapterID  string `json:"chapterId"`
	BookTitle  string `json:"bookTitle"`
	Title      string `json:"title"`
}

type SavedAssignmentAction string

const SavedAssignmentStart SavedAssignmentAction = "start"

type SavedAssignment struct {
	Body             string                  `json:"body"`
	AvailableActions []SavedAssignmentAction `json:"availableActions"`
}

type Page struct {
	Limit  int
	Cursor string
}

type PageMeta struct {
	HasMore    bool    `json:"hasMore"`
	NextCursor *string `json:"nextCursor"`
}

type SessionPage struct {
	Data []Session `json:"data"`
	Meta PageMeta  `json:"meta"`
}

type WorkerClaim struct {
	ProjectID        string
	JobID            string
	ClaimIncarnation int64
}

type ContextSelection struct {
	BookTitle       string `json:"bookTitle"`
	ChapterPosition int    `json:"chapterPosition"`
	ChapterTitle    string `json:"chapterTitle"`
}

type TurnRun struct {
	ProjectID        string
	ChatID           string
	TurnID           string
	JobID            string
	ActorUserID      string
	ClaimIncarnation int64
	SourceView       string
	Scope            Scope
	BookID           string
	Question         string
	// WorkshopLines are the lines a helper's workshop adds for the model beside the author's words: the state of the
	// drawing or the draft (specs/2026-11-03-workshop-helpers-steering.md S2.1).
	WorkshopLines []string
	// DrawAfter says «Нарисовать» sent the turn (specs/2026-11-05-drawing-one-screen.md D3.7).
	DrawAfter bool
	// Button says a button of the page sent the question (specs/2026-11-11-ux9-writing-b.md WB3.3).
	Button          bool
	SelectedContext []ContextSelection
	// Images are the gallery images the author attached to the question
	// (specs/2026-10-29-saga-illustrations.md R15.9).
	Images []int
	// ImageTitles are the captions of those images, by number, for the anchors of the question (R15.19).
	ImageTitles    map[int]string
	RootThreadID   string
	ProviderTurnID string
	// Kind is the kind of the chat session, whose bundle runs the turn; DrawingID is the drawing of the workshop a
	// session of the kind drawing belongs to (specs/2026-11-02-image-workshop.md IW4.2).
	Kind      string
	DrawingID string
	// Attachment is the header of the chapter attached to the question (specs/2026-11-07-chat-attach-chapter.md CA3.1):
	// the input of the turn carries it, never the text.
	Attachment *AssignmentAttachment
}

type FinishResponse struct {
	ProjectID        string
	ChatID           string
	TurnID           string
	JobID            string
	ClaimIncarnation int64
	RootThreadID     string
	ProviderTurnID   string
	Response         string
	// Usage is what the root thread spent in the turn; empty when the app-server sent none.
	Usage agent.TurnUsage
}

type FinishFailure struct {
	ProjectID        string
	ChatID           string
	TurnID           string
	JobID            string
	ClaimIncarnation int64
	Code             string
	Usage            agent.TurnUsage
}

// Коды отказа хода чата в Job.error.code.
const (
	CodeTurnFailed            = "model.turn_failed"
	CodeTurnInterrupted       = "model.turn_interrupted"
	CodeUsageLimitExceeded    = "model.usage_limit_exceeded"
	CodeServerOverloaded      = "model.server_overloaded"
	CodeConnectionFailed      = "model.http_connection_failed"
	CodeContextWindowExceeded = "model.context_window_exceeded"
)

// TurnFailureCodes — закрытый набор кодов отказа хода и английская фраза каждого для журнала
// (jobs.Error.Message); FinishFailure принимает только эти коды.
var TurnFailureCodes = map[string]string{
	CodeTurnFailed:            "native research turn did not complete",
	CodeTurnInterrupted:       "native research turn was interrupted",
	CodeUsageLimitExceeded:    "model usage limit exceeded",
	CodeServerOverloaded:      "model server overloaded or temporarily unavailable",
	CodeConnectionFailed:      "connection to the model failed",
	CodeContextWindowExceeded: "model context window exceeded",
}

type TurnResult struct {
	RootThreadID   string
	ProviderTurnID string
	Response       string
}

type CancelTarget struct {
	// Flow is the app-server of the chat session's kind.
	Flow             agent.Flow
	ProjectID        string
	JobID            string
	ChatID           string
	RootThreadID     string
	ClaimIncarnation int64
	NeedsInterrupt   bool
	ActorUserID      string
	TargetReadable   bool
}

type DeleteTarget struct {
	Flow         agent.Flow
	ProjectID    string
	ChatID       string
	RootThreadID string
}

type ReadAuthority struct {
	ProjectID      string
	JobID          string
	ChatID         string
	RootThreadID   string
	ProviderTurnID string
	SourceView     string
	Scope          Scope
	BookID         string
	// ChatTurnID and ActorUserID name the chat turn and its author: a drawing of the turn belongs
	// to them (specs/2026-10-29-saga-illustrations.md R1.4, R1.7).
	ChatTurnID  string
	ActorUserID string
	// Moment says the turn is of the helper of a chapter in writing: it reads the saga only before the place of its
	// draft, an empty place being after the last chapter (specs/2026-11-06-writing-agent.md, WA3.3). The chat reads it all.
	Moment               bool
	MomentBookID         string
	MomentAfterChapterID string
}

// PhotoName is the anchor of an image attached to the author's words (specs/2026-10-29-saga-illustrations.md R15.17):
// its place in the list.
func PhotoName(position int) string {
	return fmt.Sprintf("фото %d", position)
}
