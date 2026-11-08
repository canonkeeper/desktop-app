package deletion

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	deletiondomain "canonkeeper/backend/internal/domain/deletion"
	"canonkeeper/backend/internal/governance"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestTombstoneSourceDeletedModelJobsHandsExactVectorBoundJobsToTombstoner(t *testing.T) {
	const (
		projectID = "10000000-0000-4000-8000-000000000001"
		bookID    = "20000000-0000-4000-8000-000000000002"
		firstJob  = "30000000-0000-4000-8000-000000000003"
		secondJob = "30000000-0000-4000-8000-000000000004"
	)
	selection, err := postgresDeletionSelectionForRequest(
		sourceDeletionTargetRequestForTest(t, governance.DeletionSelectionBook, projectID, bookID, ""),
	)
	if err != nil {
		t.Fatal(err)
	}
	tx := &modelTombstoneTx{rows: &modelTombstoneRows{ids: []pgtype.UUID{
		mustUUID(firstJob), mustUUID(secondJob),
	}}}
	tombstoner := &recordingModelJobTombstoner{}

	if err := tombstoneSourceDeletedModelJobsInTx(t.Context(), tx, selection, tombstoner); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(tx.query, postgresOwnerSelectionPredicate("owner")) ||
		!strings.Contains(tx.query, "owner.artifact_kind = 'job'") ||
		!strings.Contains(tx.query, "JOIN job_model_bindings AS binding") ||
		!strings.Contains(tx.query, "binding.job_id = job.id") ||
		!strings.Contains(tx.query, "ORDER BY job.id") {
		t.Fatalf("bound model job selection is not the exact reject vector: %s", tx.query)
	}
	if !strings.Contains(tx.query, "ref.book_id = $4::uuid AND ref.book_generation = $5::bigint") {
		t.Fatalf("bound model job selection lost the exact book vector: %s", tx.query)
	}
	if len(tx.args) != 9 || tx.args[0] != projectID || tx.args[1] != "book" || tx.args[3] != bookID {
		t.Fatalf("selection args = %#v", tx.args)
	}
	if len(tombstoner.calls) != 2 ||
		tombstoner.calls[0].projectID != projectID || tombstoner.calls[0].jobID != firstJob ||
		tombstoner.calls[1].projectID != projectID || tombstoner.calls[1].jobID != secondJob {
		t.Fatalf("tombstoner calls = %#v", tombstoner.calls)
	}
}

func TestTombstoneSourceDeletedModelJobsStopsAtFirstTombstoneError(t *testing.T) {
	const (
		projectID = "10000000-0000-4000-8000-000000000001"
		bookID    = "20000000-0000-4000-8000-000000000002"
		firstJob  = "30000000-0000-4000-8000-000000000003"
		secondJob = "30000000-0000-4000-8000-000000000004"
	)
	selection, err := postgresDeletionSelectionForRequest(
		sourceDeletionTargetRequestForTest(t, governance.DeletionSelectionBook, projectID, bookID, ""),
	)
	if err != nil {
		t.Fatal(err)
	}
	tx := &modelTombstoneTx{rows: &modelTombstoneRows{ids: []pgtype.UUID{
		mustUUID(firstJob), mustUUID(secondJob),
	}}}
	tombstoner := &recordingModelJobTombstoner{err: deletiondomain.ErrLostTargetClaim}

	err = tombstoneSourceDeletedModelJobsInTx(t.Context(), tx, selection, tombstoner)
	if !errors.Is(err, deletiondomain.ErrLostTargetClaim) {
		t.Fatalf("pre-pass error = %v, want ErrLostTargetClaim", err)
	}
	if len(tombstoner.calls) != 1 || tombstoner.calls[0].jobID != firstJob {
		t.Fatalf("tombstoner calls after failure = %#v", tombstoner.calls)
	}
}

func TestTombstoneSourceDeletedModelJobsSkipsChatEpochAndPermissionLoss(t *testing.T) {
	const (
		projectID = "10000000-0000-4000-8000-000000000001"
		bookID    = "20000000-0000-4000-8000-000000000002"
		chatID    = "40000000-0000-4000-8000-000000000003"
	)
	// Store без базы и без tombstoner: обе ветки обязаны уходить в no-op до любых fail-closed проверок.
	store := New(nil)

	chatRequest := sourceDeletionTargetRequestForTest(t, governance.DeletionSelectionChat, projectID, bookID, chatID)
	if chatRequest.Tombstone.Identity.Mode != governance.PurgeModeChatEpoch {
		t.Fatalf("chat request mode = %q", chatRequest.Tombstone.Identity.Mode)
	}
	if err := store.TombstoneSourceDeletedModelJobs(t.Context(), chatRequest); err != nil {
		t.Fatalf("chat-epoch pre-pass error = %v", err)
	}

	permissionLoss := sourceDeletionTargetRequestForTest(t, governance.DeletionSelectionBook, projectID, bookID, "")
	permissionLoss.Tombstone.Identity.PermissionLossActorUserID = "60000000-0000-4000-8000-000000000005"
	if err := store.TombstoneSourceDeletedModelJobs(t.Context(), permissionLoss); err != nil {
		t.Fatalf("permission-loss pre-pass error = %v", err)
	}
}

func sourceDeletionTargetRequestForTest(
	t *testing.T,
	kind governance.DeletionSelectionKind,
	projectID, bookID, chatID string,
) deletiondomain.TargetRequest {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Microsecond)
	var vector governance.DeletionSelectedVector
	var mode governance.PurgeMode
	var err error
	switch kind {
	case governance.DeletionSelectionBook:
		vector, err = governance.BookDeletionVector(governance.SourceGenerationVector{
			ProjectID: projectID, ProjectGeneration: 1, BookID: bookID, BookGeneration: 2,
		})
		mode = governance.PurgeModeDerivedGeneration
	case governance.DeletionSelectionChat:
		vector, err = governance.ChatDeletionVector(projectID, chatID, 7)
		mode = governance.PurgeModeChatEpoch
	default:
		t.Fatalf("unsupported selection kind %q", kind)
	}
	if err != nil {
		t.Fatal(err)
	}
	deadline, err := governance.PresentPurgeDeadline(now.Add(24 * time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	var digest governance.Digest
	for index := range digest {
		digest[index] = byte(index + 3)
	}
	const tombstoneID = "50000000-0000-4000-8000-000000000004"
	return deletiondomain.TargetRequest{
		Claim: deletiondomain.TargetClaim{
			ProjectID: projectID, TombstoneID: tombstoneID, Target: governance.TargetPostgreSQL,
			ClaimIncarnation: 1, LeaseOwnerDigest: digest, ObservedLeaseExpiry: now.Add(5 * time.Minute),
		},
		Tombstone: deletiondomain.Tombstone{
			ProjectID: projectID,
			Identity: governance.DeletionTombstoneInput{
				TombstoneID: tombstoneID, Origin: governance.DeletionOriginGovernedOnline,
				Mode: mode, SelectedVector: vector, FencedAt: now, Deadline: deadline,
			},
			Status: deletiondomain.TombstoneProcessing,
		},
		Attempt: 1,
	}
}

type modelTombstoneCall struct {
	projectID string
	jobID     string
}

type recordingModelJobTombstoner struct {
	calls []modelTombstoneCall
	err   error
}

func (tombstoner *recordingModelJobTombstoner) TombstoneSourceDeletedModelJobInTx(
	_ context.Context,
	_ pgx.Tx,
	projectID, jobID string,
) error {
	tombstoner.calls = append(tombstoner.calls, modelTombstoneCall{projectID: projectID, jobID: jobID})
	return tombstoner.err
}

type modelTombstoneTx struct {
	pgx.Tx
	query string
	args  []any
	rows  *modelTombstoneRows
}

func (tx *modelTombstoneTx) Query(_ context.Context, query string, args ...any) (pgx.Rows, error) {
	tx.query, tx.args = query, args
	return tx.rows, nil
}

type modelTombstoneRows struct {
	ids  []pgtype.UUID
	next int
}

func (rows *modelTombstoneRows) Close()                                       {}
func (rows *modelTombstoneRows) Err() error                                   { return nil }
func (rows *modelTombstoneRows) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (rows *modelTombstoneRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (rows *modelTombstoneRows) Next() bool {
	rows.next++
	return rows.next <= len(rows.ids)
}

func (rows *modelTombstoneRows) Scan(dest ...any) error {
	if rows.next == 0 || rows.next > len(rows.ids) || len(dest) != 1 {
		return errors.New("scan without current row")
	}
	*dest[0].(*pgtype.UUID) = rows.ids[rows.next-1]
	return nil
}

func (rows *modelTombstoneRows) Values() ([]any, error) { return nil, nil }
func (rows *modelTombstoneRows) RawValues() [][]byte    { return nil }
func (rows *modelTombstoneRows) Conn() *pgx.Conn        { return nil }
