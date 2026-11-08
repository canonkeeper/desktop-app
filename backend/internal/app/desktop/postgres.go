// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	storedb "canonkeeper/backend/internal/store/db"

	"github.com/jackc/pgx/v5"
)

// The roles and the database of the cluster are those of infra/postgres/init-local.sh and bootstrap-local.sh: the
// product and the worker log in with their passwords, the migrations run as the administrator (DA1.3).
const (
	adminRole    = "canonkeeper_local_admin"
	productRole  = "canonkeeper_local_product"
	workerRole   = "canonkeeper_local_worker"
	databaseName = "canonkeeper"
	// socketPort names the socket file of a cluster without TCP: the directory is the user's alone.
	socketPort = 5432
	// settingsMark opens the settings the backend adds to postgresql.conf once, after initdb.
	settingsMark = "# CanonKeeper desktop (specs/2026-11-07-desktop-app.md DA1.3, DA1.8)"
	runSettings  = "canonkeeper.run.conf"
	// maxSocketPath is the longest path of a Unix socket on every system: sockaddr_un holds 104 bytes on macOS and 108
	// on Linux with the final NUL.
	maxSocketPath = 103
)

// clusterSettings keep the cluster small (DA1.8; S0: the default max_wal_size tripled the data directory) and its log
// free of the text of books: no statement and no detail of an error is logged.
var clusterSettings = []string{
	"max_connections = 20",
	"max_wal_size = 256MB",
	"min_wal_size = 32MB",
	"log_min_messages = warning",
	"log_min_error_statement = panic",
	"log_error_verbosity = terse",
	"include_if_exists = '" + runSettings + "'",
}

// Cluster is the PostgreSQL 18 of the author (DA1.3): the programs of the bundle, the cluster in the data directory,
// a Unix socket in a directory of the user on macOS and Linux and 127.0.0.1 on a free port on Windows, SCRAM passwords
// for every role. The backend owns it: it creates, starts and stops it, and stops the one a crash left running.
type Cluster struct {
	bin     string
	data    DataDir
	secrets clusterSecrets
	// host is the socket directory or 127.0.0.1, port its port, both of the running cluster; fallback is a socket
	// directory outside the data directory the cluster removes when it stops.
	host     string
	port     int
	fallback string
}

var (
	errClusterDamaged = errors.New("desktop: the cluster directory holds something that is not a cluster")
	errPostgresTool   = errors.New("desktop: a PostgreSQL program failed")
)

func newCluster(bin string, data DataDir, secrets clusterSecrets) *Cluster {
	return &Cluster{bin: bin, data: data, secrets: secrets}
}

// Start brings the cluster up (DA1.6): the one a crashed backend left running stops first, a missing cluster is
// created, and the cluster starts on this run's socket or port and waits until it accepts connections.
func (cluster *Cluster) Start(ctx context.Context) error {
	if err := cluster.stopLeftover(ctx); err != nil {
		return err
	}
	created, err := cluster.initialized()
	if err != nil {
		return err
	}
	if !created {
		if err := cluster.initialize(ctx); err != nil {
			return err
		}
	}
	if err := cluster.ensureSettings(); err != nil {
		return err
	}
	if err := cluster.listen(); err != nil {
		return err
	}
	logFile := filepath.Join(cluster.data.Logs(), "postgres-"+time.Now().UTC().Format("2006-01-02")+".log")
	return cluster.tool(ctx, "pg_ctl", "start", "-w", "-t", "60", "-D", cluster.data.Cluster(), "-l", logFile)
}

// Stop stops the cluster fast (pg_ctl stop -m fast): open transactions roll back, what was committed stays.
func (cluster *Cluster) Stop(ctx context.Context) error {
	err := cluster.tool(ctx, "pg_ctl", "stop", "-w", "-t", "60", "-m", "fast", "-D", cluster.data.Cluster())
	if cluster.fallback != "" {
		_ = os.RemoveAll(cluster.fallback)
	}
	return err
}

// stopLeftover stops the cluster a crashed backend left running: postmaster.pid names it, and pg_ctl status says
// whether its process lives (DA1.6).
func (cluster *Cluster) stopLeftover(ctx context.Context) error {
	if _, err := os.Stat(filepath.Join(cluster.data.Cluster(), "postmaster.pid")); err != nil {
		return nil
	}
	if cluster.tool(ctx, "pg_ctl", "status", "-D", cluster.data.Cluster()) != nil {
		// No live server: pg_ctl start sees the stale file and starts anyway.
		return nil
	}
	if err := cluster.tool(ctx, "pg_ctl", "stop", "-w", "-t", "60", "-m", "fast", "-D", cluster.data.Cluster()); err != nil {
		return cluster.tool(ctx, "pg_ctl", "stop", "-w", "-t", "30", "-m", "immediate", "-D", cluster.data.Cluster())
	}
	return nil
}

// initialized reports a cluster in the data directory; a directory with files but without PG_VERSION is refused, not
// overwritten.
func (cluster *Cluster) initialized() (bool, error) {
	if _, err := os.Stat(filepath.Join(cluster.data.Cluster(), "PG_VERSION")); err == nil {
		return true, nil
	}
	entries, err := os.ReadDir(cluster.data.Cluster())
	if errors.Is(err, fs.ErrNotExist) || err == nil && len(entries) == 0 {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return false, errClusterDamaged
}

// initialize runs initdb (DA1.3): the builtin C.UTF-8 provider gives the same case and order on every system without
// ICU, LC_CTYPE is a UTF-8 locale of the system — with C the text search parser takes every non-ASCII character for a
// letter (S0) — and everything else is C; the administrator gets its SCRAM password from a file that lives only for
// initdb.
func (cluster *Cluster) initialize(ctx context.Context) error {
	passwords := filepath.Join(cluster.data.Root, "pg.initdb")
	if err := os.WriteFile(passwords, []byte(cluster.secrets.Admin+"\n"), 0o600); err != nil {
		return err
	}
	defer os.Remove(passwords)
	return cluster.tool(ctx, "initdb", "-D", cluster.data.Cluster(), "-U", adminRole, "--pwfile="+passwords,
		"--auth=scram-sha-256", "--encoding=UTF8", "--locale-provider=builtin", "--builtin-locale=C.UTF-8",
		"--lc-ctype="+systemUTF8Locale(), "--lc-collate=C", "--lc-messages=C", "--lc-monetary=C", "--lc-numeric=C",
		"--lc-time=C", "--no-instructions")
}

// systemUTF8Locale is the UTF-8 locale every system of DA2.1 has (DA1.3).
func systemUTF8Locale() string {
	switch runtime.GOOS {
	case "windows":
		return "en-US"
	case "darwin":
		return "en_US.UTF-8"
	default:
		return "C.UTF-8"
	}
}

// ensureSettings adds clusterSettings to postgresql.conf once: an initdb interrupted before them gets them on the next
// start.
func (cluster *Cluster) ensureSettings() error {
	path := filepath.Join(cluster.data.Cluster(), "postgresql.conf")
	current, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if strings.Contains(string(current), settingsMark) {
		return nil
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	if _, err := file.WriteString("\n" + settingsMark + "\n" + strings.Join(clusterSettings, "\n") + "\n"); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

// listen writes how this run's cluster listens (DA1.3, DA3.10): on macOS and Linux only a Unix socket in pg.run, or in
// a short directory of the user's own when the path of the socket would be too long; on Windows 127.0.0.1 on a port
// that is free now.
func (cluster *Cluster) listen() error {
	var settings []string
	if runtime.GOOS == "windows" {
		port, err := freePort()
		if err != nil {
			return err
		}
		cluster.host, cluster.port = "127.0.0.1", port
		settings = []string{"listen_addresses = '127.0.0.1'", "port = " + strconv.Itoa(port), "unix_socket_directories = ''"}
	} else {
		directory := cluster.data.Socket()
		if len(filepath.Join(directory, ".s.PGSQL."+strconv.Itoa(socketPort))) > maxSocketPath {
			fallback, err := os.MkdirTemp("", "ck-pg-")
			if err != nil {
				return err
			}
			directory, cluster.fallback = fallback, fallback
		}
		if err := os.Chmod(directory, 0o700); err != nil {
			return err
		}
		cluster.host, cluster.port = directory, socketPort
		settings = []string{
			"listen_addresses = ''", "port = " + strconv.Itoa(socketPort),
			"unix_socket_directories = '" + strings.ReplaceAll(directory, "'", "''") + "'", "unix_socket_permissions = 0700",
		}
	}
	return os.WriteFile(filepath.Join(cluster.data.Cluster(), runSettings), []byte(strings.Join(settings, "\n")+"\n"), 0o600)
}

func freePort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port, nil
}

// url is the address of a database of the running cluster for a role; parameters are the pool's and the session's.
func (cluster *Cluster) url(role, password, database string, parameters url.Values) string {
	query := url.Values{"sslmode": {"disable"}}
	for key, values := range parameters {
		query[key] = values
	}
	address := url.URL{Scheme: "postgres", User: url.UserPassword(role, password), Path: "/" + database}
	if runtime.GOOS == "windows" {
		address.Host = net.JoinHostPort(cluster.host, strconv.Itoa(cluster.port))
	} else {
		query.Set("host", cluster.host)
		query.Set("port", strconv.Itoa(cluster.port))
	}
	address.RawQuery = query.Encode()
	return address.String()
}

// ProductURL is the address of the product's pool; WorkerURL is the worker's, which acts as the discovery role.
// Both pools are small (DA1.8).
func (cluster *Cluster) ProductURL() string {
	return cluster.url(productRole, cluster.secrets.Product, databaseName, url.Values{"pool_max_conns": {"8"}})
}

func (cluster *Cluster) WorkerURL() string {
	return cluster.url(workerRole, cluster.secrets.Worker, databaseName, url.Values{
		"pool_max_conns": {"3"}, "options": {"-c role=canonkeeper_worker_discovery"},
	})
}

// Bootstrap prepares the database of the product (DA1.3): the roles and the database of init-local.sh with this
// computer's passwords, the numbered migrations of the site as the administrator, and the runtime grants of
// bootstrap-local.sh. Every step is repeatable, so an interrupted first start finishes on the next one.
func (cluster *Cluster) Bootstrap(ctx context.Context) error {
	admin, err := pgx.Connect(ctx, cluster.url(adminRole, cluster.secrets.Admin, "postgres", nil))
	if err != nil {
		return fmt.Errorf("connect the administrator: %w", err)
	}
	defer admin.Close(context.Background())
	var exists bool
	if err := admin.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)`, databaseName).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		if _, err := admin.Exec(ctx, "CREATE DATABASE "+databaseName); err != nil {
			return err
		}
	}
	for role, password := range map[string]string{productRole: cluster.secrets.Product, workerRole: cluster.secrets.Worker} {
		if err := admin.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = $1)`, role).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			if _, err := admin.Exec(ctx, "CREATE ROLE "+role+" LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION"); err != nil {
				return err
			}
		}
		if _, err := admin.Exec(ctx, "ALTER ROLE "+role+" PASSWORD "+quoteLiteral(password)); err != nil {
			return err
		}
	}
	for _, statement := range []string{
		"REVOKE CONNECT ON DATABASE postgres FROM PUBLIC",
		"REVOKE CONNECT ON DATABASE " + databaseName + " FROM PUBLIC",
		"GRANT CONNECT ON DATABASE " + databaseName + " TO " + productRole + ", " + workerRole,
	} {
		if _, err := admin.Exec(ctx, statement); err != nil {
			return err
		}
	}
	database, err := storedb.Open(ctx, cluster.url(adminRole, cluster.secrets.Admin, databaseName, url.Values{"pool_max_conns": {"2"}}))
	if err != nil {
		return fmt.Errorf("connect the database: %w", err)
	}
	defer database.Close()
	if err := storedb.ApplyMigrations(ctx, database); err != nil {
		return fmt.Errorf("apply migrations: %w", err)
	}
	for _, statement := range []string{
		"GRANT canonkeeper_product_runtime TO " + productRole,
		"GRANT canonkeeper_worker_discovery TO " + workerRole,
	} {
		if _, err := database.Pool.Exec(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}

// quoteLiteral quotes a string as an SQL literal; utility statements take no parameters.
func quoteLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

// tool runs a PostgreSQL program of the bundle with its output in a file, not a pipe: on Windows the server pg_ctl
// starts inherits the handles of pg_ctl, and waiting for a pipe would never end (S0). The error names the program and
// the last lines of its output, which name no text of books.
func (cluster *Cluster) tool(ctx context.Context, name string, args ...string) error {
	program := filepath.Join(cluster.bin, name)
	if runtime.GOOS == "windows" {
		program += ".exe"
	}
	output, err := os.CreateTemp(cluster.data.Logs(), "pg-tool-*.log")
	if err != nil {
		return err
	}
	defer os.Remove(output.Name())
	defer output.Close()
	toolCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	command := exec.CommandContext(toolCtx, program, args...)
	command.Stdout, command.Stderr = output, output
	command.Env = toolEnvironment()
	if err := command.Run(); err != nil {
		raw, _ := os.ReadFile(output.Name())
		return fmt.Errorf("%w: %s %s: %v: %s", errPostgresTool, name, firstArg(args), err, lastLines(string(raw), 8))
	}
	return nil
}

// toolEnvironment is the environment of the programs: the user's, without the PG* variables that would point them at
// another server, and with the C locale for their messages.
func toolEnvironment() []string {
	var environment []string
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(strings.ToUpper(name), "PG") || name == "LC_ALL" || name == "LC_MESSAGES" {
			continue
		}
		environment = append(environment, entry)
	}
	return append(environment, "LC_MESSAGES=C")
}

func firstArg(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return args[0]
}

func lastLines(text string, count int) string {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	if len(lines) > count {
		lines = lines[len(lines)-count:]
	}
	return strings.Join(lines, " | ")
}
