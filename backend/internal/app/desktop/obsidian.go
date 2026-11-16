// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"context"
	"crypto/rand"
	"errors"
	"log"
	"sync"
	"time"
	"unicode/utf8"

	"canonkeeper/backend/internal/adapters/obsidianvault"
	"canonkeeper/backend/internal/app"
	"canonkeeper/backend/internal/auth"
	httpobsidian "canonkeeper/backend/internal/delivery/http/obsidian"
	"canonkeeper/backend/internal/domain/aisettings"
	"canonkeeper/backend/internal/library"
	obsidianrepo "canonkeeper/backend/internal/repository/postgres/obsidian"
	service "canonkeeper/backend/internal/services/obsidian"
	readingservice "canonkeeper/backend/internal/services/readingconsent"
	librarystore "canonkeeper/backend/internal/store/library"
)

// obsidianBridge is the bridge of this run with the vaults of Obsidian (specs/2026-11-13-desk-obsidian.md): the choices
// of a folder of the process and the shell they ask, the gate of the operations that read a vault; compose gives it the
// store of the links: of the books with the folders (part A2, specs/2026-11-14-desk-obsidian-books.md) and of the records with
// the notes (part A3, specs/2026-11-15-desk-obsidian-notes.md).
type obsidianBridge struct {
	choices  *service.Choices
	openings *service.Openings
	shell    folderShell
	gate     *service.Gate
	logger   *log.Logger
	mu       sync.Mutex
	pick     *service.PickFolder
	// drafts cancels the drafts of the bridge of every saga (specs/2026-11-14-desk-obsidian-books.md OK1.4); set by compose.
	drafts func(context.Context) error
}

func newObsidianBridge(send func(ControlMessage) error, logger *log.Logger) *obsidianBridge {
	return &obsidianBridge{
		choices: service.NewChoices(time.Now, rand.Reader), openings: service.NewOpenings(), shell: folderShell{send: send},
		gate: service.NewGate(), logger: logger,
	}
}

// handlers are the operations of the bridge over the core of the run; helper says whether «Свой ИИ» is connected.
func (bridge *obsidianBridge) handlers(core app.Core, helper func(context.Context) service.Helper, consent sagaConsent, users httpobsidian.CurrentUserService) httpobsidian.Handlers {
	vaults := obsidianrepo.New(core.DB)
	links := obsidianrepo.NewBooks(core.DB)
	files := obsidianvault.Files{}
	shelf := obsidianShelf{library: core.LibraryService, store: core.Library, jobs: core.JobService, keys: links}
	bridge.mu.Lock()
	bridge.pick = &service.PickFolder{
		Choices: bridge.choices, Vaults: vaults, Files: files, Shell: bridge.shell, CheckTime: service.CheckTime, Logger: bridge.logger,
	}
	// specs/2026-11-14-desk-obsidian-books.md OK1.4: the drafts a load left when the process fell are canceled at the start.
	bridge.drafts = func(ctx context.Context) error {
		return service.CancelDrafts(ctx, links, shelf, bridge.logger, "")
	}
	bridge.mu.Unlock()
	return httpobsidian.Handlers{
		Read: service.ReadVault{
			Vaults: vaults, Files: files, Links: links, Choices: bridge.choices, Openings: bridge.openings, Wait: service.AvailableTime,
		},
		Begin:  service.BeginChoice{Vaults: vaults, Choices: bridge.choices, Shell: bridge.shell},
		Unlink: service.Unlink{Vaults: vaults},
		Scan: service.Scan{
			Vaults: vaults, Files: files, Links: links, Gate: bridge.gate, Limits: service.DefaultLimits,
		},
		Preview: service.PreviewImport{
			Vaults: vaults, Files: files, Links: links, Plans: obsidianPlans{store: core.Library}, Volumes: obsidianVolumes{},
			Plain: library.MarkdownPlainLines,
			Saga:  obsidianSaga{core: core, helper: helper, consent: consent},
			Gate:  bridge.gate, Limits: service.DefaultLimits,
		},
		Load: service.ImportBooks{
			Vaults: vaults, Files: files, Links: links, Volumes: obsidianVolumes{}, Shelf: shelf, Replays: obsidianrepo.NewReplays(core.DB),
			Gate: bridge.gate, Limits: service.DefaultLimits, Logger: bridge.logger, Now: time.Now, Failpoint: obsidianPoint,
		},
		Update: service.UpdateBook{
			Vaults: vaults, Files: files, Links: links, Plans: obsidianPlans{store: core.Library}, Volumes: obsidianVolumes{}, Shelf: shelf,
			Gate: bridge.gate, Limits: service.DefaultLimits, Logger: bridge.logger,
		},
		LinkBook: service.LinkBookFolder{
			Vaults: vaults, Files: files, Links: links, Shelf: shelf, Gate: bridge.gate, Limits: service.DefaultLimits, Logger: bridge.logger,
			Now: time.Now,
		},
		UnlinkBook: service.UnlinkBookFolder{Vaults: vaults, Links: links},
		// The records of «Ваши материалы» from notes (specs/2026-11-15-desk-obsidian-notes.md ON1).
		Materials: service.ImportMaterials{
			Vaults: vaults, Files: files, Links: links, Notes: obsidianrepo.NewNotes(core.DB), Book: obsidianSaga{core: core},
			Plain: library.MarkdownPlainLines, Gate: bridge.gate, Limits: service.DefaultLimits,
		},
		// The export of the map and of the notes of the chapters (specs/2026-11-15-desk-obsidian-export.md OE1).
		Export: service.Export{
			Exports: obsidianrepo.NewExports(core.DB), Saga: newObsidianSagaMap(core), Writers: obsidianvault.Writers{},
			Gate: bridge.gate, Limits: service.DefaultLimits, Now: time.Now,
		},
		Users: users, AllowedOrigins: core.Config.AllowedOrigins,
	}
}

// cancelDrafts cancels the drafts of the bridge a load left when the process fell (OK1.4), once compose built the bridge.
func (bridge *obsidianBridge) cancelDrafts(ctx context.Context) {
	bridge.mu.Lock()
	drafts := bridge.drafts
	bridge.mu.Unlock()
	if drafts == nil {
		return
	}
	if err := drafts(ctx); err != nil {
		bridge.logger.Print(`{"event":"obsidian_drafts_not_canceled"}`)
	}
}

// obsidianVolumes reads a glued book as the import queue reads the file of a volume (library.CheckQueueVolume, OB4.2):
// the refusal a file gets at the upload — too large, a name the queue does not take — is its code too.
type obsidianVolumes struct{}

func (obsidianVolumes) Check(fileName string, content []byte) service.Volume {
	volume, err := library.CheckQueueVolume(fileName, content)
	switch {
	case errors.Is(err, library.ErrImportFileTooLarge):
		return service.Volume{Refusal: "import.file_too_large"}
	case err != nil:
		return service.Volume{Refusal: "request.validation"}
	case volume.Refusal != "":
		return service.Volume{Refusal: volume.Refusal}
	}
	chapters := make([]service.VolumeChapter, 0, len(volume.Book.Chapters))
	for _, chapter := range volume.Book.Chapters {
		characters := 0
		for _, paragraph := range chapter.Paragraphs {
			characters += utf8.RuneCountInString(paragraph)
		}
		chapters = append(chapters, service.VolumeChapter{
			Title: chapter.Title, Characters: characters, Paragraphs: chapter.Paragraphs, Markdown: chapter.Markdown,
		})
	}
	return service.Volume{Title: volume.Book.Title, Chapters: chapters}
}

// obsidianPlans say whether «Обновить из файла» of a book by a glued text would change it: the plan the update of the core
// writes by (specs/2026-11-14-desk-obsidian-books.md OK0.4, OK2.2).
type obsidianPlans struct{ store *librarystore.Store }

func (plans obsidianPlans) Changes(ctx context.Context, projectID, bookID string, chapters []service.VolumeChapter) (bool, error) {
	file := make([]library.ImportedChapter, len(chapters))
	for index, chapter := range chapters {
		file[index] = library.ImportedChapter{Title: chapter.Title, Paragraphs: chapter.Paragraphs, Markdown: chapter.Markdown}
	}
	changed, err := plans.store.UpdateChanges(ctx, projectID, bookID, file)
	if errors.Is(err, library.ErrNotFound) {
		return false, service.ErrNotFound
	}
	return changed, err
}

// obsidianSaga is what a preview reads of a saga in the core of the run: the names of the prepared map and the reading —
// «Свой ИИ» and the consent of the author to the saga (OB4.4).
type obsidianSaga struct {
	core app.Core
	// helper names who reads the books for the app now: nobody, «Свой ИИ» or the helper of the account.
	helper  func(context.Context) service.Helper
	consent sagaConsent
}

// sagaConsent says whether the books of a saga are read now under the consent of its author — the policy of the reading the
// builders of the cards obey (specs/2026-11-13-desk-consent.md CN3.1), one owner of the rule.
type sagaConsent func(ctx context.Context, projectID string) (bool, error)

func (saga obsidianSaga) BookNames(ctx context.Context, projectID string, keys []string) (map[string]string, error) {
	return saga.core.SagaStore.BookNames(ctx, projectID, keys)
}

func (saga obsidianSaga) Reading(ctx context.Context, projectID string) (service.Reading, error) {
	reading := service.Reading{Helper: saga.helper(ctx)}
	consent, err := saga.consent(ctx, projectID)
	reading.Consent = consent
	return reading, err
}

// picked hears folder-picked of the shell and returns at once (OB1.3): a folder before compose — no choice can have been
// declared then — ends stale.
func (bridge *obsidianBridge) picked(message ControlMessage) {
	bridge.mu.Lock()
	pick := bridge.pick
	bridge.mu.Unlock()
	if pick == nil {
		_ = bridge.shell.FolderResult(message.Request, service.OutcomeStale)
		return
	}
	folder, err := plainPath(message.Path)
	if err != nil {
		folder = message.Path
	}
	pick.Hear(message.Request, folder)
}

// folderShell asks the shell to choose a folder and tells it how the choice ended (folder-choice, folder-result).
type folderShell struct{ send func(ControlMessage) error }

func (shell folderShell) ChooseFolder(request, sagaTitle string) error {
	return shell.send(ControlMessage{Type: messageFolderChoice, Request: request, Saga: sagaTitle})
}

func (shell folderShell) FolderResult(request, outcome string) error {
	return shell.send(ControlMessage{Type: messageFolderResult, Request: request, Outcome: outcome})
}

var _ httpobsidian.CurrentUserService = (*auth.Service)(nil)

// sagaReading is the policy of the reading of the run; allowed is its answer for a saga outside a sample.
type sagaReading struct{ readingservice.Policy }

func (reading sagaReading) allowed(ctx context.Context, projectID string) (bool, error) {
	return reading.ReadingAllowed(ctx, projectID, false)
}

// reads names who reads the books for the app now, as the preview of a vault says it (specs/2026-11-13-desk-obsidian.md
// OB4.4; specs/2026-11-13-desk-account-ai.md AI1.2): nobody until a helper is connected, else the source of the settings.
func (own ownAI) reads(ctx context.Context) service.Helper {
	switch {
	case !own.Connected(ctx):
		return service.HelperNone
	case own.AI().Source == aisettings.SourceAccount:
		return service.HelperAccount
	default:
		return service.HelperOwn
	}
}
