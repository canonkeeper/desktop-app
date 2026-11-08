package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
)

func waitForShutdown(
	ctx context.Context,
	shutdownHTTP func(context.Context) error,
	waitBackground func(context.Context) error,
) error {
	httpDone := make(chan error, 1)
	go func() { httpDone <- shutdownHTTP(ctx) }()
	return errors.Join(<-httpDone, waitBackground(ctx))
}

func waitForBackground(ctx context.Context, workers []<-chan struct{}) error {
	for _, done := range workers {
		select {
		case <-done:
		case <-ctx.Done():
			return errors.New("api: background shutdown timed out")
		}
	}
	return nil
}

// openFileRoot opens the private directory of product files (book covers); every file
// operation stays inside it.
func openFileRoot(storageDir string) (*os.Root, error) {
	fileDir, err := filepath.Abs(storageDir)
	if err != nil {
		return nil, errors.New("api: invalid file storage root")
	}
	fileDir = filepath.Clean(fileDir)
	if err := os.MkdirAll(fileDir, 0o700); err != nil {
		return nil, errors.New("api: failed to prepare file storage root")
	}
	fileRoot, err := os.OpenRoot(fileDir)
	if err != nil {
		return nil, errors.New("api: failed to open file storage root")
	}
	return fileRoot, nil
}
