// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/hex"
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

//go:embed probe.sql
var probeSQL string

// Locale variants of initdb. All use the builtin C.UTF-8 provider for collation and SQL case
// mapping; they differ in LC_CTYPE, which PostgreSQL 18 still consults for the text search
// parser (database_ctype_is_c makes every non-ASCII character a letter).
var pgVariants = []struct {
	name string
	args func() []string
}{
	{"ctype_os_utf8", func() []string {
		return []string{"--lc-ctype=" + osUTF8Locale(), "--lc-collate=C", "--lc-messages=C", "--lc-monetary=C", "--lc-numeric=C", "--lc-time=C"}
	}},
	{"ctype_c", func() []string { return []string{"--locale=C"} }},
	{"ctype_env", func() []string { return nil }},
}

func osUTF8Locale() string {
	switch runtime.GOOS {
	case "windows":
		return "en-US"
	case "darwin":
		return "en_US.UTF-8"
	default:
		return "C.UTF-8"
	}
}

var pgChecks = []struct{ name, sql string }{
	{"version", "SELECT version()"},
	{"database_locale", "SELECT concat_ws(' | ', datlocprovider, datlocale, datcollate, datctype, pg_encoding_to_char(encoding)) FROM pg_database WHERE datname = current_database()"},
	{"lower_upper", "SELECT lower('ЁЖ ПЬЕР') || ' / ' || upper('ёж пьер') || ' / ' || initcap('пьер безухов')"},
	{"ts_lexize", "SELECT ts_lexize('russian_stem', 'кутузова')::text"},
	{"to_tsvector", "SELECT to_tsvector('russian', '«Война и мир» — роман… Кутузова! Ёлка, ель.')::text"},
	{"websearch", "SELECT count(*)::text FROM probe.chapters WHERE search @@ websearch_to_tsquery('russian', 'Кутузова ждали')"},
	{"websearch_quoted", "SELECT count(*)::text FROM probe.chapters WHERE search @@ websearch_to_tsquery('russian', '\"Кутузова ждали\"')"},
	{"order", "SELECT string_agg(w, ' ' ORDER BY w) FROM unnest(ARRAY['ёж', 'еж', 'Яблоко', 'абв', 'Ёлка', 'ель', 'Zeta', 'alpha']) AS w"},
	{"ilike_regex", "SELECT ('ПЬЕР' ILIKE 'пьер')::text || ' ' || ('Ёлка' ~* '^ёлка$')::text"},
	{"jsonb", "SELECT meta->'tags'->>0 FROM probe.chapters WHERE key = 'ch-001'"},
	{"trigger", "SELECT count(*)::text FROM probe.audit"},
}

var pgExpected = map[string]string{
	"lower_upper":      "ёж пьер / ЁЖ ПЬЕР / Пьер Безухов",
	"ts_lexize":        "{кутузов}",
	"websearch":        "1",
	"websearch_quoted": "1",
	"ilike_regex":      "true true",
	"jsonb":            "война",
	"trigger":          "2",
}

func runPG(args []string) error {
	flags := flag.NewFlagSet("pg", flag.ExitOnError)
	bin := flags.String("bin", "", "bin directory of the PostgreSQL set")
	root := flags.String("root", "", "data root (default: the product data directory with a Cyrillic segment)")
	out := flags.String("out", "-", "result JSON")
	flags.Parse(args)
	if *bin == "" {
		return fmt.Errorf("-bin is required")
	}
	if *root == "" {
		value, err := productDataRoot()
		if err != nil {
			return fmt.Errorf("data root: %w", err)
		}
		*root = value
	}
	binDir, err := filepath.Abs(*bin)
	if err != nil {
		return err
	}
	result := map[string]any{"system": systemInfo(), "root": *root}
	variants := []map[string]any{}
	for index, variant := range pgVariants {
		report := map[string]any{"variant": variant.name}
		err := pgVariant(binDir, filepath.Join(*root, "pg-"+strconv.Itoa(index)), variant.args(), index == 0, report)
		if err != nil {
			report["error"] = err.Error()
		}
		variants = append(variants, report)
	}
	if runtime.GOOS == "windows" {
		// The fallback of the spec for a non-ASCII profile path: its 8.3 alias.
		report := map[string]any{"variant": "short_path"}
		dir := filepath.Join(*root, "pg-short")
		err := os.MkdirAll(dir, 0o700)
		if err == nil {
			var short string
			short, err = shortPath(dir)
			report["short_path"] = short
			if err == nil {
				err = pgVariant(binDir, short, pgVariants[0].args(), false, report)
			}
		}
		if err != nil {
			report["error"] = err.Error()
		}
		variants = append(variants, report)
	}
	result["variants"] = variants
	return writeJSON(*out, result)
}

func exe(binDir, name string) string {
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(binDir, name)
}

func randomHex(n int) string {
	buffer := make([]byte, n)
	if _, err := rand.Read(buffer); err != nil {
		panic(err)
	}
	return hex.EncodeToString(buffer)
}

func freePort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port, nil
}

// runTool runs a PostgreSQL program with its output in a file, not a pipe: on Windows the server
// that `pg_ctl start` launches inherits the handles of pg_ctl, and a pipe would stay open for as
// long as the server runs, so waiting for pg_ctl would never end.
func runTool(path string, args ...string) (string, time.Duration, error) {
	command := exec.Command(path, args...)
	log, err := os.CreateTemp("", "pg-tool-*.log")
	if err != nil {
		return "", 0, err
	}
	defer os.Remove(log.Name())
	defer log.Close()
	command.Stdout = log
	command.Stderr = log
	started := time.Now()
	err = command.Start()
	if err == nil {
		done := make(chan error, 1)
		go func() { done <- command.Wait() }()
		select {
		case err = <-done:
		case <-time.After(3 * time.Minute):
			_ = command.Process.Kill()
			err = fmt.Errorf("no exit in 3 minutes")
		}
	}
	elapsed := time.Since(started)
	raw, _ := os.ReadFile(log.Name())
	output := string(raw)
	if err != nil {
		return output, elapsed, fmt.Errorf("%s: %w: %s", filepath.Base(path), err, lastLines(output, 12))
	}
	return output, elapsed, nil
}

func lastLines(text string, n int) string {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

func pgVariant(binDir, dir string, localeArgs []string, full bool, report map[string]any) error {
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data := filepath.Join(dir, "pg")
	password := randomHex(24)
	secret := filepath.Join(dir, "pg.secret")
	if err := os.WriteFile(secret, []byte(password+"\n"), 0o600); err != nil {
		return err
	}
	initArgs := []string{"-D", data, "-U", "canonkeeper_admin", "--pwfile=" + secret, "--auth=scram-sha-256",
		"--encoding=UTF8", "--locale-provider=builtin", "--builtin-locale=C.UTF-8", "--no-instructions"}
	initArgs = append(initArgs, localeArgs...)
	report["initdb_args"] = strings.Join(initArgs[5:], " ")
	output, elapsed, err := runTool(exe(binDir, "initdb"), initArgs...)
	report["initdb_seconds"] = seconds(elapsed)
	if err != nil {
		return err
	}
	if strings.Contains(output, "WARNING") {
		report["initdb_warnings"] = lastLines(output, 6)
	}
	size, _ := dirSize(data)
	report["cluster_bytes_after_initdb"] = size

	connect, err := configureCluster(dir, data, report)
	if err != nil {
		return err
	}
	logFile := filepath.Join(dir, "postgres.log")
	_, elapsed, err = runTool(exe(binDir, "pg_ctl"), "start", "-w", "-t", "60", "-D", data, "-l", logFile)
	report["first_start_seconds"] = seconds(elapsed)
	if err != nil {
		report["postgres_log"] = readTail(logFile)
		return err
	}
	stop := func() (time.Duration, error) {
		_, elapsed, err := runTool(exe(binDir, "pg_ctl"), "stop", "-w", "-m", "fast", "-D", data)
		return elapsed, err
	}
	defer func() {
		if _, err := os.Stat(filepath.Join(data, "postmaster.pid")); err == nil {
			_, _ = stop()
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	checks, err := pgSession(ctx, connect, password, report)
	report["checks"] = checks
	if err != nil {
		report["postgres_log"] = readTail(logFile)
		return err
	}
	mismatches := map[string]string{}
	for name, want := range pgExpected {
		if got, ok := checks[name]; !ok || got != want {
			mismatches[name] = fmt.Sprintf("got %q, want %q", got, want)
		}
	}
	report["checks_ok"] = len(mismatches) == 0
	if len(mismatches) > 0 {
		report["check_mismatches"] = mismatches
	}
	if !full {
		elapsed, err = stop()
		report["stop_seconds"] = seconds(elapsed)
		return err
	}

	time.Sleep(3 * time.Second)
	if pid, err := postmasterPID(data); err == nil {
		report["idle_memory"] = processTreeMemory(pid, nil)
	}
	elapsed, err = stop()
	report["stop_seconds"] = seconds(elapsed)
	if err != nil {
		return err
	}
	size, _ = dirSize(data)
	report["cluster_bytes_after_probe"] = size
	_, elapsed, err = runTool(exe(binDir, "pg_ctl"), "start", "-w", "-t", "60", "-D", data, "-l", logFile)
	report["second_start_seconds"] = seconds(elapsed)
	if err != nil {
		return err
	}
	conn, err := pgx.Connect(ctx, connect("canonkeeper_admin", password, "canonkeeper"))
	if err != nil {
		return fmt.Errorf("reconnect after restart: %w", err)
	}
	var count int
	err = conn.QueryRow(ctx, "SELECT count(*) FROM probe.chapters").Scan(&count)
	conn.Close(ctx)
	report["rows_after_restart"] = count
	if err != nil {
		return err
	}
	elapsed, err = stop()
	report["second_stop_seconds"] = seconds(elapsed)
	return err
}

// configureCluster writes the desktop settings: on macOS and Linux only a Unix socket in a 0700
// directory next to the cluster, on Windows 127.0.0.1 on a free port.
func configureCluster(dir, data string, report map[string]any) (func(user, password, database string) string, error) {
	settings := []string{"max_connections = 20", "max_wal_size = 256MB", "min_wal_size = 32MB"}
	var connect func(user, password, database string) string
	if runtime.GOOS == "windows" {
		port, err := freePort()
		if err != nil {
			return nil, err
		}
		settings = append(settings, "listen_addresses = '127.0.0.1'", fmt.Sprintf("port = %d", port), "unix_socket_directories = ''")
		report["listen"] = fmt.Sprintf("127.0.0.1:%d", port)
		connect = func(user, password, database string) string {
			return fmt.Sprintf("host=127.0.0.1 port=%d user=%s password=%s dbname=%s sslmode=disable", port, user, password, database)
		}
	} else {
		socketDir := filepath.Join(dir, "pg.run")
		socketPath := filepath.Join(socketDir, ".s.PGSQL.5432")
		report["socket_path_bytes"] = len(socketPath)
		// sockaddr_un holds 104 bytes on macOS and 108 on Linux, including the final NUL.
		if len(socketPath) > 103 {
			socketDir = filepath.Join(os.TempDir(), "ck-pg-"+randomHex(4))
			report["socket_fallback"] = socketDir
		}
		if err := os.MkdirAll(socketDir, 0o700); err != nil {
			return nil, err
		}
		settings = append(settings, "listen_addresses = ''", "unix_socket_directories = '"+strings.ReplaceAll(socketDir, "'", "''")+"'", "unix_socket_permissions = 0700")
		report["listen"] = "unix:" + socketDir
		connect = func(user, password, database string) string {
			return fmt.Sprintf("host='%s' user=%s password=%s dbname=%s sslmode=disable", socketDir, user, password, database)
		}
	}
	file, err := os.OpenFile(filepath.Join(data, "postgresql.conf"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	_, err = file.WriteString("\n# CanonKeeper desktop probe\n" + strings.Join(settings, "\n") + "\n")
	return connect, err
}

func pgSession(ctx context.Context, connect func(user, password, database string) string, password string, report map[string]any) (map[string]string, error) {
	checks := map[string]string{}
	started := time.Now()
	admin, err := pgx.Connect(ctx, connect("canonkeeper_admin", password, "postgres"))
	if err != nil {
		return checks, fmt.Errorf("connect admin: %w", err)
	}
	report["connect_seconds"] = seconds(time.Since(started))
	productPassword := randomHex(24)
	for _, statement := range []string{
		"CREATE DATABASE canonkeeper",
		"CREATE ROLE canonkeeper_product LOGIN PASSWORD '" + productPassword + "'",
	} {
		if _, err := admin.Exec(ctx, statement); err != nil {
			admin.Close(ctx)
			return checks, fmt.Errorf("%s: %w", strings.Fields(statement)[1], err)
		}
	}
	admin.Close(ctx)

	conn, err := pgx.Connect(ctx, connect("canonkeeper_admin", password, "canonkeeper"))
	if err != nil {
		return checks, fmt.Errorf("connect canonkeeper: %w", err)
	}
	defer conn.Close(ctx)
	started = time.Now()
	if _, err := conn.Exec(ctx, probeSQL); err != nil {
		return checks, fmt.Errorf("probe.sql: %w", err)
	}
	report["schema_seconds"] = seconds(time.Since(started))
	if _, err := conn.Exec(ctx, "GRANT probe_group TO canonkeeper_product; GRANT CONNECT ON DATABASE canonkeeper TO canonkeeper_product"); err != nil {
		return checks, err
	}
	for _, check := range pgChecks {
		var value string
		if err := conn.QueryRow(ctx, check.sql).Scan(&value); err != nil {
			checks[check.name] = "error: " + err.Error()
			continue
		}
		checks[check.name] = value
	}

	product, err := pgx.Connect(ctx, connect("canonkeeper_product", productPassword, "canonkeeper"))
	if err != nil {
		checks["scram_product_login"] = "error: " + err.Error()
		return checks, nil
	}
	defer product.Close(ctx)
	checks["scram_product_login"] = "ok"
	var claimed *string
	if err := product.QueryRow(ctx, "SELECT probe.claim_next()::text").Scan(&claimed); err != nil {
		checks["security_definer_claim"] = "error: " + err.Error()
	} else if claimed != nil {
		checks["security_definer_claim"] = "ok"
	}
	var denied error
	_, denied = product.Exec(ctx, "SELECT count(*) FROM probe.chapters")
	if denied != nil {
		checks["direct_table_read_denied"] = "ok"
	} else {
		checks["direct_table_read_denied"] = "no: product role read the table"
	}
	return checks, nil
}

func postmasterPID(data string) (int32, error) {
	raw, err := os.ReadFile(filepath.Join(data, "postmaster.pid"))
	if err != nil {
		return 0, err
	}
	first := strings.TrimSpace(strings.SplitN(string(raw), "\n", 2)[0])
	pid, err := strconv.Atoi(first)
	return int32(pid), err
}

func readTail(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return lastLines(string(raw), 20)
}

func dirSize(root string) (int64, error) {
	var total int64
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.Mode().IsRegular() {
			total += info.Size()
		}
		return nil
	})
	return total, err
}
