package deletion

import (
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"path"
	"sort"
	"strings"
	"time"

	deletiondomain "canonkeeper/backend/internal/domain/deletion"
	"canonkeeper/backend/internal/governance"
)

var (
	ErrFilesystemTargetConflict  = errors.New("deletion: filesystem target conflict")
	ErrFilesystemUnprovenMissing = errors.New("deletion: filesystem object absence is unproven")
)

type FilesystemObjectState string

const (
	FilesystemObjectDeleting FilesystemObjectState = "deleting"
	FilesystemObjectDeleted  FilesystemObjectState = "deleted"
)

// FilesystemDeletionObject is one book cover file of a source hard purge; the tombstone is its
// delete operation.
type FilesystemDeletionObject struct {
	ArtifactID            string
	DeleteOperationID     string
	ObjectKey             string
	State                 FilesystemObjectState
	DeletionReceiptDigest governance.Digest
}

func (object FilesystemDeletionObject) validate(projectID string) error {
	if !deletiondomain.ValidUUID(projectID) || !deletiondomain.ValidUUID(object.ArtifactID) ||
		!deletiondomain.ValidUUID(object.DeleteOperationID) ||
		ValidateFilesystemObjectKey(object.ObjectKey) != nil {
		return ErrFilesystemTargetConflict
	}
	expected, err := FilesystemObjectReceiptDigest(projectID, object.ArtifactID, object.DeleteOperationID)
	if err != nil {
		return err
	}
	switch object.State {
	case FilesystemObjectDeleting:
		if object.DeletionReceiptDigest != (governance.Digest{}) {
			return ErrFilesystemTargetConflict
		}
	case FilesystemObjectDeleted:
		if object.DeletionReceiptDigest != expected {
			return ErrFilesystemTargetConflict
		}
	default:
		return ErrFilesystemTargetConflict
	}
	return nil
}

type FilesystemTargetStore interface {
	BeginFilesystemDeletion(context.Context, deletiondomain.TargetRequest) ([]FilesystemDeletionObject, error)
	CommitFilesystemObjectDeletion(context.Context, deletiondomain.TargetRequest, FilesystemDeletionObject, governance.Digest) error
	VerifyFilesystemDeletionComplete(context.Context, deletiondomain.TargetRequest) error
}

type FilesystemTarget struct {
	root  *os.Root
	store FilesystemTargetStore
	// projectDirectories name the directories of files a project keeps without rows of their own
	// (the saga gallery); the hard purge of the project removes them whole.
	projectDirectories []func(projectID string) string
}

func NewFilesystemTarget(root *os.Root, store FilesystemTargetStore, projectDirectories ...func(projectID string) string) (*FilesystemTarget, error) {
	if root == nil || store == nil {
		return nil, deletiondomain.ErrInvalidTargetRequest
	}
	for _, directory := range projectDirectories {
		if directory == nil {
			return nil, deletiondomain.ErrInvalidTargetRequest
		}
	}
	return &FilesystemTarget{root: root, store: store, projectDirectories: projectDirectories}, nil
}

func (target *FilesystemTarget) Apply(ctx context.Context, request deletiondomain.TargetRequest) (deletiondomain.TargetResult, error) {
	if target == nil || target.root == nil || target.store == nil ||
		request.Claim.Target != governance.TargetFilesystem ||
		request.Validate(time.Now().UTC()) != nil {
		return deletiondomain.TargetResult{}, deletiondomain.ErrInvalidTargetRequest
	}
	if request.Tombstone.Identity.PermissionLossActorUserID != "" {
		return deletiondomain.TargetResult{
			Disposition: deletiondomain.DispositionNoData, HandlerVersion: governance.HandlerFilesystemOSRootV1,
			Reason: governance.ReasonNoData,
		}, nil
	}
	objects, err := target.store.BeginFilesystemDeletion(ctx, request)
	if err != nil {
		return deletiondomain.TargetResult{}, err
	}
	sort.Slice(objects, func(left, right int) bool {
		return objects[left].ArtifactID < objects[right].ArtifactID
	})
	for index, object := range objects {
		if err := ctx.Err(); err != nil {
			return deletiondomain.TargetResult{}, err
		}
		if err := object.validate(request.Claim.ProjectID); err != nil {
			return deletiondomain.TargetResult{}, err
		}
		if index > 0 && objects[index-1].ArtifactID == object.ArtifactID {
			return deletiondomain.TargetResult{}, ErrFilesystemTargetConflict
		}
		if object.State == FilesystemObjectDeleted {
			if err := target.requireObjectAbsent(object.ObjectKey); err != nil {
				return deletiondomain.TargetResult{}, err
			}
			if err := target.store.CommitFilesystemObjectDeletion(
				ctx, request, object, object.DeletionReceiptDigest,
			); err != nil {
				return deletiondomain.TargetResult{}, err
			}
			continue
		}
		if err := target.removeObject(object.ObjectKey); err != nil {
			return deletiondomain.TargetResult{}, err
		}
		receipt, err := FilesystemObjectReceiptDigest(request.Claim.ProjectID, object.ArtifactID, object.DeleteOperationID)
		if err != nil {
			return deletiondomain.TargetResult{}, err
		}
		if err := target.store.CommitFilesystemObjectDeletion(ctx, request, object, receipt); err != nil {
			return deletiondomain.TargetResult{}, err
		}
	}
	if err := target.removeProjectDirectories(request); err != nil {
		return deletiondomain.TargetResult{}, err
	}
	if err := target.store.VerifyFilesystemDeletionComplete(ctx, request); err != nil {
		return deletiondomain.TargetResult{}, err
	}
	return deletiondomain.TargetResult{
		Disposition:    deletiondomain.DispositionPurged,
		HandlerVersion: governance.HandlerFilesystemOSRootV1,
		Reason:         governance.ReasonPurged,
	}, nil
}

// removeProjectDirectories removes the project's own file directories when the whole project is
// purged (specs/2026-10-29-saga-illustrations.md R4.1); a book, a chapter or a chat keeps them.
func (target *FilesystemTarget) removeProjectDirectories(request deletiondomain.TargetRequest) error {
	fields, err := request.Tombstone.Identity.SelectedVector.Fields()
	if err != nil {
		return ErrFilesystemTargetConflict
	}
	if request.Tombstone.Identity.Mode != governance.PurgeModeSourceHardPurge || fields.Kind != governance.DeletionSelectionProject {
		return nil
	}
	for _, directory := range target.projectDirectories {
		key := directory(request.Claim.ProjectID)
		if ValidateFilesystemObjectKey(key) != nil {
			return ErrFilesystemTargetConflict
		}
		if err := target.root.RemoveAll(key); err != nil {
			return ErrFilesystemTargetConflict
		}
		if err := target.requireObjectAbsent(key); err != nil {
			return err
		}
	}
	return nil
}

func (target *FilesystemTarget) removeObject(key string) error {
	info, err := target.root.Lstat(key)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return ErrFilesystemTargetConflict
	}
	if info.IsDir() {
		return ErrFilesystemTargetConflict
	}
	if err := target.root.Remove(key); err != nil && !errors.Is(err, os.ErrNotExist) {
		return ErrFilesystemTargetConflict
	}
	return nil
}

func (target *FilesystemTarget) requireObjectAbsent(key string) error {
	if _, err := target.root.Lstat(key); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return ErrFilesystemTargetConflict
	}
	return ErrFilesystemUnprovenMissing
}

func ValidateFilesystemObjectKey(key string) error {
	if key == "" || strings.IndexByte(key, 0) >= 0 || strings.ContainsRune(key, '\\') ||
		path.IsAbs(key) || strings.HasPrefix(key, "//") || windowsVolumePrefixedObjectKey(key) ||
		path.Clean(key) != key {
		return ErrFilesystemTargetConflict
	}
	for _, component := range strings.Split(key, "/") {
		if component == "" || component == "." || component == ".." {
			return ErrFilesystemTargetConflict
		}
	}
	return nil
}

func windowsVolumePrefixedObjectKey(key string) bool {
	if len(key) < 2 || key[1] != ':' {
		return false
	}
	return (key[0] >= 'a' && key[0] <= 'z') || (key[0] >= 'A' && key[0] <= 'Z')
}

// FilesystemObjectReceiptDigest proves the deletion of one book cover file by its tombstone.
func FilesystemObjectReceiptDigest(projectID, artifactID, deleteOperationID string) (governance.Digest, error) {
	if !deletiondomain.ValidUUID(projectID) || !deletiondomain.ValidUUID(artifactID) || !deletiondomain.ValidUUID(deleteOperationID) {
		return governance.Digest{}, ErrFilesystemTargetConflict
	}
	projection := "CKBookCoverDeletionReceiptV1\x00" + projectID + "\x00" + artifactID + "\x00" +
		deleteOperationID + "\x00privacy"
	return governance.Digest(sha256.Sum256([]byte(projection))), nil
}
