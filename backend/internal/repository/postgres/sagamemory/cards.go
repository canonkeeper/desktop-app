package sagamemory

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	domain "canonkeeper/backend/internal/domain/sagamemory"
	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/jobs"
	service "canonkeeper/backend/internal/services/sagamemory"
	storedb "canonkeeper/backend/internal/store/db"
	"canonkeeper/backend/internal/store/sqlc"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// ErrClaimLost reports that the cards Job is no longer running under the caller's claim.
var ErrClaimLost = errors.New("saga memory: cards claim lost")

// Cards loads the given parts of the cards of the given chapter texts.
func (repository *Repository) Cards(ctx context.Context, projectID string, keys []service.CardKey, parts domain.CardParts) (map[service.CardKey]domain.Card, error) {
	project, chapterIDs, err := cardQueryArgs(projectID, keys)
	if err != nil || len(chapterIDs) == 0 {
		return map[service.CardKey]domain.Card{}, err
	}
	rows, err := sqlc.New(repository.db.Pool).ListChapterCards(ctx, sqlc.ListChapterCardsParams{
		ProjectID: project, ChapterIds: chapterIDs, Omitted: parts.Omitted(),
	})
	if err != nil {
		return nil, err
	}
	wanted := cardKeySet(keys)
	cards := make(map[service.CardKey]domain.Card, len(rows))
	for _, row := range rows {
		key := service.CardKey{ChapterID: storedb.UUIDString(row.ChapterID), ContentHash: hex.EncodeToString(row.ContentHash)}
		if !wanted[key] {
			continue
		}
		var card domain.Card
		if err := json.Unmarshal(row.CardJson, &card); err != nil {
			return nil, fmt.Errorf("saga memory: card of chapter %s: %w", key.ChapterID, err)
		}
		cards[key] = card
	}
	return cards, nil
}

// ExistingCards reports which of the given chapter texts already have cards of the current or
// newer instructions.
func (repository *Repository) ExistingCards(ctx context.Context, projectID string, keys []service.CardKey) (map[service.CardKey]bool, error) {
	project, chapterIDs, err := cardQueryArgs(projectID, keys)
	if err != nil || len(chapterIDs) == 0 {
		return map[service.CardKey]bool{}, err
	}
	rows, err := sqlc.New(repository.db.Pool).ListChapterCardKeys(ctx, sqlc.ListChapterCardKeysParams{
		ProjectID: project, ChapterIds: chapterIDs,
	})
	if err != nil {
		return nil, err
	}
	existing := make(map[service.CardKey]bool, len(rows))
	for _, row := range rows {
		existing[service.CardKey{ChapterID: storedb.UUIDString(row.ChapterID), ContentHash: hex.EncodeToString(row.ContentHash)}] = true
	}
	return existing, nil
}

// CardsStamp reads the stamp of the cards of the given chapter texts and of the name registry
// (specs/2026-11-04-saga-map.md, R1.5): their number, the time the latest was written and the digest
// of the registry, empty without one.
func (repository *Repository) CardsStamp(ctx context.Context, projectID string, keys []service.CardKey) (service.CardsStamp, error) {
	project, chapterIDs, err := cardQueryArgs(projectID, keys)
	if err != nil {
		return service.CardsStamp{}, err
	}
	texts := make([]string, len(keys))
	for i, key := range keys {
		texts[i] = strings.ToLower(key.ChapterID) + ":" + strings.ToLower(key.ContentHash)
	}
	row, err := sqlc.New(repository.db.Pool).GetChapterCardsStamp(ctx, sqlc.GetChapterCardsStampParams{
		ProjectID: project, ChapterIds: chapterIDs, ChapterTexts: texts,
	})
	if err != nil {
		return service.CardsStamp{}, err
	}
	return service.CardsStamp{Cards: row.Cards, Written: row.Written.Time, Names: row.NamesDigest, Materials: row.Materials}, nil
}

// SaveCard writes one card and the Job progress while the Job still runs under the claim.
func (repository *Repository) SaveCard(ctx context.Context, claim jobs.WorkerClaim, card service.StoredCard, progress float64) error {
	projectID, jobID, err := claimIDs(claim)
	if err != nil {
		return err
	}
	chapterID, err := storedb.ParseUUID(card.ChapterID)
	if err != nil {
		return service.ErrInvalid
	}
	hash, err := hex.DecodeString(card.ContentHash)
	if err != nil || len(hash) != len(governance.Digest{}) {
		return service.ErrInvalid
	}
	body, err := json.Marshal(card.Card)
	if err != nil {
		return err
	}
	return repository.db.WithTx(ctx, func(tx pgx.Tx) error {
		queries := sqlc.New(tx)
		claimed, err := queries.RecordChapterCardsProgress(ctx, sqlc.RecordChapterCardsProgressParams{
			Progress: progress, ProjectID: projectID, JobID: jobID, ClaimIncarnation: claim.ClaimIncarnation,
		})
		if err != nil {
			return err
		}
		if claimed != 1 {
			return ErrClaimLost
		}
		_, err = queries.InsertChapterCard(ctx, sqlc.InsertChapterCardParams{
			ContentHash: hash, CardJson: body, Model: card.Model, Version: domain.CardVersion,
			ProjectID: projectID, ChapterID: chapterID,
		})
		return err
	})
}

// YieldCards puts the claimed cards Job back to the queue after a portion (quota spec R12.2):
// queued at once with the next claim incarnation, so cards Jobs of other projects that waited
// longer are claimed first; attempts and progress stay.
func (repository *Repository) YieldCards(ctx context.Context, claim jobs.WorkerClaim) error {
	projectID, jobID, err := claimIDs(claim)
	if err != nil {
		return err
	}
	updated, err := repository.db.Pool.Exec(ctx, `
		UPDATE jobs
		SET status='queued', claim_incarnation=claim_incarnation + 1, claimed_at=NULL,
		    next_attempt_at=clock_timestamp(), updated_at=clock_timestamp()
		WHERE project_id=$1 AND id=$2 AND kind='chapter_cards' AND status='running'
		  AND claim_incarnation=$3 AND cancel_requested_at IS NULL AND governance_payload_state='active'
	`, projectID, jobID, claim.ClaimIncarnation)
	if err != nil {
		return err
	}
	if updated.RowsAffected() != 1 {
		return ErrClaimLost
	}
	return nil
}

// FinishCards commits the terminal state of the cards Job under its claim, deletes the cards
// no chapter text matches any more (R2.3) and seals the Job's source owner.
func (repository *Repository) FinishCards(ctx context.Context, claim jobs.WorkerClaim, outcome service.CardsOutcome) error {
	if outcome.Subject.ProjectID != claim.ProjectID {
		return service.ErrInvalid
	}
	var resultJSON, errorJSON []byte
	var err error
	if outcome.Result != nil {
		if resultJSON, err = jobs.EncodeResult(jobs.KindChapterCards, *outcome.Result); err != nil {
			return service.ErrInvalid
		}
	}
	if outcome.Error != nil {
		if errorJSON, err = jobs.EncodeError(*outcome.Error); err != nil {
			return service.ErrInvalid
		}
	}
	return repository.endClaimedJob(ctx, claimedJobEnd{
		claim: claim, kind: jobs.KindChapterCards, view: outcome.Subject.SourceView, status: outcome.Status,
		resultJSON: resultJSON, errorJSON: errorJSON,
		inTx: func(tx pgx.Tx, projectID pgtype.UUID) error {
			_, err := sqlc.New(tx).DeleteStaleChapterCards(ctx, projectID)
			return err
		},
	})
}

// claimedJobEnd is the terminal state of a system Job over a text version; inTx runs in its transaction
// while the project and the Job are locked.
type claimedJobEnd struct {
	claim      jobs.WorkerClaim
	kind       jobs.Kind
	view       governance.SourceView
	status     jobs.Status
	resultJSON []byte
	errorJSON  []byte
	inTx       func(tx pgx.Tx, projectID pgtype.UUID) error
}

func (repository *Repository) endClaimedJob(ctx context.Context, end claimedJobEnd) error {
	projectID, jobID, err := claimIDs(end.claim)
	if err != nil {
		return service.ErrInvalid
	}
	from := jobs.StatusRunning
	progress := 0.0
	switch end.status {
	case jobs.StatusSucceeded:
		progress = 1
	case jobs.StatusFailed:
	case jobs.StatusCanceled:
		from = jobs.StatusCanceling
	default:
		return service.ErrInvalid
	}
	return repository.db.WithTx(ctx, func(tx pgx.Tx) error {
		var lockedProject pgtype.UUID
		if err := tx.QueryRow(ctx, `
			SELECT id FROM projects WHERE id=$1 AND deleted_at IS NULL FOR SHARE
		`, projectID).Scan(&lockedProject); err != nil {
			return claimError(err)
		}
		owner, err := lockClaimedJobOwner(ctx, tx, projectID, jobID, end, from)
		if err != nil {
			return err
		}
		if end.inTx != nil {
			if err := end.inTx(tx, projectID); err != nil {
				return err
			}
		}
		updated, err := tx.Exec(ctx, `
			WITH terminal_clock AS (
			  SELECT date_trunc('microseconds', clock_timestamp()) AS value
			)
			UPDATE jobs
			SET status=$1,
			    progress_json=jsonb_set(COALESCE(progress_json, '{}'::jsonb), '{value}', to_jsonb($2::double precision), true),
			    result_json=$3::jsonb, error_json=$4::jsonb,
			    claimed_at=NULL, completed_at=terminal_clock.value, updated_at=terminal_clock.value
			FROM terminal_clock
			WHERE project_id=$5 AND id=$6 AND kind=$10
			  AND status=$7 AND claim_incarnation=$8 AND artifact_source_owner_id=$9
		`, string(end.status), progress, end.resultJSON, end.errorJSON, projectID, jobID, string(from), end.claim.ClaimIncarnation, owner.id, string(end.kind))
		if err != nil {
			return err
		}
		if updated.RowsAffected() != 1 {
			return ErrClaimLost
		}
		if _, err := sqlc.New(tx).SealArtifactSourceOwner(ctx, sqlc.SealArtifactSourceOwnerParams{
			OwnerID: owner.id, ProjectID: projectID, ArtifactKind: string(governance.ArtifactJob), ArtifactID: jobID,
			SourceView: owner.sourceView, ManifestVersion: owner.manifestVersion, ManifestHash: owner.manifestHash,
			RefCount: owner.refCount, SourceIdentityState: owner.sourceIdentityState,
		}); err != nil {
			return claimError(err)
		}
		return nil
	})
}

type claimedJobOwner struct {
	id                  pgtype.UUID
	sourceView          pgtype.Text
	manifestVersion     int32
	manifestHash        []byte
	refCount            int32
	sourceIdentityState string
}

func lockClaimedJobOwner(
	ctx context.Context,
	tx pgx.Tx,
	projectID, jobID pgtype.UUID,
	end claimedJobEnd,
	from jobs.Status,
) (claimedJobOwner, error) {
	var owner claimedJobOwner
	err := tx.QueryRow(ctx, `
		SELECT job.artifact_source_owner_id
		FROM jobs AS job
		WHERE job.project_id=$1 AND job.id=$2 AND job.kind=$7
		  AND job.status=$3 AND job.claim_incarnation=$4
		  AND job.governance_payload_state='active'
		  AND job.subject_json->>'sourceView'=$5
		  AND (($6='canceled' AND job.cancel_requested_at IS NOT NULL)
		       OR ($6<>'canceled' AND job.cancel_requested_at IS NULL))
		FOR UPDATE
	`, projectID, jobID, string(from), end.claim.ClaimIncarnation, string(end.view), string(end.status), string(end.kind)).Scan(&owner.id)
	if err != nil {
		return claimedJobOwner{}, claimError(err)
	}
	err = tx.QueryRow(ctx, `
		SELECT source_view, manifest_version, manifest_hash, ref_count, source_identity_state
		FROM artifact_source_owners
		WHERE id=$1 AND project_id=$2 AND artifact_kind='job' AND artifact_id=$3
		  AND read_state='active' AND fenced_at IS NULL
		FOR UPDATE
	`, owner.id, projectID, jobID).Scan(
		&owner.sourceView, &owner.manifestVersion, &owner.manifestHash, &owner.refCount, &owner.sourceIdentityState,
	)
	if err != nil {
		return claimedJobOwner{}, claimError(err)
	}
	if !owner.sourceView.Valid || owner.sourceView.String != string(end.view) ||
		owner.manifestVersion != 1 || len(owner.manifestHash) != len(governance.Digest{}) || owner.refCount < 1 {
		return claimedJobOwner{}, ErrClaimLost
	}
	return owner, nil
}

func claimIDs(claim jobs.WorkerClaim) (pgtype.UUID, pgtype.UUID, error) {
	if claim.Validate() != nil {
		return pgtype.UUID{}, pgtype.UUID{}, service.ErrInvalid
	}
	projectID, err := storedb.ParseUUID(claim.ProjectID)
	if err != nil {
		return pgtype.UUID{}, pgtype.UUID{}, service.ErrInvalid
	}
	jobID, err := storedb.ParseUUID(claim.JobID)
	if err != nil {
		return pgtype.UUID{}, pgtype.UUID{}, service.ErrInvalid
	}
	return projectID, jobID, nil
}

func claimError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrClaimLost
	}
	return err
}

func cardQueryArgs(projectID string, keys []service.CardKey) (pgtype.UUID, []pgtype.UUID, error) {
	project, err := storedb.ParseUUID(projectID)
	if err != nil {
		return pgtype.UUID{}, nil, service.ErrInvalid
	}
	seen := make(map[string]bool, len(keys))
	chapterIDs := make([]pgtype.UUID, 0, len(keys))
	for _, key := range keys {
		if seen[key.ChapterID] {
			continue
		}
		seen[key.ChapterID] = true
		id, err := storedb.ParseUUID(key.ChapterID)
		if err != nil {
			return pgtype.UUID{}, nil, service.ErrInvalid
		}
		chapterIDs = append(chapterIDs, id)
	}
	return project, chapterIDs, nil
}

func cardKeySet(keys []service.CardKey) map[service.CardKey]bool {
	set := make(map[service.CardKey]bool, len(keys))
	for _, key := range keys {
		set[key] = true
	}
	return set
}

// Facts loads the facts of the cards of the given chapter texts whose subject contains one of
// the names (glossary keys); the caller compares the exact names.
func (repository *Repository) Facts(ctx context.Context, projectID string, keys []service.CardKey, names []string) (map[service.CardKey][]domain.Fact, error) {
	project, chapterIDs, err := cardQueryArgs(projectID, keys)
	if err != nil || len(chapterIDs) == 0 || len(names) == 0 {
		return map[service.CardKey][]domain.Fact{}, err
	}
	patterns := make([]string, 0, len(names))
	for _, name := range names {
		if name = strings.TrimSpace(name); name != "" {
			patterns = append(patterns, "%"+likeEscaper.Replace(name)+"%")
		}
	}
	rows, err := sqlc.New(repository.db.Pool).ListChapterCardFacts(ctx, sqlc.ListChapterCardFactsParams{
		ProjectID: project, ChapterIds: chapterIDs, Patterns: patterns,
	})
	if err != nil {
		return nil, err
	}
	wanted := cardKeySet(keys)
	facts := map[service.CardKey][]domain.Fact{}
	for _, row := range rows {
		key := service.CardKey{ChapterID: storedb.UUIDString(row.ChapterID), ContentHash: hex.EncodeToString(row.ContentHash)}
		if !wanted[key] {
			continue
		}
		var fact domain.Fact
		if json.Unmarshal(row.Fact, &fact) != nil {
			continue
		}
		facts[key] = append(facts[key], fact)
	}
	return facts, nil
}

// likeEscaper escapes the LIKE wildcards of a name.
var likeEscaper = strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`)
