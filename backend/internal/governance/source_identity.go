package governance

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"strings"
)

const sourceGenerationDomain = "CKSourceGenerationV1\x00"

type LifecycleGeneration uint64

type SourceEntityKind string

const (
	SourceEntityProject SourceEntityKind = "project"
	SourceEntityBook    SourceEntityKind = "book"
	SourceEntityChapter SourceEntityKind = "chapter"
)

func (kind SourceEntityKind) Valid() bool {
	switch kind {
	case SourceEntityProject, SourceEntityBook, SourceEntityChapter:
		return true
	default:
		return false
	}
}

type SourceGenerationVector struct {
	ProjectID         string
	ProjectGeneration LifecycleGeneration
	BookID            string
	BookGeneration    LifecycleGeneration
	ChapterID         string
	ChapterGeneration LifecycleGeneration
}

func (vector SourceGenerationVector) Validate() error {
	if _, err := parseCanonicalUUID(vector.ProjectID); err != nil || vector.ProjectGeneration == 0 {
		return errors.New("governance: invalid project lifecycle component")
	}
	bookPresent := vector.BookID != "" || vector.BookGeneration != 0
	if bookPresent {
		if _, err := parseCanonicalUUID(vector.BookID); err != nil || vector.BookGeneration == 0 {
			return errors.New("governance: invalid book lifecycle component")
		}
	}
	chapterPresent := vector.ChapterID != "" || vector.ChapterGeneration != 0
	if chapterPresent {
		if !bookPresent {
			return errors.New("governance: chapter lifecycle component has no book")
		}
		if _, err := parseCanonicalUUID(vector.ChapterID); err != nil || vector.ChapterGeneration == 0 {
			return errors.New("governance: invalid chapter lifecycle component")
		}
	}
	return nil
}

func (vector SourceGenerationVector) Kind() (SourceEntityKind, error) {
	if err := vector.Validate(); err != nil {
		return "", err
	}
	if vector.ChapterID != "" {
		return SourceEntityChapter, nil
	}
	if vector.BookID != "" {
		return SourceEntityBook, nil
	}
	return SourceEntityProject, nil
}

func SourceGenerationProjection(vector SourceGenerationVector) ([]byte, error) {
	if err := vector.Validate(); err != nil {
		return nil, err
	}
	projectID, _ := parseCanonicalUUID(vector.ProjectID)
	projection := make([]byte, 0, 96)
	projection = appendLengthPrefixed(projection, projectID[:])
	projection = appendU64(projection, uint64(vector.ProjectGeneration))
	if vector.BookID != "" {
		bookID, _ := parseCanonicalUUID(vector.BookID)
		projection = appendLengthPrefixed(projection, bookID[:])
		projection = appendU64(projection, uint64(vector.BookGeneration))
	}
	if vector.ChapterID != "" {
		chapterID, _ := parseCanonicalUUID(vector.ChapterID)
		projection = appendLengthPrefixed(projection, chapterID[:])
		projection = appendU64(projection, uint64(vector.ChapterGeneration))
	}
	return projection, nil
}

func SourceGenerationDigest(vector SourceGenerationVector) ([32]byte, error) {
	projection, err := SourceGenerationProjection(vector)
	if err != nil {
		return [32]byte{}, err
	}
	payload := make([]byte, 0, len(sourceGenerationDomain)+len(projection))
	payload = append(payload, sourceGenerationDomain...)
	payload = append(payload, projection...)
	return sha256.Sum256(payload), nil
}

func parseCanonicalUUID(value string) ([16]byte, error) {
	var parsed [16]byte
	if len(value) != 36 || value != strings.ToLower(value) || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return parsed, errors.New("governance: invalid canonical UUID")
	}
	hexValue := value[:8] + value[9:13] + value[14:18] + value[19:23] + value[24:]
	if len(hexValue) != 32 {
		return parsed, errors.New("governance: invalid canonical UUID")
	}
	for _, char := range hexValue {
		if !(char >= '0' && char <= '9') && !(char >= 'a' && char <= 'f') {
			return parsed, errors.New("governance: invalid canonical UUID")
		}
	}
	decoded, err := hex.DecodeString(hexValue)
	if err != nil || len(decoded) != len(parsed) {
		return parsed, errors.New("governance: invalid canonical UUID")
	}
	copy(parsed[:], decoded)
	return parsed, nil
}

func appendLengthPrefixed(target, value []byte) []byte {
	target = appendU64(target, uint64(len(value)))
	return append(target, value...)
}

func appendU64(target []byte, value uint64) []byte {
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], value)
	return append(target, encoded[:]...)
}
