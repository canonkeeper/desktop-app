// SPDX-License-Identifier: AGPL-3.0-only

// Command probe is the S0 probe of the CanonKeeper desktop app: it measures the parts the app
// will carry (embedded PostgreSQL 18, codex-app-server behind a local provider gateway) on the
// system it runs on and writes the facts as JSON. It holds no CanonKeeper product code.
//
//	probe pg     -bin <pg set>/bin -out pg.json [-root <dir>]
//	probe codex  -codex <codex-app-server> -home <profile dir> -out codex.json [-root <dir>]
//	probe stub   -addr 127.0.0.1:0 -out stub.json   (a Chat Completions stub on its own)
//	probe trace  -in strace.txt -out net.json        (outbound connections in an strace log)
//	probe serve  -dir <dir> -addr-file <file>         (static files for the updater test)
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"time"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: probe pg|codex|stub|trace ...")
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "pg":
		err = runPG(os.Args[2:])
	case "codex":
		err = runCodex(os.Args[2:])
	case "stub":
		err = runStubAlone(os.Args[2:])
	case "trace":
		err = runTrace(os.Args[2:])
	case "serve":
		err = runServe(os.Args[2:])
	default:
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "probe:", err)
		os.Exit(1)
	}
}

// productDataRoot is the data directory of the spec (DA1) with a Cyrillic segment in front of
// the app folder, so every path the parts see carries non-ASCII characters.
func productDataRoot() (string, error) {
	switch runtime.GOOS {
	case "windows":
		base := os.Getenv("LOCALAPPDATA")
		if base == "" {
			var err error
			if base, err = localAppData(); err != nil {
				return "", err
			}
		}
		return filepath.Join(base, "Тест", "CanonKeeper"), nil
	case "darwin":
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, "Library", "Application Support", "Тест", "CanonKeeper"), nil
	default:
		base := os.Getenv("XDG_DATA_HOME")
		if base == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", err
			}
			base = filepath.Join(home, ".local", "share")
		}
		return filepath.Join(base, "Тест", "canonkeeper"), nil
	}
}

func writeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	if path == "" || path == "-" {
		_, err = os.Stdout.Write(append(data, '\n'))
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

func seconds(d time.Duration) float64 {
	return float64(d.Round(time.Millisecond)) / float64(time.Second)
}

func systemInfo() map[string]any {
	info := map[string]any{"os": runtime.GOOS, "arch": runtime.GOARCH, "cpus": runtime.NumCPU()}
	if acp := activeCodePage(); acp != 0 {
		info["ansi_code_page"] = acp
	}
	// The account of the process, not an inherited variable.
	if account, err := user.Current(); err == nil {
		info["user"] = account.Username
	}
	return info
}
