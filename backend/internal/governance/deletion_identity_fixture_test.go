package governance

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"
	"time"
)

func TestDeletionIdentityGoldenFixtureMatchesGoEncoder(t *testing.T) {
	raw, err := os.ReadFile("testdata/deletion_identity_v1_golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture deletionIdentityFixture
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&fixture); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if fixture.Schema != "CKDeletionIdentityFixtureV1" {
		t.Fatalf("schema = %q", fixture.Schema)
	}

	selectedByName := make(map[string]DeletionSelectedVector)
	selectedDigestByName := make(map[string][32]byte)
	for _, row := range fixture.SelectedVectors {
		selection := row.selection(t)
		projection, err := SelectedVectorProjection(selection)
		if err != nil {
			t.Fatal(err)
		}
		digest, err := SelectedVectorDigest(selection)
		if err != nil {
			t.Fatal(err)
		}
		assertFixtureProjection(t, row.Name, projection, digest, row.ProjectionHex, row.ExpectedSHA256)
		selectedByName[row.Name] = selection
		selectedDigestByName[row.Name] = digest
	}

	tombstoneDigestByName := make(map[string][32]byte)
	for _, row := range fixture.Tombstones {
		selection, ok := selectedByName[row.Input.SelectedVector]
		if !ok {
			t.Fatalf("unknown selected vector %q", row.Input.SelectedVector)
		}
		input := DeletionTombstoneInput{
			TombstoneID: row.Input.TombstoneID, Origin: DeletionOrigin(row.Input.Origin), Mode: PurgeMode(row.Input.Mode),
			SelectedVector: selection, FencedAt: parseFixtureTime(t, row.Input.FencedAt), Deadline: AbsentPurgeDeadline(),
		}
		if row.Input.Deadline != nil {
			input.Deadline = mustPurgeDeadline(t, parseFixtureTime(t, *row.Input.Deadline))
		}
		projection, err := DeletionTombstoneProjection(input)
		if err != nil {
			t.Fatal(err)
		}
		digest, err := DeletionTombstoneDigest(input)
		if err != nil {
			t.Fatal(err)
		}
		assertFixtureProjection(t, row.Name, projection, digest, row.ProjectionHex, row.ExpectedSHA256)
		tombstoneDigestByName[row.Name] = digest
	}

	receiptByName := make(map[string]DeletionTargetReceiptRow)
	receiptInputByName := make(map[string]deletionReceiptFixtureInput)
	for _, row := range fixture.Receipts {
		input := DeletionTargetReceiptInput{
			Target: DeletionTarget(row.Input.Target), TombstoneDigest: tombstoneDigestByName[row.Input.Tombstone],
			SelectedVectorDigest: selectedDigestByName[row.Input.SelectedVector], HandlerVersion: DeletionHandlerVersion(row.Input.HandlerVersion),
			Disposition: ReceiptDisposition(row.Input.Disposition),
			CompletedAt: parseFixtureTime(t, row.Input.CompletedAt),
		}
		projection, err := DeletionTargetReceiptProjection(input)
		if err != nil {
			t.Fatal(err)
		}
		digest, err := DeletionTargetReceiptDigest(input)
		if err != nil {
			t.Fatal(err)
		}
		assertFixtureProjection(t, row.Name, projection, digest, row.ProjectionHex, row.ExpectedSHA256)
		receiptByName[row.Name] = DeletionTargetReceiptRow{Target: input.Target, Digest: digest}
		receiptInputByName[row.Name] = row.Input
	}

	for _, row := range fixture.ReceiptSets {
		receipts := make([]DeletionTargetReceiptRow, 0, len(row.Input.Receipts))
		for _, name := range row.Input.Receipts {
			receipts = append(receipts, receiptByName[name])
			input := receiptInputByName[name]
			if input.Tombstone != row.Input.Tombstone || input.SelectedVector != row.Input.SelectedVector {
				t.Fatalf("receipt %q does not belong to set %q", name, row.Name)
			}
		}
		projection, err := DeletionTargetReceiptsProjection(PurgeMode(row.Input.Mode), tombstoneDigestByName[row.Input.Tombstone], selectedDigestByName[row.Input.SelectedVector], receipts)
		if err != nil {
			t.Fatal(err)
		}
		digest, err := DeletionTargetReceiptsDigest(PurgeMode(row.Input.Mode), tombstoneDigestByName[row.Input.Tombstone], selectedDigestByName[row.Input.SelectedVector], receipts)
		if err != nil {
			t.Fatal(err)
		}
		assertFixtureProjection(t, row.Name, projection, digest, row.ProjectionHex, row.ExpectedSHA256)
	}

	for _, row := range fixture.PredecessorSets {
		predecessors := make([]DeletionPredecessor, len(row.Input.Rows))
		for index, input := range row.Input.Rows {
			selection, ok := selectedByName[input.SelectedVector]
			if !ok {
				t.Fatalf("unknown predecessor selected vector %q", input.SelectedVector)
			}
			deadline := AbsentPurgeDeadline()
			if input.Deadline != nil {
				deadline = mustPurgeDeadline(t, parseFixtureTime(t, *input.Deadline))
			}
			predecessors[index] = DeletionPredecessor{
				Tombstone: DeletionTombstoneInput{
					TombstoneID: input.TombstoneID, Origin: DeletionOrigin(input.Origin), Mode: PurgeMode(input.Mode),
					SelectedVector: selection, FencedAt: parseFixtureTime(t, input.FencedAt), Deadline: deadline,
				},
				Status:               DeletionPredecessorStatus(input.Status),
				ReceiptVectorDigest:  fixtureDigest(t, input.ReceiptVectorDigest),
				TargetReceiptsDigest: fixtureDigest(t, input.TargetReceiptsDigest),
			}
		}
		hardSelection, ok := selectedByName[row.Input.HardPurgeSelectedVector]
		if !ok {
			t.Fatalf("unknown hard-purge selected vector %q", row.Input.HardPurgeSelectedVector)
		}
		projection, err := DeletionPredecessorSetProjection(hardSelection, predecessors)
		if err != nil {
			t.Fatal(err)
		}
		digest, err := DeletionPredecessorSetDigest(hardSelection, predecessors)
		if err != nil {
			t.Fatal(err)
		}
		assertFixtureProjection(t, row.Name, projection, digest, row.ProjectionHex, row.ExpectedSHA256)
	}

	if len(selectedByName) != 4 || len(fixture.Receipts) != 3 || len(fixture.ReceiptSets) != 1 || len(fixture.NegativeCases) != 4 {
		t.Fatal("deletion fixture coverage is incomplete")
	}
}

type deletionIdentityFixture struct {
	Schema          string                             `json:"schema"`
	SelectedVectors []deletionSelectedFixtureRow       `json:"selectedVectors"`
	Tombstones      []deletionTombstoneFixtureRow      `json:"tombstones"`
	Receipts        []deletionReceiptFixtureRow        `json:"receipts"`
	ReceiptSets     []deletionReceiptSetFixtureRow     `json:"receiptSets"`
	PredecessorSets []deletionPredecessorSetFixtureRow `json:"predecessorSets"`
	NegativeCases   []struct {
		Rule string `json:"rule"`
	} `json:"negativeCases"`
}

type deletionExpectedFixtureRow struct {
	Name           string `json:"name"`
	ProjectionHex  string `json:"projectionHex"`
	ExpectedSHA256 string `json:"expectedSha256"`
}

type deletionSourceFixture struct {
	ProjectID         string `json:"projectId"`
	ProjectGeneration uint64 `json:"projectGeneration"`
	BookID            string `json:"bookId"`
	BookGeneration    uint64 `json:"bookGeneration"`
	ChapterID         string `json:"chapterId"`
	ChapterGeneration uint64 `json:"chapterGeneration"`
}

func (source deletionSourceFixture) vector() SourceGenerationVector {
	return SourceGenerationVector{
		ProjectID: source.ProjectID, ProjectGeneration: LifecycleGeneration(source.ProjectGeneration),
		BookID: source.BookID, BookGeneration: LifecycleGeneration(source.BookGeneration),
		ChapterID: source.ChapterID, ChapterGeneration: LifecycleGeneration(source.ChapterGeneration),
	}
}

type deletionSelectedFixtureRow struct {
	deletionExpectedFixtureRow
	Input struct {
		Arm       string                `json:"arm"`
		Source    deletionSourceFixture `json:"source"`
		ProjectID string                `json:"projectId"`
		ChatID    string                `json:"chatId"`
		ChatEpoch uint64                `json:"chatEpoch"`
	} `json:"input"`
}

func (input deletionSelectedFixtureRow) selection(t *testing.T) DeletionSelectedVector {
	t.Helper()
	switch input.Input.Arm {
	case "project":
		return mustSelectedVector(t, func() (DeletionSelectedVector, error) { return ProjectDeletionVector(input.Input.Source.vector()) })
	case "book":
		return mustSelectedVector(t, func() (DeletionSelectedVector, error) { return BookDeletionVector(input.Input.Source.vector()) })
	case "chapter":
		return mustSelectedVector(t, func() (DeletionSelectedVector, error) { return ChapterDeletionVector(input.Input.Source.vector()) })
	case "chat":
		return mustSelectedVector(t, func() (DeletionSelectedVector, error) {
			return ChatDeletionVector(input.Input.ProjectID, input.Input.ChatID, ChatLifecycleEpoch(input.Input.ChatEpoch))
		})
	default:
		t.Fatalf("unknown selected arm %q", input.Input.Arm)
		return DeletionSelectedVector{}
	}
}

type deletionTombstoneFixtureRow struct {
	deletionExpectedFixtureRow
	Input struct {
		TombstoneID    string  `json:"tombstoneId"`
		Origin         string  `json:"origin"`
		Mode           string  `json:"mode"`
		SelectedVector string  `json:"selectedVector"`
		FencedAt       string  `json:"fencedAt"`
		Deadline       *string `json:"deadline"`
	} `json:"input"`
}

type deletionReceiptFixtureInput struct {
	Target         string `json:"target"`
	Tombstone      string `json:"tombstone"`
	SelectedVector string `json:"selectedVector"`
	HandlerVersion string `json:"handlerVersion"`
	Disposition    string `json:"disposition"`
	CompletedAt    string `json:"completedAt"`
}

type deletionReceiptFixtureRow struct {
	deletionExpectedFixtureRow
	Input deletionReceiptFixtureInput `json:"input"`
}
type deletionReceiptSetFixtureRow struct {
	deletionExpectedFixtureRow
	Input struct {
		Mode           string   `json:"mode"`
		Tombstone      string   `json:"tombstone"`
		SelectedVector string   `json:"selectedVector"`
		Receipts       []string `json:"receipts"`
	} `json:"input"`
}
type deletionPredecessorFixtureInput struct {
	TombstoneID          string  `json:"tombstoneId"`
	Origin               string  `json:"origin"`
	Mode                 string  `json:"mode"`
	SelectedVector       string  `json:"selectedVector"`
	FencedAt             string  `json:"fencedAt"`
	Deadline             *string `json:"deadline"`
	Status               string  `json:"status"`
	ReceiptVectorDigest  string  `json:"receiptVectorDigest"`
	TargetReceiptsDigest string  `json:"targetReceiptsDigest"`
}
type deletionPredecessorSetFixtureRow struct {
	deletionExpectedFixtureRow
	Input struct {
		HardPurgeSelectedVector string                            `json:"hardPurgeSelectedVector"`
		Rows                    []deletionPredecessorFixtureInput `json:"rows"`
	} `json:"input"`
}

func assertFixtureProjection(t *testing.T, name string, projection []byte, digest [32]byte, projectionHex, expectedSHA256 string) {
	t.Helper()
	if hex.EncodeToString(projection) != projectionHex || hex.EncodeToString(digest[:]) != expectedSHA256 {
		t.Fatalf("fixture %q mismatch: projection=%x digest=%x", name, projection, digest)
	}
}

func fixtureDigest(t *testing.T, value string) [32]byte {
	t.Helper()
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != 32 {
		t.Fatalf("invalid fixture digest %q", value)
	}
	var digest [32]byte
	copy(digest[:], decoded)
	return digest
}

func parseFixtureTime(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		t.Fatalf("invalid fixture timestamp %q: %v", value, err)
	}
	return parsed
}
