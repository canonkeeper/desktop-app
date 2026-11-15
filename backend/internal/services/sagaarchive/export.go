// SPDX-License-Identifier: AGPL-3.0-only

// Package sagaarchive saves a saga into its file and opens a file as a new saga (specs/2026-11-13-desk-sagafile.md SF2,
// SF3): one use case for each, over the snapshot and the writes of the repository, the threads of the chat, the covers and
// the temporary files of the build.
package sagaarchive

import (
	"context"
	"errors"
	"io"
	"time"

	agentdomain "canonkeeper/backend/internal/domain/agent"
	timeline "canonkeeper/backend/internal/domain/agenttimeline"
	library "canonkeeper/backend/internal/domain/library"
	domain "canonkeeper/backend/internal/domain/sagaarchive"
	"canonkeeper/backend/internal/domain/sagalayer"
)

var (
	ErrNotFound  = errors.New("saga archive: saga not found")
	ErrForbidden = errors.New("saga archive: only the owner saves a saga into a file")
)

// Busy is a save the gate does not let through now: the author has one running, the process has its four, or the author
// saved ten in the last hour (SF2.4).
type Busy struct{ RetryAfter time.Duration }

func (Busy) Error() string { return "saga archive: busy" }

// SnapshotStore reads a saga in one snapshot (SF2.2): read runs inside the transaction of a reader who is a member of a
// live saga; one who is not is ErrNotFound, one below the owner ErrForbidden, before read runs.
type SnapshotStore interface {
	Snapshot(ctx context.Context, actorUserID, projectID string, read func(Snapshot) error) error
}

// Snapshot is the saga as one transaction sees it.
type Snapshot interface {
	Saga() (domain.Saga, error)
	Books() ([]SnapshotBook, error)
	// Chapters gives every live chapter of a live book with the paragraphs of its published text, or of its draft without
	// one, book after book in the order of the shelf.
	Chapters(each func(domain.Chapter) error) error
	// Cards gives every card of the saga; the caller keeps those of the text it wrote.
	Cards(each func(domain.Card) error) error
	Names() (*domain.Names, error)
	// Layer is the finished build of the layer, nil without one.
	Layer() (*Layer, error)
	MapHidden() ([]domain.MapHidden, error)
	Materials() ([]domain.Material, error)
	// Memory gives every record with the origin OriginOf says; Chapter is the id of its chapter.
	Memory() ([]domain.Memory, error)
	// Notes gives the notes of the owner whose source is in the saga.
	Notes(ownerUserID string) ([]SnapshotNote, error)
	// Transcripts are the chats an earlier saga file brought.
	Transcripts() ([]Transcript, error)
	// Chats are the chats of the saga with their turns, in the order they were asked; Mine marks a turn of the owner.
	Chats(ownerUserID string) ([]ChatSource, error)
	LeftOut() (domain.LeftOut, error)
}

// SnapshotBook is a book with the file of its cover, if any.
type SnapshotBook struct {
	Book        domain.Book
	CoverKey    string
	CoverMIME   string
	CoverSHA256 string
}

// Layer is the finished build of the layer with the ids of the saga in it.
type Layer struct {
	Model    string
	Chapters []sagalayer.Stamp
	Books    []sagalayer.Book
	Threads  []sagalayer.Thread
}

// SnapshotNote is a note of the owner with its source: a place of a chapter of the saga or a quote of a chat of it.
type SnapshotNote struct {
	Title, Body          string
	CreatedAt, UpdatedAt time.Time
	Kind                 string
	ChapterID            string
	ParagraphID          string
	Range                *library.TextRange
	Quote                string
}

// Transcript is a chat a saga file brought (SF3.8).
type Transcript struct {
	ID          string
	Title       string
	CreatedAt   time.Time
	LastTurnAt  time.Time
	OthersTurns int
	Turns       []domain.ChatTurn
}

// ChatSource is a chat of the saga as the base keeps it: its thread and its turns.
type ChatSource struct {
	ID        string
	Title     string
	ThreadID  string
	CreatedAt time.Time
	Turns     []TurnSource
}

// TurnSource is a turn of a chat: who asked it, the outcome of its Job when the thread does not tell it, the title of the
// chapter attached to the question.
type TurnSource struct {
	ProviderTurnID string
	AskedAt        time.Time
	Mine           bool
	Settled        string
	Attached       string
}

// CoverObjects open the files of the covers.
type CoverObjects interface {
	Open(ctx context.Context, key string) (io.ReadCloser, error)
}

// Threads read the thread of a chat.
type Threads interface {
	ThreadItems(ctx context.Context, flow agentdomain.Flow, rootThreadID string) ([]timeline.RawItem, error)
}

// TempFiles are the temporary files of the build, in the one directory its start clears (SF2.6).
type TempFiles interface {
	Create() (TempFile, error)
}

// TempFile is a temporary file: written, then read from its start; Remove closes it and removes it.
type TempFile interface {
	io.Writer
	io.ReaderAt
	Size() (int64, error)
	Remove() error
}

// Drawings counts the drawings of a saga the file does not carry; a build without drawings has none.
type Drawings func(ctx context.Context, projectID string) (int, error)
