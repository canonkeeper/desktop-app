// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// syntheticBook is a long book of the made-up saga «Северный предел» in Markdown: its import runs long enough to be
// caught running.
func syntheticBook(chapters, paragraphs int) []byte {
	var book strings.Builder
	for chapter := 1; chapter <= chapters; chapter++ {
		fmt.Fprintf(&book, "# Глава %d. Северный предел\n\n", chapter)
		for paragraph := 1; paragraph <= paragraphs; paragraph++ {
			fmt.Fprintf(&book, "Ветер над перевалом нёс снег %d-й день подряд, и караван у Северного предела ждал, "+
				"пока проводник Ирвен прочтёт следы на льду; абзац %d главы %d.\n\n", paragraph, paragraph, chapter)
		}
	}
	return []byte(book.String())
}

// importBook sends a book file to importBookFile and returns the id of its Job.
func (browser *client) importBook(projectID string, book []byte) string {
	browser.t.Helper()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, _ := form.CreateFormFile("file", "severny-predel.md")
	_, _ = part.Write(book)
	_ = form.WriteField("metadata", `{"sourceFileName":"severny-predel.md","title":"Северный предел"}`)
	_ = form.Close()
	request, _ := http.NewRequest(http.MethodPost, browser.origin+"/api/v1/projects/"+projectID+"/imports", &body)
	request.Header.Set("Origin", browser.origin)
	request.Header.Set("Content-Type", form.FormDataContentType())
	request.Header.Set("X-CSRF-Token", browser.csrf())
	request.Header.Set("Idempotency-Key", "import-of-the-crash-test-0001")
	request.AddCookie(&http.Cookie{Name: "ck_session", Value: browser.cookie})
	response, err := browser.http.Do(request)
	if err != nil {
		browser.t.Fatal(err)
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(response.Body)
	var started struct {
		JobID string `json:"jobId"`
	}
	if response.StatusCode != http.StatusAccepted || json.Unmarshal(raw, &started) != nil || started.JobID == "" {
		browser.t.Fatalf("importBookFile = %d %s", response.StatusCode, raw)
	}
	return started.JobID
}

func (browser *client) jobStatus(jobID string) string {
	browser.t.Helper()
	response, body := browser.do(http.MethodGet, "/api/v1/jobs/"+jobID, "", nil)
	var job struct {
		Status string `json:"status"`
	}
	if response.StatusCode != http.StatusOK || json.Unmarshal(body, &job) != nil {
		browser.t.Fatalf("getJob = %d %s", response.StatusCode, body)
	}
	return job.Status
}

// DA1.6, DA1.7 (specs/2026-11-12-desk-process.md, Decision log): a backend killed while a Job runs leaves the Job running
// in the database; with no migration pending, the next start does not ask the runner — whose check of running Jobs is
// for the cutovers of the site — and starts; the Job is there to go back to the queue.
func TestAJobRunningAtACrashDoesNotStopTheNextStart(t *testing.T) {
	binary, resources := desktopBinary(t)
	data := filepath.Join(t.TempDir(), "data")
	first := startBackend(t, binary, resources, data, nil)
	browser := &client{t: t, origin: first.origin(), http: &http.Client{Timeout: 60 * time.Second}}
	if response := browser.open(first.ready.Code); response.StatusCode != http.StatusOK {
		t.Fatalf("open = %d", response.StatusCode)
	}
	response, body := browser.do(http.MethodPost, "/api/v1/projects", browser.csrf(), map[string]string{"title": "Северный предел"})
	var project struct {
		ID string `json:"id"`
	}
	if response.StatusCode != http.StatusCreated || json.Unmarshal(body, &project) != nil || project.ID == "" {
		t.Fatalf("createProject = %d %s", response.StatusCode, body)
	}
	jobID := browser.importBook(project.ID, syntheticBook(150, 40))
	deadline := time.Now().Add(60 * time.Second)
	for status := browser.jobStatus(jobID); status != "running"; status = browser.jobStatus(jobID) {
		if status != "queued" || time.Now().After(deadline) {
			t.Fatalf("the import is %s before it was caught running", status)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := first.command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = first.command.Wait()

	second := launchBackend(t, binary, resources, data, nil)
	if second.ready.Type != messageReady {
		t.Fatalf("the start after a crash with a running Job answered %+v", second.ready)
	}
	browser = &client{t: t, origin: second.origin(), http: &http.Client{Timeout: 60 * time.Second}}
	if response := browser.open(second.ready.Code); response.StatusCode != http.StatusOK {
		t.Fatalf("open after the crash = %d", response.StatusCode)
	}
	// DA1.7: the Job the crash left goes back to the queue when its claim runs out and the book is imported.
	started := time.Now()
	deadline = started.Add(4 * time.Minute)
	for status := browser.jobStatus(jobID); status != "succeeded"; status = browser.jobStatus(jobID) {
		if status == "failed" || status == "canceled" || time.Now().After(deadline) {
			t.Fatalf("the import after the crash = %s", status)
		}
		time.Sleep(time.Second)
	}
	t.Logf("the import left by the crash finished %s after the start", time.Since(started).Round(time.Second))
	second.stop()
}
