// SPDX-License-Identifier: AGPL-3.0-only

package obsidian

import (
	"testing"
	"time"
)

// specs/2026-11-14-desk-obsidian-books.md OK0.3: the rule of reading of the links, case by case — what a link is by what
// the core says of its volume and its book, and what holds a folder of several links.
func TestTheRuleOfWhatHoldsAFolder(t *testing.T) {
	at := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	ended := at.Add(time.Minute)
	direct := func(id, folder, book string, minute int) linkFacts {
		return linkFacts{id: id, folder: folder, book: book, title: "Книга " + book, linkedAt: at.Add(time.Duration(minute) * time.Minute)}
	}
	volume := func(id, folder string, minute int) linkFacts {
		return linkFacts{
			id: id, folder: folder, volume: true, sameFile: true, job: "succeeded", jobEnded: &ended, queue: "q-" + id,
			queueStatus: "succeeded", book: "b-" + id, title: "Книга " + id, linkedAt: at.Add(time.Duration(minute) * time.Minute),
		}
	}
	with := func(link linkFacts, change func(*linkFacts)) linkFacts {
		change(&link)
		return link
	}
	for _, test := range []struct {
		name string
		link linkFacts
		want linkState
	}{
		{"a direct link of a live book", direct("1", "Ф", "b", 0), acts},
		{"a direct link of a book in the trash", with(direct("1", "Ф", "b", 0), func(link *linkFacts) { link.deleted = true }), trashed},
		{"a direct link of a book purged", with(direct("1", "Ф", "", 0), func(link *linkFacts) { link.gone = true }), inert},
		{"a volume whose Job succeeded", volume("1", "Ф", 0), acts},
		{"a volume whose book is in the trash", with(volume("1", "Ф", 0), func(link *linkFacts) { link.deleted = true }), trashed},
		{"a volume whose book was purged", with(volume("1", "Ф", 0), func(link *linkFacts) { link.book, link.title = "", "" }), inert},
		{"a volume skipped", with(volume("1", "Ф", 0), func(link *linkFacts) { link.skipped, link.sameFile = true, false }), inert},
		{"a volume given another file", with(volume("1", "Ф", 0), func(link *linkFacts) { link.sameFile = false }), inert},
		{"a volume whose Job failed", with(volume("1", "Ф", 0), func(link *linkFacts) { link.job, link.book, link.queueStatus = "failed", "", "running" }), inert},
		{"a volume whose Job was canceled", with(volume("1", "Ф", 0), func(link *linkFacts) { link.job, link.book, link.queueStatus = "canceled", "", "canceled" }), inert},
		// The deletion of the book detaches the Job of its volume as expired; the volume gave its book all the same.
		{"a volume whose Job the deletion of its book detached: in the trash", with(volume("1", "Ф", 0), func(link *linkFacts) {
			link.job, link.deleted = "expired", true
		}), trashed},
		{"a volume whose Job the deletion of its book detached: given back", with(volume("1", "Ф", 0), func(link *linkFacts) { link.job = "expired" }), acts},
		{"a volume whose Job ended without a book", with(volume("1", "Ф", 0), func(link *linkFacts) { link.job, link.book = "expired", "" }), inert},
		{"a volume in work", with(volume("1", "Ф", 0), func(link *linkFacts) { link.job, link.book, link.queueStatus = "running", "", "running" }), queued},
		{"a volume of a draft without its file yet", with(volume("1", "Ф", 0), func(link *linkFacts) {
			link.job, link.book, link.sameFile, link.noFile, link.queueStatus = "", "", false, true, "uploading"
		}), queued},
		{"a volume of a canceled draft", with(volume("1", "Ф", 0), func(link *linkFacts) { link.job, link.book, link.queueStatus = "", "", "canceled" }), inert},
		{"a volume of a queue whose file did not pass", with(volume("1", "Ф", 0), func(link *linkFacts) {
			link.job, link.book, link.queueStatus, link.invalid = "", "", "running", true
		}), inert},
	} {
		if got := stateOf(test.link); got != test.want {
			t.Errorf("%s = %d, want %d", test.name, got, test.want)
		}
	}

	folders := hold([]linkFacts{
		direct("a", "Общая", "older", 1),
		direct("b", "Общая", "later", 2),
		with(direct("c", "Общая", "trash", 3), func(link *linkFacts) { link.deleted = true }),
		with(direct("d", "Корзина", "trash", 1), func(link *linkFacts) { link.deleted = true }),
		with(volume("e", "Корзина", 2), func(link *linkFacts) { link.job, link.book, link.queueStatus = "running", "", "running" }),
		with(volume("f", "Очередь", 1), func(link *linkFacts) { link.job, link.book, link.queueStatus = "queued", "", "running" }),
		with(volume("g", "Инертная", 1), func(link *linkFacts) { link.skipped, link.sameFile = true, false }),
		volume("h", "Том", 1),
		with(volume("i", "Том", 2), func(link *linkFacts) { link.sameFile = false }),
	})
	byFolder := map[string]int{}
	for index, folder := range folders {
		byFolder[folder.Folder] = index
	}
	if _, ok := byFolder["Инертная"]; ok || len(folders) != 4 {
		t.Fatalf("the folders = %+v", folders)
	}
	shared := folders[byFolder["Общая"]]
	if shared.Book == nil || shared.Book.BookID != "later" || len(shared.Others) != 1 || shared.Others[0].BookID != "older" ||
		shared.Trash != nil || shared.Queue != "" {
		t.Fatalf("a folder of two live books and one in the trash = %+v", shared)
	}
	trash := folders[byFolder["Корзина"]]
	if trash.Book != nil || trash.Trash == nil || trash.Trash.BookID != "trash" || trash.Queue != "" {
		t.Fatalf("a folder of a book in the trash and a volume in work = %+v", trash)
	}
	if queue := folders[byFolder["Очередь"]]; queue.Book != nil || queue.Trash != nil || queue.Queue != "q-f" {
		t.Fatalf("a folder of a volume in work = %+v", queue)
	}
	if volume := folders[byFolder["Том"]]; volume.Book == nil || volume.Book.BookID != "b-h" || volume.Book.LoadedAt == nil ||
		!volume.Book.LoadedAt.Equal(ended) {
		t.Fatalf("a folder whose later volume was given another file = %+v", volume)
	}
}
