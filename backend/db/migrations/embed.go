package migrations

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// FS is the canonical, ordered migration inventory.
//
//go:embed *.sql
var FS embed.FS

type Migration struct {
	Version  int64
	Name     string
	SQL      []byte
	Checksum string
}

// Read returns every numbered migration and validates the inventory before a
// caller opens a database.
func Read() ([]Migration, error) {
	return ReadFS(FS)
}

// ReadFS is used by inventory tests to exercise the same validation against a
// small in-memory filesystem.
func ReadFS(fsys fs.FS) ([]Migration, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, fmt.Errorf("read migration inventory: %w", err)
	}
	files := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		files = append(files, entry.Name())
	}
	sort.Strings(files)

	migrations := make([]Migration, 0, len(files))
	for index, name := range files {
		version, err := migrationVersion(name)
		if err != nil {
			return nil, err
		}
		if version != int64(index) {
			return nil, fmt.Errorf("migration inventory is not sequential: file %q has version %d, want %d", name, version, index)
		}
		raw, err := fs.ReadFile(fsys, name)
		if err != nil {
			return nil, fmt.Errorf("read migration %q: %w", name, err)
		}
		if err := ValidateSQL(name, raw); err != nil {
			return nil, err
		}
		digest := sha256.Sum256(raw)
		migrations = append(migrations, Migration{
			Version:  version,
			Name:     name,
			SQL:      raw,
			Checksum: hex.EncodeToString(digest[:]),
		})
	}
	if len(migrations) == 0 {
		return nil, fmt.Errorf("migration inventory is empty")
	}
	return migrations, nil
}

var (
	destructiveStatement = regexp.MustCompile(`(?is)\b(?:TRUNCATE|DROP\s+(?:TABLE|SCHEMA|DATABASE))\b`)
	deleteStatement      = regexp.MustCompile(`(?is)\bDELETE\s+FROM\b.*?(?:;|$)`)
	filterClause         = regexp.MustCompile(`(?is)\b(?:WHERE|USING)\b`)
)

// destructiveCleanupMigrations drop the tables of removed product features; every other
// forward migration must keep data.
var destructiveCleanupMigrations = map[string]bool{
	"0017_drop_legacy_agent_runtime.sql":          true,
	"0038_remove_writer_and_canon_correction.sql": true,
	"0041_remove_canon_graph.sql":                 true,
	"0044_remove_writer_leases.sql":               true,
	"0045_remove_audited_import_queues.sql":       true,
	"0046_remove_export_and_audit.sql":            true,
	"0047_remove_reader_annotations.sql":          true,
}

// ValidateSQL rejects destructive statements from normal forward migrations.
func ValidateSQL(name string, raw []byte) error {
	if match := destructiveStatement.FindString(string(raw)); !destructiveCleanupMigrations[name] && match != "" {
		return fmt.Errorf("migration %q contains forbidden destructive statement %q", name, strings.TrimSpace(match))
	}
	for _, statement := range deleteStatement.FindAllString(string(raw), -1) {
		if !filterClause.MatchString(statement) {
			return fmt.Errorf("migration %q contains broad DELETE", name)
		}
	}
	return nil
}

func migrationVersion(name string) (int64, error) {
	if !strings.HasSuffix(name, ".sql") {
		return 0, fmt.Errorf("migration %q is not SQL", name)
	}
	base := strings.TrimSuffix(name, ".sql")
	prefix, _, ok := strings.Cut(base, "_")
	if !ok || prefix == "" {
		return 0, fmt.Errorf("migration %q has no numeric version", name)
	}
	for _, runeValue := range prefix {
		if runeValue < '0' || runeValue > '9' {
			return 0, fmt.Errorf("migration %q has non-numeric version", name)
		}
	}
	version, err := strconv.ParseInt(prefix, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("migration %q has invalid version: %w", name, err)
	}
	return version, nil
}
