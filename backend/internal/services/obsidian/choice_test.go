// SPDX-License-Identifier: AGPL-3.0-only

package obsidian_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"log"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"canonkeeper/backend/internal/adapters/obsidianvault"
	"canonkeeper/backend/internal/domain/obsidian"
	service "canonkeeper/backend/internal/services/obsidian"
)

// shell is the shell of a test: what it was asked to choose and how each choice ended.
type shell struct {
	mu      sync.Mutex
	asked   []string
	titles  []string
	results chan [2]string
	refuse  bool
}

func (shell *shell) ChooseFolder(request, title string) error {
	shell.mu.Lock()
	defer shell.mu.Unlock()
	if shell.refuse {
		return errors.New("no channel")
	}
	shell.asked, shell.titles = append(shell.asked, request), append(shell.titles, title)
	return nil
}

func (shell *shell) FolderResult(request, outcome string) error {
	shell.results <- [2]string{request, outcome}
	return nil
}

func (shell *shell) result(t *testing.T) [2]string {
	t.Helper()
	select {
	case result := <-shell.results:
		return result
	case <-time.After(5 * time.Second):
		t.Fatal("no folder-result")
	}
	return [2]string{}
}

// clock is a time a test moves.
type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (clock *clock) Now() time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return clock.now
}

func (clock *clock) pass(by time.Duration) {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	clock.now = clock.now.Add(by)
}

type choosing struct {
	vaults  *vaults
	choices *service.Choices
	shell   *shell
	clock   *clock
	begin   service.BeginChoice
	pick    service.PickFolder
	read    service.ReadVault
	log     *bytes.Buffer
}

func newChoosing(files service.Files) *choosing {
	setup := &choosing{
		vaults: &vaults{paths: map[string]string{}}, shell: &shell{results: make(chan [2]string, 8)},
		clock: &clock{now: time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)}, log: &bytes.Buffer{},
	}
	setup.choices = service.NewChoices(setup.clock.Now, rand.Reader)
	setup.begin = service.BeginChoice{Vaults: setup.vaults, Choices: setup.choices, Shell: setup.shell}
	setup.pick = service.PickFolder{
		Choices: setup.choices, Vaults: setup.vaults, Files: files, Shell: setup.shell, CheckTime: service.CheckTime,
		Logger: log.New(setup.log, "", 0),
	}
	setup.read = service.ReadVault{
		Vaults: setup.vaults, Files: files, Links: &bookLinks{}, Choices: setup.choices, Openings: service.NewOpenings(),
		Wait: service.AvailableTime,
	}
	return setup
}

// specs/2026-11-13-desk-obsidian.md OB1.1, OB1.3, OB1.4: one choice waits at a time; folder-picked with the declared number
// links the saga and ends with linked; a number never declared, gone by or spent changes nothing; each refusal leaves the
// link before and is the refusal getObsidianVault names.
func TestAChoiceOfTheFolderOfAVault(t *testing.T) {
	root := t.TempDir()
	first, second := filepath.Join(root, "Северный предел"), filepath.Join(root, "Новое хранилище")
	for _, folder := range []string{first, second} {
		write(t, folder, "Книга 1/01 Застава.md", "Застава.")
	}
	write(t, root, "заметка.md", "Не папка.")
	setup := newChoosing(obsidianvault.Files{})
	ctx := context.Background()

	stale, err := setup.begin.Execute(ctx, "author", "saga")
	if err != nil {
		t.Fatal(err)
	}
	request, err := setup.begin.Execute(ctx, "author", "saga")
	if err != nil || len(request) != 43 || request == stale || setup.shell.titles[1] != "Северный предел" {
		t.Fatalf("a choice = %q %v, titles %q", request, err, setup.shell.titles)
	}
	if state, _ := setup.read.Execute(ctx, "reader", "saga"); !state.Choosing || state.Linked {
		t.Fatalf("while the choice waits = %+v", state)
	}
	setup.pick.Hear(stale, first)
	if result := setup.shell.result(t); result != [2]string{stale, service.OutcomeStale} {
		t.Fatalf("the number put out = %v", result)
	}
	setup.pick.Hear(request, first)
	if result := setup.shell.result(t); result != [2]string{request, service.OutcomeLinked} {
		t.Fatalf("the declared number = %v", result)
	}
	state, err := setup.read.Execute(ctx, "reader", "saga")
	if err != nil || !state.Linked || state.Path != first || state.Name != "Северный предел" || state.Available == nil || !*state.Available || state.Choosing {
		t.Fatalf("the link = %+v, %v", state, err)
	}
	setup.pick.Hear(request, second)
	if result := setup.shell.result(t); result[1] != service.OutcomeStale || setup.vaults.paths["saga"] != first {
		t.Fatalf("a spent number = %v, link %s", result, setup.vaults.paths["saga"])
	}

	for folder, reason := range map[string]obsidian.Reason{
		filepath.Join(root, "Нет"):        obsidian.ReasonNotFound,
		filepath.Join(root, "заметка.md"): obsidian.ReasonNotFolder,
		"относительная":                   obsidian.ReasonNotFound,
	} {
		request, err := setup.begin.Execute(ctx, "author", "saga")
		if err != nil {
			t.Fatal(err)
		}
		setup.pick.Hear(request, folder)
		if result := setup.shell.result(t); result[1] != string(reason) || setup.vaults.paths["saga"] != first {
			t.Fatalf("%s = %v, link %s", folder, result, setup.vaults.paths["saga"])
		}
		if state, _ := setup.read.Execute(ctx, "author", "saga"); state.Refused != reason || state.Path != first {
			t.Fatalf("after the refusal = %+v", state)
		}
	}
	gone, err := setup.begin.Execute(ctx, "author", "saga")
	if err != nil {
		t.Fatal(err)
	}
	setup.clock.pass(service.ChoiceTerm)
	setup.pick.Hear(gone, second)
	if result := setup.shell.result(t); result[1] != service.OutcomeStale || setup.vaults.paths["saga"] != first {
		t.Fatalf("a number gone by = %v", result)
	}
	request, _ = setup.begin.Execute(ctx, "author", "saga")
	setup.pick.Hear(request, second)
	if result := setup.shell.result(t); result[1] != service.OutcomeLinked || setup.vaults.paths["saga"] != second {
		t.Fatalf("a new path = %v, link %s", result, setup.vaults.paths["saga"])
	}
	if state, _ := setup.read.Execute(ctx, "author", "saga"); state.Refused != "" {
		t.Fatalf("a link keeps the refusal before: %+v", state)
	}
	if _, err := setup.begin.Execute(ctx, "reader", "saga"); !errors.Is(err, service.ErrForbidden) {
		t.Fatalf("a reader chooses: %v", err)
	}
	setup.shell.refuse = true
	if _, err := setup.begin.Execute(ctx, "author", "saga"); !errors.Is(err, service.ErrChoiceUnavailable) {
		t.Fatalf("without the shell: %v", err)
	}
	if strings.Contains(setup.log.String(), root) || strings.Contains(setup.log.String(), request) {
		t.Fatalf("the log names a path or a number:\n%s", setup.log.String())
	}
}

// files whose root never opens: a folder on a network that does not answer until the test lets it; opens counts the tries.
type hanging struct {
	release chan struct{}
	opens   *atomic.Int32
}

func newHanging() hanging { return hanging{release: make(chan struct{}), opens: &atomic.Int32{}} }

func (files hanging) Open(string) (service.Vault, error) {
	files.opens.Add(1)
	<-files.release
	return nil, obsidian.FolderError{Reason: obsidian.ReasonUnreadable}
}

// specs/2026-11-13-desk-obsidian.md OB1.1, OB1.3: folder-picked returns at once while the check of the folder hangs within
// its whole time — the loop of the channel goes on to its next message, a shutdown too —; the check ends with the shell
// told how, and a check past its time is a folder that does not read.
func TestTheCheckOfAFolderHoldsNobody(t *testing.T) {
	held := newHanging()
	setup := newChoosing(held)
	setup.pick.CheckTime = time.Minute
	request, err := setup.begin.Execute(context.Background(), "author", "saga")
	if err != nil {
		t.Fatal(err)
	}
	heard := make(chan struct{})
	go func() {
		setup.pick.Hear(request, "/vault/on/a/network")
		close(heard)
	}()
	select {
	case <-heard:
	case <-time.After(time.Second):
		t.Fatal("folder-picked waits for the check of the folder")
	}
	select {
	case result := <-setup.shell.results:
		t.Fatalf("the shell heard %v before the check ended", result)
	case <-time.After(50 * time.Millisecond):
	}
	close(held.release)
	if result := setup.shell.result(t); result != [2]string{request, string(obsidian.ReasonUnreadable)} {
		t.Fatalf("the check that ended = %v", result)
	}

	slow := newHanging()
	defer close(slow.release)
	setup.pick.Files, setup.pick.CheckTime = slow, 50*time.Millisecond
	request, err = setup.begin.Execute(context.Background(), "author", "saga")
	if err != nil {
		t.Fatal(err)
	}
	setup.pick.Hear(request, "/vault/on/a/network")
	if result := setup.shell.result(t); result != [2]string{request, string(obsidian.ReasonUnreadable)} {
		t.Fatalf("a check past its time = %v", result)
	}
}

// specs/2026-11-13-desk-obsidian.md OB1.5: getObsidianVault waits no longer than its time to know whether the vault opens;
// while a check of a vault that does not answer still runs, the link reads as not known and no second check starts.
func TestTheLinkOfAVaultThatDoesNotAnswer(t *testing.T) {
	files := newHanging()
	setup := newChoosing(files)
	setup.read.Wait = 50 * time.Millisecond
	setup.vaults.paths["saga"] = "/vault/on/a/network"
	for range 3 {
		started := time.Now()
		state, err := setup.read.Execute(context.Background(), "author", "saga")
		if err != nil || !state.Linked || state.Available != nil || time.Since(started) > 2*time.Second {
			t.Fatalf("a vault that does not answer = %+v, %v", state, err)
		}
	}
	if opens := files.opens.Load(); opens != 1 {
		t.Fatalf("%d checks of one vault that does not answer", opens)
	}
	close(files.release)
	deadline := time.Now().Add(5 * time.Second)
	for files.opens.Load() < 2 {
		if time.Now().After(deadline) {
			t.Fatal("the check that ended keeps its place")
		}
		state, err := setup.read.Execute(context.Background(), "author", "saga")
		if err != nil || state.Available != nil && *state.Available {
			t.Fatalf("after the check = %+v, %v", state, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
