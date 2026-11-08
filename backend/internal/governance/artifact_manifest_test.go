package governance

import (
	"fmt"
	"testing"
)

func TestArtifactKindsMatchCurrentRegistry(t *testing.T) {
	want := []ArtifactKind{
		ArtifactJob, ArtifactImportUpload, ArtifactImportJobRequest,
		ArtifactChatTurn, ArtifactAgentWorkflowInput,
	}
	got := ArtifactKinds()
	if len(got) != len(want) {
		t.Fatalf("ArtifactKinds() count = %d, want %d", len(got), len(want))
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("ArtifactKinds()[%d] = %q, want %q", index, got[index], want[index])
		}
	}
}

func TestArtifactManifestUsesV1ForScopesAndV2ForExactChapters(t *testing.T) {
	draft := manifestTestView(t, SourceViewDraft)
	project := manifestTestProjectRef(t, draft)
	scopeInput := ArtifactManifestInput{
		Kind: ArtifactChatTurn, ProjectID: manifestTestID(1), ArtifactID: manifestTestID(10),
		OwnerSourceView: draft, Refs: []ArtifactSourceRef{project},
	}
	scope, err := NewArtifactManifest(scopeInput)
	if err != nil {
		t.Fatal(err)
	}
	if scope.Version() != 1 || scope.SourceIdentityState() != SourceIdentityScopeOnly {
		t.Fatalf("scope manifest = version %d state %q", scope.Version(), scope.SourceIdentityState())
	}

	exact := manifestTestExactChapterRef(t, manifestTestID(1), manifestTestID(2), manifestTestID(3), draft, AbsentChatArm())
	exactInput := ArtifactManifestInput{
		Kind: ArtifactChatTurn, ProjectID: manifestTestID(1), ArtifactID: manifestTestID(11),
		OwnerSourceView: draft, Refs: []ArtifactSourceRef{exact},
	}
	if _, err := NewArtifactManifest(exactInput); err == nil {
		t.Fatal("V1 accepted an exact chapter ref")
	}
	current, err := NewArtifactManifestV2(exactInput)
	if err != nil {
		t.Fatal(err)
	}
	if current.Version() != 2 || current.SourceIdentityState() != SourceIdentityTextDerivedCurrent {
		t.Fatalf("exact manifest = version %d state %q", current.Version(), current.SourceIdentityState())
	}
	exactInput.Refs = append(exactInput.Refs, exact)
	if _, err := NewArtifactManifestV2(exactInput); err == nil {
		t.Fatal("duplicate exact chapter ref accepted")
	}
}

func TestArtifactManifestIsOrderIndependentAndRejectsMixedAuthority(t *testing.T) {
	draft := manifestTestView(t, SourceViewDraft)
	first := manifestTestExactChapterRef(t, manifestTestID(1), manifestTestID(2), manifestTestID(3), draft, AbsentChatArm())
	second := manifestTestExactChapterRef(t, manifestTestID(1), manifestTestID(2), manifestTestID(4), draft, AbsentChatArm())
	base := ArtifactManifestInput{
		Kind: ArtifactChatTurn, ProjectID: manifestTestID(1), ArtifactID: manifestTestID(15),
		OwnerSourceView: draft, Refs: []ArtifactSourceRef{first, second},
	}
	left, err := NewArtifactManifestV2(base)
	if err != nil {
		t.Fatal(err)
	}
	base.Refs[0], base.Refs[1] = base.Refs[1], base.Refs[0]
	right, err := NewArtifactManifestV2(base)
	if err != nil {
		t.Fatal(err)
	}
	if left.Hash() != right.Hash() {
		t.Fatal("manifest hash depends on ref order")
	}

	published := manifestTestView(t, SourceViewPublished)
	base.Refs = []ArtifactSourceRef{
		first,
		manifestTestExactChapterRef(t, manifestTestID(1), manifestTestID(2), manifestTestID(4), published, AbsentChatArm()),
	}
	if _, err := NewArtifactManifestV2(base); err == nil {
		t.Fatal("mixed source views accepted")
	}
	base.Refs = []ArtifactSourceRef{
		first,
		manifestTestExactChapterRef(t, manifestTestID(20), manifestTestID(21), manifestTestID(22), draft, AbsentChatArm()),
	}
	if _, err := NewArtifactManifestV2(base); err == nil {
		t.Fatal("mixed projects accepted")
	}
}

func manifestTestProjectRef(t *testing.T, view SourceViewArm) ArtifactSourceRef {
	t.Helper()
	ref, err := NewViewBoundProjectRef(SourceGenerationVector{
		ProjectID: manifestTestID(1), ProjectGeneration: 1,
	}, view, AbsentChatArm())
	if err != nil {
		t.Fatal(err)
	}
	return ref
}

func manifestTestExactChapterRef(
	t *testing.T,
	projectID, bookID, chapterID string,
	view SourceViewArm,
	chat ChatArm,
) ArtifactSourceRef {
	t.Helper()
	vector := SourceGenerationVector{
		ProjectID: projectID, ProjectGeneration: 1,
		BookID: bookID, BookGeneration: 1,
		ChapterID: chapterID, ChapterGeneration: 1,
	}
	generation, err := SourceGenerationDigest(vector)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := NewExactChapterArtifactRef(ChapterSourceRefInput{
		Source: vector, SourceRevision: 1, SourceView: view,
		SourceGenerationDigest: generation, ContentHash: Digest{1}, Chat: chat,
	})
	if err != nil {
		t.Fatal(err)
	}
	return ref
}

func manifestTestView(t *testing.T, view SourceView) SourceViewArm {
	t.Helper()
	arm, err := PresentSourceView(view)
	if err != nil {
		t.Fatal(err)
	}
	return arm
}

func manifestTestID(number int) string {
	return fmt.Sprintf("10000000-0000-4000-8000-%012d", number)
}
