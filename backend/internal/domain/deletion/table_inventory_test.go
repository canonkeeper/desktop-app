package deletion

import (
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/testutil"
)

func TestTableInventoryCoversEveryRetainedBaselineTable(t *testing.T) {
	want := baselineTableNames(t)
	want = append(want, "personal_notes") // Added by forward migration 0035, retained as account data.
	want = append(want, "chapter_cards")  // Added by forward migration 0039, derived from chapter text.
	// Added by forward migration 0060: saga search rows, derived from chapter text.
	want = append(want, "chapter_search_texts", "chapter_search_words")
	want = append(want, "account_tokens") // Added by forward migration 0053, retained as account data.
	// Added by forward migration 0056: writer limits and counters hold only ids and numbers.
	want = append(want, "writer_quota_overrides", "writer_usage_days", "model_usage_days", "model_limit_samples")
	// Added by forward migration 0061: the saga gallery, removed with the project.
	want = append(want, "project_illustration_settings", "saga_images", "saga_image_references")
	// Added by forward migration 0062: the layers of region edits, removed with the project.
	want = append(want, "saga_image_layers")
	// Added by forward migration 0063: the right to illustrations and the requests for it, account data.
	want = append(want, "illustration_access", "illustration_access_requests")
	// Added by forward migration 0068: the platform's limits by default, only numbers and the administrator's id.
	want = append(want, "platform_quota_defaults")
	// Added by forward migration 0069: the author's confirmation of the rights to his images, account data.
	want = append(want, "image_upload_consents")
	// Added by forward migration 0071: promotion permissions of a saga, removed with the project.
	want = append(want, "promo_permissions")
	// Added by forward migration 0083: the name registry of a saga, derived from chapter cards.
	want = append(want, "saga_names", "saga_map_graphs", "saga_map_nodes", "saga_map_node_parts", "saga_map_names")
	// Added by forward migration 0084: the thread layer of a saga, derived from chapter cards.
	want = append(want, "saga_threads")
	// Added by forward migration 0086: what the author said or approved about his saga.
	want = append(want, "author_memory", "author_materials", "author_material_imports")
	// Added by forward migration 0103: what the author took off the map.
	want = append(want, "saga_map_hidden")
	// Added by forward migration 0106: the blind comparison of texts, no link to the product.
	want = append(want, "reading_tests", "reading_test_votes")
	// Added by forward migration 0087: the chapters in writing of the section «Письмо».
	want = append(want, "chapter_drafts")
	// Added by forward migration 0089: the drawings of the workshop belong to the project (IW6.5).
	want = append(want, "saga_drawings")
	// Added by forward migration 0090: the notes on a chapter in writing.
	want = append(want, "chapter_draft_notes")
	// Added by forward migration 0107: the plans, packs and versions of the writing agent.
	want = append(want, "chapter_draft_packs", "chapter_draft_plans", "chapter_draft_versions")
	// Added by forward migration 0111: the chapter the author loaded as a file.
	want = append(want, "chapter_draft_originals")
	// Added by forward migration 0112: the chapter attached to a question of the chat.
	want = append(want, "chat_turn_attachments")
	// Added by forward migration 0113: the contacts of the service and the feedback of authors and guests.
	want = append(want, "platform_contacts", "feedback_threads", "feedback_messages", "feedback_images")
	// Added by forward migration 0114: the consent to cards, the journal of what went to the AI and the Obsidian vault
	// of a saga in the desktop build, removed with the project.
	want = append(want, "card_build_permissions", "model_requests", "model_disclosures", "obsidian_vaults")
	removed := map[string]struct{}{
		"audit_logs":                            {}, // Dropped by forward migration 0046.
		"canon_book_enrichment_steps":           {},
		"canon_correction_proposals":            {}, // Dropped by forward migration 0038.
		"export_artifacts":                      {}, // Dropped by forward migration 0046.
		"governance_writer_leases":              {}, // Dropped by forward migration 0044.
		"job_agent_threads":                     {},
		"reader_annotations":                    {}, // Dropped by forward migration 0047.
		"runtime_session_deletion_associations": {},
		"runtime_sessions":                      {},
		"writer_proposals":                      {}, // Dropped by forward migration 0038.
	}
	// Dropped by forward migration 0041 together with the canon graph.
	for _, table := range removedCanonGraphTables {
		removed[table] = struct{}{}
	}
	want = slices.DeleteFunc(want, func(table string) bool {
		_, drop := removed[table]
		return drop
	})
	sort.Strings(want)

	inventory := DeletionTableInventory()
	got := make([]string, 0, len(inventory))
	seen := make(map[string]struct{}, len(inventory))
	for _, disposition := range inventory {
		if disposition.Validate() != nil {
			t.Fatalf("invalid disposition for %s: %#v", disposition.Table, disposition)
		}
		if _, duplicate := seen[disposition.Table]; duplicate {
			t.Fatalf("duplicate table inventory entry: %s", disposition.Table)
		}
		seen[disposition.Table] = struct{}{}
		got = append(got, disposition.Table)
	}
	sort.Strings(got)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("table inventory drift\ngot:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestBaselineExcludesLegacyBookSummaryAndTextChunkTables(t *testing.T) {
	tables := baselineTableNames(t)
	for _, legacy := range []string{"book_summaries", "text_chunks"} {
		if slices.Contains(tables, legacy) {
			t.Fatalf("baseline retains legacy table %s", legacy)
		}
	}
}

func TestBaselineExcludesLegacyGraphSnapshotAndOwnerKinds(t *testing.T) {
	raw := testutil.MigrationSQL(t, 1)
	text := string(raw)
	for _, legacy := range []string{"current_draft_json", "'text_chunk'", "'book_summary'"} {
		if strings.Contains(text, legacy) {
			t.Fatalf("baseline retains legacy graph/owner surface %q", legacy)
		}
	}
}

func TestDeletionRegistriesExcludeRemovedChatStorage(t *testing.T) {
	removedTables := []string{"chat_messages", "chat_artifacts"}
	for _, disposition := range DeletionTableInventory() {
		if slices.Contains(removedTables, disposition.Table) {
			t.Fatalf("deletion inventory retains removed %s table", disposition.Table)
		}
	}
	for _, mode := range []governance.PurgeMode{
		governance.PurgeModeDerivedGeneration,
		governance.PurgeModeSourceHardPurge,
		governance.PurgeModeChatEpoch,
	} {
		steps, err := PostgresStepRegistry(governance.DeletionSelectionProject, mode)
		if mode == governance.PurgeModeChatEpoch {
			steps, err = PostgresStepRegistry(governance.DeletionSelectionChat, mode)
		}
		if err != nil {
			t.Fatal(err)
		}
		for _, table := range removedTables {
			if hasPostgresDeleteStep(steps, table) {
				t.Fatalf("%s registry retains removed %s table", mode, table)
			}
		}
	}
}

var removedCanonGraphTables = []string{
	"canon_author_rows", "canon_book_enrichment_inputs", "canon_graph_attempts", "canon_graph_book_slots",
	"canon_graph_contributions", "canon_graph_draft_review_reads", "canon_graph_draft_rows",
	"canon_graph_overlays", "canon_graph_row_evidence", "canon_graph_rows", "chapter_summaries",
}

func TestTableInventoryIncludesCanonicalSourceTablesWithoutPersistedSteps(t *testing.T) {
	want := map[string]TableDisposition{
		"project_source_views":     {"project_source_views", TableActionRetainSource, TableActionDeleteWithParent, TableActionRetainSource},
		"book_source_members":      {"book_source_members", TableActionRetainSource, TableActionDeleteWithParent, TableActionRetainSource},
		"chapter_source_members":   {"chapter_source_members", TableActionRetainSource, TableActionDeleteWithParent, TableActionRetainSource},
		"chapter_text_index_state": {"chapter_text_index_state", TableActionDeleteWithParent, TableActionDeleteWithParent, TableActionRetainSource},
		"chapter_text_search_rows": {"chapter_text_search_rows", TableActionDeleteWithParent, TableActionDeleteWithParent, TableActionRetainSource},
		"chapter_cards":            {"chapter_cards", TableActionDeleteWithParent, TableActionDeleteWithParent, TableActionRetainSource},
		"chapter_search_texts":     {"chapter_search_texts", TableActionDeleteWithParent, TableActionDeleteWithParent, TableActionRetainSource},
		"chapter_search_words":     {"chapter_search_words", TableActionDeleteWithParent, TableActionDeleteWithParent, TableActionRetainSource},
		"saga_names":               {"saga_names", TableActionDeleteWithParent, TableActionDeleteWithParent, TableActionRetainSource},
		"saga_map_graphs":          {"saga_map_graphs", TableActionDeleteWithParent, TableActionDeleteWithParent, TableActionRetainSource},
		"saga_map_nodes":           {"saga_map_nodes", TableActionDeleteWithParent, TableActionDeleteWithParent, TableActionRetainSource},
		"saga_map_node_parts":      {"saga_map_node_parts", TableActionDeleteWithParent, TableActionDeleteWithParent, TableActionRetainSource},
		"saga_map_names":           {"saga_map_names", TableActionDeleteWithParent, TableActionDeleteWithParent, TableActionRetainSource},
		"saga_map_hidden":          {"saga_map_hidden", TableActionDeleteWithParent, TableActionDeleteWithParent, TableActionRetainSource},
		"saga_threads":             {"saga_threads", TableActionDeleteWithParent, TableActionDeleteWithParent, TableActionRetainSource},
		"author_memory":            {"author_memory", TableActionRetainSource, TableActionDeleteWithParent, TableActionRetainSource},
		"author_materials":         {"author_materials", TableActionRetainSource, TableActionDeleteWithParent, TableActionRetainSource},
		"author_material_imports":  {"author_material_imports", TableActionRetainSource, TableActionDeleteWithParent, TableActionRetainSource},
		"chapter_drafts":           {"chapter_drafts", TableActionRetainSource, TableActionDeleteWithParent, TableActionRetainSource},
		"chapter_draft_notes":      {"chapter_draft_notes", TableActionRetainSource, TableActionDeleteWithParent, TableActionRetainSource},
		"card_build_permissions":   {"card_build_permissions", TableActionRetainSource, TableActionDeleteWithParent, TableActionRetainSource},
		"model_requests":           {"model_requests", TableActionRetainSource, TableActionDeleteWithParent, TableActionRetainSource},
		"model_disclosures":        {"model_disclosures", TableActionRetainSource, TableActionDeleteWithParent, TableActionRetainSource},
		"obsidian_vaults":          {"obsidian_vaults", TableActionRetainSource, TableActionDeleteWithParent, TableActionRetainSource},
	}

	for _, disposition := range DeletionTableInventory() {
		if expected, ok := want[disposition.Table]; ok {
			if disposition != expected || disposition.Validate() != nil {
				t.Fatalf("canonical disposition %s = %#v, want %#v", disposition.Table, disposition, expected)
			}
			delete(want, disposition.Table)
		}
		if slices.Contains([]string{"chapter_text_chunks", "text_chunks", "book_summaries"}, disposition.Table) {
			t.Fatalf("legacy table entered the inventory: %s", disposition.Table)
		}
	}
	if len(want) != 0 {
		t.Fatalf("missing canonical table dispositions: %#v", want)
	}

	for _, selection := range []struct {
		kind governance.DeletionSelectionKind
		mode governance.PurgeMode
	}{
		{governance.DeletionSelectionProject, governance.PurgeModeDerivedGeneration},
		{governance.DeletionSelectionBook, governance.PurgeModeDerivedGeneration},
		{governance.DeletionSelectionChapter, governance.PurgeModeDerivedGeneration},
		{governance.DeletionSelectionProject, governance.PurgeModeSourceHardPurge},
		{governance.DeletionSelectionBook, governance.PurgeModeSourceHardPurge},
		{governance.DeletionSelectionChapter, governance.PurgeModeSourceHardPurge},
		{governance.DeletionSelectionChat, governance.PurgeModeChatEpoch},
	} {
		steps, err := PostgresStepRegistry(selection.kind, selection.mode)
		if err != nil {
			t.Fatal(err)
		}
		for _, step := range steps {
			if _, forbidden := map[string]struct{}{
				"project_source_views": {}, "book_source_members": {}, "chapter_source_members": {},
				"chapter_text_index_state": {}, "chapter_text_search_rows": {}, "chapter_cards": {},
				"chapter_search_texts": {}, "chapter_search_words": {},
			}[step.Table]; forbidden {
				t.Fatalf("%s/%s persisted step registry contains %s", selection.kind, selection.mode, step.Table)
			}
		}
	}
}

func TestPostgresStepRegistryExcludesRemovedTables(t *testing.T) {
	removed := append([]string{"runtime_sessions", "runtime_session_deletion_associations"}, removedCanonGraphTables...)
	for _, selection := range []struct {
		kind governance.DeletionSelectionKind
		mode governance.PurgeMode
	}{
		{governance.DeletionSelectionProject, governance.PurgeModeDerivedGeneration},
		{governance.DeletionSelectionBook, governance.PurgeModeDerivedGeneration},
		{governance.DeletionSelectionChapter, governance.PurgeModeDerivedGeneration},
		{governance.DeletionSelectionProject, governance.PurgeModeSourceHardPurge},
		{governance.DeletionSelectionBook, governance.PurgeModeSourceHardPurge},
		{governance.DeletionSelectionChapter, governance.PurgeModeSourceHardPurge},
		{governance.DeletionSelectionChat, governance.PurgeModeChatEpoch},
	} {
		steps, err := PostgresStepRegistry(selection.kind, selection.mode)
		if err != nil {
			t.Fatal(err)
		}
		for _, step := range steps {
			if slices.Contains(removed, step.Table) {
				t.Fatalf("%s/%s retains removed step: %#v", selection.kind, selection.mode, step)
			}
		}
	}
}

func TestPostgresStepRegistryIsClosedOrderedAndScopeSpecific(t *testing.T) {
	tests := []struct {
		kind governance.DeletionSelectionKind
		mode governance.PurgeMode
	}{
		{governance.DeletionSelectionProject, governance.PurgeModeDerivedGeneration},
		{governance.DeletionSelectionBook, governance.PurgeModeDerivedGeneration},
		{governance.DeletionSelectionChapter, governance.PurgeModeDerivedGeneration},
		{governance.DeletionSelectionProject, governance.PurgeModeSourceHardPurge},
		{governance.DeletionSelectionBook, governance.PurgeModeSourceHardPurge},
		{governance.DeletionSelectionChapter, governance.PurgeModeSourceHardPurge},
		{governance.DeletionSelectionChat, governance.PurgeModeChatEpoch},
	}
	for _, test := range tests {
		t.Run(string(test.kind)+"/"+string(test.mode), func(t *testing.T) {
			steps, err := PostgresStepRegistry(test.kind, test.mode)
			if err != nil {
				t.Fatal(err)
			}
			if len(steps) == 0 || steps[len(steps)-1].Operation != PostgresOperationFinalize {
				t.Fatalf("registry has no terminal aggregate assertion: %#v", steps)
			}
			seenNames := make(map[string]struct{}, len(steps))
			previousOrder := 0
			for _, step := range steps {
				if step.Validate() != nil || step.Order <= previousOrder {
					t.Fatalf("invalid or unordered step: %#v", step)
				}
				if _, duplicate := seenNames[step.Name]; duplicate {
					t.Fatalf("duplicate step name: %s", step.Name)
				}
				seenNames[step.Name] = struct{}{}
				previousOrder = step.Order
			}
		})
	}

	derived, err := PostgresStepRegistry(governance.DeletionSelectionProject, governance.PurgeModeDerivedGeneration)
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"projects", "books", "chapters", "chat_sessions"} {
		if hasPostgresDeleteStep(derived, table) {
			t.Fatalf("derived-generation registry deletes retained source table %s", table)
		}
	}
	projectHard, err := PostgresStepRegistry(governance.DeletionSelectionProject, governance.PurgeModeSourceHardPurge)
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"projects", "books", "chapters", "project_memberships", "project_invitations", "chat_sessions"} {
		if !hasPostgresDeleteStep(projectHard, table) {
			t.Fatalf("project hard-purge registry omits %s", table)
		}
	}
	bookHard, err := PostgresStepRegistry(governance.DeletionSelectionBook, governance.PurgeModeSourceHardPurge)
	if err != nil {
		t.Fatal(err)
	}
	if !hasPostgresDeleteStep(bookHard, "chat_sessions") {
		t.Fatal("book hard-purge registry omits chat_sessions")
	}
	chapterHard, err := PostgresStepRegistry(governance.DeletionSelectionChapter, governance.PurgeModeSourceHardPurge)
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"projects", "books", "project_memberships", "project_invitations", "chat_sessions"} {
		if hasPostgresDeleteStep(chapterHard, table) {
			t.Fatalf("chapter hard-purge registry broadens into ancestor table %s", table)
		}
	}
}

func TestPostgresStepRegistryRespectsForeignKeyOrder(t *testing.T) {
	steps, err := PostgresStepRegistry(governance.DeletionSelectionProject, governance.PurgeModeSourceHardPurge)
	if err != nil {
		t.Fatal(err)
	}
	order := make(map[string]int, len(steps))
	for _, step := range steps {
		order[step.Table] = step.Order
	}
	dependencies := [][2]string{
		{"chat_turns", "agent_workflow_inputs"},
		{"chat_turns", "chat_sessions"},
		{"chat_sessions", "chapter_paragraphs"},
		{"agent_workflow_inputs", "jobs"},
		{"import_job_requests", "import_uploads"},
		{"chapter_paragraphs", "chapters"},
		{"chapters", "books"},
		{"books", "projects"},
		{"project_memberships", "projects"},
		{"project_invitations", "projects"},
	}
	for _, edge := range dependencies {
		if order[edge[0]] == 0 || order[edge[1]] == 0 || order[edge[0]] >= order[edge[1]] {
			t.Fatalf("FK order %s before %s is not closed: %#v", edge[0], edge[1], order)
		}
	}

	bookSteps, err := PostgresStepRegistry(governance.DeletionSelectionBook, governance.PurgeModeSourceHardPurge)
	if err != nil {
		t.Fatal(err)
	}
	bookOrder := make(map[string]int, len(bookSteps))
	for _, step := range bookSteps {
		bookOrder[step.Table] = step.Order
	}
	for _, edge := range [][2]string{
		{"chat_turns", "chat_sessions"},
		{"chat_sessions", "chapter_paragraphs"},
		{"chat_sessions", "chapters"},
		{"chat_sessions", "books"},
	} {
		if bookOrder[edge[0]] == 0 || bookOrder[edge[1]] == 0 || bookOrder[edge[0]] >= bookOrder[edge[1]] {
			t.Fatalf("book hard-purge order %s before %s is not closed: %#v", edge[0], edge[1], bookOrder)
		}
	}

	chatSteps, err := PostgresStepRegistry(governance.DeletionSelectionChat, governance.PurgeModeChatEpoch)
	if err != nil {
		t.Fatal(err)
	}
	chatOrder := make(map[string]int, len(chatSteps))
	for _, step := range chatSteps {
		chatOrder[step.Table] = step.Order
	}
	if chatOrder["chat_sessions"] == 0 || chatOrder["chat_sessions"] >= chatOrder["deletion_target_tasks"] {
		t.Fatalf("chat-epoch session step shifted: %#v", chatSteps)
	}
}

func TestDeletionPostgresJobRegistryRejectsBindingOnlyModelCleanup(t *testing.T) {
	for _, selection := range []struct {
		kind governance.DeletionSelectionKind
		mode governance.PurgeMode
	}{
		{governance.DeletionSelectionProject, governance.PurgeModeDerivedGeneration},
		{governance.DeletionSelectionProject, governance.PurgeModeSourceHardPurge},
		{governance.DeletionSelectionChat, governance.PurgeModeChatEpoch},
	} {
		steps, err := PostgresStepRegistry(selection.kind, selection.mode)
		if err != nil {
			t.Fatal(err)
		}
		if hasPostgresDeleteStep(steps, "job_model_bindings") {
			t.Fatalf("%s/%s exposes binding-only model cleanup", selection.kind, selection.mode)
		}
	}
}

func baselineTableNames(t *testing.T) []string {
	t.Helper()
	raw := testutil.MigrationSQL(t, 1)
	expression := regexp.MustCompile(`(?m)^CREATE TABLE (?:public\.)?([a-z0-9_]+) \(`)
	matches := expression.FindAllSubmatch(raw, -1)
	result := make([]string, 0, len(matches))
	for _, match := range matches {
		result = append(result, string(match[1]))
	}
	return result
}

func hasPostgresDeleteStep(steps []PostgresStep, table string) bool {
	for _, step := range steps {
		if step.Table == table && step.Operation == PostgresOperationDelete {
			return true
		}
	}
	return false
}
