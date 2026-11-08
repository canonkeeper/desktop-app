package governance

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

func TestSourceGenerationV1GoldenVectors(t *testing.T) {
	fixtureBytes, err := os.ReadFile("testdata/source_generation_v1_golden.json")
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	var fixture sourceGenerationFixture
	decoder := json.NewDecoder(bytes.NewReader(fixtureBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&fixture); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if fixture.Schema != "CKSourceGenerationV1" || len(fixture.Vectors) != 3 {
		t.Fatalf("fixture header = %#v", fixture)
	}
	for _, row := range fixture.Vectors {
		t.Run(row.Name, func(t *testing.T) {
			got, err := SourceGenerationDigest(row.Vector)
			if err != nil {
				t.Fatalf("SourceGenerationDigest() error = %v", err)
			}
			if hex.EncodeToString(got[:]) != row.ExpectedSHA256 {
				t.Fatalf("digest = %x, want %s", got, row.ExpectedSHA256)
			}
		})
	}
}

func TestSourceGenerationCanonicalShapeAndDomainSeparation(t *testing.T) {
	project := SourceGenerationVector{ProjectID: "00112233-4455-6677-8899-aabbccddeeff", ProjectGeneration: 1}
	book := project
	book.BookID = "11111111-2222-3333-4444-555555555555"
	book.BookGeneration = 2
	chapter := book
	chapter.ChapterID = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	chapter.ChapterGeneration = 3

	projectProjection, err := SourceGenerationProjection(project)
	if err != nil {
		t.Fatalf("project projection error = %v", err)
	}
	bookProjection, err := SourceGenerationProjection(book)
	if err != nil {
		t.Fatalf("book projection error = %v", err)
	}
	chapterProjection, err := SourceGenerationProjection(chapter)
	if err != nil {
		t.Fatalf("chapter projection error = %v", err)
	}
	if !bytes.HasPrefix(bookProjection, projectProjection) || !bytes.HasPrefix(chapterProjection, bookProjection) {
		t.Fatal("source generation projections are not canonical ancestor prefixes")
	}
	if bytes.Contains(chapterProjection, []byte(SourceViewDraft)) || bytes.Contains(chapterProjection, []byte(SourceViewPublished)) {
		t.Fatal("source view entered CKSourceGenerationV1")
	}

	invalid := []SourceGenerationVector{
		{},
		{ProjectID: project.ProjectID},
		{ProjectID: project.ProjectID, ProjectGeneration: 1, BookGeneration: 2},
		{ProjectID: project.ProjectID, ProjectGeneration: 1, BookID: book.BookID},
		{ProjectID: project.ProjectID, ProjectGeneration: 1, ChapterID: chapter.ChapterID, ChapterGeneration: 3},
		{ProjectID: "00112233-4455-6677-8899-AABBCCDDEEFF", ProjectGeneration: 1},
		{ProjectID: "{00112233-4455-6677-8899-aabbccddeeff}", ProjectGeneration: 1},
	}
	for _, vector := range invalid {
		if _, err := SourceGenerationDigest(vector); err == nil {
			t.Fatalf("SourceGenerationDigest(%#v) error = nil", vector)
		}
	}
}

func TestSourceViewAndEntityKindContractsAreClosed(t *testing.T) {
	if _, err := PresentSourceView(SourceView("preview")); err == nil {
		t.Fatal("unknown source view accepted")
	}
	for _, kind := range []SourceEntityKind{SourceEntityProject, SourceEntityBook, SourceEntityChapter} {
		if !kind.Valid() {
			t.Fatalf("SourceEntityKind(%q).Valid() = false", kind)
		}
	}
	if SourceEntityKind("paragraph").Valid() {
		t.Fatal("unknown source entity kind accepted")
	}
}

type sourceGenerationFixture struct {
	Schema  string                       `json:"schema"`
	Vectors []sourceGenerationFixtureRow `json:"vectors"`
}

type sourceGenerationFixtureRow struct {
	Name           string                 `json:"name"`
	Vector         SourceGenerationVector `json:"vector"`
	ExpectedSHA256 string                 `json:"expectedSha256"`
}

func mustSourceDigest(t *testing.T, vector SourceGenerationVector) [32]byte {
	t.Helper()
	digest, err := SourceGenerationDigest(vector)
	if err != nil {
		t.Fatalf("SourceGenerationDigest() error = %v", err)
	}
	return digest
}

func TestSourceGenerationFixtureSchemaIsExact(t *testing.T) {
	want := sourceGenerationFixture{
		Schema: "CKSourceGenerationV1",
		Vectors: []sourceGenerationFixtureRow{
			{Name: "project", Vector: SourceGenerationVector{ProjectID: "00112233-4455-6677-8899-aabbccddeeff", ProjectGeneration: 1}, ExpectedSHA256: "62b825be0e8a536c8c449719854ee43c77be41f855636460d5b610e0e381ea40"},
			{Name: "book", Vector: SourceGenerationVector{ProjectID: "00112233-4455-6677-8899-aabbccddeeff", ProjectGeneration: 1, BookID: "11111111-2222-3333-4444-555555555555", BookGeneration: 2}, ExpectedSHA256: "3fd98b9225500ac65ee049c47619907f0d0b7d143a8a76cc59ebae865fd114f4"},
			{Name: "chapter", Vector: SourceGenerationVector{ProjectID: "00112233-4455-6677-8899-aabbccddeeff", ProjectGeneration: 1, BookID: "11111111-2222-3333-4444-555555555555", BookGeneration: 2, ChapterID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", ChapterGeneration: 3}, ExpectedSHA256: "2e00d3b3b4b5a26863c23e71163ed38217f2090b8c96149bd81dee84289a2b18"},
		},
	}
	bytes, err := os.ReadFile("testdata/source_generation_v1_golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var got sourceGenerationFixture
	if err := json.Unmarshal(bytes, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("fixture = %#v", got)
	}
}
