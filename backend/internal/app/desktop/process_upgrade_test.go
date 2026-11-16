// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"canonkeeper/backend/internal/app/desktop/desktoptest"
)

// programChangingJobs knows one migration after the last of the tree, and it changes the table of the Jobs.
var programChangingJobs = programVariant{
	name: "changing-jobs", ahead: 1, later: "ALTER TABLE public.jobs ADD COLUMN desktop_test_later integer;\n",
}

// sagaWithAnImport makes a saga with the title and imports a long book into it; the id of the saga and of the Job.
func sagaWithAnImport(browser *client, title, key string) (string, string) {
	browser.t.Helper()
	response, body := browser.do(http.MethodPost, "/api/v1/projects", browser.csrf(), map[string]string{"title": title})
	var project struct {
		ID string `json:"id"`
	}
	if response.StatusCode != http.StatusCreated || json.Unmarshal(body, &project) != nil || project.ID == "" {
		browser.t.Fatalf("createProject = %d %s", response.StatusCode, body)
	}
	return project.ID, browser.importBookKeyed(project.ID, syntheticBook(300, 60), key)
}

// caughtRunning waits for the Job to be caught running: false when it left the queue for another status first, or stayed
// queued a minute.
func (browser *client) caughtRunning(jobID string) bool {
	browser.t.Helper()
	deadline := desktoptest.Deadline(60 * time.Second)
	for status := browser.jobStatus(jobID); status != "running"; status = browser.jobStatus(jobID) {
		if status != "queued" || time.Now().After(deadline) {
			browser.t.Logf("the import is %s before it was caught running", status)
			return false
		}
		time.Sleep(20 * time.Millisecond)
	}
	return true
}

// untilFinished waits for the Job to end and returns how it ended.
func (browser *client) untilFinished(jobID string, timeout time.Duration) string {
	browser.t.Helper()
	deadline := desktoptest.Deadline(timeout)
	for {
		status := browser.jobStatus(jobID)
		if status == "succeeded" || status == "failed" || status == "canceled" || status == "expired" {
			return status
		}
		if time.Now().After(deadline) {
			browser.t.Fatalf("the Job is still %s after %s", status, timeout)
		}
		time.Sleep(time.Second)
	}
}

// statusesOf reads the statuses of Jobs of a stopped run: the PostgreSQL a killed backend left still runs.
func statusesOf(t *testing.T, data string, ids ...string) []string {
	t.Helper()
	connection := database(t, data)
	defer connection.Close(context.Background())
	statuses := make([]string, len(ids))
	for index, id := range ids {
		if err := connection.QueryRow(context.Background(), `SELECT status FROM public.jobs WHERE id = $1`, id).Scan(&statuses[index]); err != nil {
			t.Fatal(err)
		}
	}
	return statuses
}

// specs/2026-11-13-desk-upgrade.md UG1.1, UG1.2: the earlier program is killed while one import runs and another is
// canceling; the next program to start knows a migration that changes the table of the Jobs. It copies the data, applies
// the migration on its own cluster without waiting for those Jobs, and starts; then its workers take them back the
// standard way: the running import is done, the canceling one ends canceled.
func TestAnUpdateStartsOverTheJobsTheEarlierProgramLeft(t *testing.T) {
	binary, resources := desktopBinary(t)
	last := lastVersion(t)
	var data, running, canceling string
	for attempt := 1; ; attempt++ {
		data = filepath.Join(t.TempDir(), "data")
		first := startBackend(t, binary, resources, data, nil)
		browser := &client{t: t, origin: first.origin(), http: &http.Client{Timeout: desktoptest.Waited(60 * time.Second)}}
		if response := browser.open(first.ready.Code); response.StatusCode != http.StatusOK {
			t.Fatalf("open = %d", response.StatusCode)
		}
		_, running = sagaWithAnImport(browser, "Северный предел", "import-of-the-update-test-1")
		_, canceling = sagaWithAnImport(browser, "Северный предел: зима", "import-of-the-update-test-2")
		caught := browser.caughtRunning(running) && browser.caughtRunning(canceling)
		if caught {
			if response, body := browser.do(http.MethodPost, "/api/v1/jobs/"+canceling+"/cancel", browser.csrf(), nil); response.StatusCode >= 300 {
				t.Fatalf("cancelJob = %d %s", response.StatusCode, body)
			}
		}
		if err := first.command.Process.Kill(); err != nil {
			t.Fatal(err)
		}
		_ = first.command.Wait()
		statuses := statusesOf(t, data, running, canceling)
		if caught && statuses[0] == "running" && statuses[1] == "canceling" {
			break
		}
		// The machine was faster or slower than the stand: an import was not caught running, or the worker ended the cancel
		// before the kill. The stand is made again.
		if attempt == 3 {
			t.Fatalf("the Jobs left by the kill are %v; want running and canceling", statuses)
		}
		stoppedFingerprint(t, resources, data)
	}

	updated := launchBackend(t, program(t, programChangingJobs), resources, data, nil)
	if updated.ready.Type != messageReady {
		t.Fatalf("the update with Jobs left by a crash answered %+v", updated.ready)
	}
	if complete, _ := copies(t, data); len(complete) != 1 || !strings.HasPrefix(complete[0], fmt.Sprintf("pre-%d-", last)) {
		t.Fatalf("the copies of the update = %v", complete)
	}
	if schema := schemaOf(t, administrator(t, data, databaseName)); schema != last+1 {
		t.Fatalf("the schema after the update = %d", schema)
	}
	browser := &client{t: t, origin: updated.origin(), http: &http.Client{Timeout: desktoptest.Waited(60 * time.Second)}}
	if response := browser.open(updated.ready.Code); response.StatusCode != http.StatusOK {
		t.Fatalf("open after the update = %d", response.StatusCode)
	}
	if status := browser.untilFinished(canceling, 4*time.Minute); status != "canceled" {
		t.Fatalf("the canceling import after the update = %s", status)
	}
	if status := browser.untilFinished(running, 4*time.Minute); status != "succeeded" {
		t.Fatalf("the running import after the update = %s", status)
	}
	updated.stop()
}

// bundleWithout is the bundle of the test without one of its parts: links to the bundle's others. Linux only.
func bundleWithout(t *testing.T, resources, part string) string {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("the bundle of the test is made of links")
	}
	bundle := t.TempDir()
	for _, kept := range []string{"postgres", "codex", "codex-homes"} {
		if kept == part {
			continue
		}
		if err := os.Symlink(filepath.Join(resources, kept), filepath.Join(bundle, kept)); err != nil {
			t.Fatal(err)
		}
	}
	return bundle
}

// versioned is a program of the tree, or with migrations ahead of it, built with a version.
func versioned(name, version string, ahead int) programVariant {
	return programVariant{name: name + "-" + version, version: version, ahead: ahead}
}

// filesOf are the files of a directory with their sizes and times of change.
func filesOf(t *testing.T, root string) map[string]string {
	t.Helper()
	found := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		found[path] = fmt.Sprintf("%d %s %s", info.Size(), info.ModTime().Format(time.RFC3339Nano), info.Mode())
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return found
}

// settingsOf is settings.json of the data directory.
func settingsOf(t *testing.T, data string) map[string]json.RawMessage {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(data, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	var values map[string]json.RawMessage
	if err := json.Unmarshal(raw, &values); err != nil {
		t.Fatal(err)
	}
	return values
}

// UG2.1: a backend of another version than its shell — an installation cut short — says so with both versions before it
// touches the data directory: no file of it changes.
func TestABackendOfAnotherVersionThanItsShellLeavesTheDataAlone(t *testing.T) {
	_, resources := desktopBinary(t)
	binary := program(t, versioned("tree", "1.3.0", 0))
	data := filepath.Join(t.TempDir(), "data")
	startBackend(t, binary, resources, data, nil).stop()
	before := filesOf(t, data)
	time.Sleep(20 * time.Millisecond)
	for _, shell := range []string{"1.2.0", ""} {
		mismatch := launchWith(t, binary, ControlMessage{
			Type: messageStart, DataDir: data, Resources: resources, SecretsStore: "file", Version: shell,
		})
		if mismatch.ready.Type != messageFatal || mismatch.ready.Reason != "version_mismatch" ||
			mismatch.ready.Version != "1.3.0" || mismatch.ready.Shell != shell {
			t.Fatalf("a shell of %q answered %+v", shell, mismatch.ready)
		}
		mismatch.exited(60 * time.Second)
	}
	if after := filesOf(t, data); !reflect.DeepEqual(before, after) {
		t.Fatalf("the data directory changed:\nbefore %v\nafter  %v", before, after)
	}
	startBackend(t, binary, resources, data, nil).stop()
}

// UG2.2, UG2.3: a newer program applied its migrations and its core fell before ready; the earlier program on these data
// names the newer version and the copy a return would bring back — the newest whose schema and PostgreSQL it knows. A
// cluster a later PostgreSQL made is the same refusal, before the cluster starts. Without copies there is no candidate;
// with settings.json broken and no copy the version is not known.
func TestAProgramOlderThanItsDataNamesTheVersionThatOpenedThemAndACopy(t *testing.T) {
	_, resources := desktopBinary(t)
	last := lastVersion(t)
	older, newer := program(t, versioned("tree", "1.2.0", 0)), program(t, versioned("ahead", "1.3.0", 1))
	data := filepath.Join(t.TempDir(), "data")
	first := startBackend(t, older, resources, data, nil)
	makeSaga(t, first)
	first.stop()

	falling := launchBackend(t, newer, bundleWithout(t, resources, "codex-homes"), data, nil)
	if falling.ready.Type != messageFatal || falling.ready.Reason != "backend" {
		t.Fatalf("the newer program without its profile answered %+v", falling.ready)
	}
	falling.exited(60 * time.Second)
	if len(falling.steps) != 2 || falling.steps[0].Step != "backup" || falling.steps[1].Step != "migrate" ||
		falling.steps[0].Version != "1.3.0" || falling.steps[1].Version != "1.3.0" {
		t.Fatalf("the steps of the update = %+v", falling.steps)
	}
	if version := string(settingsOf(t, data)["version"]); version != `"1.3.0"` {
		t.Fatalf("settings.json after the migrations names %s", version)
	}
	complete, _ := copies(t, data)
	if len(complete) != 1 {
		t.Fatalf("the copies = %v", complete)
	}

	refused := launchBackend(t, older, resources, data, nil)
	refused.exited(60 * time.Second)
	candidate := refused.ready.Copy
	if refused.ready.Reason != "newer_schema" || refused.ready.Opened != "1.3.0" || candidate == nil ||
		candidate.Name != complete[0] || candidate.Version != "1.3.0" {
		t.Fatalf("the older program answered %+v (copy %+v)", refused.ready, candidate)
	}
	if made, err := time.Parse(time.RFC3339, candidate.MadeAt); err != nil || time.Since(made) > time.Hour {
		t.Fatalf("the copy was made at %q: %v", candidate.MadeAt, err)
	}

	// A copy of a schema the older program does not know, and one another PostgreSQL made, are no candidates.
	backups := DataDir{Root: data}.Backups()
	later := time.Now().Add(time.Hour).UTC().Format(backupTime)
	for name, major := range map[string]string{
		fmt.Sprintf("pre-%d-%s", last+1, later): "18",
		fmt.Sprintf("pre-%d-%s", last, later):   "17",
	} {
		makeCopy(t, backups, name, true)
		writeFile(t, filepath.Join(backups, name, backupPostgres), major+"\n")
	}
	again := launchBackend(t, older, resources, data, nil)
	again.exited(60 * time.Second)
	if again.ready.Copy == nil || again.ready.Copy.Name != complete[0] {
		t.Fatalf("the candidate among foreign copies = %+v", again.ready.Copy)
	}

	// A cluster a later major version of PostgreSQL made is refused before it starts, with the same data.
	version := filepath.Join(data, "pg", "PG_VERSION")
	writeFile(t, version, "19\n")
	major := launchBackend(t, older, resources, data, nil)
	major.exited(60 * time.Second)
	if major.ready.Reason != "newer_schema" || major.ready.Opened != "1.3.0" || major.ready.Copy == nil {
		t.Fatalf("a cluster of PostgreSQL 19 answered %+v", major.ready)
	}
	writeFile(t, version, "18\n")

	if err := os.RemoveAll(backups); err != nil {
		t.Fatal(err)
	}
	none := launchBackend(t, older, resources, data, nil)
	none.exited(60 * time.Second)
	if none.ready.Reason != "newer_schema" || none.ready.Opened != "1.3.0" || none.ready.Copy != nil {
		t.Fatalf("without copies the older program answered %+v", none.ready)
	}

	// The data of the newer program from their first start: no copy; settings.json broken — no version is known.
	fresh := filepath.Join(t.TempDir(), "data")
	startBackend(t, newer, resources, fresh, nil).stop()
	writeFile(t, filepath.Join(fresh, "settings.json"), "{broken")
	unknown := launchBackend(t, older, resources, fresh, nil)
	unknown.exited(60 * time.Second)
	if unknown.ready.Reason != "newer_schema" || unknown.ready.Opened != "" || unknown.ready.Copy != nil {
		t.Fatalf("with settings.json broken the older program answered %+v", unknown.ready)
	}
}

// UG2.4: a migration that fails names its copy and the version that opened the data before; one that fails for want of
// place on the disk is its own reason.
func TestAFailedUpdateNamesTheVersionBeforeItOrTheDisk(t *testing.T) {
	_, resources := desktopBinary(t)
	data := filepath.Join(t.TempDir(), "data")
	startBackend(t, program(t, versioned("tree", "1.2.0", 0)), resources, data, nil).stop()

	broken := launchBackend(t, program(t, programVariant{name: "broken-1.3.0", broken: true, version: "1.3.0"}), resources, data, nil)
	broken.exited(60 * time.Second)
	if broken.ready.Reason != "migrate" || broken.ready.Opened != "1.2.0" || !strings.HasPrefix(broken.ready.Backup, "pre-") {
		t.Fatalf("a broken migration answered %+v", broken.ready)
	}
	full := programVariant{
		name: "full-1.3.0", version: "1.3.0", ahead: 1,
		later: "DO $$ BEGIN RAISE EXCEPTION 'the disk is full' USING ERRCODE = 'disk_full'; END $$;\n",
	}
	space := launchBackend(t, program(t, full), resources, data, nil)
	space.exited(60 * time.Second)
	if space.ready.Reason != "migrate_space" {
		t.Fatalf("a migration without place answered %+v", space.ready)
	}
	if version := string(settingsOf(t, data)["version"]); version != `"1.2.0"` {
		t.Fatalf("settings.json after failed updates names %s", version)
	}
}

// UG2.5: ready names the version the data were opened with before an update, once; a return to an earlier version
// without migrations names nothing.
func TestReadyNamesTheVersionBeforeAnUpdateOnce(t *testing.T) {
	_, resources := desktopBinary(t)
	data := filepath.Join(t.TempDir(), "data")
	earlier, later := program(t, versioned("tree", "1.2.0", 0)), program(t, versioned("tree", "1.3.0", 0))
	for index, step := range []struct {
		binary  string
		updated string
	}{{earlier, ""}, {later, "1.2.0"}, {later, ""}, {earlier, ""}} {
		process := startBackend(t, step.binary, resources, data, nil)
		if process.ready.UpdatedFrom != step.updated {
			t.Fatalf("start %d named the update from %q; want %q", index+1, process.ready.UpdatedFrom, step.updated)
		}
		process.stop()
	}
	if values := settingsOf(t, data); string(values["version"]) != `"1.2.0"` || values["updatedFrom"] != nil {
		t.Fatalf("settings.json = %v", values)
	}
}

// UG5.1: «Добро пожаловать» is shown at the first start; dismissWelcome keeps the choice to check for a new version in
// settings.json, and neither the welcome nor the choice comes back after the address of the window changed.
func TestTheWelcomeIsClosedOnceAndKeepsTheChoiceThroughANewAddress(t *testing.T) {
	binary, resources := desktopBinary(t)
	data := filepath.Join(t.TempDir(), "data")
	first := startBackend(t, binary, resources, data, nil)
	window := opened(t, first)
	state := stateOfTheWindow(t, window)
	if state.Welcome == nil || !*state.Welcome || state.CheckUpdates == nil || !*state.CheckUpdates {
		t.Fatalf("the first start = welcome %v, checkUpdates %v", state.Welcome, state.CheckUpdates)
	}
	if response, body := window.do(http.MethodPost, "/api/v1/desktop/welcome", window.csrf(), map[string]bool{"checkUpdates": false}); response.StatusCode != http.StatusNoContent {
		t.Fatalf("dismissWelcome = %d %s", response.StatusCode, body)
	}
	if state := stateOfTheWindow(t, window); *state.Welcome || *state.CheckUpdates {
		t.Fatalf("after dismissWelcome = welcome %v, checkUpdates %v", *state.Welcome, *state.CheckUpdates)
	}
	port := first.ready.Port
	first.stop()
	if values := settingsOf(t, data); string(values["welcomed"]) != "true" || compact(t, values["updates"]) != `{"daily":false}` {
		t.Fatalf("settings.json = %v", values)
	}

	taken, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		t.Fatal(err)
	}
	defer taken.Close()
	moved := startBackend(t, binary, resources, data, nil)
	if !moved.ready.PortChanged || moved.ready.Port == port {
		t.Fatalf("the start with the port taken = %+v", moved.ready)
	}
	if state := stateOfTheWindow(t, opened(t, moved)); *state.Welcome || *state.CheckUpdates {
		t.Fatalf("after a new address = welcome %v, checkUpdates %v", *state.Welcome, *state.CheckUpdates)
	}
	moved.stop()
}
