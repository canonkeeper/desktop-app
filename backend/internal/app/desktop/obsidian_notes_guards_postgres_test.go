// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"context"
	"errors"
	"testing"
	"time"

	"canonkeeper/backend/internal/auth"
	"canonkeeper/backend/internal/domain/authormaterials"
	"canonkeeper/backend/internal/domain/obsidian"
	"canonkeeper/backend/internal/domain/sagamemory"
	"canonkeeper/backend/internal/projects"
	materialsrepo "canonkeeper/backend/internal/repository/postgres/authormaterials"
	obsidianrepo "canonkeeper/backend/internal/repository/postgres/obsidian"
	service "canonkeeper/backend/internal/services/obsidian"
	authstore "canonkeeper/backend/internal/store/auth"
	jobstore "canonkeeper/backend/internal/store/jobs"
	projectstore "canonkeeper/backend/internal/store/projects"
)

// specs/2026-11-15-desk-obsidian-notes.md ON1.8: the key of a load asked again with another request — other decisions — is
// refused before the vault is read, and so is a transaction of the key with another request; nothing more is written.
func TestTheKeyOfALoadIsTheKeyOfOneRequestPostgres(t *testing.T) {
	core := newBridgeCore(t, "ck_obsidian_notes_key")
	core.write(map[string]string{"Персонажи/Ведана.md": "Знахарка.", "Персонажи/Перевал.md": "Прозвище."})
	core.byHand("Перевал")
	input, _ := core.previewed("notes-key-0001", characters, obsidian.Decisions{})
	if _, err := core.execute(input); err != nil {
		t.Fatal(err)
	}
	revisions := core.revisions()
	other := input
	other.Decisions = replaceTaken
	if _, err := core.execute(other); !errors.Is(err, service.ErrReplayConflict) {
		t.Fatalf("the key with other decisions = %v", err)
	}
	if _, err := obsidianrepo.NewNotes(core.db).WriteNotes(context.Background(), service.NotesWrite{
		ActorUserID: core.author, ProjectID: core.saga, Proof: core.proof,
		Key: service.ReplayKey{ActorUserID: core.author, ProjectID: core.saga, Key: input.Key, Request: "another request"},
	}); !errors.Is(err, service.ErrReplayConflict) {
		t.Fatalf("a transaction of the key with another request = %v", err)
	}
	if core.revisions() != revisions || core.records() != 2 {
		t.Fatalf("the refusals wrote: %s", core.revisions())
	}
}

// waiting waits until a write of the test stops on a lock of the database of the test, or ends: it says whether it stopped.
func (core *bridgeCore) waiting(done <-chan error) (bool, error) {
	core.t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-done:
			return false, err
		default:
		}
		var waits int
		if err := core.db.Pool.QueryRow(context.Background(), `
			SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock'
		`).Scan(&waits); err != nil {
			core.t.Fatal(err)
		}
		if waits > 0 {
			return true, nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	core.t.Fatal("the write of the test neither waits nor ends")
	return false, nil
}

// ON1.3, ON1.5: the races of a load with the author. A name the author gives a record between the facts of the load and its
// rename falls on the uniqueness of the names: conflict, nothing written, and the same key then keeps the note and names it.
// A record the author edits while the load holds it waits for the load — the rows the load reads are locked —, the load
// writes it by what it read, and the edit is refused as made over another revision.
func TestARaceOfALoadWithTheAuthorPostgres(t *testing.T) {
	core := newBridgeCore(t, "ck_obsidian_notes_race")
	core.write(map[string]string{"Персонажи/А.md": "---\ntitle: Ведана\n---\nЗнахарка.", "Персонажи/Ольга.md": "Старшая на заставе."})
	core.loadNotes("notes-race-0001", characters, obsidian.Decisions{})
	before := core.materials()

	core.write(map[string]string{"Персонажи/А.md": "---\ntitle: Злата\n---\nЗнахарка."})
	renamed, _ := core.previewed("notes-race-0002", characters, obsidian.Decisions{})
	racing := obsidianrepo.NewNotes(core.db)
	racing.Failpoint = func(point string) error {
		if point == "facts" {
			core.byHand("Злата")
		}
		return nil
	}
	if _, err := core.materialsUseCase(racing, nil).Execute(context.Background(), renamed); !errors.Is(err, service.ErrConflict) {
		t.Fatalf("a name the author takes during the rename = %v", err)
	}
	if record := core.materials()["Ведана"]; record.ID != before["Ведана"].ID || record.Revision != before["Ведана"].Revision || core.noteLinks() != 2 {
		t.Fatalf("the race wrote %+v; %d links", record, core.noteLinks())
	}
	if again, err := core.execute(renamed); err != nil || groupNames(again.Kept.Changed) != "1 [Злата]" {
		t.Fatalf("the same key after the race = %+v, %v", again, err)
	}

	core.write(map[string]string{"Персонажи/Ольга.md": "Старшая на заставе, носит отцовский меч."})
	changed, _ := core.previewed("notes-race-0003", characters, obsidian.Decisions{})
	olga := before["Ольга"]
	edit := make(chan error, 1)
	var stopped bool
	holding := obsidianrepo.NewNotes(core.db)
	holding.Failpoint = func(point string) error {
		if point != "facts" {
			return nil
		}
		go func() {
			draft := olga.Draft
			draft.Description = "Правлено во время загрузки."
			_, err := materialsrepo.New(core.db, jobstore.New(core.db)).UpdateAuthorMaterial(context.Background(), core.materialsWrite(), olga.ID,
				olga.Revision, draft, sagamemory.GlossaryKey(draft.Name))
			edit <- err
		}()
		var err error
		if stopped, err = core.waiting(edit); !stopped {
			edit <- err
		}
		return nil
	}
	loaded, err := core.materialsUseCase(holding, nil).Execute(context.Background(), changed)
	if err != nil || !stopped || groupNames(loaded.Updated) != "1 [Ольга]" {
		t.Fatalf("a load during an edit of the author = %+v, %v; the edit waited: %v", loaded, err, stopped)
	}
	if err := <-edit; !errors.Is(err, authormaterials.ErrConflict) {
		t.Fatalf("the edit of the author after the load = %v", err)
	}
	if record := core.materials()["Ольга"]; record.Description != "Старшая на заставе, носит отцовский меч." || record.Revision != olga.Revision+1 {
		t.Fatalf("the record after the race = %+v", record)
	}
}

// member is an author the owner of the saga invites with a role, with the proof of his session and what ends his sessions.
type member struct {
	id, membership string
	proof          auth.SessionWriteProof
	logout         func() error
}

func (core *bridgeCore) invite(email string, role projects.Role) member {
	core.t.Helper()
	ctx := context.Background()
	accounts := auth.NewService(authstore.New(core.db), auth.NewSecureIDGenerator(), auth.SystemClock{})
	user, err := accounts.CreateUser(ctx, auth.NewUser{Email: email, DisplayName: email, EmailVerified: true})
	if err != nil {
		core.t.Fatal(err)
	}
	session, csrf, err := accounts.CreateSession(ctx, auth.NewSession{UserID: user.ID, ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		core.t.Fatal(err)
	}
	_, proof, err := accounts.AuthenticateWrite(ctx, session.CookieValue, csrf)
	if err != nil {
		core.t.Fatal(err)
	}
	sagas := projects.NewService(projectstore.New(core.db), projects.NewSecureIDGenerator(), auth.SystemClock{})
	invitation, err := sagas.CreateProjectInvitation(ctx, core.author, core.saga, projects.CreateProjectInvitationInput{Email: email, Role: role})
	if err != nil {
		core.t.Fatal(err)
	}
	membership, err := sagas.AcceptProjectInvitation(ctx, projects.AuthenticatedUser{ID: user.ID, Email: email, EmailVerified: true}, invitation.ID)
	if err != nil {
		core.t.Fatal(err)
	}
	return member{id: user.ID, membership: membership.ID, proof: proof, logout: func() error {
		return authstore.New(core.db).RevokeUserSessions(ctx, user.ID, time.Now())
	}}
}

// ON1.5: the transaction of a load checks the role of an editor and the proof of the session itself — an editor made a
// reader, or whose sessions ended, after the reading of the load writes nothing.
func TestTheRoleAndTheSessionOfALoadAreCheckedInItsTransactionPostgres(t *testing.T) {
	core := newBridgeCore(t, "ck_obsidian_notes_role")
	core.write(map[string]string{"Персонажи/Ведана.md": "Знахарка."})
	editor := core.invite("editor@example.test", projects.RoleEditor)
	sagas := projects.NewService(projectstore.New(core.db), projects.NewSecureIDGenerator(), auth.SystemClock{})
	role := func(to projects.Role) {
		if _, err := sagas.UpdateProjectMemberRole(context.Background(), core.author, core.saga, editor.membership, projects.UpdateProjectMemberRoleInput{Role: to}); err != nil {
			t.Error(err)
		}
	}
	input, _ := core.previewed("notes-role-0001", characters, obsidian.Decisions{})
	input.ActorUserID, input.Proof = editor.id, editor.proof
	reader := meanwhile(func() { role(projects.RoleViewer) })
	if _, err := core.materialsUseCase(obsidianrepo.NewNotes(core.db), reader).Execute(context.Background(), input); !errors.Is(err, service.ErrForbidden) {
		t.Fatalf("an editor made a reader after the reading = %v", err)
	}
	role(projects.RoleEditor)
	away := meanwhile(func() {
		if err := editor.logout(); err != nil {
			t.Error(err)
		}
	})
	if _, err := core.materialsUseCase(obsidianrepo.NewNotes(core.db), away).Execute(context.Background(), input); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Fatalf("an editor whose sessions ended after the reading = %v", err)
	}
	if records, links := core.records(), core.noteLinks(); records != 0 || links != 0 {
		t.Fatalf("the refusals wrote %d records and %d links", records, links)
	}
}

// ON1.2, ON1.4 (one rule on both sides): of two notes of one name in two folders the load gives the name to the first by the
// order of the paths, whatever the order of the folders of the choice; a name the record of another note of the load keeps is
// never replaced, the rule «replace» or not.
func TestTheLoadGivesANameByTheOrderOfThePathsPostgres(t *testing.T) {
	core := newBridgeCore(t, "ck_obsidian_notes_order")
	core.write(map[string]string{"Персонажи/Ольга.md": "Сестра Мирона.", "Герои/Ольга.md": "Старшая на заставе."})
	both := obsidian.Choice{Materials: []obsidian.MaterialFolder{
		{Path: "Персонажи", Kind: authormaterials.KindCharacter}, {Path: "Герои", Kind: authormaterials.KindCharacter},
	}}
	first := core.loadNotes("notes-order-0001", both, obsidian.Decisions{})
	record := core.materials()["Ольга"]
	if groupNames(first.Created) != "1 [Ольга]" || groupNames(first.Kept.Taken) != "1 [Ольга]" || record.SourceName != "Герои/Ольга.md" {
		t.Fatalf("the first load = %+v; the record %+v", first, record)
	}
	replaced := core.loadNotes("notes-order-0002", both, replaceTaken)
	after := core.materials()["Ольга"]
	if groupNames(replaced.Kept.Taken) != "1 [Ольга]" || replaced.Replaced.Count != 0 || after.Revision != record.Revision || after.SourceName != "Герои/Ольга.md" {
		t.Fatalf("a load with the rule to replace = %+v; the record %+v", replaced, after)
	}
	if id, _ := core.linkOf("Герои/Ольга.md"); id != record.ID {
		t.Fatalf("the record is linked with another note: %q", id)
	}
}
