// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

// logDays is how long the logs of the processes stay (DA1, «Data»): they name no text of books and no key.
const logDays = 7

// openLog opens the log of the backend for today and removes the logs of the backend and of PostgreSQL older than a week.
func openLog(directory string, now time.Time) (*os.File, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, err
	}
	cutoff := now.AddDate(0, 0, -logDays)
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".log") || !(strings.HasPrefix(name, "backend-") || strings.HasPrefix(name, "postgres-")) {
			continue
		}
		if info, err := entry.Info(); err == nil && info.ModTime().Before(cutoff) {
			_ = os.Remove(filepath.Join(directory, name))
		}
	}
	return os.OpenFile(filepath.Join(directory, "backend-"+now.UTC().Format("2006-01-02")+".log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
}
