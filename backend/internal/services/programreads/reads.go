// SPDX-License-Identifier: AGPL-3.0-only

// Package programreads is the reading of the sagas by a program of the author through the bridge
// (specs/2026-11-15-desk-mcp.md MC2.3, MC4.2, MC4.3): the sagas of the local author, the saga an argument names, what an
// answer named — kept in the memory of the call —, the name of the program and the record of every reading in the journal
// before its answer goes. It declares its ports; the door of the bridge calls it, it imports no delivery.
package programreads

import (
	"context"
	"errors"
	"strings"
	"time"

	"golang.org/x/text/unicode/norm"

	"canonkeeper/backend/internal/domain/disclosure"
	"canonkeeper/backend/internal/domain/modeljournal"
	"canonkeeper/backend/internal/projects"
)

// Projects lists the sagas of the local author, a page at a time: the living ones only.
type Projects interface {
	ListProjects(ctx context.Context, actorUserID string, input projects.ListProjectsInput) (projects.ProjectPage, error)
}

// Journal writes the record of a reading in one transaction before the answer goes: the author a member of a living saga,
// then the record; modeljournal.ErrSagaGone when the saga is no more his or is deleted.
type Journal interface {
	RecordRead(ctx context.Context, read modeljournal.ProgramRead) error
}

// KeyLength is the length of the key of a saga: the beginning of its id, which never changes (MC2.3).
const KeyLength = 8

// Saga is a living saga of the author as sagas gives it.
type Saga struct {
	ID       string
	Key      string
	Title    string
	Books    int
	Chapters int
}

// The refusals of a saga, none of them naming a saga (MC2.3).
var (
	ErrNoSagas   = errors.New("program reads: the author has no saga")
	ErrSeveral   = errors.New("program reads: the author has several sagas and the argument names none of them")
	ErrNoSuch    = errors.New("program reads: no such saga")
	ErrAmbiguous = errors.New("program reads: the beginning of the id is the beginning of two sagas")
)

// Reads are the readings of the programs of the local author.
type Reads struct {
	projects Projects
	journal  Journal
	actor    string
	now      func() time.Time
}

// New makes the readings of the programs of the local author.
func New(projects Projects, journal Journal, actorUserID string) *Reads {
	if projects == nil || journal == nil {
		panic("program reads: the sagas and the journal are required")
	}
	return &Reads{projects: projects, journal: journal, actor: actorUserID, now: time.Now}
}

// listPage is how many sagas one page of the list asks.
const listPage = 100

// Sagas are the living sagas of the author, every page of the list.
func (reads *Reads) Sagas(ctx context.Context) ([]Saga, error) {
	var sagas []Saga
	cursor := ""
	for {
		page, err := reads.projects.ListProjects(ctx, reads.actor, projects.ListProjectsInput{Limit: listPage, Cursor: cursor})
		if err != nil {
			return nil, err
		}
		for _, project := range page.Data {
			key := project.ID
			if len(key) > KeyLength {
				key = key[:KeyLength]
			}
			sagas = append(sagas, Saga{ID: project.ID, Key: key, Title: project.Title, Books: project.BookCount, Chapters: project.ChapterCount})
		}
		if !page.Meta.HasMore || page.Meta.NextCursor == nil || *page.Meta.NextCursor == "" {
			return sagas, nil
		}
		cursor = *page.Meta.NextCursor
	}
}

// Find is the saga an argument names: none — the one saga of the author; then a key or a longer beginning of an id; then a
// title one living saga has (NFC, any case, the outer spaces trimmed).
func (reads *Reads) Find(ctx context.Context, argument string) (Saga, error) {
	sagas, err := reads.Sagas(ctx)
	if err != nil {
		return Saga{}, err
	}
	if len(sagas) == 0 {
		return Saga{}, ErrNoSagas
	}
	argument = strings.TrimSpace(argument)
	if argument == "" {
		if len(sagas) == 1 {
			return sagas[0], nil
		}
		return Saga{}, ErrSeveral
	}
	if len(argument) >= KeyLength {
		var found []Saga
		for _, saga := range sagas {
			if strings.HasPrefix(strings.ToLower(saga.ID), strings.ToLower(argument)) {
				found = append(found, saga)
			}
		}
		switch len(found) {
		case 1:
			return found[0], nil
		case 0:
		default:
			return Saga{}, ErrAmbiguous
		}
	}
	title := comparable(argument)
	var found []Saga
	for _, saga := range sagas {
		if comparable(saga.Title) == title {
			found = append(found, saga)
		}
	}
	switch len(found) {
	case 1:
		return found[0], nil
	case 0:
		return Saga{}, ErrNoSuch
	default:
		return Saga{}, ErrSeveral
	}
}

func comparable(title string) string {
	return strings.ToLower(norm.NFC.String(strings.TrimSpace(title)))
}

// Read is one reading a program got: the name and the title it gave itself on initialize, the saga — none for sagas —, the
// bytes of the page, its number and the number of the pages, what the answer named, and for sagas how many titles it gave.
type Read struct {
	Client    string
	Title     string
	ProjectID string
	Bytes     int64
	Page      int
	Pages     int
	Named     disclosure.Named
	Sagas     int
}

// Record writes the reading in the journal before its answer goes (MC4.2): a page of an answer of several pages names its
// places at the precision of the pages. ErrNoSuch — the saga went away before the record; any other error — no record.
func (reads *Reads) Record(ctx context.Context, read Read) error {
	named := read.Named
	if read.Pages > 1 {
		named = named.InPages()
	}
	named.Sagas = read.Sagas
	err := reads.journal.RecordRead(ctx, modeljournal.ProgramRead{
		ID: modeljournal.NewID(), At: reads.now(), ActorUserID: reads.actor, ProjectID: read.ProjectID,
		Client: ProgramName(read.Client, read.Title), Bytes: read.Bytes, Page: read.Page, Pages: read.Pages, Named: named,
	})
	if errors.Is(err, modeljournal.ErrSagaGone) {
		return ErrNoSuch
	}
	return err
}
