package governance

import (
	"encoding/hex"
	"math"
	"testing"

	"canonkeeper/backend/internal/domain/textface"
)

const (
	canonicalTestProjectID  = "00000000-0000-4000-8000-000000000001"
	canonicalTestBookID     = "00000000-0000-4000-8000-000000000002"
	canonicalTestChapterAID = "00000000-0000-4000-8000-000000000003"
	canonicalTestChapterBID = "00000000-0000-4000-8000-000000000004"
	canonicalTestParagraphA = "00000000-0000-4000-8000-000000000005"
	canonicalTestParagraphB = "00000000-0000-4000-8000-000000000006"
)

func TestCanonicalSourceHashesAreDeterministicAndSensitive(t *testing.T) {
	paragraphs := []CanonicalParagraphContent{
		{ParagraphID: canonicalTestParagraphB, Order: 1, Content: "второй"},
		{ParagraphID: canonicalTestParagraphA, Order: 0, Content: "первый"},
	}
	chapterHash := mustCanonicalChapterHash(t, paragraphs)
	reordered := mustCanonicalChapterHash(t, []CanonicalParagraphContent{paragraphs[1], paragraphs[0]})
	if chapterHash != reordered {
		t.Fatal("chapter hash depends on DB row arrival order")
	}
	for name, changed := range map[string][]CanonicalParagraphContent{
		"content": {{ParagraphID: canonicalTestParagraphA, Order: 0, Content: "изменён"}, paragraphs[0]},
		"order":   {{ParagraphID: canonicalTestParagraphA, Order: 2, Content: "первый"}, paragraphs[0]},
		"id":      {{ParagraphID: canonicalTestChapterBID, Order: 0, Content: "первый"}, paragraphs[0]},
	} {
		if got := mustCanonicalChapterHash(t, changed); got == chapterHash {
			t.Fatalf("%s change did not change chapter hash", name)
		}
	}

	base := canonicalBookSourceForTest(t, chapterHash)
	manifest := mustCanonicalBookManifest(t, base)
	arrivalOrder := base
	arrivalOrder.Chapters = []CanonicalBookChapter{base.Chapters[1], base.Chapters[0]}
	if got := mustCanonicalBookManifest(t, arrivalOrder); got != manifest {
		t.Fatal("book manifest depends on DB row arrival order")
	}

	mutations := map[string]func(*CanonicalBookSource){
		"book title":        func(v *CanonicalBookSource) { v.Title = "Другая книга" },
		"chapter title":     func(v *CanonicalBookSource) { v.Chapters[0].Title = "Другая глава" },
		"membership":        func(v *CanonicalBookSource) { v.Chapters = v.Chapters[:1] },
		"chapter order":     func(v *CanonicalBookSource) { v.Chapters[0].Order = 3 },
		"source revision":   func(v *CanonicalBookSource) { v.Chapters[0].SourceRevision++ },
		"project lifecycle": func(v *CanonicalBookSource) { v.ProjectLifecycleGeneration++ },
		"book lifecycle":    func(v *CanonicalBookSource) { v.BookLifecycleGeneration++ },
		"chapter lifecycle": func(v *CanonicalBookSource) { v.Chapters[0].SourceGeneration[0]++ },
		"content":           func(v *CanonicalBookSource) { v.Chapters[0].ChapterContentHash[0]++ },
		"view":              func(v *CanonicalBookSource) { v.SourceView = SourceViewPublished },
	}
	for name, mutate := range mutations {
		changed := base
		changed.Chapters = append([]CanonicalBookChapter(nil), base.Chapters...)
		mutate(&changed)
		if got := mustCanonicalBookManifest(t, changed); got == manifest {
			t.Fatalf("%s change did not change book manifest", name)
		}
	}

	ignored := base
	ignored.ProjectBookOrder = 99
	ignored.CatalogRevision = 42
	if got := mustCanonicalBookManifest(t, ignored); got != manifest {
		t.Fatal("project-relative book order or catalog revision changed manifest")
	}

	invalidBook := base
	invalidBook.Chapters = append([]CanonicalBookChapter(nil), base.Chapters...)
	invalidBook.Chapters[0].SourceRevision = uint64(math.MaxInt64) + 1
	if _, err := CanonicalBookSourceManifest(invalidBook); err == nil {
		t.Error("book manifest accepted source revision above int64")
	}
}

func TestCanonicalSourceHashGoldens(t *testing.T) {
	paragraphHash, err := CanonicalParagraphContentDigest("первый")
	if err != nil {
		t.Fatal(err)
	}
	paragraphs := []CanonicalParagraphContent{
		{ParagraphID: canonicalTestParagraphB, Order: 1, Content: "второй"},
		{ParagraphID: canonicalTestParagraphA, Order: 0, Content: "первый"},
	}
	chapterHash := mustCanonicalChapterHash(t, paragraphs)
	bookHash := mustCanonicalBookManifest(t, canonicalBookSourceForTest(t, chapterHash))
	metadataHash, err := CanonicalMetadataValueDigest(CanonicalMetadataBookTitle, "Книга")
	if err != nil {
		t.Fatal(err)
	}

	for name, testCase := range map[string]struct {
		got  Digest
		want string
	}{
		"paragraph": {paragraphHash, "2049986c289624086dea1fc991dbdb4b67c7021d31b0b85d5d0b7963d2e26100"},
		"chapter":   {chapterHash, "54744d5537ce8369a6a9b90357df47c49a9159db941249bc752a19dc8c80dae7"},
		"book":      {bookHash, "2e9e557803b5f5281e119091471f065514898d98171b946ee4bb771ce60f6172"},
		"metadata":  {metadataHash, "b01631bada0a8f14fa3def87a24155a30d2dc5f83e6d5aa072cd0c2d53661703"},
	} {
		if got := hex.EncodeToString(testCase.got[:]); got != testCase.want {
			t.Fatalf("%s golden = %s, want %s", name, got, testCase.want)
		}
	}
}

func TestCanonicalChapterSourceV1ValidatesWholeChapterIdentity(t *testing.T) {
	valid := CanonicalChapterSourceV1{
		ProjectID:        "10000000-0000-4000-8000-000000000001",
		BookID:           "20000000-0000-4000-8000-000000000001",
		ChapterID:        "30000000-0000-4000-8000-000000000001",
		SourceView:       SourceViewDraft,
		SourceRevision:   1,
		SourceGeneration: Digest{1},
		ContentHash:      Digest{2},
	}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*CanonicalChapterSourceV1){
		"chapter":    func(value *CanonicalChapterSourceV1) { value.ChapterID = "" },
		"view":       func(value *CanonicalChapterSourceV1) { value.SourceView = "unknown" },
		"revision":   func(value *CanonicalChapterSourceV1) { value.SourceRevision = 0 },
		"generation": func(value *CanonicalChapterSourceV1) { value.SourceGeneration = Digest{} },
		"hash":       func(value *CanonicalChapterSourceV1) { value.ContentHash = Digest{} },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := valid
			mutate(&candidate)
			if candidate.Validate() == nil {
				t.Fatalf("invalid chapter source accepted: %#v", candidate)
			}
		})
	}
}

func TestCanonicalChapterSourceLocatorRequiresLiteralChapterRef(t *testing.T) {
	valid := CanonicalChapterSourceLocatorV1{
		ProjectID:        canonicalTestProjectID,
		BookID:           canonicalTestBookID,
		ChapterID:        canonicalTestChapterAID,
		SourceRef:        canonicalTestChapterAID,
		SourceView:       SourceViewDraft,
		SourceRevision:   1,
		SourceGeneration: Digest{1},
		ContentHash:      Digest{2},
	}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, sourceRef := range map[string]string{
		"missing":  "",
		"mismatch": canonicalTestChapterBID,
	} {
		t.Run(name, func(t *testing.T) {
			candidate := valid
			candidate.SourceRef = sourceRef
			if candidate.Validate() == nil {
				t.Fatalf("invalid chapter source ref accepted: %#v", candidate)
			}
		})
	}
}

func TestChapterSourceRefPreservesWholeChapterIdentity(t *testing.T) {
	vector := SourceGenerationVector{
		ProjectID: canonicalTestProjectID, ProjectGeneration: 1,
		BookID: canonicalTestBookID, BookGeneration: 2,
		ChapterID: canonicalTestChapterAID, ChapterGeneration: 3,
	}
	generation, err := SourceGenerationDigest(vector)
	if err != nil {
		t.Fatal(err)
	}
	view, err := PresentSourceView(SourceViewDraft)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := NewExactChapterArtifactRef(ChapterSourceRefInput{
		Source: vector, SourceRevision: 4, SourceView: view,
		SourceGenerationDigest: generation, ContentHash: canonicalTestDigest(5), Chat: AbsentChatArm(),
	})
	if err != nil {
		t.Fatal(err)
	}
	fields, err := ref.Fields()
	if err != nil {
		t.Fatal(err)
	}
	if fields.RefKind != ArtifactSourceRefChapter || fields.ScopeKind != "" ||
		fields.ProjectID != vector.ProjectID || fields.BookID != vector.BookID || fields.ChapterID != vector.ChapterID ||
		fields.SourceRevision != 4 || fields.LocatorSourceGenerationDigest != generation ||
		fields.ContentHash != canonicalTestDigest(5) || fields.TextIdentityState != SourceIdentityTextDerivedCurrent {
		t.Fatalf("chapter ref fields = %#v", fields)
	}
}

func canonicalBookSourceForTest(t *testing.T, chapterHash Digest) CanonicalBookSource {
	t.Helper()
	return CanonicalBookSource{
		ProjectID: canonicalTestProjectID, ProjectLifecycleGeneration: 1,
		SourceView: SourceViewDraft, BookID: canonicalTestBookID,
		Title: "Книга", BookLifecycleGeneration: 1,
		ProjectBookOrder: 7, CatalogRevision: 9,
		Chapters: []CanonicalBookChapter{
			{ChapterID: canonicalTestChapterBID, Order: 1, Title: "Вторая", SourceRevision: 1, SourceGeneration: canonicalTestDigest(3), ChapterContentHash: chapterHash},
			{ChapterID: canonicalTestChapterAID, Order: 0, Title: "Первая", SourceRevision: 1, SourceGeneration: canonicalTestDigest(4), ChapterContentHash: chapterHash},
		},
	}
}

func mustCanonicalChapterHash(t *testing.T, paragraphs []CanonicalParagraphContent) Digest {
	t.Helper()
	digest, err := CanonicalChapterContentDigest(paragraphs)
	if err != nil {
		t.Fatal(err)
	}
	return digest
}

func mustCanonicalBookManifest(t *testing.T, source CanonicalBookSource) Digest {
	t.Helper()
	digest, err := CanonicalBookSourceManifest(source)
	if err != nil {
		t.Fatal(err)
	}
	return digest
}

func canonicalTestDigest(value byte) Digest {
	var digest Digest
	digest[0] = value
	return digest
}

// specs/2026-11-08-italic-bold-in-text.md TF2.4: the digest of a chapter reads the plain text of its paragraphs, so a
// change of the author's italic and bold alone keeps it.
func TestTheDigestOfAChapterKeepsAChangeOfFaces(t *testing.T) {
	digest := func(markdown string) Digest {
		return mustCanonicalChapterHash(t, []CanonicalParagraphContent{
			{ParagraphID: canonicalTestParagraphA, Order: 0, Content: textface.Plain(textface.Marked(textface.FromMarkdown(markdown))).Text},
		})
	}
	if digest("Кайл *вошёл* в **кузницу**.") != digest("Кайл вошёл в кузницу.") {
		t.Fatal("a change of faces changed the digest of the chapter")
	}
}
