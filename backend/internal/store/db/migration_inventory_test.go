package storedb

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	dbmigrations "canonkeeper/backend/db/migrations"
)

const baselineSHA256 = "ddea2950588c8412beca5f49021b50701cc521c99e1b3d3d1ea2243b0c166f8b"

func TestMigrationInventoryIsSequentialAndImmutable(t *testing.T) {
	inventory, err := dbmigrations.Read()
	if err != nil {
		t.Fatal(err)
	}
	if len(inventory) != 143 {
		t.Fatalf("migration inventory length = %d, want 143", len(inventory))
	}
	if inventory[0].Version != 0 || inventory[0].Name != "0000_schema_migrations.sql" {
		t.Fatalf("first migration = %+v", inventory[0])
	}
	if inventory[1].Version != 1 || inventory[1].Name != "0001_baseline.sql" {
		t.Fatalf("second migration = %+v", inventory[1])
	}
	if inventory[2].Version != 2 || inventory[2].Name != "0002_baseline_acl_hardening.sql" {
		t.Fatalf("third migration = %+v", inventory[2])
	}
	if inventory[3].Version != 3 || inventory[3].Name != "0003_canon_graph_review_read_versions.sql" {
		t.Fatalf("fourth migration = %+v", inventory[3])
	}
	if inventory[4].Version != 4 || inventory[4].Name != "0004_exact_indexing_job_resolver.sql" {
		t.Fatalf("fifth migration = %+v", inventory[4])
	}
	if inventory[5].Version != 5 || inventory[5].Name != "0005_context_owned_job_claims.sql" {
		t.Fatalf("sixth migration = %+v", inventory[5])
	}
	if inventory[6].Version != 6 || inventory[6].Name != "0006_agent_thread_launch_intents.sql" {
		t.Fatalf("seventh migration = %+v", inventory[6])
	}
	if inventory[7].Version != 7 || inventory[7].Name != "0007_agent_timeline_order.sql" {
		t.Fatalf("eighth migration = %+v", inventory[7])
	}
	if inventory[8].Version != 8 || inventory[8].Name != "0008_import_queue_audited_progression.sql" {
		t.Fatalf("ninth migration = %+v", inventory[8])
	}
	if inventory[9].Version != 9 || inventory[9].Name != "0009_chat_provider_turn_bindings.sql" {
		t.Fatalf("tenth migration = %+v", inventory[9])
	}
	if inventory[10].Version != 10 || inventory[10].Name != "0010_legacy_baseline_function_repair.sql" {
		t.Fatalf("eleventh migration = %+v", inventory[10])
	}
	if inventory[11].Version != 11 || inventory[11].Name != "0011_runtime_acl_repair.sql" {
		t.Fatalf("twelfth migration = %+v", inventory[11])
	}
	if inventory[12].Version != 12 || inventory[12].Name != "0012_context_owned_job_claim_repair.sql" {
		t.Fatalf("thirteenth migration = %+v", inventory[12])
	}
	if inventory[13].Version != 13 || inventory[13].Name != "0013_runtime_thread_cleanup_acl.sql" {
		t.Fatalf("fourteenth migration = %+v", inventory[13])
	}
	if inventory[14].Version != 14 || inventory[14].Name != "0014_runtime_deletion_receipt_projection_acl.sql" {
		t.Fatalf("fifteenth migration = %+v", inventory[14])
	}
	if inventory[15].Version != 15 || inventory[15].Name != "0015_native_codex_job_binding.sql" {
		t.Fatalf("sixteenth migration = %+v", inventory[15])
	}
	if inventory[16].Version != 16 || inventory[16].Name != "0016_permission_loss_deletion_intent.sql" {
		t.Fatalf("seventeenth migration = %+v", inventory[16])
	}
	if inventory[17].Version != 17 || inventory[17].Name != "0017_drop_legacy_agent_runtime.sql" {
		t.Fatalf("eighteenth migration = %+v", inventory[17])
	}
	if inventory[18].Version != 18 || inventory[18].Name != "0018_native_enrichment_claim.sql" {
		t.Fatalf("nineteenth migration = %+v", inventory[18])
	}
	if inventory[19].Version != 19 || inventory[19].Name != "0019_stale_native_enrichment_claim.sql" {
		t.Fatalf("twentieth migration = %+v", inventory[19])
	}
	if inventory[20].Version != 20 || inventory[20].Name != "0020_native_terminal_cleanup_acl.sql" {
		t.Fatalf("twenty-first migration = %+v", inventory[20])
	}
	if inventory[21].Version != 21 || inventory[21].Name != "0021_stale_running_native_enrichment_claim.sql" {
		t.Fatalf("twenty-second migration = %+v", inventory[21])
	}
	if inventory[22].Version != 22 || inventory[22].Name != "0022_terminal_import_queue_order_release.sql" {
		t.Fatalf("twenty-third migration = %+v", inventory[22])
	}
	if inventory[23].Version != 23 || inventory[23].Name != "0023_usage_limited_enrichment_claim.sql" {
		t.Fatalf("twenty-fourth migration = %+v", inventory[23])
	}
	if inventory[24].Version != 24 || inventory[24].Name != "0024_product_runtime_export_audit_acl.sql" {
		t.Fatalf("twenty-fifth migration = %+v", inventory[24])
	}
	if inventory[25].Version != 25 || inventory[25].Name != "0025_usage_limited_enrichment_claim_repair.sql" {
		t.Fatalf("twenty-sixth migration = %+v", inventory[25])
	}
	if inventory[26].Version != 26 || inventory[26].Name != "0026_runtime_acl_restore.sql" {
		t.Fatalf("twenty-seventh migration = %+v", inventory[26])
	}
	if inventory[27].Version != 27 || inventory[27].Name != "0027_product_runtime_source_hard_purge_acl.sql" {
		t.Fatalf("twenty-eighth migration = %+v", inventory[27])
	}
	if inventory[28].Version != 28 || inventory[28].Name != "0028_backfill_imported_publication.sql" {
		t.Fatalf("twenty-ninth migration = %+v", inventory[28])
	}
	if inventory[29].Version != 29 || inventory[29].Name != "0029_book_cover_artifacts.sql" {
		t.Fatalf("thirtieth migration = %+v", inventory[29])
	}
	if inventory[30].Version != 30 || inventory[30].Name != "0030_chat_saved_assignment.sql" {
		t.Fatalf("thirty-first migration = %+v", inventory[30])
	}
	if inventory[31].Version != 31 || inventory[31].Name != "0031_research_chat_reference_snapshots.sql" {
		t.Fatalf("thirty-second migration = %+v", inventory[31])
	}
	if inventory[32].Version != 32 || inventory[32].Name != "0032_chat_session_rename.sql" {
		t.Fatalf("thirty-third migration = %+v", inventory[32])
	}
	if inventory[33].Version != 33 || inventory[33].Name != "0033_book_cover_runtime_acl.sql" {
		t.Fatalf("thirty-fourth migration = %+v", inventory[33])
	}
	if inventory[34].Version != 34 || inventory[34].Name != "0034_saga_chat_scope.sql" {
		t.Fatalf("thirty-fifth migration = %+v", inventory[34])
	}
	if inventory[35].Version != 35 || inventory[35].Name != "0035_personal_notes.sql" {
		t.Fatalf("thirty-sixth migration = %+v", inventory[35])
	}
	if inventory[36].Version != 36 || inventory[36].Name != "0036_personal_note_titles.sql" {
		t.Fatalf("title migration = %+v", inventory[36])
	}
	if inventory[37].Version != 37 || inventory[37].Name != "0037_saga_memory_chat.sql" {
		t.Fatalf("saga memory chat migration = %+v", inventory[37])
	}
	if inventory[38].Version != 38 || inventory[38].Name != "0038_remove_writer_and_canon_correction.sql" {
		t.Fatalf("writer and correction removal migration = %+v", inventory[38])
	}
	if inventory[39].Version != 39 || inventory[39].Name != "0039_chapter_cards.sql" {
		t.Fatalf("chapter cards migration = %+v", inventory[39])
	}
	if inventory[40].Version != 40 || inventory[40].Name != "0040_job_claim_recheck.sql" {
		t.Fatalf("job claim recheck migration = %+v", inventory[40])
	}
	if inventory[41].Version != 41 || inventory[41].Name != "0041_remove_canon_graph.sql" {
		t.Fatalf("canon graph removal migration = %+v", inventory[41])
	}
	if inventory[42].Version != 42 || inventory[42].Name != "0042_chapter_card_version.sql" {
		t.Fatalf("chapter card version migration = %+v", inventory[42])
	}
	if inventory[43].Version != 43 || inventory[43].Name != "0043_deletion_registry_normalization.sql" {
		t.Fatalf("deletion registry normalization migration = %+v", inventory[43])
	}
	if inventory[44].Version != 44 || inventory[44].Name != "0044_remove_writer_leases.sql" {
		t.Fatalf("writer lease removal migration = %+v", inventory[44])
	}
	if inventory[45].Version != 45 || inventory[45].Name != "0045_remove_audited_import_queues.sql" {
		t.Fatalf("audited import queue removal migration = %+v", inventory[45])
	}
	if inventory[46].Version != 46 || inventory[46].Name != "0046_remove_export_and_audit.sql" {
		t.Fatalf("export and audit removal migration = %+v", inventory[46])
	}
	if inventory[47].Version != 47 || inventory[47].Name != "0047_remove_reader_annotations.sql" {
		t.Fatalf("reader annotation removal migration = %+v", inventory[47])
	}
	if inventory[48].Version != 48 || inventory[48].Name != "0048_chat_history_outlives_sources.sql" {
		t.Fatalf("chat history outlives sources migration = %+v", inventory[48])
	}
	if inventory[49].Version != 49 || inventory[49].Name != "0049_chat_turns_readable_after_source_deletion.sql" {
		t.Fatalf("chat turns readable after source deletion migration = %+v", inventory[49])
	}
	if inventory[50].Version != 50 || inventory[50].Name != "0050_import_updates_existing_book.sql" {
		t.Fatalf("import updates existing book migration = %+v", inventory[50])
	}
	if inventory[51].Version != 51 || inventory[51].Name != "0051_job_creator_request.sql" {
		t.Fatalf("job creator request migration = %+v", inventory[51])
	}
	if inventory[52].Version != 52 || inventory[52].Name != "0052_chat_keys_survive_source_rebuild.sql" {
		t.Fatalf("chat keys survive source rebuild migration = %+v", inventory[52])
	}
	if inventory[53].Version != 53 || inventory[53].Name != "0053_account_mail.sql" {
		t.Fatalf("account mail migration = %+v", inventory[53])
	}
	if inventory[54].Version != 54 || inventory[54].Name != "0054_legal_documents_consent.sql" {
		t.Fatalf("legal documents consent migration = %+v", inventory[54])
	}
	if inventory[55].Version != 55 || inventory[55].Name != "0055_claims_of_earlier_process.sql" {
		t.Fatalf("claims of earlier process migration = %+v", inventory[55])
	}
	if inventory[56].Version != 56 || inventory[56].Name != "0056_writer_quotas_and_fair_queue.sql" {
		t.Fatalf("writer quotas migration = %+v", inventory[56])
	}
	if inventory[57].Version != 57 || inventory[57].Name != "0057_account_data_rights.sql" {
		t.Fatalf("account data rights migration = %+v", inventory[57])
	}
	if inventory[58].Version != 58 || inventory[58].Name != "0058_fast_job_list_and_statement_stats.sql" {
		t.Fatalf("fast job list migration = %+v", inventory[58])
	}
	if inventory[59].Version != 59 || inventory[59].Name != "0059_owner_counters.sql" {
		t.Fatalf("owner counters migration = %+v", inventory[59])
	}
	if inventory[60].Version != 60 || inventory[60].Name != "0060_saga_search_rows.sql" {
		t.Fatalf("saga search rows migration = %+v", inventory[60])
	}
	if inventory[61].Version != 61 || inventory[61].Name != "0061_saga_illustrations.sql" {
		t.Fatalf("saga illustrations migration = %+v", inventory[61])
	}
	if inventory[62].Version != 62 || inventory[62].Name != "0062_illustration_experience.sql" {
		t.Fatalf("illustration experience migration = %+v", inventory[62])
	}
	if inventory[63].Version != 63 || inventory[63].Name != "0063_illustration_access.sql" {
		t.Fatalf("illustration access migration = %+v", inventory[63])
	}
	if inventory[83].Version != 83 || inventory[83].Name != "0083_saga_names.sql" {
		t.Fatalf("saga names migration = %+v", inventory[83])
	}
	if inventory[84].Version != 84 || inventory[84].Name != "0084_saga_threads.sql" {
		t.Fatalf("saga threads migration = %+v", inventory[84])
	}
	if inventory[85].Version != 85 || inventory[85].Name != "0085_chapter_draft_job.sql" {
		t.Fatalf("chapter draft migration = %+v", inventory[85])
	}
	if inventory[86].Version != 86 || inventory[86].Name != "0086_author_memory.sql" {
		t.Fatalf("author memory migration = %+v", inventory[86])
	}
	if inventory[87].Version != 87 || inventory[87].Name != "0087_chapter_drafts.sql" {
		t.Fatalf("chapter drafts migration = %+v", inventory[87])
	}
	if inventory[88].Version != 88 || inventory[88].Name != "0088_author_description_length.sql" {
		t.Fatalf("author description length migration = %+v", inventory[88])
	}
	if inventory[89].Version != 89 || inventory[89].Name != "0089_image_workshop.sql" {
		t.Fatalf("image workshop migration = %+v", inventory[89])
	}
	if inventory[90].Version != 90 || inventory[90].Name != "0090_writer_workshop.sql" {
		t.Fatalf("writer workshop migration = %+v", inventory[90])
	}
	if inventory[91].Version != 91 || inventory[91].Name != "0091_writer_helper.sql" {
		t.Fatalf("writer helper migration = %+v", inventory[91])
	}
	if inventory[92].Version != 92 || inventory[92].Name != "0092_assistant_voice.sql" {
		t.Fatalf("assistant voice migration = %+v", inventory[92])
	}
	if inventory[93].Version != 93 || inventory[93].Name != "0093_voice_memory.sql" {
		t.Fatalf("voice memory migration = %+v", inventory[93])
	}
	if inventory[94].Version != 94 || inventory[94].Name != "0094_voice_live.sql" {
		t.Fatalf("voice live migration = %+v", inventory[94])
	}
	if inventory[95].Version != 95 || inventory[95].Name != "0095_rework_own_text.sql" {
		t.Fatalf("rework own text migration = %+v", inventory[95])
	}
	if inventory[96].Version != 96 || inventory[96].Name != "0096_saga_map_rows.sql" {
		t.Fatalf("saga map rows migration = %+v", inventory[96])
	}
	if inventory[97].Version != 97 || inventory[97].Name != "0097_saga_map_search.sql" {
		t.Fatalf("saga map search migration = %+v", inventory[97])
	}
	if inventory[98].Version != 98 || inventory[98].Name != "0098_writing_plain_path.sql" {
		t.Fatalf("writing plain path migration = %+v", inventory[98])
	}
	if inventory[99].Version != 99 || inventory[99].Name != "0099_author_materials.sql" {
		t.Fatalf("author materials migration = %+v", inventory[99])
	}
	if inventory[100].Version != 100 || inventory[100].Name != "0100_writing_rework_press.sql" {
		t.Fatalf("writing rework press migration = %+v", inventory[100])
	}
	if inventory[101].Version != 101 || inventory[101].Name != "0101_drop_unread_columns.sql" {
		t.Fatalf("drop unread columns migration = %+v", inventory[101])
	}
	if inventory[102].Version != 102 || inventory[102].Name != "0102_chapter_edit.sql" {
		t.Fatalf("chapter edit migration = %+v", inventory[102])
	}
	if inventory[103].Version != 103 || inventory[103].Name != "0103_saga_map_hidden.sql" {
		t.Fatalf("saga map hidden migration = %+v", inventory[103])
	}
	if inventory[104].Version != 104 || inventory[104].Name != "0104_writing_long_scene.sql" {
		t.Fatalf("writing long scene migration = %+v", inventory[104])
	}
	if inventory[105].Version != 105 || inventory[105].Name != "0105_book_update_preview.sql" {
		t.Fatalf("book update preview migration = %+v", inventory[105])
	}
	if inventory[106].Version != 106 || inventory[106].Name != "0106_reading_tests.sql" {
		t.Fatalf("reading tests migration = %+v", inventory[106])
	}
	if inventory[107].Version != 107 || inventory[107].Name != "0107_writing_agent.sql" {
		t.Fatalf("writing agent migration = %+v", inventory[107])
	}
	if inventory[108].Version != 108 || inventory[108].Name != "0108_writing_agent_job.sql" {
		t.Fatalf("writing agent job migration = %+v", inventory[108])
	}
	if inventory[109].Version != 109 || inventory[109].Name != "0109_writing_one_text.sql" {
		t.Fatalf("writing one text migration = %+v", inventory[109])
	}
	if inventory[110].Version != 110 || inventory[110].Name != "0110_writing_sealed_old_runs.sql" {
		t.Fatalf("writing sealed old runs migration = %+v", inventory[110])
	}
	if inventory[111].Version != 111 || inventory[111].Name != "0111_writing_proofread.sql" {
		t.Fatalf("writing proofread migration = %+v", inventory[111])
	}
	if inventory[112].Version != 112 || inventory[112].Name != "0112_chat_turn_attachments.sql" {
		t.Fatalf("chat turn attachments migration = %+v", inventory[112])
	}
	if inventory[113].Version != 113 || inventory[113].Name != "0113_contacts_and_feedback.sql" {
		t.Fatalf("contacts and feedback migration = %+v", inventory[113])
	}
	if inventory[114].Version != 114 || inventory[114].Name != "0114_desktop_app.sql" {
		t.Fatalf("desktop app migration = %+v", inventory[114])
	}
	if inventory[115].Version != 115 || inventory[115].Name != "0115_chat_attachment_document.sql" {
		t.Fatalf("chat attachment document migration = %+v", inventory[115])
	}
	if inventory[116].Version != 116 || inventory[116].Name != "0116_text_faces.sql" {
		t.Fatalf("text faces migration = %+v", inventory[116])
	}
	if inventory[117].Version != 117 || inventory[117].Name != "0117_subscription_tiers.sql" {
		t.Fatalf("subscription tiers migration = %+v", inventory[117])
	}
	if inventory[118].Version != 118 || inventory[118].Name != "0118_subscription_keys.sql" {
		t.Fatalf("subscription keys migration = %+v", inventory[118])
	}
	if inventory[119].Version != 119 || inventory[119].Name != "0119_tier_drawings.sql" {
		t.Fatalf("tier drawings migration = %+v", inventory[119])
	}
	if inventory[120].Version != 120 || inventory[120].Name != "0120_subscription_chapter_charges.sql" {
		t.Fatalf("subscription chapter charges migration = %+v", inventory[120])
	}
	if inventory[121].Version != 121 || inventory[121].Name != "0121_subscription_voice.sql" {
		t.Fatalf("subscription voice migration = %+v", inventory[121])
	}
	if inventory[122].Version != 122 || inventory[122].Name != "0122_plan_keys_without_platform_defaults.sql" {
		t.Fatalf("plan keys without platform defaults migration = %+v", inventory[122])
	}
	if inventory[123].Version != 123 || inventory[123].Name != "0123_saga_ready.sql" {
		t.Fatalf("saga ready migration = %+v", inventory[123])
	}
	if inventory[124].Version != 124 || inventory[124].Name != "0124_demo_saga.sql" {
		t.Fatalf("demo saga migration = %+v", inventory[124])
	}
	if inventory[125].Version != 125 || inventory[125].Name != "0125_saga_map_places.sql" {
		t.Fatalf("saga map places migration = %+v", inventory[125])
	}
	if inventory[126].Version != 126 || inventory[126].Name != "0126_service_letters.sql" {
		t.Fatalf("service letters migration = %+v", inventory[126])
	}
	if inventory[127].Version != 127 || inventory[127].Name != "0127_feedback_tariff_topic.sql" {
		t.Fatalf("feedback tariff topic migration = %+v", inventory[127])
	}
	if inventory[128].Version != 128 || inventory[128].Name != "0128_author_materials_by_hand.sql" {
		t.Fatalf("author materials by hand migration = %+v", inventory[128])
	}
	if inventory[129].Version != 129 || inventory[129].Name != "0129_draft_version_before_voice.sql" {
		t.Fatalf("draft version before voice migration = %+v", inventory[129])
	}
	if inventory[130].Version != 130 || inventory[130].Name != "0130_tier_limits.sql" {
		t.Fatalf("tier limits migration = %+v", inventory[130])
	}
	if inventory[131].Version != 131 || inventory[131].Name != "0131_note_thought_beside_quote.sql" {
		t.Fatalf("note thought beside quote migration = %+v", inventory[131])
	}
	if inventory[132].Version != 132 || inventory[132].Name != "0132_draft_version_author_variant.sql" {
		t.Fatalf("draft version author variant migration = %+v", inventory[132])
	}
	if inventory[133].Version != 133 || inventory[133].Name != "0133_drawing_book_place.sql" {
		t.Fatalf("drawing book place migration = %+v", inventory[133])
	}
	if inventory[134].Version != 134 || inventory[134].Name != "0134_drawing_trash.sql" {
		t.Fatalf("drawing trash migration = %+v", inventory[134])
	}
	if inventory[135].Version != 135 || inventory[135].Name != "0135_book_key_numbers.sql" {
		t.Fatalf("book key numbers migration = %+v", inventory[135])
	}
	if inventory[136].Version != 136 || inventory[136].Name != "0136_cards_one_per_saga.sql" {
		t.Fatalf("cards one per saga migration = %+v", inventory[136])
	}
	if inventory[137].Version != 137 || inventory[137].Name != "0137_proofread_from_chat.sql" {
		t.Fatalf("proofread from chat migration = %+v", inventory[137])
	}
	if inventory[138].Version != 138 || inventory[138].Name != "0138_upload_volume_waits.sql" {
		t.Fatalf("upload volume waits migration = %+v", inventory[138])
	}
	if inventory[139].Version != 139 || inventory[139].Name != "0139_drawing_angle.sql" {
		t.Fatalf("drawing angle migration = %+v", inventory[139])
	}
	if inventory[140].Version != 140 || inventory[140].Name != "0140_user_last_seen.sql" {
		t.Fatalf("user last seen migration = %+v", inventory[140])
	}
	if inventory[141].Version != 141 || inventory[141].Name != "0141_chapter_draft_version_place.sql" {
		t.Fatalf("chapter draft version place migration = %+v", inventory[141])
	}
	if inventory[142].Version != 142 || inventory[142].Name != "0142_book_update_snapshots.sql" {
		t.Fatalf("book update snapshots migration = %+v", inventory[142])
	}
	if inventory[64].Version != 64 || inventory[64].Name != "0064_illustration_checks.sql" {
		t.Fatalf("illustration checks migration = %+v", inventory[64])
	}
	if inventory[65].Version != 65 || inventory[65].Name != "0065_illustration_layer_artifact.sql" {
		t.Fatalf("illustration layer artifact migration = %+v", inventory[65])
	}
	if inventory[66].Version != 66 || inventory[66].Name != "0066_illustration_title.sql" {
		t.Fatalf("illustration title migration = %+v", inventory[66])
	}
	if inventory[67].Version != 67 || inventory[67].Name != "0067_illustration_provider_limit.sql" {
		t.Fatalf("illustration provider limit migration = %+v", inventory[67])
	}
	if inventory[68].Version != 68 || inventory[68].Name != "0068_platform_quota_defaults.sql" {
		t.Fatalf("platform quota defaults migration = %+v", inventory[68])
	}
	if inventory[69].Version != 69 || inventory[69].Name != "0069_illustration_branches.sql" {
		t.Fatalf("illustration branches migration = %+v", inventory[69])
	}
	if inventory[70].Version != 70 || inventory[70].Name != "0070_image_uploads.sql" {
		t.Fatalf("image uploads migration = %+v", inventory[70])
	}
	if inventory[71].Version != 71 || inventory[71].Name != "0071_promo_permissions.sql" {
		t.Fatalf("promotion permissions migration = %+v", inventory[71])
	}
	if inventory[72].Version != 72 || inventory[72].Name != "0072_edit_attachments.sql" {
		t.Fatalf("edit attachments migration = %+v", inventory[72])
	}
	if inventory[73].Version != 73 || inventory[73].Name != "0073_image_formats.sql" {
		t.Fatalf("image formats migration = %+v", inventory[73])
	}
	if inventory[74].Version != 74 || inventory[74].Name != "0074_image_enlarge.sql" {
		t.Fatalf("image enlarge migration = %+v", inventory[74])
	}
	if inventory[75].Version != 75 || inventory[75].Name != "0075_style_sample_and_more_inputs.sql" {
		t.Fatalf("style sample migration = %+v", inventory[75])
	}
	if inventory[78].Version != 78 || inventory[78].Name != "0078_promo_saga_wide.sql" {
		t.Fatalf("seventy-ninth migration = %+v", inventory[78])
	}
	if inventory[80].Version != 80 || inventory[80].Name != "0080_image_description.sql" {
		t.Fatalf("image description migration = %+v", inventory[80])
	}
	if inventory[81].Version != 81 || inventory[81].Name != "0081_exact_layer_references.sql" {
		t.Fatalf("exact layer references migration = %+v", inventory[81])
	}
	if inventory[82].Version != 82 || inventory[82].Name != "0082_regenerate_note.sql" {
		t.Fatalf("regenerate note migration = %+v", inventory[82])
	}
	if inventory[79].Version != 79 || inventory[79].Name != "0079_reference_looks.sql" {
		t.Fatalf("eightieth migration = %+v", inventory[79])
	}
	if inventory[77].Version != 77 || inventory[77].Name != "0077_image_variants.sql" {
		t.Fatalf("seventy-eighth migration = %+v", inventory[77])
	}
	if inventory[76].Version != 76 || inventory[76].Name != "0076_upload_content_hash.sql" {
		t.Fatalf("upload content hash migration = %+v", inventory[76])
	}
	if got := sha256Hex(inventory[1].SQL); got != inventory[1].Checksum {
		t.Fatalf("baseline checksum = %q, inventory checksum = %q", got, inventory[1].Checksum)
	}
	if inventory[0].Checksum == inventory[1].Checksum {
		t.Fatal("migration checksums must identify distinct files")
	}
}

func TestRuntimeACLRepairMigrationIsNarrow(t *testing.T) {
	inventory, err := dbmigrations.Read()
	if err != nil {
		t.Fatal(err)
	}
	if len(inventory) <= 11 {
		t.Fatalf("migration inventory length = %d, want migration 0011", len(inventory))
	}
	sql := string(inventory[11].SQL)
	for _, want := range []string{
		"REVOKE ALL ON FUNCTION public.ck_canon_graph_handle_bindings_valid(jsonb) FROM PUBLIC;",
		"GRANT EXECUTE ON FUNCTION public.ck_canon_graph_handle_bindings_valid(jsonb) TO canonkeeper_product_runtime;",
		"REVOKE ALL ON SEQUENCE public.agent_timeline_events_sequence_seq FROM PUBLIC;",
		"GRANT USAGE, SELECT ON SEQUENCE public.agent_timeline_events_sequence_seq TO canonkeeper_product_runtime;",
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("runtime ACL repair migration missing %q", want)
		}
	}
	for _, forbidden := range []string{
		"ON ALL FUNCTIONS IN SCHEMA",
		"TO PUBLIC",
		"ck_canon_graph_draft_valid",
		"ck_audit_payload_shape_valid",
	} {
		if strings.Contains(sql, forbidden) {
			t.Fatalf("runtime ACL repair migration contains forbidden %q", forbidden)
		}
	}
}

func TestRuntimeDeletionReceiptProjectionACLRepairMigrationIsNarrow(t *testing.T) {
	inventory, err := dbmigrations.Read()
	if err != nil {
		t.Fatal(err)
	}
	if len(inventory) <= 14 {
		t.Fatalf("migration inventory length = %d, want migration 0014", len(inventory))
	}
	sql := string(inventory[14].SQL)
	for _, want := range []string{
		"REVOKE ALL ON FUNCTION public.ck_deletion_target_receipt_set_projection(text, bytea, bytea, bytea[]) FROM PUBLIC;",
		"GRANT EXECUTE ON FUNCTION public.ck_deletion_target_receipt_set_projection(text, bytea, bytea, bytea[]) TO canonkeeper_product_runtime;",
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("runtime deletion receipt projection ACL repair migration missing %q", want)
		}
	}
	for _, forbidden := range []string{
		"ON ALL FUNCTIONS IN SCHEMA",
		"TO PUBLIC",
		"TO canonkeeper_governance_owner",
		"TO canonkeeper_worker_discovery",
	} {
		if strings.Contains(sql, forbidden) {
			t.Fatalf("runtime deletion receipt projection ACL repair migration contains forbidden %q", forbidden)
		}
	}
}

func TestProductRuntimeExportAuditACLRepairMigrationIsNarrow(t *testing.T) {
	inventory, err := dbmigrations.Read()
	if err != nil {
		t.Fatal(err)
	}
	if len(inventory) <= 24 {
		t.Fatalf("migration inventory length = %d, want migration 0024", len(inventory))
	}
	sql := string(inventory[24].SQL)
	for _, want := range []string{
		"REVOKE ALL ON FUNCTION public.ck_audit_payload_shape_valid(text, text, text, text, jsonb) FROM PUBLIC;",
		"GRANT EXECUTE ON FUNCTION public.ck_audit_payload_shape_valid(text, text, text, text, jsonb) TO canonkeeper_product_runtime;",
		"REVOKE ALL ON FUNCTION public.ck_jsonb_object_key_count(jsonb) FROM PUBLIC;",
		"GRANT EXECUTE ON FUNCTION public.ck_jsonb_object_key_count(jsonb) TO canonkeeper_product_runtime;",
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("export audit ACL repair migration missing %q", want)
		}
	}
	for _, forbidden := range []string{
		"ON ALL FUNCTIONS IN SCHEMA",
		"GRANT ALL",
		"TO PUBLIC",
		"TO canonkeeper_governance_owner",
		"TO canonkeeper_worker_discovery",
	} {
		if strings.Contains(sql, forbidden) {
			t.Fatalf("export audit ACL repair migration contains forbidden %q", forbidden)
		}
	}
}

func TestRuntimeFullACLRepairMigrationIsClosed(t *testing.T) {
	inventory, err := dbmigrations.Read()
	if err != nil {
		t.Fatal(err)
	}
	if len(inventory) <= 26 {
		t.Fatalf("migration inventory length = %d, want migration 0026", len(inventory))
	}
	sql := string(inventory[26].SQL)
	for _, want := range []string{
		"REVOKE ALL PRIVILEGES ON ALL TABLES IN SCHEMA public FROM PUBLIC;",
		"REVOKE ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA public FROM PUBLIC;",
		"REVOKE ALL PRIVILEGES ON ALL FUNCTIONS IN SCHEMA public FROM PUBLIC;",
		"REVOKE ALL PRIVILEGES ON SCHEMA public FROM PUBLIC;",
		"REVOKE ALL PRIVILEGES ON ALL TABLES IN SCHEMA canonkeeper_worker FROM PUBLIC;",
		"REVOKE ALL PRIVILEGES ON ALL FUNCTIONS IN SCHEMA canonkeeper_worker FROM PUBLIC;",
		"REVOKE ALL PRIVILEGES ON SCHEMA canonkeeper_worker FROM PUBLIC;",
		"REVOKE ALL PRIVILEGES ON ALL TABLES IN SCHEMA public FROM canonkeeper_product_runtime;",
		"REVOKE ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA public FROM canonkeeper_product_runtime;",
		"REVOKE ALL PRIVILEGES ON ALL FUNCTIONS IN SCHEMA public FROM canonkeeper_product_runtime;",
		"REVOKE ALL PRIVILEGES ON SCHEMA public FROM canonkeeper_product_runtime;",
		"GRANT USAGE ON SCHEMA public TO canonkeeper_product_runtime;",
		"REVOKE ALL PRIVILEGES ON ALL TABLES IN SCHEMA public FROM canonkeeper_governance_owner;",
		"REVOKE ALL PRIVILEGES ON ALL FUNCTIONS IN SCHEMA public FROM canonkeeper_governance_owner;",
		"GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE public.governance_writer_leases",
		"REVOKE ALL PRIVILEGES ON ALL FUNCTIONS IN SCHEMA canonkeeper_worker FROM canonkeeper_worker_discovery;",
		"GRANT USAGE ON SCHEMA canonkeeper_worker TO canonkeeper_worker_discovery;",
		"canonkeeper_worker.claim_next_queued_job_for_worker(text[])",
		"public.ck_writer_lease_identity_is_valid(bytea, text, bytea)",
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("product runtime ACL repair migration missing %q", want)
		}
	}
	for _, table := range []string{
		"agent_tool_calls", "agent_tool_refs", "agent_workflow_inputs", "artifact_source_owners",
		"artifact_source_refs", "audit_logs", "book_source_members", "books", "canon_author_rows",
		"canon_book_enrichment_inputs", "canon_correction_proposals",
		"canon_graph_attempts", "canon_graph_book_slots", "canon_graph_contributions",
		"canon_graph_draft_review_reads", "canon_graph_draft_rows", "canon_graph_overlays",
		"canon_graph_row_evidence", "canon_graph_rows", "chapter_paragraphs", "chapter_source_members",
		"chapter_summaries", "chapter_text_index_state", "chapter_text_search_rows", "chapters",
		"chat_sessions", "chat_turns", "deletion_completion_receipts",
		"deletion_hard_purge_predecessors", "deletion_target_step_progress", "deletion_target_tasks",
		"deletion_tombstones", "export_artifacts", "idempotency_records", "import_job_requests",
		"import_queue_audit_verdicts", "import_queue_items", "import_queues", "import_uploads",
		"job_model_bindings", "jobs", "project_invitations",
		"project_memberships", "project_source_views", "projects", "reader_annotations",
		"sessions", "users", "writer_proposals",
	} {
		if !strings.Contains(sql, "public."+table) {
			t.Fatalf("product runtime ACL repair migration omits table %q", table)
		}
	}
	for _, forbidden := range []string{
		"GRANT ALL", "TO PUBLIC",
	} {
		if strings.Contains(sql, forbidden) {
			t.Fatalf("product runtime ACL repair migration contains forbidden %q", forbidden)
		}
	}
}

func TestBaselineSQLIsImmutable(t *testing.T) {
	inventory, err := dbmigrations.Read()
	if err != nil {
		t.Fatal(err)
	}
	baseline := inventory[1]
	if baseline.Name != "0001_baseline.sql" {
		t.Fatalf("baseline migration name = %q", baseline.Name)
	}
	if baseline.Checksum == "" || len(baseline.SQL) == 0 {
		t.Fatalf("baseline migration is empty: %+v", baseline)
	}
	if baseline.Checksum != baselineSHA256 {
		t.Fatalf("baseline checksum = %q, want immutable %q", baseline.Checksum, baselineSHA256)
	}
}

func TestBaselinePhase11WorkflowSQL(t *testing.T) {
	inventory, err := dbmigrations.Read()
	if err != nil {
		t.Fatal(err)
	}
	dbRoot := filepath.Join("..", "..", "..", "db")
	entries, err := os.ReadDir(filepath.Join(dbRoot, "queries"))
	if err != nil {
		t.Fatal(err)
	}
	var sql strings.Builder
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		contents, err := os.ReadFile(filepath.Join(dbRoot, "queries", entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		sql.Write(contents)
	}
	sql.Write(inventory[1].SQL)
	baseline := sql.String()

	for _, want := range []string{
		"CREATE TABLE public.agent_workflow_inputs",
		"CREATE TABLE public.writer_proposals",
		"CREATE TABLE public.canon_correction_proposals",
		"CREATE TABLE public.chat_sessions",
		"CREATE TABLE public.chat_turns",
		"input.job_kind = 'research_chat_turn'",
		"proposal.mode = 'expand_outline'",
		"input.job_kind = 'writer_agent'",
		"OR NOT v1_scope_only_product_owner",
		"WHEN 'finding' THEN state = 'read_only'",
		"(state = 'applied'::text) AND (post_apply_revision IS NOT NULL)",
		"(state = 'applied'::text) AND (applied_result_json IS NOT NULL)",
		"ARRAY['source'::text, 'writer'::text]",
		"deleted chat session lifecycle cannot change in place",
		// One chat list per project; a draft chat stays readable only by editor roles.
		"chat.source_view = 'published'\n    OR membership.role IN ('owner', 'admin', 'editor')",
	} {
		if !strings.Contains(baseline, want) {
			t.Fatalf("baseline migration missing %q", want)
		}
	}

	for _, legacy := range []string{
		"agent_suggestion",
		"chat_message_parts",
		"chat_turn_events",
		"state = 'readonly'",
		"ref.value->>'type'",
		"ck_chat_source_reference_valid",
		"ck_chat_canon_row_display_valid",
		"GetReadableChatCorrectionOrigin",
		"ARRAY['source'::text, 'chat'::text, 'writer'::text]",
		"CREATE TABLE public.chat_messages",
		"chat_messages_",
		"chat_turns_activity_json_check",
		"chat_artifacts_references_json_check",
		"CREATE FUNCTION public.chat_references_valid",
		"CREATE FUNCTION public.ck_chat_message_commit_guard",
	} {
		if strings.Contains(baseline, legacy) {
			t.Fatalf("baseline migration retains legacy %q", legacy)
		}
	}
}

func TestActiveProjectRoleQueryHasOneOwner(t *testing.T) {
	dbRoot := filepath.Join("..", "..", "..", "db")
	entries, err := os.ReadDir(filepath.Join(dbRoot, "queries"))
	if err != nil {
		t.Fatal(err)
	}
	var sql strings.Builder
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		contents, err := os.ReadFile(filepath.Join(dbRoot, "queries", entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		sql.Write(contents)
	}
	queries := sql.String()
	const name = "-- name: GetActiveProjectRoleFor"
	if got := strings.Count(queries, name); got != 1 {
		t.Fatalf("active project role query owners = %d, want 1", got)
	}
	start := strings.Index(queries, name)
	end := strings.Index(queries[start+len(name):], "\n-- name:")
	if end < 0 {
		end = len(queries)
	} else {
		end += start + len(name)
	}
	owner := queries[start:end]
	for _, want := range []string{
		"-- name: GetActiveProjectRoleForActor :one",
		"m.user_id = sqlc.arg(actor_user_id)",
		"m.project_id = sqlc.arg(project_id)",
		"m.removed_at IS NULL",
		"p.deleted_at IS NULL",
	} {
		if !strings.Contains(owner, want) {
			t.Fatalf("active project role owner missing %q", want)
		}
	}
}

func TestMigrationInventoryRejectsGapsDuplicatesNonnumericAndDestructiveSQL(t *testing.T) {
	tests := []struct {
		name string
		fs   fstest.MapFS
	}{
		{
			name: "gap",
			fs: fstest.MapFS{
				"0000_first.sql": &fstest.MapFile{Data: []byte("SELECT 1;")},
				"0002_later.sql": &fstest.MapFile{Data: []byte("SELECT 1;")},
			},
		},
		{
			name: "duplicate",
			fs: fstest.MapFS{
				"0000_first.sql":  &fstest.MapFile{Data: []byte("SELECT 1;")},
				"0000_second.sql": &fstest.MapFile{Data: []byte("SELECT 1;")},
			},
		},
		{
			name: "nonnumeric",
			fs: fstest.MapFS{
				"baseline.sql": &fstest.MapFile{Data: []byte("SELECT 1;")},
			},
		},
		{
			name: "destructive",
			fs: fstest.MapFS{
				"0000_first.sql": &fstest.MapFile{Data: []byte("TRUNCATE public.users;")},
			},
		},
		{
			name: "delete without semicolon",
			fs: fstest.MapFS{
				"0000_first.sql": &fstest.MapFile{Data: []byte("DELETE FROM public.users")},
			},
		},
		{
			name: "delete with newline tokens",
			fs: fstest.MapFS{
				"0000_first.sql": &fstest.MapFile{Data: []byte("DELETE\nFROM public.users;")},
			},
		},
		{
			name: "drop table with newline tokens",
			fs: fstest.MapFS{
				"0000_first.sql": &fstest.MapFile{Data: []byte("DROP\nTABLE public.users;")},
			},
		},
		{
			name: "drop schema without semicolon",
			fs: fstest.MapFS{
				"0000_first.sql": &fstest.MapFile{Data: []byte("DROP SCHEMA public")},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := dbmigrations.ReadFS(tt.fs); err == nil {
				t.Fatal("expected invalid migration inventory")
			}
		})
	}
}

func TestMigrationLedgerDDLRevokesRuntimeRoles(t *testing.T) {
	inventory, err := dbmigrations.Read()
	if err != nil {
		t.Fatal(err)
	}
	ledgerSQL := string(inventory[0].SQL)
	if !strings.Contains(ledgerSQL, "REVOKE ALL ON TABLE public.schema_migrations FROM PUBLIC;") {
		t.Fatal("schema migration ledger does not revoke PUBLIC access")
	}
	for _, role := range []string{"canonkeeper_product_runtime", "canonkeeper_worker_discovery"} {
		if strings.Contains(ledgerSQL, " TO "+role) {
			t.Fatalf("schema migration ledger grants access to %s", role)
		}
	}
}

func sha256Hex(raw []byte) string {
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}
