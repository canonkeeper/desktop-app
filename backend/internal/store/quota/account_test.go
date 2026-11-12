package quotastore

import (
	"context"
	"testing"
	"time"

	"canonkeeper/backend/internal/domain/quota"
	storedb "canonkeeper/backend/internal/store/db"
	"canonkeeper/backend/internal/testutil"
)

const (
	quotaWriterA  = "93000000-0000-4000-8000-00000000000a"
	quotaWriterB  = "93000000-0000-4000-8000-00000000000b"
	quotaProjectA = "93000000-0000-4000-8000-0000000000a1"
	quotaProjectB = "93000000-0000-4000-8000-0000000000b1"
)

type sourceSeed struct {
	t   *testing.T
	db  *storedb.DB
	now time.Time
	seq int
}

func (seed *sourceSeed) exec(query string, args ...any) {
	seed.t.Helper()
	if _, err := seed.db.Pool.Exec(context.Background(), query, args...); err != nil {
		seed.t.Fatal(err)
	}
}

func (seed *sourceSeed) id() string {
	seed.seq++
	return "93000000-0000-4000-9000-" + padID(seed.seq)
}

func padID(value int) string {
	digits := []byte("000000000000")
	for index := len(digits) - 1; value > 0 && index >= 0; index-- {
		digits[index] = byte('0' + value%10)
		value /= 10
	}
	return string(digits)
}

func (seed *sourceSeed) user(id, email string) {
	seed.exec(`INSERT INTO users (id,email,display_name,password_hash,email_verified_at) VALUES ($1,$2,'Quota','hash',$3)`, id, email, seed.now)
}

func (seed *sourceSeed) project(id, owner string) {
	seed.exec(`INSERT INTO projects (id,owner_user_id,name,description,created_at,updated_at) VALUES ($1,$2,'Quota','',$3,$3)`, id, owner, seed.now)
	seed.exec(`INSERT INTO project_memberships (id,project_id,user_id,role,created_at,updated_at) VALUES ($1,$2,$3,'owner',$4,$4)`, seed.id(), id, owner, seed.now)
}

// book adds a book whose chapters hold one paragraph each of the given lengths in both text
// versions.
func (seed *sourceSeed) book(projectID string, deleted bool, chapterLengths ...int) string {
	bookID := seed.id()
	var deletedAt any
	if deleted {
		deletedAt = seed.now
	}
	seed.exec(`INSERT INTO books (id,project_id,title,"order",display_number,status,revision,deleted_at,
		governance_retention_started_at,purge_after,purge_deadline,created_at,updated_at)
		VALUES ($1,$2,'Book',$3,'1','draft',1,$4::timestamptz,$4::timestamptz,$4::timestamptz + interval '7 days',
		$4::timestamptz + interval '8 days',$5,$5)`, bookID, projectID, seed.seq, deletedAt, seed.now)
	for index, length := range chapterLengths {
		chapterID := seed.id()
		seed.exec(`INSERT INTO chapters (id,project_id,book_id,title,"order",status,revision,created_at,updated_at) VALUES ($1,$2,$3,'Chapter',$4,'draft',2,$5,$5)`, chapterID, projectID, bookID, index, seed.now)
		text := make([]byte, length)
		for position := range text {
			text[position] = 'a'
		}
		for _, variant := range []string{"draft", "published"} {
			seed.exec(`INSERT INTO chapter_paragraphs (id,chapter_id,revision,variant,kind,"order",markdown,plain_text,word_count) VALUES ($1,$2,2,$3,'paragraph',0,$4,$4,1)`, seed.id(), chapterID, variant, string(text))
		}
		// An older revision of the chapter does not count.
		seed.exec(`INSERT INTO chapter_paragraphs (id,chapter_id,revision,variant,kind,"order",markdown,plain_text,word_count) VALUES ($1,$2,1,'draft','paragraph',0,'old text','old text',2)`, seed.id(), chapterID)
	}
	return bookID
}

// R5.1, R3.5: the writer sees only their own projects; deleted books, the published copy and
// older revisions do not count; the day resets at 00:00 Moscow time.
func TestAccountQuotaCountsOwnLiveDraftText(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_quota_account")
	seed := &sourceSeed{t: t, db: db, now: time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)}
	seed.user(quotaWriterA, "quota-a@example.test")
	seed.user(quotaWriterB, "quota-b@example.test")
	seed.project(quotaProjectA, quotaWriterA)
	seed.project(quotaProjectB, quotaWriterB)
	seed.book(quotaProjectA, false, 100, 200)
	seed.book(quotaProjectA, true, 5000)
	seed.book(quotaProjectB, false, 9000)
	seed.exec(`INSERT INTO writer_usage_days (user_id, day, chat_turns, imports) VALUES ($1,'2026-10-28',4,1), ($1,'2026-10-27',9,9)`, quotaWriterA)

	account, err := New(db, Fence{}).AccountQuota(context.Background(), quotaWriterA, time.Date(2026, 10, 28, 20, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if account.ChatTurns != 4 || account.Imports != 1 || account.ActiveTurns != 0 || account.Projects != 1 {
		t.Fatalf("usage = %+v", account)
	}
	want := quota.Sources{Books: 1, Chapters: 2, Characters: 300}
	if account.Sources != want || len(account.OwnedProjects) != 1 ||
		account.OwnedProjects[0].ProjectID != quotaProjectA || account.OwnedProjects[0].Sources != want {
		t.Fatalf("sources = %+v, projects = %+v; want %+v in project A only", account.Sources, account.OwnedProjects, want)
	}
	if !account.ResetsAt.Equal(time.Date(2026, 10, 28, 21, 0, 0, 0, time.UTC)) || account.Limits != quota.Defaults() {
		t.Fatalf("resetsAt = %v limits = %+v", account.ResetsAt, account.Limits)
	}
}
