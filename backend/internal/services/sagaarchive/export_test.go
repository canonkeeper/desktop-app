// SPDX-License-Identifier: AGPL-3.0-only

package sagaarchive

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"canonkeeper/backend/internal/adapters/tempfiles"
	agentdomain "canonkeeper/backend/internal/domain/agent"
	timeline "canonkeeper/backend/internal/domain/agenttimeline"
	domain "canonkeeper/backend/internal/domain/sagaarchive"
)

const (
	owner   = "00000000-0000-4000-8000-0000000000a1"
	other   = "00000000-0000-4000-8000-0000000000a2"
	bookRef = "11111111-1111-4111-8111-111111111111"
	chapter = "aaaaaaaa-0000-4000-8000-000000000001"
	trashed = "aaaaaaaa-0000-4000-8000-000000000009"
	para    = "bbbbbbbb-0000-4000-8000-000000000001"
)

var at = time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)

// fakeSnapshot is a saga as one transaction sees it; open says the transaction is open.
type fakeSnapshot struct {
	open  bool
	chats []ChatSource
	notes []SnapshotNote
	cards []domain.Card
}

func (fake *fakeSnapshot) Snapshot(_ context.Context, actor, _ string, read func(Snapshot) error) error {
	if actor != owner {
		return ErrForbidden
	}
	fake.open = true
	defer func() { fake.open = false }()
	return read(fake)
}

func (fake *fakeSnapshot) Saga() (domain.Saga, error) {
	return domain.Saga{Name: "Северный предел", BookKeyNumbers: 1}, nil
}

func (fake *fakeSnapshot) Books() ([]SnapshotBook, error) {
	return []SnapshotBook{{Book: domain.Book{Ref: bookRef, KeyNumber: 1, Order: 1, Title: "Перевал"}}}, nil
}

func (fake *fakeSnapshot) Chapters(each func(domain.Chapter) error) error { return each(fakeChapter()) }

func fakeChapter() domain.Chapter {
	return domain.Chapter{Ref: chapter, Book: bookRef, Order: 1, Title: "Глава 1", Paragraphs: []domain.Paragraph{
		{ID: para, Order: 1, Kind: "paragraph", Markdown: strings.Repeat("Мара шла на север. ", 4000)},
	}}
}

func (fake *fakeSnapshot) Cards(each func(domain.Card) error) error {
	for _, card := range fake.cards {
		if err := each(card); err != nil {
			return err
		}
	}
	return nil
}
func (fake *fakeSnapshot) Names() (*domain.Names, error) { return nil, nil }
func (fake *fakeSnapshot) Layer() (*Layer, error)        { return nil, nil }
func (fake *fakeSnapshot) MapHidden() ([]domain.MapHidden, error) {
	return nil, nil
}
func (fake *fakeSnapshot) Materials() ([]domain.Material, error) { return nil, nil }

func (fake *fakeSnapshot) Memory() ([]domain.Memory, error) {
	return []domain.Memory{{Kind: "plan", Body: "Маяк погаснет.", Chapter: trashed, Origin: "drawing", CreatedAt: at, UpdatedAt: at}}, nil
}

func (fake *fakeSnapshot) Notes(string) ([]SnapshotNote, error) { return fake.notes, nil }
func (fake *fakeSnapshot) Transcripts() ([]Transcript, error)   { return nil, nil }
func (fake *fakeSnapshot) Chats(string) ([]ChatSource, error)   { return fake.chats, nil }
func (fake *fakeSnapshot) LeftOut() (domain.LeftOut, error) {
	return domain.LeftOut{Drafts: 2, TrashedBooks: 1, TrashedChapters: 1}, nil
}

// threadsOf answers the thread of a chat by its id; a thread named broken cannot be read.
type threadsOf map[string][]timeline.RawItem

func (threads threadsOf) ThreadItems(_ context.Context, _ agentdomain.Flow, thread string) ([]timeline.RawItem, error) {
	if thread == "broken" {
		return nil, errors.New("the app-server is gone")
	}
	return threads[thread], nil
}

func tempDirectory(t *testing.T) *tempfiles.Directory {
	t.Helper()
	directory, err := tempfiles.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return directory
}

type directoryPort struct{ directory *tempfiles.Directory }

func (port directoryPort) Create() (TempFile, error) { return port.directory.Create() }

func newExport(snapshot SnapshotStore, threads Threads, temp TempFiles) *ExportSaga {
	return &ExportSaga{Snapshot: snapshot, Threads: threads, Temp: temp, Gate: NewGate(nil), CreatedBy: "CanonKeeper 0.1.0", Now: time.Now}
}

// watcher is a slow reader: it says whether the transaction was open when the first byte came.
type watcher struct {
	snapshot *fakeSnapshot
	first    bool
	openThen bool
	bytes.Buffer
}

func (watch *watcher) Write(data []byte) (int, error) {
	if !watch.first {
		watch.first, watch.openThen = true, watch.snapshot.open
	}
	time.Sleep(time.Millisecond)
	return watch.Buffer.Write(data)
}

// specs/2026-11-13-desk-sagafile.md SF2.2, SF2.6: the transaction of the base is closed before the first byte goes to a
// slow reader, and no temporary file is left after the answer.
func TestTheSnapshotIsClosedBeforeTheFirstByte(t *testing.T) {
	snapshot := &fakeSnapshot{}
	directory := tempDirectory(t)
	export := newExport(snapshot, threadsOf{}, directoryPort{directory})
	prepared, err := export.Prepare(context.Background(), ExportInput{ActorUserID: owner, ProjectID: "saga"})
	if err != nil {
		t.Fatal(err)
	}
	reader := &watcher{snapshot: snapshot}
	if err := prepared.WriteTo(context.Background(), reader); err != nil {
		t.Fatal(err)
	}
	prepared.Close()
	if !reader.first || reader.openThen {
		t.Fatalf("the first byte came while the transaction was open: %v", reader.openThen)
	}
	if entries, err := os.ReadDir(directory.Path()); err != nil || len(entries) != 0 {
		t.Fatalf("the directory after the answer = %v, %v; want it empty", entries, err)
	}
	archive, err := domain.Read(bytes.NewReader(reader.Bytes()), int64(reader.Len()))
	if err != nil {
		t.Fatalf("the file of the save: %v", err)
	}
	// SF1.7, SF1.9: a record about a chapter in «Недавно удалённые» goes without its chapter; what is left out is counted.
	if len(archive.Memory) != 1 || archive.Memory[0].Chapter != "" || archive.Memory[0].Origin != "drawing" {
		t.Fatalf("the memory of the file = %+v", archive.Memory)
	}
	if archive.Manifest.LeftOut != (domain.LeftOut{Drafts: 2, TrashedBooks: 1, TrashedChapters: 1}) || archive.Manifest.CreatedBy != "CanonKeeper 0.1.0" {
		t.Fatalf("the manifest = %+v", archive.Manifest)
	}
}

// failing is a temporary file whose reading breaks after its first bytes.
type failing struct{ TempFile }

func (file failing) ReadAt(data []byte, offset int64) (int, error) {
	if offset > 0 {
		return 0, errors.New("the disk is gone")
	}
	return file.TempFile.ReadAt(data[:min(len(data), 64)], offset)
}

type failingPort struct{ directory *tempfiles.Directory }

func (port failingPort) Create() (TempFile, error) {
	file, err := port.directory.Create()
	return failing{file}, err
}

// SF2.3: a failure to read the temporary file after the first byte is an error of the save, and what the reader got is a
// damaged file.
func TestAFailureAfterTheFirstByteLeavesADamagedFile(t *testing.T) {
	export := newExport(&fakeSnapshot{}, threadsOf{}, failingPort{tempDirectory(t)})
	prepared, err := export.Prepare(context.Background(), ExportInput{ActorUserID: owner, ProjectID: "saga"})
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Close()
	var got bytes.Buffer
	if err := prepared.WriteTo(context.Background(), &got); err == nil {
		t.Fatal("the save ended well over a broken disk")
	}
	_, err = domain.Read(bytes.NewReader(got.Bytes()), int64(got.Len()))
	var refusal *domain.Refusal
	if got.Len() == 0 || !errors.As(err, &refusal) || refusal.Code != domain.CodeDamaged {
		t.Fatalf("what the reader got = %d bytes, %v; want a damaged file", got.Len(), err)
	}
}

func items(t *testing.T, turn, question, answer string) []timeline.RawItem {
	t.Helper()
	assignment, err := json.Marshal(map[string]string{"question": question, "projectRef": "project", "sourceView": "published", "scope": "project"})
	if err != nil {
		t.Fatal(err)
	}
	asked, err := json.Marshal(map[string]any{"type": "userMessage", "id": "user-" + turn, "content": []map[string]string{{"type": "text", "text": string(assignment)}}})
	if err != nil {
		t.Fatal(err)
	}
	answered, err := json.Marshal(map[string]string{"type": "agentMessage", "id": "agent-" + turn, "text": answer, "phase": "final_answer"})
	if err != nil {
		t.Fatal(err)
	}
	return []timeline.RawItem{
		{Role: timeline.RoleMain, TurnID: turn, TurnStatus: timeline.TurnStatusCompleted, Item: asked},
		{Role: timeline.RoleMain, TurnID: turn, TurnStatus: timeline.TurnStatusCompleted, Item: answered},
	}
}

// SF1.8: the turns of the owner go in, the turns of another member, his correction and the title of a chat he began do not;
// a chat without a turn of the owner is not in the file; a thread that cannot be read is named in chatsNotRead.
func TestTheChatsOfTheFileAreTheTurnsOfTheOwner(t *testing.T) {
	threads := threadsOf{
		"mine":   append(items(t, "t1", "Кто ждёт Мару?", "Тарен."), items(t, "t2", "Тайное имя второго", "Ответ второму.")...),
		"theirs": append(items(t, "t3", "Мой вопрос второго", "Ответ."), items(t, "t4", "Вопрос владельца", "Ответ владельцу.")...),
	}
	snapshot := &fakeSnapshot{chats: []ChatSource{
		{ID: "c1", Title: "Кто ждёт Мару", ThreadID: "mine", CreatedAt: at, Turns: []TurnSource{
			{ProviderTurnID: "t1", AskedAt: at, Mine: true, Attached: "Набросок"}, {ProviderTurnID: "t2", AskedAt: at.Add(time.Minute)},
		}},
		{ID: "c2", Title: "Мой вопрос второго", ThreadID: "theirs", CreatedAt: at, Turns: []TurnSource{
			{ProviderTurnID: "t3", AskedAt: at}, {ProviderTurnID: "t4", AskedAt: at.Add(time.Minute), Mine: true},
		}},
		{ID: "c3", Title: "Чужой чат", ThreadID: "theirs", CreatedAt: at, Turns: []TurnSource{{ProviderTurnID: "t3", AskedAt: at}}},
		{ID: "c4", Title: "Сломанный", ThreadID: "broken", CreatedAt: at, Turns: []TurnSource{{ProviderTurnID: "t9", AskedAt: at, Mine: true}}},
	}, notes: []SnapshotNote{
		{Title: "Из чата", Kind: domain.NoteSourceChat, Quote: "Тарен — брат.", Body: "Проверить.", CreatedAt: at, UpdatedAt: at},
		{Title: "О удалённой", Kind: domain.NoteSourceChapter, ChapterID: trashed, Quote: "Маяк горел.", CreatedAt: at, UpdatedAt: at},
		{Title: "О прежней правке", Kind: domain.NoteSourceChapter, ChapterID: chapter, ParagraphID: "bbbbbbbb-0000-4000-8000-0000000000ff", Quote: "Мара шла", CreatedAt: at, UpdatedAt: at},
	}}
	export := newExport(snapshot, threads, directoryPort{tempDirectory(t)})
	prepared, err := export.Prepare(context.Background(), ExportInput{ActorUserID: owner, ProjectID: "saga"})
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Close()
	var file bytes.Buffer
	if err := prepared.WriteTo(context.Background(), &file); err != nil {
		t.Fatal(err)
	}
	archive, err := domain.Read(bytes.NewReader(file.Bytes()), int64(file.Len()))
	if err != nil {
		t.Fatal(err)
	}
	var questions []string
	if err := archive.EachChat(func(entry domain.ChatEntry, transcript domain.ChatTranscript) error {
		for _, turn := range transcript.Turns {
			questions = append(questions, entry.Title+": "+turn.Question+" → "+turn.Answer+" ["+turn.Attached+"]")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	want := []string{"Кто ждёт Мару: Кто ждёт Мару? → Тарен. [Набросок]", "Чат от 13.11.2026: Вопрос владельца → Ответ владельцу. []"}
	if strings.Join(questions, "\n") != strings.Join(want, "\n") {
		t.Fatalf("the chats of the file:\n%s\nwant\n%s", strings.Join(questions, "\n"), strings.Join(want, "\n"))
	}
	if strings.Contains(file.String(), "Тайное имя второго") || strings.Contains(file.String(), "Мой вопрос второго") || strings.Contains(file.String(), "Чужой чат") {
		t.Fatal("the words of another member went into the file")
	}
	if len(archive.Manifest.ChatsNotRead) != 1 || archive.Manifest.ChatsNotRead[0] != "Сломанный" || archive.Manifest.LeftOut.OthersTurns != 3 {
		t.Fatalf("the manifest = %+v", archive.Manifest)
	}
	// SF1.7: a quote of a chat stays its source; a note of a chapter the file does not carry keeps its quote in its body; a
	// paragraph of an earlier revision goes, the chapter stays.
	notes := archive.Notes
	if len(notes) != 3 || notes[0].Source.Kind != domain.NoteSourceChat || notes[1].Source != nil || !strings.HasPrefix(notes[1].Body, "> Маяк горел.") ||
		notes[2].Source.Chapter != chapter || notes[2].Source.Paragraph != "" {
		t.Fatalf("the notes of the file = %+v", notes)
	}
}

// save saves the saga of a snapshot and reads its file back.
func save(t *testing.T, snapshot *fakeSnapshot) *domain.Archive {
	t.Helper()
	prepared, err := newExport(snapshot, threadsOf{}, directoryPort{tempDirectory(t)}).Prepare(context.Background(), ExportInput{ActorUserID: owner, ProjectID: "saga"})
	if err != nil {
		t.Fatalf("the save of the saga: %v", err)
	}
	defer prepared.Close()
	var file bytes.Buffer
	if err := prepared.WriteTo(context.Background(), &file); err != nil {
		t.Fatal(err)
	}
	archive, err := domain.Read(bytes.NewReader(file.Bytes()), int64(file.Len()))
	if err != nil {
		t.Fatalf("the reader refused the file of the save: %v", err)
	}
	return archive
}

// SF1.4: the card of the text the file carries goes into it; a card of another text of the chapter — a card the cards Job
// has not yet replaced — does not, so the chapter has one card in the file.
func TestOnlyTheCardOfTheTextOfTheFileGoes(t *testing.T) {
	digest, ok := domain.ContentHash(fakeChapter().Paragraphs)
	if !ok {
		t.Fatal("the digest of the chapter")
	}
	stale := strings.Repeat("ab", 32)
	card := func(hash, summary string) domain.Card {
		return domain.Card{Chapter: chapter, ContentHash: hash, Version: 4, Model: "cards-model", Card: json.RawMessage(`{"summary":"` + summary + `"}`)}
	}
	archive := save(t, &fakeSnapshot{cards: []domain.Card{card(stale, "Прежний текст."), card(digest, "Мара идёт на север.")}})
	if len(archive.Cards) != 1 || archive.Cards[0].ContentHash != digest || !archive.Cards[0].Accepted {
		t.Fatalf("the cards of the file = %+v; want the one of its text", archive.Cards)
	}
}

// SF1.7, SF1.10: a note on a chapter of «Недавно удалённые» whose thought is as long as a note may be is saved: its quote
// gives way to the thought.
func TestALongThoughtOnAChapterOfTheTrashIsSaved(t *testing.T) {
	thought := strings.Repeat("м", 99_998)
	archive := save(t, &fakeSnapshot{notes: []SnapshotNote{
		{Title: "О удалённой", Kind: domain.NoteSourceChapter, ChapterID: trashed, Quote: "Маяк горел.", Body: thought, CreatedAt: at, UpdatedAt: at},
	}})
	if len(archive.Notes) != 1 || archive.Notes[0].Source != nil || !strings.HasSuffix(archive.Notes[0].Body, thought) {
		t.Fatalf("the note of the file = %d notes", len(archive.Notes))
	}
}

// SF2.4: one save of an author at a time — another author's goes; four for the process; ten an hour for an author.
func TestTheGateKeepsTheLimitsOfTheSaves(t *testing.T) {
	now := at
	gate := NewGate(func() time.Time { return now })
	release, _, ok := gate.Enter(owner)
	if !ok {
		t.Fatal("the first save waits")
	}
	if _, wait, ok := gate.Enter(owner); ok || wait <= 0 {
		t.Fatalf("a second save of the author = %v, %v; want it to wait", ok, wait)
	}
	otherRelease, _, ok := gate.Enter(other)
	if !ok {
		t.Fatal("the save of another author waits")
	}
	var releases []func()
	for index := 0; index < 2; index++ {
		next, _, ok := gate.Enter(string(rune('x' + index)))
		if !ok {
			t.Fatalf("save %d of the process waits", index+3)
		}
		releases = append(releases, next)
	}
	if _, _, ok := gate.Enter("fifth"); ok {
		t.Fatal("a fifth save goes at once")
	}
	release()
	otherRelease()
	for _, next := range releases {
		next()
	}
	for index := 1; index < MaxSavesPerHour; index++ {
		next, _, ok := gate.Enter(owner)
		if !ok {
			t.Fatalf("save %d of the hour waits", index+1)
		}
		next()
	}
	if _, wait, ok := gate.Enter(owner); ok || wait <= 0 || wait > time.Hour {
		t.Fatalf("an eleventh save of the hour = %v, %v", ok, wait)
	}
	now = now.Add(time.Hour)
	if _, _, ok := gate.Enter(owner); !ok {
		t.Fatal("a save an hour later waits")
	}
}

// SF2.6: the start clears the directory a fallen process left its files in.
func TestTheStartClearsTheDirectory(t *testing.T) {
	directory := tempDirectory(t)
	file, err := directory.Create()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(file, "a file a fallen process left"); err != nil {
		t.Fatal(err)
	}
	if err := directory.Clear(); err != nil {
		t.Fatal(err)
	}
	if entries, err := os.ReadDir(directory.Path()); err != nil || len(entries) != 0 {
		t.Fatalf("the directory after the start = %v, %v", entries, err)
	}
}
