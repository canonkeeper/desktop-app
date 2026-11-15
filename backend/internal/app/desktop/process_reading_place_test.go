// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// specs/2026-11-14-chapter-place-number.md CP1.1 on the built backend of the app: a book of «.md» goes in with its chapters
// published, whose order a publication writes from zero, and the cards of the sample name its chapters by their places in
// the book from one — «Книга 1, гл. 1 „Застава у перевала“» —, as the contents of the book does.
func TestTheSampleNamesTheChaptersByTheirPlacesFromOne(t *testing.T) {
	binary, resources := desktopBinary(t)
	data := filepath.Join(t.TempDir(), "data")
	titles := []string{"Застава у перевала", "Тропа над ледником", "Сигнальные огни", "Дозор"}
	stub := &readingStub{titles: append([]string(nil), titles...)}
	server := httptest.NewServer(stub)
	defer server.Close()
	process := startBackend(t, binary, resources, data, nil)
	window := openWindow(t, process)
	saga := openSaga(t, window, "Северный предел", bookOf(titles...))
	connect(t, window, server.URL, map[string]any{"parallel": 2, "maxOutput": 1000})
	readingSettled(t, data, saga.project, 2*time.Minute)

	if response, raw := saga.post("/api/v1/projects/"+saga.project+"/reading/sample", map[string]any{}, "sample-of-the-places"); response.StatusCode != http.StatusAccepted {
		t.Fatalf("startReadingSample = %d %s", response.StatusCode, raw)
	}
	var sample struct {
		State    string `json:"state"`
		Chapters []struct {
			Place string `json:"place"`
		} `json:"chapters"`
	}
	for deadline := time.Now().Add(3 * time.Minute); ; {
		window.read("/api/v1/projects/"+saga.project+"/reading/sample", &sample)
		if sample.State == "succeeded" || sample.State == "failed" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the sample never ended: %+v", sample)
		}
		time.Sleep(300 * time.Millisecond)
	}
	var places []string
	for _, chapter := range sample.Chapters {
		places = append(places, chapter.Place)
	}
	want := []string{"Книга 1, гл. 1 „Застава у перевала“", "Книга 1, гл. 2 „Тропа над ледником“", "Книга 1, гл. 3 „Сигнальные огни“"}
	if sample.State != "succeeded" || !slices.Equal(places, want) {
		t.Fatalf("the sample = %s with the places %q; want %q", sample.State, places, want)
	}
	process.stop()
}
