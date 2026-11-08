package canonstore

import (
	"bytes"
	"context"
	"errors"
	"os"
	"testing"

	"canonkeeper/backend/internal/governance"
	governancestore "canonkeeper/backend/internal/store/governance"

	"github.com/jackc/pgx/v5"
)

func TestArtifactSourceTransferNormalizesCurrentStaleAndDuplicateRefs(t *testing.T) {
	if os.Getenv("CANONKEEPER_TEST_DATABASE_URL") == "" {
		t.Skip("CANONKEEPER_TEST_DATABASE_URL is required")
	}
	db := openArtifactConstraintTestDB(t)
	locator := seedCanonicalSourceStoreFixture(t, db)
	ctx := context.Background()
	resolved, err := resolveChapterSourceForTransferTest(ctx, db, locator)
	if err != nil {
		t.Fatal(err)
	}
	view, err := governance.PresentSourceView(locator.SourceView)
	if err != nil {
		t.Fatal(err)
	}
	originalProjection := transferProjection(t, resolved.ExactRef)

	err = db.WithTx(ctx, func(tx pgx.Tx) error {
		transfer, err := db.NormalizeArtifactSourceTransferInTx(
			ctx, tx, locator.ProjectID, view,
			[]governance.ArtifactSourceRef{resolved.ExactRef, resolved.ExactRef},
		)
		if err != nil {
			return err
		}
		if !transfer.ContentAllowed || len(transfer.Refs) != 1 ||
			!bytes.Equal(transferProjection(t, transfer.Refs[0]), originalProjection) {
			t.Fatalf("current transfer = %#v", transfer)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(transferProjection(t, resolved.ExactRef), originalProjection) {
		t.Fatal("normalization mutated its input ref")
	}

	if _, err := db.Pool.Exec(ctx, `
		UPDATE chapter_source_members
		SET source_revision = source_revision + 1
		WHERE project_id=$1 AND source_view='draft' AND chapter_id=$2
	`, locator.ProjectID, locator.ChapterID); err != nil {
		t.Fatal(err)
	}
	err = db.WithTx(ctx, func(tx pgx.Tx) error {
		transfer, err := db.NormalizeArtifactSourceTransferInTx(
			ctx, tx, locator.ProjectID, view, []governance.ArtifactSourceRef{resolved.ExactRef},
		)
		if err != nil {
			return err
		}
		if !transfer.ContentAllowed || len(transfer.Refs) != 1 {
			t.Fatalf("stale-live transfer = %#v", transfer)
		}
		fields, err := transfer.Refs[0].Fields()
		if err != nil || fields.RefKind != governance.ArtifactSourceRefScope || fields.ScopeKind != "chapter" ||
			fields.ProjectID != locator.ProjectID || fields.BookID != locator.BookID || fields.ChapterID != locator.ChapterID ||
			fields.ProjectGeneration != 1 || fields.BookGeneration != 1 || fields.ChapterGeneration != 1 {
			t.Fatalf("stale-live normalized fields = (%#v, %v)", fields, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

const (
	transferErrorProjectID  = "11111111-1111-4111-8111-111111111111"
	transferErrorBookID     = "22222222-2222-4222-8222-222222222222"
	transferErrorOriginID   = "66666666-6666-4666-8666-666666666666"
	transferErrorProposalID = "77777777-7777-4777-8777-777777777777"
	transferErrorOwnerID    = "99999999-9999-4999-8999-999999999999"
)

func TestArtifactSourceTransferResolvedChapterDriftBecomesScope(t *testing.T) {
	view, err := governance.PresentSourceView(governance.SourceViewDraft)
	if err != nil {
		t.Fatal(err)
	}
	vector := governance.SourceGenerationVector{
		ProjectID: transferErrorProjectID, ProjectGeneration: 1,
		BookID: transferErrorBookID, BookGeneration: 1,
		ChapterID: transferErrorOriginID, ChapterGeneration: 1,
	}
	generation, err := governance.SourceGenerationDigest(vector)
	if err != nil {
		t.Fatal(err)
	}
	exact := func(revision uint64, content governance.Digest) governance.ArtifactSourceRef {
		ref, err := governance.NewExactChapterArtifactRef(governance.ChapterSourceRefInput{
			Source: vector, SourceRevision: revision, SourceView: view,
			SourceGenerationDigest: generation, ContentHash: content, Chat: governance.AbsentChatArm(),
		})
		if err != nil {
			t.Fatal(err)
		}
		return ref
	}
	original := exact(1, governance.Digest{1})
	current := exact(2, governance.Digest{2})
	fields, err := original.Fields()
	if err != nil {
		t.Fatal(err)
	}
	projection, err := governance.ArtifactSourceRefProjection(original)
	if err != nil {
		t.Fatal(err)
	}

	normalized, err := artifactSourceTransferNormalizeResolvedChapterRef(current, fields, projection)
	if err != nil {
		t.Fatal(err)
	}
	normalizedFields, err := normalized.Fields()
	if err != nil || normalizedFields.RefKind != governance.ArtifactSourceRefScope || normalizedFields.ScopeKind != "chapter" ||
		normalizedFields.ProjectID != vector.ProjectID || normalizedFields.BookID != vector.BookID ||
		normalizedFields.ChapterID != vector.ChapterID || normalizedFields.ChapterGeneration != vector.ChapterGeneration {
		t.Fatalf("normalized fields = (%#v, %v), want exact chapter scope", normalizedFields, err)
	}
}

func TestArtifactSourceTransferAcceptsCurrentChatLifecycle(t *testing.T) {
	if os.Getenv("CANONKEEPER_TEST_DATABASE_URL") == "" {
		t.Skip("CANONKEEPER_TEST_DATABASE_URL is required")
	}
	db := openArtifactConstraintTestDB(t)
	locator := seedCanonicalSourceStoreFixture(t, db)
	ctx := context.Background()
	chatID := "10000000-0000-4000-8000-000000000711"
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO chat_sessions (id, project_id, source_view, scope, title)
		VALUES ($1, $2, 'draft', 'project', 'Current chat lifecycle')
	`, chatID, locator.ProjectID); err != nil {
		t.Fatal(err)
	}
	view, err := governance.PresentSourceView(locator.SourceView)
	if err != nil {
		t.Fatal(err)
	}
	chat, err := governance.PresentChatArm(locator.ProjectID, chatID, 1)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := governance.NewViewBoundProjectRef(governance.SourceGenerationVector{
		ProjectID: locator.ProjectID, ProjectGeneration: 1,
	}, view, chat)
	if err != nil {
		t.Fatal(err)
	}

	err = db.WithTx(ctx, func(tx pgx.Tx) error {
		transfer, err := db.NormalizeArtifactSourceTransferInTx(
			ctx, tx, locator.ProjectID, view, []governance.ArtifactSourceRef{ref},
		)
		if err != nil {
			return err
		}
		if !transfer.ContentAllowed || len(transfer.Refs) != 1 {
			t.Fatalf("chat transfer = %#v", transfer)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestArtifactSourceTransferPreservesCurrentScopesAndSortsUnion(t *testing.T) {
	if os.Getenv("CANONKEEPER_TEST_DATABASE_URL") == "" {
		t.Skip("CANONKEEPER_TEST_DATABASE_URL is required")
	}
	db := openArtifactConstraintTestDB(t)
	locator := seedCanonicalSourceStoreFixture(t, db)
	ctx := context.Background()
	resolved, err := resolveChapterSourceForTransferTest(ctx, db, locator)
	if err != nil {
		t.Fatal(err)
	}
	fields, _ := resolved.ExactRef.Fields()
	view, _ := governance.PresentSourceView(locator.SourceView)
	chapterVector := governance.SourceGenerationVector{
		ProjectID: fields.ProjectID, ProjectGeneration: fields.ProjectGeneration,
		BookID: fields.BookID, BookGeneration: fields.BookGeneration,
		ChapterID: fields.ChapterID, ChapterGeneration: fields.ChapterGeneration,
	}
	bookScope, err := governance.NewBookArtifactRef(governance.SourceGenerationVector{
		ProjectID: fields.ProjectID, ProjectGeneration: fields.ProjectGeneration,
		BookID: fields.BookID, BookGeneration: fields.BookGeneration,
	}, view, governance.AbsentChatArm())
	if err != nil {
		t.Fatal(err)
	}
	chapterScope, err := governance.NewChapterArtifactRef(chapterVector, view, governance.AbsentChatArm())
	if err != nil {
		t.Fatal(err)
	}
	wantChapter := transferProjection(t, chapterScope)

	err = db.WithTx(ctx, func(tx pgx.Tx) error {
		transfer, err := db.NormalizeArtifactSourceTransferInTx(ctx, tx, locator.ProjectID, view,
			[]governance.ArtifactSourceRef{chapterScope, resolved.ExactRef, bookScope, chapterScope})
		if err != nil {
			return err
		}
		if !transfer.ContentAllowed || len(transfer.Refs) != 3 {
			t.Fatalf("scope union = %#v", transfer)
		}
		foundChapter := false
		for index, ref := range transfer.Refs {
			projection := transferProjection(t, ref)
			if index > 0 && bytes.Compare(transferProjection(t, transfer.Refs[index-1]), projection) >= 0 {
				t.Fatalf("refs are not strictly sorted: %#v", transfer.Refs)
			}
			foundChapter = foundChapter || bytes.Equal(projection, wantChapter)
		}
		if !foundChapter {
			t.Fatal("current chapter scope was not preserved")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestArtifactSourceTransferForbidsMissingDependencyAndRejectsForeignView(t *testing.T) {
	if os.Getenv("CANONKEEPER_TEST_DATABASE_URL") == "" {
		t.Skip("CANONKEEPER_TEST_DATABASE_URL is required")
	}
	db := openArtifactConstraintTestDB(t)
	locator := seedCanonicalSourceStoreFixture(t, db)
	ctx := context.Background()
	resolved, err := resolveChapterSourceForTransferTest(ctx, db, locator)
	if err != nil {
		t.Fatal(err)
	}
	fields, _ := resolved.ExactRef.Fields()
	view, _ := governance.PresentSourceView(locator.SourceView)
	missing, err := governance.NewChapterArtifactRef(governance.SourceGenerationVector{
		ProjectID: fields.ProjectID, ProjectGeneration: fields.ProjectGeneration,
		BookID: fields.BookID, BookGeneration: fields.BookGeneration,
		ChapterID: "10000000-0000-4000-8000-000000000099", ChapterGeneration: 1,
	}, view, governance.AbsentChatArm())
	if err != nil {
		t.Fatal(err)
	}
	err = db.WithTx(ctx, func(tx pgx.Tx) error {
		transfer, err := db.NormalizeArtifactSourceTransferInTx(
			ctx, tx, locator.ProjectID, view, []governance.ArtifactSourceRef{missing},
		)
		if err != nil {
			return err
		}
		if transfer.ContentAllowed || len(transfer.Refs) != 0 {
			t.Fatalf("missing dependency transfer = %#v", transfer)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	published, _ := governance.PresentSourceView(governance.SourceViewPublished)
	err = db.WithTx(ctx, func(tx pgx.Tx) error {
		_, err := db.NormalizeArtifactSourceTransferInTx(
			ctx, tx, locator.ProjectID, published, []governance.ArtifactSourceRef{resolved.ExactRef},
		)
		return err
	})
	if !errors.Is(err, governancestore.ErrArtifactSourcesValidation) {
		t.Fatalf("foreign view error = %v", err)
	}
}

func TestArtifactSourceTransferFailsClosedForInvalidLifecycleAndFencedOwner(t *testing.T) {
	if os.Getenv("CANONKEEPER_TEST_DATABASE_URL") == "" {
		t.Skip("CANONKEEPER_TEST_DATABASE_URL is required")
	}
	db := openArtifactConstraintTestDB(t)
	locator := seedCanonicalSourceStoreFixture(t, db)
	ctx := context.Background()
	view, _ := governance.PresentSourceView(locator.SourceView)
	badScope, err := governance.NewChapterArtifactRef(governance.SourceGenerationVector{
		ProjectID: locator.ProjectID, ProjectGeneration: 2,
		BookID: locator.BookID, BookGeneration: 1,
		ChapterID: locator.ChapterID, ChapterGeneration: 1,
	}, view, governance.AbsentChatArm())
	if err != nil {
		t.Fatal(err)
	}
	err = db.WithTx(ctx, func(tx pgx.Tx) error {
		_, err := db.NormalizeArtifactSourceTransferInTx(
			ctx, tx, locator.ProjectID, view, []governance.ArtifactSourceRef{badScope},
		)
		return err
	})
	if !errors.Is(err, governancestore.ErrArtifactSourcesConflict) {
		t.Fatalf("lifecycle mismatch error = %v", err)
	}
	err = db.WithTx(ctx, func(tx pgx.Tx) error {
		_, err := db.NormalizeArtifactSourceTransferInTx(
			ctx, tx, "10000000-0000-4000-8000-000000000099", view, []governance.ArtifactSourceRef{badScope},
		)
		return err
	})
	if !errors.Is(err, governancestore.ErrArtifactSourcesValidation) {
		t.Fatalf("foreign project error = %v", err)
	}
}

func resolveChapterSourceForTransferTest(
	ctx context.Context,
	db *Store,
	locator governance.CanonicalChapterSourceLocatorV1,
) (ResolvedChapterSource, error) {
	var resolved ResolvedChapterSource
	err := db.WithTx(ctx, func(tx pgx.Tx) error {
		var err error
		resolved, err = db.ResolveCanonicalChapterSourceInTx(
			ctx, tx, locator.ProjectID, locator.SourceView, locator.BookID, locator.ChapterID,
		)
		return err
	})
	return resolved, err
}

func transferProjection(t *testing.T, ref governance.ArtifactSourceRef) []byte {
	t.Helper()
	projection, err := governance.ArtifactSourceRefProjection(ref)
	if err != nil {
		t.Fatal(err)
	}
	return projection
}
