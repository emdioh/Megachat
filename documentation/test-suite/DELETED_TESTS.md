# Tests deleted with the Python server (2026-11-08)

The legacy Python server (`server/`, `protocols/`, `transcription/`, `web/*.py`) was deleted in bead mc-zao.4.4, together with the TUI's `--in-process` mode, which ran that server's core inside the TUI. By user decision, tests that depended on that code were deleted rather than ported. This file lists every one of them; bead mc-gux tracks rebuilding the important TUI behaviours on a stub `ChatClient`.

**Totals:** 187 test files deleted outright, plus test functions removed from 15 kept files: 3685 test functions in all (parametrized cases count once).

What remains: megachatd replaces the server and is covered by its Go tests and by the black-box contract suite (`tests/contract/`, socket/HTTP/WS). The kept Python tests cover the TUI's widgets, pickers, startup/attach and launcher; the TUI's flows that used to run on the in-process core (send, edit, events, contact list, chat view) are what mc-gux has to rebuild. Two renamed tests are listed below as such and not counted.

## Test files deleted outright

Each file imported code that no longer exists (listed), so it tested the Python server or ran the TUI on top of it.

### `tests/contract/test_parity.py` (19 tests)

Imported: `server.api`, `server.client_log`, `server.clients`, `server.config`, `server.logs`, `server.ping`, `server.shutdown`, `server.status`
- `test_messages_page`
- `test_messages_page_variants`
- `test_messages_page_since_cuts`
- `test_messages_window`
- `test_messages_window_without_backend`
- `test_messages_snapshot_signal`
- `test_messages_snapshot_without_backend`
- `test_operation_parity`
- `test_link_status_shape_matches`
- `test_link_finalize_without_signal_cli`
- `test_hello_operations`
- `test_invalid_params_parity`
- `test_unknown_operation_error_code`
- `test_route_parity`
- `test_route_unauthorized_parity`
- `test_route_edge_parity`
- `test_blob_route_parity`
- `test_handshake_parity`
- `test_v2_replay_parity`

### `tests/event_harness.py` (0 tests, test helper)

Imported: `server.bus`, `server.core`, `web.legacy_events`

### `tests/protocols/telegram/test_regression.py` (39 tests)

Imported: `protocols`, `protocols.base`, `protocols.config`, `protocols.telegram`, `protocols.whatsapp`
- `TestMultiBackendRouting::test_all_protocols_registered`
- `TestMultiBackendRouting::test_list_contacts_merges_three_backends`
- `TestMultiBackendRouting::test_get_attachment_path_routes_to_correct_backend`
- `TestMultiBackendRouting::test_send_message_routes_to_correct_backend`
- `TestMultiBackendRouting::test_send_message_unknown_protocol_still_raises`
- `TestMultiBackendRouting::test_mark_read_routes_to_correct_backend`
- `TestMultiBackendRouting::test_poll_once_independent_per_backend`
- `TestMultiBackendRouting::test_register_same_protocol_twice_overwrites`
- `TestCacheIsolation::test_signal_messages_not_visible_in_whatsapp_cache`
- `TestCacheIsolation::test_whatsapp_messages_not_visible_in_signal_cache`
- `TestCacheIsolation::test_same_phone_different_protocol_different_cache_keys`
- `TestCacheIsolation::test_contact_cache_key_protocol_namespacing`
- `TestModelConstants::test_signal_constant_unchanged`
- `TestModelConstants::test_whatsapp_constant_unchanged`
- `TestModelConstants::test_telegram_constant_new`
- `TestModelConstants::test_emoji_map_retains_signal`
- `TestModelConstants::test_emoji_map_retains_whatsapp`
- `TestModelConstants::test_emoji_map_has_telegram`
- `TestModelConstants::test_protocol_emoji_fallback_unchanged`
- `TestModelConstants::test_protocol_emoji_signal`
- `TestModelConstants::test_protocol_emoji_whatsapp`
- `TestModelConstants::test_protocol_emoji_telegram`
- `TestModelConstants::test_contact_cache_key_format_unchanged`
- `TestConfigIsolation::test_telegram_disabled_by_default`
- `TestConfigIsolation::test_whatsapp_enabled_not_affected_by_telegram_config`
- `TestConfigIsolation::test_telegram_enabled_requires_both_credentials`
- `TestConfigIsolation::test_session_path_uses_cache_dir`
- `TestProtocolFilterFourWay::test_filter_cycle_order_with_telegram`
- `TestProtocolFilterFourWay::test_filtered_contacts_respects_telegram`
- `TestEdgeCases::test_register_empty_protocol_still_rejected`
- `TestEdgeCases::test_whatsapp_backend_not_affected_by_telegram_registration`
- `TestEdgeCases::test_all_method_returns_backends_in_registration_order`
- `TestEdgeCases::test_disconnect_all_with_partial_failure`
- `TestEdgeCases::test_mark_read_unknown_protocol_still_raises`
- `TestEdgeCases::test_get_unknown_protocol_still_returns_none`
- `TestEdgeCases::test_signal_contacts_unchanged_after_telegram_registration`
- `TestEdgeCases::test_whatsapp_specific_functionality_not_broken`
- `TestEdgeCases::test_filter_title_suffix_includes_telegram`
- `TestEdgeCases::test_protocol_class_includes_telegram`

### `tests/protocols/telegram/test_telegram_backend.py` (35 tests)

Imported: `protocols.base`, `protocols.db`, `protocols.telegram`
- `TestContactMapping::test_contact_from_user`
- `TestContactMapping::test_contact_from_user_no_last_name`
- `TestContactMapping::test_contact_from_user_username_fallback`
- `TestContactMapping::test_contact_from_user_id_fallback`
- `TestContactMapping::test_contact_from_chat`
- `TestContactMapping::test_contact_from_channel`
- `TestContactMapping::test_contact_cache_key_includes_protocol`
- `TestMessageMapping::test_message_to_chat_event_basic`
- `TestMessageMapping::test_message_outgoing_is_mine`
- `TestMessageMapping::test_message_photo_type`
- `TestMessageMapping::test_message_sticker_type`
- `TestMessageMapping::test_message_document_type`
- `TestMessageMapping::test_message_video_audio_types`
- `TestMessageMapping::test_message_sender_fallback_to_id`
- `TestMessageMapping::test_timestamp_conversion`
- `TestMessageMapping::test_message_quote_no_crash`
- `TestTypingEvent::test_typing_started_event`
- `TestEventQueue::test_poll_once_drains_queue`
- `TestEventQueue::test_poll_once_empty_queue`
- `TestEventQueue::test_queue_thread_safe`
- `TestCacheDedup::test_ingest_message_adds_to_cache`
- `TestCacheDedup::test_ingest_message_dedup_same_id`
- `TestCacheDedup::test_ingest_message_different_contact`
- `TestCacheDedup::test_ingest_message_distinct_ids_same_second`
- `TestCacheDedup::test_optimistic_echo_upgrades_id_and_persists_keeps_ts`
- `TestThreadBoundary::test_handler_only_normalizes_and_puts`
- `TestThreadBoundary::test_cache_written_only_after_poll_once`
- `TestDisconnect::test_disconnect_sync_sets_running_false`
- `TestDisconnect::test_disconnect_sync_noop_when_not_connected`
- `TestDisconnect::test_connect_sync_disconnects_previous_client_first`
- `TestProtocolConstant::test_protocol_telegram_constant`
- `TestProtocolConstant::test_protocol_telegram_in_emoji_map`
- `TestProtocolConstant::test_telegram_backend_protocol`
- `TestProtocolConstant::test_is_chat_backend_subclass`
- `TestProtocolConstant::test_instantiable`

### `tests/test_address_book.py` (148 tests)

Imported: `protocols`, `protocols.base`, `protocols.db`, `protocols.manager`, `protocols.signal`, `protocols.telegram`, `protocols.whatsapp`, `protocols.whatsapp_rest`
- `TestContactPhone::test_phone_from_extras`
- `TestContactPhone::test_phone_default_empty`
- `TestContactPhone::test_phone_none_treated_as_empty`
- `TestListAddressBookSyncDefault::test_returns_contacts_marked`
- `TestListAddressBookSyncDefault::test_does_not_mutate_original_contacts`
- `TestListAddressBookSyncDefault::test_empty_contacts_returns_empty`
- `TestListAddressBookSyncDefault::test_force_accepted`
- `TestListAddressBookSyncDefault::test_preserves_existing_extras`
- `TestListAddressBookSyncDefault::test_async_wrapper_delegates`
- `TestRegisterContact::test_appends_new_contact`
- `TestRegisterContact::test_does_not_duplicate`
- `TestRegisterContact::test_dedup_uses_cache_key_not_dataclass_eq`
- `TestAddressBookConfig::test_address_book_ttl_default`
- `TestAddressBookConfig::test_address_book_ttl_env`
- `TestAddressBookConfig::test_address_book_ttl_env_invalid`
- `TestAddressBookConfig::test_address_book_ttl_config`
- `TestAddressBookConfig::test_wa_lid_cache_ttl_default`
- `TestAddressBookConfig::test_wa_lid_cache_ttl_env`
- `TestAddressBookConfig::test_wa_lid_cache_ttl_env_invalid`
- `TestAddressBookConfig::test_wa_lid_cache_ttl_config`
- `TestAddressBookConfig::test_picker_max_results_default`
- `TestAddressBookConfig::test_picker_max_results_env`
- `TestAddressBookConfig::test_picker_max_results_env_invalid`
- `TestAddressBookConfig::test_picker_max_results_config`
- `TestAddressBookConfig::test_picker_preferred_backend_default`
- `TestAddressBookConfig::test_picker_preferred_backend_env`
- `TestAddressBookConfig::test_picker_preferred_backend_config`
- `TestWADedup::test_duplicates_collapse_to_one`
- `TestWADedup::test_name_beats_pushname`
- `TestWADedup::test_c_us_tiebreak`
- `TestWADedup::test_discards_groups_broadcast_and_no_digits`
- `TestWADedup::test_serialized_dict_id`
- `TestWADedup::test_lid_row_uses_explicit_number_and_keeps_lid_alias`
- `TestWADedup::test_lid_row_without_number_keeps_digits_and_lid`
- `TestWADedup::test_lid_and_c_us_same_number_collapse_and_keep_lid`
- `TestWADedup::test_c_us_duplicate_with_lid_number_dedups_on_real_phone`
- `TestWADedup::test_c_us_row_with_only_lid_number_keeps_real_phone`
- `TestWARestAddressBook::test_list_all_contacts_accepts_str_and_dict_id`
- `TestWARestAddressBook::test_list_all_contacts_unwraps_nested_data`
- `TestWARestAddressBook::test_list_all_contacts_error_returns_none`
- `TestWARestAddressBook::test_resolve_contact_percent_encodes_path`
- `TestWARestAddressBook::test_resolve_contact_error_returns_none`
- `TestWARestAddressBook::test_check_number_exists_exists`
- `TestWARestAddressBook::test_check_number_exists_number_exists`
- `TestWARestAddressBook::test_check_number_exists_missing_key_returns_none`
- `TestWARestAddressBook::test_check_number_exists_error_returns_none`
- `TestWARestAddressBook::test_list_lids_returns_rows`
- `TestWARestAddressBook::test_list_lids_unwraps_nested_data`
- `TestWARestAddressBook::test_list_lids_error_returns_none`
- `TestWALidCache::test_roundtrip_on_tmp_path`
- `TestWALidCache::test_positive_ttl`
- `TestWALidCache::test_negative_ttl_24h`
- `TestWALidCache::test_corrupt_file_starts_empty`
- `TestWALidCache::test_lookup_is_memory_only_no_network`
- `TestWAJidToPhone::test_c_us_and_s_whatsapp_net`
- `TestWAJidToPhone::test_lid_from_cache`
- `TestWAJidToPhone::test_group_and_unknown_return_empty`
- `TestWABulkLidImport::test_imports_valid_pairs`
- `TestWABulkLidImport::test_non_list_response_is_ignored`
- `TestWABulkLidImport::test_error_is_swallowed`
- `TestWAMerge::test_c_us_in_book_merges`
- `TestWAMerge::test_book_lid_row_keeps_lid_alias`
- `TestWAMerge::test_c_us_not_in_book_extra`
- `TestWAMerge::test_lid_resolved_from_cache_merges`
- `TestWAMerge::test_lid_unresolved_standalone_no_network`
- `TestWAMerge::test_groups_included_marked`
- `TestWAMerge::test_display_name_falls_back_to_chat_when_book_empty`
- `TestWAAddressBookCache::test_ttl_cache_and_force`
- `TestWAAddressBookCache::test_error_returns_stale_cache`
- `TestWAAddressBookCache::test_error_none_does_not_wipe_cached_book`
- `TestWAAddressBookCache::test_error_returns_empty`
- `TestWALidResolver::test_start_idempotent`
- `TestWALidResolver::test_batch_max_30_and_save_once`
- `TestWALidResolver::test_lid_resolver_run_does_not_restart_resolver`
- `TestWALidResolver::test_lid_resolver_run_bulk_only_does_not_restart_resolver`
- `TestWALidResolver::test_lid_resolver_run_emits_warmup_done_once_bulk_only`
- `TestWALidResolver::test_lid_resolver_run_emits_warmup_done_on_error`
- `TestWALidResolver::test_lid_resolver_run_emits_warmup_done_once_with_candidates`
- `TestWALidResolver::test_lid_resolver_run_swallows_enqueue_failure`
- `TestTelegramAddressBook::test_build_skips_bots_deleted_and_sets_extras`
- `TestTelegramAddressBook::test_display_name_falls_back_to_phone_then_id`
- `TestTelegramAddressBook::test_merge_with_dialogs_and_lookup_extension`
- `TestTelegramAddressBook::test_error_returns_stale_or_empty`
- `TestTelegramAddressBook::test_not_connected_returns_empty`
- `TestTelegramResolveInputEntity::test_get_input_entity_fast_path`
- `TestTelegramResolveInputEntity::test_fallback_builds_input_peer_user_with_access_hash`
- `TestTelegramResolveInputEntity::test_fallback_without_hash_raises_runtime_error`
- `TestSignalAddressBook::test_markers_and_no_mutation_of_originals`
- `TestSignalAddressBook::test_cache_until_force`
- `TestManagerAddressBook::test_aggregates_across_backends`
- `TestManagerAddressBook::test_error_isolation_keeps_other_backends`
- `TestManagerAddressBook::test_protocols_scoping`
- `TestManagerAddressBook::test_errors_reset_between_calls`
- `TestFixtureAnonymizer::test_deterministic_same_input`
- `TestFixtureAnonymizer::test_seed_shifts_tokens_but_stays_deterministic`
- `TestFixtureAnonymizer::test_phone_number_format`
- `TestFixtureAnonymizer::test_duplicate_numbers_stay_equal`
- `TestFixtureAnonymizer::test_names_usernames_uuid`
- `TestFixtureAnonymizer::test_access_hash_zeroed_and_ids_sequential`
- `TestFixtureAnonymizer::test_shape_preserved_and_input_not_mutated`
- `TestFixtureAnonymizer::test_lid_jid_preserves_suffix`
- `TestFixtureAnonymizer::test_empty_values_preserved`
- `TestFixtureIntegration::test_wa_fixture_dedup_counts`
- `TestFixtureIntegration::test_wa_fixture_merge_with_chat`
- `TestFixtureIntegration::test_tg_fixture_counts_skip_bots`
- `TestDynamicRefreshConfig::test_interval_default`
- `TestDynamicRefreshConfig::test_interval_env`
- `TestDynamicRefreshConfig::test_interval_env_invalid`
- `TestDynamicRefreshConfig::test_interval_config`
- `TestDynamicRefreshConfig::test_cooldown_default`
- `TestDynamicRefreshConfig::test_cooldown_env`
- `TestDynamicRefreshConfig::test_cooldown_env_invalid`
- `TestDynamicRefreshConfig::test_cooldown_config`
- `TestWARefreshContacts::test_fetch_none_returns_errors_and_no_mutation`
- `TestWARefreshContacts::test_fetch_empty_commits_and_preserves_ghost`
- `TestWARefreshContacts::test_new_and_renamed_with_kept_identity`
- `TestWARefreshContacts::test_lid_alias_restored_after_rebuild`
- `TestWARefreshContacts::test_does_not_regress_last_message_ts`
- `TestTelegramRefreshContacts::test_get_dialogs_error_leaves_state_untouched`
- `TestTelegramRefreshContacts::test_rename_preserves_object_access_hash_and_read_max`
- `TestTelegramRefreshContacts::test_ghost_preserved_when_absent_from_dialogs`
- `TestTelegramRefreshContacts::test_refresh_updates_read_outbox_max_id_from_dialog`
- `TestTelegramRefreshContacts::test_to_int_is_canonical`
- `TestTelegramRefreshContacts::test_tg_is_placeholder_guard_not_inverted`
- `TestTelegramRefreshContacts::test_pure_rename_no_duplicates`
- `TestSignalRefreshContacts::test_rpc_error_returns_errors_without_mutation_or_subprocess`
- `TestSignalRefreshContacts::test_unexpected_result_returns_errors_no_subprocess`
- `TestSignalRefreshContacts::test_new_and_renamed_restores_ts_from_cache`
- `TestSignalRefreshContacts::test_parse_contact_dict_extracts_fields`
- `TestSignalRefreshContacts::test_parse_contact_dict_matches_parse_and_update`
- `TestSignalRefreshContacts::test_ghost_preserved_when_absent_from_list_contacts`
- `TestManagerRefreshContacts::test_no_backends_returns_empty`
- `TestManagerRefreshContacts::test_protocol_scoping`
- `TestManagerRefreshContacts::test_error_isolation_keeps_other_backends`
- `TestManagerRefreshContacts::test_force_forwarded`
- `TestManagerRefreshContacts::test_shutdown_is_non_blocking`
- `TestRefreshNotConnectedGuards::test_base_default_is_noop`
- `TestRefreshNotConnectedGuards::test_whatsapp_not_connected`
- `TestRefreshNotConnectedGuards::test_telegram_not_connected`
- `TestRefreshNotConnectedGuards::test_signal_daemon_not_running`
- `TestWALidResolverForceAndB3::test_t7_candidates_branch_uses_force_and_replaces_stale`
- `TestWALidResolverForceAndB3::test_t7_bulk_only_branch_uses_force_and_replaces_stale`
- `TestWALidResolverForceAndB3::test_t11_interamente_vuoto_conserva_entry_wa_book`
- `TestWALidResolverForceAndB3::test_t11b_stale_string_fixture_is_defensive`
- `TestWALidResolverForceAndB3::test_t11c_gen1_entry_not_mutated_and_not_reused`
- `TestWALidResolverForceAndB3::test_t11d_published_list_has_no_duplicate_ids`
- `TestWALidResolverForceAndB3::test_t12_primo_build_vuoto_committa_lista_vuota`
- `TestWALidResolverForceAndB3::test_t13_resolved_name_reapplied_on_refresh`

### `tests/test_attach_spawn_integration.py` (3 tests)

Imported: `server`, `server.paths`, `server.status`
- `test_spawn_then_attach_two_tuis_and_stop`
- `test_two_tuis_started_together_end_up_with_one_server`
- `test_detach_keeps_the_server_and_quit_from_another_tui_stops_it`

### `tests/test_autotranscribe.py` (33 tests)

Imported: `protocols`, `protocols.config`, `protocols.manager`, `server`, `server.api`, `server.autotranscribe`, `server.bus`, `transcription`, `transcription.service`
- `test_effective_resolution_global_and_override`
- `test_config_persistence_keeps_other_keys`
- `test_concurrent_writes_do_not_lose_updates`
- `test_setting_off_does_nothing`
- `test_old_messages_ignored`
- `test_not_added_or_other_protocol_ignored`
- `test_voice_submitted_and_posted_whatsapp`
- `test_quote_fields_telegram`
- `test_quote_fields_signal`
- `test_chat_override_beats_global`
- `test_already_ok_posts_immediately_without_submit`
- `test_no_double_post`
- `test_failure_posts_short_italian_error_and_allows_retry`
- `test_not_configured_is_noop_and_logs_once`
- `test_own_posts_not_reprocessed`
- `test_send_failure_allows_later_retry`
- `test_never_marks_read`
- `test_t_replies_to_voice_whatsapp`
- `test_t_works_with_global_off_and_no_double_with_auto`
- `test_t_signal_by_quote_timestamp`
- `test_t_telegram_by_reply_id`
- `test_t_without_quote_or_non_voice_gets_hint`
- `test_t_unconfigured_replies`
- `test_transcription_status_and_set`
- `test_transcription_invalid_args_show_usage`
- `test_commands_from_others_or_old_ignored`
- `test_service_on_done_callback`
- `test_service_on_done_errors_do_not_kill_worker`
- `test_service_without_callback_unchanged`
- `test_bus_thread_end_to_end_and_gap`
- `test_ops_roundtrip_share_state_with_commands`
- `test_ops_reject_invalid`
- `test_audio_in_ogg_opus_counts_as_voice`

### `tests/test_backend_attachments.py` (5 tests)

Imported: `protocols.rpc`
- `TestGetAttachmentPath::test_attachment_found`
- `TestGetAttachmentPath::test_attachment_not_found`
- `TestGetAttachmentPath::test_attachment_empty_id`
- `TestGetAttachmentPath::test_attachment_none_id`
- `TestGetAttachmentPath::test_attachment_is_directory`

### `tests/test_backend_cache.py` (23 tests)

Imported: `protocols.db`, `protocols.rpc`
- `TestCacheAddLoad::test_add_and_load`
- `TestCacheAddLoad::test_load_empty_db`
- `TestCacheAddLoad::test_add_creates_directory`
- `TestCacheAddLoad::test_add_preserves_optional_fields`
- `TestCacheAddLoad::test_quote_attachment_fields_survive_roundtrip`
- `TestCacheAddLoad::test_quote_attachment_path_survives_roundtrip`
- `TestCachePrune::test_prune_keeps_messages_below_cap`
- `TestCachePrune::test_prune_max_200_messages`
- `TestCachePrune::test_prune_empty_contact_removed`
- `TestCachePrune::test_prune_no_modification`
- `TestCacheMarkAsRead::test_mark_as_read`
- `TestCacheMarkAsRead::test_mark_as_read_no_contact`
- `TestUpdateMessageStatus::test_update_status`
- `TestUpdateMessageStatus::test_update_status_no_match`
- `TestUpdateMessageStatus::test_update_status_scoped_by_protocol`
- `TestUpdateMessageStatus::test_update_status_scoped_by_contact`
- `TestUpdateMessageStatus::test_status_transition_is_conditional_and_never_regresses_receipts`
- `TestProcessReceipt::test_receipt_delivery`
- `TestProcessReceipt::test_receipt_read`
- `TestProcessReceipt::test_receipt_no_match`
- `TestProcessReceipt::test_receipt_no_timestamps`
- `TestProcessReceipt::test_receipt_no_source`
- `TestProcessReceipt::test_receipt_only_upgrades_status`

### `tests/test_backend_contacts.py` (4 tests)

Imported: `protocols.rpc`
- `TestContactModel::test_contact_with_name`
- `TestContactModel::test_contact_without_name`
- `TestContactModel::test_contact_with_aci`
- `TestContactModel::test_contact_empty_name_fallback`

### `tests/test_backend_download.py` (15 tests)

Imported: `protocols.download`
- `TestLocalIp::test_uses_server_ip_from_ssh_connection`
- `TestLocalIp::test_short_ssh_connection_falls_back_to_socket`
- `TestLocalIp::test_socket_failure_falls_back_to_loopback`
- `TestDownloadDirectory::test_get_temp_download_dir_creates_and_caches_directory`
- `TestDownloadDirectory::test_get_temp_download_dir_uses_existing_cache`
- `TestDownloadDirectory::test_clean_download_dir_removes_entries_except_keep`
- `TestDownloadDirectory::test_clean_download_dir_ignores_oserror`
- `TestDownloadServer::test_handler_initializes_with_temp_directory`
- `TestDownloadServer::test_ensure_server_returns_existing_url`
- `TestDownloadServer::test_ensure_server_starts_mocked_server`
- `TestServingFiles::test_serve_file_path_symlinks_source`
- `TestServingFiles::test_serve_file_path_copies_when_symlink_fails`
- `TestServingFiles::test_serve_attachment_handles_missing_and_existing_paths`
- `TestServingFiles::test_serve_text_as_file_writes_and_returns_url`
- `TestServingFiles::test_serve_text_as_file_returns_error_on_write_failure`

### `tests/test_backend_lazy_config.py` (5 tests)

Imported: `protocols`, `protocols.rpc`
- `test_import_clean_without_config_in_ci_environment`
- `test_get_user_number_best_effort_and_require`
- `test_find_signal_cli_best_effort_and_find`
- `test_signal_backend_instantiates_without_config`
- `test_run_subprocess_raises_lazy`

### `tests/test_backend_rpc.py` (14 tests)

Imported: `protocols.rpc`
- `TestSignalRPCClient::test_call_success`
- `TestSignalRPCClient::test_call_error`
- `TestSignalRPCClient::test_list_contacts_success`
- `TestSignalRPCClient::test_list_contacts_error`
- `TestSignalRPCClient::test_receive_success`
- `TestSignalRPCClient::test_receive_error`
- `TestSignalRPCClient::test_send_message_serializes_quote_attachments`
- `TestSignalRPCClient::test_send_message_omits_quote_attachments_when_none`
- `TestSignalSSE::test_listen_events_yields_envelope`
- `TestSignalSSE::test_listen_events_skips_keepalive`
- `TestSignalSSE::test_listen_events_connection_error`
- `TestSignalSSE::test_listen_events_bad_json`
- `TestIsDaemonRunning::test_daemon_running`
- `TestIsDaemonRunning::test_daemon_not_running`

### `tests/test_backend_send.py` (6 tests)

Imported: `protocols.rpc`
- `TestSendSubprocess::test_send_basic`
- `TestSendSubprocess::test_send_with_quote`
- `TestSendSubprocess::test_send_with_partial_quote`
- `TestSendRPC::test_send_message_basic`
- `TestSendRPC::test_send_message_with_timestamp`
- `TestSendRPC::test_send_message_with_quote`

### `tests/test_backend_webhook.py` (11 tests)

Imported: `protocols`
- `TestWebhookPost::test_wrong_path_returns_404`
- `TestWebhookPost::test_valid_json_forwards_to_target_and_acks`
- `TestWebhookPost::test_invalid_json_returns_400`
- `TestWebhookPost::test_body_read_failure_returns_400`
- `TestWebhookPost::test_target_failure_still_acks`
- `TestWebhookPost::test_no_target_or_non_dict_data_still_acks`
- `TestWebhookResponse::test_writes_json_response`
- `TestWebhookResponse::test_response_write_failure_is_ignored`
- `TestWebhookServer::test_existing_server_rebinds_target`
- `TestWebhookServer::test_starts_mocked_server`
- `TestWebhookServer::test_bind_error_returns_zero`

### `tests/test_backends.py` (69 tests)

Imported: `protocols`, `protocols.base`, `protocols.db`, `protocols.rpc`, `protocols.signal`
- `TestChatBackendABC::test_cannot_instantiate_base`
- `TestChatBackendABC::test_protocol_empty_by_default`
- `TestChatBackendABC::test_register_rejects_empty_protocol`
- `TestChatBackendABC::test_default_get_attachment_path_none`
- `TestChatBackendABC::test_default_needs_pairing_false`
- `TestChatBackendABC::test_default_is_connected_false`
- `TestChatBackendABC::test_default_get_pairing_qr_none`
- `TestChatBackendABC::test_abstract_method_raises_not_implemented`
- `TestChatBackendABC::test_abstract_receive_raises_not_implemented`
- `TestSignalBackend::test_protocol_signal`
- `TestSignalBackend::test_is_connected_false_before_daemon_session`
- `TestSignalBackend::test_is_connected_true_once_daemon_session_up`
- `TestSignalBackend::test_to_chat_contact`
- `TestSignalBackend::test_mark_read_sync_persists`
- `TestSignalBackend::test_parse_contacts_from_output`
- `TestSignalBackend::test_parse_contacts_from_output_name_with_spaces`
- `TestSignalBackend::test_set_contacts_recovers_last_message_ts_from_cache`
- `TestSignalBackend::test_set_contacts_filters_status_broadcast`
- `TestSignalBackend::test_envelope_to_event_message`
- `TestSignalBackend::test_envelope_to_event_unknown_contact`
- `TestSignalBackend::test_envelope_to_event_sent_unknown_dest_returns_none`
- `TestSignalBackend::test_envelope_to_event_typing`
- `TestSignalBackend::test_envelope_to_event_receipt`
- `TestSignalBackend::test_single_image_attachment_backward_compat`
- `TestSignalBackend::test_multiple_image_attachments`
- `TestSignalBackend::test_mixed_attachments`
- `TestSignalBackend::test_signal_media_persists_content_type`
- `TestSignalBackend::test_signal_media_content_type_null_when_absent`
- `TestSignalBackend::test_text_with_multiple_attachments_only_first_has_text`
- `TestSignalBackend::test_no_attachments_pure_text`
- `TestSignalBackend::test_sent_multiple_attachments`
- `TestSignalBackend::test_envelope_empty_data_returns_empty_list`
- `TestBackendManager::test_register_and_get`
- `TestBackendManager::test_get_missing_returns_none`
- `TestBackendManager::test_list_contacts_merges`
- `TestBackendManager::test_send_message_routes_to_backend`
- `TestBackendManager::test_send_message_forwards_reply_to_message_id`
- `TestBackendManager::test_send_message_unknown_protocol_raises`
- `TestBackendManager::test_connect_all`
- `TestBackendManager::test_list_contacts_ignores_backend_without_contacts`
- `TestSendMsgSync::test_send_message_sync_calls_rpc`
- `TestSendMsgSync::test_send_message_sync_raises_on_rpc_error`
- `TestSendMsgSync::test_send_message_sync_fallback_subprocess`
- `TestIngestDedup::test_ingest_identical_message_not_added_twice`
- `TestIngestDedup::test_distinct_messages_both_kept`
- `TestIngestDedup::test_dedup_survives_across_callers`
- `TestIngestDedup::test_outgoing_echo_with_different_ts_not_duplicated`
- `TestIngestDedup::test_full_pipeline_multi_attachment_to_cache_and_db`
- `TestIngestDedup::test_outgoing_same_text_far_apart_is_distinct`
- `TestSignalQuoteMedia::test_signal_quote_image_placeholder`
- `TestSignalQuoteMedia::test_signal_quote_image_placeholder_via_fixture`
- `TestSignalQuoteMedia::test_signal_quote_image_with_filename_prepended`
- `TestSignalQuoteMedia::test_signal_sent_message_quote_media`
- `TestSignalQuoteMedia::test_signal_quote_caption_preferred`
- `TestSignalQuoteMedia::test_signal_quote_empty_remains_none`
- `TestSignalQuoteMedia::test_media_message_with_quote_placeholder_not_duplicated`
- `TestSignalQuoteThumbnail::test_extract_quote_thumbnail_writes_valid_file`
- `TestSignalQuoteThumbnail::test_extract_quote_thumbnail_absent_returns_none`
- `TestSignalQuoteThumbnail::test_extract_quote_thumbnail_malformed_base64`
- `TestSignalQuoteThumbnail::test_extract_quote_thumbnail_non_image`
- `TestSignalQuoteThumbnail::test_extract_quote_thumbnail_none_or_empty`
- `TestSignalQuoteThumbnail::test_thumbnailData_fallback`
- `TestSignalQuoteThumbnail::test_thumbnail_raw_bytes`
- `TestSignalQuoteThumbnail::test_envelope_carries_quote_thumbnail_and_content_type`
- `TestSignalQuoteThumbnail::test_envelope_without_thumbnail_no_path`
- `TestSignalQuoteAttachmentId::test_attachment_id_from_id`
- `TestSignalQuoteAttachmentId::test_attachment_id_from_attachmentId`
- `TestSignalQuoteAttachmentId::test_attachment_id_none_when_absent`
- `TestSignalQuoteAttachmentId::test_envelope_carries_quote_attachment_id`

### `tests/test_blob_ops.py` (13 tests)

Imported: `protocols.db`, `protocols.rpc`, `protocols.signal`, `server`, `server.api`, `web.api`, `web.ops`
- `test_attachment_route_equals_operation`
- `test_thumbnail_route_equals_operation`
- `test_unusual_width_serves_the_original`
- `test_missing_attachment_is_not_found_everywhere`
- `test_unknown_protocol_is_not_found`
- `test_attachment_never_leaves_the_media_root`
- `test_real_signal_resolver_rejects_traversal_ids`
- `test_quote_media_route_equals_operation`
- `test_avatar_route_equals_operation`
- `test_signal_avatar_blob_is_a_content_addressed_file`
- `test_quoted_attachment_lookup`
- `test_tui_resolves_attachments_through_the_client`
- `test_adapters_do_not_resolve_attachment_files_themselves`

### `tests/test_bug44_data_loss.py` (6 tests)

Imported: `protocols.db`, `protocols.whatsapp`
- `TestUpdateMessageIdWindow::test_update_message_id_multi_row_same_text`
- `TestUpdateMessageIdWindow::test_update_message_id_outside_window_returns_false`
- `TestDedupDefensiveGuard::test_dedup_skips_partition_with_divergent_timestamps`
- `TestIngestFailedRowReuse::test_ingest_reuses_failed_db_row_without_cache_duplication`
- `TestIngestFailedRowReuse::test_reuse_scoped_to_failed_row_when_sent_idless_is_closer`
- `TestIngestFailedRowReuse::test_receipt_fallback_uniqueness_after_failed_row_reuse`

### `tests/test_cache_debounce.py` (10 tests)

Imported: `protocols.db`
- `TestIncrementalWrites::test_add_message_persists_immediately`
- `TestIncrementalWrites::test_multiple_adds_all_persisted`
- `TestIncrementalWrites::test_no_debounce_attributes`
- `TestMarkAsReadPersistence::test_mark_as_read_persists`
- `TestUpdateMessageStatusPersistence::test_update_status_persists`
- `TestReceiptPersistence::test_receipt_updates_cache_and_db`
- `TestReceiptPersistence::test_receipt_no_match_returns_false`
- `TestIncrementalUnreadBadges::test_incremental_only_updates_given_contact`
- `TestIncrementalUnreadBadges::test_incremental_no_change_returns_early`
- `TestIncrementalUnreadBadges::test_full_update_all_contacts`

### `tests/test_config.py` (25 tests)

Imported: `protocols`
- `TestLoadDotenv::test_load_dotenv_missing`
- `TestLoadDotenv::test_load_dotenv_oserror`
- `TestLoadConfig::test_load_config_missing`
- `TestLoadConfig::test_load_config_invalid_json`
- `TestLoadConfig::test_load_config_oserror`
- `TestGetPrecedence::test_get_env_precedence`
- `TestWhatsAppApiPort::test_api_port_invalid`
- `TestLocalWahaReachable::test_unreachable`
- `TestWebhookPort::test_valid`
- `TestWebhookPort::test_invalid`
- `TestWebhookPort::test_default`
- `TestWebhookUrl::test_explicit`
- `TestWebhookUrl::test_default`
- `TestTelegramApiId::test_env_invalid`
- `TestTelegramApiId::test_config_int`
- `TestTelegramApiId::test_config_invalid`
- `TestTelegramApiId::test_dotenv_invalid`
- `TestTelegramApiHash::test_config_str`
- `TestSignalEnabled::test_default_with_user_number`
- `TestSignalEnabled::test_default_without_user_number`
- `TestSignalEnabled::test_config_user_number_enables`
- `TestSignalEnabled::test_explicit_env_disables`
- `TestSignalEnabled::test_explicit_env_enables_without_number`
- `TestSignalEnabled::test_config_flag_disables`
- `TestSignalEnabled::test_dotenv_flag_disables`

### `tests/test_contact_ops.py` (20 tests)

Imported: `server.api`, `server.bus`, `server.core`, `server.domain`, `server.events_api`
- `TestForProtocol::test_returns_json_wire_copies`
- `TestForProtocol::test_unknown_backend_is_refused`
- `TestRegister::test_registers_a_ghost_in_the_backend_and_returns_it`
- `TestRegister::test_metadata_merged_by_the_backend_is_returned`
- `TestRegister::test_refusals`
- `TestAddressBook::test_scope_force_errors_and_resolver`
- `TestAddressBook::test_no_scope_means_all_protocols`
- `TestRecency::test_bump_recency_raises_but_never_lowers_the_backend_contact`
- `TestRecency::test_bump_recency_unknown_contact_is_a_noop`
- `TestRecency::test_bump_recency_ignores_non_int`
- `TestRecency::test_sync_recency_uses_cache_across_storage_keys`
- `TestRecency::test_core_syncs_recency_before_announcing_the_backend`
- `TestTuiSeeding::test_backend_ready_seeds_wire_copies_not_backend_objects`
- `TestTuiSeeding::test_second_ready_upgrades_recency_and_does_not_duplicate`
- `TestTuiSeeding::test_second_ready_refreshes_name_and_extras_keeping_identity`
- `TestTuiSeeding::test_refused_operation_still_reports_the_backend_done`
- `TestTuiRegisterAndMarkRead::test_picker_contact_becomes_registered_ghost_copy`
- `TestTuiRegisterAndMarkRead::test_mark_read_runs_off_the_caller_thread`
- `TestTuiRegisterAndMarkRead::test_background_failure_is_logged_not_raised`
- `TestTuiAddressBook::test_worker_applies_contacts_and_reports_partial_errors`

### `tests/test_core_shutdown.py` (12 tests)

Imported: `protocols.db`, `server`, `server.bus`, `server.core`
- `test_order_stop_adapters_telegram_prune`
- `test_runs_once`
- `test_each_step_is_best_effort`
- `test_without_telegram_still_prunes`
- `test_sigint_shuts_the_core_down_once`
- `test_sigint_stops_the_web_adapter`
- `test_shutdown_stops_the_transcription_service_after_the_adapters`
- `test_start_initializes_the_transcription_service`
- `test_a_failing_transcription_init_does_not_stop_the_core`
- `test_shutdown_closes_the_api_resources_after_the_adapters`
- `test_close_resources_closes_the_imap_pool_and_wipes_blobs`
- `test_a_failing_resource_close_does_not_skip_the_rest`

### `tests/test_cross_key_dedup.py` (31 tests)

Imported: `protocols.db`, `server.messages`
- `TestContactStorageKeys::test_lid_canonical_includes_cus_alias`
- `TestContactStorageKeys::test_cus_canonical_includes_lid_alias`
- `TestContactStorageKeys::test_non_whatsapp_single_key`
- `TestContactStorageKeys::test_group_is_never_expanded`
- `TestCrossKeyEquivalent::test_same_id_ignores_attachment_id`
- `TestCrossKeyEquivalent::test_different_id_identical_tuple_is_equivalent`
- `TestCrossKeyEquivalent::test_different_id_different_attachment_not_equivalent`
- `TestDedupCrossKeyPure::test_twins_same_id_different_attachment_collapse`
- `TestDedupCrossKeyPure::test_different_id_identical_tuple_collapse`
- `TestDedupCrossKeyPure::test_two_distinct_images_stay`
- `TestDedupCrossKeyPure::test_single_key_is_no_op`
- `TestDedupCrossKeyPure::test_empty_records_is_no_op`
- `TestCrossKeyDedupRows::test_alias_rowid_before_canonical_same_msg_id_collapses`
- `TestCrossKeyDedupRows::test_canonical_first_same_msg_id_collapses`
- `TestCrossKeyDedupRows::test_two_distinct_images_same_key_stay_two`
- `TestCrossKeyDedupRows::test_two_incoming_ok_same_key_stay_two`
- `TestCrossKeyDedupRows::test_multi_attachment_same_key_untouched`
- `TestCrossKeyDedupRows::test_single_key_is_no_op`
- `TestMessagesReadUnion::test_read_union_dedups_twin_and_keeps_distinct`
- `TestMessagesReadUnion::test_single_key_control_does_not_apply_union`
- `TestMessagesReadUnion::test_union_is_symmetric_when_canonical_is_the_alias_key`
- `TestMergeBackendCache::test_cross_key_same_id_different_attachment_collapses`
- `TestMergeBackendCache::test_cross_key_id_divergence_identical_tuple_collapses`
- `TestMergeBackendCache::test_same_key_multi_attachment_not_collapsed`
- `TestMergeBackendCache::test_cross_key_distinct_messages_both_kept`
- `TestOnBackendReady::test_cross_key_union_merges_once_under_canonical`
- `TestOnBackendReady::test_on_backend_ready_cross_key_distinct_messages_both_kept`
- `TestOnBackendReady::test_on_backend_ready_same_key_multi_attachment_intact`
- `test_falsification_distinct_ids_identical_tuple_should_not_collapse`
- `test_falsification_missing_id_different_attachment_should_dedup`
- `test_falsification_same_key_multi_attachment_survives_api`

### `tests/test_db_edit.py` (17 tests)

Imported: `protocols.db`
- `TestUpdateMessageText::test_update_by_msg_id`
- `TestUpdateMessageText::test_update_by_timestamp_with_old_text_guard`
- `TestUpdateMessageText::test_wrong_old_text_returns_false_and_leaves_row_intact`
- `TestUpdateMessageText::test_wrong_old_text_returns_false_on_msg_id_path`
- `TestUpdateMessageText::test_mark_edited_false_sets_edited_to_zero`
- `TestUpdateMessageText::test_no_key_returns_false`
- `TestUpdateMessageText::test_is_mine_guard`
- `TestUpdateMessageText::test_update_scoped_by_protocol`
- `TestUpdateMessageText::test_update_scoped_by_contact`
- `TestUpdateMessageText::test_update_by_msg_id_scoped_by_protocol_and_contact`
- `TestUpdateMessageText::test_update_preserves_timestamp_and_id`
- `TestEditedColumnMigration::test_v2_to_v3_adds_edited_column`
- `TestEditedColumnMigration::test_v2_existing_rows_get_edited_zero`
- `TestEditedColumnMigration::test_migration_idempotent_double_init`
- `TestEditedColumnMigration::test_v3_db_missing_edited_column_still_gets_column`
- `TestEditedColumnMigration::test_load_cache_exposes_edited_as_bool`
- `TestEditedColumnMigration::test_fresh_rows_have_edited_false`

### `tests/test_db_isolation.py` (4 tests)

Imported: `protocols`, `protocols.db`, `protocols.download`, `protocols.signal`, `protocols.whatsapp`
- `test_autouse_fixture_routes_ingest_away_from_real_database`
- `test_autouse_fixture_patches_all_value_imports`
- `test_autouse_fixture_resets_download_globals`
- `test_autouse_fixture_real_db_unchanged`

### `tests/test_db_schema_versioning.py` (7 tests)

Imported: `protocols.db`
- `TestSchemaVersioning::test_init_db_migrates_legacy_and_sets_user_version`
- `TestSchemaVersioning::test_second_init_does_not_churn_index`
- `TestSchemaVersioning::test_init_db_builds_protocol_index_and_columns`
- `TestSchemaVersioning::test_migrate_on_versioned_connection_is_noop`
- `TestSchemaVersioning::test_v4_connection_still_ensures_media_kind_column`
- `TestSchemaVersioning::test_init_db_fast_on_populated_migrated_db`
- `TestSchemaVersioning::test_reply_relationship_survives_cache_reload`

### `tests/test_download_mode.py` (16 tests)

Imported: `protocols.download`
- `TestDownloadMode::test_open_media_uses_macos_open`
- `TestDownloadMode::test_open_media_reports_path_when_xdg_open_exits_unsuccessfully`
- `TestDownloadMode::test_action_download_mode_toggles_and_updates_bar`
- `TestDownloadMode::test_update_download_bar_shows_hint_when_enabled`
- `TestDownloadMode::test_update_download_bar_hides_when_disabled_without_reply`
- `TestDownloadMode::test_update_download_bar_preserves_reply_bar_when_reply_active`
- `TestDownloadMode::test_start_download_serves_text_and_mounts_link`
- `TestDownloadMode::test_start_download_default_text_filename`
- `TestDownloadMode::test_start_download_serves_resolved_attachment`
- `TestDownloadMode::test_start_download_defaults_attachment_protocol_to_signal`
- `TestDownloadMode::test_start_download_does_not_resolve_the_path_itself`
- `TestDownloadMode::test_media_download_resolves_once_and_caches_path`
- `TestDownloadMode::test_start_download_reports_attachment_and_server_errors`
- `TestDownloadMode::test_url_copied_reports_status`
- `TestDownloadMode::test_image_click_resolves_then_downloads`
- `TestDownloadMode::test_image_click_opens_modal_or_reports_missing`

### `tests/test_download_ops.py` (12 tests)

Imported: `protocols.download`, `server.client`, `server.media`
- `TestServeText::test_url_and_file`
- `TestServeText::test_default_filename`
- `TestServeText::test_serves_one_file_at_a_time`
- `TestServeText::test_rejects_names_that_escape_the_download_dir`
- `TestServeText::test_write_error_is_a_backend_error`
- `TestServeAttachment::test_symlinks_into_download_dir`
- `TestServeAttachment::test_missing_attachment_keeps_the_error_text`
- `TestServeAttachment::test_unknown_protocol_is_not_found`
- `TestServeAttachment::test_directory_is_not_served`
- `TestServeAttachment::test_server_start_failure_is_a_backend_error`
- `TestServeAttachment::test_confined_to_media_root`
- `test_download_server_constants_unchanged`

### `tests/test_dynamic_address_book.py` (25 tests)

Imported: `protocols.whatsapp`, `server.address_book_refresh`, `server.bus`, `server.core`
- `TestMessageEventReanchor::test_message_event_updates_tui_object_and_backend_contact`
- `TestMessageEventReanchor::test_message_event_appends_unknown_contact_once`
- `TestContactUpdateEvent::test_appends_new_contact_without_touching_cache`
- `TestContactUpdateEvent::test_updates_existing_display_name_in_place`
- `TestContactUpdateEvent::test_fills_missing_phone_without_overwriting_existing`
- `TestSignalStableKey::test_aci_wins_over_number`
- `TestSignalStableKey::test_number_normalized_when_no_aci`
- `TestSignalStableKey::test_falls_back_to_id`
- `TestSignalDedupOnContactUpdate::test_uuid_to_number_removes_stale_row`
- `TestSignalDedupOnContactUpdate::test_number_match_without_aci_dedups`
- `TestSignalDedupOnContactUpdate::test_different_person_is_not_deduped`
- `TestLooksUnresolved::test_whatsapp`
- `TestLooksUnresolved::test_signal_and_telegram`
- `TestLazyTrigger::test_unresolved_placeholder_schedules_scoped_refresh`
- `TestLazyTrigger::test_resolved_contact_does_not_schedule`
- `TestLazyTrigger::test_trigger_not_gated_on_contact_none`
- `TestLazyTrigger::test_trigger_is_scoped_to_message_protocol`
- `TestScheduler::test_start_is_idempotent`
- `TestScheduler::test_lazy_cooldown_throttles_second_rapid_wake`
- `TestScheduler::test_first_lazy_fires_with_small_monotonic`
- `TestScheduler::test_periodic_cadence_independent_of_lazy_cooldown`
- `TestScheduler::test_stop_unblocks_without_join`
- `TestDynamicAddressBookIntegration::test_picker_worker_forces_address_book`
- `TestDynamicAddressBookIntegration::test_unknown_sender_then_contact_update_renames_row`
- `TestCoreOwnsRefreshWorker::test_start_and_stop_drive_the_worker`

### `tests/test_edit_contract.py` (10 tests)

Imported: `protocols.base`, `protocols.manager`
- `TestEditDefaults::test_edit_message_sync_default_false`
- `TestEditDefaults::test_edit_message_async_default_false`
- `TestEditDefaults::test_apply_edit_default_none`
- `TestEditDefaults::test_apply_edit_accepts_keyword_only_arguments`
- `TestManagerEditRouting::test_unknown_protocol_returns_false`
- `TestManagerEditRouting::test_registered_backend_without_support_returns_false`
- `TestManagerEditRouting::test_router_delegates_with_correct_arguments`
- `TestManagerEditRouting::test_router_returns_backend_result`
- `TestAsyncWrapperDelegation::test_delegates_with_same_arguments`
- `TestAsyncWrapperDelegation::test_delegates_result_false_when_sync_returns_false`

### `tests/test_edit_flow.py` (12 tests)

Imported: `server.messages`, `server.ops`
- `test_edit_gate_not_mine_rejected`
- `test_edit_gate_pending_rejected`
- `test_edit_gate_media_rejected`
- `test_edit_request_populates_state`
- `test_submit_edit_updates_cache_widget_and_backend`
- `test_submit_edit_failure_rolls_back`
- `test_incoming_edit_chat_open_updates_widget_no_new_bubble`
- `test_incoming_edit_chat_closed_updates_cache_only`
- `test_opening_edit_cancels_reply`
- `test_opening_reply_cancels_edit`
- `test_refresh_chat_after_edit_no_duplicates`
- `test_double_edit_consecutive`

### `tests/test_edit_signal.py` (27 tests)

Imported: `protocols`, `protocols.db`, `protocols.rpc`, `protocols.signal`
- `TestRPCSendMessageEdit::test_edit_timestamp_populated`
- `TestRPCSendMessageEdit::test_edit_timestamp_absent_when_none`
- `TestRPCSendMessageEdit::test_edit_timestamp_absent_when_not_passed`
- `TestSendSubprocessEdit::test_argv_contains_edit_timestamp`
- `TestSendSubprocessEdit::test_argv_omits_edit_timestamp_when_none`
- `TestSendSubprocessEdit::test_argv_omits_edit_timestamp_when_not_passed`
- `TestEditMessageSync::test_daemon_rpc_success_returns_true`
- `TestEditMessageSync::test_daemon_rpc_error_raises_runtime_error`
- `TestEditMessageSync::test_subprocess_fallback`
- `TestEditMessageSync::test_non_numeric_message_id_returns_false`
- `TestEditEnvelopeToEvent::test_incoming_edit_produces_single_message_edit_event`
- `TestEditEnvelopeToEvent::test_incoming_edit_no_duplicate_message_event`
- `TestEditEnvelopeToEvent::test_sync_sent_edit_is_mine_true`
- `TestEditEnvelopeToEvent::test_edit_hook_does_not_break_normal_message`
- `TestEditEnvelopeToEvent::test_unknown_contact_edit_returns_empty`
- `TestEditEnvelopeMalformed::test_no_target_sent_timestamp`
- `TestEditEnvelopeMalformed::test_empty_text`
- `TestEditEnvelopeMalformed::test_attachments_present`
- `TestEditEnvelopeMalformed::test_no_edit_message`
- `TestApplyEdit::test_hit_updates_cache_and_db`
- `TestApplyEdit::test_calls_update_message_text_with_correct_params`
- `TestApplyEdit::test_identical_text_returns_none`
- `TestApplyEdit::test_media_message_returns_none`
- `TestApplyEdit::test_unknown_timestamp_returns_none`
- `TestApplyEdit::test_is_mine_mismatch_returns_none`
- `TestApplyEdit::test_is_mine_matching_applies`
- `TestApplyEdit::test_non_numeric_message_id_returns_none`

### `tests/test_edit_whatsapp.py` (38 tests)

Imported: `protocols.db`, `protocols.whatsapp`
- `TestWhatsAppRESTClientEdit::test_edit_message_sends_put_with_encoded_path_and_body`
- `TestWhatsAppRESTClientEdit::test_edit_message_returns_none_on_error`
- `TestExtractMessageId::test_id_dict_prefers_serialized`
- `TestExtractMessageId::test_id_dict_falls_back_to_dollar1`
- `TestExtractMessageId::test_id_dict_hex_only_uses_id`
- `TestExtractMessageId::test_flat_string_id_regression`
- `TestExtractMessageId::test_key_id`
- `TestExtractMessageId::test_no_id_returns_none`
- `TestDetectEdit::test_match_by_id_returns_entry`
- `TestDetectEdit::test_match_by_id_identical_text_returns_none`
- `TestDetectEdit::test_empty_text_returns_none`
- `TestDetectEdit::test_match_by_id_skips_media`
- `TestDetectEdit::test_fallback_single_candidate_returns_hit`
- `TestDetectEdit::test_fallback_two_candidates_in_same_second_returns_none`
- `TestDetectEdit::test_fallback_identical_text_returns_none`
- `TestDetectEdit::test_fallback_not_applied_for_outgoing`
- `TestDetectEdit::test_require_history_source_rejects_live_only_candidate`
- `TestDetectEdit::test_require_history_source_accepts_history_sourced_candidate`
- `TestHandleWebhookEdit::test_incoming_edit_enqueues_message_edit_not_message`
- `TestHandleWebhookEdit::test_retry_is_deduplicated`
- `TestHandleWebhookEdit::test_own_edit_echo_is_absorbed_by_dedup`
- `TestHandleWebhookEdit::test_forwarded_messages_in_quick_succession_are_not_merged_as_edit`
- `TestHandleWebhookEdit::test_synthetic_ack_edit_enqueues_message_edit_not_message`
- `TestFetchHistoryEdit::test_fetch_history_applies_edit_and_keeps_row_count`
- `TestFetchHistoryEdit::test_fetch_history_calls_apply_edit_with_correct_args`
- `TestFetchHistoryEdit::test_forwarded_messages_in_same_history_batch_are_not_merged_as_edit`
- `TestFetchHistoryEdit::test_fetch_history_still_reconciles_edit_against_prior_session_entry`
- `TestEditMessageSync::test_no_rest_returns_false`
- `TestEditMessageSync::test_rest_dict_returns_true`
- `TestEditMessageSync::test_rest_none_returns_false`
- `TestApplyEdit::test_hit_updates_cache_and_db`
- `TestApplyEdit::test_calls_update_message_text_with_msg_id`
- `TestApplyEdit::test_no_id_falls_back_to_timestamp_and_old_text`
- `TestApplyEdit::test_identical_text_returns_none`
- `TestApplyEdit::test_media_returns_none`
- `TestApplyEdit::test_unknown_id_returns_none`
- `TestApplyEdit::test_is_mine_mismatch_returns_none`
- `TestApplyEdit::test_is_mine_matching_applies`

### `tests/test_exit_dialog.py` (25 tests)

Imported: `server.api`, `server.attach`, `server.bus`, `server.clients`, `server.paths`, `server.shutdown`, `server.socket_adapter`, `server.socket_client`, `server.status`
- `test_default_focus_follows_who_spawned_the_server`
- `test_keys`
- `test_other_clients_are_announced_and_quit_needs_confirmation`
- `test_confirmation_can_be_cancelled_with_enter_and_escape`
- `test_quit_without_other_clients_is_immediate`
- `test_other_clients_text`
- `test_shutdown_is_not_supported_without_a_host_hook`
- `test_shutdown_acknowledges_first_then_runs_the_hook`
- `test_clients_counts_socket_connections_and_web_separately`
- `test_clients_without_a_socket_adapter`
- `test_shutdown_over_the_socket_answers_before_the_stop`
- `test_shutdown_response_survives_a_stop_that_follows_at_once`
- `test_adapter_unregisters_its_counter_on_stop`
- `test_wait_server_stopped_follows_the_process`
- `test_wait_server_stopped_without_pid_watches_the_runtime_file`
- `test_quit_key_opens_the_dialog_and_detach_leaves_the_server`
- `test_quit_asks_the_server_to_stop_and_waits_for_it`
- `test_other_clients_trigger_the_warning_and_second_confirmation`
- `test_cancel_stays_and_the_dialog_can_be_reopened`
- `test_ctrl_c_detaches_without_a_dialog_and_never_stops_the_server`
- `test_unreachable_server_exits_without_a_dialog`
- `test_known_lost_connection_exits_without_asking`
- `test_quit_exits_even_if_the_server_vanishes_before_shutdown`
- `test_quit_failure_shows_the_error_and_stays`
- `test_in_process_tui_quits_without_a_dialog`

### `tests/test_failed_send_status.py` (16 tests)

Imported: `server.ops`
- `TestMessageStatusWidgets::test_exact_identity_match_does_not_update_timestamp_collision`
- `TestMessageStatusWidgets::test_fuzzy_status_match_accepts_waha_timestamp_drift_within_two_seconds`
- `TestMessageStatusWidgets::test_fuzzy_status_match_ignores_widget_with_different_text`
- `TestFailedMessageClick::test_clicking_own_failed_message_retries_without_entering_reply_flow`
- `TestFailedMessageClick::test_non_retryable_message_click_keeps_normal_reply_flow`
- `TestRetryGuards::test_retry_returns_without_selected_contact`
- `TestRetryGuards::test_retry_rejects_reloaded_reply_without_quote_timestamp`
- `TestRetryGuards::test_retry_keeps_legacy_telegram_reply_failed_without_message_id`
- `TestRetryGuards::test_retry_telegram_reply_forwards_persisted_message_id_to_backend`
- `TestRetryGuards::test_retry_returns_when_message_is_not_failed`
- `TestRetryGuards::test_retry_that_the_core_refuses_leaves_the_bubble_failed`
- `TestRetryGuards::test_retry_preserves_quote_metadata_for_send_worker`
- `test_telegram_send_result_updates_cache_and_optimistic_widget_message_id`
- `test_telegram_message_id_update_keeps_cache_when_optimistic_widget_is_gone`
- `test_send_without_backend_marks_optimistic_message_as_failed`
- `test_failed_send_shows_the_backend_error_and_fails_the_bubble`

### `tests/test_fix_a_verification.py` (9 tests)

Imported: `protocols`, `protocols.telegram`, `protocols.whatsapp`
- `test_whatsapp_batch_slots_distinct_per_message`
- `test_whatsapp_batch_slot_db_not_overwritten`
- `test_telegram_batch_slot_db_not_overwritten`
- `test_merge_batch_slot_return_semantics`
- `test_merge_batch_slot_rowcount_zero_returns_false_and_warns`
- `test_echo_first_batch_merge_on_cache_only_row_does_not_report_changed`
- `test_telegram_echo_first_merge_pushes_web_event`
- `test_whatsapp_echo_first_merge_pushes_web_event`
- `test_mirror_first_echo_does_not_push_duplicate`

### `tests/test_gmail_address_book.py` (11 tests)

Imported: `server`
- `test_address_book_weighs_sent_over_inbox_and_skips_noise`
- `test_address_book_reads_only_the_latest_window`
- `test_address_book_is_cached_until_refresh`
- `test_address_book_op`
- `test_address_lists_tolerate_empty_items`
- `test_invalid_addresses_are_still_rejected`
- `test_send_accepts_the_autocomplete_output`
- `test_leaving_the_field_drops_the_trailing_comma`
- `test_address_fields_are_tidied_before_sending`
- `test_suggestions_match_names_and_addresses_without_accents`
- `test_formatting_and_inserting_a_suggestion`

### `tests/test_gmail_api.py` (110 tests)

Imported: `protocols`, `server`, `web.gmail_api`
- `test_status_is_offline_and_hides_password`
- `test_status_not_configured`
- `test_list_is_date_descending_and_reports_window`
- `test_list_offset_limit_and_has_more`
- `test_list_ignores_removed_unread_only_filter`
- `test_list_rejects_unsafe_folder`
- `test_filtered_headers_remove_unrequested_headers`
- `test_filtered_headers_parse_like_the_full_message`
- `test_single_part_content_disposition_attachment_is_detected`
- `test_content_type_name_is_detected_as_attachment`
- `test_missing_headers_are_tolerated`
- `test_folded_and_duplicate_headers_match_the_full_message`
- `test_empty_filtered_literal_does_not_crash`
- `test_second_list_is_a_cache_hit`
- `test_cached_list_paginates_with_the_same_contract`
- `test_mutating_a_hit_does_not_corrupt_the_cache`
- `test_account_address_change_is_a_cache_miss`
- `test_account_host_change_is_a_cache_miss`
- `test_account_port_change_is_a_cache_miss`
- `test_delete_invalidates_every_cached_folder`
- `test_bulk_delete_invalidates_every_cached_folder`
- `test_delete_in_another_folder_invalidates_the_cached_folder`
- `test_partial_delete_failure_still_invalidates`
- `test_send_invalidates_the_cached_list`
- `test_mark_read_updates_the_entry_in_place`
- `test_mark_read_on_already_seen_message_keeps_the_entry`
- `test_mark_read_outside_the_window_keeps_the_entry`
- `test_mark_read_does_not_touch_another_folder_entry`
- `test_mark_read_with_empty_cache_discards_the_in_flight_put`
- `test_refresh_forces_a_miss`
- `test_refresh_zero_keeps_the_cache_hit`
- `test_refresh_rejects_non_boolean_values`
- `test_list_cache_expires_after_the_fixed_ttl`
- `test_clear_inbox_cache_forces_a_miss`
- `test_detail_prefers_plain_text_and_hides_payload`
- `test_detail_html_only_is_text_and_sanitized_html`
- `test_detail_missing_uid_is_404`
- `test_attachment_download_is_deterministic`
- `test_attachment_out_of_range_is_404`
- `test_mark_read_sets_seen_flag`
- `test_delete_moves_message_to_trash`
- `test_delete_missing_message_is_404`
- `test_delete_rejects_unsafe_folder`
- `test_bulk_delete_moves_existing_messages_and_reports_missing`
- `test_bulk_delete_deduplicates_uids`
- `test_bulk_delete_rejects_invalid_payload`
- `test_bulk_delete_rejects_invalid_json`
- `test_bulk_delete_rejects_unsafe_folder`
- `test_bulk_delete_all_missing_does_nothing`
- `test_bulk_delete_not_configured_returns_503`
- `test_send_simple_email_uses_starttls_and_fixed_from`
- `test_send_reply_sets_threading_headers_and_quotes_once`
- `test_send_reply_does_not_double_re_prefix`
- `test_send_reply_to_a_mail_with_folded_headers`
- `test_send_forward_of_a_mail_with_a_folded_subject`
- `test_message_detail_headers_are_unfolded`
- `test_decode_mime_header_unfolds`
- `test_send_rejects_header_injection`
- `test_send_rejects_missing_recipient`
- `test_send_rejects_too_many_attachments`
- `test_send_rejects_oversized_attachment`
- `test_send_with_attachment_sanitizes_filename`
- `test_scenario_a_missing_secret_never_serves_a_warm_list`
- `test_scenario_b_wrong_secret_serves_stale_then_502`
- `test_not_configured_returns_503`
- `test_upstream_error_is_generic_and_does_not_leak`
- `test_imap_pool_reuses_one_login_for_two_operations`
- `test_imap_pool_never_exceeds_max_with_four_idle_and_five_requests`
- `test_imap_pool_acquire_timeout_is_busy_and_keeps_the_slot`
- `test_imap_pool_busy_maps_to_502_and_recovers`
- `test_imap_pool_noop_only_after_the_idle_threshold`
- `test_imap_pool_reaps_idle_connections_past_max_idle`
- `test_imap_pool_dead_peer_noop_discards_and_reconnects`
- `test_imap_pool_releases_the_slot_after_five_failed_logins`
- `test_imap_pool_fingerprint_change_with_idle_closes_the_old_account`
- `test_imap_pool_fingerprint_change_with_active_request_never_uses_old_conn`
- `test_imap_pool_clear_marks_active_but_closes_only_on_release`
- `test_imap_pool_close_is_idempotent_and_reusable`
- `test_imap_pool_discards_on_transport_errors`
- `test_imap_pool_keeps_on_logical_errors`
- `test_imap_pool_disabled_uses_one_connection_per_operation`
- `test_close_many_empty_is_a_noop`
- `test_close_many_deadline_respected_by_a_socket_bound_logout`
- `test_close_many_returns_at_the_deadline_when_logout_hangs`
- `test_pool_fingerprint_is_opaque_and_tracks_credentials`
- `test_imap_pool_busy_after_fingerprint_detach_closes_idle_connections`
- `test_p3b_detail_matches_the_full_parser`
- `test_p3b_detail_does_not_download_attachment_parts`
- `test_p3b_attachment_download_fetches_only_its_part`
- `test_p3b_attachment_out_of_range_does_not_download_the_full_body`
- `test_p3b_413_7bit_is_an_early_reject_without_part_fetch`
- `test_p3b_413_base64_qp_unknown_are_post_checked`
- `test_p3b_attachment_at_the_decoded_limit_is_allowed`
- `test_p3b_octets_nil_with_structure_is_partial_with_a_null_size`
- `test_p3b_full_fallback_with_a_structure_uses_body_octets`
- `test_p3b_fallback_without_a_structure_uses_encoded_size`
- `test_p3b_bodystructure_status_no_falls_back_to_full`
- `test_p3b_render_parts_over_the_limit_fall_back_to_full`
- `test_p3b_flag_off_rolls_back_to_the_full_fetch`
- `test_p3b_structure_transport_errors_propagate_and_discard`
- `test_p3b_part_transport_errors_propagate_and_discard`
- `test_p3b_logical_part_error_falls_back_and_keeps_the_connection`
- `test_p3b_single_acquisition_for_detail_and_attachment`
- `test_p3b_detail_and_attachment_can_run_concurrently`
- `test_close_many_returns_at_the_deadline_without_an_event`
- `test_p3b_attachment_part_fetch_failure_falls_back_to_full`
- `test_p3b_attachment_decode_failure_falls_back_to_full`
- `test_p3b_nil_octets_skips_the_7bit_early_reject_and_post_checks`
- `test_p3b_413_8bit_binary_are_early_rejects_without_part_fetch`
- `test_p3b_7bit_octets_at_the_limit_are_fetched_and_allowed`

### `tests/test_gmail_backgrounds.py` (8 tests)

Imported: `server`
- `test_backgrounds_are_deferred_through_the_proxy`
- `test_unsafe_backgrounds_are_dropped`
- `test_only_layout_tags_get_a_background`
- `test_sender_data_bg_is_signed_and_not_duplicated`
- `test_background_layout_properties_are_kept`
- `test_background_layout_properties_are_validated`
- `test_load_images_applies_backgrounds_and_counts_them`
- `test_fit_zooms_wide_mails_down`

### `tests/test_gmail_cache.py` (10 tests)

Imported: `protocols`, `server`
- `test_secret_cache_key_is_keyed_blake2b_hex_string`
- `test_secret_generation_rejects_an_in_flight_put`
- `test_secret_prune_removes_every_expired_entry`
- `test_inbox_epoch_rejects_an_in_flight_put`
- `test_add_flag_is_case_insensitive_and_ordered`
- `test_freeze_and_thaw_do_not_share_mutable_flags`
- `test_freeze_and_thaw_deep_copy_nested_fields`
- `test_inbox_lru_evicts_the_oldest_entry_without_touching_the_epoch`
- `test_inbox_expired_entry_is_removed_on_get`
- `test_account_fingerprint_is_a_hash_of_address_host_and_port`

### `tests/test_gmail_config.py` (37 tests)

Imported: `protocols`
- `test_defaults`
- `test_env_precedence`
- `test_secret_source_getters_resolve_env_config_and_dotenv`
- `test_app_password_file_preferred`
- `test_app_password_file_relative_to_project`
- `test_app_password_file_missing_falls_back`
- `test_app_password_command`
- `test_app_password_command_failure_falls_back`
- `test_app_password_file_precedes_command`
- `test_config_json_fallback`
- `test_dotenv_fallback`
- `test_blank_values_fall_through`
- `test_enabled_requires_both_credentials`
- `test_invalid_ports_and_timeout_fall_back`
- `test_folder_defaults_when_blank`
- `test_source_switch_file_command_env`
- `test_empty_file_falls_back_to_command`
- `test_command_output_is_cached`
- `test_command_cache_key_is_keyed_blake2b_hex_string`
- `test_command_cache_uses_fixed_ttl`
- `test_clear_command_cache_forces_a_new_decrypt`
- `test_clear_during_decrypt_discards_the_in_flight_put`
- `test_failed_command_is_not_cached_and_falls_back`
- `test_empty_command_output_is_not_cached`
- `test_changing_command_prunes_the_expired_entry`
- `test_file_secret_is_reread_even_with_same_size_and_mtime`
- `test_file_source_is_not_cached`
- `test_malformed_quotes_do_not_leak_the_command`
- `test_missing_command_does_not_leak_the_command`
- `test_timeout_does_not_leak_the_command`
- `test_nonzero_returncode_does_not_leak_the_command`
- `test_empty_output_does_not_leak_the_command`
- `test_empty_command_is_rejected_before_subprocess`
- `test_value_error_from_run_is_sanitized`
- `test_unicode_decode_error_is_sanitized`
- `test_file_oserror_does_not_leak_the_path`
- `test_file_unicode_decode_error_does_not_leak_content`

### `tests/test_gmail_html.py` (9 tests)

Imported: `server`
- `test_sanitize_strips_scripts_and_event_handlers`
- `test_sanitize_keeps_safe_links_and_drops_javascript`
- `test_sanitize_strips_styles_and_remote_resources`
- `test_sanitize_keeps_basic_tables`
- `test_sanitize_rejects_oversized_html`
- `test_parse_message_returns_sanitized_html`
- `test_sanitize_keeps_safe_layout_table_attributes`
- `test_sanitize_rejects_unsafe_image_sources`
- `test_image_proxy_token_roundtrip_and_tamper`

### `tests/test_gmail_html_phase1.py` (120 tests)

Imported: `server`
- `test_css_preserves_safe_colors`
- `test_css_preserves_multivalue_font_family`
- `test_css_preserves_quoted_font_family`
- `test_css_font_family_caps`
- `test_quoted_font_family_at_limit_is_idempotent`
- `test_quoted_font_family_over_limit_is_idempotent`
- `test_css_preserves_safe_properties`
- `test_css_preserves_border_radius_shorthand`
- `test_css_preserves_border_radius_longhand`
- `test_css_canonical_output_can_be_longer_than_input`
- `test_css_rejects_unsafe_or_overbound`
- `test_css_rejects_at_rules`
- `test_css_removes_important_flag_and_rejects_bang_values`
- `test_css_keeps_valid_declarations_when_mixed_with_rejected_ones`
- `test_css_empty_or_only_invalid_style_never_emits_empty_attribute`
- `test_css_resolves_escaped_safe_declarations`
- `test_css_rejects_escaped_unsafe_declarations`
- `test_css_rejects_surrogate_escape`
- `test_css_rejects_control_and_noncharacter_values`
- `test_css_skips_comments_and_normalizes_whitespace`
- `test_css_decodes_html_entities_and_double_encoding`
- `test_css_property_names_are_case_insensitive`
- `test_css_rejects_style_breakout_payloads`
- `test_lone_surrogates_fail_closed`
- `test_non_finite_numbers_are_rejected`
- `test_legacy_table_attributes_are_preserved`
- `test_legacy_table_scope_colgroup_lists_and_images`
- `test_legacy_invalid_and_over_bound_values_are_removed`
- `test_legacy_nowrap_is_never_emitted`
- `test_remote_image_src_is_replaced_by_signed_data_src`
- `test_remote_image_data_src_is_signed`
- `test_signed_image_path_is_preserved_unchanged`
- `test_signed_image_path_with_amp_entity_is_preserved`
- `test_tampered_or_multi_dot_signed_tokens_are_removed`
- `test_unsafe_image_sources_are_removed`
- `test_safe_data_uri_is_preserved`
- `test_image_alt_with_gt_and_url_with_gt_are_escaped`
- `test_src_inside_style_is_not_an_attack_surface`
- `test_image_handling_is_idempotent`
- `test_large_signed_token_is_preserved_above_url_length_cap`
- `test_unsigned_remote_url_above_cap_is_removed`
- `test_uppercase_signed_digest_is_rejected`
- `test_tag_cap_boundary`
- `test_style_attribute_cap_boundary`
- `test_depth_cap_boundary`
- `test_tbody_is_excluded_from_tag_and_depth_caps`
- `test_attribute_count_cap_boundary`
- `test_attribute_length_cap_boundary`
- `test_text_cap_boundary_is_independent_of_input_cap`
- `test_void_and_raw_text_tags_do_not_count_raw_content`
- `test_unquoted_attributes_and_case_are_handled`
- `test_cdata_and_comments_are_stable`
- `test_cap_violations_are_fail_closed_and_idempotent`
- `test_idempotence_at_cap_boundaries`
- `test_scanner_treats_every_optional_end_tag_as_flat`
- `test_optional_end_tags_without_closers_do_not_trigger_depth_cap`
- `test_nested_optional_end_tags_below_tag_cap_are_accepted`
- `test_129_nested_div_still_triggers_depth_cap`
- `test_entity_heavy_text_is_idempotent`
- `test_quote_heavy_text_is_idempotent`
- `test_style_with_many_quotes_plus_entity_heavy_text`
- `test_entity_expansion_output_cap_boundary`
- `test_output_cap_is_enforced_after_nh3`
- `test_style_serialized_cap_is_fail_closed`
- `test_style_declaration_cap_is_fail_closed`
- `test_style_input_length_cap_is_enforced`
- `test_style_attribute_escaping_stays_below_hard_attribute_cap`
- `test_kill_switch_removes_styles_but_keeps_legacy_attributes`
- `test_kill_switch_accepts_values_other_than_zero`
- `test_missing_tinycss2_removes_styles_but_keeps_legacy`
- `test_missing_nh3_disables_html_rendering`
- `test_nh3_exception_is_fail_closed`
- `test_pre_scan_exception_is_fail_closed`
- `test_style_callback_exception_is_fail_closed`
- `test_style_validator_exception_is_fail_closed`
- `test_legacy_validator_exception_is_fail_closed`
- `test_public_sanitizer_api_contract`
- `test_output_is_csp_attribute_only`
- `test_extract_html_from_multipart_alternative_is_sanitized`
- `test_parse_message_returns_sanitized_html_with_inline_cid`
- `test_fetch_message_detail_applies_sanitizer`
- `test_p1_950kb_reference_is_correct_and_bounded`
- `test_p2_400_style_worst_case_is_correct_and_bounded`
- `test_p3_adversarial_payloads_are_rejected_quickly`
- `test_gmail_shaped_corpus_stays_safe_and_idempotent`
- `test_find_tag_end_honours_quotes_and_unterminated_tags`
- `test_parse_tag_rejects_empty_and_unterminated_names`
- `test_parse_tag_handles_self_closing_and_odd_attribute_spacing`
- `test_parse_tag_keeps_boolean_attributes`
- `test_find_raw_text_close_reports_missing_and_mismatched_closers`
- `test_render_img_tag_quotes_unquoted_values_and_keeps_boolean_attrs`
- `test_pre_scan_handles_doctype_and_processing_instructions`
- `test_pre_scan_keeps_malformed_less_than_as_text`
- `test_pre_scan_handles_unclosed_raw_text_and_depth`
- `test_cdata_text_cap_is_fail_closed`
- `test_unclosed_raw_text_cap_is_fail_closed`
- `test_closed_raw_text_cap_is_fail_closed`
- `test_malformed_less_than_text_cap_is_fail_closed`
- `test_text_safety_control_and_format_characters`
- `test_unknown_token_type_is_rejected`
- `test_strip_whitespace_skips_leading_whitespace`
- `test_finite_number_and_dimension_helpers_reject_bad_values`
- `test_length_value_number_and_percentage_boundaries`
- `test_css_validator_invalid_values`
- `test_css_validator_valid_values`
- `test_parse_color_rejects_non_color_function`
- `test_style_property_names_mirror_validator_table`
- `test_sanitize_style_parser_exception_is_fail_closed`
- `test_sanitize_style_revalidates_serialized_output`
- `test_sanitize_style_rejects_unencodable_serialized_output`
- `test_overlong_unvalidated_attribute_is_removed`
- `test_nh3_non_string_result_is_fail_closed`
- `test_nh3_unencodable_result_is_fail_closed`
- `test_second_pre_scan_empty_result_is_fail_closed`
- `test_second_pre_scan_exception_is_fail_closed`
- `test_pre_scan_appends_unclosed_raw_text`
- `test_css_validator_additional_invalid_values`
- `test_css_validator_additional_valid_values`
- `test_text_safety_accepts_non_ascii_letters`
- `test_font_family_validator_rejects_unsafe_token_directly`

### `tests/test_gmail_html_phase2.py` (14 tests)

Imported: `server`
- `test_body_colors_survive_as_div_style`
- `test_body_explicit_style_wins_over_bgcolor`
- `test_body_bad_attributes_are_dropped`
- `test_body_quoted_style_is_preserved`
- `test_body_added_style_counts_towards_style_cap`
- `test_font_tag_keeps_valid_attributes`
- `test_font_tag_drops_invalid_attributes`
- `test_dir_attribute_is_allowed_on_any_tag`
- `test_dir_attribute_invalid_is_dropped`
- `test_legacy_bgcolor_accepts_named_colors`
- `test_css_phase2_properties_are_preserved`
- `test_css_phase2_rejects_unsafe_or_overbound`
- `test_background_alias_keeps_declaration_order`
- `test_hidden_preheader_pattern_survives`

### `tests/test_gmail_html_phase3.py` (22 tests)

Imported: `server`
- `test_class_rules_are_inlined`
- `test_body_rule_reaches_the_body_div`
- `test_existing_inline_style_wins_unless_important`
- `test_descendant_and_child_selectors_are_inlined`
- `test_unsafe_or_unsupported_rules_are_not_inlined`
- `test_selector_list_keeps_stateless_selectors`
- `test_remote_stylesheet_link_is_never_loaded`
- `test_style_inside_comment_is_not_applied`
- `test_style_block_in_body_is_inlined`
- `test_kill_switch_disables_inlining`
- `test_styles_kill_switch_also_disables_inlining`
- `test_missing_css_inline_falls_back`
- `test_inliner_failure_falls_back`
- `test_inlined_document_over_budget_falls_back_to_original`
- `test_oversized_stylesheet_is_ignored`
- `test_rule_count_is_capped`
- `test_filter_stylesheet_validates_and_aliases`
- `test_declaration_count_uses_decoded_entities`
- `test_declaration_budget_boundary_is_idempotent`
- `test_inlining_large_newsletter_is_bounded`
- `test_repeated_styles_are_parsed_once_per_document`
- `test_style_cache_does_not_leak_across_documents`

### `tests/test_gmail_html_phase4.py` (18 tests)

Imported: `server`, `web.gmail_api`
- `test_part_token_roundtrip`
- `test_part_token_expires`
- `test_part_token_rejects_malformed_payloads`
- `test_remote_image_urls_are_never_signed_as_parts`
- `test_cid_images_are_rewritten_to_signed_part_urls`
- `test_cid_images_without_mapping_are_dropped`
- `test_parse_full_with_folder_and_uid_links_inline_images`
- `test_parse_message_without_location_drops_inline_images`
- `test_proxy_serves_inline_part_as_raster_image`
- `test_proxy_refuses_non_raster_parts`
- `test_proxy_refuses_oversized_parts`
- `test_proxy_rejects_expired_part_token`
- `test_proxy_maps_missing_part_to_404`
- `test_inline_candidates_are_small_cid_images_not_marked_as_attachments`
- `test_inline_candidates_respect_the_total_budget`
- `test_cache_only_inline_attachments_from_payloads`
- `test_inline_cache_expires_and_is_bounded`
- `test_proxy_serves_prefetched_image_without_imap`

### `tests/test_gmail_html_phase5.py` (7 tests)

Imported: `server`
- `test_quote_containers_are_marked`
- `test_other_elements_are_not_marked`
- `test_sender_supplied_marker_values_are_validated`
- `test_marker_is_added_once_and_survives_inlining`
- `test_gmail_style_quote_folds_with_nested_quotes`
- `test_outlook_quote_folds_everything_after_the_marker`
- `test_message_that_is_only_a_quote_is_not_folded`

### `tests/test_gmail_image_proxy.py` (3 tests)

Imported: `server`, `server.link_preview`, `web.gmail_api`
- `test_invalid_token_returns_404`
- `test_proxy_returns_raster_image_with_safety_headers`
- `test_proxy_maps_fetch_failure_to_502`

### `tests/test_gmail_images_default.py` (26 tests)

Imported: `server`, `server.link_preview`, `web.gmail_api`
- `test_safe_image_url_and_signed_token_contract`
- `test_load_images_sets_hints_before_src_and_skips_existing`
- `test_load_images_cap_and_invalid_urls`
- `test_load_images_error_generation_guard_and_no_error_notice`
- `test_load_images_late_load_after_cancel_does_not_mark_loaded`
- `test_cancel_pending_images_order_and_toggle_reload`
- `test_index_html_removes_toggle_and_bumps_gmail_js`
- `test_gmail_js_has_no_orphan_toggle_remnants`
- `test_server_html_keeps_data_src_without_remote_src`
- `test_docs_no_longer_describe_click_to_load_images`
- `test_raster_dimensions_parses_raster_headers`
- `test_jpeg_parser_ignores_fake_sof_inside_app1`
- `test_jpeg_parser_stops_at_eoi_before_sof`
- `test_jpeg_parser_reads_sof_before_eoi`
- `test_jpeg_parser_skips_restart_markers_before_sof`
- `test_jpeg_dimensions_rejects_non_ff_and_truncated_marker`
- `test_jpeg_dimensions_skips_stuffed_zero_marker`
- `test_jpeg_sof_with_too_short_length_returns_none`
- `test_webp_dimensions_invalid_headers_return_none`
- `test_raster_dimensions_unknown_content_type_and_empty_headers`
- `test_raster_dimensions_truncated_or_malformed_returns_none`
- `test_fetch_external_image_applies_pixel_cap`
- `test_fetch_external_image_rejects_non_raster_body`
- `test_fetch_external_image_pixel_cap_fails_open`
- `test_image_proxy_limits_concurrent_fetches_to_four`
- `test_semaphore_is_not_loop_bound_across_routers`

### `tests/test_gmail_ops.py` (14 tests)

Imported: `protocols.db`, `server`, `server.api`, `server.link_preview`, `server.models_api`
- `test_operations_are_registered`
- `test_error_mapping`
- `test_requests_are_validated`
- `test_attachment_returns_a_private_temporary_blob`
- `test_attachment_download_limit_is_kept`
- `test_stale_blobs_are_pruned_and_close_resources_wipes_them`
- `test_image_fetches_a_signed_url_into_a_blob`
- `test_image_unsigned_or_forged_token_is_never_fetched`
- `test_image_signature_does_not_transfer_to_another_url`
- `test_image_fetch_failure_is_a_backend_error`
- `test_send_reads_the_stored_files`
- `test_send_check_is_side_effect_free`
- `test_send_sizes_are_checked_in_order_before_reading`
- `test_send_rejects_unreadable_or_missing_slots`

### `tests/test_gmail_optimistic_delete.py` (14 tests)

Imported: — (helper of deleted tests)
- `test_delete_removes_the_message_before_the_server_answers`
- `test_failed_delete_puts_the_message_back_in_place`
- `test_retry_after_a_network_error_keeps_the_delete`
- `test_delete_asks_no_confirmation`
- `test_deletes_made_while_one_is_in_flight_share_one_request`
- `test_bulk_delete_is_optimistic_and_leaves_selection_mode`
- `test_bulk_delete_failure_restores_every_message_in_order`
- `test_reload_started_before_the_delete_landed_does_not_resurrect_it`
- `test_load_after_the_delete_settled_clears_the_filter`
- `test_unsettled_delete_stays_hidden_from_any_reload`
- `test_background_reload_keeps_page_and_selection_quietly`
- `test_background_reload_failure_keeps_the_list`
- `test_foreground_reload_still_resets_selection_and_page`
- `test_index_and_handlers_wire_the_optimistic_delete`

### `tests/test_gmail_parse.py` (59 tests)

Imported: `server`
- `test_parse_plain_message`
- `test_parse_summary_uses_headers_without_body`
- `test_parse_summary_hints_multipart_attachments`
- `test_prefers_plain_text_over_html_alternative`
- `test_html_only_is_converted_to_text`
- `test_encoded_headers_are_decoded`
- `test_unknown_charset_header_does_not_crash`
- `test_date_parsing_is_tolerant`
- `test_attachments_have_deterministic_indices_and_sanitized_names`
- `test_inline_image_without_filename_is_indexed_with_content_id`
- `test_plain_text_attachment_is_not_used_as_body`
- `test_public_attachments_strips_payload`
- `test_append_quote_prefixes_every_line_once`
- `test_append_quote_without_user_text`
- `test_validate_header_value_rejects_crlf`
- `test_validate_folder_accepts_safe_names`
- `test_validate_folder_rejects_unsafe_names`
- `test_validate_folder_uses_configured_default`
- `test_parse_fetch_data_handles_flags_only_bytes`
- `test_find_trash_mailbox_is_robust`
- `test_find_trash_mailbox_falls_back_on_list_error`
- `test_bodystructure_parses_quotes_escapes_nil_and_numbers`
- `test_bodystructure_parses_nested_multipart`
- `test_bodystructure_rejects_literals_anywhere`
- `test_extract_bodystructure_is_quote_aware_and_balanced`
- `test_extract_bodystructure_rejects_malformed_metadata`
- `test_extract_bodystructure_respects_the_byte_limit`
- `test_bodystructure_tokenizer_limits`
- `test_parse_bodystructure_never_evaluates`
- `test_index_bodystructure_single_part_root_uses_1`
- `test_index_bodystructure_nested_multipart_numbering`
- `test_index_bodystructure_forward_skips_the_rfc822_container`
- `test_index_bodystructure_root_rfc822_forward_numbers_nested_parts`
- `test_attachment_filenames_decode_rfc2231_and_rfc2047`
- `test_disposition_filename_precedes_content_type_name`
- `test_content_type_name_is_the_filename_fallback`
- `test_nil_filename_falls_back_to_the_content_type_name`
- `test_attachment_without_a_filename_is_still_indexed`
- `test_parse_part_fetch_inherits_the_uid_from_the_first_tuple`
- `test_parse_part_fetch_accepts_a_uid_on_the_trailer`
- `test_parse_part_fetch_rejects_inconsistent_responses`
- `test_parse_part_fetch_rejects_invalid_part_numbers`
- `test_fetch_part_payloads_wraps_status_errors_as_part_fetch_errors`
- `test_fetch_part_payloads_propagates_transport_errors`
- `test_fetch_part_payloads_rejects_invalid_parts_before_fetching`
- `test_fetch_structure_returns_none_for_logical_errors`
- `test_fetch_structure_propagates_transport_errors`
- `test_encoded_size_matches_the_wire_transfer_encoding`
- `test_encoded_size_is_none_for_an_unknown_transfer_encoding`
- `test_encoded_size_handles_a_missing_transfer_encoding_as_7bit`
- `test_golden_gmail_bodystructure_indices`
- `test_golden_gmail_base64_octets_match_encoded_size`
- `test_attachment_filename_is_sanitized_by_the_partial_path`
- `test_extract_bodystructure_only_rejects_syntactic_literals`
- `test_parse_structure_fetch_builds_headers_and_index`
- `test_parse_structure_fetch_accepts_headers_in_a_later_tuple`
- `test_parse_structure_fetch_returns_none_for_logical_failures`
- `test_plain_filename_with_two_apostrophes_is_preserved`
- `test_attachment_size_is_null_when_the_transfer_encoding_is_unknown`

### `tests/test_gmail_perf_review.py` (4 tests)

Imported: `protocols`, `server`
- `test_mark_read_advances_epoch_for_a_target_outside_the_window`
- `test_mark_read_advances_epoch_for_an_already_seen_target`
- `test_secret_cache_is_memory_only`
- `test_inbox_cache_is_memory_only`

### `tests/test_gmail_reader_layout.py` (7 tests)

Imported: `server`
- `test_mark_inline_images_requires_a_cid_reference_and_an_image`
- `test_parse_message_flags_inline_image_and_keeps_regular_attachment`
- `test_reader_markup_has_sticky_actions_and_attachments_before_body`
- `test_split_addresses_keeps_commas_inside_quotes_and_brackets`
- `test_render_recipients_collapses_long_lists`
- `test_attachments_render_as_chips_and_collapse_when_many`
- `test_inline_images_are_grouped_behind_a_link`

### `tests/test_gmail_remote_image_cache.py` (7 tests)

Imported: `server`, `server.link_preview`, `server.ops`
- `test_remote_image_urls_skip_inline_parts_and_duplicates`
- `test_concurrent_requests_share_one_fetch`
- `test_cancelled_request_does_not_cancel_the_shared_fetch`
- `test_failed_fetch_is_not_cached`
- `test_remote_cache_expires_and_is_bounded`
- `test_opening_a_message_prefetches_its_remote_images`
- `test_prefetch_can_be_disabled`

### `tests/test_gmail_ui_actions.py` (14 tests)

Imported: `server`
- `test_mark_unread_clears_seen_and_patches_the_cache`
- `test_remove_flag_is_case_insensitive`
- `test_archive_removes_the_inbox_label`
- `test_archive_only_applies_to_the_inbox`
- `test_archive_missing_message_is_not_found`
- `test_ops_call_the_action_and_poke_the_unread_count`
- `test_forward_adds_header_block_text_and_original_files`
- `test_forward_keeps_an_existing_prefix`
- `test_forward_respects_the_attachment_limits`
- `test_reply_and_forward_cannot_be_combined`
- `test_send_op_passes_the_forward_fields`
- `test_reply_all_recipients_skip_own_address_and_duplicates`
- `test_subject_prefixes_and_forwarded_file_count`
- `test_static_wiring`

### `tests/test_gmail_unread.py` (16 tests)

Imported: `server`, `server.api`, `server.bus`, `server.core`, `server.gmail_unread`, `web.legacy_events`
- `test_unread_counts_only_the_200_listed_messages`
- `test_small_mailbox_window_starts_at_one`
- `test_empty_mailbox_skips_the_search`
- `test_fetch_unread_status_rejects_bad_replies`
- `test_watcher_publishes_changes_and_flags_new_mail`
- `test_watcher_does_nothing_when_gmail_is_not_configured`
- `test_zero_interval_disables_the_worker`
- `test_poll_interval_from_environment`
- `test_running_worker_polls_and_wakes_on_poke`
- `test_unread_op_when_gmail_is_not_configured`
- `test_unread_op_uses_the_worker_value_or_fetches`
- `test_actions_that_change_the_count_poke_the_worker`
- `test_core_owns_the_watcher_without_starting_threads`
- `test_published_event_reaches_the_legacy_websocket_frame`
- `test_tab_dots_gmail_dot_and_app_badge_total`
- `test_static_wiring`

### `tests/test_gmail_warm_cache.py` (6 tests)

Imported: — (helper of deleted tests)
- `test_skips_when_gmail_is_not_configured`
- `test_skips_when_secret_comes_from_a_file`
- `test_skips_when_secret_command_is_empty`
- `test_tty_success_is_silent_and_has_no_timeout`
- `test_tty_empty_output_warns_with_manual_command`
- `test_non_tty_timeout_warns_without_crashing`

### `tests/test_image_async_download.py` (9 tests)

Imported: `protocols`, `protocols.rpc`
- `TestImageAsyncDownload::test_render_mounts_placeholder_without_sync_download`
- `TestImageAsyncDownload::test_resolve_updates_widget_with_path`
- `TestImageAsyncDownload::test_resolve_none_keeps_fallback_and_clickable`
- `TestImageAsyncDownload::test_finish_resolve_on_unmounted_widget_is_noop`
- `TestImageAsyncDownload::test_signal_existing_file_resolves`
- `TestImageAsyncDownload::test_signal_missing_file_falls_back`
- `TestImageAsyncDownload::test_telegram_existing_file_resolves`
- `TestImageAsyncDownload::test_telegram_missing_file_falls_back`
- `TestImageAsyncDownload::test_update_attachment_sets_path_and_click_emits_new_path`

### `tests/test_image_caption.py` (20 tests)

Imported: `protocols`
- `TestCacheImageAlignment::test_cached_sent_image_is_msg_right`
- `TestCacheImageAlignment::test_cached_received_image_is_msg_left`
- `TestImageCaptionResolver::test_signal_body_caption`
- `TestImageCaptionResolver::test_signal_synthetic_text_not_caption`
- `TestImageCaptionResolver::test_signal_per_attachment_caption`
- `TestImageCaptionResolver::test_whatsapp_caption_from_attachment_info`
- `TestImageCaptionResolver::test_whatsapp_technical_attachment_info_not_caption`
- `TestImageCaptionResolver::test_whatsapp_synthetic_text_never_caption`
- `TestImageCaptionResolver::test_telegram_text_is_caption`
- `TestImageCaptionResolver::test_telegram_empty_text_no_caption`
- `TestCaptionBubbleLive::test_live_received_image_with_caption_shows_bubble`
- `TestCaptionBubbleLive::test_live_sent_image_with_caption_bubble_msg_right`
- `TestCaptionBubbleLive::test_live_image_without_caption_shows_no_bubble`
- `TestCaptionBubbleLive::test_live_telegram_photo_caption_in_text`
- `TestCaptionBubbleCache::test_cached_image_with_caption_shows_bubble`
- `TestCaptionBubbleCache::test_cached_image_without_caption_single_widget`
- `TestCaptionBubbleCache::test_cached_telegram_photo_no_double_emoji`
- `TestCaptionBubbleCache::test_cached_telegram_photo_caption_bubble`
- `TestCaptionBubbleCache::test_cached_signal_body_caption`
- `TestCaptionBubbleCache::test_cached_gif_caption_matches_image_caption`

### `tests/test_image_detect.py` (40 tests)

Imported: `protocols`
- `TestOverride::test_kitty`
- `TestOverride::test_catimg`
- `TestOverride::test_off`
- `TestOverride::test_override_wins_over_non_tty`
- `TestOverride::test_override_case_insensitive`
- `TestOverride::test_auto_falls_through_to_detection`
- `TestNonTty::test_non_tty_returns_catimg`
- `TestNonTty::test_non_tty_never_queries`
- `TestTmuxScreenGuard::test_tmux_present`
- `TestTmuxScreenGuard::test_term_screen`
- `TestTmuxScreenGuard::test_term_screen_256color`
- `TestTmuxScreenGuard::test_tmux_never_queries`
- `TestKittyGate::test_xterm_kitty_query_ok`
- `TestKittyGate::test_xterm_kitty_query_ko_with_catimg`
- `TestKittyGate::test_xterm_kitty_query_ko_without_catimg`
- `TestFalsePositiveTerm::test_iterm2_xterm_256color_never_kitty`
- `TestFalsePositiveTerm::test_xterm_256color_without_catimg`
- `TestFalsePositiveTerm::test_xterm_256color_never_queries`
- `TestWezTermGate::test_wezterm_pane_marker`
- `TestWezTermGate::test_wezterm_unix_socket_marker`
- `TestWezTermGate::test_wezterm_term_program`
- `TestWezTermGate::test_term_wezterm`
- `TestWezTermGate::test_query_ko_with_catimg`
- `TestWezTermGate::test_query_ko_without_catimg`
- `TestWezTermGate::test_tmux_guard_prioritary_never_queries`
- `TestWezTermGate::test_query_called_exactly_once`
- `TestIsWezTerm::test_markers`
- `TestIsWezTerm::test_not_wezterm`
- `TestFallback::test_catimg_present`
- `TestFallback::test_catimg_absent`
- `TestQueryKittyOk::test_non_tty_returns_false`
- `TestConfigGetters::test_image_protocol_default`
- `TestConfigGetters::test_image_protocol_env`
- `TestConfigGetters::test_image_protocol_config`
- `TestConfigGetters::test_image_protocol_env_precedence`
- `TestConfigGetters::test_thumbnail_max_lines_default`
- `TestConfigGetters::test_thumbnail_max_lines_env`
- `TestConfigGetters::test_thumbnail_max_lines_invalid`
- `TestConfigGetters::test_thumbnail_max_cols_default`
- `TestConfigGetters::test_thumbnail_max_cols_config`

### `tests/test_incoming_attachment_dedup.py` (36 tests)

Imported: `protocols`, `server.messages`
- `test_boot_dedup_fuses_synthetic_and_blank_pair`
- `test_boot_dedup_keeps_highest_status_and_no_downgrade`
- `test_boot_dedup_is_idempotent`
- `test_boot_dedup_skips_real_caption_pair`
- `test_boot_dedup_skips_two_synthetic_rows`
- `test_boot_dedup_outside_window_not_fused`
- `test_boot_dedup_skips_ambiguous_three_row_group`
- `test_boot_dedup_skips_distinct_attachment_ids`
- `test_boot_dedup_preserves_survivor_batch_slot`
- `test_boot_dedup_merges_quote_fields_and_edited`
- `test_boot_dedup_prefers_display_sender`
- `test_run_startup_maintenance_invokes_incoming_dedup`
- `test_boot_dedup_ts_zero_twin_fused`
- `test_boot_dedup_both_ts_zero_fused`
- `test_boot_dedup_survivor_prefers_real_timestamp`
- `test_boot_dedup_fuses_synth_and_filename_twin`
- `test_boot_dedup_skips_synth_and_url_caption`
- `test_boot_dedup_skips_media_url_caption_no_attachment_match`
- `test_boot_dedup_merges_attachment_info`
- `test_web_intra_key_echo_caption_survivor_keeps_attachment`
- `test_web_intra_key_caption_only_in_text_row_transferred`
- `test_web_intra_key_two_no_attachment_divergent_texts_not_merged`
- `test_web_intra_key_synthetic_vs_blank_no_attachment_merges`
- `test_web_intra_key_distinct_attachment_ids_stay_separate`
- `test_web_intra_key_batch_slot_only_from_survivor`
- `test_attachment_display_name_url_falls_back_to_basename`
- `test_attachment_display_name_mime_falls_back_to_basename`
- `test_attachment_display_name_keeps_plain_filename`
- `test_web_synth_url_caption_shows_filename_not_url`
- `test_web_intra_key_caption_recovered_from_same_attachment_twin`
- `test_web_intra_key_url_caption_recovered_from_same_attachment_twin`
- `test_web_intra_key_divergent_captions_not_merged`
- `test_web_intra_key_media_and_divergent_text_rows_not_merged`
- `test_web_intra_key_single_text_survives_read_blank_survivor`
- `test_web_intra_key_caption_transfer_keeps_survivor_identity`
- `test_web_intra_key_dedup_idempotent`

### `tests/test_link_service.py` (73 tests)

Imported: `protocols`, `protocols.db`, `server.api`, `server.bus`, `server.client`, `server.core`, `server.link`
- `TestSignalLinkPersistence::test_persists_only_linked_account_and_preserves_config`
- `TestSignalLinkPersistence::test_single_linked_account_does_not_require_phone_input`
- `TestSignalLinkPersistence::test_new_account_is_selected_from_pre_link_snapshot`
- `TestSignalLinkPersistence::test_new_account_overrides_stale_entered_phone`
- `TestSignalLinkPersistence::test_multiple_accounts_without_match_are_rejected`
- `TestSignalLinkPersistence::test_environment_account_conflict_is_rejected`
- `TestSignalLinkPersistenceEdgeCases::test_list_signal_accounts_failure`
- `TestSignalLinkPersistenceEdgeCases::test_single_account_matching_previous_snapshot`
- `TestSignalLinkPersistenceEdgeCases::test_no_accounts_reported_is_rejected`
- `TestSignalLinkPersistenceEdgeCases::test_persist_rejects_invalid_config`
- `TestSignalLinkPersistenceEdgeCases::test_persist_rejects_non_dict_config`
- `TestSignalLinkPersistenceEdgeCases::test_persist_creates_config_when_missing`
- `TestSignalLinkPersistenceEdgeCases::test_persist_cleans_temp_file_on_replace_failure`
- `TestStatus::test_reports_configuration_without_io`
- `TestStatus::test_unconfigured_protocols`
- `TestSignalFlow::test_start_extracts_link_and_names_the_device`
- `TestSignalFlow::test_snapshot_failure_does_not_block_the_qr`
- `TestSignalFlow::test_no_link_url_raises_and_kills_the_process`
- `TestSignalFlow::test_poll_follows_the_subprocess`
- `TestSignalFlow::test_poll_without_a_link_is_an_error`
- `TestSignalFlow::test_cancel_terminates_a_running_subprocess_only`
- `TestSignalFlow::test_cancel_survives_terminate_failure`
- `TestSignalFlow::test_cancel_while_starting_does_not_leak_the_process`
- `TestSignalFlow::test_new_start_replaces_and_stops_the_previous_link`
- `TestSignalFlow::test_abandoned_sessions_are_reaped`
- `TestSignalFlow::test_cancel_all`
- `TestSignalFlow::test_finalize_updates_active_backend`
- `TestSignalFlow::test_finalize_rejects_switch_before_persisting`
- `TestSignalFlow::test_finalize_without_backend`
- `TestSignalFlow::test_failed_finalize_keeps_the_session`
- `TestWhatsAppFlow::test_working_session_needs_no_qr`
- `TestWhatsAppFlow::test_current_qr_png_is_base64`
- `TestWhatsAppFlow::test_text_qr_is_passed_through`
- `TestWhatsAppFlow::test_dead_session_is_restarted_with_reset`
- `TestWhatsAppFlow::test_restart_failure`
- `TestWhatsAppFlow::test_falls_back_to_a_fresh_qr_without_reset`
- `TestWhatsAppFlow::test_no_qr_available`
- `TestWhatsAppFlow::test_backend_missing`
- `TestWhatsAppFlow::test_poll_done_when_working_with_identity`
- `TestWhatsAppFlow::test_poll_working_without_identity_is_not_done`
- `TestWhatsAppFlow::test_qr_is_not_renewed_before_a_minute`
- `TestWhatsAppFlow::test_qr_is_renewed_after_a_minute_without_reset`
- `TestWhatsAppFlow::test_qr_not_renewed_in_other_states`
- `TestWhatsAppFlow::test_missing_fresh_qr_is_shown_as_text`
- `TestWhatsAppFlow::test_renewal_failure_retries_on_the_next_poll`
- `TestWhatsAppFlow::test_poll_without_rest_is_waiting`
- `TestTelegramFlow::test_start_returns_the_login_url`
- `TestTelegramFlow::test_already_logged_in_is_info`
- `TestTelegramFlow::test_start_errors`
- `TestTelegramFlow::test_missing_backend`
- `TestTelegramFlow::test_poll_done_when_connected`
- `TestTelegramFlow::test_2fa_is_reported_once`
- `TestTelegramFlow::test_qr_is_renewed_after_a_minute_and_rearms_2fa`
- `TestTelegramFlow::test_no_renewal_before_a_minute`
- `TestTelegramFlow::test_renewal_messages`
- `TestTelegramFlow::test_renewal_failure_retries_on_the_next_poll`
- `TestTelegramFlow::test_submit_2fa`
- `TestTelegramFlow::test_submit_2fa_without_backend`
- `test_whatsapp_working_requires_authenticated_identity`
- `TestOps::test_status_shape`
- `TestOps::test_start_poll_cancel_round_trip`
- `TestOps::test_default_device_name`
- `TestOps::test_start_errors_keep_the_user_facing_text`
- `TestOps::test_unknown_protocol_is_invalid`
- `TestOps::test_finalize_error_is_a_backend_error`
- `TestOps::test_finalize_returns_the_account`
- `TestOps::test_submit_2fa`
- `TestOps::test_reconnect_goes_to_the_host`
- `TestOps::test_reconnect_none_is_still_ok`
- `TestOps::test_reconnect_rejects_unknown_protocols`
- `TestOps::test_reconnect_unsupported_host`
- `TestOps::test_reconnect_only_reconnects_the_named_backends`
- `test_shutdown_cancels_running_links`

### `tests/test_live_quote_media.py` (7 tests)

Imported: `protocols.rpc`, `protocols.signal`, `protocols.telegram`, `protocols.whatsapp`, `protocols.whatsapp_events`
- `test_e1_signal_quote_media_no_caption_wire_empty`
- `test_e2_signal_quote_media_with_caption_wire_is_caption`
- `test_e3_signal_text_reply_unchanged`
- `test_e7_signal_retry_after_failure_wire_empty`
- `test_e5_whatsapp_quote_photo_reply_to_baileys_id`
- `test_e6_telegram_quote_photo_reply_to_numeric_id`
- `test_e4_ingest_manual`

### `tests/test_logs_diagnostics.py` (7 tests)

Imported: `protocols.config`, `protocols.db`, `server`, `server.logs`, `server.logs_clear`, `transcription`, `transcription.config`, `web.api`, `web.auth`
- `test_buffer_keeps_records_and_redacts_secrets`
- `test_install_is_idempotent`
- `test_server_logs_op_after_and_level`
- `test_diagnostics_unconfigured`
- `test_diagnostics_configured_shows_service_and_recent`
- `test_check_without_key`
- `test_rest_routes`

### `tests/test_media_kind.py` (7 tests)

Imported: `protocols.db`, `protocols.signal`, `protocols.telegram`
- `test_media_kind_from_mime`
- `test_msg_type_for_media_kind`
- `test_signal_attachment_media_kind`
- `test_telegram_document_attribute_media_kind`
- `test_media_kind_backfill_is_idempotent`
- `test_media_kind_correction_reclassifies_migrated_audio_documents`
- `test_media_kind_correction_backfills_null_audio_on_v4_database`

### `tests/test_media_lazy_open.py` (30 tests)

Imported: `protocols.config`, `protocols.db`, `protocols.whatsapp`, `server.config`, `server.messages`
- `test_pending_payload_for_media_rows_without_attachment`
- `test_pending_payload_keeps_caption_and_document_name`
- `test_media_row_without_msg_id_gets_a_visible_placeholder`
- `test_other_protocols_never_get_pending_tokens`
- `test_token_round_trip_and_rejections`
- `test_invalid_token_is_not_found_without_waha_calls`
- `test_lazy_resolve_downloads_once_persists_and_heals_in_place`
- `test_resolved_event_heals_the_cache_row_as_changed`
- `test_lazy_resolve_finds_the_row_under_an_alias_storage_key`
- `test_concurrent_requests_for_the_same_media_are_coalesced`
- `test_different_media_are_downloaded_one_at_a_time`
- `test_failure_returns_none_and_is_negatively_cached`
- `test_failed_download_is_retryable_after_the_negative_ttl`
- `test_stopped_backend_does_not_resolve_and_releases_waiters`
- `test_pending_token_has_no_remote_chunk`
- `test_refresh_history_on_chat_open_is_metadata_only_for_whatsapp`
- `test_refresh_history_for_telegram_is_unchanged`
- `test_fetch_history_ingests_pending_media_without_scheduling_the_resolver`
- `test_chat_open_page_triggers_zero_waha_media_downloads`
- `test_startup_resync_keeps_skipping_pending_media`
- `test_kill_switch_reverts_fetch_and_pending_payload`
- `test_lazy_media_config`
- `test_web_pending_image_loads_only_when_visible`
- `test_web_media_placeholder_is_never_an_empty_bubble`
- `test_tui_changed_media_event_updates_the_existing_entry_and_rerenders`
- `test_tui_changed_media_event_never_swaps_a_real_attachment`
- `test_tui_pending_media_widget_gets_a_lazy_token_not_stored_in_cache`
- `test_tui_follows_the_server_lazy_media_switch`
- `test_server_config_exposes_the_lazy_media_switch`
- `test_tui_applies_the_lazy_media_switch_from_server_config`

### `tests/test_media_prune.py` (11 tests)

Imported: `protocols`, `protocols.media_prune`
- `test_resolve_protected_basename_and_urls`
- `test_resolve_telegram_refs`
- `test_compute_orphans_protection_grace_and_symlink`
- `test_collect_refs_single_protocol`
- `test_prune_media_missing_db_never_deletes_files`
- `test_prune_media_counts_per_file_classification_and_stat_errors`
- `test_prune_media_dry_run_execute_metrics_and_grace`
- `test_prune_media_missing_scope_filter_and_quote_thumbs`
- `test_prune_media_unlink_error_is_nonfatal`
- `test_prune_cache_always_calls_media_prune`
- `test_prune_cache_media_failure_does_not_propagate`

### `tests/test_merge_cache_edit.py` (13 tests)

Imported: — (helper of deleted tests)
- `TestEditAwareMerge::test_incoming_edit_same_id_updates_text_and_marks_edited`
- `TestEditAwareMerge::test_incoming_same_id_same_text_is_noop`
- `TestEditAwareMerge::test_outgoing_edit_same_id_updates_text`
- `TestEditAwareMerge::test_incoming_different_id_and_text_is_new_message_even_with_close_ts`
- `TestEditAwareMerge::test_incoming_same_text_different_id_within_window_is_deduped`
- `test_merge_ui_two_identical_outgoing_stay_two`
- `TestStatusUpgrade::test_status_only_upgrade_returns_false_but_updates_status`
- `TestStatusUpgrade::test_status_never_downgrades`
- `TestMediaGuard::test_media_message_text_not_updated_on_edit`
- `TestMediaGuard::test_distinct_attachments_same_id_are_kept_both`
- `TestMediaGuard::test_same_attachment_same_id_still_deduped`
- `TestMediaGuard::test_ack_echo_without_attachment_id_still_deduped_by_id`
- `TestLoadWorkerRemount::test_load_messages_worker_remounts_window_when_cache_changed`

### `tests/test_message_window_ops.py` (24 tests)

Imported: `server.api`
- `TestWindowOperation::test_returns_core_cache_rows_in_tui_shape`
- `TestWindowOperation::test_rows_that_are_not_persisted_are_included`
- `TestWindowOperation::test_rows_are_copies`
- `TestWindowOperation::test_whatsapp_read_union_collapses_the_ghost_twin`
- `TestWindowOperation::test_ghost_id_opens_the_canonical_chat`
- `TestWindowOperation::test_refresh_sees_the_rows_the_download_adds`
- `TestWindowOperation::test_failed_download_is_not_reported_as_refreshed`
- `TestWindowOperation::test_backend_without_history_download_is_not_refreshed`
- `TestWindowOperation::test_unknown_backend_is_empty`
- `TestWindowOperation::test_invalid_protocol_is_refused`
- `TestSnapshotOperation::test_non_whatsapp_keeps_every_key`
- `TestSnapshotOperation::test_whatsapp_union_appears_once_under_the_canonical_id`
- `TestSnapshotOperation::test_rows_are_copies`
- `TestSnapshotOperation::test_unknown_backend_is_empty`
- `TestBackendReadyThroughClient::test_ghost_and_lid_history_lands_once_under_the_canonical_key`
- `TestBackendReadyThroughClient::test_second_ready_adds_no_duplicate_bubbles`
- `TestBackendReadyThroughClient::test_status_is_never_downgraded_by_a_re_ready`
- `TestBackendReadyThroughClient::test_optimistic_row_learns_its_id_instead_of_duplicating`
- `TestBackendReadyThroughClient::test_unpersisted_core_rows_are_mirrored`
- `TestBackendReadyThroughClient::test_refused_snapshot_still_completes_the_ready`
- `TestLoadWorkerThroughClient::test_worker_merges_the_downloaded_union_without_touching_the_backend`
- `TestLoadWorkerThroughClient::test_failed_download_stays_on_the_local_cache`
- `TestLoadWorkerThroughClient::test_empty_download_retries_three_times_then_gives_up`
- `test_chat_loading_modules_do_not_read_backends`

### `tests/test_migrate_protocol.py` (12 tests)

Imported: `migrate_cache_protocol`, `protocols.db`
- `TestMigrateProtocol::test_adds_protocol_column`
- `TestMigrateProtocol::test_defaults_to_signal`
- `TestMigrateProtocol::test_idempotent`
- `TestMigrateProtocol::test_index_uses_protocol`
- `TestMigrateProtocol::test_no_db_is_noop`
- `TestMigrateProtocolNewDB::test_keeps_existing_protocol`
- `TestInitDBAutoMigrate::test_init_db_adds_protocol_column`
- `TestInitDBAutoMigrate::test_init_db_defaults_existing_rows_to_signal`
- `TestInitDBAutoMigrate::test_init_db_rebuilds_protocol_index`
- `TestInitDBAutoMigrate::test_init_db_then_load_and_prune_no_error`
- `TestInitDBAutoMigrate::test_init_db_idempotent`
- `test_main_guard`

### `tests/test_migrate_quote_text_backfill.py` (17 tests)

Imported: `migrate_quote_text_backfill`, `protocols`
- `test_dry_run_writes_nothing_and_creates_no_backup`
- `test_apply_creates_backup_and_is_idempotent`
- `test_auto_quote_is_skipped`
- `test_real_caption_is_preserved_and_used_as_quote_text`
- `test_quoted_not_found_is_skipped`
- `test_quoted_ambiguous_is_skipped`
- `test_n1_synthetic_placeholder_without_content_type_is_skipped`
- `test_n2_real_caption_ending_with_placeholder_is_preserved`
- `test_raw_audio_identity_is_never_written_verbatim`
- `test_contact_and_limit_filters_are_applied`
- `test_n3_descriptor_parser_is_robust`
- `test_bug2_audio_identity_with_attachment_id_derives_placeholder`
- `test_bug2_audio_identity_without_attachment_id_uses_fallback`
- `test_bug2_audio_caption_with_spaces_is_preserved_with_attachment_id`
- `test_bug2_audio_caption_with_spaces_is_preserved_without_attachment_id`
- `test_legacy_per_component_audio_identity_is_synthetic`
- `test_63745_like_auto_quote_is_skipped`

### `tests/test_migrate_reactions_cleanup.py` (2 tests)

Imported: `migrate_reactions_cleanup`, `protocols.db`
- `test_dry_run_reports_candidates_without_deleting`
- `test_apply_removes_only_candidates_and_reports_protocol_counts`

### `tests/test_migrate_sqlite.py` (5 tests)

Imported: `migrate_cache_sqlite`
- `TestMigrate::test_migrate_creates_db`
- `TestMigrate::test_migrate_backs_up_json`
- `TestMigrate::test_migrate_no_json_is_noop`
- `TestMigrate::test_migrate_preserves_optional_fields`
- `test_main_guard`

### `tests/test_migrate_status.py` (6 tests)

Imported: `migrate_cache_status`
- `TestMigrateStatus::test_no_cache_file_noop`
- `TestMigrateStatus::test_adds_status_to_sent_messages`
- `TestMigrateStatus::test_received_messages_not_migrated`
- `TestMigrateStatus::test_message_with_status_untouched`
- `TestMigrateStatus::test_all_already_migrated_noop`
- `test_main_guard`

### `tests/test_migrate_wa_e2e_notice.py` (10 tests)

Imported: `migrate_wa_e2e_notice`, `protocols.db`
- `test_dry_run_reports_candidates_without_writing`
- `test_apply_rewrites_only_candidates`
- `test_apply_is_idempotent`
- `test_missing_db_raises_file_not_found`
- `test_missing_db_main_returns_1`
- `test_operational_error_main_returns_2`
- `test_selection_filters`
- `test_cli_dry_run_default`
- `test_cli_apply_prints_updated`
- `test_main_guard_missing_db_returns_1`

### `tests/test_migrate_whatsapp_media_name.py` (8 tests)

Imported: `migrate_whatsapp_media_name`, `protocols`
- `test_dry_run_writes_nothing_and_creates_no_backup`
- `test_apply_moves_caption_to_text_and_uses_basename`
- `test_synthetic_text_is_replaced_by_caption`
- `test_url_with_query_uses_path_basename`
- `test_real_text_is_preserved`
- `test_sent_mirror_uses_kind_label`
- `test_missing_attachment_id_uses_file_label`
- `test_image_row_untouched`

### `tests/test_open_or_create.py` (11 tests)

Imported: `protocols.telegram`, `server.core`
- `TestEnsureContactSelectable::test_known_contact_returns_canonical_object`
- `TestEnsureContactSelectable::test_new_contact_ghost_append_register_and_render`
- `TestEnsureContactSelectable::test_missing_backend_returns_none_without_mutation`
- `TestGhostWhatsAppNumberCheck::test_ghost_id_is_phone_at_c_us`
- `TestGhostWhatsAppNumberCheck::test_false_warns`
- `TestGhostWhatsAppNumberCheck::test_true_no_warning`
- `TestGhostWhatsAppNumberCheck::test_none_no_warning`
- `TestGhostWhatsAppNumberCheck::test_check_error_no_warning`
- `TestSendGhostTelegram::test_send_ghost_uses_input_peer_user`
- `TestSendGhostTelegram::test_send_ghost_without_access_hash_raises`
- `TestSelectContactRegression::test_select_contact_missing_backend_does_not_open`

### `tests/test_outgoing_status_fallback.py` (9 tests)

Imported: `protocols.db`, `server.outgoing`
- `TestUpdateMessageStatusByText::test_updates_row_when_timestamp_mismatch`
- `TestUpdateMessageStatusByText::test_respects_expected_statuses`
- `TestUpdateMessageStatusByText::test_rank_guard_no_downgrade`
- `TestUpdateMessageStatusByText::test_scoped_to_contact_and_outgoing_only`
- `TestUpdateMessageStatusByText::test_updates_most_recent_row`
- `TestTransitionOutgoingStatusFallback::test_fallback_updates_db_and_backend_cache`
- `TestTransitionOutgoingStatusFallback::test_no_downgrade_when_echo_advanced_first`
- `TestViewStatusTransition::test_updates_ui_cache_and_widget`
- `TestViewStatusTransition::test_does_not_overwrite_a_receipt_that_arrived_first`

### `tests/test_prune_safety.py` (17 tests)

Imported: `protocols`, `protocols.db`, `server.bus`, `server.core`
- `TestCachePruneSafety::test_protected_rows_survive_outside_cap`
- `TestCachePruneSafety::test_old_idless_row_beyond_cap_is_pruned`
- `TestCachePruneSafety::test_cap_is_independent_per_protocol_and_contact`
- `TestCachePruneSafety::test_equal_timestamps_keep_highest_rowids`
- `TestCachePruneSafety::test_default_limit_comes_from_environment`
- `TestCachePruneSafety::test_zero_disables_prune`
- `TestCachePruneSafety::test_limit_below_floor_is_clamped_with_warning`
- `TestCachePruneSafety::test_config_json_limit_is_used`
- `TestCachePruneSafety::test_invalid_environment_value_falls_back_to_300`
- `TestCachePruneSafety::test_config_precedence_env_then_json_then_dotenv`
- `TestCachePruneSafety::test_dedup_refetch_preserves_count_and_read_state`
- `TestCachePruneSafety::test_vacuum_runs_only_if_rows_were_deleted`
- `TestCachePruneSafety::test_prune_logs_outcome`
- `TestCachePruneSafety::test_vacuum_failure_is_non_fatal`
- `TestAppExitPrune::test_ctrl_q_dispatches_on_exit_app_and_prunes`
- `TestAppExitPrune::test_on_exit_app_invokes_prune_and_swallows_failure`
- `TestAppExitPrune::test_on_exit_app_sets_native_shutting_down_flag_first`

### `tests/test_purge_ghost_outgoing.py` (4 tests)

Imported: `protocols`, `purge_ghost_outgoing`
- `test_dry_run_reports_candidate_without_deleting`
- `test_apply_rank_rules_and_optional_equal_rank`
- `test_apply_creates_backup_before_mutation`
- `test_msg_id_applies_only_to_targeted_pair`

### `tests/test_purge_orphan_media.py` (6 tests)

Imported: `protocols.media_prune`, `purge_orphan_media`
- `test_default_is_dry_run`
- `test_execute_deletes_only_orphan`
- `test_protocol_limits_scopes`
- `test_missing_db_exits_two`
- `test_missing_selected_directory_exits_one`
- `test_execute_deletion_error_exits_three`

### `tests/test_purge_test_rows.py` (13 tests)

Imported: `purge_test_rows`
- `test_purge_backs_up_and_removes_only_known_whatsapp_rows`
- `test_purge_dry_run_does_not_delete`
- `test_purge_apply_creates_backup_and_deletes`
- `test_purge_signal_batch_predicate`
- `test_purge_telegram_timestamp_predicate`
- `test_purge_idempotent`
- `test_purge_abort_if_count_exceeds_expected`
- `test_test_rows_and_expected_counts_share_the_same_keys`
- `test_backup_database_avoids_existing_collision`
- `test_purge_rolls_back_on_post_verify_mismatch`
- `test_purge_aborts_on_invalid_backup`
- `test_main_dry_run_returns_zero_without_backup`
- `test_main_apply_returns_zero_on_success`

### `tests/test_purge_wa_hd_children.py` (3 tests)

Imported: `protocols`, `purge_wa_hd_children`
- `test_dry_run_deletes_nothing`
- `test_apply_removes_only_confirmed_hd_child`
- `test_unreachable_waha_keeps_everything`

### `tests/test_quote_dedup_merge.py` (7 tests)

Imported: `protocols`
- `test_sent_placeholder_and_read_empty_merge_into_read_survivor`
- `test_dedup_merge_is_idempotent`
- `test_dedup_merges_edited_as_max`
- `test_dedup_never_overwrites_existing_survivor_values`
- `test_divergent_partition_is_not_merged`
- `test_multi_attachment_batch_rows_are_not_collapsed`
- `test_same_status_tie_break_merges_from_higher_rowid_into_lower`

### `tests/test_quote_thumbnail_diagnostics.py` (6 tests)

Imported: `protocols.db`, `protocols.signal`, `protocols.telegram`, `protocols.whatsapp`
- `TestReenterDropsLocalQuotePath::test_whatsapp_persists_quote_attachment_path`
- `TestReenterDropsLocalQuotePath::test_telegram_persists_quote_attachment_path`
- `TestSignalStalePathNoRecovery::test_signal_reenter_stale_path_recovers_thumbnail`
- `TestSignalStalePathNoRecovery::test_signal_live_payload_lacks_quote_attachment_id`
- `TestQuoteFinishAfterUnmount::test_finish_after_unmount_stashes_and_retries`
- `TestQuoteSemaphoreSharing::test_quote_has_dedicated_semaphore`

### `tests/test_quote_tui_parity.py` (5 tests)

Imported: `protocols.signal`
- `test_click_on_openable_voice_emits_media_open_not_message_clicked`
- `test_enter_on_openable_voice_emits_media_open_not_message_clicked`
- `test_click_on_text_message_still_emits_message_clicked`
- `test_signal_enqueue_media_reply_builds_display_quote_text`
- `test_signal_ingest_media_reply_row_keeps_display_quote_text`

### `tests/test_quote_v3_helpers_and_gating.py` (19 tests)

Imported: `protocols.manager`, `protocols.signal`, `protocols.telegram`, `protocols.whatsapp`
- `test_signal_quote_text_byte_identical`
- `test_signal_quote_text_empty_content_type_with_filename_is_none`
- `test_kind_to_placeholder_is_total_and_labels_are_canonical`
- `test_every_media_kind_maps_to_its_canonical_placeholder`
- `test_media_quote_display_mime_mapping`
- `test_media_quote_display_filename_and_em_dash`
- `test_parse_quote_attachment_descriptor`
- `test_send_message_sync_never_forwards_service_kwargs_to_backend`
- `test_send_attachments_sync_never_forwards_service_kwargs_to_backend`
- `test_enqueue_service_kwargs_are_gated_out_for_non_signal`
- `test_enqueue_service_kwargs_are_forwarded_for_signal`
- `test_convergence_mirror_first_then_echo`
- `test_convergence_echo_first_then_mirror`
- `test_convergence_both_orders_yield_the_same_quote_text`
- `test_barrier_reply_with_attachment_mirrors_quote_text`
- `test_barrier_real_caption_wins_over_descriptor`
- `test_echo_attachment_upgrade_also_merges_missing_quote_fields`
- `test_send_attachment_sync_never_forwards_service_kwargs_to_backend`
- `test_signal_media_reply_wire_quote_message_stays_empty`

### `tests/test_quote_v3_ratification.py` (6 tests)

Imported: `protocols`, `protocols.signal`
- `test_r1_incoming_partial_quote_fills_and_second_echo_is_noop`
- `test_r2_incoming_changed_adds_no_db_row_and_no_ui_cache_entry`
- `test_r3_incoming_changed_keeps_unread_unchanged_in_ui_and_db`
- `test_r4_existing_outgoing_upgrade_and_quote_fill_then_noop`
- `test_r5_fill_quote_fields_preserves_status_id_timestamp`
- `test_r6_changed_echo_pushes_web_event_exactly_once`

### `tests/test_reactions_db.py` (6 tests)

Imported: `protocols.db`
- `test_init_db_creates_reactions_schema_idempotently`
- `test_delta_add_change_remove_and_unique_identity`
- `test_snapshot_replace_preserves_delta_rows`
- `test_resolve_reaction_target_for_each_protocol`
- `test_prune_orphan_reactions_keeps_existing_targets`
- `test_cache_prune_removes_reactions_for_pruned_messages`

### `tests/test_reactions_ingest_filter.py` (17 tests)

Imported: `protocols`, `protocols.telegram`, `protocols.whatsapp`, `protocols.whatsapp_events`
- `TestSignalReactionIngestFilter::test_data_message_reaction_is_filtered`
- `TestSignalReactionIngestFilter::test_sync_sent_message_reaction_is_filtered`
- `TestSignalReactionIngestFilter::test_malformed_reaction_is_filtered`
- `TestSignalReactionIngestFilter::test_normal_message_is_unchanged`
- `TestSignalReactionIngestFilter::test_edit_message_is_unchanged`
- `TestWhatsAppReactionIngestFilter::test_reaction_type_is_filtered`
- `TestWhatsAppReactionIngestFilter::test_nested_reaction_message_is_filtered`
- `TestWhatsAppReactionIngestFilter::test_top_level_reaction_without_text_is_filtered`
- `TestWhatsAppReactionIngestFilter::test_normal_message_is_unchanged`
- `TestWhatsAppReactionIngestFilter::test_empty_text_without_media_is_filtered`
- `TestWhatsAppReactionIngestFilter::test_empty_text_with_media_is_preserved`
- `TestWhatsAppReactionIngestFilter::test_missing_chat_jid_returns_empty_list`
- `TestWhatsAppReactionIngestFilter::test_media_ack_without_downloaded_media_is_not_enqueued`
- `TestWhatsAppReactionIngestFilter::test_media_ack_with_downloaded_media_is_enqueued_as_image`
- `TestTelegramEmptyMessageIngestFilter::test_empty_message_without_media_or_reply_is_filtered`
- `TestTelegramEmptyMessageIngestFilter::test_photo_without_caption_is_preserved`
- `TestTelegramEmptyMessageIngestFilter::test_non_empty_text_message_is_preserved`

### `tests/test_reactions_model.py` (7 tests)

Imported: `protocols.base`
- `test_reaction_event_constructs_and_serializes`
- `test_reaction_dispatch_without_backend_support_returns_false`
- `test_reaction_dispatch_applies_without_marking_contact_dirty`
- `test_reaction_dispatch_unknown_backend_returns_false`
- `test_reaction_event_pushes_complete_web_update`
- `test_reaction_event_does_not_push_when_web_is_disabled`
- `test_apply_reaction_default_returns_none`

### `tests/test_reactions_send.py` (5 tests)

Imported: `protocols.db`, `protocols.rpc`, `protocols.signal`, `protocols.telegram`, `protocols.whatsapp`, `server.bus`, `web.api`
- `test_base_send_reaction_default_is_false`
- `test_signal_rpc_and_backend_send_reaction`
- `test_whatsapp_send_reaction_uses_waha_endpoint`
- `test_telegram_send_reaction_runs_on_backend_loop`
- `test_reaction_endpoint_validates_sends_persists_and_pushes`

### `tests/test_reactions_signal.py` (8 tests)

Imported: `protocols`, `protocols.db`
- `TestReactionEnvelopeToEvent::test_incoming_reaction_is_normalized_as_delta`
- `TestReactionEnvelopeToEvent::test_sync_sent_reaction_is_mine`
- `TestReactionEnvelopeToEvent::test_remove_reaction_sets_remove_flag`
- `TestReactionEnvelopeToEvent::test_missing_target_is_dropped_without_message_fallback`
- `TestApplyReaction::test_add_change_remove_and_unknown_target`
- `TestApplyReaction::test_matches_idless_target_by_timestamp`
- `TestApplyReaction::test_aggregates_two_authors_and_deduplicates_names`
- `TestApplyReaction::test_non_delta_mode_is_ignored`

### `tests/test_reactions_telegram.py` (16 tests)

Imported: `protocols.db`, `protocols.telegram`
- `test_get_available_reactions_filters_and_caches`
- `test_get_available_reactions_returns_empty_on_error`
- `test_handle_reactions_update_enqueues_renderable_snapshot_only`
- `test_message_reactions_enqueues_renderable_snapshot_only`
- `test_message_without_reactions_enqueues_no_extra_event`
- `test_configure_reaction_notify_enables_all_by_default`
- `test_configure_reaction_notify_does_not_set_when_disabled`
- `test_message_reactions_payload_persists_aggregate_rows`
- `test_handle_reactions_update_peer_contact_conventions`
- `test_handle_reactions_update_hides_authors_when_list_is_private`
- `test_handle_reactions_update_enqueues_empty_snapshot`
- `test_apply_reaction_replaces_and_clears_aggregate_rows`
- `test_apply_reaction_resolves_target_from_db_when_cache_misses`
- `test_apply_reaction_unknown_target_returns_none`
- `test_on_raw_dispatches_reaction_update`
- `test_reaction_catchup_during_connect_is_enqueued_and_persisted`

### `tests/test_reactions_whatsapp.py` (10 tests)

Imported: `protocols.db`, `protocols.whatsapp`, `protocols.whatsapp_events`
- `test_reaction_webhook_and_direct_payload_are_normalized`
- `test_from_me_reaction_uses_me_as_author`
- `test_from_me_reaction_ignores_participant_even_when_present`
- `test_empty_reaction_text_is_remove`
- `test_group_reaction_uses_participant_as_author_key`
- `test_configure_webhook_subscribes_to_reactions`
- `test_compose_subscribes_both_event_variables_to_reactions`
- `test_apply_reaction_add_change_and_remove`
- `test_apply_reaction_matches_plain_id_canonically`
- `test_apply_reaction_unknown_target_is_persisted_without_update`

### `tests/test_script_connections_closed.py` (5 tests)

Imported: `migrate_reactions_cleanup`, `migrate_wa_e2e_notice`, `protocols.db`, `purge_ghost_outgoing`, `purge_test_rows`
- `test_migrate_reactions_cleanup_closes_connection`
- `test_migrate_wa_e2e_notice_closes_connection`
- `test_purge_test_rows_dry_run_closes_connection`
- `test_purge_test_rows_backup_closes_connections`
- `test_purge_ghost_outgoing_backup_closes_connections`

### `tests/test_send_message_ids.py` (3 tests)

Imported: `server.api`
- `test_text_send_returns_backend_message_id`
- `test_text_send_without_backend_id_returns_empty_list`
- `test_attachment_send_returns_backend_message_ids`

### `tests/test_send_optimistic_op.py` (14 tests)

Imported: `protocols`, `protocols.db`, `server.api`, `server.bus`, `server.ops`
- `test_sends_and_settles_the_pending_row`
- `test_recency_bumped_and_one_message_frame`
- `test_failure_marks_failed_and_attempts_once`
- `test_retry_policy_is_explicit`
- `test_resend_rearms_the_failed_row`
- `test_resend_of_a_row_that_is_not_failed_conflicts`
- `test_identical_send_inside_dedup_window_adds_and_persists_once`
- `test_row_keeps_quote_and_display_metadata`
- `test_telegram_reply_carries_the_original_id`
- `test_unreachable_contact_is_rejected_before_anything_is_stored`
- `test_optimistic_is_text_only`
- `test_client_msg_id_makes_a_replayed_call_a_duplicate`
- `test_empty_quote_body_stays_on_the_wire_for_the_optimistic_caller`
- `test_the_web_flavour_still_normalises_a_blank_quote_body`

### `tests/test_send_persist_offthread.py` (31 tests)

Imported: `protocols`, `protocols.db`, `protocols.signal`
- `TestSendPersistOffthread::test_submit_does_not_persist_on_ui_thread`
- `TestSendPersistOffthread::test_telegram_reply_without_valid_message_id_keeps_input_and_reply`
- `TestSendPersistOffthread::test_worker_persists_row_with_correct_fields`
- `TestSendPersistOffthread::test_persist_happens_before_send`
- `TestSendPersistOffthread::test_optimistic_send_then_echo_not_duplicated`
- `TestSendPersistOffthread::test_persisted_send_pushes_web_event_once_across_echo`
- `TestSendPersistOffthread::test_double_same_text_not_persisted`
- `TestSendPersistOffthread::test_whatsapp_send_persists_whatsapp_protocol`
- `TestSendPersistOffthread::test_signal_send_persists_signal_protocol`
- `TestSendPersistOffthread::test_reply_persists_quote_and_forwards_quote_params`
- `TestSendPersistOffthread::test_reply_persists_quote_attachment_metadata`
- `TestSendPersistOffthread::test_whatsapp_reply_persists_quote_attachment_id`
- `TestSendPersistOffthread::test_live_event_propagates_quote_attachment_metadata`
- `TestSendPersistOffthread::test_worker_uses_original_contact_after_same_protocol_switch`
- `TestSendPersistOffthread::test_worker_uses_original_protocol_after_protocol_switch`
- `TestSendPersistOffthread::test_failed_send_is_persisted_and_retry_reuses_the_same_message`
- `TestMediaReplySend::test_send_media_reply_without_caption_sends_empty_quote_message`
- `TestMediaReplySend::test_send_media_reply_with_caption_sends_caption`
- `TestMediaReplySend::test_send_text_reply_is_unchanged`
- `TestMediaReplySend::test_media_reply_signal_builds_quote_attachments_with_preview`
- `TestMediaReplySend::test_media_reply_signal_with_caption_builds_quote_attachments`
- `TestMediaReplySend::test_text_reply_omits_quote_attachments`
- `TestMediaReplySend::test_media_reply_signal_missing_preview_falls_back_to_content_type`
- `TestMediaReplySend::test_retry_media_reply_reconstructs_quote_attachments`
- `TestMediaReplySend::test_retry_media_reply_with_caption_reconstructs_quote_attachments`
- `TestMediaReplySend::test_media_reply_without_content_type_omits_quote_attachments`
- `TestMediaReplySend::test_retry_media_reply_reconstructs_empty_wire_body`
- `TestMediaReplySend::test_retry_text_reply_is_unchanged`
- `TestMediaReplySend::test_whatsapp_media_reply_without_id_is_blocked`
- `TestMediaReplySend::test_whatsapp_media_reply_sends_placeholder_and_native_reply_id`
- `TestMediaReplySend::test_telegram_media_reply_guard_unchanged`

### `tests/test_send_timing.py` (3 tests)

Imported: `server.ops`
- `TestSendTimingInstrumentation::test_slow_send_logs_warning`
- `TestSendTimingInstrumentation::test_fast_send_logs_debug`
- `TestSendTimingInstrumentation::test_transition_called_after_send`

### `tests/test_server_api.py` (14 tests)

Imported: `server.api`, `server.bus`
- `test_call_validates_dict_into_request_model`
- `test_call_accepts_request_model_instance`
- `test_extra_request_keys_are_ignored`
- `test_validation_error_becomes_invalid_request`
- `test_unknown_operation_is_not_found`
- `test_handler_api_error_propagates`
- `test_unknown_error_code_rejected`
- `test_duplicate_registration_rejected`
- `test_call_json_serializes_through_response_type`
- `test_call_json_rejects_response_violating_the_type`
- `test_async_handler_runs_in_sync_call_and_acall`
- `test_operations_listing_and_schema_export`
- `test_publish_web_push_uses_bus_and_tolerates_none`
- `test_builtin_operations_registered_and_schema_exportable`

### `tests/test_server_attach.py` (18 tests)

Imported: `server`, `server.paths`, `server.socket_client`
- `test_connect_path_is_used_and_never_spawns`
- `test_connect_path_without_server_fails_without_spawning`
- `test_attaches_to_a_live_server_on_the_default_path`
- `test_spawns_when_absent_and_connects_when_the_socket_answers`
- `test_spawn_failure_shows_the_log_tail_and_does_not_hide_the_exit`
- `test_spawn_failure_surfaces_the_server_validation_error`
- `test_incompatible_server_is_an_error_not_a_reason_to_spawn`
- `test_timeout_stops_the_process_we_spawned`
- `test_ctrl_c_while_waiting_stops_the_process_we_spawned`
- `test_loser_of_the_spawn_race_attaches_to_the_winner`
- `test_answering_server_that_is_not_ours_is_not_spawned`
- `test_serve_args_for_web`
- `test_spawn_server_command_and_detachment`
- `test_spawn_server_python_escape_hatch`
- `test_spawn_server_without_binary_is_an_attach_error`
- `test_lock_env_override`
- `test_simultaneous_acquire_has_exactly_one_winner`
- `test_lock_holder`

### `tests/test_server_bus.py` (12 tests)

Imported: `server.bus`
- `test_seq_monotonic_and_defaults`
- `test_fanout_and_no_history_without_since`
- `test_since_replay`
- `test_replay_racing_publishers`
- `test_buffer_eviction_gap`
- `test_overflow_gap_then_recovery`
- `test_close_unblocks_get`
- `test_get_timeout_and_context_manager`
- `test_bus_close_closes_subscriptions`
- `test_drain_order`
- `test_aget`
- `test_publish_never_blocks_with_stuck_subscriber`

### `tests/test_server_client.py` (12 tests)

Imported: `server.api`, `server.bus`, `server.client`, `server.core`, `server.events_api`, `server.status`
- `test_in_process_client_is_a_chat_client`
- `test_call_returns_plain_json`
- `test_call_validation_error_is_an_api_error`
- `test_call_unknown_operation`
- `test_acall_matches_call_and_raises_api_error`
- `test_subscribe_yields_envelopes_with_json_payloads`
- `test_subscribe_since_replays_missed_events`
- `test_subscribe_since_outside_buffer_signals_gap`
- `test_subscription_close_and_client_close`
- `test_blocked_get_wakes_on_close`
- `test_aget`
- `test_tui_uses_an_in_process_client_for_events`

### `tests/test_server_config_op.py` (9 tests)

Imported: `protocols.config`, `server.config`
- `test_op_returns_configured_limits`
- `test_op_defaults_match_model_constants`
- `test_tui_never_calls_the_client_for_thumbnail_limits`
- `test_prefetched_config_sets_the_limits_once`
- `test_failed_prefetch_keeps_defaults_and_a_retry_applies`
- `test_chat_view_has_no_backend_imports`
- `test_op_returns_picker_options`
- `test_tui_configures_the_picker_from_the_server`
- `test_contact_picker_has_no_protocols_import`

### `tests/test_server_core_connect.py` (45 tests)

Imported: `protocols.whatsapp`, `server.bus`, `server.core`
- `TestCoreConstruction::test_registers_only_enabled_backends`
- `TestCoreConstruction::test_registers_whatsapp_and_telegram_when_enabled`
- `TestCoreStartGating::test_signal_enabled_spawns_worker`
- `TestCoreStartGating::test_signal_disabled_reports_done`
- `TestCoreStartGating::test_whatsapp_uses_boot_worker`
- `TestCoreStartGating::test_telegram_started_only_when_not_needing_pairing`
- `TestCoreStartGating::test_telegram_skipped_when_pairing_needed`
- `TestSignalConnectEvents::test_success_with_daemon_event_order`
- `TestSignalConnectEvents::test_success_without_daemon_is_persistent_warning`
- `TestSignalConnectEvents::test_failure_reports_error_then_done`
- `TestTelegramConnectGuard::test_skips_when_already_connecting`
- `TestTelegramConnectGuard::test_runs_and_resets_guard`
- `TestTelegramConnectGuard::test_history_sync_status_when_contacts`
- `TestTelegramConnectGuard::test_guard_reset_on_failure`
- `TestWhatsappConnect::test_skips_when_already_connecting`
- `TestWhatsappConnect::test_contacts_ready_then_resync_resets_guard`
- `TestWhatsappConnect::test_no_contacts_spawns_poller_and_keeps_guard`
- `TestWhatsappConnect::test_failure_reports_done_and_resets_guard`
- `TestWhatsappConnect::test_poll_contacts_success`
- `TestWhatsappConnect::test_poll_contacts_timeout`
- `TestWhatsappBootConnect::test_connects_immediately_when_working`
- `TestWhatsappBootConnect::test_skips_pairing_sessions`
- `TestWhatsappBootConnect::test_waits_until_working_then_connects`
- `TestWhatsappBootConnect::test_timeout_marks_backend_done`
- `TestWhatsappBootConnect::test_no_backend_is_noop`
- `TestReconnect::test_empty_set_reconnects_nothing`
- `TestReconnect::test_telegram_only`
- `TestReconnect::test_signal_only`
- `TestReconnect::test_whatsapp_only`
- `TestReconnect::test_multiple`
- `TestReconnect::test_missing_backend_skipped`
- `TestReconnect::test_spawn_runs_daemon_named_thread`
- `TestResyncWaHistory::test_calls_resync_when_connected`
- `TestResyncWaHistory::test_does_not_resync_when_disconnected`
- `TestResyncWaHistory::test_no_backend_returns_zero`
- `TestResyncWaHistory::test_backend_error_is_swallowed`
- `TestTuiDispatch::test_connecting`
- `TestTuiDispatch::test_status_persistent_uses_timeout_zero`
- `TestTuiDispatch::test_status_default_timeout`
- `TestTuiDispatch::test_ready_passes_the_protocol_not_the_backend`
- `TestTuiDispatch::test_done`
- `TestTuiDispatch::test_unknown_and_gap_ignored`
- `TestTuiDispatch::test_reconnect_goes_through_the_link_reconnect_operation`
- `TestTuiDispatch::test_tui_has_no_backend_aliases`
- `TestTuiDispatch::test_event_worker_forwards_to_ui_thread`

### `tests/test_server_events_api.py` (11 tests)

Imported: `server`, `server.bus`, `server.core`, `server.events_api`, `web.legacy_events`
- `test_contact_roundtrip_keeps_identity_fields_and_extras`
- `test_contact_without_recency_roundtrips_without_adding_it`
- `test_contact_extras_are_restricted_to_json_values`
- `test_jsonable_coerces_non_json_values`
- `test_json_schema_covers_every_event_model`
- `test_every_core_event_type_is_wire_form`
- `test_to_wire_keeps_unmodelled_values_when_schema_mismatches`
- `test_to_wire_passes_unknown_event_types_through`
- `test_message_payload_is_a_copy_not_the_backend_dict`
- `test_legacy_frames_from_wire_events_match_the_old_shapes`
- `test_guard_marker_allows_non_json_payloads`

### `tests/test_server_import_rules.py` (19 tests)

Imported: `protocols.config`, `protocols.db`, `server.api`, `server.bus`, `server.client`, `server.core`, `server.messages`, `server.models_api`, `server.paths`, `server.socket_adapter`, `server.uploads`, `server.version`
- `test_server_imports_no_ui_or_web`
- `test_serve_path_has_no_textual_or_tui`
- `test_protocols_import_no_web`
- `test_server_modules_import_without_ui_modules_loaded`
- `test_web_only_imports_the_api_surface`
- `test_web_allowlist_has_no_unused_entries`
- `test_web_rule_detects_violations`
- `test_tui_and_ui_modules_never_import_protocols_or_web`
- `test_composition_root_exception_is_exactly_protocols_config_and_web`
- `test_server_package_does_not_import_tui_or_web_except_main`
- `test_web_modules_import_without_protocols_loaded`
- `test_ui_modules_import_without_protocols_or_web_loaded`
- `test_tui_has_no_test_only_plumbing`
- `test_nobody_imports_the_removed_web_bridge`
- `test_socket_adapter_imports_only_the_api_surface`
- `test_socket_adapter_allowlist_has_no_unused_entries`
- `test_socket_adapter_imports_without_ui_or_protocols_loaded`
- `test_socket_client_imports_only_the_client_surface`
- `test_socket_client_imports_without_ui_or_protocols_loaded`

### `tests/test_server_main.py` (16 tests)

Imported: `protocols.db`, `server`, `server.bus`, `server.clients`, `server.core`, `server.lock`, `server.main`, `server.shutdown`, `server.sock`, `server.status`
- `test_serve_end_to_end_with_web`
- `test_lock_contention_exits_1`
- `test_shutdown_order`
- `test_shutdown_failures_are_non_fatal`
- `test_no_web_and_no_socket_warns_that_nobody_can_connect`
- `test_no_web_still_serves_the_socket`
- `test_serve_starts_and_stops_the_socket_by_default`
- `test_server_shutdown_op_stops_serve_cleanly`
- `test_no_socket_flag_skips_the_adapter`
- `test_socket_flags_are_exclusive`
- `test_socket_path_flag_is_passed_through_and_expanded`
- `test_socket_failure_exits_1_and_cleans_up`
- `test_shutdown_stops_the_socket_before_the_web_adapter`
- `test_web_failure_exits_1_and_cleans_up`
- `test_signal_stops_server`
- `test_invalid_port_rejected`

### `tests/test_server_process.py` (34 tests)

Imported: `server.bus`, `server.core`, `server.events_api`
- `TestProcessMessage::test_no_backend_yields_nothing`
- `TestProcessMessage::test_payload_contact_ingested_and_emitted`
- `TestProcessMessage::test_ingest_result_flags_always_emitted`
- `TestProcessMessage::test_backend_without_ingest_counts_as_added`
- `TestProcessMessage::test_identify_contact_used_when_payload_has_none`
- `TestProcessMessage::test_placeholder_registered_with_backend`
- `TestProcessMessage::test_unknown_contact_appended_when_backend_has_no_register`
- `TestProcessMessage::test_contact_known_to_backend_is_not_reregistered`
- `TestProcessMessage::test_ingest_uses_resolved_contact_id`
- `TestProcessMessage::test_process_does_not_publish`
- `TestProcessEdit::test_emits_info_and_text`
- `TestProcessEdit::test_noop_edit_emits_nothing`
- `TestProcessEdit::test_missing_id_or_text_is_ignored`
- `TestProcessEdit::test_no_backend_or_no_apply_edit`
- `TestProcessReaction::test_emits_info`
- `TestProcessReaction::test_nothing_without_info_backend_or_method`
- `TestProcessReceipt::test_signal_builds_envelope`
- `TestProcessReceipt::test_generic_uses_message_ids_payload`
- `TestProcessReceipt::test_generic_defaults`
- `TestProcessReceipt::test_nothing_without_backend_or_method`
- `TestProcessTyping::test_valid_actions`
- `TestProcessTyping::test_invalid_action_ignored`
- `TestProcessPassthrough::test_contact_update_passthrough`
- `TestProcessPassthrough::test_contact_merge_passthrough_keeps_merged_into`
- `TestProcessPassthrough::test_lid_warmup_done_passthrough`
- `TestProcessPassthrough::test_sent_mirror_registers_unknown_contact`
- `TestProcessPassthrough::test_sent_mirror_prefers_backend_object_without_lookup`
- `TestProcessPassthrough::test_sent_mirror_without_backend`
- `TestProcessPassthrough::test_unknown_type_ignored`
- `TestPump::test_pump_publishes_processed_events_and_stops_promptly`
- `TestPump::test_start_launches_pump_and_stop_exits`
- `TestPump::test_bad_events_and_backends_do_not_kill_the_loop`
- `TestPump::test_stop_before_start_and_restart`
- `TestPump::test_pump_runs_on_single_named_daemon_thread`

### `tests/test_signal_dedup_id_mismatch.py` (8 tests)

Imported: `protocols`, `protocols.db`, `protocols.signal`
- `test_two_images_rapid_echoes_not_merged`
- `test_two_identical_texts_confirmed_not_merged`
- `test_echo_upgrades_only_idless_row`
- `test_echo_out_of_order_picks_closest_idless`
- `test_outgoing_multi_attachment_same_id_not_collapsed`
- `test_last_ditch_idless_requires_same_attachment`
- `test_existing_row_gets_missing_quote_fields`
- `test_incoming_enriched_echo_fills_quote_fields_idempotently`

### `tests/test_signal_ingest_race.py` (5 tests)

Imported: `protocols`, `protocols.db`, `protocols.signal`
- `test_ingest_dedup_survives_cache_swap`
- `test_add_message_to_cache_idempotent`
- `test_ingest_concurrent_check_then_act`
- `test_connect_merge_preserves_inflight`
- `test_signal_quote_timestamp_extraction`

### `tests/test_signal_real_timestamp.py` (14 tests)

Imported: `protocols`, `protocols.db`, `protocols.signal`, `server.ops`
- `TestSendMessageSyncRealTimestamp::test_daemon_rpc_returns_real_timestamp`
- `TestSendMessageSyncRealTimestamp::test_daemon_rpc_without_timestamp_falls_back_to_optimistic_ts`
- `TestSendMessageSyncRealTimestamp::test_subprocess_parses_stdout_timestamp`
- `TestSendMessageSyncRealTimestamp::test_subprocess_dirty_stdout_falls_back_to_optimistic_ts`
- `TestIngestEchoUpgrade::test_echo_upgrades_optimistic_twin_keeps_optimistic_ts`
- `TestIngestEchoUpgrade::test_second_echo_is_idempotent`
- `TestIngestEchoUpgrade::test_sync_sent_without_optimistic_twin_adds_entry_and_row_with_id`
- `TestEnvelopeToEventRealId::test_sync_sent_payload_carries_real_id`
- `TestEnvelopeToEventRealId::test_incoming_data_message_has_no_id`
- `TestApplyEditIdFirst::test_id_first_match_uses_entry_timestamp`
- `TestApplyEditIdFirst::test_ts_fallback_still_works_for_entry_without_id`
- `TestSignalWorkerRealId::test_worker_ingests_real_id_and_updates_cache_and_widget`
- `TestEditUIRealId::test_entry_with_id_uses_entry_id`
- `TestEditUIRealId::test_entry_without_id_falls_back_to_ts_and_warns`

### `tests/test_signal_sse_listener.py` (3 tests)

Imported: `protocols`, `protocols.signal`
- `test_empty_stream_logs_connection_lost_no_unbound_error`
- `test_envelopes_processed_and_logged`
- `test_internal_error_logged_as_error`

### `tests/test_signal_tui_lock.py` (6 tests)

Imported: `server.lock`
- `TestLockFile::test_acquire_lock_success`
- `TestLockFile::test_acquire_lock_alive_process`
- `TestLockFile::test_acquire_lock_dead_process`
- `TestLockFile::test_release_lock`
- `TestLockFile::test_release_lock_not_ours`
- `TestLockFile::test_acquire_lock_exception_safe`

### `tests/test_socket_adapter.py` (73 tests)

Imported: `protocols.db`, `server`, `server.api`, `server.bus`, `server.json`, `server.ping`, `server.sock`, `server.socket_adapter`
- `test_ops`
- `test_hello_lists_operations`
- `test_ping`
- `test_sync_operation_matches_call_json`
- `test_string_id_and_null_params`
- `test_async_operation_messages_send`
- `test_notification_is_executed_but_not_answered`
- `test_every_api_error_code_is_mapped`
- `test_application_codes_are_distinct_and_in_range`
- `test_invalid_params_from_validation`
- `test_unexpected_exception_is_internal_and_hides_details`
- `test_unknown_method`
- `test_params_must_be_an_object`
- `test_malformed_json_keeps_the_connection`
- `test_invalid_envelopes`
- `test_oversize_line_gets_an_error_and_closes`
- `test_large_response_and_large_request_round_trip`
- `test_half_closed_client_still_gets_its_answers`
- `test_large_response_is_flushed_before_close`
- `test_out_of_order_answers_and_other_connections_not_blocked`
- `test_events_flow_while_a_call_is_blocked`
- `test_timeout_member_and_operation_keeps_running`
- `test_async_operation_timeout`
- `test_default_timeout_applies`
- `test_too_many_requests_in_flight`
- `test_too_many_connections`
- `test_subscribe_live_events_in_order`
- `test_no_events_before_subscribe`
- `test_subscribe_replays_from_since`
- `test_subscribe_gap_when_since_left_the_buffer`
- `test_slow_consumer_overflow_becomes_gap`
- `test_subscribe_rejects_bad_since`
- `test_unsubscribe_stops_events_and_resubscribe_restarts`
- `test_heartbeat_when_idle`
- `test_subscription_closed_when_client_disconnects`
- `test_permissions`
- `test_existing_loose_default_directory_is_tightened`
- `test_socket_is_never_group_or_world_accessible_under_a_loose_umask`
- `test_foreign_uid_peer_is_refused`
- `test_same_uid_peer_is_accepted_and_uid_is_read_from_the_kernel`
- `test_runtime_file_lifecycle`
- `test_stop_drops_connections_and_is_idempotent`
- `test_stop_leaves_a_successors_files_alone`
- `test_stale_socket_and_runtime_file_are_cleaned`
- `test_live_server_on_the_path_blocks_startup`
- `test_regular_file_on_the_path_is_never_removed`
- `test_too_long_path_is_a_clear_error`
- `test_bind_failure_cleans_up`
- `test_default_socket_path_resolution`
- `test_attachment_is_copied_and_the_copy_cleaned_up`
- `test_staged_copy_is_private_and_removed_when_the_send_fails`
- `test_missing_filename_defaults_to_the_basename`
- `test_wrong_extension_still_rejected`
- `test_non_media_file_is_rejected_and_not_kept`
- `test_symlink_is_refused`
- `test_special_files_and_directories_are_refused`
- `test_relative_or_empty_paths_are_refused`
- `test_missing_file`
- `test_invalid_payload_is_rejected_before_any_copy`
- `test_too_many_attachments_copy_nothing`
- `test_oversized_total_is_too_large_and_cleans_up`
- `test_malformed_attachments_are_invalid_params`
- `test_rejected_slot_passes_through`
- `test_check_operation_never_sees_client_paths`
- `test_attachment_policy_covers_every_operation_with_attachments`
- `test_stopping_closes_loop_bound_resources_on_the_adapter_loop`
- `test_gmail_attachments_are_copied_read_and_cleaned_up`
- `test_gmail_attachment_checks_run_before_any_copy`
- `test_gmail_attachment_over_the_gmail_limit_is_too_large`
- `test_gmail_attachment_symlink_and_missing_paths_are_refused`
- `test_unknown_attachment_operation_blocks_startup`
- `test_attachment_field_with_another_name_blocks_startup`
- `test_stale_staged_files_are_pruned_at_start`

### `tests/test_socket_client.py` (44 tests)

Imported: `protocols.db`, `server.api`, `server.bus`, `server.client`, `server.socket_adapter`, `server.socket_client`, `server.status`
- `test_is_a_chat_client`
- `test_call_returns_plain_json`
- `test_validation_error`
- `test_unknown_operation`
- `test_api_error_keeps_code_message_and_details`
- `test_non_serializable_request`
- `test_acall`
- `test_async_operation`
- `test_many_threads_get_their_own_answers`
- `test_subscribe_events_drain_and_timeout`
- `test_subscribe_since_replays`
- `test_subscribe_since_outside_buffer_is_a_gap`
- `test_close_wakes_getters_and_closes_subscriptions`
- `test_aget`
- `test_send_with_an_attachment_path`
- `test_operation_timeout_does_not_cancel_the_operation`
- `test_blocked_operation_times_out_in_time`
- `test_acall_cancellation_leaves_the_connection_usable`
- `test_wire_constants_match_the_adapter`
- `test_connect_resolves_the_default_path`
- `test_connect_without_a_server_raises_server_unavailable`
- `test_connect_waits_for_a_server_that_is_starting`
- `test_major_version_mismatch_is_refused`
- `test_version_change_on_reconnect_is_final`
- `test_in_flight_call_fails_with_server_unavailable_and_client_recovers`
- `test_calls_fail_fast_while_disconnected`
- `test_subscription_resumes_after_a_lost_connection`
- `test_resume_without_events_missed`
- `test_gap_when_the_outage_outlasted_the_server_buffer`
- `test_server_restart_gives_a_gap_event`
- `test_restart_is_detected_from_the_seq_alone`
- `test_late_subscription_is_served_from_the_ring`
- `test_subscribing_while_disconnected_starts_the_stream_on_reconnect`
- `test_no_reconnect_option_makes_a_loss_final`
- `test_reconnect_attempts_are_bounded`
- `test_reconnect_timeout_gives_up`
- `test_silent_server_is_detected_by_the_missing_heartbeat`
- `test_idle_connection_without_subscription_is_kept_alive_by_pings`
- `test_heartbeats_keep_a_subscribed_connection_alive`
- `test_close_stops_the_threads_and_fails_later_calls`
- `test_close_during_reconnect_does_not_leak_the_thread`
- `test_close_fails_calls_in_flight`
- `test_events_and_responses_from_many_threads_stay_ordered`
- `test_a_server_that_drops_every_connection_is_not_hammered`

### `tests/test_telegram.py` (56 tests)

Imported: `protocols`, `protocols.db`, `protocols.telegram`
- `TestTelegramStatus::test_is_connected_false_before_client_up`
- `TestTelegramStatus::test_is_connected_true_once_client_up`
- `TestTelegramContacts::test_attachment_path_returns_existing_file_only`
- `TestTelegramContacts::test_get_attachment_path_tgref_not_connected`
- `TestTelegramContacts::test_get_attachment_path_tgref_loop_not_running`
- `TestTelegramContacts::test_get_attachment_path_tgref_lazy_download_success`
- `TestTelegramContacts::test_get_attachment_path_tgref_dedup_existing_file`
- `TestTelegramContacts::test_get_attachment_path_tgref_message_gone`
- `TestTelegramContacts::test_get_attachment_path_tgref_no_media`
- `TestTelegramContacts::test_get_attachment_path_tgref_download_fails`
- `TestTelegramContacts::test_get_attachment_path_tgref_future_raises`
- `TestTelegramContacts::test_get_attachment_chunk_uses_telethon_partial_download`
- `TestTelegramContacts::test_entity_to_contact_handles_duck_types_and_real_telethon_entities`
- `TestTelegramContacts::test_identify_and_list_contacts_return_known_contact_and_copy`
- `TestTelegramContacts::test_load_contacts_populates_contacts_and_handles_errors`
- `TestTelegramMessages::test_message_to_event_normalizes_text_media_reply_and_contact`
- `TestTelegramMessages::test_quote_resolves_target_attachment_metadata`
- `TestTelegramMessages::test_message_to_event_normalizes_media`
- `TestTelegramMessages::test_message_photo_with_caption_uses_text_as_info`
- `TestTelegramMessages::test_message_photo_without_download_gets_tgref`
- `TestTelegramMessages::test_message_photo_with_caption_gets_tgref`
- `TestTelegramMessages::test_message_to_event_uses_document_filename_and_id_fallback`
- `TestTelegramMessages::test_receipts_polling_and_ingest_deduplication`
- `TestTelegramMessages::test_ingest_bounds_cache_and_persist_message_passes_protocol`
- `TestTelegramMessages::test_poll_once_drains_events_and_receive_is_empty`
- `TestTelegramTyping::test_user_typing_is_started`
- `TestTelegramTyping::test_user_typing_cancel_is_stopped`
- `TestTelegramTyping::test_non_cancel_send_actions_are_started`
- `TestTelegramTyping::test_chat_and_channel_contact_id_conventions`
- `TestTelegramTyping::test_typing_is_pure_translator_no_cache_mutation`
- `TestTelegramTyping::test_unknown_update_returns_none_no_event`
- `TestTelegramBackendOperations::test_voice_attachment_sets_voice_note`
- `TestTelegramBackendOperations::test_attachment_mirror_uses_original_filename`
- `TestTelegramBackendOperations::test_attachment_send_timeout_is_120_and_configurable`
- `TestTelegramBackendOperations::test_load_cache_mark_read_and_pairing`
- `TestTelegramBackendOperations::test_send_methods_validate_connection_ids_and_send`
- `TestTelegramBackendOperations::test_send_reply_uses_positive_original_message_id`
- `TestTelegramBackendOperations::test_validated_reply_to_message_id_rejects_non_numeric_string`
- `TestTelegramBackendOperations::test_disconnect_fetch_history_and_complete_2fa`
- `TestTelegramBackendOperations::test_fetch_history_photo_persists_tgref`
- `TestTelegramBackendOperations::test_complete_2fa_failure_and_qr_missing_configuration`
- `TestTelegramQuoteMedia::test_tg_quote_media_from_cache_placeholder`
- `TestTelegramQuoteMedia::test_tg_quote_media_placeholder_variants`
- `TestTelegramQuoteMedia::test_tg_quote_unmapped_filename_enriches_placeholder`
- `TestTelegramQuoteMedia::test_tg_quote_caption_from_cache`
- `TestTelegramQuoteMedia::test_tg_quote_no_cache_hit_is_none`
- `TestTelegramQuoteMedia::test_tg_quote_media_through_message_to_event`
- `test_migrate_legacy_media_dir`
- `TestTelegramDisconnectLifecycle::test_disconnect_sync_handles_telethon_144_sync_wrapper`
- `TestTelegramDisconnectLifecycle::test_disconnect_sync_closed_loop_skips_client_cleanly`
- `TestTelegramProfilePhoto::test_returns_none_without_client_or_running_loop`
- `TestTelegramProfilePhoto::test_returns_none_for_non_numeric_id`
- `TestTelegramProfilePhoto::test_returns_bytes_and_sniffed_content_type`
- `TestTelegramProfilePhoto::test_returns_none_when_no_photo`
- `TestTelegramProfilePhoto::test_falls_back_to_input_entity`
- `TestTelegramProfilePhoto::test_returns_none_on_exception`

### `tests/test_telegram_edit.py` (27 tests)

Imported: `protocols.db`, `protocols.telegram`
- `TestHandleMessageEdited::test_produces_correct_message_edit_event`
- `TestHandleMessageEdited::test_incoming_edit_is_mine_false_and_no_edit_date`
- `TestHandleMessageEdited::test_media_edit_is_skipped`
- `TestHandleMessageEdited::test_empty_text_is_skipped`
- `TestHandleMessageEdited::test_none_message_is_skipped`
- `TestHandleMessageEdited::test_none_chat_id_is_skipped`
- `TestEditMessageSync::test_not_connected_raises_runtime_error`
- `TestEditMessageSync::test_success_calls_edit_message_with_resolved_entity`
- `TestEditMessageSync::test_success_with_client_get_input_entity`
- `TestEditMessageSync::test_non_numeric_message_id_raises_value_error`
- `TestEditMessageSync::test_non_numeric_contact_id_raises_value_error`
- `TestEditMessageSync::test_negative_mid_raises_value_error`
- `TestEditMessageSync::test_zero_mid_raises_value_error`
- `TestEditMessageSync::test_validation_does_not_schedule_on_loop`
- `TestApplyEdit::test_hit_updates_cache_and_db`
- `TestApplyEdit::test_calls_update_message_text_with_correct_params`
- `TestApplyEdit::test_identical_text_returns_none`
- `TestApplyEdit::test_media_message_returns_none`
- `TestApplyEdit::test_unknown_id_returns_none`
- `TestApplyEdit::test_is_mine_mismatch_returns_none`
- `TestApplyEdit::test_is_mine_matching_applies`
- `TestIngestMessageEdit::test_known_id_different_text_invokes_apply_edit`
- `TestIngestMessageEdit::test_known_id_different_text_updates_cache_and_db`
- `TestIngestMessageEdit::test_known_id_same_text_does_not_apply_edit`
- `TestIngestMessageEdit::test_known_id_empty_text_does_not_apply_edit`
- `TestIngestMessageEdit::test_known_id_media_entry_does_not_apply_edit`
- `TestFetchRecentHistoryEdit::test_fetch_recent_history_applies_edit_to_cached_message`

### `tests/test_telegram_media_portable.py` (3 tests)

Imported: `protocols.telegram`
- `test_resolves_foreign_absolute_path_by_basename`
- `test_does_not_resolve_path_traversal_by_basename`
- `test_tgref_still_uses_lazy_download`

### `tests/test_telegram_read_receipt_fix.py` (21 tests)

Imported: `protocols.db`, `protocols.telegram`
- `TestDefectADoubleProcessing::test_handle_read_receipt_is_pure_translator`
- `TestDefectADoubleProcessing::test_process_receipt_returns_updated_and_persists_read`
- `TestDefectADoubleProcessing::test_process_receipt_is_idempotent_no_repeat_update`
- `TestDefectADoubleProcessing::test_process_receipt_never_downgrades_read`
- `TestDefectADoubleProcessing::test_handle_read_receipt_peer_id_conventions`
- `TestReceiptScoping::test_process_receipt_does_not_update_other_chats_with_same_id`
- `TestDefectBStartupDuplicate::test_history_ingest_same_msg_id_no_duplicate_keeps_read`
- `TestDefectBStartupDuplicate::test_message_to_chat_event_does_not_hardcode_status`
- `TestDefectCReconciliation::test_load_contacts_reconciles_read_outbox_max_id`
- `TestDedupById::test_dedup_keeps_read_over_lower_rowid_sent`
- `TestDedupById::test_dedup_is_idempotent`
- `TestDedupById::test_dedup_merges_missing_quote_fields_into_survivor`
- `TestDedupById::test_dedup_keeps_distinct_text_for_same_msg_id`
- `TestDedupById::test_dedup_ignores_rows_without_msg_id`
- `TestUpdateStatusById::test_rank_guard_never_downgrades_read`
- `TestUpdateStatusById::test_rank_guard_upgrades_sent_to_read`
- `TestUpdateStatusById::test_update_scoped_by_contact`
- `TestReconcileReadState::test_reconcile_is_scoped_per_contact`
- `TestReconcileReadState::test_reconcile_does_not_mark_above_max_id`
- `TestReconcileReadState::test_reconcile_never_downgrades`
- `TestReceiptEndToEndContract::test_live_receipt_event_scopes_process_receipt`

### `tests/test_telegram_send_reorder.py` (1 tests)

Imported: `protocols`, `protocols.db`, `protocols.telegram`
- `test_telegram_send_updates_last_ts_and_reorders_immediately`

### `tests/test_transcribe_settings.py` (11 tests)

Imported: `protocols.config`, `server.api`, `transcription.config`, `web.api`, `web.auth`
- `test_defaults`
- `test_partial_update_and_reset`
- `test_invalid_values`
- `test_valid_bounds`
- `test_env_overrides_reported`
- `test_never_returns_api_key`
- `test_reinit_applies_settings_and_keeps_on_done`
- `test_web_requires_auth`
- `test_web_roundtrip_matches_operation`
- `test_web_invalid_is_400`
- `test_web_route_does_not_shadow_status`

### `tests/test_transcription_client.py` (9 tests)

Imported: `transcription.client`
- `test_rejects_audio_larger_than_25_mb`
- `test_upload_sends_multipart_auth_and_parses_text`
- `test_http_error_raises_transcription_error`
- `test_language_field_depends_on_model`
- `test_unsupported_extension_is_transcoded_before_upload`
- `test_unsupported_format_http_error_retries_after_transcoding`
- `test_ogg_voice_note_is_sent_as_ogg_without_ffmpeg`
- `test_ogg_upload_sets_ogg_filename`
- `test_ogg_rejected_without_ffmpeg_explains`

### `tests/test_transcription_providers.py` (23 tests)

Imported: `protocols.config`, `server.api`, `transcription.client`, `transcription.config`, `web.api`, `web.auth`
- `test_provider_default_and_invalid_fall_back`
- `test_api_keys_per_provider_with_precedence`
- `test_models_and_urls_defaults_and_precedence`
- `test_openrouter_headers_only_when_set`
- `test_set_settings_removes_legacy_key_on_reset`
- `test_openrouter_multipart_request`
- `test_openai_headers_have_no_extras`
- `test_gemini_request_shape`
- `test_gemini_unsupported_format_is_transcoded`
- `test_gemini_rejects_large_audio`
- `test_gemini_empty_responses`
- `test_gemini_blocked`
- `test_gemini_http_error_never_leaks_key`
- `test_build_client_selects_class`
- `test_set_api_key_validation`
- `test_set_api_key_saves_and_activates_selected_provider`
- `test_legacy_openai_key_op_delegates`
- `test_provider_switch_reinits_and_keeps_on_done`
- `test_provider_settings_and_validation`
- `test_provider_invalid_values_save_nothing`
- `test_env_overrides_per_provider`
- `test_unconfigured_follows_selected_provider`
- `test_rest_endpoints`

### `tests/test_transcription_service.py` (3 tests)

Imported: `protocols.db`, `transcription`, `transcription.service`
- `test_submit_worker_persists_success`
- `test_submit_worker_persists_failure`
- `test_duplicate_pending_submission_calls_client_once`

### `tests/test_transcription_store.py` (4 tests)

Imported: `protocols.db`, `transcription`
- `test_get_missing_key_returns_none`
- `test_set_then_get_returns_persisted_record`
- `test_set_upserts_existing_key_without_duplicate`
- `test_init_db_creates_transcriptions_table_at_schema_v5`

### `tests/test_tui_bus_consumer.py` (7 tests)

Imported: `server.bus`
- `test_worker_applies_domain_events_with_view_handlers`
- `test_backend_events_go_to_ui_thread_dispatch`
- `test_gap_marks_list_dirty_and_triggers_full_flush`
- `test_feed_publishes_on_bus_and_applies_view_once`
- `test_feed_returns_false_when_core_produces_nothing`
- `test_feed_does_not_swallow_view_errors`
- `test_pump_to_view_end_to_end`

### `tests/test_tui_client_resilience.py` (34 tests)

Imported: `server.api`, `server.bus`, `server.client`, `server.config`, `server.core`, `server.socket_client`, `server.status`
- `test_every_operation_the_tui_names_has_a_timeout`
- `test_no_direct_client_call_outside_the_timeout_helper`
- `test_timeout_values`
- `test_send_passes_its_timeout`
- `test_timed_out_send_stays_pending_and_resyncs`
- `test_unreachable_server_during_send_stays_pending_without_resync`
- `test_incompatible_server_fails_the_send_with_a_persistent_message`
- `test_api_error_on_send_is_unchanged`
- `test_timed_out_edit_keeps_the_new_text_and_resyncs`
- `TestReporting::test_unavailable_is_reported_once_per_interval`
- `TestReporting::test_nothing_replaces_the_persistent_lost_message`
- `TestReporting::test_incompatible_is_persistent_and_shown_once`
- `TestReporting::test_timeout_message_names_the_action`
- `TestReporting::test_other_errors_are_left_to_the_caller`
- `TestReporting::test_restored_clears_lost_and_resyncs`
- `TestEventRouting::test_connection_event_goes_to_the_ui_handler`
- `TestEventRouting::test_gap_resyncs`
- `test_resync_fetches_everything_with_timeouts`
- `test_resync_twice_adds_no_duplicate_contacts_or_messages`
- `test_resync_marks_the_open_chat_read`
- `test_resync_rerenders_only_when_the_open_chat_changed`
- `test_resync_reports_and_aborts_on_a_transport_error`
- `test_resync_skips_a_protocol_the_server_refuses`
- `TestPendingSends::test_unknown_to_the_server_becomes_failed`
- `TestPendingSends::test_known_and_sent_is_upgraded_not_failed`
- `TestPendingSends::test_known_and_still_pending_stays_pending`
- `TestPendingSends::test_known_and_failed_on_the_server_becomes_failed`
- `test_resyncs_coalesce`
- `test_timeouts_start_at_most_one_resync_per_interval`
- `test_background_failure_is_reported_not_raised`
- `test_device_link_does_not_block_the_ui`
- `test_opening_an_unknown_contact_does_not_block_the_ui`
- `test_download_does_not_block_the_ui`
- `test_server_config_is_fetched_in_a_worker_at_mount`

### `tests/test_tui_socket_e2e.py` (1 tests)

Imported: `protocols`, `protocols.db`, `server.api`, `server.bus`, `server.core`, `server.socket_adapter`, `server.socket_client`
- `test_tui_over_the_socket_loads_sends_and_survives_a_restart`

### `tests/test_typing_indicator.py` (29 tests)

Imported: `protocols.db`, `protocols.rpc`
- `TestBackendProcessTyping::test_started`
- `TestBackendProcessTyping::test_stopped`
- `TestBackendProcessTyping::test_not_typing_envelope`
- `TestBackendProcessTyping::test_unknown_action`
- `TestBackendProcessTyping::test_missing_source`
- `TestSignalTUITyping::test_started_adds_to_typing_contacts`
- `TestSignalTUITyping::test_stopped_moves_to_mumbling`
- `TestSignalTUITyping::test_typing_envelope_not_saved_to_cache`
- `TestSignalTUITyping::test_message_moves_typing_contact_to_mumbling`
- `TestSignalTUITyping::test_message_from_non_typing_contact_no_mumbling`
- `TestSignalTUITyping::test_message_refreshes_existing_mumbling`
- `TestSignalTUITyping::test_new_started_after_message_readds_indicator`
- `TestSignalTUITyping::test_contact_label_includes_typing_icon`
- `TestSignalTUITyping::test_contact_label_icon_after_unread_badge`
- `TestSignalTUITyping::test_contact_label_includes_mumbling_icon`
- `TestSignalTUITyping::test_typing_timeout_moves_to_mumbling`
- `TestSignalTUITyping::test_mumbling_expiry_removes_indicator`
- `TestSignalTUITyping::test_started_clears_mumbling`
- `TestSignalTUITyping::test_sort_keeps_alphabetical_when_typing`
- `TestSignalTUITyping::test_sort_keeps_alphabetical_with_unread`
- `TestSignalTUITyping::test_sort_keeps_alphabetical_when_mumbling`
- `TestSignalTUITyping::test_sort_selected_typing_not_reordered`
- `TestSignalTUITyping::test_sort_selected_mumbling_not_reordered`
- `TestSignalTUITyping::test_selected_typing_still_shows_icon`
- `TestSignalTUITyping::test_selected_mumbling_still_shows_icon`
- `TestUpdateTypingLabel::test_updates_only_matching_row`
- `TestUpdateTypingLabel::test_updates_mumbling_icon`
- `TestUpdateTypingLabel::test_noop_when_row_not_rendered`
- `TestUpdateTypingLabel::test_noop_when_contact_unknown`

### `tests/test_ui_protocol.py` (55 tests)

Imported: `server.ops`
- `TestProtocolLabel::test_signal_label_name`
- `TestProtocolLabel::test_whatsapp_label_name`
- `TestProtocolLabel::test_protocol_class`
- `TestProtocolFilter::test_filtered_contacts_default_all`
- `TestProtocolFilter::test_filtered_contacts_signal`
- `TestProtocolFilter::test_filtered_contacts_whatsapp`
- `TestProtocolFilter::test_cycle_filter_order`
- `TestProtocolFilter::test_ctrl_w_binding_is_priority`
- `TestProtocolFilter::test_select_contact_no_crash_when_filtered_out`
- `TestProtocolFilter::test_select_contact_ok_without_filter_when_visible`
- `TestProtocolFilter::test_select_contact_highlights_correct_row_with_hidden_contacts`
- `TestProtocolFilter::test_apply_contact_visibility_preserves_selected_highlight`
- `TestProtocolFilter::test_apply_contact_visibility_clears_stale_highlight_after_reorder`
- `TestProtocolFilter::test_select_contact_clears_stale_highlight`
- `TestProtocolFilter::test_reorder_keeps_all_contacts_in_dom_with_filter`
- `TestProtocolFilter::test_filter_render_applies_to_view`
- `TestProtocolFilter::test_filter_title_suffix`
- `TestProtocolFilter::test_cycle_filter_does_not_pollute_chat`
- `TestProtocolFilter::test_contact_picker_uses_filtered_contacts`
- `TestProtocolFilter::test_apply_contact_filter_updates_title_by_id`
- `TestProtocolFilter::test_apply_contact_filter_colors_chat_border`
- `TestMessageAccent::test_signal_accent_class`
- `TestMessageAccent::test_whatsapp_accent_class`
- `TestMessageAccent::test_no_protocol_no_accent`
- `TestMessageAccent::test_selected_drops_accent_to_keep_highlight`
- `TestContactSorting::test_with_messages_sorted_desc`
- `TestContactSorting::test_no_messages_after_with_messages`
- `TestContactSorting::test_no_messages_alphabetical`
- `TestContactSorting::test_unnamed_no_messages_last_even_after_named_no_msg`
- `TestContactSorting::test_unnamed_with_messages_follows_date`
- `TestContactSorting::test_full_mix`
- `TestContactSorting::test_sync_last_ts_from_cache`
- `TestContactSorting::test_sync_last_ts_keeps_existing_max`
- `TestContactSorting::test_reorder_contact_list_preserves_selection`
- `TestContactListSelect::test_contact_list_widget_uses_no_select_list_view`
- `TestContactListSelect::test_contact_list_view_is_still_a_list_view`
- `TestWhatsAppHistoryLoad::test_load_messages_worker_fetches_history_for_empty_whatsapp_cache`
- `TestWhatsAppHistoryLoad::test_load_messages_worker_fetches_history_even_when_cache_nonempty`
- `TestContactListViewCrashFix::test_click_on_removed_item_does_not_crash`
- `TestContactListViewCrashFix::test_render_in_place_when_composition_unchanged`
- `TestContactListViewCrashFix::test_reorder_in_place_when_order_changes`
- `TestSendOptimisticRouting::test_whatsapp_send_ingest_uses_whatsapp_backend`
- `TestContactListFlush::test_flush_incremental_with_single_dirty_key`
- `TestContactListFlush::test_flush_falls_back_to_full_when_no_keys`
- `TestContactListFlush::test_flush_falls_back_to_full_when_too_many`
- `TestContactListFlush::test_message_for_other_contact_populates_dirty_keys`
- `TestContactListFlush::test_badge_visible_after_recompute_for_unselected_wa`
- `TestWhatsAppLoadRetry::test_worker_retries_when_fetch_returns_empty_first`
- `TestWhatsAppMountOrdering::test_worker_mounts_in_cronological_order_no_duplicates`
- `TestWhatsAppRenderFirst::test_renders_cache_before_fetch_then_appends_new`
- `TestWhatsAppRenderFirst::test_no_remount_when_fetch_adds_nothing`
- `TestWhatsAppRenderFirst::test_older_gapfill_rerenders_in_correct_order`
- `TestWhatsAppRenderFirst::test_load_more_banner_shows_for_144_messages`
- `TestWhatsAppRenderFirst::test_merge_never_shrinks_ui_cache`
- `TestWhatsAppRenderFirst::test_incoming_message_with_different_ids_not_duplicated`

### `tests/test_wa_known_groups.py` (21 tests)

Imported: `protocols.whatsapp`, `server.api`, `server.ops`, `web.api`
- `TestPaginatedListContacts::test_paginate_false_is_single_unpaginated_request`
- `TestPaginatedListContacts::test_paginate_walks_pages_and_includes_beyond_page`
- `TestPaginatedListContacts::test_paginate_stops_when_offset_is_ignored`
- `TestPaginatedListContacts::test_paginate_empty_page_stops`
- `TestPaginatedListContacts::test_paginate_transport_error_on_first_page_returns_none`
- `TestPaginatedListContacts::test_paginate_transport_error_after_first_page_returns_collected`
- `TestKnownGroupsPopulation::test_known_groups_populated_by_paginated_chats`
- `TestKnownGroupsPopulation::test_known_groups_captures_first_page_group_too`
- `TestKnownGroupsPopulation::test_known_groups_only_contains_g_us`
- `TestKnownGroupsPopulation::test_known_groups_not_populated_on_book_transport_error`
- `TestKnownGroupsPopulation::test_known_groups_rebuilt_on_connect`
- `TestKnownGroupsPopulation::test_known_groups_not_polluting_contacts`
- `TestKnownGroupsPopulation::test_known_groups_monotonic_when_group_leaves_chats`
- `TestFindContactKnownGroups::test_find_contact_resolves_group_when_book_is_none`
- `TestFindContactKnownGroups::test_known_groups_survives_address_book_invalidation`
- `TestFindContactKnownGroups::test_find_contact_unknown_group_returns_none`
- `TestFindContactKnownGroups::test_find_contact_unknown_group_zero_network`
- `TestFindContactKnownGroups::test_find_contact_order_contacts_then_book_then_known_groups`
- `TestFindContactKnownGroups::test_group_storage_keys_is_never_expanded`
- `TestWebSendKnownGroupsE2E::test_send_group_from_known_groups_200_ghost`
- `TestWebSendKnownGroupsE2E::test_send_unknown_group_404_no_send`

### `tests/test_wa_session_guard.py` (33 tests)

Imported: `protocols`, `protocols.whatsapp`, `protocols.whatsapp_rest`, `server`, `server.bus`, `server.core`, `server.link`
- `test_failed_restarts_once_then_recovers`
- `test_qr_after_restart_needs_pairing_no_more_restarts`
- `test_backoff_cap_and_gave_up`
- `test_gave_up_cleared_by_manual`
- `test_restart_window_prunes_old_restarts`
- `test_restart_failure_counts_and_backs_off`
- `test_restart_timeout_while_starting`
- `test_unreachable_alerts_once_then_recovers`
- `test_non_restartable_states`
- `test_link_flow_active_blocks_restart`
- `test_working_without_me_is_transient`
- `test_single_flight`
- `TestLifecycle::test_thread_starts_and_stops`
- `TestLifecycle::test_loop_survives_tick_errors`
- `TestLifecycle::test_disabled_by_env`
- `TestLifecycle::test_no_backend`
- `TestConfig::test_defaults`
- `TestConfig::test_invalid_falls_back`
- `TestConfig::test_zero_valid_where_allowed`
- `TestConfig::test_backoff_invalid`
- `TestConfig::test_backoff_valid`
- `TestConfig::test_get_bool`
- `TestCoreIntegration::test_start_and_stop_drive_the_guard`
- `TestCoreIntegration::test_reconnect_notes_manual`
- `TestCoreIntegration::test_reconnect_other_protocol_does_not_note`
- `TestRestSurface::test_rest_restart_session_posts`
- `TestRestSurface::test_backend_restart_session`
- `TestRestSurface::test_link_is_active`
- `TestLinkRequiredFrame::test_enter_publishes_once_and_state_flag`
- `TestLinkRequiredFrame::test_leaving_via_working_publishes_false`
- `TestLinkRequiredFrame::test_leaving_via_stopped_publishes_false`
- `TestLinkRequiredFrame::test_manual_note_publishes_false_only_when_pairing`
- `TestLinkRequiredFrame::test_publish_failure_is_swallowed`

### `tests/test_wa_startup_media_burst.py` (17 tests)

Imported: `protocols.config`, `protocols.db`, `protocols.whatsapp`, `server.messages`
- `test_t1_bulk_query_forces_download_media_false`
- `test_t2_bulk_never_schedules_the_media_resolver`
- `test_t2b_media_none_without_text_is_not_ingested`
- `test_t2b_media_none_with_text_is_ingested_as_text`
- `test_t2d_skipped_mine_media_still_emits_read_receipt`
- `test_t2c_mixed_bulk_ingests_text_and_url_media_only`
- `test_t3_cap_and_tie_break_unread_then_ts_then_jid`
- `test_cache_last_ts_skips_invalid_rows_and_returns_max`
- `test_select_resync_targets_filters_non_string_and_broadcast_jids`
- `test_t5_single_flag_rollback_restores_download_and_resolution`
- `test_t5b_full_rollback_uses_download_limit_workers_and_no_cap`
- `test_t4_zero_max_chats_is_unlimited_and_broadcast_excluded`
- `test_t6_waha_down_stays_on_db_chats_without_raising`
- `test_t7_limit_workers_and_constant_stagger`
- `test_t8_chat_open_refresh_keeps_positional_call_when_lazy_media_off`
- `test_t9_out_of_window_media_are_absent_not_empty_bubbles`
- `test_t10_config_validation_and_negative_max_chats`

### `tests/test_wa_startup_resync.py` (16 tests)

Imported: `protocols.db`, `protocols.whatsapp`, `purge_whatsapp_cache`
- `TestResyncHistory::test_targets_are_unread_union_of_db_cached_chats`
- `TestResyncHistory::test_no_targets_does_not_fetch`
- `TestResyncHistory::test_errors_are_nonfatal`
- `TestResyncHistory::test_disconnected_returns_zero`
- `TestReconcileUnread::test_marks_locally_unread_as_read_when_waha_reports_zero`
- `TestReconcileUnread::test_throttled_no_repeat_rest_call_within_min_interval`
- `TestReconcileUnread::test_disconnected_is_a_noop`
- `TestReconcileUnread::test_rest_error_is_swallowed`
- `test_purge_removes_only_whatsapp`
- `test_purge_creates_backup_before_delete`
- `test_purge_is_failsafe_when_whatsapp_offline`
- `TestWaitSessionReady::test_returns_true_when_working`
- `TestWaitSessionReady::test_retries_until_working`
- `TestWaitSessionReady::test_times_out_returns_false`
- `TestWaitSessionReady::test_none_status_keeps_polling_to_timeout`
- `test_resync_requests_only_fetchable_numeric_jids`

### `tests/test_web_account_link_contract.py` (19 tests)

Imported: `server.api`, `server.link`, `web.api`, `web.auth`
- `test_auth_required`
- `test_status_and_required_equal_operations`
- `test_start_png_passthrough`
- `test_start_converts_raw_qr_to_png`
- `test_start_message_kinds_unchanged`
- `test_poll_without_and_with_qr`
- `test_poll_without_link_is_409`
- `test_invalid_protocol_is_rejected`
- `test_cancel_and_reconnect`
- `test_reconnect_without_host_support_is_501`
- `test_qr_as_png_renders_image`
- `test_qr_as_png_passthrough`
- `test_link_required_reflects_host`
- `test_link_required_host_without_method_or_failing`
- `test_finalize_equals_operation_and_requires_auth`
- `test_finalize_error_maps_to_502_with_message`
- `test_submit_2fa_equals_operation`
- `test_submit_2fa_error_mapping`
- `test_submit_2fa_password_is_never_logged`

### `tests/test_web_api_contract.py` (13 tests)

Imported: `server.ops`, `server.status`, `web.api`, `web.auth`, `web.ops`
- `test_emoji_matches_operation`
- `test_reactions_matches_operation`
- `test_status_matches_operation`
- `test_contacts_matches_operation`
- `test_contacts_reconcile_failure_is_swallowed`
- `test_contacts_book_matches_operation`
- `test_contacts_book_requires_q`
- `test_messages_read_matches_operation_and_marks_read`
- `test_messages_read_strips_and_swallows_backend_errors`
- `test_messages_read_invalid_is_400`
- `test_chats_archive_matches_operation`
- `test_chats_archive_invalid_is_400`
- `test_chats_archive_backend_errors`

### `tests/test_web_autotranscribe.py` (9 tests)

Imported: `protocols.config`, `web.api`, `web.auth`
- `test_requires_auth`
- `test_config_matches_operation`
- `test_set_global_roundtrip`
- `test_set_global_invalid_protocol_and_value`
- `test_chat_override_set_and_clear`
- `test_chat_contact_id_with_special_chars`
- `test_chat_invalid_requests`
- `test_static_ui_wiring`
- `test_static_wiring_for_transcription_settings`

### `tests/test_web_avatar.py` (31 tests)

Imported: `protocols`, `protocols.rpc`, `protocols.signal`, `server`, `web.api`
- `test_avatar_downloads_and_serves_jpeg`
- `test_avatar_without_photo_writes_negative_marker`
- `test_avatar_errors_do_not_write_negative_marker`
- `test_avatar_rejects_other_protocols`
- `test_avatar_rejects_empty_contact_id`
- `test_avatar_cache_hit_and_ttl_refresh`
- `test_concurrent_avatar_requests_fetch_once`
- `test_avatar_download_failure_does_not_poison_negative_cache`
- `test_avatar_missing_backend_returns_no_photo`
- `test_avatar_lock_is_shared_per_path`
- `test_negative_marker_is_used_within_ttl`
- `test_static_assets_declare_thread_avatar_and_stealth`
- `test_thread_avatar_opens_image_modal_only_with_photo`
- `test_attach_contact_avatar_calls_on_loaded_from_cache`
- `test_stealth_disables_contact_avatar_url_in_node`
- `test_signal_avatar_serves_jpeg`
- `test_signal_avatar_serves_png`
- `test_signal_avatar_missing_returns_404`
- `test_signal_avatar_uuid_literal`
- `test_signal_avatar_phone_fallback_for_uuid`
- `test_signal_avatar_profile_fallback`
- `test_signal_avatar_prefers_profile_over_contact`
- `test_signal_avatar_rejects_path_traversal`
- `test_resolve_avatar_path_rejects_unsafe`
- `test_telegram_avatar_serves_jpeg`
- `test_telegram_avatar_serves_png`
- `test_telegram_avatar_missing_returns_404`
- `test_telegram_avatar_cache_hit_and_ttl_refresh`
- `test_concurrent_telegram_avatar_requests_fetch_once`
- `test_telegram_negative_marker_used_within_ttl`
- `test_resolve_avatar_dispatches_telegram`

### `tests/test_web_contact_update.py` (6 tests)

Imported: `protocols.whatsapp`, `server.bus`, `server.core`, `server.events_api`, `web.legacy_events`
- `test_dispatcher_routes_contact_update`
- `test_contact_update_marks_dirty_and_pushes_web`
- `test_contact_update_without_web_does_not_push`
- `test_materialized_contact_is_added_to_tui_list_on_message`
- `test_materialized_contact_is_added_to_tui_list_on_sent_mirror`
- `test_placeholder_is_registered_with_backend`

### `tests/test_web_edit.py` (8 tests)

Imported: `protocols.db`, `server.bus`, `web.api`, `web.auth`
- `test_edit_requires_bearer_token`
- `test_edit_rejects_invalid_payload`
- `test_edit_returns_404_for_unknown_or_wrong_scope`
- `test_edit_returns_500_for_database_lookup_error`
- `test_edit_rejects_non_editable_message`
- `test_edit_maps_backend_failure_to_502`
- `test_edit_signal_idless_message_uses_timestamp_fallback`
- `test_edit_success_applies_locally_and_pushes_event`

### `tests/test_web_edit_persistence.py` (1 tests)

Imported: `protocols.db`, `web.api`, `web.auth`
- `test_web_edit_persists_when_message_is_not_in_backend_cache`

### `tests/test_web_edit_reaction_contract.py` (10 tests)

Imported: `protocols`, `protocols.db`, `server.api`, `server.bus`, `web.api`, `web.auth`, `web.ops`
- `test_edit_validation_failures`
- `test_edit_missing_field_is_invalid_request`
- `test_edit_not_found_and_not_editable`
- `test_edit_backend_failure`
- `test_edit_database_error`
- `test_edit_success_matches_operation_and_frame`
- `test_reaction_validation_failures`
- `test_reaction_backend_missing_and_message_missing`
- `test_reaction_send_failure`
- `test_reaction_success_matches_operation_and_frame`

### `tests/test_web_gmail_contract.py` (25 tests)

Imported: `protocols.db`, `server`, `server.api`, `server.link_preview`, `server.models_api`, `server.uploads`, `web.gmail_api`, `web.ops`
- `test_status_matches_operation`
- `test_list_matches_operation_and_passes_the_query`
- `test_get_delete_mark_read_match_operations`
- `test_bulk_delete_matches_operation`
- `test_bulk_delete_request_errors`
- `test_bulk_delete_malformed_json`
- `test_error_mapping_matches_operation`
- `test_attachment_operation_returns_a_private_temporary_blob`
- `test_attachment_route_bytes_headers_and_cleanup`
- `test_attachment_route_errors_match_operation`
- `test_image_route_matches_operation_and_cleans_up`
- `test_image_forged_token_never_reaches_the_fetcher`
- `test_image_tampered_payload_is_rejected`
- `test_image_fetch_failure_is_502`
- `test_send_matches_operation`
- `test_send_without_files_and_empty_optional_fields`
- `test_send_invalid_reply_uid`
- `test_send_too_many_attachments_stores_nothing`
- `test_send_check_is_side_effect_free`
- `test_send_oversized_file_is_413_in_file_order`
- `test_send_total_limit_is_413`
- `test_send_transport_cuts_off_a_file_over_the_limit`
- `test_send_accepts_any_file_type_up_to_the_gmail_limit`
- `test_send_operation_rejects_bad_slots`
- `test_send_domain_error_texts_come_from_gmail`

### `tests/test_web_group_sender.py` (8 tests)

Imported: `protocols.db`, `protocols.whatsapp`, `server.messages`
- `test_lid_row_without_number_resolves_by_digits`
- `test_lid_row_with_number_resolves_via_alias`
- `test_lid_resolved_through_backend_cache_then_phone`
- `test_c_us_sender_resolves_by_local_phone`
- `test_unknown_lid_falls_back_to_readable_number_not_jid`
- `test_backend_without_jid_to_phone_does_not_crash`
- `test_address_book_error_is_swallowed`
- `test_whatsapp_backend_end_to_end_lid_alias`

### `tests/test_web_legacy_events.py` (18 tests)

Imported: `server.bus`, `web.legacy_events`
- `TestMessage::test_added`
- `TestMessage::test_changed_only`
- `TestMessage::test_neither_added_nor_changed_is_not_sent`
- `TestMessage::test_missing_id_and_is_mine`
- `TestMessage::test_contact_id_is_the_resolved_one`
- `TestMessage::test_frame_is_json_serializable`
- `TestOtherEvents::test_message_edit`
- `TestOtherEvents::test_reaction_update`
- `TestOtherEvents::test_receipt_normalizes_updates`
- `TestOtherEvents::test_typing`
- `TestOtherEvents::test_contact_update`
- `TestOtherEvents::test_contact_update_missing_fields_are_none`
- `TestOtherEvents::test_contact_merge_whatsapp`
- `TestOtherEvents::test_merged_into_ignored_outside_whatsapp`
- `TestOtherEvents::test_lid_warmup_done`
- `TestOtherEvents::test_web_push_is_passed_through_unchanged`
- `TestOtherEvents::test_web_push_without_dict_is_dropped`
- `TestOtherEvents::test_no_legacy_frame`

### `tests/test_web_link_preview.py` (13 tests)

Imported: `protocols.location_thumbs`, `server.link_preview`, `web.api`
- `test_parse_preview_prefers_open_graph_and_resolves_image`
- `test_parse_preview_falls_back_to_title_and_host`
- `test_parse_preview_drops_private_image`
- `test_validate_url_blocks`
- `test_validate_url_allows_public`
- `test_resolver_rejects_private_answers`
- `test_fetch_follows_redirect_and_serves_image`
- `test_fetch_external_image_reads_the_whole_body`
- `test_redirect_to_blocked_host_gives_no_preview`
- `test_endpoint_returns_preview_without_image_url`
- `test_endpoint_rejects_blocked_and_missing`
- `test_endpoint_404_when_no_preview`
- `test_location_link_uses_stored_thumbnail_without_network`

### `tests/test_web_links_contract.py` (6 tests)

Imported: `server.api`, `server.client_log`, `server.link_preview`, `web.api`, `web.auth`
- `test_preview_matches_operation_and_sets_cache_header`
- `test_preview_image_matches_operation_and_headers`
- `test_blocked_urls_are_refused_by_operations_and_routes`
- `test_missing_preview_and_image_are_404`
- `test_preview_routes_require_auth`
- `test_client_log_route_and_operation`

### `tests/test_web_media_kind.py` (4 tests)

Imported: `protocols.db`, `server.messages`
- `test_messages_exposes_media_kind_and_keeps_type_fallback`
- `test_file_attachment_renders_kind_icon_name_and_opens_media`
- `test_file_attachment_shows_unavailable_whatsapp_media_on_404`
- `test_message_media_type_prefers_kind_and_keeps_reconciliation_categories`

### `tests/test_web_media_version.py` (12 tests)

Imported: `protocols.rpc`, `server.media`, `server.messages`, `server.thumbnails`, `web.api`
- `test_attachment_version_signal_present_and_missing`
- `test_attachment_version_rejects_traversal_and_symlink`
- `test_attachment_version_unknown_protocol`
- `test_attachment_version_uses_raw_id_for_query`
- `test_attachment_version_telegram_rejects_tgref_and_outside`
- `test_attach_media_versions_is_best_effort_when_root_fails`
- `test_attach_media_versions_resolves_root_once`
- `test_whatsapp_version_never_downloads`
- `test_messages_endpoint_attaches_and_refreshes_versions`
- `test_media_endpoint_ignores_version_param`
- `test_thumbnail_invalidates_on_size_change_with_same_mtime`
- `test_spa_media_version_and_failure_namespaces`

### `tests/test_web_messages_contract.py` (10 tests)

Imported: `protocols.db`, `server.api`, `web.api`, `web.auth`, `web.ops`
- `test_one_to_one_matches_operation`
- `test_unknown_chat_is_empty`
- `test_limit_and_since_semantics`
- `test_invalid_limit_is_400_in_route_and_operation`
- `test_invalid_protocol_is_rejected_by_route`
- `test_group_resolves_sender_and_quote_author`
- `test_group_detected_from_contact_extras`
- `test_whatsapp_ghost_id_is_canonicalized_and_unioned`
- `test_history_refresh_policy`
- `test_telegram_refresh_runs_off_the_event_loop_and_errors_are_swallowed`

### `tests/test_web_messages_delta.py` (5 tests)

Imported: `protocols.db`, `web.api`
- `test_default_returns_everything_oldest_first`
- `test_limit_returns_newest_in_chronological_order`
- `test_since_returns_delta_inclusive`
- `test_since_and_limit_combined`
- `test_invalid_limit_rejected`

### `tests/test_web_multi_host.py` (5 tests)

Imported: `protocols`, `web.server`
- `test_parse_hosts`
- `test_format_hosts_brackets_ipv6`
- `test_config_web_host_accepts_a_list`
- `test_listens_on_every_host`
- `test_one_unbindable_host_brings_the_web_down`

### `tests/test_web_ops.py` (6 tests)

Imported: `server.api`, `web.ops`
- `test_every_error_code_has_an_http_status`
- `test_error_codes_map_to_status_and_detail`
- `test_unknown_operation_is_404`
- `test_sync_handler_runs_off_the_loop_thread_and_async_on_it`
- `test_get_api_returns_the_api_the_app_was_given`
- `test_web_adapter_has_no_manager_fallback`

### `tests/test_web_outgoing_mirror.py` (51 tests)

Imported: `protocols`, `protocols.db`, `protocols.manager`, `protocols.signal`, `protocols.telegram`, `protocols.whatsapp`, `server.bus`, `server.core`
- `test_facade_send_enqueues_outgoing_event_that_is_ingested`
- `test_facade_reply_mirrors_complete_quote_into_tui_ingest`
- `test_facade_signal_media_reply_derives_quote_text_from_descriptor`
- `test_signal_enqueue_sent_message_prefers_real_quote_message`
- `test_facade_send_attachment_enqueues_event_with_media_data`
- `test_web_image_mirror_has_empty_text_and_resolvable_attachment`
- `test_web_document_mirror_keeps_filename_and_caption_text`
- `test_image_caption_race_always_persists_caption`
- `test_image_echo_filename_does_not_replace_existing_caption`
- `test_mirror_copy_failure_warns_and_enqueues_without_attachment`
- `test_signal_attachment_rpc_and_echo_reuse_persistent_file`
- `test_signal_attachment_filename_is_sanitized_and_collision_safe`
- `test_signal_named_attachment_is_upgraded_by_outgoing_echo`
- `test_signal_attachment_forwards_quote_attachments`
- `test_facade_send_echo_upgrades_optimistic_without_duplicate`
- `test_signal_send_attachments_sync_materializes_batch_rows`
- `test_signal_send_attachments_sync_empty_list_sends_nothing`
- `test_signal_send_attachments_barrier_blocks_echo_until_complete`
- `test_signal_batch_echo_before_barrier_still_gets_batch_slots`
- `test_signal_batch_partial_echo_before_barrier_still_gets_all_slots`
- `test_signal_batch_repair_more_rows_than_files_leaves_extras_slotless`
- `test_signal_batch_slots_survive_cache_reload`
- `test_signal_multi_attachment_echo_upgrades_each_mirror_row`
- `test_signal_same_filename_attachments_keep_unambiguous_association`
- `test_signal_multi_attachment_echo_without_full_mirror_never_duplicates`
- `test_signal_send_attachments_barrier_rollback_cleans_partial_rows`
- `test_signal_barrier_rollback_clears_sent_attachment_registry`
- `test_whatsapp_send_attachments_sync_sends_one_call_per_file`
- `test_whatsapp_send_attachments_sync_routes_kind_per_file`
- `test_whatsapp_send_attachments_sync_stops_early_on_failure`
- `test_whatsapp_manager_batch_mirrors_n_events_with_batch_index`
- `test_manager_batch_without_batch_id_keeps_legacy_backend_mirror`
- `test_manager_batch_with_batch_id_forwards_batch_metadata`
- `test_telegram_send_attachments_sync_sends_album`
- `test_telegram_send_attachments_sync_scales_timeout_with_batch_size`
- `test_telegram_send_attachments_sync_normalizes_single_message`
- `test_telegram_send_attachments_sync_rejects_invalid_ids`
- `test_telegram_manager_batch_mirrors_n_events_with_batch_index`
- `test_telegram_batch_slot_fuses_on_echo_mirror_dedup`
- `test_telegram_batch_slots_fuse_per_message`
- `test_telegram_batch_slot_fuses_onto_optimistic_row`
- `test_telegram_batch_slot_never_overwrites_existing_batch`
- `test_whatsapp_batch_slot_fuses_on_echo_mirror_dedup`
- `test_whatsapp_batch_slot_fuses_onto_optimistic_row`
- `test_whatsapp_batch_slot_never_overwrites_existing_batch`
- `test_sent_mirror_event_is_routed_to_dedicated_handler`
- `test_sent_mirror_updates_real_contact_object_and_flags`
- `test_sent_mirror_prefers_self_contacts_over_rebuilt_payload_copy`
- `test_sent_mirror_uses_backend_identify_when_not_in_tui_list`
- `test_sent_mirror_creates_placeholder_for_unknown_contact`
- `test_sent_mirror_does_not_ingest_or_push_web_event`

### `tests/test_web_phase2_fixes.py` (40 tests)

Imported: `protocols.db`, `protocols.signal`, `protocols.telegram`, `protocols.whatsapp`, `server.messages`, `web.api`
- `test_messages_refreshes_only_telegram_history`
- `test_messages_refreshes_whatsapp_history_when_refresh_requested`
- `test_messages_refresh_without_fetch_history_is_safe`
- `test_telegram_attachment_upgrade_works_in_both_event_orders`
- `test_telegram_tgref_prefers_mirrored_file`
- `test_signal_unresolved_echo_is_upgraded_to_sent_attachment`
- `test_signal_echo_never_adds_row_and_keeps_mirrored_file`
- `test_signal_outgoing_attachment_is_resolvable_or_null`
- `test_batch_columns_added_to_modern_versioned_db`
- `test_batch_columns_present_on_fresh_db`
- `test_messages_exposes_batch_fields`
- `test_web_intra_key_dedup_single_key`
- `test_web_intra_key_dedup_keeps_multi_attachment`
- `test_web_intra_key_dedup_keeps_batch_members`
- `test_load_cache_orders_same_timestamp_rows_deterministically`
- `test_telegram_persists_content_type_and_api_marks_tgref_as_image`
- `test_web_api_does_not_use_mimetype_as_caption`
- `test_web_document_shows_filename_and_caption`
- `test_web_does_not_hide_literal_media_caption`
- `test_push_on_attachment_upgrade`
- `test_receipt_event_pushes_normalized_web_update`
- `test_receipt_event_does_not_push_when_web_is_disabled`
- `test_edit_event_pushes_web_update`
- `test_edit_event_does_not_push_when_web_is_disabled`
- `test_whatsapp_echo_first_is_upgraded_to_sent_attachment`
- `test_signal_and_whatsapp_never_downgrade_sent_attachment`
- `test_whatsapp_dedup_fills_missing_quote_fields`
- `test_whatsapp_text_quote_reconciliation_is_timestamp_tolerant_and_unique`
- `test_reconcile_multi_falls_back_to_signature_without_batch_match`
- `test_web_image_retry_recovers_expires_and_aborts`
- `test_web_reconcile_caches_preview_and_reuses_it_without_fetching`
- `test_web_media_cache_lru_revokes_only_oldest_url`
- `test_web_prune_orphans_preserves_draft_preview_urls`
- `test_web_media_renders_reuse_inflight_request_and_limit_concurrency`
- `test_web_thread_switch_aborts_media_but_submit_does_not`
- `test_web_messages_dedup_synthetic_and_blank_same_attachment`
- `test_web_messages_keeps_distinct_attachment_ids`
- `test_web_messages_preserves_batch_members`
- `test_web_intra_key_dedup_ts_zero_prefers_real_timestamp`
- `test_web_intra_key_dedup_guards_is_mine`

### `tests/test_web_plugin.py` (94 tests)

Imported: `protocols`, `protocols.base`, `protocols.db`, `protocols.manager`, `protocols.rpc`, `protocols.signal`, `protocols.telegram`, `protocols.whatsapp`, `server`, `server.bus`, `web`, `web.api`, `web.auth`, `web.server`, `web.uploads`, `web.ws`
- `test_rest_auth_requires_correct_bearer`
- `test_rest_no_auth_accepts_missing_or_wrong_bearer`
- `test_emoji_requires_bearer_token`
- `test_emoji_uses_raw_predefined_categories`
- `test_available_reactions_are_telegram_only`
- `test_send_requires_bearer_token`
- `test_send_accepts_origin_matching_host`
- `test_send_accepts_https_origin_matching_forwarded_host`
- `test_send_rejects_public_origin_without_forwarded_host`
- `test_send_rejects_forwarded_host_when_proxy_reports_http`
- `test_send_rejects_origin_different_from_effective_host`
- `test_send_routes_reply_metadata_according_to_protocol`
- `test_whatsapp_text_reply_strips_boundary_whitespace_from_quote_metadata`
- `test_send_rejects_empty_text`
- `test_send_internal_timeout_waits_for_worker_and_returns_generic_502`
- `test_spa_submit_image_caption_keeps_caption_in_optimistic_message`
- `test_optimistic_image_caption_reconciles_echo_without_duplicate`
- `test_optimistic_image_without_caption_still_reconciles_by_media_signature`
- `test_optimistic_plain_text_still_reconciles_without_attachment`
- `test_optimistic_image_caption_with_quote_reconciles_echo`
- `test_render_optimistic_image_preview_includes_caption`
- `test_optimistic_dedup_two_identical_messages_is_one_to_one`
- `test_optimistic_dedup_accepts_echo_after_120_seconds`
- `test_optimistic_failed_message_remains_without_echo`
- `test_optimistic_reply_reconciles_echo_with_partial_quote_metadata`
- `test_whatsapp_optimistic_reply_reconciles_echo_without_quote`
- `test_whatsapp_media_reply_reconciles_echo_without_quote_timestamp`
- `test_whatsapp_media_reply_fallback_requires_a_unique_row`
- `test_optimistic_image_reply_reconciles_waha_media_placeholder_echo`
- `test_optimistic_media_reply_reconciles_quote_forms`
- `test_optimistic_signal_image_reply_reconciles_forwarded_quote_echo`
- `test_optimistic_telegram_image_reply_reconciles_media_echo`
- `test_optimistic_image_reconciles_media_placeholder_and_legacy_upload_echo`
- `test_backend_manager_send_message_sync_routes_to_backend`
- `test_backend_manager_send_attachment_sync_routes_to_backend`
- `test_backend_manager_send_attachments_sync_routes_to_backend`
- `test_backend_manager_send_attachments_sync_signal_notifies_once`
- `test_backend_manager_send_attachments_sync_whatsapp_enqueues_per_message`
- `test_base_send_attachments_sync_defaults_to_sequential_single_sends`
- `test_base_send_attachments_sync_forwards_extended_kwargs`
- `test_backend_manager_send_attachments_sync_falls_back_to_single_sends`
- `test_send_multipart_image_routes_and_cleans_temporary_file`
- `test_send_multipart_pdf_routes_as_document`
- `test_send_signal_media_reply_builds_quote_attachment_descriptor`
- `test_send_signal_media_reply_rejects_unknown_attachment`
- `test_send_multipart_whatsapp_lid_uses_resolved_chat_id`
- `test_send_multipart_whatsapp_unresolved_lid_returns_502`
- `test_send_multipart_image_routes_quote`
- `test_send_multipart_rejects_image_over_20_mib`
- `test_send_multipart_rejects_non_image_magic`
- `test_send_multipart_maps_backend_errors`
- `test_send_multipart_rejects_non_upload_file_field`
- `test_starlette_form_repeated_file_field_keeps_order_and_get_returns_last`
- `test_send_multipart_single_file_keeps_batch_contract`
- `test_send_multipart_three_files_send_batch_in_order`
- `test_send_multipart_text_only_without_files_still_sends`
- `test_send_multipart_rejects_more_than_max_attachments`
- `test_send_multipart_normalizes_starlette_too_many_files`
- `test_send_multipart_rejects_batch_over_total_byte_cap`
- `test_send_multipart_chunked_enforces_total_cap_while_storing`
- `test_send_multipart_cleanup_when_second_file_is_invalid_media`
- `test_send_multipart_cleanup_when_second_file_is_oversize`
- `test_send_multipart_cleanup_when_batch_send_fails`
- `test_upload_janitor_removes_only_files_older_than_one_hour`
- `test_websocket_rejects_missing_token`
- `test_websocket_accepts_browser_subprotocol_token`
- `test_websocket_no_auth_accepts_connection_without_any_token`
- `test_media_whatsapp_accepts_full_waha_url_and_masks_download_failure`
- `test_media_telegram_accepts_absolute_path_only_inside_media_root`
- `test_media_telegram_tgref_download_success_and_failure`
- `test_media_rejects_nested_traversal_and_symlink_escape`
- `test_media_signal_serves_file_inside_configured_root_and_rejects_outside`
- `test_contacts_schema_order_and_unread_count`
- `test_contacts_calls_whatsapp_reconcile_unread`
- `test_contacts_swallows_reconcile_unread_error`
- `test_messages_schema_filters_and_stable_chronological_order`
- `test_messages_exposes_delivery_and_edit_state`
- `test_messages_edit_id_rules`
- `test_messages_exposes_persisted_quote_fields`
- `test_messages_never_serializes_image_placeholder_text`
- `test_messages_distinguishes_video_placeholders_from_captions`
- `test_messages_preserves_image_caption_cleanup`
- `test_messages_attachment_info_exposes_only_real_image_captions`
- `test_web_ui_static_contracts`
- `test_messages_infers_missing_image_content_type`
- `test_whatsapp_messages_filter_only_synthetic_media_text`
- `test_web_push_is_bounded_nonblocking_and_overflow_becomes_gap`
- `test_occupied_port_degrades_to_down_without_raising`
- `test_clean_start_and_stop_web_server`
- `test_start_web_server_requires_token_by_default`
- `test_start_web_server_no_auth_works_without_a_token`
- `test_health_reports_auth_required_flag`
- `test_default_tui_import_does_not_import_optional_web_package`
- `test_web_send_text_dedup_but_attachments_stay_legacy`

### `tests/test_web_quote_thumbs.py` (14 tests)

Imported: `protocols.db`, `server.media`, `server.messages`, `web.api`, `web.auth`
- `test_message_payload_maps_quote_thumbnails_and_raw_fields`
- `test_signal_embedded_video_thumbnail_is_content_type_agnostic`
- `test_quote_media_thumbnail_is_cached`
- `test_quote_media_rejects_missing_and_unsafe_paths`
- `test_quote_media_requires_bearer`
- `test_quote_media_database_error_returns_500`
- `test_spa_renders_eager_quote_thumbnails`
- `test_spa_optimistic_reply_thumbnail_all_protocols`
- `test_spa_start_reply_marks_video`
- `test_quote_thumb_resolved_by_filename_when_timestamp_missing`
- `test_quote_video_resolver_path_timestamp_and_filename`
- `test_quote_filename_fallback_ignores_unknown_names_and_future`
- `test_quote_thumb_resolved_by_reply_to_message_id_telegram`
- `test_quote_thumb_whatsapp_prefixed_msg_id`

### `tests/test_web_reactions_api.py` (5 tests)

Imported: `protocols.db`, `server.messages`
- `test_messages_aggregates_and_orders_reactions_and_omits_empty_messages`
- `test_messages_deduplicates_and_sorts_non_empty_authors`
- `test_messages_matches_reactions_by_message_id`
- `test_messages_matches_signal_legacy_reactions_by_timestamp`
- `test_messages_omits_reactions_after_last_reaction_is_removed`

### `tests/test_web_retry_verification.py` (35 tests)

Imported: `server.api`, `server.ops`, `server.send_registry`, `web.ops`, `web.ws`
- `test_indep_duplicate_same_payload_single_send`
- `test_indep_conflict_same_id_different_text_422_never_drops`
- `test_indep_conflict_same_text_different_quote_422`
- `test_indep_different_contact_is_separate_key_not_conflict`
- `test_indep_502_releases_inflight_and_allows_later_retry`
- `test_indep_inflight_409_then_release_allows_retry`
- `test_indep_attachments_never_touch_registry`
- `test_indep_legacy_missing_client_msg_id_not_deduped`
- `test_indep_unicode_and_whitespace_text_distinct`
- `test_indep_registry_young_sent_survives_capacity_pressure`
- `test_indep_registry_sent_removed_only_after_ttl`
- `test_indep_registry_release_and_mark_sent_wrong_token_noop`
- `test_indep_registry_stale_inflight_reclaimable_and_different_token`
- `test_indep_registry_thread_safety_single_winner`
- `test_indep_outbox_classify_full_table`
- `test_indep_outbox_build_send_payload_matches_legacy_wire`
- `test_indep_outbox_dispatched_persisted_before_fetch_and_confirm_window`
- `test_indep_outbox_retention_two_days`
- `test_indep_outbox_recover_retry_reset_and_suspend`
- `test_indep_outbox_inmemory_fallback_when_no_indexeddb`
- `test_indep_outbox_max_attempts_cap`
- `test_indep_spa_composer_cleared_only_after_enqueue`
- `test_indep_spa_legacy_path_when_outbox_absent`
- `test_indep_spa_static_contracts_for_triggers_and_logout`
- `test_indep_reconcile_restored_outbox_must_not_match_older_row`
- `test_indep_reconcile_cross_contact_isolated`
- `test_indep_spa_no_innerhtml_with_user_payload_in_outbox_ui`
- `test_indep_concurrent_same_id_produces_single_send`
- `test_indep_manual_retry_while_flush_in_progress_is_not_lost`
- `test_indep_outbox_falls_back_when_idb_open_fails`
- `test_indep_reconcile_restored_matches_newer_echo_and_flag_path`
- `test_indep_fresh_send_reconciliation_not_regressed_by_guard`
- `test_indep_init_outbox_falls_back_to_memory_on_recover_failure`
- `test_indep_registry_stale_sent_beyond_ttl_is_reclaimed`
- `test_indep_fresh_empty_chat_echo_earlier_than_client_still_reconciles`

### `tests/test_web_send_address_book.py` (35 tests)

Imported: `protocols.base`, `protocols.signal`, `protocols.telegram`, `protocols.whatsapp`, `server.api`, `server.bus`, `server.core`, `server.ops`, `web.api`, `web.legacy_events`
- `TestFindContact::test_base_defaults`
- `TestFindContact::test_signal_address_book_is_contacts`
- `TestFindContact::test_whatsapp_prefers_active_chat_over_book`
- `TestFindContact::test_whatsapp_falls_back_to_book`
- `TestFindContact::test_telegram_falls_back_to_book`
- `TestCacheOnlyLookup::test_send_never_consults_manager_list_contacts`
- `TestCacheOnlyLookup::test_unit_lookup_touches_no_network`
- `TestCacheOnlyLookup::test_send_book_only_never_fetches`
- `test_register_contact_barrier_single_append`
- `test_register_lock_is_class_scoped`
- `TestActiveChatRegression::test_find_contact_does_not_touch_book`
- `TestActiveChatRegression::test_send_active_chat_skips_registration_and_keeps_id`
- `TestWhatsAppIdentity::test_book_only_send_keeps_client_id_everywhere`
- `TestWhatsAppIdentity::test_book_only_attachment_keeps_client_id`
- `TestWhatsAppIdentity::test_alias_points_to_same_client_contact`
- `TestWhatsAppIdentity::test_alias_setdefault_never_overwrites_real_lid`
- `TestWhatsAppIdentity::test_alias_is_created_when_cache_fills_later`
- `TestWhatsAppIdentity::test_alias_retried_on_second_send_after_lid_cache_fills`
- `TestWhatsAppIdentity::test_register_lid_alias_early_return_without_phone_or_c_us`
- `TestWhatsAppIdentity::test_incoming_lid_event_ingests_under_client_contact`
- `TestWhatsAppIdentity::test_phone_to_lid_applies_ttl`
- `TestTelegramBookOnlySend::test_book_only_send_registers_and_indexes_id`
- `TestCacheSemantics::test_stale_cache_is_usable`
- `TestCacheSemantics::test_missing_cache_returns_none_and_404`
- `TestCacheSemantics::test_unknown_id_with_populated_book_404`
- `TestRegisterContactContract::test_returns_true_then_false`
- `TestRegisterContactContract::test_dedup_is_by_cache_key_not_eq`
- `TestRegisterContactContract::test_overrides_update_index_only_on_append`
- `TestBookSendRace::test_t1_resolve_does_not_drop_book_then_send_succeeds`
- `TestBookSendRace::test_t2_rebuild_rewrites_id_and_fallback_finds_by_phone`
- `TestBookSendRace::test_t3_paginated_lid_chat_fallback_finds_by_phone`
- `TestBookSendRace::test_t4_tie_break_discards_lid_unresolved`
- `TestBookSendRace::test_t5_unknown_phone_stays_none`
- `TestBookSendRace::test_t5b_malformed_plus_id_does_not_use_fallback`
- `TestConcurrentSendWhileBookRebuilds::test_t14_no_404_no_duplicate_no_exception`

### `tests/test_web_send_contract.py` (28 tests)

Imported: `protocols.db`, `server.api`, `server.bus`, `server.models_api`, `server.ops`, `server.retry`, `server.send_registry`, `server.uploads`, `web.api`, `web.auth`, `web.ops`
- `test_json_text_send_matches_operation_and_publishes_message_frame`
- `test_request_body_cannot_select_local_frontend_modes`
- `test_duplicate_client_msg_id_is_idempotent_like_the_operation`
- `test_client_msg_id_reused_with_other_payload_is_422_like_the_operation`
- `test_inflight_claim_is_409_like_the_operation`
- `test_backend_failure_retries_then_502_like_the_operation`
- `test_terminal_backend_failure_is_502_without_retry_frames`
- `test_json_validation_failures_match_operation_errors`
- `test_missing_backend_is_404_like_the_operation`
- `test_reply_validation_matches_operation`
- `test_json_body_cannot_inject_attachment_paths`
- `test_multipart_attachments_match_operation_and_temp_files_are_removed`
- `test_attachments_are_not_deduplicated_or_retried`
- `test_multipart_unsupported_media_matches_operation`
- `test_multipart_kind_limit_matches_operation`
- `test_multipart_total_budget_is_reported_in_file_order`
- `test_too_many_attachments_matches_operation`
- `test_multipart_without_text_or_files_is_invalid`
- `test_attachment_backend_failures_match_operation`
- `test_multipart_quote_timestamp_form_conversion`
- `test_operation_registered_with_schema`
- `test_rejected_multipart_never_stores_uploads`
- `test_missing_backend_with_files_never_stores_uploads`
- `test_non_file_field_named_file_never_stores_uploads`
- `test_form_field_named_attachments_cannot_break_the_check`
- `test_duplicate_and_inflight_text_sends_store_nothing`
- `test_failed_upload_store_takes_no_claim_and_sends_nothing`
- `test_check_operation_is_registered_and_sends_nothing`

### `tests/test_web_send_retry.py` (42 tests)

Imported: `server.api`, `server.ops`, `server.retry`, `server.send_registry`, `web.ops`, `web.ws`
- `test_classify_send_error_signal`
- `test_classify_send_error_whatsapp`
- `test_classify_send_error_telegram`
- `test_classify_send_error_unknown_protocol_is_terminal`
- `test_classify_send_error_never_raises_when_str_explodes`
- `test_compute_delay_grows_with_attempt`
- `test_compute_delay_is_deterministic_with_seed`
- `test_compute_delay_bounds`
- `test_send_success_first_attempt_no_retry`
- `test_send_retries_twice_then_succeeds`
- `test_send_terminal_error_does_not_retry`
- `test_send_exhausts_retries_returns_502`
- `test_send_attachments_never_retries`
- `test_send_without_client_msg_id_still_succeeds`
- `test_send_client_msg_id_over_128_falls_back_to_uuid`
- `test_web_send_retry_frontend_contract`
- `test_send_retry_survives_exploding_str_and_succeeds`
- `test_classify_whatsapp_http_error_retryable`
- `test_compute_delay_large_attempt_is_finite`
- `test_compute_delay_non_positive_attempt_is_positive`
- `test_client_msg_id_fallback_variants`
- `test_send_retry_error_message_is_truncated_to_200_chars`
- `test_index_html_bumps_static_asset_versions`
- `test_send_registry_claim_release_and_mark_sent_require_token`
- `test_send_registry_inflight_branch_does_not_mutate`
- `test_send_registry_sent_ttl_prune_only_after_expiry`
- `test_send_registry_capacity_pressure_keeps_young_sent`
- `test_send_registry_stale_inflight_is_reclaimable`
- `test_send_registry_concurrent_claim_has_single_winner`
- `test_build_fingerprint_stable_and_payload_sensitive`
- `test_send_duplicate_client_msg_id_is_idempotent`
- `test_send_same_client_msg_id_different_payload_conflicts_422`
- `test_send_registry_is_scoped_per_contact`
- `test_send_502_releases_inflight_claim`
- `test_send_inflight_returns_409_without_registry_mutation`
- `test_send_attachments_are_not_deduplicated`
- `test_outbox_classify_and_delay_contract`
- `test_outbox_build_send_payload_matches_wire_rules`
- `test_outbox_dispatched_persisted_before_fetch_and_auto_resend_window`
- `test_outbox_recover_orphan_sending_records`
- `test_app_submit_text_uses_outbox_when_available`
- `test_outbox_retryable_backoff_and_max_attempts`

### `tests/test_web_shared_session.py` (14 tests)

Imported: `server`, `server.link_preview`, `web.server`
- `test_two_fetches_in_one_loop_share_one_client_session`
- `test_shared_session_does_not_persist_cookies`
- `test_ssrf_guard_blocks_before_any_request`
- `test_two_asyncio_run_calls_create_distinct_sessions`
- `test_loop_isolation_creates_per_loop_sessions_and_skips_cross_loop_close`
- `test_close_shared_session_closes_session_connector_and_resolver`
- `test_close_shared_session_handles_already_closed_session`
- `test_close_shared_session_closes_resolver_when_session_close_raises`
- `test_disabled_shared_session_uses_transient_closed_in_finally`
- `test_disabled_shared_session_closes_transient_on_error`
- `test_disabled_shared_session_during_shutdown_creates_nothing`
- `test_concurrent_fetches_share_session_and_respect_connector_limit`
- `test_web_server_shutdown_closes_the_shared_session_on_the_web_loop`
- `test_api_close_loop_resources_closes_on_the_running_loop_and_never_raises`

### `tests/test_web_status.py` (4 tests)

Imported: `web.api`, `web.auth`
- `test_status_reports_connected_backend_with_zero_contacts`
- `test_status_reports_unregistered_protocol_as_false`
- `test_status_reports_registered_but_not_yet_connected_backend`
- `test_status_requires_auth`

### `tests/test_web_thumbs.py` (11 tests)

Imported: `protocols.rpc`, `server.video_thumbs`, `web.api`
- `test_thumbnail_is_small_jpeg_and_second_request_is_cached`
- `test_non_thumbnail_media_and_invalid_width_serve_original`
- `test_m4a_media_uses_audio_mp4_content_type`
- `test_m4a_media_ignores_mimetypes_contamination`
- `test_aac_media_keeps_audio_aac_content_type`
- `test_oga_media_uses_audio_ogg_content_type`
- `test_non_thumbnail_image_keeps_guessed_content_type`
- `test_thumbnail_request_keeps_path_validation`
- `test_concurrent_thumbnail_requests_generate_once`
- `test_spa_lazy_negative_cache_and_upload_optimization`
- `test_spa_video_thumbnail_badge_dispatch_and_text_fallback`

### `tests/test_web_transcribe.py` (7 tests)

Imported: `server.media`, `web.api`, `web.auth`
- `test_post_returns_503_without_service`
- `test_post_returns_cached_transcription`
- `test_post_returns_404_when_attachment_cannot_be_resolved`
- `test_post_submits_resolved_attachment`
- `test_get_returns_transcription_status`
- `test_post_rejects_invalid_request`
- `test_get_status_by_query_keeps_url_shaped_whatsapp_id`

### `tests/test_web_transcribe_contract.py` (9 tests)

Imported: `protocols.config`, `server.api`, `server.media`, `web.api`, `web.auth`
- `test_config_matches_operation`
- `test_status_matches_operation`
- `test_unconfigured_is_503_with_the_same_detail`
- `test_submit_confines_the_audio_to_the_media_root`
- `test_openai_key_rejects_invalid_values`
- `test_openai_key_is_saved_and_the_service_restarted`
- `test_openai_key_save_and_init_failures`
- `test_host_init_without_key_leaves_no_service`
- `test_host_stop_is_idempotent`

### `tests/test_web_upload_media.py` (5 tests)

Imported: `server.uploads`
- `test_store_upload_sniffs_media_kind`
- `test_store_upload_sanitizes_original_filename`
- `test_store_upload_applies_kind_limit`
- `test_store_upload_rejects_incoherent_extension`
- `test_store_upload_rejects_unknown_signature`

### `tests/test_web_video_thumbs.py` (10 tests)

Imported: `protocols.rpc`, `protocols.telegram`, `server`, `server.media`, `server.video_thumbs`, `web.api`
- `test_mp4_box_parser_detects_faststart_and_tail_moov`
- `test_video_thumbnail_invokes_ffmpeg_and_caches_jpeg`
- `test_is_video_candidate_uses_content_type_and_extension`
- `test_media_video_thumbnail_returns_jpeg`
- `test_media_video_thumbnail_unavailable_returns_422`
- `test_chunk_pipeline_uses_head_and_tail_and_stable_cache`
- `test_chunk_pipeline_faststart_uses_only_head`
- `test_whatsapp_media_endpoint_uses_chunk_without_full_download`
- `test_chunk_failure_falls_back_to_complete_file`
- `test_insufficient_chunk_without_complete_file_returns_422`

### `tests/test_web_voice_note_upload.py` (22 tests)

Imported: `protocols.db`, `protocols.rpc`, `protocols.whatsapp`, `server.media`, `server.uploads`, `web.api`
- `test_sniff_webm_kind_codec_ids`
- `test_mp4_has_video_track_audio_only_returns_audio`
- `test_mp4_has_video_track_video_returns_video`
- `test_mp4_has_video_track_r1_audio_first_video_second_returns_video`
- `test_mp4_has_video_track_moov_in_tail_returns_none`
- `test_mp4_has_video_track_extended_size_moov_is_a_known_gap`
- `test_classify_upload_is_pure_and_uses_content_type_hint`
- `test_store_webm_audio_only_without_hint`
- `test_store_webm_video`
- `test_store_webm_audio_with_content_type_hint`
- `test_store_m4a_mp42_tail_with_audio_hint`
- `test_store_mp4_audio_hint_incoherent_suffix_stays_video`
- `test_store_png_ignores_audio_hint`
- `test_store_r3_iso_brands_do_not_400`
- `test_store_unknown_ftyp_brand_rejects_400`
- `test_store_mixed_voice_batch_no_400_and_mime`
- `test_media_content_type_prefer_audio_only_for_webm`
- `test_is_video_candidate_rejects_audio_content_type`
- `test_is_video_candidate_rejects_audio_media_kind_without_content_type`
- `test_is_video_candidate_keeps_video_webm_and_mp4`
- `test_whatsapp_audio_media_kind_routes_to_file_not_video`
- `test_media_endpoint_serves_webm_by_persisted_kind`

### `tests/test_web_ws_bus.py` (5 tests)

Imported: `server.bus`, `web`
- `test_core_events_are_sent_as_legacy_frames_in_order`
- `test_web_push_dicts_reach_the_socket_unchanged`
- `test_heartbeat_is_sent_when_idle`
- `test_without_a_bus_the_broadcaster_idles_without_error`
- `test_events_before_the_subscription_are_not_replayed`

### `tests/test_web_ws_v2.py` (16 tests)

Imported: `server.bus`, `web`, `web.server`
- `test_live_stream_sends_every_event_type_as_a_wire_frame`
- `test_since_query_replays_missed_events_then_goes_live`
- `test_subscribe_frame_replays_from_since_without_losing_or_repeating`
- `test_events_published_during_the_grace_period_are_delivered`
- `test_gap_when_since_fell_out_of_the_buffer`
- `test_gap_when_the_connection_queue_overflows`
- `test_since_beyond_the_head_is_clamped_to_live`
- `test_heartbeat_when_idle_and_without_a_bus`
- `test_malformed_requests`
- `test_unauthenticated_connection_is_refused`
- `test_refused_connection_gets_no_events_even_with_a_since`
- `test_bearer_header_and_browser_subprotocol_are_accepted`
- `test_legacy_socket_still_gets_legacy_frames_next_to_v2`
- `test_legacy_socket_ignores_since_and_never_replays`
- `test_connections_are_tracked_for_shutdown_and_released`
- `test_real_server_streams_v2_resumes_and_closes_on_stop`

### `tests/test_whatsapp_archived.py` (9 tests)

Imported: `protocols.whatsapp`, `protocols.whatsapp_rest`, `web.api`
- `test_normalize_chat_reads_archived_flag`
- `test_set_chat_archived_calls_waha_endpoint`
- `test_contacts_carry_archived_flag`
- `test_archive_action_updates_contact_and_notifies`
- `test_archive_action_failure_leaves_flag`
- `test_chat_archive_webhook_from_phone`
- `test_refresh_updates_archived_flag_in_place`
- `test_archive_endpoint`
- `test_archive_endpoint_rejects_bad_input_and_reports_failure`

### `tests/test_whatsapp_backend.py` (243 tests)

Imported: `protocols`, `protocols.config`, `protocols.db`, `protocols.manager`, `protocols.whatsapp`, `protocols.whatsapp_rest`, `server.bus`, `server.core`, `server.domain`
- `TestWhatsAppRESTClient::test_protocol`
- `TestWhatsAppRESTClient::test_get_message_media_encodes_path_and_forces_download`
- `TestWhatsAppRESTClient::test_list_contacts`
- `TestWhatsAppRESTClient::test_list_contacts_nested_data`
- `TestWhatsAppRESTClient::test_list_contacts_uses_chats_directly`
- `TestWhatsAppRESTClient::test_list_contacts_chats_parses_last_ts`
- `TestWhatsAppRESTClient::test_get_pairing_qr_text_fallback`
- `TestWhatsAppRESTClient::test_get_pairing_qr_binary_png`
- `TestWhatsAppRESTClient::test_get_pairing_qr_starts_session_when_stopped`
- `TestWhatsAppRESTClient::test_get_fresh_pairing_qr_resets_then_starts`
- `TestWhatsAppRESTClient::test_get_fresh_pairing_qr_no_reset`
- `TestWhatsAppRESTClient::test_get_fresh_pairing_qr_waits_for_scan_state`
- `TestWhatsAppRESTClient::test_get_fresh_pairing_qr_returns_none_on_timeout`
- `TestWhatsAppRESTClient::test_reset_session_uses_logout_then_returns`
- `TestWhatsAppRESTClient::test_send_message`
- `TestWhatsAppRESTClient::test_send_message_uses_waha_reply_to`
- `TestWhatsAppRESTClient::test_send_message_error_returns_none`
- `TestWhatsAppRESTClient::test_request_sends_api_key_header`
- `TestWhatsAppRESTClient::test_request_skips_api_key_header_when_unset`
- `TestWhatsAppRESTClient::test_http_401_returns_none_and_sets_last_status`
- `TestWhatsAppEvents::test_message_event`
- `TestWhatsAppEvents::test_status_broadcast_is_ignored`
- `TestWhatsAppEvents::test_typing_event`
- `TestWhatsAppEvents::test_receipt_event`
- `TestWhatsAppEvents::test_ack_delivery_event`
- `TestWhatsAppEvents::test_ack_read_event`
- `TestWhatsAppEvents::test_ack_server_ack_ignored`
- `TestWhatsAppEvents::test_ack_not_mine_ignored`
- `TestWhatsAppEvents::test_ack_no_chat_id_returns_none`
- `TestWhatsAppEvents::test_ack_no_msg_id_returns_none`
- `TestWhatsAppEvents::test_ack_dispatch_via_raw`
- `TestWhatsAppEvents::test_ack_slash_variant_dispatch`
- `TestWhatsAppEvents::test_dispatch_by_event_type`
- `TestWhatsAppEvents::test_group_message_extracts_sender_from_participant`
- `TestWhatsAppEvents::test_group_message_sender_falls_back_to_jid`
- `TestWhatsAppEvents::test_group_message_sender_from_sender_field`
- `TestWhatsAppEvents::test_group_message_jid_resolved_to_contact_name`
- `TestWhatsAppEvents::test_group_message_jid_exact_match`
- `TestWhatsAppEvents::test_group_message_jid_not_in_contacts_keeps_jid`
- `TestWhatsAppEvents::test_mention_in_text_resolved_to_contact_name`
- `TestWhatsAppEvents::test_mention_unknown_contact_left_unchanged`
- `TestWhatsAppEvents::test_mention_no_matching_contact_among_known_contacts_left_unchanged`
- `TestWhatsAppEvents::test_mention_short_digit_run_not_treated_as_phone_number`
- `TestWhatsAppEvents::test_mention_multiple_in_same_message_all_resolved`
- `TestWhatsAppEvents::test_mention_falls_back_to_phone_via_lid_lookup`
- `TestWhatsAppEvents::test_mention_contact_name_preferred_over_lid_phone_fallback`
- `TestWhatsAppEvents::test_mention_unresolved_by_name_or_lid_lookup_left_unchanged`
- `TestWhatsAppEvents::test_mention_unresolved_schedules_background_lid_resolve`
- `TestWhatsAppEvents::test_mention_resolved_by_name_does_not_schedule`
- `TestWhatsAppEvents::test_direct_message_not_group`
- `TestWhatsAppEvents::test_hasMedia_image`
- `TestWhatsAppEvents::test_hasMedia_caption_in_body`
- `TestWhatsAppEvents::test_hasMedia_no_caption_keeps_mime`
- `TestWhatsAppEvents::test_hasMedia_video`
- `TestWhatsAppEvents::test_hasMedia_audio`
- `TestWhatsAppEvents::test_attachments_without_mime_fall_back_to_document`
- `TestWhatsAppEvents::test_hasMedia_without_mime_falls_back_to_document`
- `TestWhatsAppEvents::test_ack_media_without_mime_falls_back_to_document`
- `TestWhatsAppEvents::test_nested_media_kind_protocol_hints`
- `TestWhatsAppEvents::test_media_synthetic_text_uses_url_then_parent_part_identity`
- `TestWhatsAppEvents::test_single_media_without_id_or_url_uses_parent_part_identity_in_all_forms`
- `TestWhatsAppEvents::test_hasMedia_no_media_dict`
- `TestWhatsAppEvents::test_hasMedia_no_text_key_still_recognized_by_msg`
- `TestWhatsAppEvents::test_event_from_raw_fallback_recognizes_hasMedia_without_text_key`
- `TestWhatsAppEvents::test_event_from_raw_fallback_accepts_hasMedia_video_too`
- `TestWhatsAppBackend::test_list_contacts_from_rest`
- `TestWhatsAppBackend::test_load_contacts_sets_phone_for_c_us`
- `TestWhatsAppBackend::test_load_contacts_sets_phone_for_resolved_lid`
- `TestWhatsAppBackend::test_load_contacts_no_phone_for_unresolved_lid`
- `TestWhatsAppBackend::test_load_contacts_preserves_jid_and_last_ts_with_phone`
- `TestWhatsAppBackend::test_send_message_sync_calls_rest`
- `TestWhatsAppBackend::test_resolve_send_chat_id_uses_cached_lid_phone`
- `TestWhatsAppBackend::test_resolve_send_chat_id_registers_reverse_alias_from_cache`
- `TestWhatsAppBackend::test_resolve_send_chat_id_registers_reverse_alias_from_remote`
- `TestWhatsAppBackend::test_resolve_send_chat_id_leaves_c_us_chats_untouched`
- `TestWhatsAppBackend::test_resolve_send_chat_id_unresolvable_lid_raises_without_alias`
- `TestWhatsAppBackend::test_webhook_echo_c_us_after_send_resolves_to_lid_contact`
- `TestWhatsAppBackend::test_receipt_scoped_lid_matches_outgoing_row_after_send`
- `TestWhatsAppBackend::test_unresolved_lid_never_reaches_send_request`
- `TestWhatsAppBackend::test_send_message_forwards_reply_to_message_id`
- `TestWhatsAppBackend::test_send_message_sync_raises_when_unreachable`
- `TestWhatsAppBackend::test_send_attachment_sync_sends_waha_file_object`
- `TestWhatsAppBackend::test_send_attachment_sync_routes_media_kind`
- `TestWhatsAppBackend::test_send_video_falls_back_to_send_file_on_missing_endpoint`
- `TestWhatsAppBackend::test_send_attachment_sync_reports_waha_error`
- `TestWhatsAppBackend::test_waha_error_redacts_data_url`
- `TestWhatsAppBackend::test_is_connected_false_before_connect`
- `TestWhatsAppBackend::test_is_connected_true_once_connect_completes`
- `TestWhatsAppBackend::test_needs_pairing`
- `TestWhatsAppBackend::test_get_attachment_path`
- `TestWhatsAppBackend::test_get_attachment_path_missing_downloads`
- `TestWhatsAppBackend::test_get_attachment_path_download_fails_returns_none`
- `TestWhatsAppBackend::test_get_attachment_path_forces_redownload_after_stale_url`
- `TestWhatsAppBackend::test_get_attachment_chunk_uses_fresh_media_url`
- `TestWhatsAppBackend::test_download_media_range_rewrites_url_and_requires_206`
- `TestWhatsAppBackend::test_get_attachment_path_forced_redownload_without_url_returns_none`
- `TestWhatsAppBackend::test_resolve_wa_media_chat_id_from_url`
- `TestWhatsAppBackend::test_get_attachment_path_no_rest_returns_none`
- `TestWhatsAppBackend::test_download_media_direct_binary`
- `TestWhatsAppBackend::test_download_media_falls_back_to_legacy_url`
- `TestWhatsAppBackend::test_download_media_returns_none_when_all_fail`
- `TestWhatsAppBackend::test_download_media_direct_url`
- `TestWhatsAppBackend::test_download_media_url_fetch_failure_falls_back_to_id_endpoint`
- `TestWhatsAppBackend::test_download_media_url_without_extension_keeps_complete_id`
- `TestWhatsAppBackend::test_download_media_encodes_at_sign`
- `TestWhatsAppBackend::test_poll_once_drains_queue`
- `TestWhatsAppBackend::test_ingest_message_dedup`
- `TestWhatsAppBackend::test_ingest_message_keeps_distinct_same_second_with_ids`
- `TestWhatsAppBackend::test_ingest_message_dedup_falls_back_without_id`
- `TestWhatsAppBackend::test_two_identical_texts_confirmed_not_merged`
- `TestWhatsAppBackend::test_three_images_rapid_send_not_merged`
- `TestWhatsAppBackend::test_echo_upgrades_only_idless_row`
- `TestWhatsAppBackend::test_media_race_row_is_upgraded_by_later_echo`
- `TestWhatsAppBackend::test_named_attachment_mirror_reconciles_waha_echo`
- `TestWhatsAppBackend::test_echo_out_of_order_picks_closest_idless`
- `TestWhatsAppBackend::test_ack_echo_media_does_not_duplicate`
- `TestWhatsAppBackend::test_ack_echo_text_still_dedups_optimistic`
- `TestWhatsAppBackend::test_ack_echo_media_reverse_order`
- `TestWhatsAppMediaResolver::test_schedule_deduplicates_and_resolver_enqueues_image`
- `TestWhatsAppMediaResolver::test_resolver_gives_up_after_three_attempts`
- `TestWhatsAppMentionLidResolver::test_schedule_deduplicates_and_resolver_populates_lid_cache`
- `TestWhatsAppMentionLidResolver::test_resolver_gives_up_after_three_attempts`
- `TestWhatsAppMentionLidResolver::test_empty_jid_and_stopped_resolver_are_noops`
- `TestWhatsAppMentionLidResolver::test_second_distinct_jid_reuses_already_running_thread`
- `TestWhatsAppMentionLidResolver::test_start_resolver_is_a_noop_once_stopped`
- `TestWhatsAppMentionLidResolver::test_resolve_exception_is_swallowed_and_retried`
- `TestWhatsAppMentionLidResolver::test_stop_flag_mid_batch_breaks_out_without_processing_rest`
- `TestWhatsAppMentionLidResolver::test_unexpected_error_in_loop_body_is_swallowed`
- `TestWhatsAppMentionLidResolver::test_entry_vanished_before_first_recheck_is_skipped`
- `TestWhatsAppMentionLidResolver::test_entry_vanished_before_second_recheck_is_skipped`
- `TestWhatsAppMediaIdentityUpdate::test_does_not_overwrite_existing_attachment`
- `TestWhatsAppMediaIdentityUpdate::test_updates_missing_attachment`
- `TestWhatsAppWebhook::test_media_race_webhook_schedules_resolver_once`
- `TestWhatsAppWebhook::test_handle_webhook_enqueues_message_event`
- `TestWhatsAppWebhook::test_handle_webhook_deduplicates_retried_id`
- `TestWhatsAppWebhook::test_attachment_parts_have_stable_text_and_live_dedup_is_per_part`
- `TestWhatsAppWebhook::test_single_media_has_filename_info_and_empty_text`
- `TestWhatsAppWebhook::test_outgoing_echo_and_ack_read_keep_the_parent_message_id`
- `TestWhatsAppWebhook::test_handle_webhook_ignores_non_message_events`
- `TestWhatsAppWebhook::test_handle_webhook_normalizes_from_message_nested_key`
- `TestWhatsAppWebhook::test_handle_webhook_image_message_ack_retains_image_fields`
- `TestWhatsAppWebhook::test_handle_webhook_image_ack_caption_in_body`
- `TestWhatsAppWebhook::test_handle_webhook_image_message_ack_without_hasMedia_is_plain`
- `TestWhatsAppWebhook::test_list_messages_uses_worker_timeout`
- `TestWhatsAppWebhook::test_list_messages_rest`
- `TestWhatsAppWebhook::test_fetch_history_normalizes_and_ingests`
- `TestWhatsAppConfigGating::test_disabled_when_no_api_url_and_no_local`
- `TestWhatsAppConfigGating::test_enabled_with_api_url`
- `TestWhatsAppConfigGating::test_enabled_when_local_waha_detected`
- `TestWhatsAppConfigGating::test_resolve_whatsapp_api_url_prefers_configured`
- `TestWhatsAppConfigGating::test_resolve_whatsapp_api_url_falls_back_to_local_port`
- `TestWhatsAppConfigGating::test_api_key_prefers_env_over_config_and_dotenv`
- `TestWhatsAppConfigGating::test_api_key_falls_back_to_config`
- `TestWhatsAppConfigGating::test_api_key_falls_back_to_dotenv_waha`
- `TestWhatsAppConfigGating::test_api_key_empty_when_nowhere`
- `TestWAHAContract::test_webhook_path_is_delivery_contract`
- `TestWAHAContract::test_rest_paths_are_api_prefixed`
- `TestWAHAContract::test_waha_ws_frame_unwraps_payload`
- `TestWAHAContract::test_waha_ws_frame_chat_is_object`
- `TestWAHAContract::test_waha_message_any_recognized`
- `TestWAHAContract::test_webhook_image_via_hasMedia_end_to_end`
- `TestWAHAContract::test_webhook_image_via_message_any_end_to_end`
- `TestWAHAContract::test_webhook_image_via_nested_message_imageMessage`
- `TestWAHAContract::test_webhook_image_via_nested_message_imageMessage_no_text_key`
- `TestSeedCacheFromDB::test_multipart_attachments_persist_and_seed_after_restart_without_schema_change`
- `TestSeedCacheFromDB::test_single_media_variants_share_a_canonical_identity`
- `TestSeedCacheFromDB::test_connect_sync_seeds_cache_from_db`
- `TestSeedCacheFromDB::test_connect_sync_dedup_prevents_db_duplicates`
- `TestSeedCacheFromDB::test_db_seeded_cache_keeps_distinct_same_second_with_ids`
- `TestSeedCacheFromDB::test_optimistic_send_echo_upgrades_id_no_duplicate`
- `TestSeedCacheFromDB::test_legacy_idless_sent_does_not_swallow_new_message`
- `TestSeedCacheFromDB::test_echo_with_nested_key_id_deduped_and_upgrades`
- `TestSeedCacheFromDB::test_load_contacts_single_call`
- `TestSeedCacheFromDB::test_load_contacts_stays_empty_after_single_call`
- `TestSeedCacheFromDB::test_load_contacts_merges_address_book`
- `TestSeedCacheFromDB::test_load_contacts_skips_groups`
- `TestSeedCacheFromDB::test_load_contacts_skips_lid_unresolved`
- `TestSeedCacheFromDB::test_lid_resolver_reapplies_names`
- `TestSeedCacheFromDB::test_address_book_lid_without_number`
- `TestSeedCacheFromDB::test_looks_like_phone`
- `TestSeedCacheFromDB::test_build_address_book_name_map`
- `TestSeedCacheFromDB::test_placeholder_uses_address_book_name`
- `TestWhatsAppWebhookRegistration::test_update_session_config_puts_config`
- `TestWhatsAppWebhookRegistration::test_configure_webhook_registers_when_missing`
- `TestWhatsAppWebhookRegistration::test_configure_webhook_skips_when_already_registered`
- `TestWhatsAppWebhookRegistration::test_configure_webhook_updates_when_events_outdated`
- `TestWhatsAppWebhookRegistration::test_configure_webhook_never_raises_on_error`
- `TestWhatsAppWebhookRegistration::test_configure_webhook_noop_without_rest`
- `TestWhatsAppWebhookRegistration::test_init_registers_webhook_from_connect_sync`
- `TestWhatsAppQuoteMedia::test_wa_quote_nested_media_placeholders`
- `TestWhatsAppQuoteMedia::test_wa_quote_sticker_fixture`
- `TestWhatsAppQuoteMedia::test_wa_quote_flat_type_and_mimetype`
- `TestWhatsAppQuoteMedia::test_wa_quote_media_uses_explicit_description`
- `TestWhatsAppQuoteMedia::test_waha_reply_to_image_base64_uses_placeholder`
- `TestWhatsAppQuoteMedia::test_wa_quote_unknown_is_none`
- `test_waha_reply_to_echo_normalizes_text_quote_metadata`
- `test_tui_optimistic_reply_is_upgraded_by_waha_echo_without_quote_loss`
- `test_fetch_history_rejects_non_fetchable_jid_without_rest_call`
- `test_fetch_history_negative_caches_no_lid_500_for_session`
- `test_fetch_history_falls_back_to_lid_when_cus_empty`
- `test_fetch_history_no_lid_fallback_when_cus_has_data`
- `test_fetch_history_cus_not_blacklisted_when_lid_fallback_exists`
- `test_fetch_history_blacklists_lid_when_both_no_lid`
- `test_resolve_fetch_lid_prefers_contact_extras_over_phone_cache`
- `test_resolve_fetch_lid_falls_back_to_phone_cache`
- `test_request_http_error_logs_local_status_and_single_line`
- `test_mark_read_uses_send_seen_and_404_still_marks_local`
- `TestWhatsAppLiveContactLidResolver::test_n1_apply_updates_existing_target_without_materialize`
- `TestWhatsAppLiveContactLidResolver::test_n1_apply_materializes_unknown_contact`
- `TestWhatsAppLiveContactLidResolver::test_n2_concurrent_load_and_register_never_torn`
- `TestWhatsAppLiveContactLidResolver::test_n2_apply_materialize_survives_concurrent_load`
- `TestWhatsAppLiveContactLidResolver::test_n3_mention_entry_upgraded_to_contact_is_applied`
- `TestWhatsAppLiveContactLidResolver::test_b2_cooldown_after_give_up_blocks_then_rearms`
- `TestWhatsAppLiveContactLidResolver::test_cache_hit_materializes_without_rest_and_readable_name`
- `TestWhatsAppLiveContactLidResolver::test_cache_hit_real_name_is_not_reapplied`
- `TestWhatsAppLiveContactLidResolver::test_cache_hit_known_raw_lid_display_is_upgraded_once`
- `TestWhatsAppLiveContactLidResolver::test_b3_is_placeholder_display_name`
- `TestWhatsAppLiveContactLidResolver::test_end_to_end_webhook_unknown_lid_resolves_display`
- `TestWhatsAppLiveContactLidResolver::test_web_manager_list_contacts_exposes_resolved_name`
- `TestWhatsAppLiveContactLidResolver::test_dedup_mention_then_contact_single_resolve`
- `TestWhatsAppMediaNameSemantics::test_document_with_caption_keeps_filename_and_caption`
- `TestWhatsAppMediaNameSemantics::test_document_without_filename_falls_back_to_mime_not_caption`
- `TestWhatsAppMediaNameSemantics::test_document_filename_equal_to_caption_is_kept_twice`
- `TestWhatsAppMediaNameSemantics::test_b2_nested_document_caption_preserved`
- `TestWhatsAppMediaNameSemantics::test_b3_restricted_predicate`
- `TestWhatsAppMediaNameSemantics::test_incoming_dedup_serialized_and_hex_same_attachment`
- `TestWhatsAppMediaNameSemantics::test_incoming_dedup_serialized_and_hex_reverse_order`
- `TestWhatsAppMediaNameSemantics::test_incoming_dedup_distinct_attachment_ids_kept`
- `TestWhatsAppMediaNameSemantics::test_incoming_two_media_without_id_or_caption_kept`
- `TestWhatsAppMediaNameSemantics::test_incoming_distinct_canonical_ids_not_collapsed`
- `TestWhatsAppMediaNameSemantics::test_ack_non_image_filename_does_not_become_caption`
- `TestWhatsAppMediaNameSemantics::test_ack_non_image_real_caption_kept`
- `TestWhatsAppMediaNameSemantics::test_self_heal_updates_caption_info_to_filename`
- `TestWhatsAppMediaNameSemantics::test_self_heal_ignores_empty_incoming_info`
- `TestIncomingAttachmentIdFirstDedup::test_incoming_msg_id_match_precedes_text_placeholder`
- `TestIncomingAttachmentIdFirstDedup::test_incoming_msg_id_different_keeps_text_fallback`
- `TestWhatsAppWebhookConfig::test_configure_webhook_skips_put_while_working`
- `TestWhatsAppWebhookConfig::test_configure_webhook_updates_while_starting`
- `TestWhatsAppWebhookConfig::test_configure_webhook_noop_when_working_and_matching`
- `TestWhatsAppWebhookConfig::test_client_configure_webhook_payload`
- `TestWhatsAppWebhookConfig::test_client_configure_webhook_failure`
- `TestAddressBookNeverNoneAfterFirstBuild::test_t6_apply_contact_lid_resolution_keeps_populated_book`
- `TestAddressBookNeverNoneAfterFirstBuild::test_t8_transport_error_keeps_cache_and_does_not_bump_ts`

### `tests/test_whatsapp_e2e_notification.py` (7 tests)

Imported: `protocols.whatsapp_events`
- `test_identity_change_becomes_system_notice`
- `test_identity_change_resolves_contact_name`
- `test_identity_change_falls_back_to_lid_phone`
- `test_unresolved_lid_schedules_background_resolve`
- `test_other_e2e_subtypes_are_dropped`
- `test_top_level_type_is_honoured`
- `test_plain_text_message_is_unaffected`

### `tests/test_whatsapp_fix_40_41.py` (26 tests)

Imported: `protocols.whatsapp`, `protocols.whatsapp_events`
- `TestWAHAAckConstants::test_constants_match_official_enum`
- `TestWAHAAckConstants::test_ack_value_names`
- `TestWAHAAckConstants::test_ack_value_int_ack_is_authoritative`
- `TestWAHAAckConstants::test_ack_value_unknown_name_returns_none`
- `TestWAHAAckConstants::test_event_from_ack_new_thresholds`
- `TestFetchHistoryAckThresholds::test_ack2_delivered_ack3_read_ack1_none`
- `TestWebhookAck2Ordering::test_ack2_enqueues_message_then_receipt`
- `TestTypingOfficialShape::test_typing_started`
- `TestTypingOfficialShape::test_recording_started`
- `TestTypingOfficialShape::test_paused_stopped`
- `TestTypingOfficialShape::test_online_offline_unavailable_filtered`
- `TestTypingOfficialShape::test_legacy_scalar_fallback`
- `TestTypingMultiPresencePriority::test_composing_wins_over_paused`
- `TestTypingMultiPresencePriority::test_paused_wins_over_online`
- `TestTypingMultiPresencePriority::test_only_online_offline_filtered`
- `TestTypingEndToEnd::test_webhook_presence_update_enqueues_typing`
- `TestWebhookDesiredEvents::test_desired_events_include_presence_update`
- `TestWebhookDesiredEvents::test_no_put_when_presence_update_already_subscribed`
- `TestWebhookDesiredEvents::test_put_when_events_outdated`
- `TestPresenceSubscribe::test_rest_endpoint_percent_encodes_jid`
- `TestPresenceSubscribe::test_idempotent_guard_prevents_double_post`
- `TestPresenceSubscribe::test_subscribe_best_effort_when_rest_returns_none`
- `TestPresenceSubscribe::test_subscribe_post_never_raises`
- `TestPresenceSubscribe::test_lazy_subscribe_marks_and_spawns_thread`
- `TestPresenceSubscribe::test_disabled_by_default_noop`
- `TestPresenceSubscribe::test_sweep_subscribes_known_chats`

### `tests/test_whatsapp_hd_dual_upload.py` (6 tests)

Imported: `protocols`, `protocols.whatsapp`, `protocols.whatsapp_events`
- `test_is_hd_child_detection`
- `test_event_from_message_drops_hd_child_keeps_parent`
- `test_fetch_history_ingests_only_parent`
- `test_webhook_hd_child_is_not_enqueued`
- `test_webhook_parent_is_enqueued`
- `test_ack_of_outgoing_hd_child_creates_no_message`

### `tests/test_whatsapp_location.py` (9 tests)

Imported: `protocols.location_thumbs`, `protocols.whatsapp_events`
- `test_flat_location_webjs`
- `test_nested_location_message_outgoing`
- `test_live_location`
- `test_unknown_base64_body_is_not_shown_raw`
- `test_plain_text_untouched`
- `test_location_thumbnail_saved_from_nested_jpeg`
- `test_location_thumbnail_saved_from_webjs_body`
- `test_non_jpeg_thumbnail_is_ignored`
- `test_maps_url_round_trip`

### `tests/test_whatsapp_outgoing_attachment_dedup.py` (25 tests)

Imported: `protocols`, `protocols.manager`, `protocols.whatsapp`
- `test_echo_first_url_then_sent_mirror_is_single_entry`
- `test_mirror_first_then_url_stays_single_entry`
- `test_ack_follow_up_without_attachment_is_ignored`
- `test_two_distinct_sent_mirrors_same_msg_id_not_merged`
- `test_boot_dedup_fuses_url_and_sent_mirror`
- `test_boot_dedup_keeps_highest_status`
- `test_boot_dedup_null_status_does_not_downgrade`
- `test_boot_dedup_preserves_multiword_caption_text`
- `test_boot_dedup_clears_synthetic_url_text`
- `test_boot_dedup_transfers_real_caption_from_other_row`
- `test_boot_dedup_does_not_lose_real_caption`
- `test_boot_dedup_filename_wins_over_caption_in_info`
- `test_boot_dedup_preserves_batch_quote_and_edited`
- `test_boot_dedup_skips_ambiguous_three_row_group`
- `test_boot_dedup_skips_two_url_rows_same_attachment`
- `test_boot_dedup_outside_window_not_fused`
- `test_boot_dedup_is_idempotent`
- `test_run_startup_maintenance_force_and_once`
- `test_run_startup_maintenance_concurrent_blocks_and_runs_once`
- `test_run_startup_maintenance_failure_does_not_wedge`
- `test_connect_all_survives_maintenance_failure`
- `test_path_form_mirror_classifier_consistent`
- `test_signal_multi_attachment_same_msg_id_untouched`
- `test_incoming_whatsapp_multi_media_untouched`
- `test_is_whatsapp_synthetic_media_text`

### `tests/test_whatsapp_read_receipt_fix.py` (14 tests)

Imported: `protocols.db`, `protocols.whatsapp`, `server.ops`
- `TestSendReturnsBaileysId::test_send_message_sync_returns_flat_id`
- `TestSendReturnsBaileysId::test_send_message_sync_returns_nested_key_id`
- `TestSendReturnsBaileysId::test_send_message_sync_returns_none_without_id`
- `TestSendReturnsBaileysId::test_extract_message_id_probes_multiple_fields`
- `TestWebhookSingleMutationPoint::test_handle_webhook_ack_does_not_mutate_cache`
- `TestHistoryReconciliation::test_fetch_history_emits_read_receipt`
- `TestHistoryReconciliation::test_fetch_history_emits_delivery_receipt_for_ack2`
- `TestHistoryReconciliation::test_fetch_history_no_receipt_for_server_ack`
- `TestProcessReceiptPersistsById::test_process_receipt_persists_read_by_msg_id`
- `TestProcessReceiptPersistsById::test_process_receipt_never_downgrades_read`
- `TestMergeUpdatesStatus::test_merge_window_rows_updates_status_higher_rank`
- `TestMergeUpdatesStatus::test_on_backend_ready_updates_existing_entry_status`
- `TestSendPropagatesIdToUi::test_whatsapp_send_result_updates_ui_cache_and_widget_id`
- `TestSendPropagatesIdToUi::test_echo_does_not_rewrite_timestamp_when_id_already_present`

### `tests/test_whatsapp_receipt_id_match.py` (20 tests)

Imported: `protocols.db`, `protocols.whatsapp`, `protocols.whatsapp_events`
- `TestCanonicalMsgId::test_dm_serialized`
- `TestCanonicalMsgId::test_group_serialized_with_participant`
- `TestCanonicalMsgId::test_s_whatsapp_net_jid`
- `TestCanonicalMsgId::test_plain_hex_uppercased`
- `TestCanonicalMsgId::test_plain_hex_unchanged_when_already_upper`
- `TestCanonicalMsgId::test_flat_opaque_ids_kept`
- `TestCanonicalMsgId::test_unknown_serialized_shape_kept`
- `TestCanonicalMsgId::test_empty_and_none`
- `TestCanonicalMsgId::test_dm_and_hex_are_equivalent`
- `TestCanonicalMsgId::test_group_and_hex_are_equivalent`
- `TestProcessReceiptCanonical::test_dm_serialized_cache_matches_hex_receipt`
- `TestProcessReceiptCanonical::test_group_serialized_cache_matches_hex_receipt`
- `TestProcessReceiptCanonical::test_receipt_carries_serialized_id_also_matches`
- `TestProcessReceiptCanonical::test_rank_guard_still_respected_with_canonical_match`
- `TestProcessReceiptIdlessFallback::test_single_idless_sent_entry_is_upgraded`
- `TestProcessReceiptIdlessFallback::test_multiple_idless_sent_entries_not_matched`
- `TestProcessReceiptIdlessFallback::test_idless_non_sent_entry_not_matched`
- `TestProcessReceiptIdlessFallback::test_no_match_logs_diagnostic_warning`
- `TestEventFromAckCanonical::test_ack_serialized_id_is_canonicalized`
- `TestEventFromAckCanonical::test_ack_flat_id_kept`

### `tests/test_whatsapp_reconcile_ghost.py` (14 tests)

Imported: `protocols`, `protocols.db`, `protocols.whatsapp`
- `test_reconcile_different_id_keeps_original_row`
- `test_reconcile_exact_ids_keeps_repeated_real_sends`
- `test_reconcile_ambiguous_text_skips_unknown_id`
- `test_reconcile_text_ignores_media_with_same_caption`
- `test_default_ingest_keeps_distinct_ids`
- `test_whitespace_only_edit_is_noop_and_rollback_clears_edited`
- `test_boot_purge_removes_lower_rank_but_keeps_equal_rank`
- `test_boot_detection_never_deletes_legitimate_divergent_rank_pair`
- `test_boot_purge_does_not_touch_telegram_pair`
- `test_fetch_history_reconciles_remote_id_without_ghost`
- `test_reconcile_same_text_weeks_apart_is_a_new_message`
- `test_reconcile_old_ambiguous_candidates_do_not_skip`
- `test_reconcile_candidate_present_in_remote_page_is_distinct`
- `test_fetch_history_keeps_repeated_text_sent_from_phone`

### `tests/test_whatsapp_voice_kind.py` (2 tests)

Imported: `protocols.whatsapp_events`
- `test_flat_payload_voice_note`
- `test_flat_payload_plain_audio_stays_audio`

### `tests/upload_harness.py` (0 tests, test helper)

Imported: `server.uploads`, `web.uploads`

## Test functions removed from kept files

The rest of each file still runs. Reason = the error the test hit once the server was gone.

### `tests/contract/test_socket_ops.py` (1 removed)

- `test_hello_lists_every_python_operation` — renamed `test_hello_lists_every_operation` (checks a frozen copy of the Python registry)

### `tests/test_backend_connect.py` (4 removed)

- `TestBackendReadyAutoSelect::test_no_selection_until_all_backends_reported` — used `app.core` (the in-process `ChatCore` with mocked backends)
- `TestBackendReadyMergeDedup::test_ack_echo_without_attachment_id_still_deduped` — used `app.core` (the in-process `ChatCore` with mocked backends)
- `TestBackendReadyMergeDedup::test_same_id_same_attachment_is_deduped` — used `app.core` (the in-process `ChatCore` with mocked backends)
- `TestBackendReadyMergeDedup::test_two_attachments_same_id_both_kept` — used `app.core` (the in-process `ChatCore` with mocked backends)

### `tests/test_chat_view_images.py` (22 removed)

- `TestFinishNativeThumbnail::test_no_renderer_falls_back_to_catimg` — used `app.core` (the in-process `ChatCore` with mocked backends)
- `TestFinishNativeThumbnail::test_unmounted_widget_is_noop` — used `app.core` (the in-process `ChatCore` with mocked backends)
- `TestMediaKindRendering::test_non_image_worker_only_resolves_path_then_opens` — used `app.core` (the in-process `ChatCore` with mocked backends)
- `TestNativePlaceholder::test_build_message_widgets_empty_in_kitty` — used `app.core` (the in-process `ChatCore` with mocked backends)
- `TestNativePlaceholder::test_empty_in_kitty_with_id` — used `app.core` (the in-process `ChatCore` with mocked backends)
- `TestNativePlaceholder::test_render_image_in_chat_empty_in_kitty` — used `app.core` (the in-process `ChatCore` with mocked backends)
- `TestNativePlaceholder::test_text_when_no_attachment_id` — used `app.core` (the in-process `ChatCore` with mocked backends)
- `TestNativePlaceholder::test_text_when_no_renderer` — used `app.core` (the in-process `ChatCore` with mocked backends)
- `TestResolveAttachmentWorkerKitty::test_kitty_branch_prepares_transmits_and_registers` — used `app.core` (the in-process `ChatCore` with mocked backends)
- `TestResolveAttachmentWorkerKitty::test_prepare_failure_falls_back_to_catimg_resolve` — used `app.core` (the in-process `ChatCore` with mocked backends)
- `TestResolveAttachmentWorkerKitty::test_semaphore_limits_concurrency_to_4` — used `app.core` (the in-process `ChatCore` with mocked backends)
- `TestResolveAttachmentWorkerKitty::test_worker_uses_semaphore` — used `app.core` (the in-process `ChatCore` with mocked backends)
- `TestResolveMountedImagePaths::test_noop_when_no_renderer` — used `app.core` (the in-process `ChatCore` with mocked backends)
- `TestResolveMountedImagePaths::test_noop_when_not_kitty` — used `app.core` (the in-process `ChatCore` with mocked backends)
- `TestResolveMountedImagePaths::test_schedules_worker_only_for_eligible_widgets` — used `app.core` (the in-process `ChatCore` with mocked backends)
- `TestScrollAnchor::test_no_scroll_when_user_scrolled_up` — used `app.core` (the in-process `ChatCore` with mocked backends)
- `TestScrollAnchor::test_scrolls_when_at_bottom` — used `app.core` (the in-process `ChatCore` with mocked backends)
- `TestScrollAnchor::test_scrolls_when_shorter_than_viewport` — used `app.core` (the in-process `ChatCore` with mocked backends)
- `TestWindowLoadAnchor::test_all_terminal_branches_decrement` — used `app.core` (the in-process `ChatCore` with mocked backends)
- `TestWindowLoadAnchor::test_live_worker_does_not_touch_counter` — used `app.core` (the in-process `ChatCore` with mocked backends)
- `TestWindowLoadAnchor::test_token_change_ignores_orphan_workers` — used `app.core` (the in-process `ChatCore` with mocked backends)
- `TestWindowLoadAnchor::test_window_load_scrolls_once_at_end` — used `app.core` (the in-process `ChatCore` with mocked backends)

### `tests/test_contact_grouping.py` (8 removed)

- `TestHeaderBadge::test_header_badge_suppressed_after_select_contact` — used `app.core` (the in-process `ChatCore` with mocked backends)
- `TestHeaderHighlight::test_picker_select_into_collapsed_group_no_auto_expand` — used `app.core` (the in-process `ChatCore` with mocked backends)
- `TestHeaderHighlight::test_select_contact_with_collapsed_group_highlights_header` — used `app.core` (the in-process `ChatCore` with mocked backends)
- `TestHeaderHighlight::test_select_contact_with_visible_member_highlights_member` — used `app.core` (the in-process `ChatCore` with mocked backends)
- `TestToggleGroupHighlight::test_collapse_returns_highlight_to_header` — used `app.core` (the in-process `ChatCore` with mocked backends)
- `TestToggleGroupHighlight::test_expand_moves_highlight_to_selected_member` — used `app.core` (the in-process `ChatCore` with mocked backends)
- `TestToggleGroupHighlight::test_go_to_all_then_expand_highlights_open_member` — used `app.core` (the in-process `ChatCore` with mocked backends)
- `TestToggleGroupHighlight::test_toggle_other_group_keeps_selected_member_highlight` — used `app.core` (the in-process `ChatCore` with mocked backends)

### `tests/test_device_link_screen.py` (3 removed)

- `TestDeviceLinkScreenFlows::test_signal_flow_over_real_operations` — used `protocols.rpc` (the Python Signal RPC layer)
- `TestDeviceLinkScreenFlows::test_telegram_2fa_flow_over_real_operations` — drove the TUI through the in-process `ChatAPI` (`client_harness.client_for`)
- `TestDeviceLinkScreenFlows::test_whatsapp_flow_over_real_operations` — drove the TUI through the in-process `ChatAPI` (`client_harness.client_for`)

### `tests/test_launcher_backend_restart.py` (1 removed)

- `TestRunWahaSelection::test_python_server_restarts_waha` — tested the launcher's Python-server / WAHA branch (`MEGACHAT_SERVER=python`)

### `tests/test_launcher_handover_more.py` (2 removed)

- `TestToLocal::test_falls_back_to_no_wait_start_when_waha_start_fails` — tested the launcher's Python-server / WAHA branch (`MEGACHAT_SERVER=python`)
- `TestToLocal::test_python_server_starts_waha_first` — tested the launcher's Python-server / WAHA branch (`MEGACHAT_SERVER=python`)

### `tests/test_launcher_server_more.py` (3 removed)

- `TestStartAll::test_python_fallback_spawns_the_old_server` — tested the launcher's Python-server / WAHA branch (`MEGACHAT_SERVER=python`)
- `TestStartAll::test_waha_session_reported_with_python_server_and_flag` — tested the launcher's Python-server / WAHA branch (`MEGACHAT_SERVER=python`)
- `TestStatus::test_reports_all_off` — tested the launcher's Python-server / WAHA branch (`MEGACHAT_SERVER=python`)

### `tests/test_launcher_server_options.py` (1 removed)

- `TestStartWithOptions::test_waha_flag_starts_waha_with_the_python_server` — tested the launcher's Python-server / WAHA branch (`MEGACHAT_SERVER=python`)

### `tests/test_launcher_serverbin.py` (4 removed)

- `TestServeCommand::test_missing_binary_raises_with_italian_hint` — tested the launcher's Python-server / WAHA branch (`MEGACHAT_SERVER=python`)
- `TestServeCommand::test_python_default_interpreter_is_the_repo_venv` — tested the launcher's Python-server / WAHA branch (`MEGACHAT_SERVER=python`)
- `TestServeCommand::test_python_escape_hatch_from_dotenv` — tested the launcher's Python-server / WAHA branch (`MEGACHAT_SERVER=python`)
- `TestServeCommand::test_python_escape_hatch_from_environment` — tested the launcher's Python-server / WAHA branch (`MEGACHAT_SERVER=python`)

### `tests/test_megachat_entry.py` (4 removed)

- `test_in_process_tui_refuses_when_lock_is_held` — tested the removed `--in-process` mode (TUI owning the core and the lock)
- `test_python_m_server_dispatches_to_serve` — tested the Python server entry points (`megachat serve` with `MEGACHAT_SERVER=python`, `python -m server`)
- `test_python_m_server_rejects_bad_port` — tested the Python server entry points (`megachat serve` with `MEGACHAT_SERVER=python`, `python -m server`)
- `test_serve_dispatches_to_server_main` — tested the Python server entry points (`megachat serve` with `MEGACHAT_SERVER=python`, `python -m server`)

### `tests/test_quote_widget.py` (5 removed)

- `TestQuoteThumbnailFlow::test_attachment_id_unresolvable_no_thumbnail` — used `app.core` (the in-process `ChatCore` with mocked backends)
- `TestQuoteThumbnailFlow::test_generates_thumbnail_from_attachment_id` — used `app.core` (the in-process `ChatCore` with mocked backends)
- `TestQuoteThumbnailFlow::test_stale_path_and_lazy_fails_text_only` — used `app.core` (the in-process `ChatCore` with mocked backends)
- `TestQuoteThumbnailFlow::test_stale_path_falls_back_to_lazy_resolve` — used `app.core` (the in-process `ChatCore` with mocked backends)
- `TestQuoteThumbnailFlow::test_uscita_lazy_whatsapp_attachment_id` — used `app.core` (the in-process `ChatCore` with mocked backends)

### `tests/test_refresh_chat.py` (5 removed)

- `TestRenderDedupSameSecond::test_image_messages_mount_from_cache` — used `app.core` (the in-process `ChatCore` with mocked backends)
- `TestRenderDedupSameSecond::test_load_messages_worker_shows_newest_even_if_cache_unsorted` — used `app.core` (the in-process `ChatCore` with mocked backends)
- `TestRenderDedupSameSecond::test_load_more_banner_survives_clear_chat` — used `app.core` (the in-process `ChatCore` with mocked backends)
- `TestRenderDedupSameSecond::test_merge_window_rows_copies_quote_metadata` — used `app.core` (the in-process `ChatCore` with mocked backends)
- `TestRenderDedupSameSecond::test_refresh_chat_keeps_newest_after_echo_ts_change` — used `app.core` (the in-process `ChatCore` with mocked backends)

### `tests/test_signal_tui_startup.py` (4 removed)

- `test_client_modes_use_their_own_log_file` — renamed `test_tui_uses_its_own_log_file` (its `--in-process` half dropped)
- `test_connect_and_in_process_are_exclusive` — tested the removed `--in-process` mode (TUI owning the core and the lock)
- `test_in_process_owns_the_core_and_the_lock` — tested the removed `--in-process` mode (TUI owning the core and the lock)
- `test_in_process_refuses_when_the_lock_is_held` — tested the removed `--in-process` mode (TUI owning the core and the lock)

### `tests/test_unread_filter.py` (7 removed)

- `TestAutoSelectFirstVisible::test_select_first_visible_keeps_pin_and_new_selection_visible` — used `app.core` (the in-process `ChatCore` with mocked backends)
- `TestAutoSelectFirstVisible::test_select_first_visible_not_reentrant` — used `app.core` (the in-process `ChatCore` with mocked backends)
- `TestSelectContactMarksRead::test_mark_read_goes_through_the_client_and_badge_clears_at_once` — used `app.core` (the in-process `ChatCore` with mocked backends)
- `TestSelectContactUnreadOnly::test_ghost_contact_hidden_in_unread_only_but_chat_open` — used `app.core` (the in-process `ChatCore` with mocked backends)
- `TestSelectContactUnreadOnly::test_proto_unread_pin_only_when_protocol_matches` — used `app.core` (the in-process `ChatCore` with mocked backends)
- `TestSelectContactUnreadOnly::test_select_contact_pins_selected_keeps_chat` — used `app.core` (the in-process `ChatCore` with mocked backends)
- `TestSelectContactUnreadOnly::test_select_other_contact_unpins_previous` — used `app.core` (the in-process `ChatCore` with mocked backends)

## Removed in mc-zao.4.7 (Go parity tests)

The Go tests that ran the deleted Python server through a `testdata/*.py` script were removed: with `server/`, `protocols/` and their modules gone they skipped or failed instead of comparing anything. Go-only tests in the same files were kept; the parity half of `TestDedupCases` was removed and its Go assertions kept.

### `megachatd/internal/gmail/sanitize_parity_test.go` (file deleted)

- `TestSanitizeParityWithPython` — ran `testdata/sanitize_parity.py`, which imported the deleted `server.gmail`
- `TestSanitizeParityRandom` — same script, seeded random documents
- script `megachatd/internal/gmail/testdata/sanitize_parity.py` — imported `server.gmail` (nh3 + tinycss2 + css-inline)

### `megachatd/internal/messages/parity_test.go` (file deleted)

- `TestParityMessagesPage` — ran `testdata/parity.py`, which imported the deleted `server.messages`, `protocols.db` and `protocols.rpc`
- `TestParityMessagesPageEagerMedia` — same script with lazy WhatsApp media off
- `TestLIDPlaceholderAllowlist` — exercised only the allowlist that rewrote the Python payload inside that comparison
- script `megachatd/internal/messages/testdata/parity.py` — imported `protocols.db`, `protocols.rpc`, `server.messages`
- `megachatd/internal/messages/cache_test.go::TestParityWindowSnapshot` — ran the same script for `messages.window` / `messages.snapshot`

### `megachatd/internal/store` parity tests

Every one ran `testdata/pydb.py` (`protocols.db`, and `models.py` predicates).

- `compat_test.go::TestPythonWrittenDatabaseOpensInGo` (the file was deleted)
- `compat_test.go::TestGoWrittenDatabaseOpensInPython`
- `status_parity_test.go::TestStatusUpdatesMatchPython` (the file was deleted)
- `status_parity_test.go::TestMessageIDUpdateMatchesPython`
- `status_parity_test.go::TestMarkAsReadMatchesPython`
- `status_parity_test.go::TestDedupExactMatchesPython`
- `status_parity_test.go::TestAttachmentUpdatesMatchPython`
- `status_parity_test.go::TestUpdateTextMatchesPython`
- `models_test.go::TestPredicatesMatchPython`
- `prune_test.go::TestPrunePythonParity`
- `reactions_test.go::TestReactionPythonParity`
- `schema_test.go::TestFreshSchemaMatchesPythonExactly`
- `schema_test.go::TestLegacyMigrationsMatchPython`
- `transcriptions_test.go::TestTranscriptionStoreReadableByPython`
- script `megachatd/internal/store/testdata/pydb.py` — drove `protocols.db` and `models.py`

Renamed rather than deleted: `store/schema_test.go::TestPragmasMatchPython` → `TestPragmas` (it never ran Python, only checked `journal_mode=wal` and `busy_timeout`); `store/models_test.go` and the rest of `schema_test.go` kept their Go-only tests. `proto/signal/envelope_test.go::TestPlaceholdersMatchPython` → `TestPlaceholdersMatchModels`, because only its `protocols/signal.py` half was dead: the `models.py` half still checks the literals against the TUI's surviving domain module.

Kept: `megachatd/internal/rpc/interop_test.go::TestInteropPythonSocketClient` now runs `client/socket_client.py` + `client/errors.py` (the TUI's client) and passes against the Go server; `megachatd/internal/proto/telegram/markdown_parity_test.go::TestMarkdownParityTelethon` only needs the telethon library and now runs on the repo `.venv` interpreter.

## Removed in mc-zao.4.6 (launcher/installer cleanup)

The launcher, installer, CI, requirements and helper scripts no longer carry any Python-server or
WAHA path (`launcher/whatsapp.py`, `docker-compose*.yml`, `.dockerignore`,
`requirements-web.txt`, `scripts/start_whatsapp.sh`, `scripts/smoke_e2e.sh` and
`scripts/smoke_socket.py` were deleted). Three WAHA-only test files were deleted and the
launcher/installer/handover tests that covered those removed paths were deleted or renamed.
**Totals:** 3 test files deleted outright (56 test functions) plus 68 test functions removed or
renamed from 13 kept files.

- `tests/test_docker_compose_extra_hosts.py::test_compose_file_exists` — read the deleted `docker-compose.yml` (WAHA only)
- `tests/test_docker_compose_extra_hosts.py::test_whatsapp_service_extra_hosts_host_gateway` — covered the deleted WAHA compose service
- `tests/test_docker_compose_extra_hosts.py::test_whatsapp_media_files_do_not_expire` — covered the deleted WAHA compose service
- `tests/test_launcher_whatsapp.py::TestComposeArgs::test_default_layers_resources_overlay` — module `launcher.whatsapp` deleted (WAHA)
- `tests/test_launcher_whatsapp.py::TestComposeArgs::test_no_docker_limits_omits_resources_overlay` — module `launcher.whatsapp` deleted (WAHA)
- `tests/test_launcher_whatsapp.py::TestComposeArgs::test_waha_docker_limits_0_in_dotenv_omits_overlay` — module `launcher.whatsapp` deleted (WAHA)
- `tests/test_launcher_whatsapp.py::TestComposeArgs::test_waha_docker_limits_1_keeps_overlay` — module `launcher.whatsapp` deleted (WAHA)
- `tests/test_launcher_whatsapp.py::TestComposeArgs::test_extra_files_are_layered_last_in_order` — module `launcher.whatsapp` deleted (WAHA)
- `tests/test_launcher_whatsapp.py::TestComposeArgs::test_extra_files_from_dotenv` — module `launcher.whatsapp` deleted (WAHA)
- `tests/test_launcher_whatsapp.py::TestComposeArgs::test_compose_file_flag_appends_after_dotenv` — module `launcher.whatsapp` deleted (WAHA)
- `tests/test_launcher_whatsapp.py::TestComposeArgs::test_stop_uses_the_extra_files` — module `launcher.whatsapp` deleted (WAHA)
- `tests/test_launcher_whatsapp.py::TestWahaEnv::test_creates_secure_waha_credentials` — module `launcher.whatsapp` deleted (WAHA)
- `tests/test_launcher_whatsapp.py::TestWahaEnv::test_preserves_existing_values_and_is_idempotent` — module `launcher.whatsapp` deleted (WAHA)
- `tests/test_launcher_whatsapp.py::TestWahaEnv::test_recalculates_image_for_current_architecture` — module `launcher.whatsapp` deleted (WAHA)
- `tests/test_launcher_whatsapp.py::TestReadWahaApiKey::test_env_var_wins` — module `launcher.whatsapp` deleted (WAHA)
- `tests/test_launcher_whatsapp.py::TestReadWahaApiKey::test_falls_back_to_dotenv_file` — module `launcher.whatsapp` deleted (WAHA)
- `tests/test_launcher_whatsapp.py::TestReadWahaApiKey::test_strips_quotes` — module `launcher.whatsapp` deleted (WAHA)
- `tests/test_launcher_whatsapp.py::TestReadWahaApiKey::test_missing_returns_empty` — module `launcher.whatsapp` deleted (WAHA)
- `tests/test_launcher_whatsapp.py::TestSetup::test_check_only_does_not_create_env` — module `launcher.whatsapp` deleted (WAHA)
- `tests/test_launcher_whatsapp.py::TestSetup::test_start_probes_sessions_endpoint_with_api_key` — module `launcher.whatsapp` deleted (WAHA)
- `tests/test_launcher_whatsapp.py::TestSetup::test_docker_missing_fails_cleanly` — module `launcher.whatsapp` deleted (WAHA)
- `tests/test_launcher_whatsapp.py::TestSetup::test_start_default_layers_resources_overlay` — module `launcher.whatsapp` deleted (WAHA)
- `tests/test_launcher_whatsapp.py::TestSetup::test_start_no_docker_limits_skips_resources_overlay` — module `launcher.whatsapp` deleted (WAHA)
- `tests/test_launcher_whatsapp.py::TestStandaloneStartStop::test_start_no_wait_skips_readiness_probe` — module `launcher.whatsapp` deleted (WAHA)
- `tests/test_launcher_whatsapp.py::TestStandaloneStartStop::test_start_default_layers_resources_overlay` — module `launcher.whatsapp` deleted (WAHA)
- `tests/test_launcher_whatsapp.py::TestStandaloneStartStop::test_start_no_docker_limits_skips_resources_overlay` — module `launcher.whatsapp` deleted (WAHA)
- `tests/test_launcher_whatsapp.py::TestStandaloneStartStop::test_start_waits_and_reports_timeout` — module `launcher.whatsapp` deleted (WAHA)
- `tests/test_launcher_whatsapp.py::TestStandaloneStartStop::test_stop_runs_compose_down` — module `launcher.whatsapp` deleted (WAHA)
- `tests/test_launcher_whatsapp_more.py::TestListeningPortOwner::test_none_when_ss_missing` — module `launcher.whatsapp` deleted (WAHA)
- `tests/test_launcher_whatsapp_more.py::TestListeningPortOwner::test_parses_pid_and_process_name` — module `launcher.whatsapp` deleted (WAHA)
- `tests/test_launcher_whatsapp_more.py::TestListeningPortOwner::test_none_when_port_not_found` — module `launcher.whatsapp` deleted (WAHA)
- `tests/test_launcher_whatsapp_more.py::TestListeningPortOwner::test_none_on_subprocess_error` — module `launcher.whatsapp` deleted (WAHA)
- `tests/test_launcher_whatsapp_more.py::TestCheckPort::test_available_when_nothing_listens` — module `launcher.whatsapp` deleted (WAHA)
- `tests/test_launcher_whatsapp_more.py::TestCheckPort::test_webhook_port_owned_by_python_is_ok` — module `launcher.whatsapp` deleted (WAHA)
- `tests/test_launcher_whatsapp_more.py::TestCheckPort::test_in_use_by_other_process_warns_false` — module `launcher.whatsapp` deleted (WAHA)
- `tests/test_launcher_whatsapp_more.py::TestCheckPort::test_in_use_with_unknown_owner_warns_false` — module `launcher.whatsapp` deleted (WAHA)
- `tests/test_launcher_whatsapp_more.py::TestCheckFirewall::test_no_firewall_tools_present` — module `launcher.whatsapp` deleted (WAHA)
- `tests/test_launcher_whatsapp_more.py::TestCheckFirewall::test_ufw_active_and_port_allowed` — module `launcher.whatsapp` deleted (WAHA)
- `tests/test_launcher_whatsapp_more.py::TestCheckFirewall::test_ufw_active_and_port_not_allowed` — module `launcher.whatsapp` deleted (WAHA)
- `tests/test_launcher_whatsapp_more.py::TestCheckFirewall::test_iptables_allows_port` — module `launcher.whatsapp` deleted (WAHA)
- `tests/test_launcher_whatsapp_more.py::TestCheckFirewall::test_iptables_drop_policy_warns` — module `launcher.whatsapp` deleted (WAHA)
- `tests/test_launcher_whatsapp_more.py::TestSetupFailureBranches::test_docker_compose_missing_returns_false` — module `launcher.whatsapp` deleted (WAHA)
- `tests/test_launcher_whatsapp_more.py::TestSetupFailureBranches::test_start_up_failure_returns_false` — module `launcher.whatsapp` deleted (WAHA)
- `tests/test_launcher_whatsapp_more.py::TestSetupFailureBranches::test_start_not_ready_after_timeout_still_returns_true` — module `launcher.whatsapp` deleted (WAHA)
- `tests/test_launcher_whatsapp_more.py::TestStandaloneStartFailureBranches::test_docker_missing_returns_1` — module `launcher.whatsapp` deleted (WAHA)
- `tests/test_launcher_whatsapp_more.py::TestStandaloneStartFailureBranches::test_up_failure_returns_its_code` — module `launcher.whatsapp` deleted (WAHA)
- `tests/test_launcher_whatsapp_more.py::TestStandaloneStartFailureBranches::test_waits_and_reports_ready` — module `launcher.whatsapp` deleted (WAHA)
- `tests/test_launcher_whatsapp_more.py::TestStop::test_prints_on_success` — module `launcher.whatsapp` deleted (WAHA)
- `tests/test_launcher_whatsapp_more.py::TestDockerAvailable::test_true_when_info_succeeds` — module `launcher.whatsapp` deleted (WAHA)
- `tests/test_launcher_whatsapp_more.py::TestDockerAvailable::test_false_when_info_fails` — module `launcher.whatsapp` deleted (WAHA)
- `tests/test_launcher_whatsapp_more.py::TestDockerAvailable::test_false_when_docker_is_missing` — module `launcher.whatsapp` deleted (WAHA)
- `tests/test_launcher_whatsapp_more.py::TestDockerAvailable::test_false_on_timeout` — module `launcher.whatsapp` deleted (WAHA)
- `tests/test_launcher_whatsapp_more.py::TestStartRequiresDockerDaemon::test_suggests_colima_and_skips_compose` — module `launcher.whatsapp` deleted (WAHA)
- `tests/test_launcher_whatsapp_more.py::TestSessionStatus::test_reads_status_from_json` — module `launcher.whatsapp` deleted (WAHA)
- `tests/test_launcher_whatsapp_more.py::TestSessionStatus::test_none_on_transport_error` — module `launcher.whatsapp` deleted (WAHA)
- `tests/test_launcher_whatsapp_more.py::TestSessionStatus::test_none_on_http_error` — module `launcher.whatsapp` deleted (WAHA)
- `tests/test_launcher_cli.py::test_whatsapp_requires_subcommand` — the `whatsapp` subcommand was removed
- `tests/test_launcher_main.py::TestBuildParser::test_parses_whatsapp_start_with_no_docker_limits` — the `whatsapp` subcommand and `--no-docker-limits` were removed
- `tests/test_launcher_main.py::TestMainDispatch::test_whatsapp_start_dispatches_with_docker_limits` — the `whatsapp` subcommand was removed
- `tests/test_launcher_main.py::TestMainDispatch::test_whatsapp_start_no_docker_limits_flag` — the `whatsapp` subcommand and `--no-docker-limits` were removed
- `tests/test_launcher_main.py::TestMainDispatch::test_whatsapp_stop_dispatches` — the `whatsapp` subcommand was removed
- `tests/test_launcher_main.py::TestMainDispatch::test_server_start_waha_flag` — `server start --waha/--no-whatsapp` were removed
- `tests/test_launcher_main.py::TestMainDispatch::test_compose_file_flag_reaches_compose_args` — `--compose-file` and `launcher.whatsapp` were removed
- `tests/test_launcher_misc.py::TestServerHelpers::test_docker_container_running_false_without_docker` — `server._docker_container_running` was removed (WAHA)
- `tests/test_launcher_aliases.py::TestNoObsoleteKeepWhatsappFlag::test_parser_still_accepts_the_flag` — `server stop --keep-whatsapp` was removed
- `tests/test_launcher_backend_restart.py::TestRestartWaha::test_dies_without_docker` — `backend._restart_waha` was removed
- `tests/test_launcher_backend_restart.py::TestRestartWaha::test_dies_without_compose_plugin` — `backend._restart_waha` was removed
- `tests/test_launcher_backend_restart.py::TestRestartWaha::test_dies_when_compose_file_missing` — `backend._restart_waha` was removed
- `tests/test_launcher_backend_restart.py::TestRestartWaha::test_dies_when_service_not_defined` — `backend._restart_waha` was removed
- `tests/test_launcher_backend_restart.py::TestRestartWaha::test_restarts_existing_service_and_waits_for_api` — `backend._restart_waha` was removed
- `tests/test_launcher_backend_restart.py::TestRestartWaha::test_starts_up_when_service_does_not_exist_yet` — `backend._restart_waha` was removed
- `tests/test_launcher_backend_restart.py::TestRestartWaha::test_dies_when_up_fails` — `backend._restart_waha` was removed
- `tests/test_launcher_backend_restart.py::TestRestartWaha::test_dies_on_api_wait_timeout` — `backend._restart_waha` was removed
- `tests/test_launcher_backend_restart.py::TestRun::test_wires_timeouts_and_docker_limits` — renamed `test_wires_signal_timeout` (WAHA options removed)
- `tests/test_launcher_backend_restart.py::TestRun::test_reads_custom_timeouts_from_env` — renamed `test_reads_custom_timeout_from_env` (WAHA timeout removed)
- `tests/test_launcher_backend_restart.py::TestRunWahaSelection::test_megachatd_skips_waha_by_default` — WAHA selection removed
- `tests/test_launcher_server_more.py::TestDockerContainerRunning::test_true_when_name_listed` — `server._docker_container_running` was removed (WAHA)
- `tests/test_launcher_server_more.py::TestDockerContainerRunning::test_false_when_name_absent` — `server._docker_container_running` was removed (WAHA)
- `tests/test_launcher_server_more.py::TestStatus::test_reports_session_state_when_waha_is_active` — WAHA status lines were removed
- `tests/test_launcher_server_more.py::TestStopAll::test_reports_waha_stopped` — `server stop --waha` was removed
- `tests/test_launcher_server_more.py::TestStopAll::test_reports_waha_was_not_running` — `server stop --waha` was removed
- `tests/test_launcher_server_more.py::TestStartAllStuckServer::test_stop_all_keeps_waha_when_server_will_not_stop` — renamed `test_stop_all_raises_when_server_will_not_stop` (WAHA removed)
- `tests/test_launcher_server_options.py::TestStartWithOptions::test_waha_is_not_started_by_default` — WAHA start branch removed
- `tests/test_launcher_server_options.py::TestStartWithOptions::test_waha_flag_is_ignored_with_megachatd` — `server start --waha` removed
- `tests/test_launcher_server_options.py::TestStopWaha::test_default_leaves_waha_alone` — `launcher.whatsapp` removed
- `tests/test_launcher_server_options.py::TestStopWaha::test_waha_flag_brings_waha_down` — `launcher.whatsapp` removed
- `tests/test_launcher_serverbin.py::TestServeCommand::test_environment_beats_dotenv` — `serverbin.use_python`/`engine_name` removed
- `tests/test_launcher_serverbin.py::TestServeCommand::test_default_is_megachatd` — `serverbin.use_python` removed
- `tests/test_launcher_install.py::TestInstallPythonDeps::test_no_web_flag_skips_web_deps` — `requirements-web.txt` and `--no-web` removed
- `tests/test_launcher_install.py::TestInstallPythonDeps::test_web_deps_failure_is_soft` — `requirements-web.txt` removed
- `tests/test_launcher_install_run.py::TestWhatsAppFlags::test_check_whatsapp_calls_setup_without_starting` — `--check-waha` removed
- `tests/test_launcher_install_run.py::TestWhatsAppFlags::test_whatsapp_flag_calls_setup_and_starts` — `--waha` removed
- `tests/test_launcher_install_run.py::TestWhatsAppFlags::test_neither_flag_skips_whatsapp_setup` — WAHA flags removed
- `tests/test_launcher_install_run.py::TestFinalBanner::test_python_fallback_note_points_to_waha_flag` — the Python-server fallback was removed
- `tests/test_launcher_install_run.py::TestFinalBanner::test_default_install_builds_megachatd_and_never_touches_waha` — renamed `test_default_install_builds_megachatd` (WAHA assertion dropped)
- `tests/test_launcher_build_megachatd.py::test_without_go_warns_in_italian_and_falls_back` — renamed `test_without_go_and_no_binary_dies` (no Python fallback)
- `tests/test_launcher_build_megachatd.py::test_too_old_go_falls_back_when_toolchain_download_is_off` — renamed `test_too_old_go_without_toolchain_download_dies`
- `tests/test_launcher_build_megachatd.py::test_go_before_toolchain_support_falls_back` — renamed `test_go_before_toolchain_support_dies`
- `tests/test_launcher_build_megachatd.py::test_failed_build_falls_back` — renamed `test_failed_build_dies`
- `tests/test_launcher_build_megachatd.py::test_successful_build_removes_only_the_fallback_it_wrote` — the Python fallback was removed
- `tests/test_launcher_build_megachatd.py::test_users_own_setting_is_never_touched` — the Python fallback was removed
- `tests/test_launcher_build_megachatd.py::test_missing_sources_skip_quietly` — missing sources now use a release binary or fail
- `tests/test_install_script.py::TestWahaEnv::test_creates_secure_waha_credentials` — `ensure_waha_env` removed from install.sh
- `tests/test_install_script.py::TestWahaEnv::test_preserves_existing_values_and_is_idempotent` — `ensure_waha_env` removed from install.sh
- `tests/test_install_script.py::TestWahaEnv::test_recalculates_image_for_current_architecture` — `ensure_waha_env` removed from install.sh
- `tests/test_install_script.py::TestWahaEnv::test_check_only_does_not_create_env` — `setup_whatsapp` removed from install.sh
- `tests/test_install_script.py::TestWahaEnv::test_start_uses_api_key_for_readiness_probe` — `setup_whatsapp` removed from install.sh
- `tests/test_install_script.py::TestWahaEnv::test_start_no_docker_limits_skips_resources_overlay` — `setup_whatsapp` removed from install.sh
- `tests/test_install_script.py::TestPortCheck::test_lsof_reports_free_port` — `check_port` removed from install.sh (WAHA only)
- `tests/test_install_script.py::TestPortCheck::test_lsof_reports_occupied_port` — `check_port` removed from install.sh (WAHA only)
- `tests/test_install_script.py::TestPortCheck::test_lsof_recognizes_macos_python_webhook` — `check_port` removed from install.sh (WAHA only)
- `tests/test_install_script.py::TestWebDependencies::test_web_deps_installed_by_default` — `requirements-web.txt` removed
- `tests/test_install_script.py::TestWebDependencies::test_no_web_flag_skips_web_deps` — `requirements-web.txt` and `--no-web` removed
- `tests/test_install_script.py::TestWebDependencies::test_web_deps_failure_is_soft` — `requirements-web.txt` removed
- `tests/test_install_script.py::TestWebDependencies::test_help_lists_no_web` — `--no-web` removed
- `tests/test_install_script.py::TestBuildMegachatd::test_missing_go_warns_in_italian_and_sets_python_fallback` — renamed `test_missing_go_without_binary_dies`
- `tests/test_install_script.py::TestBuildMegachatd::test_old_go_without_toolchain_download_falls_back` — renamed `test_old_go_without_toolchain_download_dies`
- `tests/test_install_script.py::TestBuildMegachatd::test_failed_build_falls_back` — renamed `test_failed_build_dies`
- `tests/test_install_script.py::TestBuildMegachatd::test_build_removes_the_fallback_it_wrote_and_keeps_the_rest` — the Python fallback was removed
- `tests/test_install_script.py::TestBuildMegachatd::test_users_own_python_choice_is_kept` — `MEGACHAT_SERVER=python` is gone
- `tests/test_install_script.py::TestWahaIsOptIn::test_help_lists_waha_flags_not_a_default_docker_step` — `--waha`/`--check-waha` removed
- `tests/test_install_script.py::TestWahaIsOptIn::test_default_run_never_touches_docker` — the WAHA checks it guarded were removed
- `tests/test_handover_scripts.py::test_start_whatsapp_explains_native_whatsapp_without_waha_flag` — `scripts/start_whatsapp.sh` deleted
- `tests/test_handover_scripts.py::test_start_whatsapp_stop_needs_waha_flag` — `scripts/start_whatsapp.sh` deleted
