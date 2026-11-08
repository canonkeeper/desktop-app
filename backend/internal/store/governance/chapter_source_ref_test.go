package governancestore

import (
	"bytes"
	"testing"

	"canonkeeper/backend/internal/governance"
	storedb "canonkeeper/backend/internal/store/db"
)

func TestArtifactSourceRefParamsSupportsExactChapterIdentity(t *testing.T) {
	vector := governance.SourceGenerationVector{
		ProjectID: artifactConstraintProjectID, ProjectGeneration: 1,
		BookID: artifactConstraintBookID, BookGeneration: 2,
		ChapterID: artifactConstraintChapterID, ChapterGeneration: 3,
	}
	generation, err := governance.SourceGenerationDigest(vector)
	if err != nil {
		t.Fatal(err)
	}
	view, err := governance.PresentSourceView(governance.SourceViewDraft)
	if err != nil {
		t.Fatal(err)
	}
	contentHash := governance.Digest{9}
	ref, err := governance.NewExactChapterArtifactRef(governance.ChapterSourceRefInput{
		Source: vector, SourceRevision: 4, SourceView: view,
		SourceGenerationDigest: generation, ContentHash: contentHash, Chat: governance.AbsentChatArm(),
	})
	if err != nil {
		t.Fatal(err)
	}
	fields, err := ref.Fields()
	if err != nil {
		t.Fatal(err)
	}
	ownerID, _ := storedb.ParseUUID(artifactConstraintOwnerID)
	projectID, _ := storedb.ParseUUID(artifactConstraintProjectID)
	params, err := ArtifactSourceRefParams(ownerID, projectID, fields)
	if err != nil {
		t.Fatal(err)
	}
	if params.RefKind != "chapter" || params.ScopeKind.Valid || !params.SourceRevision.Valid ||
		params.SourceRevision.Int64 != 4 || !params.TextIdentityState.Valid ||
		params.TextIdentityState.String != string(governance.SourceIdentityTextDerivedCurrent) ||
		!bytes.Equal(params.LocatorSourceGeneration, generation[:]) || !bytes.Equal(params.ContentHash, contentHash[:]) {
		t.Fatalf("chapter source params = %#v", params)
	}
}
