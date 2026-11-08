package app

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	domainlibrary "canonkeeper/backend/internal/domain/library"
	legacylibrary "canonkeeper/backend/internal/library"
)

type coverBookReaderStub struct {
	book legacylibrary.Book
	err  error
}

func (stub coverBookReaderStub) GetBook(context.Context, legacylibrary.Actor, string) (legacylibrary.Book, error) {
	return stub.book, stub.err
}

func TestCoverBookResponseUsesCentralBookProjection(t *testing.T) {
	storedURL := "/api/v1/books/book-1/cover/4"
	uploadedURL := "/api/v1/books/book-1/cover/3"
	projection := coverBookResponse(coverBookReaderStub{book: legacylibrary.Book{ID: "book-1", CoverURL: &storedURL}})
	for name, test := range map[string]struct {
		coverURL *string
		want     string
	}{
		"upload": {coverURL: &uploadedURL, want: `"` + uploadedURL + `"`},
		"delete": {coverURL: nil, want: "null"},
	} {
		t.Run(name, func(t *testing.T) {
			value, err := projection(context.Background(), domainlibrary.Actor{UserID: "actor-1"}, "book-1", test.coverURL)
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			var response map[string]json.RawMessage
			if err := json.Unmarshal(encoded, &response); err != nil || string(response["coverUrl"]) != test.want {
				t.Fatalf("cover response = %s, %v", encoded, err)
			}
		})
	}
}

func TestCoverBookResponseMapsPostMutationDisappearanceToConflict(t *testing.T) {
	projection := coverBookResponse(coverBookReaderStub{err: legacylibrary.ErrNotFound})
	_, err := projection(context.Background(), domainlibrary.Actor{UserID: "actor-1"}, "book-1", nil)
	if !errors.Is(err, domainlibrary.ErrConflict) {
		t.Fatalf("projection error = %v, want ErrConflict", err)
	}
}

func TestDeletionTargetAndCoverRunnerRunsBoth(t *testing.T) {
	targets := &fakeDeletionTargetRunner{}
	coverErr := errors.New("cover reconciliation failed")
	coverCalls := 0
	runner := deletionTargetAndCoverRunner{
		targets: targets,
		covers: func(context.Context) (bool, error) {
			coverCalls++
			return false, coverErr
		},
	}

	if _, err := runner.RunOnceDiagnostic(context.Background()); !errors.Is(err, coverErr) {
		t.Fatalf("RunOnceDiagnostic() error = %v, want cover error", err)
	}
	if targets.callCount() != 1 || coverCalls != 1 {
		t.Fatalf("target/cover calls = %d/%d, want 1/1", targets.callCount(), coverCalls)
	}
}
