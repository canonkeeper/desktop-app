// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"canonkeeper/backend/internal/adapters/obsidianvault"
	"canonkeeper/backend/internal/auth"
	"canonkeeper/backend/internal/domain/authormaterials"
	"canonkeeper/backend/internal/domain/obsidian"
	"canonkeeper/backend/internal/domain/sagamemory"
	"canonkeeper/backend/internal/projects"
	obsidianrepo "canonkeeper/backend/internal/repository/postgres/obsidian"
	service "canonkeeper/backend/internal/services/obsidian"
	projectstore "canonkeeper/backend/internal/store/projects"
)

// meanwhile is the names of the book of a test that does something while a load reads them — after its reading, before its
// writes.
type meanwhile func()

func (during meanwhile) BookNames(context.Context, string, []string) (map[string]string, error) {
	during()
	return nil, nil
}

// slowNotes are the files of a vault whose notes answer only when the reading of their operation has ended.
type slowNotes struct{ obsidianvault.Files }

func (files slowNotes) Open(vaultPath string) (service.Vault, error) {
	vault, err := files.Files.Open(vaultPath)
	if err != nil {
		return nil, err
	}
	return slowVault{Vault: vault}, nil
}

type slowVault struct{ service.Vault }

func (vault slowVault) Read(ctx context.Context, notePath string) ([]byte, obsidian.Skip) {
	<-ctx.Done()
	return vault.Vault.Read(context.Background(), notePath)
}

func (core *bridgeCore) records() int {
	return core.count(`SELECT count(*) FROM author_materials WHERE project_id = $1`)
}

func (core *bridgeCore) noteLinks() int {
	return core.count(`SELECT count(*) FROM obsidian_notes WHERE project_id = $1`)
}

// specs/2026-11-15-desk-obsidian-notes.md ON1.1, ON1.5, ON1.8: a store that fails once the links of the notes are written,
// or at the answer of the key — the last write —, leaves neither records, nor links, nor an answer of the key; a folder
// changed since the preview is folder_changed and a reading past its time is slow, nothing written; a caller who goes away
// after the reading does not stop the writes; the saga deleted or its vault unlinked after the reading leaves nothing; the
// replay of a key answers what it answered — before the vault is read —, and writes nothing more.
func TestALoadOfNotesThatFailsLeavesNothingPostgres(t *testing.T) {
	core := newBridgeCore(t, "ck_obsidian_notes_fail")
	core.write(map[string]string{"Персонажи/Ведана.md": "Знахарка.", "Персонажи/Ярослава.md": "Воевода."})
	ctx := context.Background()

	input, _ := core.previewed("notes-fail-0001", characters, obsidian.Decisions{})
	for _, point := range []string{"links", "answer"} {
		broken := obsidianrepo.NewNotes(core.db)
		broken.Failpoint = func(at string) error {
			if at == point {
				return errors.New("the store fails at " + at)
			}
			return nil
		}
		if _, err := core.materialsUseCase(broken, nil).Execute(ctx, input); err == nil {
			t.Fatalf("a store that fails at %s answered", point)
		}
		if records, links := core.records(), core.noteLinks(); records != 0 || links != 0 {
			t.Fatalf("a failure at %s left %d records and %d links", point, records, links)
		}
	}
	if loaded, err := core.materialsUseCase(obsidianrepo.NewNotes(core.db), nil).Execute(ctx, input); err != nil || loaded.Created.Count != 2 {
		t.Fatalf("the same key after the failure = %+v, %v", loaded, err)
	}

	core.write(map[string]string{"Персонажи/Злата.md": "Купчиха."})
	stale, _ := core.previewed("notes-fail-0002", characters, obsidian.Decisions{})
	core.write(map[string]string{"Персонажи/Злата.md": "Купчиха с переправы."})
	var changed service.FolderChanged
	if _, err := core.materialsUseCase(obsidianrepo.NewNotes(core.db), nil).Execute(ctx, stale); !errors.As(err, &changed) ||
		!slices.Equal(changed.Folders, []string{"Персонажи"}) {
		t.Fatalf("a folder changed since the preview = %v", err)
	}
	slow := core.materialsUseCase(obsidianrepo.NewNotes(core.db), nil)
	slow.Files, slow.Limits = slowNotes{}, service.Limits{Read: 200 * time.Millisecond, Note: 5 * time.Second, Write: time.Minute}
	fresh, _ := core.previewed("notes-fail-0003", characters, obsidian.Decisions{})
	if _, err := slow.Execute(ctx, fresh); !errors.Is(err, service.ErrSlow) {
		t.Fatalf("a reading past its time = %v", err)
	}
	if records := core.records(); records != 2 {
		t.Fatalf("%d records after the refusals, want the two of the first load", records)
	}

	// The caller goes away once the vault is read: the writes go to their end and their answer is kept.
	away, cancel := context.WithCancel(ctx)
	defer cancel()
	gone, err := core.materialsUseCase(obsidianrepo.NewNotes(core.db), meanwhile(cancel)).Execute(away, fresh)
	if err != nil || groupNames(gone.Created) != "1 [Злата]" || core.records() != 3 {
		t.Fatalf("a load whose caller went away = %+v, %v", gone, err)
	}
	core.remove("Персонажи/Злата.md")
	replayed, err := core.materialsUseCase(obsidianrepo.NewNotes(core.db), nil).Execute(ctx, fresh)
	if err != nil || groupNames(replayed.Created) != "1 [Злата]" || core.records() != 3 {
		t.Fatalf("the replay of the key after the folder changed = %+v, %v", replayed, err)
	}

	// The vault unlinked and the saga deleted between the reading and the writes.
	core.write(map[string]string{"Персонажи/Ива.md": "Травница."})
	unlinked, _ := core.previewed("notes-fail-0004", characters, obsidian.Decisions{})
	unlink := meanwhile(func() {
		if err := core.vaults.Unlink(ctx, core.author, core.saga); err != nil {
			t.Error(err)
		}
	})
	if _, err := core.materialsUseCase(obsidianrepo.NewNotes(core.db), unlink).Execute(ctx, unlinked); !errors.Is(err, service.ErrNotLinked) {
		t.Fatalf("a vault unlinked meanwhile = %v", err)
	}
	if err := core.vaults.Link(ctx, core.saga, core.vault); err != nil {
		t.Fatal(err)
	}
	deleted, _ := core.previewed("notes-fail-0005", characters, obsidian.Decisions{})
	remove := meanwhile(func() {
		if err := projects.NewService(projectstore.New(core.db), projects.NewSecureIDGenerator(), auth.SystemClock{}).DeleteProject(ctx, core.author, core.saga); err != nil {
			t.Error(err)
		}
	})
	if _, err := core.materialsUseCase(obsidianrepo.NewNotes(core.db), remove).Execute(ctx, deleted); !errors.Is(err, service.ErrNotFound) {
		t.Fatalf("a saga deleted meanwhile = %v", err)
	}
	if records := core.records(); records != 3 {
		t.Fatalf("%d records after the refusals of the writes", records)
	}
}

// ON1.9: the unlink of the vault takes the links of the notes and leaves their records; a new path of the vault keeps them.
func TestTheLinksOfTheNotesGoWithTheVaultPostgres(t *testing.T) {
	core := newBridgeCore(t, "ck_obsidian_notes_links")
	core.write(map[string]string{"Персонажи/Ведана.md": "Знахарка.", "Персонажи/Ярослава.md": "Воевода."})
	core.loadNotes("notes-links-0001", characters, obsidian.Decisions{})
	ctx := context.Background()
	if err := core.vaults.Link(ctx, core.saga, core.vault+"-копия"); err != nil {
		t.Fatal(err)
	}
	if links := core.noteLinks(); links != 2 {
		t.Fatalf("a new path of the vault left %d links", links)
	}
	if err := core.vaults.Unlink(ctx, core.author, core.saga); err != nil {
		t.Fatal(err)
	}
	if links, records := core.noteLinks(), core.records(); links != 0 || records != 2 {
		t.Fatalf("after the unlink: %d links, %d records", links, records)
	}
}

// ON1.7: a load of more than 5 000 records, or one whose new records would bring the saga past 5 000, is obsidian.too_large
// with its numbers, and writes nothing.
func TestALoadPastTheRecordsOfASagaIsRefusedPostgres(t *testing.T) {
	core := newBridgeCore(t, "ck_obsidian_notes_limit")
	heroes := make([]obsidian.Record, obsidian.MaxRecords+1)
	for index := range heroes {
		name := fmt.Sprintf("Герой %04d", index+1)
		heroes[index] = obsidian.Record{Path: "Персонажи/" + name + ".md", Draft: authormaterials.Draft{Name: name, Kind: authormaterials.KindCharacter}}
	}
	write := func(key string, records []obsidian.Record) (service.MaterialsLoaded, error) {
		var keys, paths []string
		for _, record := range records {
			keys, paths = append(keys, sagamemory.GlossaryKey(record.Draft.Name)), append(paths, record.Path)
		}
		facts, err := core.links.Notes(context.Background(), core.saga, keys, paths)
		if err != nil {
			t.Fatal(err)
		}
		seen := map[string]string{}
		for index, state := range obsidian.NoteStates(records, facts, nil, nil) {
			seen[records[index].Path] = state.Seen
		}
		return obsidianrepo.NewNotes(core.db).WriteNotes(context.Background(), service.NotesWrite{
			ActorUserID: core.author, ProjectID: core.saga, Proof: core.proof, Records: records, Seen: seen,
			Key: service.ReplayKey{ActorUserID: core.author, ProjectID: core.saga, Key: key, Request: key},
		})
	}
	var tooMany obsidian.TooManyRecords
	if _, err := write("notes-limit-1", heroes); !errors.As(err, &tooMany) || tooMany != (obsidian.TooManyRecords{Records: obsidian.MaxRecords + 1, Saga: obsidian.MaxRecords + 1}) {
		t.Fatalf("a load of 5 001 records = %v", err)
	}
	if records := core.records(); records != 0 {
		t.Fatalf("a refused load wrote %d records", records)
	}
	if loaded, err := write("notes-limit-2", heroes[:obsidian.MaxRecords]); err != nil || loaded.Created.Count != obsidian.MaxRecords {
		t.Fatalf("a load of 5 000 records = %d, %v", loaded.Created.Count, err)
	}
	if _, err := write("notes-limit-3", heroes[obsidian.MaxRecords:]); !errors.As(err, &tooMany) || tooMany != (obsidian.TooManyRecords{Records: 1, Saga: obsidian.MaxRecords + 1}) {
		t.Fatalf("a record past 5 000 of the saga = %v", err)
	}
}
