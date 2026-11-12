package deletion

import (
	"errors"
	"fmt"
	"strings"

	"canonkeeper/backend/internal/governance"
)

var ErrInvalidPostgresRegistry = errors.New("deletion: invalid PostgreSQL deletion registry")

type TableAction string

const (
	TableActionRetainGlobal            TableAction = "retain_global"
	TableActionRetainSource            TableAction = "retain_source"
	TableActionRetainReplayRecord      TableAction = "retain_replay_record"
	TableActionCancelThenRetain        TableAction = "cancel_then_retain"
	TableActionDeleteExactOwner        TableAction = "delete_exact_owner"
	TableActionScrubPayloadDetachOwner TableAction = "scrub_payload_detach_owner"
	TableActionDeleteWithParent        TableAction = "delete_with_parent"
	TableActionDeleteSelectedSubtree   TableAction = "delete_selected_subtree"
	TableActionRetainTypedProof        TableAction = "retain_typed_proof"
)

func (action TableAction) valid() bool {
	switch action {
	case TableActionRetainGlobal,
		TableActionRetainSource,
		TableActionRetainReplayRecord,
		TableActionCancelThenRetain,
		TableActionDeleteExactOwner,
		TableActionScrubPayloadDetachOwner,
		TableActionDeleteWithParent,
		TableActionDeleteSelectedSubtree,
		TableActionRetainTypedProof:
		return true
	default:
		return false
	}
}

type TableDisposition struct {
	Table             string
	DerivedGeneration TableAction
	SourceHardPurge   TableAction
	ChatEpoch         TableAction
}

func (disposition TableDisposition) Validate() error {
	if !postgresIdentifier(disposition.Table) ||
		!disposition.DerivedGeneration.valid() ||
		!disposition.SourceHardPurge.valid() ||
		!disposition.ChatEpoch.valid() {
		return ErrInvalidPostgresRegistry
	}
	return nil
}

func (disposition TableDisposition) action(mode governance.PurgeMode) (TableAction, error) {
	switch mode {
	case governance.PurgeModeDerivedGeneration:
		return disposition.DerivedGeneration, nil
	case governance.PurgeModeSourceHardPurge:
		return disposition.SourceHardPurge, nil
	case governance.PurgeModeChatEpoch:
		return disposition.ChatEpoch, nil
	default:
		return "", ErrInvalidPostgresRegistry
	}
}

// DeletionTableInventory возвращает закрытый runtime-инвентарь, который не даёт
// запустить PostgreSQL-удаление после несовместимого изменения схемы.
func DeletionTableInventory() []TableDisposition {
	return []TableDisposition{
		{"users", TableActionRetainGlobal, TableActionRetainGlobal, TableActionRetainGlobal},
		{"personal_notes", TableActionRetainGlobal, TableActionRetainGlobal, TableActionRetainGlobal},
		{"sessions", TableActionRetainGlobal, TableActionRetainGlobal, TableActionRetainGlobal},
		{"account_tokens", TableActionRetainGlobal, TableActionRetainGlobal, TableActionRetainGlobal},
		{"writer_quota_overrides", TableActionRetainGlobal, TableActionRetainGlobal, TableActionRetainGlobal},
		{"writer_usage_days", TableActionRetainGlobal, TableActionRetainGlobal, TableActionRetainGlobal},
		{"illustration_access", TableActionRetainGlobal, TableActionRetainGlobal, TableActionRetainGlobal},
		{"image_upload_consents", TableActionRetainGlobal, TableActionRetainGlobal, TableActionRetainGlobal},
		{"platform_quota_defaults", TableActionRetainGlobal, TableActionRetainGlobal, TableActionRetainGlobal},
		// «Как с нами связаться» and the feedback of authors and guests (specs/2026-11-07-contacts.md): account data,
		// removed with the account and, for a guest, after its term (F4).
		{"platform_contacts", TableActionRetainGlobal, TableActionRetainGlobal, TableActionRetainGlobal},
		{"feedback_threads", TableActionRetainGlobal, TableActionRetainGlobal, TableActionRetainGlobal},
		{"feedback_messages", TableActionRetainGlobal, TableActionRetainGlobal, TableActionRetainGlobal},
		{"feedback_images", TableActionRetainGlobal, TableActionRetainGlobal, TableActionRetainGlobal},
		// The tiers of the site, the periods of a writer's tier and what he used of their trials
		// (specs/2026-11-08-subscription-tiers.md TR10.3): the deletion of a project does not touch them; the periods and
		// the trials go with the account (TR10.1).
		{"subscription_tiers", TableActionRetainGlobal, TableActionRetainGlobal, TableActionRetainGlobal},
		{"subscription_periods", TableActionRetainGlobal, TableActionRetainGlobal, TableActionRetainGlobal},
		// The batches and the keys of a tier (0118): the keys outlive every account. The counters of an account — refused
		// keys (0118) and the trials used (0119) — go with the account (TR10.1).
		{"subscription_key_batches", TableActionRetainGlobal, TableActionRetainGlobal, TableActionRetainGlobal},
		{"subscription_keys", TableActionRetainGlobal, TableActionRetainGlobal, TableActionRetainGlobal},
		// The trial calls of the voice co-author (0121) are a counter of the same row of an account.
		{"subscription_usage", TableActionRetainGlobal, TableActionRetainGlobal, TableActionRetainGlobal},
		// The new chapters charged against a writer's tier (0120): they outlive the draft and the project, so a deletion
		// never resets the window (TR7.5), and go with the account (TR10.1).
		{"subscription_chapter_charges", TableActionRetainGlobal, TableActionRetainGlobal, TableActionRetainGlobal},
		{"model_usage_days", TableActionRetainGlobal, TableActionRetainGlobal, TableActionRetainGlobal},
		{"model_limit_samples", TableActionRetainGlobal, TableActionRetainGlobal, TableActionRetainGlobal},
		// The blind comparison of texts has no link to the product: only `reading-test delete` removes it (specs/2026-11-05-reading-test.md).
		{"reading_tests", TableActionRetainGlobal, TableActionRetainGlobal, TableActionRetainGlobal},
		{"reading_test_votes", TableActionRetainGlobal, TableActionRetainGlobal, TableActionRetainGlobal},
		{"projects", TableActionRetainSource, TableActionDeleteSelectedSubtree, TableActionRetainSource},
		{"project_memberships", TableActionRetainSource, TableActionDeleteWithParent, TableActionRetainSource},
		{"project_invitations", TableActionCancelThenRetain, TableActionDeleteWithParent, TableActionRetainSource},
		{"books", TableActionRetainSource, TableActionDeleteSelectedSubtree, TableActionRetainSource},
		{"chapters", TableActionRetainSource, TableActionDeleteSelectedSubtree, TableActionRetainSource},
		{"chapter_paragraphs", TableActionRetainSource, TableActionDeleteWithParent, TableActionRetainSource},
		{"jobs", TableActionScrubPayloadDetachOwner, TableActionDeleteExactOwner, TableActionDeleteExactOwner},
		{"chat_sessions", TableActionRetainSource, TableActionDeleteSelectedSubtree, TableActionDeleteSelectedSubtree},
		{"chat_turns", TableActionDeleteExactOwner, TableActionDeleteExactOwner, TableActionDeleteExactOwner},
		{"agent_workflow_inputs", TableActionDeleteExactOwner, TableActionDeleteExactOwner, TableActionDeleteExactOwner},
		// The chapter attached to a question goes with its turn row (specs/2026-11-07-chat-attach-chapter.md, CA6.2).
		{"chat_turn_attachments", TableActionDeleteWithParent, TableActionDeleteWithParent, TableActionDeleteWithParent},
		{"idempotency_records", TableActionRetainReplayRecord, TableActionRetainReplayRecord, TableActionRetainReplayRecord},
		{"import_uploads", TableActionDeleteExactOwner, TableActionDeleteExactOwner, TableActionRetainSource},
		{"import_queues", TableActionRetainSource, TableActionDeleteWithParent, TableActionRetainSource},
		{"import_queue_items", TableActionRetainSource, TableActionDeleteWithParent, TableActionRetainSource},
		{"import_job_requests", TableActionDeleteExactOwner, TableActionDeleteExactOwner, TableActionDeleteExactOwner},

		{"deletion_tombstones", TableActionRetainTypedProof, TableActionRetainTypedProof, TableActionRetainTypedProof},
		{"deletion_target_tasks", TableActionRetainTypedProof, TableActionRetainTypedProof, TableActionRetainTypedProof},
		{"deletion_target_step_progress", TableActionRetainTypedProof, TableActionRetainTypedProof, TableActionRetainTypedProof},
		{"deletion_completion_receipts", TableActionRetainTypedProof, TableActionRetainTypedProof, TableActionRetainTypedProof},
		{"deletion_hard_purge_predecessors", TableActionRetainTypedProof, TableActionRetainTypedProof, TableActionRetainTypedProof},
		{"artifact_source_owners", TableActionDeleteExactOwner, TableActionDeleteExactOwner, TableActionDeleteExactOwner},
		{"artifact_source_refs", TableActionDeleteExactOwner, TableActionDeleteExactOwner, TableActionDeleteExactOwner},
		{"job_model_bindings", TableActionDeleteExactOwner, TableActionDeleteExactOwner, TableActionDeleteExactOwner},
		{"project_source_views", TableActionRetainSource, TableActionDeleteWithParent, TableActionRetainSource},
		{"book_source_members", TableActionRetainSource, TableActionDeleteWithParent, TableActionRetainSource},
		{"chapter_source_members", TableActionRetainSource, TableActionDeleteWithParent, TableActionRetainSource},
		{"chapter_text_index_state", TableActionDeleteWithParent, TableActionDeleteWithParent, TableActionRetainSource},
		{"chapter_text_search_rows", TableActionDeleteWithParent, TableActionDeleteWithParent, TableActionRetainSource},
		{"chapter_cards", TableActionDeleteWithParent, TableActionDeleteWithParent, TableActionRetainSource},
		// The name registry is scrubbed at any source fence and leaves with the project row.
		{"saga_names", TableActionDeleteWithParent, TableActionDeleteWithParent, TableActionRetainSource},
		// The prepared map too (specs/2026-11-04-saga-map.md, R10.4): its nodes, their parts and names go with its graph.
		{"saga_map_graphs", TableActionDeleteWithParent, TableActionDeleteWithParent, TableActionRetainSource},
		{"saga_map_nodes", TableActionDeleteWithParent, TableActionDeleteWithParent, TableActionRetainSource},
		{"saga_map_node_parts", TableActionDeleteWithParent, TableActionDeleteWithParent, TableActionRetainSource},
		{"saga_map_hidden", TableActionDeleteWithParent, TableActionDeleteWithParent, TableActionRetainSource},
		{"saga_map_names", TableActionDeleteWithParent, TableActionDeleteWithParent, TableActionRetainSource},
		// The thread layer too (specs/2026-11-01-writer-assistant-index.md, R2.8).
		{"saga_threads", TableActionDeleteWithParent, TableActionDeleteWithParent, TableActionRetainSource},
		{"chapter_search_texts", TableActionDeleteWithParent, TableActionDeleteWithParent, TableActionRetainSource},
		{"chapter_search_words", TableActionDeleteWithParent, TableActionDeleteWithParent, TableActionRetainSource},
		// The saga gallery belongs to the project: deleting a book or a chat keeps it, the project
		// purge removes it with the project row; its files go with the filesystem target.
		{"project_illustration_settings", TableActionRetainSource, TableActionDeleteWithParent, TableActionRetainSource},
		{"saga_images", TableActionRetainSource, TableActionDeleteWithParent, TableActionRetainSource},
		{"saga_image_references", TableActionRetainSource, TableActionDeleteWithParent, TableActionRetainSource},
		{"saga_image_layers", TableActionRetainSource, TableActionDeleteWithParent, TableActionRetainSource},
		// A drawing of the workshop is the author's (specs/2026-11-02-image-workshop.md, IW6.5): deleting a book
		// or a chat keeps it, the project purge removes it.
		{"saga_drawings", TableActionRetainSource, TableActionDeleteWithParent, TableActionRetainSource},
		// The memory of the author is written by him and belongs to the project: deleting a book keeps
		// it (specs/2026-11-01-writer-assistant-index.md, R4.6), the project purge removes it.
		{"author_memory", TableActionRetainSource, TableActionDeleteWithParent, TableActionRetainSource},
		// The materials of the author belong to the project as his memory does (specs/2026-11-04-saga-map.md, R9.1).
		{"author_materials", TableActionRetainSource, TableActionDeleteWithParent, TableActionRetainSource},
		{"author_material_imports", TableActionRetainSource, TableActionDeleteWithParent, TableActionRetainSource},
		// A chapter draft is written by the author and belongs to the project: deleting a book keeps it
		// (specs/2026-11-01-writer-assistant-index.md, R7.2), the project purge removes it.
		{"chapter_drafts", TableActionRetainSource, TableActionDeleteWithParent, TableActionRetainSource},
		// Its notes go with it (specs/2026-11-02-writer-workshop.md, W7.5).
		{"chapter_draft_notes", TableActionRetainSource, TableActionDeleteWithParent, TableActionRetainSource},
		// The plans, packs and versions of the text of the writing agent go with it (specs/2026-11-06-writing-agent.md, WA10.3).
		{"chapter_draft_packs", TableActionRetainSource, TableActionDeleteWithParent, TableActionRetainSource},
		{"chapter_draft_plans", TableActionRetainSource, TableActionDeleteWithParent, TableActionRetainSource},
		{"chapter_draft_versions", TableActionRetainSource, TableActionDeleteWithParent, TableActionRetainSource},
		// The chapter the author loaded as a file goes with its draft (specs/2026-11-07-writing-proofread.md, PR7.1).
		{"chapter_draft_originals", TableActionRetainSource, TableActionDeleteWithParent, TableActionRetainSource},
		// Promotion permissions of a saga (specs/2026-10-30-promo-permissions.md R7.1) leave with the project.
		{"promo_permissions", TableActionRetainSource, TableActionDeleteWithParent, TableActionRetainSource},
		// The desktop build (specs/2026-11-07-desktop-app.md, DA6.3, DA15.1): the consent to cards, the journal of what
		// went to the AI and the Obsidian vault of a saga leave with the project; deleting a book, a chapter or a chat
		// keeps them. A request of the connection check has no project and stays until the author clears the journal.
		{"card_build_permissions", TableActionRetainSource, TableActionDeleteWithParent, TableActionRetainSource},
		{"model_requests", TableActionRetainSource, TableActionDeleteWithParent, TableActionRetainSource},
		{"model_disclosures", TableActionRetainSource, TableActionDeleteWithParent, TableActionRetainSource},
		{"obsidian_vaults", TableActionRetainSource, TableActionDeleteWithParent, TableActionRetainSource},
		// The wave of the preparation of a saga (specs/2026-11-09-first-session.md, FS1) is a fact of the project:
		// deleting a book keeps the row (the closing of the wave reads what is left), the project purge removes it.
		{"saga_ready_waves", TableActionRetainSource, TableActionDeleteWithParent, TableActionRetainSource},
		// The snapshot of the last update of a book from a file (specs/2026-11-12-ux10-book-revert.md BR1.2) is a fact of its
		// book: the book and the saga take it in their fences, a chapter purged for good takes its row, and the purge of the
		// book or the project removes what is left.
		{"book_update_snapshots", TableActionRetainSource, TableActionDeleteWithParent, TableActionRetainSource},
		{"book_update_snapshot_chapters", TableActionRetainSource, TableActionDeleteWithParent, TableActionRetainSource},
	}
}

type PostgresOperation string

const (
	PostgresOperationDelete       PostgresOperation = "delete"
	PostgresOperationDetach       PostgresOperation = "detach"
	PostgresOperationAssertAbsent PostgresOperation = "assert_absent"
	PostgresOperationFinalize     PostgresOperation = "finalize"
)

func (operation PostgresOperation) valid() bool {
	switch operation {
	case PostgresOperationDelete, PostgresOperationDetach,
		PostgresOperationAssertAbsent, PostgresOperationFinalize:
		return true
	default:
		return false
	}
}

type PostgresCursorKind string

const (
	PostgresCursorNone PostgresCursorKind = "none"
	PostgresCursorUUID PostgresCursorKind = "uuid"
)

func (kind PostgresCursorKind) valid() bool {
	return kind == PostgresCursorNone || kind == PostgresCursorUUID
}

type PostgresStep struct {
	Name       string
	Order      int
	Table      string
	Operation  PostgresOperation
	CursorKind PostgresCursorKind
}

func (step PostgresStep) Validate() error {
	if step.Order <= 0 || step.Order > 999 || !postgresIdentifier(step.Table) ||
		!step.Operation.valid() || !step.CursorKind.valid() ||
		step.Name != fmt.Sprintf("%03d:%s:%s", step.Order, step.Table, step.Operation) {
		return ErrInvalidPostgresRegistry
	}
	if step.Operation == PostgresOperationFinalize &&
		(step.Table != "deletion_target_tasks" || step.CursorKind != PostgresCursorNone) {
		return ErrInvalidPostgresRegistry
	}
	return nil
}

func PostgresStepRegistry(kind governance.DeletionSelectionKind, mode governance.PurgeMode) ([]PostgresStep, error) {
	if !validSelectionMode(kind, mode) {
		return nil, ErrInvalidPostgresRegistry
	}
	inventory := make(map[string]TableDisposition)
	for _, disposition := range DeletionTableInventory() {
		if disposition.Validate() != nil {
			return nil, ErrInvalidPostgresRegistry
		}
		inventory[disposition.Table] = disposition
	}

	type specification struct {
		table     string
		operation PostgresOperation
		cursor    PostgresCursorKind
	}
	specifications := make([]specification, 0, 32)
	add := func(table string, operation PostgresOperation, cursor PostgresCursorKind) {
		specifications = append(specifications, specification{table, operation, cursor})
	}

	add("chat_turns", PostgresOperationDelete, PostgresCursorUUID)
	add("agent_workflow_inputs", PostgresOperationDelete, PostgresCursorUUID)
	add("import_job_requests", PostgresOperationDelete, PostgresCursorUUID)
	if mode != governance.PurgeModeChatEpoch {
		add("import_uploads", PostgresOperationDelete, PostgresCursorUUID)
	}
	if mode == governance.PurgeModeDerivedGeneration {
		add("jobs", PostgresOperationDetach, PostgresCursorUUID)
	} else {
		add("jobs", PostgresOperationDelete, PostgresCursorUUID)
	}
	add("artifact_source_owners", PostgresOperationDelete, PostgresCursorUUID)
	add("artifact_source_refs", PostgresOperationAssertAbsent, PostgresCursorNone)

	if mode == governance.PurgeModeSourceHardPurge {
		if kind == governance.DeletionSelectionProject || kind == governance.DeletionSelectionBook {
			add("chat_sessions", PostgresOperationDelete, PostgresCursorNone)
		}
		add("chapter_paragraphs", PostgresOperationDelete, PostgresCursorUUID)
		add("chapters", PostgresOperationDelete, PostgresCursorUUID)
		if kind != governance.DeletionSelectionChapter {
			add("books", PostgresOperationDelete, PostgresCursorUUID)
		}
		if kind == governance.DeletionSelectionProject {
			add("project_invitations", PostgresOperationDelete, PostgresCursorUUID)
			add("project_memberships", PostgresOperationDelete, PostgresCursorUUID)
		}
	}
	if mode == governance.PurgeModeChatEpoch {
		add("chat_sessions", PostgresOperationDelete, PostgresCursorNone)
	}
	if mode == governance.PurgeModeSourceHardPurge {
		switch kind {
		case governance.DeletionSelectionProject:
			add("projects", PostgresOperationDelete, PostgresCursorNone)
		case governance.DeletionSelectionBook, governance.DeletionSelectionChapter:
			// Выбранная строка источника уже входит в UUID-поддерево на предыдущем шаге.
		default:
			return nil, ErrInvalidPostgresRegistry
		}
	}
	add("deletion_target_tasks", PostgresOperationFinalize, PostgresCursorNone)

	steps := make([]PostgresStep, 0, len(specifications))
	seenTables := make(map[string]struct{}, len(specifications))
	for index, specification := range specifications {
		disposition, known := inventory[specification.table]
		if !known {
			return nil, ErrInvalidPostgresRegistry
		}
		if _, duplicate := seenTables[specification.table]; duplicate {
			return nil, ErrInvalidPostgresRegistry
		}
		seenTables[specification.table] = struct{}{}
		if _, err := disposition.action(mode); err != nil {
			return nil, ErrInvalidPostgresRegistry
		}
		order := index + 1
		step := PostgresStep{
			Name:       fmt.Sprintf("%03d:%s:%s", order, specification.table, specification.operation),
			Order:      order,
			Table:      specification.table,
			Operation:  specification.operation,
			CursorKind: specification.cursor,
		}
		if step.Validate() != nil {
			return nil, ErrInvalidPostgresRegistry
		}
		steps = append(steps, step)
	}
	return steps, nil
}

func validSelectionMode(kind governance.DeletionSelectionKind, mode governance.PurgeMode) bool {
	switch mode {
	case governance.PurgeModeDerivedGeneration, governance.PurgeModeSourceHardPurge:
		return kind == governance.DeletionSelectionProject ||
			kind == governance.DeletionSelectionBook ||
			kind == governance.DeletionSelectionChapter
	case governance.PurgeModeChatEpoch:
		return kind == governance.DeletionSelectionChat
	default:
		return false
	}
}

func postgresIdentifier(value string) bool {
	if value == "" || (value[0] < 'a' || value[0] > 'z') {
		return false
	}
	for _, character := range value {
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '_' {
			return false
		}
	}
	return !strings.Contains(value, "__")
}
