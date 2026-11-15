// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"cmp"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// The versions the start compares before it touches the data (specs/2026-11-13-desk-upgrade.md UG2): the version of the
// app — the shell and the backend are one version — the version that last opened the data, the version of the program
// that made a copy, and the major version of PostgreSQL the copy and the cluster were made by.

// versionLength bounds a version read from a file: a version of the app, not a text.
const versionLength = 64

// plainVersion is version when it is one: printable ASCII without spaces, not too long; otherwise none.
func plainVersion(version string) string {
	if version == "" || len(version) > versionLength || strings.ContainsFunc(version, func(r rune) bool { return r < '!' || r > '~' }) {
		return ""
	}
	return version
}

// versionNumbers are the numbers of a version like 1.3.0 (a leading v and a pre-release after - are left out); ok is false
// for anything else.
func versionNumbers(version string) ([]int, bool) {
	core, _, _ := strings.Cut(strings.TrimPrefix(version, "v"), "-")
	core, _, _ = strings.Cut(core, "+")
	parts := strings.Split(core, ".")
	numbers := make([]int, 0, len(parts))
	for _, part := range parts {
		number, err := strconv.Atoi(part)
		if err != nil || number < 0 {
			return nil, false
		}
		numbers = append(numbers, number)
	}
	return numbers, len(numbers) > 0
}

// newerVersion says whether a is a later version than b; versions that cannot be compared are not.
func newerVersion(a, b string) bool {
	left, ok := versionNumbers(a)
	right, known := versionNumbers(b)
	return ok && known && slices.Compare(left, right) > 0
}

// latestVersion is the later of two versions; a version that cannot be compared gives way to one that can.
func latestVersion(a, b string) string {
	if a == "" || newerVersion(b, a) {
		return b
	}
	if _, ok := versionNumbers(a); !ok {
		if _, known := versionNumbers(b); known {
			return b
		}
	}
	return a
}

// BackupCopy names a copy before the migrations to the shell: its directory in backups/, when it was made — the local
// time of the computer with its offset, which the words of the shell say — and the version of the app that made it.
type BackupCopy struct {
	Name    string `json:"name"`
	MadeAt  string `json:"madeAt"`
	Version string `json:"version,omitempty"`
}

// backupPostgres holds the major version of PostgreSQL that made the database of a copy (UG2.3).
const backupPostgres = "postgres"

// copyOf describes a complete copy of backups/ by its name; ok is false for anything else, a link among them.
func copyOf(backups, name string) (BackupCopy, int64, bool) {
	schema, made, ok := backupOf(name)
	if !ok {
		return BackupCopy{}, 0, false
	}
	path := filepath.Join(backups, name)
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || !backupFinished(path) {
		return BackupCopy{}, 0, false
	}
	at, err := time.Parse(backupTime, made)
	if err != nil {
		return BackupCopy{}, 0, false
	}
	return BackupCopy{Name: name, MadeAt: at.In(time.Local).Format(time.RFC3339), Version: versionOf(path)}, schema, true
}

// completeCopies are the complete copies of backups/ from the newest, by the time in their names.
func completeCopies(backups string) []BackupCopy {
	entries, err := os.ReadDir(backups)
	if err != nil {
		return nil
	}
	type found struct {
		copy   BackupCopy
		made   string
		schema int64
	}
	var copies []found
	for _, entry := range entries {
		if described, schema, ok := copyOf(backups, entry.Name()); ok {
			_, made, _ := backupOf(entry.Name())
			copies = append(copies, found{described, made, schema})
		}
	}
	slices.SortFunc(copies, func(a, b found) int {
		return cmp.Or(strings.Compare(b.made, a.made), cmp.Compare(b.schema, a.schema))
	})
	described := make([]BackupCopy, len(copies))
	for index, each := range copies {
		described[index] = each.copy
	}
	return described
}

// openedBy is the version that opened the data last as far as the start knows (UG2.3): the later of the one settings.json
// keeps and the one the newest copy holds; empty when neither is known.
func openedBy(remembered, backups string) string {
	opened := plainVersion(remembered)
	if copies := completeCopies(backups); len(copies) > 0 {
		opened = latestVersion(opened, copies[0].Version)
	}
	return opened
}

// restoreCandidate is the copy a return brings the data back from (UG2.3, UG4.1): the newest complete copy whose schema
// the program knows — no later than known — and whose database the PostgreSQL of the bundle made, major. None without
// one.
func restoreCandidate(backups string, known int64, major string) *BackupCopy {
	for _, candidate := range completeCopies(backups) {
		schema, _, _ := backupOf(candidate.Name)
		if schema <= known && major != "" && postgresOf(filepath.Join(backups, candidate.Name)) == major {
			return &candidate
		}
	}
	return nil
}

// postgresOf reads the major version of PostgreSQL a copy names; a copy without one names none.
func postgresOf(copy string) string {
	raw, err := os.ReadFile(filepath.Join(copy, backupPostgres))
	if err != nil || len(raw) > 16 {
		return ""
	}
	return majorOf(strings.TrimSpace(string(raw)))
}

// majorOf is the major version of PostgreSQL in text like 18 or 18.6; empty for anything else.
func majorOf(text string) string {
	major, _, _ := strings.Cut(text, ".")
	if number, err := strconv.Atoi(major); err != nil || number <= 0 {
		return ""
	}
	return major
}

// clusterMajor is the major version of PostgreSQL that made the cluster in directory (PG_VERSION); empty when there is no
// cluster.
func clusterMajor(directory string) string {
	raw, err := os.ReadFile(filepath.Join(directory, "PG_VERSION"))
	if err != nil || len(raw) > 16 {
		return ""
	}
	return majorOf(strings.TrimSpace(string(raw)))
}

// newerMajor says whether major a is later than major b.
func newerMajor(a, b string) bool {
	left, errLeft := strconv.Atoi(a)
	right, errRight := strconv.Atoi(b)
	return errLeft == nil && errRight == nil && left > right
}
