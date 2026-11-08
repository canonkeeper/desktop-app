package deletion

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	deletiondomain "canonkeeper/backend/internal/domain/deletion"
	"canonkeeper/backend/internal/governance"
)

const (
	filesystemArtifactID  = "20000000-0000-4000-8000-000000000001"
	filesystemOperationID = "30000000-0000-4000-8000-000000000001"
)

func TestFilesystemTargetRemovesOnlyRegisteredObjects(t *testing.T) {
	rootPath := t.TempDir()
	writeFilesystemTargetFile(t, rootPath, "book-covers/old.final", []byte("old"))
	writeFilesystemTargetFile(t, rootPath, "book-covers/restored.final", []byte("restored"))
	writeFilesystemTargetFile(t, rootPath, "unrelated/final.jpg", []byte("unrelated"))
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	store := &filesystemTargetStoreForTest{objects: []FilesystemDeletionObject{{
		ArtifactID:        filesystemArtifactID,
		DeleteOperationID: filesystemOperationID,
		ObjectKey:         "book-covers/old.final",
		State:             FilesystemObjectDeleting,
	}}}
	target, err := NewFilesystemTarget(root, store)
	if err != nil {
		t.Fatal(err)
	}
	result, err := target.Apply(context.Background(), targetRequestForTest(
		t,
		time.Now().UTC().Truncate(time.Microsecond),
		governance.TargetFilesystem,
	))
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if result.Disposition != deletiondomain.DispositionPurged || result.HandlerVersion != governance.HandlerFilesystemOSRootV1 ||
		result.Reason != governance.ReasonPurged {
		t.Fatalf("Apply() result = %#v", result)
	}
	assertFilesystemTargetMissing(t, rootPath, "book-covers/old.final")
	assertFilesystemTargetContents(t, rootPath, "book-covers/restored.final", []byte("restored"))
	assertFilesystemTargetContents(t, rootPath, "unrelated/final.jpg", []byte("unrelated"))
	if len(store.committed) != 1 || !store.verified {
		t.Fatalf("committed/verified = %d/%v, want 1/true", len(store.committed), store.verified)
	}
}

func TestFilesystemTargetDoesNotPurgeProductDataForPermissionLoss(t *testing.T) {
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	store := &filesystemTargetStoreForTest{beginErr: errors.New("filesystem store must not be called")}
	target, err := NewFilesystemTarget(root, store)
	if err != nil {
		t.Fatal(err)
	}
	request := targetRequestForTest(t, time.Now().UTC().Truncate(time.Microsecond), governance.TargetFilesystem)
	request.Tombstone.Identity.PermissionLossActorUserID = "33333333-3333-4333-8333-333333333333"
	result, err := target.Apply(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if result.Disposition != deletiondomain.DispositionNoData || store.verified || len(store.committed) != 0 {
		t.Fatalf("permission-loss filesystem result/store = %#v/%v/%d", result, store.verified, len(store.committed))
	}
}

func TestFilesystemTargetResumesEveryDurableDeletingObjectShape(t *testing.T) {
	for _, test := range []struct {
		name     string
		makeFile bool
	}{
		{name: "present", makeFile: true},
		{name: "already removed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			rootPath := t.TempDir()
			if test.makeFile {
				writeFilesystemTargetFile(t, rootPath, "book-covers/intent.final", []byte("cover"))
			}
			root, err := os.OpenRoot(rootPath)
			if err != nil {
				t.Fatal(err)
			}
			defer root.Close()
			store := &filesystemTargetStoreForTest{objects: []FilesystemDeletionObject{{
				ArtifactID: filesystemArtifactID, DeleteOperationID: filesystemOperationID,
				ObjectKey: "book-covers/intent.final", State: FilesystemObjectDeleting,
			}}}
			target, err := NewFilesystemTarget(root, store)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := target.Apply(context.Background(), targetRequestForTest(
				t, time.Now().UTC().Truncate(time.Microsecond), governance.TargetFilesystem,
			)); err != nil {
				t.Fatalf("Apply() error = %v", err)
			}
			assertFilesystemTargetMissing(t, rootPath, "book-covers/intent.final")
			if len(store.committed) != 1 || !store.verified {
				t.Fatalf("committed/verified = %d/%v, want 1/true", len(store.committed), store.verified)
			}
		})
	}
}

func TestFilesystemTargetValidatesAlreadyDeletedProofAndAbsence(t *testing.T) {
	request := targetRequestForTest(t, time.Now().UTC().Truncate(time.Microsecond), governance.TargetFilesystem)
	receipt, err := FilesystemObjectReceiptDigest(request.Claim.ProjectID, filesystemArtifactID, filesystemOperationID)
	if err != nil {
		t.Fatal(err)
	}
	object := FilesystemDeletionObject{
		ArtifactID: filesystemArtifactID, DeleteOperationID: filesystemOperationID,
		ObjectKey: "book-covers/already-deleted.final", State: FilesystemObjectDeleted, DeletionReceiptDigest: receipt,
	}

	t.Run("absent replay", func(t *testing.T) {
		root, err := os.OpenRoot(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		defer root.Close()
		store := &filesystemTargetStoreForTest{objects: []FilesystemDeletionObject{object}}
		target, err := NewFilesystemTarget(root, store)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := target.Apply(context.Background(), request); err != nil {
			t.Fatalf("Apply() error = %v", err)
		}
		if len(store.committed) != 1 || store.committed[0].DeletionReceiptDigest != receipt || !store.verified {
			t.Fatalf("deleted replay committed/verified = %#v/%v, want metadata finalized", store.committed, store.verified)
		}
	})

	t.Run("receipt with live object", func(t *testing.T) {
		rootPath := t.TempDir()
		writeFilesystemTargetFile(t, rootPath, object.ObjectKey, []byte("unexpected"))
		root, err := os.OpenRoot(rootPath)
		if err != nil {
			t.Fatal(err)
		}
		defer root.Close()
		target, err := NewFilesystemTarget(root, &filesystemTargetStoreForTest{objects: []FilesystemDeletionObject{object}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := target.Apply(context.Background(), request); !errors.Is(err, ErrFilesystemUnprovenMissing) {
			t.Fatalf("Apply() error = %v, want ErrFilesystemUnprovenMissing", err)
		}
		assertFilesystemTargetContents(t, rootPath, object.ObjectKey, []byte("unexpected"))
	})
}

func TestFilesystemTargetRejectsUnsafeObjectKeys(t *testing.T) {
	for _, key := range []string{
		"", "/absolute", "../escape", "a/../escape", "a//b", "./a", ".", "..",
		`a\b`, "C:/volume", "//server/share", "nul\x00byte",
	} {
		if err := ValidateFilesystemObjectKey(key); err == nil {
			t.Errorf("ValidateFilesystemObjectKey(%q) = nil", key)
		}
	}
	for _, key := range []string{"book-covers/a.final", "book-covers/20000000-0000-4000-8000-000000000001.staging"} {
		if err := ValidateFilesystemObjectKey(key); err != nil {
			t.Errorf("ValidateFilesystemObjectKey(%q) error = %v", key, err)
		}
	}
}

func TestFilesystemTargetNeverFollowsLeafOrAncestorSymlinks(t *testing.T) {
	t.Run("leaf", func(t *testing.T) {
		rootPath := t.TempDir()
		outsidePath := t.TempDir()
		sentinelPath := filepath.Join(outsidePath, "sentinel")
		if err := os.WriteFile(sentinelPath, []byte("outside"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(rootPath, "links"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(sentinelPath, filepath.Join(rootPath, "links", "final.jpg")); err != nil {
			t.Fatal(err)
		}
		root, err := os.OpenRoot(rootPath)
		if err != nil {
			t.Fatal(err)
		}
		defer root.Close()
		store := &filesystemTargetStoreForTest{objects: []FilesystemDeletionObject{{
			ArtifactID: filesystemArtifactID, DeleteOperationID: filesystemOperationID,
			ObjectKey: "links/final.jpg", State: FilesystemObjectDeleting,
		}}}
		target, err := NewFilesystemTarget(root, store)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := target.Apply(context.Background(), targetRequestForTest(
			t, time.Now().UTC().Truncate(time.Microsecond), governance.TargetFilesystem,
		)); err != nil {
			t.Fatal(err)
		}
		assertFilesystemTargetContents(t, outsidePath, "sentinel", []byte("outside"))
	})

	t.Run("ancestor swap race", func(t *testing.T) {
		rootPath := t.TempDir()
		outsidePath := t.TempDir()
		sentinelPath := filepath.Join(outsidePath, "victim")
		want := bytes.Repeat([]byte("outside-sentinel"), 64)
		if err := os.WriteFile(sentinelPath, want, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(filepath.Join(rootPath, "slot"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(rootPath, "slot", "victim"), []byte("inside"), 0o600); err != nil {
			t.Fatal(err)
		}
		root, err := os.OpenRoot(rootPath)
		if err != nil {
			t.Fatal(err)
		}
		defer root.Close()

		stop := make(chan struct{})
		var attacker sync.WaitGroup
		attacker.Add(1)
		go func() {
			defer attacker.Done()
			slot := filepath.Join(rootPath, "slot")
			parked := filepath.Join(rootPath, "slot-parked")
			for {
				select {
				case <-stop:
					return
				default:
				}
				if os.Rename(slot, parked) != nil {
					continue
				}
				if os.Symlink(outsidePath, slot) == nil {
					_ = os.Remove(slot)
				}
				_ = os.Rename(parked, slot)
			}
		}()
		request := targetRequestForTest(t, time.Now().UTC().Truncate(time.Microsecond), governance.TargetFilesystem)
		for index := 0; index < 500; index++ {
			store := &filesystemTargetStoreForTest{objects: []FilesystemDeletionObject{{
				ArtifactID: filesystemArtifactID, DeleteOperationID: filesystemOperationID,
				ObjectKey: "slot/victim", State: FilesystemObjectDeleting,
			}}}
			target, err := NewFilesystemTarget(root, store)
			if err != nil {
				close(stop)
				attacker.Wait()
				t.Fatal(err)
			}
			_, _ = target.Apply(context.Background(), request)
		}
		close(stop)
		attacker.Wait()
		got, err := os.ReadFile(sentinelPath)
		if err != nil {
			t.Fatalf("outside sentinel missing: %v", err)
		}
		if !bytes.Equal(got, want) {
			t.Fatal("outside sentinel changed during ancestor swap race")
		}
	})
}

type filesystemTargetStoreForTest struct {
	objects   []FilesystemDeletionObject
	beginErr  error
	commitErr error
	verifyErr error
	committed []FilesystemDeletionObject
	verified  bool
}

func (store *filesystemTargetStoreForTest) BeginFilesystemDeletion(
	context.Context,
	deletiondomain.TargetRequest,
) ([]FilesystemDeletionObject, error) {
	return append([]FilesystemDeletionObject(nil), store.objects...), store.beginErr
}

func (store *filesystemTargetStoreForTest) CommitFilesystemObjectDeletion(
	_ context.Context,
	_ deletiondomain.TargetRequest,
	object FilesystemDeletionObject,
	receipt governance.Digest,
) error {
	if store.commitErr != nil {
		return store.commitErr
	}
	object.State = FilesystemObjectDeleted
	object.DeletionReceiptDigest = receipt
	store.committed = append(store.committed, object)
	return nil
}

func (store *filesystemTargetStoreForTest) VerifyFilesystemDeletionComplete(context.Context, deletiondomain.TargetRequest) error {
	store.verified = true
	return store.verifyErr
}

func writeFilesystemTargetFile(t *testing.T, root, key string, content []byte) {
	t.Helper()
	fullPath := filepath.Join(root, filepath.FromSlash(key))
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fullPath, content, 0o600); err != nil {
		t.Fatal(err)
	}
}

func assertFilesystemTargetMissing(t *testing.T, root, key string) {
	t.Helper()
	if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(key))); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("%q still exists or cannot be checked: %v", key, err)
	}
}

func assertFilesystemTargetContents(t *testing.T, root, key string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(key)))
	if err != nil {
		t.Fatalf("read %q: %v", key, err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("contents %q = %q, want %q", key, got, want)
	}
}

// specs/2026-10-29-saga-illustrations.md R4.1: the hard purge of a project removes its gallery
// directory; the gallery of another project and a derived-generation purge keep theirs.
func TestFilesystemTargetRemovesTheProjectGalleryOnHardPurge(t *testing.T) {
	const otherProjectID = "22222222-2222-4222-8222-222222222222"
	gallery := func(projectID string) string { return "illustrations/" + projectID }
	for _, test := range []struct {
		name    string
		mode    governance.PurgeMode
		removed bool
	}{
		{"hard purge", governance.PurgeModeSourceHardPurge, true},
		{"derived generation", governance.PurgeModeDerivedGeneration, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			rootPath := t.TempDir()
			writeFilesystemTargetFile(t, rootPath, gallery(coordinatorProjectID)+"/image.png", []byte("png"))
			writeFilesystemTargetFile(t, rootPath, gallery(coordinatorProjectID)+"/image.thumb.jpg", []byte("jpg"))
			writeFilesystemTargetFile(t, rootPath, gallery(coordinatorProjectID)+"/image.screen.jpg", []byte("jpg"))
			writeFilesystemTargetFile(t, rootPath, gallery(coordinatorProjectID)+"/item.screen.png", []byte("png"))
			writeFilesystemTargetFile(t, rootPath, gallery(otherProjectID)+"/image.png", []byte("other"))
			root, err := os.OpenRoot(rootPath)
			if err != nil {
				t.Fatal(err)
			}
			defer root.Close()
			store := &filesystemTargetStoreForTest{}
			target, err := NewFilesystemTarget(root, store, gallery)
			if err != nil {
				t.Fatal(err)
			}
			request := targetRequestForTest(t, time.Now().UTC().Truncate(time.Microsecond), governance.TargetFilesystem)
			request.Tombstone.Identity.Mode = test.mode
			if _, err := target.Apply(t.Context(), request); err != nil {
				t.Fatalf("Apply() error = %v", err)
			}
			if test.removed {
				assertFilesystemTargetMissing(t, rootPath, gallery(coordinatorProjectID))
			} else {
				assertFilesystemTargetContents(t, rootPath, gallery(coordinatorProjectID)+"/image.png", []byte("png"))
			}
			assertFilesystemTargetContents(t, rootPath, gallery(otherProjectID)+"/image.png", []byte("other"))
		})
	}
}
