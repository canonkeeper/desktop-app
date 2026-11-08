package library

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"time"

	domainlibrary "canonkeeper/backend/internal/domain/library"

	"golang.org/x/image/webp"
)

type CoverMetadataStore interface {
	ActivateCover(context.Context, domainlibrary.ActivateCoverInput) (domainlibrary.CoverActivation, error)
	FindCover(context.Context, domainlibrary.CoverLookup) (domainlibrary.CoverArtifact, error)
	RemoveCover(context.Context, domainlibrary.RemoveCoverInput) (domainlibrary.CoverArtifact, error)
}

type CoverObjectStore interface {
	Create(context.Context, string, []byte) error
	Publish(context.Context, string, string) error
	Open(context.Context, string) (io.ReadCloser, error)
	Remove(context.Context, string) error
}

type CoverIdentity struct {
	ArtifactID string
	StagingKey string
	FinalKey   string
}

type CoverIdentityGenerator func() (CoverIdentity, error)

type UploadCoverInput struct {
	Actor   domainlibrary.Actor
	BookID  string
	Content []byte
}

type UploadCover struct {
	metadata CoverMetadataStore
	objects  CoverObjectStore
	identity CoverIdentityGenerator
}

func NewUploadCover(metadata CoverMetadataStore, objects CoverObjectStore, identity CoverIdentityGenerator) *UploadCover {
	if metadata == nil || objects == nil || identity == nil {
		panic("book cover upload: dependencies are required")
	}
	return &UploadCover{metadata: metadata, objects: objects, identity: identity}
}

func (upload *UploadCover) Execute(ctx context.Context, input UploadCoverInput) (domainlibrary.CoverActivation, error) {
	if input.Actor.UserID == "" || input.BookID == "" {
		return domainlibrary.CoverActivation{}, domainlibrary.ErrNotFound
	}
	mimeType, config, err := inspectCover(input.Content)
	if err != nil {
		return domainlibrary.CoverActivation{}, err
	}
	identity, err := upload.identity()
	if err != nil {
		return domainlibrary.CoverActivation{}, err
	}
	now := time.Now().UTC()
	digest := sha256.Sum256(input.Content)
	activationInput := domainlibrary.ActivateCoverInput{
		Actor: input.Actor, ArtifactID: identity.ArtifactID, BookID: input.BookID,
		StorageKey: identity.FinalKey, MIMEType: mimeType, ByteSize: int64(len(input.Content)),
		Width: config.Width, Height: config.Height, ContentSHA256: digest, At: now,
	}
	if err := activationInput.Validate(); err != nil || identity.StagingKey == identity.FinalKey {
		return domainlibrary.CoverActivation{}, domainlibrary.ErrValidation
	}
	if err := upload.objects.Create(ctx, identity.StagingKey, input.Content); err != nil {
		return domainlibrary.CoverActivation{}, err
	}
	cleanupCtx := context.WithoutCancel(ctx)
	if err := upload.objects.Publish(ctx, identity.StagingKey, identity.FinalKey); err != nil {
		return domainlibrary.CoverActivation{}, errors.Join(
			err,
			upload.objects.Remove(cleanupCtx, identity.StagingKey),
			upload.objects.Remove(cleanupCtx, identity.FinalKey),
		)
	}
	activation, err := upload.metadata.ActivateCover(ctx, activationInput)
	if err != nil {
		return domainlibrary.CoverActivation{}, errors.Join(err, upload.objects.Remove(cleanupCtx, identity.FinalKey))
	}
	return activation, nil
}

type CoverContent struct {
	Content  []byte
	MIMEType string
	ETag     string
}

type GetCover struct {
	metadata CoverMetadataStore
	objects  CoverObjectStore
}

func NewGetCover(metadata CoverMetadataStore, objects CoverObjectStore) *GetCover {
	if metadata == nil || objects == nil {
		panic("book cover read: dependencies are required")
	}
	return &GetCover{metadata: metadata, objects: objects}
}

func (read *GetCover) Execute(ctx context.Context, input domainlibrary.CoverLookup) (CoverContent, error) {
	artifact, err := read.metadata.FindCover(ctx, input)
	if err != nil {
		return CoverContent{}, err
	}
	object, err := read.objects.Open(ctx, artifact.StorageKey)
	if err != nil {
		return CoverContent{}, domainlibrary.ErrUnavailable
	}
	content, readErr := io.ReadAll(io.LimitReader(object, artifact.ByteSize+1))
	closeErr := object.Close()
	if readErr != nil || closeErr != nil || int64(len(content)) != artifact.ByteSize || sha256.Sum256(content) != artifact.ContentSHA256 {
		return CoverContent{}, domainlibrary.ErrUnavailable
	}
	return CoverContent{
		Content: content, MIMEType: artifact.MIMEType,
		ETag: `"` + hex.EncodeToString(artifact.ContentSHA256[:]) + `"`,
	}, nil
}

type DeleteCover struct {
	metadata CoverMetadataStore
}

func NewDeleteCover(metadata CoverMetadataStore) *DeleteCover {
	if metadata == nil {
		panic("book cover delete: metadata store is required")
	}
	return &DeleteCover{metadata: metadata}
}

func (remove *DeleteCover) Execute(ctx context.Context, input domainlibrary.RemoveCoverInput) (domainlibrary.CoverArtifact, error) {
	artifact, err := remove.metadata.RemoveCover(ctx, input)
	if err != nil {
		return domainlibrary.CoverArtifact{}, err
	}
	return artifact, nil
}

type CoverDeletionStore interface {
	NextCoverDeletion(context.Context) (domainlibrary.CoverArtifact, error)
	CompleteCoverDeletion(context.Context, domainlibrary.CoverArtifact, time.Time) error
}

type CoverDeletionReconciler struct {
	store   CoverDeletionStore
	objects CoverObjectStore
}

func NewCoverDeletionReconciler(store CoverDeletionStore, objects CoverObjectStore) *CoverDeletionReconciler {
	if store == nil || objects == nil {
		panic("book cover deletion: dependencies are required")
	}
	return &CoverDeletionReconciler{store: store, objects: objects}
}

func (reconciler *CoverDeletionReconciler) ReconcileOne(ctx context.Context) (bool, error) {
	if ctx == nil {
		return false, domainlibrary.ErrValidation
	}
	artifact, err := reconciler.store.NextCoverDeletion(ctx)
	if errors.Is(err, domainlibrary.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if artifact.StorageKey == "" || artifact.State != domainlibrary.CoverSuperseded && artifact.State != domainlibrary.CoverRemoved {
		return false, domainlibrary.ErrConflict
	}
	if err := reconciler.objects.Remove(ctx, artifact.StorageKey); err != nil {
		return false, err
	}
	if err := reconciler.store.CompleteCoverDeletion(ctx, artifact, time.Now().UTC()); err != nil && !errors.Is(err, domainlibrary.ErrNotFound) {
		return false, err
	}
	return true, nil
}

func inspectCover(content []byte) (string, image.Config, error) {
	if int64(len(content)) > domainlibrary.MaxCoverBytes {
		return "", image.Config{}, domainlibrary.ErrCoverTooLarge
	}
	if len(content) == 0 {
		return "", image.Config{}, domainlibrary.ErrUnsupportedCoverMedia
	}
	mimeType := http.DetectContentType(content)
	var config image.Config
	var err error
	switch mimeType {
	case "image/jpeg":
		config, err = jpeg.DecodeConfig(bytes.NewReader(content))
	case "image/png":
		config, err = png.DecodeConfig(bytes.NewReader(content))
	case "image/webp":
		config, err = webp.DecodeConfig(bytes.NewReader(content))
	default:
		return "", image.Config{}, domainlibrary.ErrUnsupportedCoverMedia
	}
	if err != nil {
		return "", image.Config{}, domainlibrary.ErrUnsupportedCoverMedia
	}
	if config.Width < domainlibrary.MinCoverWidth || config.Height < domainlibrary.MinCoverHeight {
		return "", image.Config{}, domainlibrary.ErrCoverTooSmall
	}
	return mimeType, config, nil
}

func NewSecureCoverIdentity() (CoverIdentity, error) {
	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		return CoverIdentity{}, err
	}
	random[6] = random[6]&0x0f | 0x40
	random[8] = random[8]&0x3f | 0x80
	var uuid [36]byte
	hex.Encode(uuid[0:8], random[0:4])
	uuid[8] = '-'
	hex.Encode(uuid[9:13], random[4:6])
	uuid[13] = '-'
	hex.Encode(uuid[14:18], random[6:8])
	uuid[18] = '-'
	hex.Encode(uuid[19:23], random[8:10])
	uuid[23] = '-'
	hex.Encode(uuid[24:36], random[10:16])
	token := hex.EncodeToString(random[16:])
	return CoverIdentity{
		ArtifactID: string(uuid[:]),
		StagingKey: "book-covers/" + token + ".staging",
		FinalKey:   "book-covers/" + token + ".final",
	}, nil
}
