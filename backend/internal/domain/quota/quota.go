// Package quota holds a writer's limits: questions and uploads per Moscow day, questions at
// once, projects and the text of their books (specs/2026-10-28-writer-quotas-and-fair-queue.md UC1).
package quota

import (
	"errors"
	"fmt"
	"time"
)

// Key names one limit; it is also the column of writer_quota_overrides and the operator key.
type Key string

const (
	KeyChatTurnsPerDay   Key = "chat_turns_per_day"
	KeyActiveTurns       Key = "active_turns"
	KeyImportsPerDay     Key = "imports_per_day"
	KeyProjects          Key = "projects"
	KeyProjectBooks      Key = "project_books"
	KeyProjectChapters   Key = "project_chapters"
	KeyProjectCharacters Key = "project_characters"
	KeyAccountBooks      Key = "account_books"
	KeyAccountChapters   Key = "account_chapters"
	KeyAccountCharacters Key = "account_characters"
	// KeyImagesPerDay counts the drawings of the saga gallery a writer starts in a Moscow day
	// (specs/2026-10-29-saga-illustrations.md R5.1).
	KeyImagesPerDay Key = "images_per_day"
	// KeyImageUploadsPerDay counts the images a writer uploads into the saga gallery in a Moscow
	// day (specs/2026-10-29-saga-illustrations.md R15.7).
	KeyImageUploadsPerDay Key = "image_uploads_per_day"
	// KeyChapterDraftsPerDay counts the runs of a plan or a chapter a writer starts in a Moscow day
	// (specs/2026-11-01-writer-assistant-index.md, R7.6).
	KeyChapterDraftsPerDay Key = "chapter_drafts_per_day"
	// KeyDrawingTurnsPerDay counts the turns of the helper of the drawing workshop a writer starts in a Moscow
	// day (specs/2026-11-02-image-workshop.md IW8.2); with the questions of the chat they keep active_turns.
	KeyDrawingTurnsPerDay Key = "drawing_turns_per_day"
	// KeyDraftTurnsPerDay counts the turns of the helper of the chapter workshop a writer starts in a Moscow day
	// (specs/2026-11-02-writer-workshop.md W1.5); with the questions of the chat they keep active_turns.
	KeyDraftTurnsPerDay Key = "draft_turns_per_day"
	// KeyDraftRewritesPerDay counts the rewrites of a selection and the checks of the seams of the writing agent a writer
	// starts in a Moscow day (specs/2026-11-06-writing-agent.md, WA5.13): cheap and frequent, so apart from the chapters.
	KeyDraftRewritesPerDay Key = "draft_rewrites_per_day"
)

// Keys lists every limit in the order the operator command and the reports show them.
func Keys() []Key {
	return []Key{
		KeyChatTurnsPerDay, KeyActiveTurns, KeyImportsPerDay, KeyProjects,
		KeyProjectBooks, KeyProjectChapters, KeyProjectCharacters,
		KeyAccountBooks, KeyAccountChapters, KeyAccountCharacters, KeyImagesPerDay, KeyImageUploadsPerDay, KeyChapterDraftsPerDay,
		KeyDrawingTurnsPerDay, KeyDraftTurnsPerDay, KeyDraftRewritesPerDay,
	}
}

// ParseKey accepts only a known limit.
func ParseKey(value string) (Key, bool) {
	for _, key := range Keys() {
		if string(key) == value {
			return key, true
		}
	}
	return "", false
}

// Limits are the limits in force for one writer.
type Limits struct {
	ChatTurnsPerDay     int64
	ActiveTurns         int64
	ImportsPerDay       int64
	Projects            int64
	ProjectBooks        int64
	ProjectChapters     int64
	ProjectCharacters   int64
	AccountBooks        int64
	AccountChapters     int64
	AccountCharacters   int64
	ImagesPerDay        int64
	ImageUploadsPerDay  int64
	ChapterDraftsPerDay int64
	DrawingTurnsPerDay  int64
	DraftTurnsPerDay    int64
	DraftRewritesPerDay int64
}

// Defaults are the limits of every writer without an override; changing them is a release.
func Defaults() Limits {
	return Limits{
		ChatTurnsPerDay:     30,
		ActiveTurns:         2,
		ImportsPerDay:       30,
		Projects:            3,
		ProjectBooks:        60,
		ProjectChapters:     3000,
		ProjectCharacters:   15_000_000,
		AccountBooks:        100,
		AccountChapters:     5000,
		AccountCharacters:   25_000_000,
		ImagesPerDay:        30,
		ImageUploadsPerDay:  30,
		ChapterDraftsPerDay: 20,
		DrawingTurnsPerDay:  60,
		DraftTurnsPerDay:    60,
		DraftRewritesPerDay: 200,
	}
}

func (limits *Limits) field(key Key) *int64 {
	switch key {
	case KeyChatTurnsPerDay:
		return &limits.ChatTurnsPerDay
	case KeyActiveTurns:
		return &limits.ActiveTurns
	case KeyImportsPerDay:
		return &limits.ImportsPerDay
	case KeyProjects:
		return &limits.Projects
	case KeyProjectBooks:
		return &limits.ProjectBooks
	case KeyProjectChapters:
		return &limits.ProjectChapters
	case KeyProjectCharacters:
		return &limits.ProjectCharacters
	case KeyAccountBooks:
		return &limits.AccountBooks
	case KeyAccountChapters:
		return &limits.AccountChapters
	case KeyAccountCharacters:
		return &limits.AccountCharacters
	case KeyImagesPerDay:
		return &limits.ImagesPerDay
	case KeyImageUploadsPerDay:
		return &limits.ImageUploadsPerDay
	case KeyChapterDraftsPerDay:
		return &limits.ChapterDraftsPerDay
	case KeyDrawingTurnsPerDay:
		return &limits.DrawingTurnsPerDay
	case KeyDraftTurnsPerDay:
		return &limits.DraftTurnsPerDay
	case KeyDraftRewritesPerDay:
		return &limits.DraftRewritesPerDay
	default:
		return nil
	}
}

// Value returns one limit; an unknown key is 0.
func (limits Limits) Value(key Key) int64 {
	if field := limits.field(key); field != nil {
		return *field
	}
	return 0
}

// With returns the limits with one value replaced; an unknown key changes nothing.
func (limits Limits) With(key Key, value int64) Limits {
	if field := limits.field(key); field != nil {
		*field = value
	}
	return limits
}

// ErrNotFound means the writer is not known.
var ErrNotFound = errors.New("quota: writer not found")

// Exceeded refuses an operation that would go over one limit. Nothing was written.
type Exceeded struct {
	Key   Key
	Limit int64
	// ResetsAt is the next Moscow midnight for a daily limit, nil otherwise.
	ResetsAt *time.Time
}

func (exceeded *Exceeded) Error() string {
	return fmt.Sprintf("quota: %s limit %d exceeded", exceeded.Key, exceeded.Limit)
}

// Code is the stable problem and Job error code (R1.6).
func (exceeded *Exceeded) Code() string {
	return Code(exceeded.Key)
}

// Code maps a limit to its problem code: daily limits are named by what they count.
func Code(key Key) string {
	switch key {
	case KeyChatTurnsPerDay:
		return "quota.chat_turns_daily"
	case KeyImportsPerDay:
		return "quota.imports_daily"
	case KeyImagesPerDay:
		return "quota.images_daily"
	case KeyImageUploadsPerDay:
		return "quota.image_uploads_daily"
	case KeyChapterDraftsPerDay:
		return "quota.chapter_drafts_daily"
	case KeyDrawingTurnsPerDay:
		return "quota.drawing_turns_daily"
	case KeyDraftTurnsPerDay:
		return "quota.draft_turns_daily"
	case KeyDraftRewritesPerDay:
		return "quota.draft_rewrites_daily"
	default:
		return "quota." + string(key)
	}
}

// Daily reports whether the limit resets at Moscow midnight.
func (key Key) Daily() bool {
	return key == KeyChatTurnsPerDay || key == KeyImportsPerDay || key == KeyImagesPerDay || key == KeyImageUploadsPerDay ||
		key == KeyChapterDraftsPerDay || key == KeyDrawingTurnsPerDay || key == KeyDraftTurnsPerDay || key == KeyDraftRewritesPerDay
}

// AsExceeded finds a quota refusal in an error chain.
func AsExceeded(err error) (*Exceeded, bool) {
	var exceeded *Exceeded
	if errors.As(err, &exceeded) && exceeded != nil {
		return exceeded, true
	}
	return nil, false
}

// moscow is UTC+3: Moscow keeps no daylight saving time since 2014, and a fixed zone needs no
// tzdata in the image.
var moscow = time.FixedZone("MSK", 3*60*60)

// InMoscow is a moment on the Moscow clock.
func InMoscow(at time.Time) time.Time { return at.In(moscow) }

// MoscowDay is the Moscow calendar day of a moment as midnight UTC of that date (the value a
// PostgreSQL date column stores).
func MoscowDay(at time.Time) time.Time {
	local := at.In(moscow)
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.UTC)
}

// NextMoscowMidnight is the moment the daily counters of the day containing at reset.
func NextMoscowMidnight(at time.Time) time.Time {
	local := at.In(moscow)
	return time.Date(local.Year(), local.Month(), local.Day()+1, 0, 0, 0, 0, moscow).UTC()
}

// DailyExceeded builds the refusal of a daily limit at the moment at.
func DailyExceeded(key Key, limit int64, at time.Time) *Exceeded {
	resetsAt := NextMoscowMidnight(at)
	return &Exceeded{Key: key, Limit: limit, ResetsAt: &resetsAt}
}

// Counter is the use of one limit.
type Counter struct {
	Used  int64
	Limit int64
}

// Sources are the live books, chapters and characters of draft text (R3.5).
type Sources struct {
	Books      int64
	Chapters   int64
	Characters int64
}

// ProjectSources are the sources of one live project the writer owns.
type ProjectSources struct {
	ProjectID string
	Sources   Sources
}

// Account is what a writer sees of their own quotas (R5.1).
type Account struct {
	Limits        Limits
	Day           time.Time
	ResetsAt      time.Time
	ChatTurns     int64
	ActiveTurns   int64
	Imports       int64
	Images        int64
	ImageUploads  int64
	ChapterDrafts int64
	DrawingTurns  int64
	DraftTurns    int64
	DraftRewrites int64
	Projects      int64
	Sources       Sources
	OwnedProjects []ProjectSources
}

// Sum adds sources of several projects.
func Sum(projects []ProjectSources) Sources {
	var total Sources
	for _, project := range projects {
		total.Books += project.Sources.Books
		total.Chapters += project.Sources.Chapters
		total.Characters += project.Sources.Characters
	}
	return total
}

// UsageSource is where model tokens were spent (UC7–UC9).
type UsageSource string

const (
	UsageChat  UsageSource = "chat"
	UsageScan  UsageSource = "scan"
	UsageCards UsageSource = "cards"
	// UsageImages is the image model drawing the saga gallery (R5.2).
	UsageImages UsageSource = "images"
	// UsageLayer is the Job building the thread layer of a saga (specs/2026-11-01-writer-assistant-index.md, R2.8).
	UsageLayer UsageSource = "layer"
	// UsageDraft is the Job writing a plan or a chapter of the section «Письмо» (specs/2026-11-01-writer-assistant-index.md, R7.6).
	UsageDraft UsageSource = "draft"
	// UsageDrawing is the helper of the drawing workshop, an agent of its own (specs/2026-11-02-image-workshop.md IW8.2).
	UsageDrawing UsageSource = "drawing"
	// UsageDraftHelper is the helper of the chapter workshop, an agent of its own (specs/2026-11-02-writer-workshop.md
	// W1.5); UsageDraft stays the Job that writes a chapter.
	UsageDraftHelper UsageSource = "draft_helper"
	// UsageVoice is the assistant of the voice co-author on GPT-Live: its turns and the summary of a conversation
	// (specs/2026-11-03-voice-assistant.md V4.3).
	UsageVoice UsageSource = "voice"
	// UsageAuthorMaterials is the Job mapping the sections of a file of the author (specs/2026-11-04-saga-map.md, R9.2).
	UsageAuthorMaterials UsageSource = "author_materials"
)

// ModelUsage is what model calls cost: calls (HTTP attempts answered, or model responses of a
// chat turn) and tokens.
type ModelUsage struct {
	Calls                 int64
	InputTokens           int64
	CachedInputTokens     int64
	OutputTokens          int64
	ReasoningOutputTokens int64
}

// Add sums two usages.
func (usage ModelUsage) Add(other ModelUsage) ModelUsage {
	return ModelUsage{
		Calls: usage.Calls + other.Calls, InputTokens: usage.InputTokens + other.InputTokens,
		CachedInputTokens:     usage.CachedInputTokens + other.CachedInputTokens,
		OutputTokens:          usage.OutputTokens + other.OutputTokens,
		ReasoningOutputTokens: usage.ReasoningOutputTokens + other.ReasoningOutputTokens,
	}
}

// Empty reports usage with nothing to record.
func (usage ModelUsage) Empty() bool { return usage == ModelUsage{} }

// UsageRecord is one direct model spend to add to the day's aggregate. Cards count for the
// project owner, a scan for the author of its chat turn and a drawing for the author of its
// Job (R7.2).
type UsageRecord struct {
	Source    UsageSource
	ProjectID string
	// JobID names the chat turn of a scan, the illustration Job of a drawing or the chapter_draft Job.
	JobID string
	Model string
	Usage ModelUsage
}

// LimitSample is one reading of the model provider's usage window (R9.1).
type LimitSample struct {
	ObservedAt    time.Time
	LimitID       string
	WindowMinutes int64
	UsedPercent   float64
	ResetsAt      *time.Time
}
