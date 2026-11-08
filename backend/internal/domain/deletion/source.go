package deletion

import (
	"errors"
	"time"

	"canonkeeper/backend/internal/governance"
)

const (
	SourceRestoreWindow  = 7 * 24 * time.Hour
	DerivedPurgeDeadline = 24 * time.Hour
	SourceHardPurgeGrace = 24 * time.Hour
)

type EntityKind = governance.SourceEntityKind

const (
	EntityProject = governance.SourceEntityProject
	EntityBook    = governance.SourceEntityBook
	EntityChapter = governance.SourceEntityChapter
)

type GovernedSourceDeleteWindow struct {
	PurgeAfter           time.Time
	DerivedPurgeDeadline time.Time
	SourcePurgeDeadline  time.Time
}

func (window GovernedSourceDeleteWindow) validate(fencedAt time.Time) error {
	if !canonicalMicrosecond(fencedAt) ||
		!canonicalMicrosecond(window.PurgeAfter) ||
		!canonicalMicrosecond(window.DerivedPurgeDeadline) ||
		!canonicalMicrosecond(window.SourcePurgeDeadline) {
		return errors.New("deletion: governed source window has a noncanonical timestamp")
	}
	if !window.PurgeAfter.Equal(fencedAt.Add(SourceRestoreWindow)) ||
		!window.DerivedPurgeDeadline.Equal(fencedAt.Add(DerivedPurgeDeadline)) ||
		!window.SourcePurgeDeadline.Equal(window.PurgeAfter.Add(SourceHardPurgeGrace)) {
		return errors.New("deletion: governed source window has a noncanonical duration")
	}
	return nil
}

type SourceDeleteRequest struct {
	TombstoneID    string
	ActorUserID    string
	ProjectID      string
	EntityKind     EntityKind
	EntityID       string
	FencedAt       time.Time
	GovernedWindow *GovernedSourceDeleteWindow
}

func (request SourceDeleteRequest) Validate() error {
	if !canonicalUUID(request.TombstoneID) ||
		!canonicalUUID(request.ActorUserID) ||
		!canonicalUUID(request.ProjectID) ||
		!canonicalUUID(request.EntityID) {
		return errors.New("deletion: source delete request has an invalid identity")
	}
	if !request.EntityKind.Valid() {
		return errors.New("deletion: source delete request has an unknown entity kind")
	}
	if request.EntityKind == EntityProject && request.EntityID != request.ProjectID {
		return errors.New("deletion: project delete identity is inconsistent")
	}
	if !canonicalMicrosecond(request.FencedAt) {
		return errors.New("deletion: source delete fence time is not a whole UTC microsecond")
	}
	if request.GovernedWindow == nil {
		return errors.New("deletion: source delete lacks its retention window")
	}
	if err := request.GovernedWindow.validate(request.FencedAt); err != nil {
		return err
	}
	return nil
}

type SourceFenceResult struct {
	TombstoneID string
	Deleted     governance.SourceGenerationVector
	Current     governance.SourceGenerationVector
}

func (result SourceFenceResult) Validate() error {
	if !canonicalUUID(result.TombstoneID) {
		return errors.New("deletion: source fence result has an invalid tombstone identity")
	}
	if err := result.Deleted.Validate(); err != nil {
		return errors.New("deletion: source fence result has an invalid deleted vector")
	}
	if err := result.Current.Validate(); err != nil {
		return errors.New("deletion: source fence result has an invalid current vector")
	}
	if err := validateFenceTransition(result.Deleted, result.Current); err != nil {
		return err
	}
	return nil
}

type SourceRestoreRequest struct {
	ActorUserID string
	ProjectID   string
	EntityKind  EntityKind
	EntityID    string
}

func (request SourceRestoreRequest) Validate() error {
	if !canonicalUUID(request.ActorUserID) ||
		!canonicalUUID(request.ProjectID) ||
		!canonicalUUID(request.EntityID) {
		return errors.New("deletion: source restore request has an invalid identity")
	}
	if !request.EntityKind.Valid() {
		return errors.New("deletion: source restore request has an unknown entity kind")
	}
	if request.EntityKind == EntityProject && request.EntityID != request.ProjectID {
		return errors.New("deletion: project restore identity is inconsistent")
	}
	return nil
}

func validateFenceTransition(deleted, current governance.SourceGenerationVector) error {
	deletedKind, err := deleted.Kind()
	if err != nil {
		return errors.New("deletion: invalid deleted source vector")
	}
	currentKind, err := current.Kind()
	if err != nil || currentKind != deletedKind {
		return errors.New("deletion: source fence changed the selected entity shape")
	}
	valid := false
	switch deletedKind {
	case EntityProject:
		valid = current.ProjectID == deleted.ProjectID &&
			current.ProjectGeneration == deleted.ProjectGeneration+1
	case EntityBook:
		valid = current.ProjectID == deleted.ProjectID &&
			current.ProjectGeneration == deleted.ProjectGeneration &&
			current.BookID == deleted.BookID &&
			current.BookGeneration == deleted.BookGeneration+1
	case EntityChapter:
		valid = current.ProjectID == deleted.ProjectID &&
			current.ProjectGeneration == deleted.ProjectGeneration &&
			current.BookID == deleted.BookID &&
			current.BookGeneration == deleted.BookGeneration &&
			current.ChapterID == deleted.ChapterID &&
			current.ChapterGeneration == deleted.ChapterGeneration+1
	}
	if !valid {
		return errors.New("deletion: source fence did not increment exactly its selected generation")
	}
	return nil
}

func canonicalMicrosecond(value time.Time) bool {
	return !value.IsZero() && value.Location() == time.UTC && value.Nanosecond()%1000 == 0
}
