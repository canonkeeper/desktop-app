package jobstore

import (
	"context"
	"errors"

	"canonkeeper/backend/internal/store/db"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type workerClaimQueryer interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

// WorkerClaimAuthority хранит отдельный пул поиска заданий и разрешает только
// выполнение запросов. Для каждой зарегистрированной функции доменный пакет
// добавляет один узкий метод в worker_claim_*.go.
type WorkerClaimAuthority struct {
	queryer workerClaimQueryer
	// releasedBefore — время старта процесса по часам базы: захват задания, сделанный раньше,
	// свободен сразу (см. releaseClaimsOfEarlierProcesses).
	releasedBefore pgtype.Timestamptz
}

func newWorkerClaimAuthority(db workerClaimQueryer) *WorkerClaimAuthority {
	return &WorkerClaimAuthority{queryer: db}
}

// NewWorkerClaimAuthority открывает пул поиска только через пять методов claim и запоминает
// время старта процесса: вызывается один раз при старте, до запуска workers.
// Продуктовые хранилища продолжают работать с отдельной базой данных.
func NewWorkerClaimAuthority(ctx context.Context, db *storedb.DB) (*WorkerClaimAuthority, error) {
	if db == nil || db.Pool == nil {
		panic("store: worker discovery DB is required")
	}
	authority := newWorkerClaimAuthority(db.Pool)
	if err := authority.releaseClaimsOfEarlierProcesses(ctx); err != nil {
		return nil, err
	}
	return authority, nil
}

func ValidateWorkerDiscoveryDatabaseIdentities(ctx context.Context, productDB, workerDB *storedb.DB) error {
	if productDB == nil || productDB.Pool == nil || workerDB == nil || workerDB.Pool == nil {
		return errors.New("store: invalid worker discovery database identity")
	}
	return validateWorkerDiscoveryDatabaseIdentities(ctx, productDB.Pool, workerDB.Pool, "canonkeeper_worker")
}

func validateWorkerDiscoveryDatabaseIdentities(
	ctx context.Context,
	product, worker workerClaimQueryer,
	workerSchema string,
) error {
	if product == nil || worker == nil || workerSchema == "" {
		return errors.New("store: invalid worker discovery database identity")
	}
	var productUser string
	if err := product.QueryRow(ctx, `SELECT current_user`).Scan(&productUser); err != nil {
		return errors.New("store: invalid worker discovery database identity")
	}

	var workerUser string
	var member, safeRole, exactFunctions, exactSurface, noTables bool
	err := worker.QueryRow(ctx, `
		WITH expected(name, arguments) AS (
			VALUES
				('claim_next_queued_job_for_worker', 'text[], timestamp with time zone'),
				('claim_next_deletion_target_task_for_worker', 'bytea, integer, boolean'),
				('claim_next_source_hard_purge_for_worker', ''),
				('claim_next_import_expiry_for_worker', ''),
				('claim_next_deletion_audit_gc_for_worker', '')
		)
		SELECT
			current_user,
			pg_catalog.pg_has_role(current_user, 'canonkeeper_worker_discovery', 'MEMBER'),
			COALESCE((
				SELECT NOT (role.rolsuper OR role.rolinherit = false OR role.rolcreaterole OR
					role.rolcreatedb OR role.rolcanlogin OR role.rolreplication OR role.rolbypassrls)
				FROM pg_catalog.pg_roles AS role
				WHERE role.rolname = current_user
			), false),
			(
				SELECT count(*) = 5
				FROM expected
				JOIN pg_catalog.pg_proc AS procedure
					ON procedure.proname = expected.name
				 AND pg_catalog.oidvectortypes(procedure.proargtypes) = expected.arguments
				JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid = procedure.pronamespace
				WHERE namespace.nspname = $1
				  AND pg_catalog.has_function_privilege(current_user, procedure.oid, 'EXECUTE')
			),
			(
				SELECT count(*) = 5
				FROM pg_catalog.pg_proc AS procedure
				JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid = procedure.pronamespace
				WHERE namespace.nspname = $1
				  AND procedure.proname LIKE 'claim_next_%_for_worker'
				  AND pg_catalog.has_function_privilege(current_user, procedure.oid, 'EXECUTE')
			),
			NOT EXISTS (
				SELECT 1
				FROM pg_catalog.pg_class AS relation
				JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid = relation.relnamespace
				WHERE relation.relkind IN ('r', 'p', 'v', 'm', 'f')
				  AND namespace.nspname !~ '^pg_'
				  AND namespace.nspname <> 'information_schema'
				  AND pg_catalog.has_table_privilege(
					current_user, relation.oid,
					'SELECT,INSERT,UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER'
				  )
			)
	`, workerSchema).Scan(&workerUser, &member, &safeRole, &exactFunctions, &exactSurface, &noTables)
	if err != nil || productUser == "" || workerUser == "" || productUser == workerUser ||
		!member || !safeRole || !exactFunctions || !exactSurface || !noTables {
		return errors.New("store: invalid worker discovery database identity")
	}
	return nil
}
