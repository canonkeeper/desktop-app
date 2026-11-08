package governance

import (
	"bytes"
	"errors"
	"math"
	"sort"
)

type ArtifactManifestInput struct {
	Kind            ArtifactKind
	ProjectID       string
	ArtifactID      string
	OwnerSourceView SourceViewArm
	Refs            []ArtifactSourceRef
}

func ArtifactManifestProjection(input ArtifactManifestInput) ([]byte, error) {
	return artifactManifestProjection(input, 1)
}

func ArtifactManifestV2Projection(input ArtifactManifestInput) ([]byte, error) {
	return artifactManifestProjection(input, 2)
}

func artifactManifestProjection(input ArtifactManifestInput, version uint32) ([]byte, error) {
	kindTag, known := input.Kind.tag()
	projectID, projectErr := parseCanonicalUUID(input.ProjectID)
	artifactID, artifactErr := parseCanonicalUUID(input.ArtifactID)
	if !known || projectErr != nil || artifactErr != nil || input.OwnerSourceView.Validate() != nil || len(input.Refs) == 0 || uint64(len(input.Refs)) > math.MaxUint32 ||
		(version != 1 && version != 2) {
		return nil, errors.New("governance: invalid artifact manifest identity")
	}

	refProjections := make([][]byte, 0, len(input.Refs))
	seen := make(map[string]struct{}, len(input.Refs))
	exactSourceCount := 0
	for _, ref := range input.Refs {
		if ref.projectID != input.ProjectID || ref.sourceView != input.OwnerSourceView {
			return nil, errors.New("governance: artifact manifest mixes project or source view arms")
		}
		if ref.arm == artifactRefChapterText {
			if version != 2 || ref.textIdentityState != SourceIdentityTextDerivedCurrent {
				return nil, errors.New("governance: exact chapter ref requires manifest V2")
			}
			exactSourceCount++
		}
		projection, err := ArtifactSourceRefProjection(ref)
		if err != nil {
			return nil, err
		}
		key := string(projection)
		if _, duplicate := seen[key]; duplicate {
			return nil, errors.New("governance: duplicate artifact source ref")
		}
		seen[key] = struct{}{}
		refProjections = append(refProjections, projection)
	}
	if version == 2 && exactSourceCount == 0 {
		return nil, errors.New("governance: V2 manifest has no exact source reference")
	}
	// Хеш манифеста не зависит от порядка refs во входном срезе.
	sort.Slice(refProjections, func(left, right int) bool {
		return bytes.Compare(refProjections[left], refProjections[right]) < 0
	})

	projection := []byte{kindTag}
	projection = append(projection, projectID[:]...)
	projection = append(projection, artifactID[:]...)
	projection = appendSourceViewArm(projection, input.OwnerSourceView)
	projection = appendU32(projection, version)
	projection = appendU32(projection, uint32(len(refProjections)))
	for _, refProjection := range refProjections {
		projection = appendLengthPrefixed(projection, refProjection)
	}
	return projection, nil
}

func ArtifactManifestHash(input ArtifactManifestInput) ([32]byte, error) {
	projection, err := ArtifactManifestProjection(input)
	return hashProjection("CKArtifactSourceManifestV1\x00", projection, err)
}

func ArtifactManifestV2Hash(input ArtifactManifestInput) ([32]byte, error) {
	projection, err := ArtifactManifestV2Projection(input)
	return hashProjection("CKArtifactSourceManifestV2\x00", projection, err)
}

type ArtifactManifest struct {
	version  uint32
	hash     [32]byte
	refCount uint32
	state    SourceIdentityState
}

func NewArtifactManifest(input ArtifactManifestInput) (ArtifactManifest, error) {
	digest, err := ArtifactManifestHash(input)
	if err != nil {
		return ArtifactManifest{}, err
	}
	return ArtifactManifest{
		version:  1,
		hash:     digest,
		refCount: uint32(len(input.Refs)),
		state:    SourceIdentityScopeOnly,
	}, nil
}

func NewArtifactManifestV2(input ArtifactManifestInput) (ArtifactManifest, error) {
	digest, err := ArtifactManifestV2Hash(input)
	if err != nil {
		return ArtifactManifest{}, err
	}
	return ArtifactManifest{
		version:  2,
		hash:     digest,
		refCount: uint32(len(input.Refs)),
		state:    SourceIdentityTextDerivedCurrent,
	}, nil
}

func (manifest ArtifactManifest) Version() uint32 {
	return manifest.version
}

func (manifest ArtifactManifest) Hash() [32]byte {
	return manifest.hash
}

func (manifest ArtifactManifest) RefCount() uint32 {
	return manifest.refCount
}

func (manifest ArtifactManifest) SourceIdentityState() SourceIdentityState {
	return manifest.state
}
