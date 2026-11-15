// SPDX-License-Identifier: AGPL-3.0-only

package sagaarchive

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"testing"
	"time"

	coverfiles "canonkeeper/backend/internal/adapters/library"
	"canonkeeper/backend/internal/adapters/tempfiles"
	"canonkeeper/backend/internal/auth"
	agentdomain "canonkeeper/backend/internal/domain/agent"
	timeline "canonkeeper/backend/internal/domain/agenttimeline"
	"canonkeeper/backend/internal/domain/quota"
	domain "canonkeeper/backend/internal/domain/sagaarchive"
	sagamemorydomain "canonkeeper/backend/internal/domain/sagamemory"
	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/library"
	"canonkeeper/backend/internal/projects"
	materialsrepo "canonkeeper/backend/internal/repository/postgres/authormaterials"
	sagamemoryrepo "canonkeeper/backend/internal/repository/postgres/sagamemory"
	coverservice "canonkeeper/backend/internal/services/library"
	service "canonkeeper/backend/internal/services/sagaarchive"
	layerservice "canonkeeper/backend/internal/services/sagalayer"
	memoryservice "canonkeeper/backend/internal/services/sagamemory"
	storedb "canonkeeper/backend/internal/store/db"
	indexingstore "canonkeeper/backend/internal/store/indexing"
	jobstore "canonkeeper/backend/internal/store/jobs"
	librarystore "canonkeeper/backend/internal/store/library"
	projectstore "canonkeeper/backend/internal/store/projects"
	"canonkeeper/backend/internal/testutil"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// world is the open core of a build over a base of the test: the author with his session, the saga file both ways and the
// builders of the memory of a saga over a model that must not be called.
type world struct {
	t       *testing.T
	ctx     context.Context
	db      *storedb.DB
	user    string
	proof   auth.SessionWriteProof
	root    string
	temp    *tempfiles.Directory
	covers  *coverfiles.BookCoverFiles
	open    *service.OpenSaga
	export  *service.ExportSaga
	saga    *sagamemoryrepo.Repository
	library *library.Service
	model   *silentModel
}

// silentModel is the model of a build the saga file must never call (SF3.11).
type silentModel struct{ calls int }

func (model *silentModel) Complete(context.Context, string, string) (string, quota.ModelUsage, error) {
	model.calls++
	return "", quota.ModelUsage{}, errors.New("the saga file called the model")
}

// noThreads is the app-server of a base without chats.
type noThreads struct{}

func (noThreads) ThreadItems(context.Context, agentdomain.Flow, string) ([]timeline.RawItem, error) {
	return nil, errors.New("no thread")
}

type tempPort struct{ directory *tempfiles.Directory }

func (port tempPort) Create() (service.TempFile, error) { return port.directory.Create() }

type marks struct{ directory *tempfiles.Directory }

func (port marks) Mark(keys []string) (service.Mark, error) { return port.directory.Mark(keys) }

func newWorld(t *testing.T, prefix string) *world {
	t.Helper()
	ctx := context.Background()
	db := testutil.OpenIsolatedPostgresTestDB(t, prefix)
	var user string
	if err := db.Pool.QueryRow(ctx, `SELECT gen_random_uuid()::text`).Scan(&user); err != nil {
		t.Fatal(err)
	}
	proof := auth.SessionWriteProof{
		SessionHash: fmt.Sprintf("%x", sha256.Sum256([]byte(user+":session"))), CSRFSecretHash: fmt.Sprintf("%x", sha256.Sum256([]byte(user+":csrf"))),
	}
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO users (id, email, display_name, password_hash, email_verified_at) VALUES ($1, $2, 'Автор', 'test-password-hash', now())
	`, user, "author-"+user[:8]+"@example.test"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO sessions (id, user_id, session_hash, csrf_secret_hash, expires_at, created_at)
		VALUES (gen_random_uuid(), $1, $2, $3, clock_timestamp() + interval '1 hour', clock_timestamp())
	`, user, proof.SessionHash, proof.CSRFSecretHash); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	files, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = files.Close() })
	covers, err := coverfiles.NewBookCoverFiles(files)
	if err != nil {
		t.Fatal(err)
	}
	temp, err := tempfiles.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	libraryStore := librarystore.New(db)
	world := &world{t: t, ctx: ctx, db: db, user: user, proof: proof, root: root, temp: temp, covers: covers,
		saga: sagamemoryrepo.New(db), model: &silentModel{},
		library: library.NewService(libraryStore, library.NewSecureIDGenerator(), auth.SystemClock{}),
	}
	world.open = &service.OpenSaga{
		Store:  NewOpener(db, projectstore.New(db), libraryStore),
		Covers: covers,
		Identity: func() (service.CoverIdentity, error) {
			identity, err := coverservice.NewSecureCoverIdentity()
			return service.CoverIdentity{ArtifactID: identity.ArtifactID, StagingKey: identity.StagingKey, FinalKey: identity.FinalKey}, err
		},
		Marks: marks{temp}, Now: time.Now,
	}
	world.export = &service.ExportSaga{
		Snapshot: New(db), Covers: covers, Threads: noThreads{}, Temp: tempPort{temp}, Gate: service.NewGate(nil),
		CreatedBy: "CanonKeeper 0.0.0-test", Now: time.Now,
	}
	return world
}

// openFile opens a saga file as the window does: the file in a temporary file, then the use case.
func (world *world) openFile(file []byte, key string) (service.Opened, error) {
	world.t.Helper()
	temp, err := world.temp.Create()
	if err != nil {
		world.t.Fatal(err)
	}
	if _, err := temp.Write(file); err != nil {
		world.t.Fatal(err)
	}
	sum := sha256.Sum256(file)
	return world.open.Execute(world.ctx, service.OpenInput{
		OpenRequest: service.OpenRequest{ActorUserID: world.user, IdempotencyKey: key, FileSHA256: hex.EncodeToString(sum[:])},
		Proof:       world.proof, File: temp,
	})
}

func (world *world) mustOpen(file []byte, key string) service.Opened {
	world.t.Helper()
	opened, err := world.openFile(file, key)
	if err != nil {
		world.t.Fatalf("the open of the file: %v", err)
	}
	return opened
}

// save saves a saga into its file as getSagaArchive does.
func (world *world) save(projectID string) []byte {
	world.t.Helper()
	prepared, err := world.export.Prepare(world.ctx, service.ExportInput{ActorUserID: world.user, ProjectID: projectID})
	if err != nil {
		world.t.Fatalf("the save of the saga: %v", err)
	}
	defer prepared.Close()
	var out bytes.Buffer
	if err := prepared.WriteTo(world.ctx, &out); err != nil {
		world.t.Fatal(err)
	}
	return out.Bytes()
}

// index runs the text index of a version as its Job does.
func (world *world) index(projectID string, view governance.SourceView) {
	world.t.Helper()
	var jobID string
	var revision uint64
	var incarnation int64
	if err := world.db.Pool.QueryRow(world.ctx, `
		UPDATE jobs SET status='running', claim_incarnation=claim_incarnation+1, claimed_at=clock_timestamp(), updated_at=clock_timestamp()
		WHERE project_id=$1 AND kind='canon_text_index' AND status='queued' AND subject_json->>'sourceView'=$2
		RETURNING id::text, (subject_json->>'catalogRevision')::bigint, claim_incarnation
	`, projectID, string(view)).Scan(&jobID, &revision, &incarnation); err != nil {
		world.t.Fatalf("the text index of the %s text: %v", view, err)
	}
	claim := jobs.WorkerClaim{ProjectID: projectID, JobID: jobID, ClaimIncarnation: incarnation}
	indexing := indexingstore.New(world.db)
	descriptors, err := indexing.ListCanonTextChapterDescriptorsForClaim(world.ctx, claim)
	if err != nil {
		world.t.Fatal(err)
	}
	for _, descriptor := range descriptors {
		snapshot, err := indexing.LoadCanonTextChapterSnapshotForClaim(world.ctx, claim, descriptor)
		if err != nil {
			world.t.Fatal(err)
		}
		if _, err := indexing.ReplaceCanonTextChapterForClaim(world.ctx, indexingstore.ReplaceCanonTextChapterInput{Snapshot: snapshot, IndexedAt: time.Now().UTC()}); err != nil {
			world.t.Fatal(err)
		}
	}
	if err := indexing.CompleteCanonTextIndexForClaim(world.ctx, claim, jobs.CanonTextIndexSubject{
		Type: jobs.KindCanonTextIndex, ProjectID: projectID, SourceView: view, CatalogRevision: revision,
	}); err != nil {
		world.t.Fatal(err)
	}
}

// runCards runs every cards Job of the saga that waits, as the cards worker of the site does, with the map and the threads
// queued by their success; it says how many layer Jobs wait after.
func (world *world) runCards(projectID string) int {
	world.t.Helper()
	sagaText := memoryservice.NewSaga(world.saga, world.saga)
	layerQueue := layerservice.NewQueueDue(world.saga)
	cards := memoryservice.NewBuildCards(world.saga, world.saga, world.model, nil, "test-cards", 1,
		func(_, stage string, err error) { world.t.Errorf("the cards Job failed at %s: %v", stage, err) },
		func(ctx context.Context, version memoryservice.TextVersion) {
			if _, err := layerQueue.Execute(ctx, version.ProjectID, version.View); err != nil {
				world.t.Errorf("the threads were not queued: %v", err)
			}
		},
	).WithMap(memoryservice.NewMapBuilder(sagaText, world.saga, materialsrepo.New(world.db, jobstore.New(world.db))), func(_, stage string, err error) {
		world.t.Errorf("the map failed at %s: %v", stage, err)
	})
	for _, view := range []governance.SourceView{governance.SourceViewDraft, governance.SourceViewPublished} {
		var jobID string
		var incarnation int64
		err := world.db.Pool.QueryRow(world.ctx, `
			UPDATE jobs SET status='running', claim_incarnation=claim_incarnation+1, claimed_at=clock_timestamp(), updated_at=clock_timestamp()
			WHERE project_id=$1 AND kind='chapter_cards' AND status='queued' AND subject_json->>'sourceView'=$2
			RETURNING id::text, claim_incarnation
		`, projectID, string(view)).Scan(&jobID, &incarnation)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			world.t.Fatal(err)
		}
		status, err := cards.RunForClaim(world.ctx, jobs.WorkerClaim{ProjectID: projectID, JobID: jobID, ClaimIncarnation: incarnation},
			jobs.ChapterCardsSubject{Type: jobs.KindChapterCards, ProjectID: projectID, SourceView: view}, true)
		if err != nil || status != jobs.StatusSucceeded {
			world.t.Fatalf("the cards Job of the %s text = %q, %v", view, status, err)
		}
	}
	return world.count(`SELECT count(*) FROM jobs WHERE project_id = $1 AND kind = 'saga_layer'`, projectID)
}

func (world *world) count(query string, args ...any) int {
	world.t.Helper()
	var value int
	if err := world.db.Pool.QueryRow(world.ctx, query, args...).Scan(&value); err != nil {
		world.t.Fatal(err)
	}
	return value
}

// ready builds the memory of a saga as the site does after its import: the text index of both texts, the cards Jobs and
// the catch-up of a start. It says how many layer Jobs it queued.
func (world *world) ready(projectID string) int {
	world.t.Helper()
	world.index(projectID, governance.SourceViewDraft)
	world.index(projectID, governance.SourceViewPublished)
	world.runCards(projectID)
	if _, err := memoryservice.NewQueueMissingCards(world.saga, world.saga, world.saga, world.saga).Execute(world.ctx); err != nil {
		world.t.Fatal(err)
	}
	if _, err := layerservice.NewQueueMissingLayers(world.saga, layerservice.NewQueueDue(world.saga)).Execute(world.ctx); err != nil {
		world.t.Fatal(err)
	}
	return world.runCards(projectID)
}

// keys are the keys and titles of the chapters of a text of a saga, in the order of reading.
func (world *world) keys(projectID string, view governance.SourceView) []string {
	world.t.Helper()
	outline, err := memoryservice.NewSaga(world.saga, world.saga).Outline(world.ctx, projectID, string(view))
	if err != nil {
		world.t.Fatal(err)
	}
	keys := make([]string, len(outline.Version.Chapters))
	for index, chapter := range outline.Version.Chapters {
		keys[index] = chapter.Key + " " + chapter.Title
	}
	return keys
}

// claim takes the next Job of a kind as the worker of the build does.
func (world *world) claim(kind jobs.Kind) jobs.WorkerClaim {
	world.t.Helper()
	worker := pgx.Identifier{testutil.IsolatedPostgresSchema(world.t, world.db) + "_worker"}.Sanitize()
	var project, job pgtype.UUID
	var claimed, state string
	var incarnation int64
	var exhausted pgtype.Text
	if err := world.db.Pool.QueryRow(world.ctx, `
		SELECT project_id, work_id, kind, state, claim_incarnation, exhausted_status
		FROM `+worker+`.claim_next_queued_job_for_worker($1, NULL)
	`, []string{string(kind)}).Scan(&project, &job, &claimed, &state, &incarnation, &exhausted); err != nil {
		world.t.Fatalf("the claim of a %s Job: %v", kind, err)
	}
	return jobs.WorkerClaim{ProjectID: project.String(), JobID: job.String(), ClaimIncarnation: incarnation}
}

// strings lists the values of a query, sorted.
func (world *world) strings(query string, args ...any) []string {
	world.t.Helper()
	rows, err := world.db.Pool.Query(world.ctx, query, args...)
	if err != nil {
		world.t.Fatal(err)
	}
	values, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		world.t.Fatal(err)
	}
	sort.Strings(values)
	return values
}

// read reads a saga file.
func read(t *testing.T, file []byte) *domain.Archive {
	t.Helper()
	archive, err := domain.Read(bytes.NewReader(file), int64(len(file)))
	if err != nil {
		t.Fatalf("the reader of the format: %v", err)
	}
	return archive
}

// projectsActor is the author as the services of the product name him.
func (world *world) actor() library.Actor { return library.Actor{UserID: world.user} }

func (world *world) projects() *projects.Service {
	return projects.NewService(projectstore.New(world.db), projects.NewSecureIDGenerator(), auth.SystemClock{})
}

var _ io.Writer = (*bytes.Buffer)(nil)

// memoryOutlines read the text of a saga as the chat does.
type memoryOutlines struct{ world *world }

func (outlines memoryOutlines) Outline(ctx context.Context, projectID, view string) (*sagamemorydomain.Outline, error) {
	return memoryservice.NewSaga(outlines.world.saga, outlines.world.saga).Outline(ctx, projectID, view)
}
