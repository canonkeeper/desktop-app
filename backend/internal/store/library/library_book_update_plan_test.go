// SPDX-License-Identifier: AGPL-3.0-only

package librarystore

import (
	"context"
	"errors"
	"testing"
	"time"

	"canonkeeper/backend/internal/auth"
	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/library"
	"canonkeeper/backend/internal/projects"
	jobstore "canonkeeper/backend/internal/store/jobs"
)

// specs/2026-11-14-desk-obsidian-books.md OK0.4, OK2.2: UpdateChanges says what the update of the book by a file would
// write — nothing for the same text, its spaces aside; something for a word, a sign, italic or bold alone, a title in
// another case, chapters in another order, a new chapter and a chapter gone. It writes nothing.
func TestUpdateChangesSaysWhatTheUpdateWouldWrite(t *testing.T) {
	ctx := context.Background()
	db := openCurrentStoreTestDB(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	var userID, projectID, membershipID string
	if err := db.Pool.QueryRow(ctx, `SELECT gen_random_uuid()::text, gen_random_uuid()::text, gen_random_uuid()::text`).
		Scan(&userID, &projectID, &membershipID); err != nil {
		t.Fatal(err)
	}
	proof := insertProjectStoreUserWithSessionForTest(t, db, userID, "plan@example.test")
	insertImportStoreProjectForTest(t, db, projectID, userID, "Северный предел", now)
	insertProjectStoreMembershipForTest(t, db, membershipID, projectID, userID, projects.RoleOwner, now, false)
	store, jobsStore := New(db), jobstore.New(db)
	service := library.NewService(store, library.NewSecureIDGenerator(), auth.SystemClock{})
	const text = "# Застава\n\n# Глава 1\n\nОльга вышла к заставе.\n\n# Глава 2\n\nМирон ждал у переправы.\n"
	if _, err := service.CreateImportJob(ctx, library.Actor{UserID: userID}, projectID, library.CreateImportJobInput{
		SourceFileName: "Застава.md", SizeBytes: int64(len(text)), FileBytes: []byte(text), IdempotencyKey: "plan-" + membershipID,
		RequestMethod: "POST", RequestPath: "/projects/" + projectID + "/imports", SessionWriteProof: proof,
	}); err != nil {
		t.Fatal(err)
	}
	decision, err := newWorkerClaimAuthorityForTest(db).ClaimNextQueuedJobForWorker(ctx, []jobs.Kind{jobs.KindImport})
	if err != nil {
		t.Fatal(err)
	}
	job, err := jobsStore.LoadJobForWorkerClaim(ctx, decision.Claim)
	if err != nil {
		t.Fatal(err)
	}
	if result, err := library.NewImportHandler(store, store).HandleResult(ctx, job); err != nil || result.Status != jobs.StatusSucceeded {
		t.Fatalf("the import = %+v, %v", result, err)
	}
	var bookID string
	if err := db.Pool.QueryRow(ctx, `SELECT id::text FROM books WHERE project_id = $1`, projectID).Scan(&bookID); err != nil {
		t.Fatal(err)
	}
	changes := func(text string) bool {
		t.Helper()
		volume, err := library.CheckQueueVolume("Застава.md", []byte(text))
		if err != nil || volume.Refusal != "" {
			t.Fatalf("the file = %+v, %v", volume, err)
		}
		changed, err := store.UpdateChanges(ctx, projectID, bookID, volume.Book.Chapters)
		if err != nil {
			t.Fatal(err)
		}
		return changed
	}
	for name, test := range map[string]struct {
		text string
		want bool
	}{
		"the same text":           {text, false},
		"the same text in spaces": {"# Застава\n\n# Глава 1\n\nОльга  вышла к заставе.\n\n# Глава 2\n\nМирон ждал у переправы.\n", false},
		"a word":                  {"# Застава\n\n# Глава 1\n\nОльга вышла к башне.\n\n# Глава 2\n\nМирон ждал у переправы.\n", true},
		"a sign":                  {"# Застава\n\n# Глава 1\n\nОльга вышла к заставе!\n\n# Глава 2\n\nМирон ждал у переправы.\n", true},
		"italic alone":            {"# Застава\n\n# Глава 1\n\nОльга *вышла* к заставе.\n\n# Глава 2\n\nМирон ждал у переправы.\n", true},
		"a title in another case": {"# Застава\n\n# глава 1\n\nОльга вышла к заставе.\n\n# Глава 2\n\nМирон ждал у переправы.\n", true},
		"another order":           {"# Застава\n\n# Глава 2\n\nМирон ждал у переправы.\n\n# Глава 1\n\nОльга вышла к заставе.\n", true},
		"a new chapter":           {text + "\n# Глава 3\n\nПаром пришёл.\n", true},
		"a chapter gone":          {"# Застава\n\n# Глава 1\n\nОльга вышла к заставе.\n", true},
	} {
		if got := changes(test.text); got != test.want {
			t.Errorf("%s: changes = %v, want %v", name, got, test.want)
		}
	}
	var revisions int
	if err := db.Pool.QueryRow(ctx, `SELECT sum(revision) FROM chapters WHERE book_id = $1`, bookID).Scan(&revisions); err != nil || revisions != 2 {
		t.Fatalf("the plan wrote: revisions %d, %v", revisions, err)
	}
	if _, err := store.UpdateChanges(ctx, projectID, "10000000-0000-4000-8000-0000000000ff", nil); !errors.Is(err, library.ErrNotFound) {
		t.Fatalf("a book that is not = %v", err)
	}
}
