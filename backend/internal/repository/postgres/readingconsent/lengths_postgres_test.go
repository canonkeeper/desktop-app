// SPDX-License-Identifier: AGPL-3.0-only

package readingconsent

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"canonkeeper/backend/internal/testutil"
)

// specs/2026-11-17-long-chapters.md LC1.6: the reading reads the characters of each paragraph of the published text of a
// chapter at its revision, in the order of the paragraphs — what the division of its card needs; the draft and another
// revision are not of it.
func TestTheLengthsOfTheParagraphsOfAChapterGoInTheirOrder(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_reading_lengths")
	seedConsentSagas(t, db)
	repository := New(db)
	ctx := context.Background()
	const (
		book    = "94000000-0000-4000-8000-0000000000b1"
		chapter = "94000000-0000-4000-8000-0000000000c1"
	)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.Pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO books (id,project_id,title,"order",display_number,status,revision,created_at,updated_at) VALUES ($1,$2,'Северный предел',1,'1','draft',1,now(),now())`, book, consentSaga)
	exec(`INSERT INTO chapters (id,project_id,book_id,title,"order",status,revision,created_at,updated_at) VALUES ($1,$2,$3,'Застава',1,'draft',2,now(),now())`, chapter, consentSaga, book)
	paragraph := func(revision int, variant string, order int, text string) {
		exec(`INSERT INTO chapter_paragraphs (id,chapter_id,revision,variant,kind,"order",markdown,plain_text,word_count) VALUES (gen_random_uuid(),$1,$2,$3,'paragraph',$4,$5,$5,1)`,
			chapter, revision, variant, order, text)
	}
	paragraph(2, "published", 3, strings.Repeat("ё", 3))
	paragraph(2, "published", 1, "Ветер выл.")
	paragraph(2, "published", 2, strings.Repeat("ы", 7))
	paragraph(2, "draft", 4, "черновик")
	paragraph(1, "published", 1, "прежний текст")
	lengths, err := repository.Lengths(ctx, consentSaga, []ChapterRevision{{ID: chapter, Revision: 2}})
	if err != nil || !reflect.DeepEqual(lengths, map[string][]int{chapter: {10, 7, 3}}) {
		t.Fatalf("the lengths = %v, %v", lengths, err)
	}
	if lengths, err := repository.Lengths(ctx, strangerSaga, []ChapterRevision{{ID: chapter, Revision: 2}}); err != nil || len(lengths) != 0 {
		t.Fatalf("the lengths of a chapter of another saga = %v, %v", lengths, err)
	}
}
