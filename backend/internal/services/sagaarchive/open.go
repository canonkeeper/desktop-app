// SPDX-License-Identifier: AGPL-3.0-only

package sagaarchive

import (
	"context"
	"crypto/sha256"
	"errors"
	"time"

	"canonkeeper/backend/internal/auth"
	domain "canonkeeper/backend/internal/domain/sagaarchive"
)

// ErrConflict is a key of a repeat that opened another file.
var ErrConflict = errors.New("saga archive: the key opened another file")

// OpenRequest names an open by its author, the key of its repeat and the SHA-256 of its file (SF3.12).
type OpenRequest struct {
	ActorUserID    string
	IdempotencyKey string
	FileSHA256     string
}

// StoredCover is the file of a cover the open put among the files of the build before its transaction.
type StoredCover struct {
	ArtifactID string
	StorageKey string
	MIME       string
	Size       int64
	Width      int
	Height     int
	SHA256     [sha256.Size]byte
}

// OpenWrite is a file the reader took, to write as a new saga.
type OpenWrite struct {
	OpenRequest
	Proof   auth.SessionWriteProof
	Archive *domain.Archive
	// Covers are the covers taken, by the ref of their book; Skipped the titles of the books whose cover was not.
	Covers  map[string]StoredCover
	Skipped []string
	At      time.Time
}

// Opened is the new saga and what the file brought (SF3.12).
type Opened struct {
	ProjectID string `json:"projectId"`
	Name      string `json:"name"`
	Renamed   bool   `json:"renamed"`
	Report    Report `json:"report"`
}

// Report says what the file brought and what it did not.
type Report struct {
	Books                int            `json:"books"`
	Chapters             int            `json:"chapters"`
	Cards                int            `json:"cards"`
	ChaptersWithoutCards WithoutCards   `json:"chaptersWithoutCards"`
	NamesFresh           bool           `json:"namesFresh"`
	Threads              bool           `json:"threads"`
	Materials            int            `json:"materials"`
	Memory               int            `json:"memory"`
	Notes                int            `json:"notes"`
	Chats                int            `json:"chats"`
	ChatsNotRead         []string       `json:"chatsNotRead"`
	CoversSkipped        []string       `json:"coversSkipped"`
	LeftOut              domain.LeftOut `json:"leftOut"`
	Sections             []string       `json:"sections"`
}

// WithoutCards counts the chapters the file brought no card for and names up to 20 of them by their place.
type WithoutCards struct {
	Count  int      `json:"count"`
	Places []string `json:"places"`
}

// OpenStore writes a file as a new saga in one transaction, the key of its repeat with it.
type OpenStore interface {
	// Replay is what a key already opened: its answer for the same file, ErrConflict for another, nil for a new key.
	Replay(ctx context.Context, request OpenRequest) (*Opened, error)
	// Open writes the saga; wrote is false when a request of the same key committed first and its answer is the one given.
	Open(ctx context.Context, write OpenWrite) (opened Opened, wrote bool, err error)
}

// CoverFiles keep the files of covers: written to a staging name, published to the final one, removed.
type CoverFiles interface {
	Create(ctx context.Context, key string, content []byte) error
	Publish(ctx context.Context, staging, final string) error
	Remove(ctx context.Context, key string) error
}

// CoverIdentity names a new cover: its row and its staging and final files.
type CoverIdentity struct {
	ArtifactID string
	StagingKey string
	FinalKey   string
}

// Marks list the files an open put among the files of the build while its transaction runs, so a start after a fall
// removes those no row names (SF3.9).
type Marks interface {
	Mark(keys []string) (Mark, error)
}

// Mark is a list of files of an open; Remove drops it once the open committed or took them away.
type Mark interface{ Remove() error }

// OpenSaga opens a saga file as a new saga (SF3): every check of the file before a write, the covers it takes among the
// files of the build, then one transaction; the temporary file goes in any outcome. No model is called.
type OpenSaga struct {
	Store    OpenStore
	Covers   CoverFiles
	Identity func() (CoverIdentity, error)
	Marks    Marks
	Now      func() time.Time
}

// OpenInput is the file the author opens, read into a temporary file, with its SHA-256.
type OpenInput struct {
	OpenRequest
	Proof auth.SessionWriteProof
	File  TempFile
}

// Execute opens the file.
func (useCase *OpenSaga) Execute(ctx context.Context, input OpenInput) (Opened, error) {
	defer func() { _ = input.File.Remove() }()
	size, err := input.File.Size()
	if err != nil {
		return Opened{}, err
	}
	if size > domain.MaxFileBytes {
		return Opened{}, &domain.Refusal{Code: domain.CodeTooLarge}
	}
	if replay, err := useCase.Store.Replay(ctx, input.OpenRequest); err != nil || replay != nil {
		if err != nil {
			return Opened{}, err
		}
		return *replay, nil
	}
	archive, err := domain.Read(input.File, size)
	if err != nil {
		return Opened{}, err
	}
	write := OpenWrite{OpenRequest: input.OpenRequest, Proof: input.Proof, Archive: archive, Covers: map[string]StoredCover{},
		Skipped: []string{}, At: useCase.Now().UTC()}
	pending, err := useCase.takeCovers(ctx, archive, &write)
	if err != nil {
		return Opened{}, err
	}
	var mark Mark
	cleanup := context.WithoutCancel(ctx)
	if len(pending) > 0 {
		keys := make([]string, 0, len(pending))
		for _, cover := range pending {
			keys = append(keys, cover.identity.StagingKey, cover.identity.FinalKey)
		}
		if mark, err = useCase.Marks.Mark(keys); err != nil {
			return Opened{}, err
		}
		defer func() { _ = mark.Remove() }()
	}
	removeCovers := func() {
		for _, cover := range pending {
			_ = useCase.Covers.Remove(cleanup, cover.identity.StagingKey)
			_ = useCase.Covers.Remove(cleanup, cover.identity.FinalKey)
		}
	}
	for _, cover := range pending {
		content, err := archive.Cover(cover.file)
		if err == nil {
			err = useCase.Covers.Create(ctx, cover.identity.StagingKey, content)
		}
		if err != nil {
			removeCovers()
			return Opened{}, err
		}
		if err := useCase.Covers.Publish(ctx, cover.identity.StagingKey, cover.identity.FinalKey); err != nil {
			removeCovers()
			return Opened{}, err
		}
	}
	opened, wrote, err := useCase.Store.Open(ctx, write)
	if err != nil || !wrote {
		// A request of the same key that committed first named its own covers: these have no row.
		removeCovers()
	}
	if err != nil {
		return Opened{}, err
	}
	return opened, nil
}

// pendingCover is a cover taken: its file in the saga file and its name among the files of the build. Its bytes are read
// again when it is written, so the covers of a saga are never all in memory.
type pendingCover struct {
	identity CoverIdentity
	file     string
}

// takeCovers reads the cover of every book that has one and takes those the rules of a cover let through (SF3.9); the
// titles of the others go to the report.
func (useCase *OpenSaga) takeCovers(ctx context.Context, archive *domain.Archive, write *OpenWrite) ([]pendingCover, error) {
	var pending []pendingCover
	for _, book := range archive.Books {
		if book.Cover == nil {
			continue
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		content, err := archive.Cover(book.Cover.File)
		if err != nil {
			return nil, err
		}
		picture, ok := domain.CoverPicture(content, book.Cover.MIME)
		if !ok {
			write.Skipped = append(write.Skipped, book.Title)
			continue
		}
		identity, err := useCase.Identity()
		if err != nil {
			return nil, err
		}
		write.Covers[book.Ref] = StoredCover{
			ArtifactID: identity.ArtifactID, StorageKey: identity.FinalKey, MIME: picture.MIME, Size: int64(len(content)),
			Width: picture.Width, Height: picture.Height, SHA256: sha256.Sum256(content),
		}
		pending = append(pending, pendingCover{identity: identity, file: book.Cover.File})
	}
	return pending, nil
}
