// SPDX-License-Identifier: AGPL-3.0-only

// Package tempfiles keeps the temporary files of a build in one named directory its start clears whole
// (specs/2026-11-13-desk-sagafile.md SF2.6): the saga file being saved or opened. A file is closed before it is removed,
// and the removal is tried again while the system holds the file (Windows).
package tempfiles

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Directory is the directory of the temporary files.
type Directory struct {
	path string
}

// Open makes the directory, private to the process.
func Open(path string) (*Directory, error) {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return nil, err
	}
	return &Directory{path: path}, nil
}

// Path is where the directory lies.
func (directory *Directory) Path() string { return directory.path }

// Clear removes everything the directory holds: what a process that fell left behind.
func (directory *Directory) Clear() error {
	entries, err := os.ReadDir(directory.path)
	if err != nil {
		return err
	}
	var failed []error
	for _, entry := range entries {
		if err := removeAll(filepath.Join(directory.path, entry.Name())); err != nil {
			failed = append(failed, err)
		}
	}
	return errors.Join(failed...)
}

// File is a temporary file.
type File struct {
	file *os.File
}

// Create makes a new file in the directory.
func (directory *Directory) Create() (*File, error) {
	file, err := os.CreateTemp(directory.path, "saga-*.part")
	if err != nil {
		return nil, err
	}
	return &File{file: file}, nil
}

func (file *File) Write(data []byte) (int, error) { return file.file.Write(data) }

func (file *File) ReadAt(data []byte, offset int64) (int, error) {
	return file.file.ReadAt(data, offset)
}

// Name is the path of the file.
func (file *File) Name() string { return file.file.Name() }

// Size is what is written.
func (file *File) Size() (int64, error) {
	info, err := file.file.Stat()
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}

// Remove closes the file and removes it.
func (file *File) Remove() error {
	closeErr := file.file.Close()
	if errors.Is(closeErr, os.ErrClosed) {
		closeErr = nil
	}
	return errors.Join(closeErr, removeAll(file.file.Name()))
}

// removeAll removes a path, again while the system holds it: on Windows a file another process reads cannot go yet.
func removeAll(path string) error {
	var err error
	for attempt := 0; attempt < 20; attempt++ {
		if err = os.RemoveAll(path); err == nil || errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return err
}

// markPattern names the files that list the files an open put among the files of the build before its transaction ended.
const markPattern = "covers-*.list"

// Mark lists files written outside the directory for a work not yet committed: while it stands, a start after a fall
// knows them (specs/2026-11-13-desk-sagafile.md SF3.9).
type Mark struct{ path string }

// Mark writes the list.
func (directory *Directory) Mark(keys []string) (*Mark, error) {
	file, err := os.CreateTemp(directory.path, markPattern)
	if err != nil {
		return nil, err
	}
	_, writeErr := file.WriteString(strings.Join(keys, "\n"))
	syncErr := file.Sync()
	if err := errors.Join(writeErr, syncErr, file.Close()); err != nil {
		return nil, errors.Join(err, removeAll(file.Name()))
	}
	return &Mark{path: file.Name()}, nil
}

// Remove drops the list: the work committed or took its files away.
func (mark *Mark) Remove() error { return removeAll(mark.path) }

// Marked reads the files the lists a fallen process left name.
func (directory *Directory) Marked() ([]string, error) {
	paths, err := filepath.Glob(filepath.Join(directory.path, markPattern))
	if err != nil {
		return nil, err
	}
	var keys []string
	for _, path := range paths {
		body, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		for _, key := range strings.Split(string(body), "\n") {
			if key != "" {
				keys = append(keys, key)
			}
		}
	}
	return keys, nil
}
