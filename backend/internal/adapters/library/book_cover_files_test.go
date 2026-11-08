package library

import (
	"context"
	"errors"
	"io"
	"os"
	"testing"
)

func TestBookCoverFilesPublishesInsideDedicatedNamespace(t *testing.T) {
	rootPath := t.TempDir()
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })
	files, err := NewBookCoverFiles(root)
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	stagingKey := "book-covers/cover.staging"
	finalKey := "book-covers/cover.final"
	if err := files.Create(ctx, stagingKey, []byte("private-cover")); err != nil {
		t.Fatal(err)
	}
	if err := files.Publish(ctx, stagingKey, finalKey); err != nil {
		t.Fatal(err)
	}
	opened, err := files.Open(ctx, finalKey)
	if err != nil {
		t.Fatal(err)
	}
	content, readErr := io.ReadAll(opened)
	closeErr := opened.Close()
	if readErr != nil || closeErr != nil || string(content) != "private-cover" {
		t.Fatalf("read/close/content = %v/%v/%q", readErr, closeErr, content)
	}
	if err := files.Remove(ctx, finalKey); err != nil {
		t.Fatal(err)
	}
	if _, err := root.Lstat(finalKey); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("removed object stat error = %v", err)
	}
}

func TestBookCoverFilesRejectsKeysOutsideFlatNamespace(t *testing.T) {
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })
	files, err := NewBookCoverFiles(root)
	if err != nil {
		t.Fatal(err)
	}

	for _, key := range []string{"cover.final", "book-covers/nested/cover.final", "book-covers/../cover.final", "../cover.final"} {
		if err := files.Create(context.Background(), key, []byte("cover")); !errors.Is(err, ErrInvalidObjectKey) {
			t.Fatalf("Create(%q) error = %v, want ErrInvalidObjectKey", key, err)
		}
	}
}
