package library

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
)

var (
	ErrInvalidObjectKey = errors.New("book cover files: invalid object key")
	ErrObjectConflict   = errors.New("book cover files: object conflict")
)

type BookCoverFiles struct {
	root *os.Root
}

func NewBookCoverFiles(root *os.Root) (*BookCoverFiles, error) {
	if root == nil {
		return nil, ErrObjectConflict
	}
	if err := root.Mkdir("book-covers", 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, err
	}
	return &BookCoverFiles{root: root}, nil
}

func (files *BookCoverFiles) Create(ctx context.Context, key string, content []byte) (result error) {
	if err := files.validate(ctx, key); err != nil {
		return err
	}
	file, err := files.root.OpenFile(key, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := file.Close(); result == nil {
			result = closeErr
		}
		if result != nil {
			_ = files.root.Remove(key)
		}
	}()
	if _, err := io.Copy(file, bytes.NewReader(content)); err != nil {
		return err
	}
	return file.Sync()
}

func (files *BookCoverFiles) Publish(ctx context.Context, stagingKey, finalKey string) (result error) {
	if err := files.validate(ctx, stagingKey); err != nil {
		return err
	}
	if err := files.validate(ctx, finalKey); err != nil || stagingKey == finalKey {
		return ErrInvalidObjectKey
	}
	info, err := files.root.Lstat(stagingKey)
	if err != nil || !info.Mode().IsRegular() {
		return ErrObjectConflict
	}
	reserved, err := files.root.OpenFile(finalKey, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if closeErr := reserved.Close(); closeErr != nil {
		_ = files.root.Remove(finalKey)
		return closeErr
	}
	if err := files.root.Rename(stagingKey, finalKey); err != nil {
		_ = files.root.Remove(finalKey)
		return err
	}
	defer func() {
		if result == nil {
			return
		}
		if err := files.root.Remove(finalKey); err != nil && !errors.Is(err, os.ErrNotExist) {
			result = errors.Join(result, err)
		}
	}()
	directory, err := files.root.Open("book-covers")
	if err != nil {
		return err
	}
	syncErr := directory.Sync()
	closeErr := directory.Close()
	if syncErr != nil {
		return syncErr
	}
	return closeErr
}

func (files *BookCoverFiles) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	if err := files.validate(ctx, key); err != nil {
		return nil, err
	}
	pathInfo, err := files.root.Lstat(key)
	if err != nil || !pathInfo.Mode().IsRegular() {
		return nil, ErrObjectConflict
	}
	file, err := files.root.Open(key)
	if err != nil {
		return nil, err
	}
	openedInfo, statErr := file.Stat()
	currentInfo, pathErr := files.root.Lstat(key)
	if statErr != nil || pathErr != nil || !currentInfo.Mode().IsRegular() || !os.SameFile(openedInfo, currentInfo) {
		_ = file.Close()
		return nil, ErrObjectConflict
	}
	return file, nil
}

func (files *BookCoverFiles) Remove(ctx context.Context, key string) error {
	if err := files.validate(ctx, key); err != nil {
		return err
	}
	info, err := files.root.Lstat(key)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || !info.Mode().IsRegular() {
		return ErrObjectConflict
	}
	return files.root.Remove(key)
}

func (files *BookCoverFiles) validate(ctx context.Context, key string) error {
	if files == nil || files.root == nil {
		return ErrObjectConflict
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	const prefix = "book-covers/"
	name := strings.TrimPrefix(key, prefix)
	if name == key || name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\\\x00") {
		return ErrInvalidObjectKey
	}
	return nil
}
