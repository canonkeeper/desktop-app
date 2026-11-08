package governance

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"math"
	"time"
	"unicode/utf8"
)

type deletionVectorArm uint8

const (
	deletionVectorProject deletionVectorArm = 1
	deletionVectorBook    deletionVectorArm = 2
	deletionVectorChapter deletionVectorArm = 3
	deletionVectorChat    deletionVectorArm = 4
)

type DeletionSelectedVector struct {
	arm       deletionVectorArm
	source    SourceGenerationVector
	projectID string
	chatID    string
	chatEpoch ChatLifecycleEpoch
}

type DeletionSelectionKind string

const (
	DeletionSelectionProject DeletionSelectionKind = "project"
	DeletionSelectionBook    DeletionSelectionKind = "book"
	DeletionSelectionChapter DeletionSelectionKind = "chapter"
	DeletionSelectionChat    DeletionSelectionKind = "chat"
)

type DeletionSelectedVectorFields struct {
	Kind               DeletionSelectionKind
	Source             SourceGenerationVector
	ProjectID          string
	ChatID             string
	ChatLifecycleEpoch ChatLifecycleEpoch
}

func (vector DeletionSelectedVector) Fields() (DeletionSelectedVectorFields, error) {
	if _, err := SelectedVectorProjection(vector); err != nil {
		return DeletionSelectedVectorFields{}, err
	}
	switch vector.arm {
	case deletionVectorProject:
		return DeletionSelectedVectorFields{
			Kind: DeletionSelectionProject, Source: vector.source, ProjectID: vector.source.ProjectID,
		}, nil
	case deletionVectorBook:
		return DeletionSelectedVectorFields{
			Kind: DeletionSelectionBook, Source: vector.source, ProjectID: vector.source.ProjectID,
		}, nil
	case deletionVectorChapter:
		return DeletionSelectedVectorFields{
			Kind: DeletionSelectionChapter, Source: vector.source, ProjectID: vector.source.ProjectID,
		}, nil
	case deletionVectorChat:
		return DeletionSelectedVectorFields{
			Kind:               DeletionSelectionChat,
			ProjectID:          vector.projectID,
			ChatID:             vector.chatID,
			ChatLifecycleEpoch: vector.chatEpoch,
		}, nil
	default:
		return DeletionSelectedVectorFields{}, errors.New("governance: unknown selected vector arm")
	}
}

func ProjectDeletionVector(vector SourceGenerationVector) (DeletionSelectedVector, error) {
	return sourceDeletionVector(vector, SourceEntityProject, deletionVectorProject)
}

func BookDeletionVector(vector SourceGenerationVector) (DeletionSelectedVector, error) {
	return sourceDeletionVector(vector, SourceEntityBook, deletionVectorBook)
}

func ChapterDeletionVector(vector SourceGenerationVector) (DeletionSelectedVector, error) {
	return sourceDeletionVector(vector, SourceEntityChapter, deletionVectorChapter)
}

func sourceDeletionVector(vector SourceGenerationVector, want SourceEntityKind, arm deletionVectorArm) (DeletionSelectedVector, error) {
	kind, err := vector.Kind()
	if err != nil || kind != want {
		return DeletionSelectedVector{}, errors.New("governance: selected source vector has the wrong scope")
	}
	return DeletionSelectedVector{arm: arm, source: vector}, nil
}

func ChatDeletionVector(projectID, chatID string, epoch ChatLifecycleEpoch) (DeletionSelectedVector, error) {
	if _, err := parseCanonicalUUID(projectID); err != nil {
		return DeletionSelectedVector{}, errors.New("governance: invalid chat project identity")
	}
	if _, err := parseCanonicalUUID(chatID); err != nil || epoch == 0 {
		return DeletionSelectedVector{}, errors.New("governance: invalid chat lifecycle identity")
	}
	return DeletionSelectedVector{arm: deletionVectorChat, projectID: projectID, chatID: chatID, chatEpoch: epoch}, nil
}

func SelectedVectorProjection(selection DeletionSelectedVector) ([]byte, error) {
	projection := []byte{byte(selection.arm)}
	switch selection.arm {
	case deletionVectorProject, deletionVectorBook, deletionVectorChapter:
		kind, err := selection.source.Kind()
		if err != nil ||
			(selection.arm == deletionVectorProject && kind != SourceEntityProject) ||
			(selection.arm == deletionVectorBook && kind != SourceEntityBook) ||
			(selection.arm == deletionVectorChapter && kind != SourceEntityChapter) {
			return nil, errors.New("governance: invalid selected source vector")
		}
		digest, err := SourceGenerationDigest(selection.source)
		if err != nil {
			return nil, err
		}
		projection = append(projection, digest[:]...)
	case deletionVectorChat:
		projectID, err := parseCanonicalUUID(selection.projectID)
		if err != nil {
			return nil, errors.New("governance: invalid selected chat project")
		}
		chatID, err := parseCanonicalUUID(selection.chatID)
		if err != nil || selection.chatEpoch == 0 {
			return nil, errors.New("governance: invalid selected chat lifecycle")
		}
		projection = append(projection, projectID[:]...)
		projection = append(projection, chatID[:]...)
		projection = appendU64(projection, uint64(selection.chatEpoch))
	default:
		return nil, errors.New("governance: unknown selected vector arm")
	}
	return projection, nil
}

func SelectedVectorDigest(selection DeletionSelectedVector) ([32]byte, error) {
	projection, err := SelectedVectorProjection(selection)
	return hashProjection("CKDeletionSelectedVectorV1\x00", projection, err)
}

type DeletionOrigin string

const (
	DeletionOriginGovernedOnline DeletionOrigin = "governed_online"
)

func (origin DeletionOrigin) tag() (byte, bool) {
	switch origin {
	case DeletionOriginGovernedOnline:
		return 1, true
	default:
		return 0, false
	}
}

type PurgeMode string

const (
	PurgeModeDerivedGeneration PurgeMode = "derived_generation"
	PurgeModeSourceHardPurge   PurgeMode = "source_hard_purge"
	PurgeModeChatEpoch         PurgeMode = "chat_epoch"
)

func (mode PurgeMode) tag() (byte, bool) {
	switch mode {
	case PurgeModeDerivedGeneration:
		return 1, true
	case PurgeModeSourceHardPurge:
		return 2, true
	case PurgeModeChatEpoch:
		return 3, true
	default:
		return 0, false
	}
}

type PurgeDeadlineArm struct {
	present bool
	value   time.Time
}

func AbsentPurgeDeadline() PurgeDeadlineArm {
	return PurgeDeadlineArm{}
}

func PresentPurgeDeadline(value time.Time) (PurgeDeadlineArm, error) {
	if _, err := unixMicros(value); err != nil {
		return PurgeDeadlineArm{}, err
	}
	return PurgeDeadlineArm{present: true, value: value}, nil
}

type DeletionTombstoneInput struct {
	TombstoneID               string
	Origin                    DeletionOrigin
	Mode                      PurgeMode
	SelectedVector            DeletionSelectedVector
	FencedAt                  time.Time
	Deadline                  PurgeDeadlineArm
	PermissionLossActorUserID string
}

func DeletionTombstoneProjection(input DeletionTombstoneInput) ([]byte, error) {
	tombstoneID, err := parseCanonicalUUID(input.TombstoneID)
	if err != nil {
		return nil, errors.New("governance: invalid deletion tombstone identity")
	}
	originTag, originOK := input.Origin.tag()
	modeTag, modeOK := input.Mode.tag()
	if !originOK || !modeOK {
		return nil, errors.New("governance: unknown deletion tombstone kind")
	}
	selectedVectorDigest, err := selectedVectorDigestForPurgeMode(input.Mode, input.SelectedVector)
	if err != nil {
		return nil, err
	}
	fencedMicros, err := unixMicros(input.FencedAt)
	if err != nil {
		return nil, err
	}
	if err := validateDeadlinePolicy(input.Deadline); err != nil {
		return nil, err
	}

	projection := make([]byte, 0, 128)
	projection = append(projection, tombstoneID[:]...)
	projection = append(projection, originTag, modeTag)
	projection = append(projection, selectedVectorDigest[:]...)
	projection = appendU64(projection, fencedMicros)
	if !input.Deadline.present {
		projection = append(projection, 0)
		return projection, nil
	}
	deadlineMicros, err := unixMicros(input.Deadline.value)
	if err != nil {
		return nil, err
	}
	projection = append(projection, 1)
	projection = appendU64(projection, deadlineMicros)
	if input.PermissionLossActorUserID != "" {
		actorUserID, err := parseCanonicalUUID(input.PermissionLossActorUserID)
		if err != nil || input.Mode != PurgeModeDerivedGeneration || input.SelectedVector.arm != deletionVectorProject {
			return nil, errors.New("governance: invalid permission-loss tombstone scope")
		}
		projection = append(projection, 1)
		projection = append(projection, actorUserID[:]...)
	}
	return projection, nil
}

func selectedVectorDigestForPurgeMode(mode PurgeMode, selection DeletionSelectedVector) ([32]byte, error) {
	digest, err := SelectedVectorDigest(selection)
	if err != nil {
		return [32]byte{}, err
	}
	switch mode {
	case PurgeModeDerivedGeneration, PurgeModeSourceHardPurge:
		if selection.arm == deletionVectorProject || selection.arm == deletionVectorBook || selection.arm == deletionVectorChapter {
			return digest, nil
		}
	case PurgeModeChatEpoch:
		if selection.arm == deletionVectorChat {
			return digest, nil
		}
	}
	return [32]byte{}, errors.New("governance: purge mode and selected vector arm do not match")
}

func validateDeadlinePolicy(deadline PurgeDeadlineArm) error {
	if !deadline.present && !deadline.value.IsZero() {
		return errors.New("governance: absent purge deadline carries a value")
	}
	if !deadline.present {
		return errors.New("governance: deletion requires a purge deadline")
	}
	return nil
}

func DeletionTombstoneDigest(input DeletionTombstoneInput) ([32]byte, error) {
	projection, err := DeletionTombstoneProjection(input)
	return hashProjection("CKDeletionTombstoneV1\x00", projection, err)
}
func hashProjection(domain string, projection []byte, err error) ([32]byte, error) {
	if err != nil {
		return [32]byte{}, err
	}
	preimage := make([]byte, 0, len(domain)+len(projection))
	preimage = append(preimage, domain...)
	preimage = append(preimage, projection...)
	return sha256.Sum256(preimage), nil
}

func appendU32(target []byte, value uint32) []byte {
	var encoded [4]byte
	binary.BigEndian.PutUint32(encoded[:], value)
	return append(target, encoded[:]...)
}

func appendEncodedString(target []byte, value string) ([]byte, error) {
	if !utf8.ValidString(value) {
		return nil, errors.New("governance: invalid UTF-8 string")
	}
	return appendLengthPrefixed(target, []byte(value)), nil
}

func isZeroDigest(digest [32]byte) bool {
	return digest == [32]byte{}
}

func unixMicros(value time.Time) (uint64, error) {
	if value.IsZero() || value.Location() != time.UTC || value.Unix() < 0 || value.Nanosecond()%1_000 != 0 {
		return 0, errors.New("governance: timestamp must be a nonnegative whole UTC microsecond")
	}
	seconds := uint64(value.Unix())
	if seconds > (math.MaxUint64-uint64(value.Nanosecond()/1_000))/1_000_000 {
		return 0, errors.New("governance: timestamp overflows microseconds")
	}
	return seconds*1_000_000 + uint64(value.Nanosecond()/1_000), nil
}

func unixSeconds(value time.Time) (uint64, error) {
	if value.IsZero() || value.Location() != time.UTC || value.Unix() < 0 || value.Nanosecond() != 0 {
		return 0, errors.New("governance: timestamp must be a nonnegative whole UTC second")
	}
	return uint64(value.Unix()), nil
}
