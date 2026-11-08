package governance

import (
	"bytes"
	"crypto/sha256"
	"testing"
	"time"
)

func TestDeletionIdentitySelectedVectorArmsAreByteExact(t *testing.T) {
	projectVector, bookVector, chapterVector := deletionTestVectors()
	project := mustSelectedVector(t, func() (DeletionSelectedVector, error) { return ProjectDeletionVector(projectVector) })
	book := mustSelectedVector(t, func() (DeletionSelectedVector, error) { return BookDeletionVector(bookVector) })
	chapter := mustSelectedVector(t, func() (DeletionSelectedVector, error) { return ChapterDeletionVector(chapterVector) })
	chat := mustSelectedVector(t, func() (DeletionSelectedVector, error) {
		return ChatDeletionVector(projectVector.ProjectID, deletionChatID, 9)
	})

	tests := []struct {
		name      string
		selection DeletionSelectedVector
		tag       byte
		source    SourceGenerationVector
	}{
		{name: "project", selection: project, tag: 1, source: projectVector},
		{name: "book", selection: book, tag: 2, source: bookVector},
		{name: "chapter", selection: chapter, tag: 3, source: chapterVector},
		{name: "chat", selection: chat, tag: 4},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			projection, err := SelectedVectorProjection(tt.selection)
			if err != nil {
				t.Fatalf("SelectedVectorProjection() error = %v", err)
			}
			if projection[0] != tt.tag {
				t.Fatalf("tag = %d", projection[0])
			}
			if tt.tag < 4 {
				sourceDigest := mustSourceDigest(t, tt.source)
				if !bytes.Equal(projection[1:], sourceDigest[:]) {
					t.Fatalf("source arm = %x, want %x", projection[1:], sourceDigest)
				}
			}
			got, err := SelectedVectorDigest(tt.selection)
			if err != nil {
				t.Fatalf("SelectedVectorDigest() error = %v", err)
			}
			want := hashTestPreimage("CKDeletionSelectedVectorV1\x00", projection)
			if got != want {
				t.Fatalf("digest = %x, want %x", got, want)
			}
		})
	}

	if _, err := ProjectDeletionVector(bookVector); err == nil {
		t.Fatal("book vector accepted as project arm")
	}
	if _, err := BookDeletionVector(chapterVector); err == nil {
		t.Fatal("chapter vector accepted as book arm")
	}
	if _, err := ChatDeletionVector(projectVector.ProjectID, deletionChatID, 0); err == nil {
		t.Fatal("zero chat epoch accepted")
	}
}

func TestDeletionIdentityTombstoneOriginModeAndDeadlineAreClosed(t *testing.T) {
	projectVector, _, _ := deletionTestVectors()
	sourceSelection := mustSelectedVector(t, func() (DeletionSelectedVector, error) { return ProjectDeletionVector(projectVector) })
	chatSelection := mustSelectedVector(t, func() (DeletionSelectedVector, error) {
		return ChatDeletionVector(projectVector.ProjectID, deletionChatID, 9)
	})
	fencedAt := time.Date(2026, 7, 12, 2, 3, 4, 567000000, time.UTC)
	deadline := mustPurgeDeadline(t, time.Date(2026, 8, 11, 2, 3, 4, 567000000, time.UTC))

	valid := []DeletionTombstoneInput{
		{TombstoneID: deletionTombstoneID, Origin: DeletionOriginGovernedOnline, Mode: PurgeModeDerivedGeneration, SelectedVector: sourceSelection, FencedAt: fencedAt, Deadline: deadline},
		{TombstoneID: deletionTombstoneID, Origin: DeletionOriginGovernedOnline, Mode: PurgeModeSourceHardPurge, SelectedVector: sourceSelection, FencedAt: fencedAt, Deadline: deadline},
		{TombstoneID: deletionTombstoneID, Origin: DeletionOriginGovernedOnline, Mode: PurgeModeChatEpoch, SelectedVector: chatSelection, FencedAt: fencedAt, Deadline: deadline},
	}
	for _, input := range valid {
		projection, err := DeletionTombstoneProjection(input)
		if err != nil {
			t.Fatalf("DeletionTombstoneProjection(%#v) error = %v", input, err)
		}
		got, err := DeletionTombstoneDigest(input)
		if err != nil {
			t.Fatal(err)
		}
		if want := hashTestPreimage("CKDeletionTombstoneV1\x00", projection); got != want {
			t.Fatalf("digest = %x, want %x", got, want)
		}
	}

	invalid := []DeletionTombstoneInput{
		{TombstoneID: deletionTombstoneID, Origin: DeletionOrigin("unknown"), Mode: PurgeModeDerivedGeneration, SelectedVector: sourceSelection, FencedAt: fencedAt, Deadline: deadline},
		{TombstoneID: deletionTombstoneID, Origin: DeletionOriginGovernedOnline, Mode: PurgeModeDerivedGeneration, SelectedVector: sourceSelection, FencedAt: fencedAt, Deadline: AbsentPurgeDeadline()},
		{TombstoneID: deletionTombstoneID, Origin: DeletionOriginGovernedOnline, Mode: PurgeModeChatEpoch, SelectedVector: sourceSelection, FencedAt: fencedAt, Deadline: deadline},
		{TombstoneID: deletionTombstoneID, Origin: DeletionOriginGovernedOnline, Mode: PurgeModeDerivedGeneration, SelectedVector: chatSelection, FencedAt: fencedAt, Deadline: deadline},
		{TombstoneID: deletionTombstoneID, Origin: DeletionOriginGovernedOnline, Mode: PurgeModeSourceHardPurge, SelectedVector: chatSelection, FencedAt: fencedAt, Deadline: deadline},
	}
	for _, input := range invalid {
		if _, err := DeletionTombstoneDigest(input); err == nil {
			t.Fatalf("DeletionTombstoneDigest(%#v) error = nil", input)
		}
	}

	nonWholeMicros := valid[0]
	nonWholeMicros.FencedAt = nonWholeMicros.FencedAt.Add(time.Nanosecond)
	if _, err := DeletionTombstoneDigest(nonWholeMicros); err == nil {
		t.Fatal("non-whole microsecond accepted")
	}
}

func TestPermissionLossActorBindsCanonicalTombstoneDigest(t *testing.T) {
	projectVector, _, _ := deletionTestVectors()
	selection := mustSelectedVector(t, func() (DeletionSelectedVector, error) {
		return ProjectDeletionVector(projectVector)
	})
	fencedAt := time.Date(2026, 7, 12, 2, 3, 4, 567000000, time.UTC)
	deadline := mustPurgeDeadline(t, fencedAt.Add(24*time.Hour))
	ordinary := DeletionTombstoneInput{
		TombstoneID: deletionTombstoneID, Origin: DeletionOriginGovernedOnline,
		Mode: PurgeModeDerivedGeneration, SelectedVector: selection,
		FencedAt: fencedAt, Deadline: deadline,
	}
	ordinaryDigest, err := DeletionTombstoneDigest(ordinary)
	if err != nil {
		t.Fatal(err)
	}
	permissionLoss := ordinary
	permissionLoss.PermissionLossActorUserID = "44444444-5555-4666-8777-888888888888"
	permissionDigest, err := DeletionTombstoneDigest(permissionLoss)
	if err != nil {
		t.Fatal(err)
	}
	if permissionDigest == ordinaryDigest {
		t.Fatal("permission-loss actor was absent from tombstone digest")
	}
	otherActor := permissionLoss
	otherActor.PermissionLossActorUserID = "55555555-6666-4777-8888-999999999999"
	otherDigest, err := DeletionTombstoneDigest(otherActor)
	if err != nil || otherDigest == permissionDigest {
		t.Fatalf("other actor digest = %x, %v", otherDigest, err)
	}
	invalid := permissionLoss
	invalid.PermissionLossActorUserID = "not-a-uuid"
	if _, err := DeletionTombstoneDigest(invalid); err == nil {
		t.Fatal("noncanonical permission-loss actor accepted")
	}
	invalid = permissionLoss
	invalid.Mode = PurgeModeSourceHardPurge
	if _, err := DeletionTombstoneDigest(invalid); err == nil {
		t.Fatal("permission-loss actor accepted outside derived project deletion")
	}
}

func TestDeletionReceiptIdentityAndOrderedAggregate(t *testing.T) {
	completedAt := time.Date(2026, 7, 12, 3, 4, 5, 0, time.UTC)
	tombstoneDigest := digestByte(30)
	vectorDigest := digestByte(31)
	receipts := []DeletionTargetReceiptInput{
		{Target: TargetRuntime, TombstoneDigest: tombstoneDigest, SelectedVectorDigest: vectorDigest, HandlerVersion: HandlerRuntimeDirectSDKV1, Disposition: ReceiptPurged, CompletedAt: completedAt},
		{Target: TargetFilesystem, TombstoneDigest: tombstoneDigest, SelectedVectorDigest: vectorDigest, HandlerVersion: HandlerFilesystemOSRootV1, Disposition: ReceiptPurged, CompletedAt: completedAt},
		{Target: TargetPostgreSQL, TombstoneDigest: tombstoneDigest, SelectedVectorDigest: vectorDigest, HandlerVersion: HandlerPostgresExactInventoryV1, Disposition: ReceiptPurged, CompletedAt: completedAt},
	}
	rows := make([]DeletionTargetReceiptRow, 0, len(receipts))
	for _, input := range receipts {
		projection, err := DeletionTargetReceiptProjection(input)
		if err != nil {
			t.Fatalf("DeletionTargetReceiptProjection() error = %v", err)
		}
		digest, err := DeletionTargetReceiptDigest(input)
		if err != nil {
			t.Fatal(err)
		}
		if want := hashTestPreimage("CKDeletionTargetReceiptV1\x00", projection); digest != want {
			t.Fatalf("receipt digest = %x, want %x", digest, want)
		}
		rows = append(rows, DeletionTargetReceiptRow{Target: input.Target, Digest: digest})
	}

	projection, err := DeletionTargetReceiptsProjection(PurgeModeDerivedGeneration, tombstoneDigest, vectorDigest, rows)
	if err != nil {
		t.Fatalf("DeletionTargetReceiptsProjection() error = %v", err)
	}
	digest, err := DeletionTargetReceiptsDigest(PurgeModeDerivedGeneration, tombstoneDigest, vectorDigest, rows)
	if err != nil {
		t.Fatal(err)
	}
	if want := hashTestPreimage("CKDeletionTargetReceiptSetV1\x00", projection); digest != want {
		t.Fatalf("receipt set digest = %x, want %x", digest, want)
	}

	invalidOrder := append([]DeletionTargetReceiptRow(nil), rows...)
	invalidOrder[0], invalidOrder[1] = invalidOrder[1], invalidOrder[0]
	if _, err := DeletionTargetReceiptsDigest(PurgeModeDerivedGeneration, tombstoneDigest, vectorDigest, invalidOrder); err == nil {
		t.Fatal("reordered target receipts accepted")
	}
	if _, err := DeletionTargetReceiptsDigest(PurgeModeDerivedGeneration, tombstoneDigest, vectorDigest, rows[:2]); err == nil {
		t.Fatal("missing target receipt accepted")
	}

	wrongHandler := receipts[0]
	wrongHandler.HandlerVersion = HandlerFilesystemOSRootV1
	if _, err := DeletionTargetReceiptDigest(wrongHandler); err == nil {
		t.Fatal("inapplicable handler version accepted")
	}
	noDataRuntime := receipts[0]
	noDataRuntime.Disposition = ReceiptNoData
	if _, err := DeletionTargetReceiptDigest(noDataRuntime); err != nil {
		t.Fatalf("runtime no-data receipt rejected: %v", err)
	}
}

func TestDeletionTargetReceiptSetUsesExactTargetsForEveryMode(t *testing.T) {
	tombstoneDigest := digestByte(40)
	vectorDigest := digestByte(41)
	rows := []DeletionTargetReceiptRow{
		{Target: TargetRuntime, Digest: digestByte(42)},
		{Target: TargetFilesystem, Digest: digestByte(43)},
		{Target: TargetPostgreSQL, Digest: digestByte(45)},
	}

	for _, mode := range []PurgeMode{PurgeModeDerivedGeneration, PurgeModeSourceHardPurge, PurgeModeChatEpoch} {
		projection, err := DeletionTargetReceiptsProjection(mode, tombstoneDigest, vectorDigest, rows)
		if err != nil {
			t.Fatalf("%s target receipts rejected: %v", mode, err)
		}
		lastTagOffset := 68 + (len(rows)-1)*33
		if projection[lastTagOffset] != 4 {
			t.Fatalf("%s last target tag = %d, want PostgreSQL tag 4", mode, projection[lastTagOffset])
		}
	}
	if tag, ok := TargetPostgreSQL.tag(); !ok || tag != 4 {
		t.Fatalf("PostgreSQL tag = %d, %v, want 4, true", tag, ok)
	}

	for name, rows := range map[string][]DeletionTargetReceiptRow{
		"reordered":  {rows[1], rows[0], rows[2]},
		"duplicated": {rows[0], rows[1], rows[0]},
		"missing":    rows[:2],
		"extra":      append(append([]DeletionTargetReceiptRow(nil), rows...), rows[0]),
	} {
		for _, mode := range []PurgeMode{PurgeModeDerivedGeneration, PurgeModeSourceHardPurge, PurgeModeChatEpoch} {
			if _, err := DeletionTargetReceiptsDigest(mode, tombstoneDigest, vectorDigest, rows); err == nil {
				t.Fatalf("%s target receipts accepted %s targets", mode, name)
			}
		}
	}
}

func TestDeletionIdentityPredecessorSetIsCompleteTypedAndOrdered(t *testing.T) {
	projectVector, bookVector, chapterVector := deletionTestVectors()
	hardSelection := mustSelectedVector(t, func() (DeletionSelectedVector, error) {
		return ProjectDeletionVector(projectVector)
	})
	bookSelection := mustSelectedVector(t, func() (DeletionSelectedVector, error) {
		return BookDeletionVector(bookVector)
	})
	chapterSelection := mustSelectedVector(t, func() (DeletionSelectedVector, error) {
		return ChapterDeletionVector(chapterVector)
	})
	bookDigest := mustSelectedVectorDigest(t, bookSelection)
	chapterDigest := mustSelectedVectorDigest(t, chapterSelection)
	fencedAt := time.Date(2026, 7, 12, 2, 3, 4, 567_000_000, time.UTC)
	deadline := mustPurgeDeadline(t, time.Date(2026, 8, 11, 2, 3, 4, 567_000_000, time.UTC))
	rows := []DeletionPredecessor{
		{
			Tombstone: DeletionTombstoneInput{
				TombstoneID: "10000000-0000-0000-0000-000000000001", Origin: DeletionOriginGovernedOnline,
				Mode: PurgeModeDerivedGeneration, SelectedVector: bookSelection, FencedAt: fencedAt,
				Deadline: deadline,
			},
			Status:              DeletionPredecessorPurged,
			ReceiptVectorDigest: bookDigest, TargetReceiptsDigest: digestByte(43),
		},
		{
			Tombstone: DeletionTombstoneInput{
				TombstoneID: "20000000-0000-0000-0000-000000000002", Origin: DeletionOriginGovernedOnline,
				Mode: PurgeModeDerivedGeneration, SelectedVector: chapterSelection, FencedAt: fencedAt,
				Deadline: deadline,
			},
			Status:              DeletionPredecessorPurged,
			ReceiptVectorDigest: chapterDigest, TargetReceiptsDigest: digestByte(46),
		},
	}
	projection, err := DeletionPredecessorSetProjection(hardSelection, rows)
	if err != nil {
		t.Fatalf("DeletionPredecessorSetProjection() error = %v", err)
	}
	digest, err := DeletionPredecessorSetDigest(hardSelection, rows)
	if err != nil {
		t.Fatal(err)
	}
	if want := hashTestPreimage("CKDeletionPredecessorSetV1\x00", projection); digest != want {
		t.Fatalf("digest = %x, want %x", digest, want)
	}

	mutations := []func([]DeletionPredecessor) []DeletionPredecessor{
		func(_ []DeletionPredecessor) []DeletionPredecessor { return nil },
		func(rows []DeletionPredecessor) []DeletionPredecessor {
			rows[0], rows[1] = rows[1], rows[0]
			return rows
		},
		func(rows []DeletionPredecessor) []DeletionPredecessor {
			rows[1].Tombstone.TombstoneID = rows[0].Tombstone.TombstoneID
			return rows
		},
		func(rows []DeletionPredecessor) []DeletionPredecessor {
			rows[0].Tombstone.Mode = PurgeModeSourceHardPurge
			return rows
		},
		func(rows []DeletionPredecessor) []DeletionPredecessor {
			rows[0].Status = DeletionPredecessorStatus("processing")
			return rows
		},
		func(rows []DeletionPredecessor) []DeletionPredecessor {
			rows[0].ReceiptVectorDigest = digestByte(99)
			return rows
		},
	}
	for _, mutate := range mutations {
		candidate := append([]DeletionPredecessor(nil), rows...)
		if _, err := DeletionPredecessorSetDigest(hardSelection, mutate(candidate)); err == nil {
			t.Fatal("invalid predecessor set accepted")
		}
	}

	foreignVector := SourceGenerationVector{ProjectID: "90112233-4455-6677-8899-aabbccddeeff", ProjectGeneration: 1}
	foreignSelection := mustSelectedVector(t, func() (DeletionSelectedVector, error) {
		return ProjectDeletionVector(foreignVector)
	})
	foreignDigest := mustSelectedVectorDigest(t, foreignSelection)
	foreignRows := append([]DeletionPredecessor(nil), rows...)
	foreignRows[0].Tombstone.SelectedVector = foreignSelection
	foreignRows[0].ReceiptVectorDigest = foreignDigest
	if _, err := DeletionPredecessorSetDigest(hardSelection, foreignRows); err == nil {
		t.Fatal("foreign nonintersecting predecessor accepted")
	}
}

const (
	deletionProjectID   = "00112233-4455-6677-8899-aabbccddeeff"
	deletionBookID      = "11111111-2222-3333-4444-555555555555"
	deletionChapterID   = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	deletionChatID      = "22222222-3333-4444-5555-666666666666"
	deletionTombstoneID = "33333333-4444-5555-6666-777777777777"
)

func deletionTestVectors() (SourceGenerationVector, SourceGenerationVector, SourceGenerationVector) {
	project := SourceGenerationVector{ProjectID: deletionProjectID, ProjectGeneration: 1}
	book := SourceGenerationVector{ProjectID: deletionProjectID, ProjectGeneration: 1, BookID: deletionBookID, BookGeneration: 2}
	chapter := SourceGenerationVector{ProjectID: deletionProjectID, ProjectGeneration: 1, BookID: deletionBookID, BookGeneration: 2, ChapterID: deletionChapterID, ChapterGeneration: 3}
	return project, book, chapter
}

func mustSelectedVector(t *testing.T, build func() (DeletionSelectedVector, error)) DeletionSelectedVector {
	t.Helper()
	selection, err := build()
	if err != nil {
		t.Fatal(err)
	}
	return selection
}

func mustSelectedVectorDigest(t *testing.T, selection DeletionSelectedVector) [32]byte {
	t.Helper()
	digest, err := SelectedVectorDigest(selection)
	if err != nil {
		t.Fatal(err)
	}
	return digest
}

func mustPurgeDeadline(t *testing.T, value time.Time) PurgeDeadlineArm {
	t.Helper()
	deadline, err := PresentPurgeDeadline(value)
	if err != nil {
		t.Fatal(err)
	}
	return deadline
}

func hashTestPreimage(domain string, projection []byte) [32]byte {
	return sha256.Sum256(append(append([]byte{}, domain...), projection...))
}

func digestByte(value byte) [32]byte {
	var digest [32]byte
	digest[0] = value
	return digest
}
