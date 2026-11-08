package library

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"testing"
	"time"

	domainlibrary "canonkeeper/backend/internal/domain/library"
)

func TestUploadCoverSniffsFormatsAndKeepsPortraitsWithoutAspectGate(t *testing.T) {
	for name, content := range map[string][]byte{
		"jpeg": encodedJPEG(t, 600, 1600),
		"png":  encodedPNG(t, 600, 900),
		"webp": webpExtendedHeader(600, 900),
	} {
		t.Run(name, func(t *testing.T) {
			metadata := &coverMetadataStoreForTest{activationVersion: 1}
			objects := newCoverObjectsForTest()
			upload := NewUploadCover(metadata, objects, fixedCoverIdentity)

			activation, err := upload.Execute(context.Background(), UploadCoverInput{
				Actor: domainlibrary.Actor{UserID: "actor"}, BookID: "book", Content: content,
			})
			if err != nil {
				t.Fatal(err)
			}
			if activation.Current.Width != 600 || activation.Current.Height < 900 || metadata.activated.MIMEType != "image/"+name {
				t.Fatalf("activation = %#v", activation.Current)
			}
			if got := string(objects.values[fixedFinalKey]); !bytes.Equal([]byte(got), content) {
				t.Fatalf("published bytes differ: %d != %d", len(got), len(content))
			}
		})
	}
}

func TestUploadCoverRejectsSizeDimensionsAndUnsupportedMedia(t *testing.T) {
	tests := map[string]struct {
		content []byte
		want    error
	}{
		"too large":   {content: make([]byte, domainlibrary.MaxCoverBytes+1), want: domainlibrary.ErrCoverTooLarge},
		"too narrow":  {content: encodedJPEG(t, 599, 900), want: domainlibrary.ErrCoverTooSmall},
		"too short":   {content: encodedPNG(t, 600, 899), want: domainlibrary.ErrCoverTooSmall},
		"unsupported": {content: []byte("GIF89a"), want: domainlibrary.ErrUnsupportedCoverMedia},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			upload := NewUploadCover(&coverMetadataStoreForTest{}, newCoverObjectsForTest(), fixedCoverIdentity)
			_, err := upload.Execute(context.Background(), UploadCoverInput{
				Actor: domainlibrary.Actor{UserID: "actor"}, BookID: "book", Content: test.content,
			})
			if !errors.Is(err, test.want) {
				t.Fatalf("Execute() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestUploadCoverCleansFailedActivationAndSupersededObject(t *testing.T) {
	t.Run("activation failure", func(t *testing.T) {
		metadata := &coverMetadataStoreForTest{activateErr: domainlibrary.ErrConflict}
		objects := newCoverObjectsForTest()
		upload := NewUploadCover(metadata, objects, fixedCoverIdentity)
		_, err := upload.Execute(context.Background(), UploadCoverInput{
			Actor: domainlibrary.Actor{UserID: "actor"}, BookID: "book", Content: encodedJPEG(t, 600, 900),
		})
		if !errors.Is(err, domainlibrary.ErrConflict) {
			t.Fatalf("Execute() error = %v", err)
		}
		if _, exists := objects.values[fixedFinalKey]; exists {
			t.Fatal("failed activation retained final object")
		}
	})

	t.Run("publish failure after final rename", func(t *testing.T) {
		metadata := &coverMetadataStoreForTest{}
		objects := newCoverObjectsForTest()
		ctx, cancel := context.WithCancel(context.Background())
		objects.cancelOnPublish = cancel
		objects.publishErr = errors.New("directory sync failed")
		upload := NewUploadCover(metadata, objects, fixedCoverIdentity)
		if _, err := upload.Execute(ctx, UploadCoverInput{
			Actor: domainlibrary.Actor{UserID: "actor"}, BookID: "book", Content: encodedJPEG(t, 600, 900),
		}); err == nil {
			t.Fatal("Execute() error = nil")
		}
		if _, exists := objects.values[fixedFinalKey]; exists {
			t.Fatal("failed publication retained final object")
		}
	})

	t.Run("replacement", func(t *testing.T) {
		metadata := &coverMetadataStoreForTest{
			activationVersion: 2,
			superseded: &domainlibrary.CoverArtifact{
				ID: "old", StorageKey: "book-covers/old.final", State: domainlibrary.CoverSuperseded,
			},
		}
		objects := newCoverObjectsForTest()
		objects.values["book-covers/old.final"] = []byte("old")
		upload := NewUploadCover(metadata, objects, fixedCoverIdentity)
		if _, err := upload.Execute(context.Background(), UploadCoverInput{
			Actor: domainlibrary.Actor{UserID: "actor"}, BookID: "book", Content: encodedJPEG(t, 600, 900),
		}); err != nil {
			t.Fatal(err)
		}
		if _, exists := objects.values["book-covers/old.final"]; !exists {
			t.Fatal("replacement deleted bytes before durable reconciliation")
		}
		metadata.pending = *metadata.superseded
		reconciled, err := NewCoverDeletionReconciler(metadata, objects).ReconcileOne(context.Background())
		if err != nil || !reconciled || !metadata.completed {
			t.Fatalf("reconcile = %v/%v, completed=%v", reconciled, err, metadata.completed)
		}
		if _, exists := objects.values["book-covers/old.final"]; exists {
			t.Fatal("reconciler retained superseded object")
		}
	})
}

func TestGetCoverVerifiesRegisteredBytesAndDeleteQueuesObjectRemoval(t *testing.T) {
	content := encodedJPEG(t, 600, 900)
	digest := sha256.Sum256(content)
	artifact := domainlibrary.CoverArtifact{
		ID: "cover", BookID: "book", Version: 1, StorageKey: "book-covers/current.final",
		MIMEType: "image/jpeg", ByteSize: int64(len(content)), ContentSHA256: digest, State: domainlibrary.CoverActive,
	}
	metadata := &coverMetadataStoreForTest{found: artifact, removed: artifact}
	objects := newCoverObjectsForTest()
	objects.values[artifact.StorageKey] = content

	result, err := NewGetCover(metadata, objects).Execute(context.Background(), domainlibrary.CoverLookup{
		Actor: domainlibrary.Actor{UserID: "actor"}, BookID: "book", Version: 1,
	})
	if err != nil || !bytes.Equal(result.Content, content) || result.ETag != `"`+bytesToHex(digest[:])+`"` {
		t.Fatalf("GetCover = %#v, %v", result, err)
	}
	objects.values[artifact.StorageKey] = []byte("corrupt")
	if _, err := NewGetCover(metadata, objects).Execute(context.Background(), domainlibrary.CoverLookup{
		Actor: domainlibrary.Actor{UserID: "actor"}, BookID: "book", Version: 1,
	}); !errors.Is(err, domainlibrary.ErrUnavailable) {
		t.Fatalf("corrupt read error = %v, want ErrUnavailable", err)
	}
	objects.values[artifact.StorageKey] = content
	if _, err := NewDeleteCover(metadata).Execute(context.Background(), domainlibrary.RemoveCoverInput{
		Actor: domainlibrary.Actor{UserID: "actor"}, BookID: "book", At: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	if _, exists := objects.values[artifact.StorageKey]; !exists {
		t.Fatal("delete removed bytes before durable reconciliation")
	}
	metadata.pending = artifact
	metadata.pending.State = domainlibrary.CoverRemoved
	if reconciled, err := NewCoverDeletionReconciler(metadata, objects).ReconcileOne(context.Background()); err != nil || !reconciled {
		t.Fatalf("reconcile = %v/%v", reconciled, err)
	}
	if _, exists := objects.values[artifact.StorageKey]; exists {
		t.Fatal("reconciler retained removed object")
	}
}

const (
	fixedStagingKey = "book-covers/fixed.staging"
	fixedFinalKey   = "book-covers/fixed.final"
)

func fixedCoverIdentity() (CoverIdentity, error) {
	return CoverIdentity{
		ArtifactID: "00000000-0000-4000-8000-000000000001",
		StagingKey: fixedStagingKey,
		FinalKey:   fixedFinalKey,
	}, nil
}

type coverMetadataStoreForTest struct {
	activated         domainlibrary.ActivateCoverInput
	activationVersion int64
	superseded        *domainlibrary.CoverArtifact
	activateErr       error
	found             domainlibrary.CoverArtifact
	removed           domainlibrary.CoverArtifact
	pending           domainlibrary.CoverArtifact
	completed         bool
}

func (store *coverMetadataStoreForTest) ActivateCover(_ context.Context, input domainlibrary.ActivateCoverInput) (domainlibrary.CoverActivation, error) {
	store.activated = input
	if store.activateErr != nil {
		return domainlibrary.CoverActivation{}, store.activateErr
	}
	current := domainlibrary.CoverArtifact{
		ID: input.ArtifactID, BookID: input.BookID, Version: store.activationVersion,
		StorageKey: input.StorageKey, MIMEType: input.MIMEType, ByteSize: input.ByteSize,
		Width: input.Width, Height: input.Height, ContentSHA256: input.ContentSHA256, State: domainlibrary.CoverActive,
	}
	return domainlibrary.CoverActivation{Current: current, Superseded: store.superseded}, nil
}

func (store *coverMetadataStoreForTest) FindCover(context.Context, domainlibrary.CoverLookup) (domainlibrary.CoverArtifact, error) {
	return store.found, nil
}

func (store *coverMetadataStoreForTest) RemoveCover(context.Context, domainlibrary.RemoveCoverInput) (domainlibrary.CoverArtifact, error) {
	return store.removed, nil
}

func (store *coverMetadataStoreForTest) NextCoverDeletion(context.Context) (domainlibrary.CoverArtifact, error) {
	if store.pending.ID == "" {
		return domainlibrary.CoverArtifact{}, domainlibrary.ErrNotFound
	}
	return store.pending, nil
}

func (store *coverMetadataStoreForTest) CompleteCoverDeletion(context.Context, domainlibrary.CoverArtifact, time.Time) error {
	store.completed = true
	store.pending = domainlibrary.CoverArtifact{}
	return nil
}

type coverObjectsForTest struct {
	values          map[string][]byte
	publishErr      error
	cancelOnPublish context.CancelFunc
}

func newCoverObjectsForTest() *coverObjectsForTest {
	return &coverObjectsForTest{values: make(map[string][]byte)}
}

func (objects *coverObjectsForTest) Create(_ context.Context, key string, content []byte) error {
	objects.values[key] = bytes.Clone(content)
	return nil
}

func (objects *coverObjectsForTest) Publish(_ context.Context, stagingKey, finalKey string) error {
	objects.values[finalKey] = objects.values[stagingKey]
	delete(objects.values, stagingKey)
	if objects.cancelOnPublish != nil {
		objects.cancelOnPublish()
	}
	return objects.publishErr
}

func (objects *coverObjectsForTest) Open(_ context.Context, key string) (io.ReadCloser, error) {
	content, exists := objects.values[key]
	if !exists {
		return nil, errors.New("missing")
	}
	return io.NopCloser(bytes.NewReader(content)), nil
}

func (objects *coverObjectsForTest) Remove(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	delete(objects.values, key)
	return nil
}

func encodedJPEG(t *testing.T, width, height int) []byte {
	t.Helper()
	var output bytes.Buffer
	if err := jpeg.Encode(&output, image.NewRGBA(image.Rect(0, 0, width, height)), nil); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func encodedPNG(t *testing.T, width, height int) []byte {
	t.Helper()
	var output bytes.Buffer
	if err := png.Encode(&output, image.NewRGBA(image.Rect(0, 0, width, height))); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func webpExtendedHeader(width, height int) []byte {
	data := []byte{'R', 'I', 'F', 'F', 22, 0, 0, 0, 'W', 'E', 'B', 'P', 'V', 'P', '8', 'X', 10, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	for index, value := range []int{width - 1, height - 1} {
		offset := 24 + index*3
		data[offset], data[offset+1], data[offset+2] = byte(value), byte(value>>8), byte(value>>16)
	}
	return data
}

func bytesToHex(value []byte) string {
	const digits = "0123456789abcdef"
	encoded := make([]byte, len(value)*2)
	for index, current := range value {
		encoded[index*2] = digits[current>>4]
		encoded[index*2+1] = digits[current&0x0f]
	}
	return string(encoded)
}
