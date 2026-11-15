// SPDX-License-Identifier: AGPL-3.0-only

package obsidian

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"io"
	"log"
	"path/filepath"
	"sync"
	"time"

	"canonkeeper/backend/internal/domain/obsidian"
)

// The terms of a choice of the folder of a vault (OB1.1, OB1.3).
const (
	// ChoiceTerm is how long a choice waits for the folder the shell picks.
	ChoiceTerm = 10 * time.Minute
	// CheckTime bounds the check of the folder the shell picked, outside the loop of the channel.
	CheckTime = 10 * time.Second
	// requestBytes is the randomness of the number of a choice.
	requestBytes = 32
)

// OutcomeLinked and OutcomeStale end a choice for the shell beside the reasons of a refusal (folder-result): the folder is
// the vault of the saga now; the number was never declared, has gone by or was spent.
const (
	OutcomeLinked = "linked"
	OutcomeStale  = "stale"
)

// Shell is the shell as a choice asks it: to choose a folder for a number it declares, and how the choice ended
// (folder-choice, folder-result).
type Shell interface {
	ChooseFolder(request, sagaTitle string) error
	FolderResult(request, outcome string) error
}

// Choices are the choices of the folder of a vault in the process (OB1.1): one waits at a time — a new one puts out the
// one before —, its number of 32 random bytes is spent once and lives ChoiceTerm; and the refusal each saga heard last.
// All of it lives in memory: a restart forgets a choice the shell then answers stale.
type Choices struct {
	mu      sync.Mutex
	waiting *choice
	refused map[string]obsidian.Reason
	now     func() time.Time
	random  io.Reader
}

type choice struct {
	project string
	request string
	until   time.Time
}

// NewChoices are the choices of a process.
func NewChoices(now func() time.Time, random io.Reader) *Choices {
	return &Choices{refused: map[string]obsidian.Reason{}, now: now, random: random}
}

// begin declares a new choice of the saga and gives its number.
func (choices *Choices) begin(project string) (string, error) {
	var raw [requestBytes]byte
	if _, err := io.ReadFull(choices.random, raw[:]); err != nil {
		return "", err
	}
	request := base64.RawURLEncoding.EncodeToString(raw[:])
	choices.mu.Lock()
	defer choices.mu.Unlock()
	choices.waiting = &choice{project: project, request: request, until: choices.now().Add(ChoiceTerm)}
	return request, nil
}

// drop puts out a choice the shell never heard of.
func (choices *Choices) drop(request string) {
	choices.mu.Lock()
	defer choices.mu.Unlock()
	if choices.waiting != nil && choices.waiting.request == request {
		choices.waiting = nil
	}
}

// take spends the number of the choice that waits: the saga it was declared for, or nothing for a number never declared,
// gone by or spent. The numbers are compared in a constant time.
func (choices *Choices) take(request string) (string, bool) {
	choices.mu.Lock()
	defer choices.mu.Unlock()
	waiting := choices.waiting
	if waiting == nil || subtle.ConstantTimeCompare([]byte(waiting.request), []byte(request)) != 1 {
		return "", false
	}
	choices.waiting = nil
	if !choices.now().Before(waiting.until) {
		return "", false
	}
	return waiting.project, true
}

// State says whether a choice of the saga waits and the refusal it heard last.
func (choices *Choices) State(project string) (choosing bool, refused obsidian.Reason) {
	choices.mu.Lock()
	defer choices.mu.Unlock()
	waiting := choices.waiting
	choosing = waiting != nil && waiting.project == project && choices.now().Before(waiting.until)
	return choosing, choices.refused[project]
}

func (choices *Choices) refuse(project string, reason obsidian.Reason) {
	choices.mu.Lock()
	defer choices.mu.Unlock()
	choices.refused[project] = reason
}

func (choices *Choices) linked(project string) {
	choices.mu.Lock()
	defer choices.mu.Unlock()
	delete(choices.refused, project)
}

// BeginChoice is beginObsidianVaultChoice (OB1.1): a member who changes the books of the saga declares a choice, and the
// shell hears its number with the title of the saga. The page gets the number for pick_folder, never a path.
type BeginChoice struct {
	Vaults  Vaults
	Choices *Choices
	Shell   Shell
}

func (begin BeginChoice) Execute(ctx context.Context, actorUserID, projectID string) (string, error) {
	linked, err := begin.Vaults.Vault(ctx, actorUserID, projectID, AccessWrite)
	if err != nil {
		return "", err
	}
	if begin.Shell == nil || begin.Choices == nil {
		return "", ErrChoiceUnavailable
	}
	request, err := begin.Choices.begin(projectID)
	if err != nil {
		return "", err
	}
	if err := begin.Shell.ChooseFolder(request, linked.Title); err != nil {
		begin.Choices.drop(request)
		return "", ErrChoiceUnavailable
	}
	return request, nil
}

// PickFolder hears folder-picked (OB1.1, OB1.3): the number is spent, the folder is checked outside the loop of the
// channel within CheckTime — it is, it is a folder, it opens as a root and reads —, kept as the vault of the saga, and
// the shell hears how the choice ended. A refusal leaves the link before. No log line names the path or the number.
type PickFolder struct {
	Choices   *Choices
	Vaults    Vaults
	Files     Files
	Shell     Shell
	CheckTime time.Duration
	Logger    *log.Logger
}

// Hear takes the folder the shell picked and returns at once: the loop of the channel goes on while the check runs.
func (pick PickFolder) Hear(request, vaultPath string) {
	go pick.run(request, vaultPath)
}

func (pick PickFolder) run(request, vaultPath string) {
	project, ok := pick.Choices.take(request)
	if !ok {
		pick.answer(request, OutcomeStale)
		return
	}
	ctx := context.Background()
	_, err := within(ctx, pick.CheckTime, func(context.Context) (struct{}, error) {
		vault, err := pick.Files.Open(vaultPath)
		if err != nil {
			return struct{}{}, err
		}
		return struct{}{}, vault.Close()
	})
	if err != nil {
		reason := obsidian.ReasonOf(err)
		pick.Choices.refuse(project, reason)
		pick.Logger.Printf(`{"event":"obsidian_folder_refused","reason":%q}`, reason)
		pick.answer(request, string(reason))
		return
	}
	if err := pick.Vaults.Link(ctx, project, vaultPath); err != nil {
		// The shell hears nothing and tells the page the choice went by: the link was not kept.
		pick.Logger.Print(`{"event":"obsidian_link_not_kept"}`)
		return
	}
	pick.Choices.linked(project)
	pick.answer(request, OutcomeLinked)
}

func (pick PickFolder) answer(request, outcome string) {
	if err := pick.Shell.FolderResult(request, outcome); err != nil {
		pick.Logger.Print(`{"event":"obsidian_folder_result_not_sent"}`)
	}
}

// AvailableTime bounds how long getObsidianVault waits to know whether the vault opens (OB1.5).
const AvailableTime = 2 * time.Second

// VaultState is the link of a saga as getObsidianVault says it (OB1.5).
type VaultState struct {
	Linked bool
	// Path is the path of the vault, the one absolute path an operation gives; Name the name of its folder.
	Path string
	Name string
	// Available says the vault opens now; nil is not known within AvailableTime.
	Available *bool
	Choosing  bool
	Refused   obsidian.Reason
	// Books are the live books that hold a folder of the vault (specs/2026-11-14-desk-obsidian-books.md OK2.6); Relink are
	// the live books whose folder another book holds — the author links them anew or takes their link off.
	Books  []VaultBook
	Relink []VaultBook
}

// VaultBook is a book of the saga linked with a folder of the vault: when it was last loaded or updated from it, and whether
// the folder is there now — nil while the one check of the saga runs or past its time (OK2.6). Choice is the composition its
// link keeps, which the glue of its update takes (specs/2026-11-15-desk-obsidian-web.md OW2.5): the page asks a preview of
// its folder by it.
type VaultBook struct {
	BookID   string
	Title    string
	Folder   string
	LoadedAt *time.Time
	Present  *bool
	Choice   BookChoice
}

// Holders read what holds each folder of the vault of a saga by the rule of reading of the links (OK0.3).
type Holders interface {
	Folders(ctx context.Context, projectID string) ([]HeldFolder, error)
}

// Openings are the checks whether a linked vault opens that still run (OB1.5): one at a time for a saga. A folder that
// does not answer holds the one check of its saga, not one for each read of the link, and while it runs the link reads
// as not known.
type Openings struct {
	mu      sync.Mutex
	running map[string]bool
}

// NewOpenings are the checks of a process.
func NewOpenings() *Openings { return &Openings{running: map[string]bool{}} }

// begin takes the check of the saga; nothing when one runs.
func (openings *Openings) begin(projectID string) (func(), bool) {
	openings.mu.Lock()
	defer openings.mu.Unlock()
	if openings.running[projectID] {
		return nil, false
	}
	openings.running[projectID] = true
	return func() {
		openings.mu.Lock()
		defer openings.mu.Unlock()
		delete(openings.running, projectID)
	}, true
}

// ReadVault is getObsidianVault: any member of the saga reads its link and its linked books. Whether the vault opens and
// whether the folder of each linked book is in it is one check of the saga within Wait (OB1.5, OK2.6).
type ReadVault struct {
	Vaults   Vaults
	Files    Files
	Links    Holders
	Choices  *Choices
	Openings *Openings
	Wait     time.Duration
}

func (read ReadVault) Execute(ctx context.Context, actorUserID, projectID string) (VaultState, error) {
	linked, err := read.Vaults.Vault(ctx, actorUserID, projectID, AccessRead)
	if err != nil {
		return VaultState{}, err
	}
	var state VaultState
	if read.Choices != nil {
		state.Choosing, state.Refused = read.Choices.State(projectID)
	}
	if linked.Path == "" {
		return state, nil
	}
	state.Linked, state.Path, state.Name = true, linked.Path, filepath.Base(linked.Path)
	folders, err := read.Links.Folders(ctx, projectID)
	if err != nil {
		return VaultState{}, err
	}
	var linkedFolders []string
	for _, folder := range folders {
		if folder.Book == nil {
			continue
		}
		linkedFolders = append(linkedFolders, folder.Folder)
		state.Books = append(state.Books, vaultBookOf(*folder.Book, folder.Folder))
		for _, other := range folder.Others {
			state.Relink = append(state.Relink, vaultBookOf(other, folder.Folder))
		}
	}
	release, ok := read.Openings.begin(projectID)
	if !ok {
		return state, nil
	}
	// OK2.6: the folders of the books are looked at within the same one check, not a check of their own.
	present, err := held(ctx, read.Wait, release, func(ctx context.Context) (map[string]bool, error) {
		vault, err := read.Files.Open(linked.Path)
		if err != nil {
			return nil, err
		}
		defer vault.Close()
		present := map[string]bool{}
		for _, folder := range linkedFolders {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			present[folder] = vault.Folder(ctx, folder) == nil
		}
		return present, nil
	})
	switch {
	case err == nil:
		state.Available = new(true)
		for _, books := range [][]VaultBook{state.Books, state.Relink} {
			for index := range books {
				books[index].Present = new(present[books[index].Folder])
			}
		}
	case errors.Is(err, ErrSlow):
	case ctx.Err() != nil:
		return VaultState{}, ctx.Err()
	default:
		state.Available = new(false)
	}
	return state, nil
}

func vaultBookOf(book LinkedBook, folder string) VaultBook {
	return VaultBook{BookID: book.BookID, Title: book.Title, Folder: folder, LoadedAt: book.LoadedAt, Choice: book.Choice}
}

// Unlink is unlinkObsidianVault (OB1.5): the saga is not linked with its vault any more; its books, its records and the
// files of the vault stay.
type Unlink struct {
	Vaults Vaults
}

func (unlink Unlink) Execute(ctx context.Context, actorUserID, projectID string) error {
	return unlink.Vaults.Unlink(ctx, actorUserID, projectID)
}
