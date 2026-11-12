package librarystore

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"canonkeeper/backend/internal/auth"
	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/library"
	"canonkeeper/backend/internal/projects"
	jobstore "canonkeeper/backend/internal/store/jobs"

	"golang.org/x/text/encoding/charmap"
)

// bookOfKindForTest is a book of two chapters as a kind of file keeps it; changed is the second chapter in other words.
type bookOfKindForTest struct {
	file, mime string
	build      func(t *testing.T, second string) []byte
	// faced says the kind keeps italic and bold.
	faced bool
}

func zipOfForTest(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for _, name := range []string{"mimetype", "content.xml", "meta.xml", "Северный ветер.fb2"} {
		body, ok := entries[name]
		if !ok {
			continue
		}
		file, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func cp1251ForTest(t *testing.T, text string) []byte {
	t.Helper()
	encoded, err := charmap.Windows1251.NewEncoder().Bytes([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

// rtfOfForTest writes a text in RTF: its signs over ASCII as \'hh of Windows-1251.
func rtfOfForTest(t *testing.T, text string) string {
	t.Helper()
	var out strings.Builder
	for _, b := range cp1251ForTest(t, text) {
		if b >= 0x80 {
			fmt.Fprintf(&out, `\'%02x`, b)
		} else {
			out.WriteByte(b)
		}
	}
	return out.String()
}

func booksOfEveryKindForTest() map[string]bookOfKindForTest {
	const odtNamespaces = `xmlns:office="urn:oasis:names:tc:opendocument:xmlns:office:1.0" xmlns:style="urn:oasis:names:tc:opendocument:xmlns:style:1.0" ` +
		`xmlns:text="urn:oasis:names:tc:opendocument:xmlns:text:1.0" xmlns:fo="urn:oasis:names:tc:opendocument:xmlns:xsl-fo-compatible:1.0" ` +
		`xmlns:dc="http://purl.org/dc/elements/1.1/"`
	return map[string]bookOfKindForTest{
		".txt": {file: "Северный ветер.txt", mime: "text/plain", build: func(t *testing.T, second string) []byte {
			return cp1251ForTest(t, "Северный ветер\r\n\r\nГлава 1\r\nКорабль вошёл в гавань.\r\nГлава 2\r\n"+second+"\r\n")
		}},
		".md": {file: "Северный ветер.md", mime: "text/markdown", faced: true, build: func(_ *testing.T, second string) []byte {
			return []byte("# Северный ветер\n\n# Глава 1\n\nКорабль *вошёл* в **гавань**.\n\n# Глава 2\n\n" + second + "\n")
		}},
		".rtf": {file: "Северный ветер.rtf", mime: "application/rtf", faced: true, build: func(t *testing.T, second string) []byte {
			return []byte(`{\rtf1\ansi\ansicpg1251{\fonttbl{\f0\fcharset204 Times New Roman;}}{\info{\title x}}\pard ` +
				rtfOfForTest(t, "Северный ветер") + `\par\par ` + rtfOfForTest(t, "Глава 1") + `\par ` +
				rtfOfForTest(t, "Корабль ") + `{\i ` + rtfOfForTest(t, "вошёл") + `} ` + rtfOfForTest(t, "в ") + `{\b ` +
				rtfOfForTest(t, "гавань") + `}.\par ` + rtfOfForTest(t, "Глава 2") + `\par ` + rtfOfForTest(t, second) + `\par}`)
		}},
		".odt": {file: "Северный ветер.odt", mime: "application/vnd.oasis.opendocument.text", faced: true, build: func(t *testing.T, second string) []byte {
			return zipOfForTest(t, map[string]string{
				"mimetype": "application/vnd.oasis.opendocument.text",
				"content.xml": `<?xml version="1.0" encoding="UTF-8"?><office:document-content ` + odtNamespaces + `><office:automatic-styles>` +
					`<style:style style:name="T1" style:family="text"><style:text-properties fo:font-style="italic"/></style:style>` +
					`<style:style style:name="T2" style:family="text"><style:text-properties fo:font-weight="bold"/></style:style>` +
					`</office:automatic-styles><office:body><office:text>` +
					`<text:h text:outline-level="1">Глава 1</text:h><text:p>Корабль <text:span text:style-name="T1">вошёл</text:span> в ` +
					`<text:span text:style-name="T2">гавань</text:span>.</text:p><text:h text:outline-level="1">Глава 2</text:h><text:p>` + second +
					`</text:p></office:text></office:body></office:document-content>`,
				"meta.xml": `<?xml version="1.0" encoding="UTF-8"?><office:document-meta ` + odtNamespaces +
					`><office:meta><dc:title>Северный ветер</dc:title></office:meta></office:document-meta>`,
			})
		}},
		".fb2.zip": {file: "Северный ветер.fb2.zip", mime: "application/zip", faced: true, build: func(t *testing.T, second string) []byte {
			return zipOfForTest(t, map[string]string{"Северный ветер.fb2": `<?xml version="1.0" encoding="utf-8"?><FictionBook>` +
				`<description><title-info><book-title>Северный ветер</book-title></title-info></description><body>` +
				`<section><title><p>Глава 1</p></title><p>Корабль <emphasis>вошёл</emphasis> в <strong>гавань</strong>.</p></section>` +
				`<section><title><p>Глава 2</p></title><p>` + second + `</p></section></body></FictionBook>`})
		}},
	}
}

// specs/2026-11-10-book-file-kinds.md K6.2: a book of each new kind goes through the upload and the import Job of the
// product into the chapters and paragraphs of the book with their italic and bold, and «Обновить из файла» reads a
// file of the same kind into the same book.
func TestEveryNewKindOfABookFileImportsAndUpdates(t *testing.T) {
	for kind, book := range booksOfEveryKindForTest() {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			db := openCurrentStoreTestDB(t)
			now := time.Now().UTC().Truncate(time.Microsecond)
			var userID, projectID, membershipID string
			if err := db.Pool.QueryRow(ctx, `SELECT gen_random_uuid()::text, gen_random_uuid()::text, gen_random_uuid()::text`).
				Scan(&userID, &projectID, &membershipID); err != nil {
				t.Fatal(err)
			}
			proof := insertProjectStoreUserWithSessionForTest(t, db, userID, "kinds@example.test")
			insertImportStoreProjectForTest(t, db, projectID, userID, "Сага автора", now)
			insertProjectStoreMembershipForTest(t, db, membershipID, projectID, userID, projects.RoleOwner, now, false)
			store, jobsStore := New(db), jobstore.New(db)
			claims := newWorkerClaimAuthorityForTest(db)
			service := library.NewService(store, library.NewSecureIDGenerator(), auth.SystemClock{})

			run := func(key string, data []byte, mode library.ImportMode, bookID string) {
				t.Helper()
				if _, err := service.CreateImportJob(ctx, library.Actor{UserID: userID}, projectID, library.CreateImportJobInput{
					SourceFileName: book.file, MIMEType: book.mime, SizeBytes: int64(len(data)), FileBytes: data,
					IdempotencyKey: key, RequestMethod: "POST", RequestPath: "/projects/" + projectID + "/imports",
					ImportMode: mode, BookID: bookID, SessionWriteProof: proof,
				}); err != nil {
					t.Fatalf("CreateImportJob(%s) error = %v", key, err)
				}
				decision, err := claims.ClaimNextQueuedJobForWorker(ctx, []jobs.Kind{jobs.KindImport})
				if err != nil {
					t.Fatalf("claim %s: %v", key, err)
				}
				job, err := jobsStore.LoadJobForWorkerClaim(ctx, decision.Claim)
				if err != nil {
					t.Fatal(err)
				}
				if result, err := library.NewImportHandler(store, store).HandleResult(ctx, job); err != nil || result.Status != jobs.StatusSucceeded {
					t.Fatalf("import %s = %+v, %v; want it succeeded", key, result, err)
				}
			}
			// The book: its title, its chapters and the Markdown and the plain text of their published paragraphs.
			read := func() (string, string, []string) {
				t.Helper()
				var bookID, title string
				if err := db.Pool.QueryRow(ctx, `SELECT id::text, title FROM books WHERE project_id = $1 AND deleted_at IS NULL`, projectID).
					Scan(&bookID, &title); err != nil {
					t.Fatal(err)
				}
				rows, err := db.Pool.Query(ctx, `
					SELECT chapter.title || '|' || paragraph.markdown || '|' || paragraph.plain_text
					FROM chapters AS chapter
					JOIN chapter_paragraphs AS paragraph ON paragraph.chapter_id = chapter.id AND paragraph.revision = chapter.revision
					WHERE chapter.book_id = $1::uuid AND chapter.deleted_at IS NULL AND paragraph.variant = 'published'
					ORDER BY chapter."order", paragraph."order"
				`, bookID)
				if err != nil {
					t.Fatal(err)
				}
				defer rows.Close()
				var lines []string
				for rows.Next() {
					var line string
					if err := rows.Scan(&line); err != nil {
						t.Fatal(err)
					}
					lines = append(lines, line)
				}
				return bookID, title, lines
			}

			run("kinds-new-"+membershipID, book.build(t, "Ночью начался шторм."), library.ImportModeNewBook, "")
			bookID, title, lines := read()
			first := "Глава 1|Корабль вошёл в гавань.|Корабль вошёл в гавань."
			if book.faced {
				first = "Глава 1|Корабль *вошёл* в **гавань**.|Корабль вошёл в гавань."
			}
			want := []string{first, "Глава 2|Ночью начался шторм.|Ночью начался шторм."}
			if title != "Северный ветер" || strings.Join(lines, "\n") != strings.Join(want, "\n") {
				t.Fatalf("the book of %s = %q\n%s\nwant «Северный ветер»\n%s", kind, title, strings.Join(lines, "\n"), strings.Join(want, "\n"))
			}

			run("kinds-update-"+membershipID, book.build(t, "К утру шторм стих."), library.ImportModeUpdateBook, bookID)
			updatedID, title, lines := read()
			want[1] = "Глава 2|К утру шторм стих.|К утру шторм стих."
			if updatedID != bookID || title != "Северный ветер" || strings.Join(lines, "\n") != strings.Join(want, "\n") {
				t.Fatalf("the book of %s after «Обновить из файла» = %q\n%s\nwant\n%s", kind, title, strings.Join(lines, "\n"), strings.Join(want, "\n"))
			}
		})
	}
}
