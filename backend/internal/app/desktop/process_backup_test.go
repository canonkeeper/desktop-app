// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// The programs the process tests run are cmd/desktop of this tree, built once per test binary. The earlier and the later
// versions are the same tree built with go build -overlay: an earlier program does not know the last migrations of the
// tree, a broken later one knows one more that fails. No file of the tree changes.
type programVariant struct {
	name   string
	behind int  // the last migrations of the tree the program does not know
	broken bool // the program knows one migration more, which fails
	edited bool // the last migration of the tree is changed after it was applied
}

var programOfTheTree = programVariant{name: "tree"}

func programBehind(count int) programVariant {
	return programVariant{name: "behind-" + strconv.Itoa(count), behind: count}
}

var programWithABrokenMigration = programVariant{name: "broken", broken: true}

var programWithAnEditedMigration = programVariant{name: "edited", edited: true}

var programs = struct {
	sync.Mutex
	directory string
	built     map[string]string
}{built: map[string]string{}}

func TestMain(m *testing.M) {
	code := m.Run()
	if programs.directory != "" {
		_ = os.RemoveAll(programs.directory)
	}
	os.Exit(code)
}

// treeMigrations are the migration files of the tree in order, as absolute paths.
func treeMigrations(t *testing.T) []string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", "..", "db", "migrations"))
	if err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob(filepath.Join(root, "*.sql"))
	if err != nil || len(files) < 3 {
		t.Fatalf("the migrations of the tree = %v, %v", files, err)
	}
	slices.Sort(files)
	return files
}

// lastVersion is the version of the last migration of the tree.
func lastVersion(t *testing.T) int64 {
	t.Helper()
	files := treeMigrations(t)
	version, err := strconv.ParseInt(strings.SplitN(filepath.Base(files[len(files)-1]), "_", 2)[0], 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	return version
}

func program(t *testing.T, variant programVariant) string {
	t.Helper()
	programs.Lock()
	defer programs.Unlock()
	if binary, ok := programs.built[variant.name]; ok {
		return binary
	}
	if programs.directory == "" {
		directory, err := os.MkdirTemp("", "canonkeeper-desktop-programs-")
		if err != nil {
			t.Fatal(err)
		}
		programs.directory = directory
	}
	binary := filepath.Join(programs.directory, "desktop-"+variant.name)
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	args := []string{"build", "-o", binary}
	if variant.behind > 0 || variant.broken || variant.edited {
		replace := map[string]string{}
		files := treeMigrations(t)
		for _, file := range files[len(files)-variant.behind:] {
			replace[file] = ""
		}
		if variant.edited {
			original, err := os.ReadFile(files[len(files)-1])
			if err != nil {
				t.Fatal(err)
			}
			edited := filepath.Join(programs.directory, "edited.sql")
			if err := os.WriteFile(edited, append(original, "\n-- edited after it was applied\n"...), 0o600); err != nil {
				t.Fatal(err)
			}
			replace[files[len(files)-1]] = edited
		}
		if variant.broken {
			broken := filepath.Join(programs.directory, "broken.sql")
			if err := os.WriteFile(broken, []byte("SELECT 1 / 0;\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			replace[filepath.Join(filepath.Dir(files[0]), fmt.Sprintf("%04d_desktop_test_broken.sql", lastVersion(t)+1))] = broken
		}
		overlay, _ := json.Marshal(map[string]any{"Replace": replace})
		path := filepath.Join(programs.directory, variant.name+".overlay.json")
		if err := os.WriteFile(path, overlay, 0o600); err != nil {
			t.Fatal(err)
		}
		args = append(args, "-overlay", path)
	}
	build := exec.Command("go", append(args, "./cmd/desktop")...)
	build.Dir = filepath.Join("..", "..", "..")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build ./cmd/desktop (%s): %v\n%s", variant.name, err, output)
	}
	programs.built[variant.name] = binary
	return binary
}

// bundleWithDump is the bundle of the test with its own pg_dump: none when dump is empty, else the script dump. The other
// programs are links to the bundle's: PostgreSQL finds its lib and share by the real path. Linux only.
func bundleWithDump(t *testing.T, resources, dump string) string {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("the bundle of the test is made of links and a shell script")
	}
	bundle := t.TempDir()
	bin := filepath.Join(bundle, "postgres", "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	real := filepath.Join(resources, "postgres", "bin")
	entries, err := os.ReadDir(real)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() == "pg_dump" {
			continue
		}
		if err := os.Symlink(filepath.Join(real, entry.Name()), filepath.Join(bin, entry.Name())); err != nil {
			t.Fatal(err)
		}
	}
	if dump != "" {
		if err := os.WriteFile(filepath.Join(bin, "pg_dump"), []byte(dump), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, part := range []string{"postgres/lib", "postgres/share", "codex", "codex-homes"} {
		if err := os.Symlink(filepath.Join(resources, part), filepath.Join(bundle, part)); err != nil {
			t.Fatal(err)
		}
	}
	return bundle
}

// recordingDump is a pg_dump that writes its own command line and environment to record before it runs the real one.
func recordingDump(resources, record string) string {
	return "#!/bin/sh\ncat /proc/$$/cmdline > '" + filepath.Join(record, "cmdline") + "'\n" +
		"cat /proc/$$/environ > '" + filepath.Join(record, "environ") + "'\n" +
		"exec '" + filepath.Join(resources, "postgres", "bin", "pg_dump") + "' \"$@\"\n"
}

// administrator connects to a database of the running cluster of the data directory as its administrator.
func administrator(t *testing.T, data, name string) *pgx.Conn {
	t.Helper()
	cluster := runningCluster(t, data)
	connection, err := pgx.Connect(context.Background(), cluster.url(adminRole, cluster.secrets.Admin, name, url.Values{}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close(context.Background()) })
	return connection
}

func schemaOf(t *testing.T, connection *pgx.Conn) int64 {
	t.Helper()
	var version int64
	if err := connection.QueryRow(context.Background(), `SELECT max(version) FROM public.schema_migrations`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	return version
}

func sagas(t *testing.T, connection *pgx.Conn) int {
	t.Helper()
	var count int
	if err := connection.QueryRow(context.Background(), `SELECT count(*) FROM public.projects WHERE name = 'Северный предел'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

// passwordFile writes the password of the administrator for the programs of the test.
func passwordFile(t *testing.T, data string) string {
	t.Helper()
	secrets, err := loadClusterSecrets(DataDir{Root: data}.Secret())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "pgpass")
	if err := os.WriteFile(path, []byte("*:*:*:"+adminRole+":"+secrets.Admin+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// postgresProgram runs a PostgreSQL program of the bundle against the running cluster of the data directory as the
// administrator and returns its standard output.
func postgresProgram(t *testing.T, resources, data, name string, args ...string) []byte {
	t.Helper()
	cluster := runningCluster(t, data)
	command := exec.Command(filepath.Join(resources, "postgres", "bin", name),
		append([]string{"-w", "-h", cluster.host, "-p", strconv.Itoa(cluster.port), "-U", adminRole}, args...)...)
	command.Env = append(toolEnvironment(), "PGPASSFILE="+passwordFile(t, data))
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	if err := command.Run(); err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, stderr.String())
	}
	return stdout.Bytes()
}

// makeSaga opens the window and makes the saga «Северный предел».
func makeSaga(t *testing.T, process *backend) {
	t.Helper()
	browser := &client{t: t, origin: process.origin(), http: &http.Client{Timeout: 10 * time.Second}}
	if response := browser.open(process.ready.Code); response.StatusCode != http.StatusOK {
		t.Fatalf("open = %d", response.StatusCode)
	}
	if response, body := browser.do(http.MethodPost, "/api/v1/projects", browser.csrf(), map[string]string{"title": "Северный предел"}); response.StatusCode != http.StatusCreated {
		t.Fatalf("createProject = %d %s", response.StatusCode, body)
	}
}

// copies are the complete copies of backups/ in the order of their names' times, and whatever else is there.
func copies(t *testing.T, data string) (complete []string, other []string) {
	t.Helper()
	for _, name := range names(t, DataDir{Root: data}.Backups()) {
		if _, err := os.Stat(filepath.Join(data, "backups", name, backupComplete)); err == nil && strings.HasPrefix(name, "pre-") {
			complete = append(complete, name)
		} else {
			other = append(other, name)
		}
	}
	slices.SortFunc(complete, func(a, b string) int {
		return strings.Compare(a[strings.LastIndexByte(a, '-'):], b[strings.LastIndexByte(b, '-'):])
	})
	return complete, other
}

// specs/2026-11-12-desk-process.md PR3.1, PR3.2: the first start makes no copy; every start of a newer program on an
// existing database copies the database (pg_dump -Fc of the bundle) and the threads of Codex into
// backups/pre-<the last applied migration>-<time>/ before its migrations, private to the user, without the password of
// the database in the arguments or the environment of pg_dump; the copy brings the database before the migration back
// with pg_restore; an unfinished copy is removed at the start and three updates in a row leave the two newest copies.
func TestAnUpdateCopiesTheDataBeforeItsMigrations(t *testing.T) {
	_, resources := desktopBinary(t)
	last := lastVersion(t)
	data := filepath.Join(t.TempDir(), "data")
	// One bundle for every start: the home of Codex links to the profile of the bundle it started with.
	record := t.TempDir()
	bundle := bundleWithDump(t, resources, recordingDump(resources, record))

	first := startBackend(t, program(t, programBehind(3)), bundle, data, nil)
	makeSaga(t, first)
	first.stop()
	if complete, other := copies(t, data); len(complete) != 0 || len(other) != 0 {
		t.Fatalf("the first start made copies %v %v", complete, other)
	}

	makeCopy(t, DataDir{Root: data}.Backups(), "pre-7-20260101T000000.000Z", false)
	writeFile(t, filepath.Join(data, "backups", "notes.txt"), "the author's")
	if _, err := os.Stat(filepath.Join(record, "cmdline")); err == nil {
		t.Fatal("the first start ran pg_dump")
	}
	second := startBackend(t, program(t, programBehind(2)), bundle, data, nil)
	complete, other := copies(t, data)
	if len(complete) != 1 || !strings.HasPrefix(complete[0], fmt.Sprintf("pre-%d-", last-3)) || !slices.Equal(other, []string{"notes.txt"}) {
		t.Fatalf("backups after the first update = %v %v", complete, other)
	}
	copyPath := filepath.Join(data, "backups", complete[0])
	if info, err := os.Stat(copyPath); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("the copy = %v, %v", info, err)
	}
	for _, part := range []string{backupDump, filepath.Join("codex", "home"), filepath.Join("codex", "sqlite")} {
		if _, err := os.Stat(filepath.Join(copyPath, part)); err != nil {
			t.Fatalf("the copy has no %s: %v", part, err)
		}
	}
	secrets, err := loadClusterSecrets(DataDir{Root: data}.Secret())
	if err != nil {
		t.Fatal(err)
	}
	commandLine, err := os.ReadFile(filepath.Join(record, "cmdline"))
	if err != nil || !bytes.Contains(commandLine, []byte("-Fc")) {
		t.Fatalf("pg_dump ran as %q, %v", commandLine, err)
	}
	environment, err := os.ReadFile(filepath.Join(record, "environ"))
	if err != nil {
		t.Fatal(err)
	}
	for _, password := range []string{secrets.Admin, secrets.Product, secrets.Worker} {
		if bytes.Contains(commandLine, []byte(password)) || bytes.Contains(environment, []byte(password)) {
			t.Fatal("a password of the database is in the arguments or the environment of pg_dump")
		}
	}
	if entries, _ := filepath.Glob(filepath.Join(data, "pg.pass*")); len(entries) != 0 {
		t.Fatalf("the password file of pg_dump outlived the copy: %v", entries)
	}

	admin := administrator(t, data, "postgres")
	if schema := schemaOf(t, administrator(t, data, databaseName)); schema != last-2 {
		t.Fatalf("the schema after the update = %d; want %d", schema, last-2)
	}
	if _, err := admin.Exec(context.Background(), "CREATE DATABASE restored"); err != nil {
		t.Fatal(err)
	}
	postgresProgram(t, resources, data, "pg_restore", "--exit-on-error", "-d", "restored", filepath.Join(copyPath, backupDump))
	restored := administrator(t, data, "restored")
	if schema, saga := schemaOf(t, restored), sagas(t, restored); schema != last-3 || saga != 1 {
		t.Fatalf("the restored copy = schema %d with %d sagas; want %d with 1", schema, saga, last-3)
	}
	_ = restored.Close(context.Background())
	if _, err := admin.Exec(context.Background(), "DROP DATABASE restored"); err != nil {
		t.Fatal(err)
	}
	_ = admin.Close(context.Background())
	second.stop()

	startBackend(t, program(t, programBehind(1)), bundle, data, nil).stop()
	third := startBackend(t, program(t, programOfTheTree), bundle, data, nil)
	complete, _ = copies(t, data)
	if len(complete) != 2 || !strings.HasPrefix(complete[0], fmt.Sprintf("pre-%d-", last-2)) || !strings.HasPrefix(complete[1], fmt.Sprintf("pre-%d-", last-1)) {
		t.Fatalf("the copies after three updates = %v", complete)
	}
	if schema := schemaOf(t, administrator(t, data, databaseName)); schema != last {
		t.Fatalf("the schema after three updates = %d", schema)
	}
	third.stop()
}

// PR3.3: a copy that fails stops the start before the migrations with the reason backup and leaves nothing behind; a
// migration that fails after the copy stops it with the reason migrate and the name of the copy. Either way the program
// of the schema the data have starts on them and finds the saga.
func TestAFailedCopyOrMigrationLeavesTheDataAsTheyWere(t *testing.T) {
	_, resources := desktopBinary(t)
	last := lastVersion(t)
	data := filepath.Join(t.TempDir(), "data")
	earlier := program(t, programBehind(1))

	first := startBackend(t, earlier, resources, data, nil)
	makeSaga(t, first)
	first.stop()

	withoutDump := launchBackend(t, program(t, programOfTheTree), bundleWithDump(t, resources, ""), data, nil)
	if withoutDump.ready.Type != messageFatal || withoutDump.ready.Reason != "backup" || withoutDump.ready.Backup != "" {
		t.Fatalf("a start without pg_dump answered %+v", withoutDump.ready)
	}
	withoutDump.exited(60 * time.Second)
	again := startBackend(t, earlier, resources, data, nil)
	if schema, saga := schemaOf(t, administrator(t, data, databaseName)), sagas(t, administrator(t, data, databaseName)); schema != last-1 || saga != 1 {
		t.Fatalf("after a failed copy = schema %d with %d sagas", schema, saga)
	}
	if complete, other := copies(t, data); len(complete) != 0 || len(other) != 0 {
		t.Fatalf("a failed copy left %v %v", complete, other)
	}
	again.stop()

	startBackend(t, program(t, programOfTheTree), resources, data, nil).stop()
	broken := launchBackend(t, program(t, programWithABrokenMigration), resources, data, nil)
	if broken.ready.Type != messageFatal || broken.ready.Reason != "migrate" || !strings.HasPrefix(broken.ready.Backup, fmt.Sprintf("pre-%d-", last)) {
		t.Fatalf("a broken migration answered %+v", broken.ready)
	}
	broken.exited(60 * time.Second)
	if _, err := os.Stat(filepath.Join(data, "backups", broken.ready.Backup, backupComplete)); err != nil {
		t.Fatalf("the copy the fatal names: %v", err)
	}
	// DT13.6: a migration that fails start after start leaves no more than two copies, the one the fatal names among them.
	for attempt := 2; attempt <= 3; attempt++ {
		again := launchBackend(t, program(t, programWithABrokenMigration), resources, data, nil)
		if again.ready.Type != messageFatal || again.ready.Reason != "migrate" || again.ready.Backup == "" {
			t.Fatalf("broken start %d answered %+v", attempt, again.ready)
		}
		again.exited(60 * time.Second)
		complete, other := copies(t, data)
		if len(complete) > backupsKept || len(other) != 0 || !slices.Contains(complete, again.ready.Backup) {
			t.Fatalf("the copies after broken start %d = %v %v; the fatal names %s", attempt, complete, other, again.ready.Backup)
		}
	}
	current := startBackend(t, program(t, programOfTheTree), resources, data, nil)
	browser := &client{t: t, origin: current.origin(), http: &http.Client{Timeout: 10 * time.Second}}
	if response := browser.open(current.ready.Code); response.StatusCode != http.StatusOK {
		t.Fatalf("open = %d", response.StatusCode)
	}
	if response, body := browser.do(http.MethodGet, "/api/v1/projects", "", nil); response.StatusCode != http.StatusOK || !strings.Contains(string(body), "Северный предел") {
		t.Fatalf("the saga after a failed migration = %d %s", response.StatusCode, body)
	}
	if schema := schemaOf(t, administrator(t, data, databaseName)); schema != last {
		t.Fatalf("the schema after a failed migration = %d", schema)
	}
	current.stop()
}

// stoppedFingerprint is the content of the database of a stopped data directory: its cluster starts by itself, gives a
// pg_dump of the data and stops.
func stoppedFingerprint(t *testing.T, resources, data string) []byte {
	t.Helper()
	secrets, err := loadClusterSecrets(DataDir{Root: data}.Secret())
	if err != nil {
		t.Fatal(err)
	}
	cluster := newCluster(Resources{Root: resources}.PostgresBin(), DataDir{Root: data}, secrets)
	if err := cluster.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := cluster.Stop(context.Background()); err != nil {
			t.Fatal(err)
		}
	}()
	var kept []string
	for _, line := range strings.Split(string(postgresProgram(t, resources, data, "pg_dump", "--data-only", "-d", databaseName)), "\n") {
		if !strings.HasPrefix(line, `\restrict`) && !strings.HasPrefix(line, `\unrestrict`) {
			kept = append(kept, line)
		}
	}
	return []byte(strings.Join(kept, "\n"))
}

// PR3.4: a program older than the schema of the data stops with the reason newer_schema before it changes anything: no
// row of the database differs and no copy is made.
func TestAProgramOlderThanItsDataRefusesToStart(t *testing.T) {
	_, resources := desktopBinary(t)
	data := filepath.Join(t.TempDir(), "data")
	current := startBackend(t, program(t, programOfTheTree), resources, data, nil)
	makeSaga(t, current)
	current.stop()

	before := stoppedFingerprint(t, resources, data)
	if !bytes.Contains(before, []byte("Северный предел")) {
		t.Fatal("the fingerprint does not hold the saga")
	}
	older := launchBackend(t, program(t, programBehind(1)), resources, data, nil)
	if older.ready.Type != messageFatal || older.ready.Reason != "newer_schema" {
		t.Fatalf("an older program answered %+v", older.ready)
	}
	older.exited(60 * time.Second)
	if after := stoppedFingerprint(t, resources, data); !bytes.Equal(before, after) {
		t.Fatal("the older program changed the database")
	}
	if complete, other := copies(t, data); len(complete) != 0 || len(other) != 0 {
		t.Fatalf("the older program made copies %v %v", complete, other)
	}
}

// PR3.5: a migration changed after the database applied it is refused as the runner refuses it, though nothing is
// pending: the start stops with the reason database before it changes anything and makes no copy.
func TestAMigrationChangedAfterItWasAppliedStopsTheStart(t *testing.T) {
	_, resources := desktopBinary(t)
	data := filepath.Join(t.TempDir(), "data")
	startBackend(t, program(t, programOfTheTree), resources, data, nil).stop()

	edited := launchBackend(t, program(t, programWithAnEditedMigration), resources, data, nil)
	if edited.ready.Type != messageFatal || edited.ready.Reason != "database" {
		t.Fatalf("a program with an edited migration answered %+v", edited.ready)
	}
	edited.exited(60 * time.Second)
	if complete, other := copies(t, data); len(complete) != 0 || len(other) != 0 {
		t.Fatalf("the program with an edited migration made copies %v %v", complete, other)
	}
}
