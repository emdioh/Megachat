# Analisi di copertura funzionale

Confronto tra aree funzionali e test presenti, sullo stato attuale (dopo l'eliminazione del server Python, mc-zao.4.4). La copertura misurata dalla CI resta qualitativa qui; il gate Python è `fail_under = 68` in `pyproject.toml`.

Legenda: **Robusta** = suite dedicata ampia; **Coperta** = test sui percorsi principali; **Parziale** = casi limite/indiretti; **Lacuna** = nessun test dedicato.

## 1. Server Go (`megachatd`)

| Area | Stato | Evidenze |
|---|---|---|
| Store SQLite: schema, migrazioni, dedup, pruning, reazioni, trascrizioni | Robusta | `internal/store/*_test.go` (`schema_test`, `dedup_test`, `messages_test`, `prune_test`, `reactions_test`, `transcriptions_test`) |
| Ingest e stato dei contatti | Robusta | `internal/core/events_test.go`, `ops_*_test.go`, `helpers_test.go` |
| Invio, edit, reazioni, retry | Robusta | `internal/core/ops_send_test.go`, `ops_edit_reactions_test.go`, `retry.go` coperto via contract |
| Address book e fan-out per protocollo | Coperta | `internal/core/ops_contacts_test.go`, `internal/proto/manager_test.go` |
| Signal: RPC, envelope, ingest, invio, quote | Robusta | `internal/proto/signal/*_test.go` |
| WhatsApp: conversione, ingest, history, ids, media, quote, guardie | Robusta | `internal/proto/whatsapp/*_test.go` |
| Telegram: conversione, sessione Telethon, migrazione id, invio, media | Robusta | `internal/proto/telegram/*_test.go` |
| Gmail: IMAP/SMTP, sanitizer, immagini, watcher | Robusta | `internal/gmail/*_test.go` |
| Media: upload, thumbnail, link preview, avatar | Robusta | `internal/media/*_test.go` |
| Linking QR (stati, scadenze, 2FA) | Robusta | `internal/linking/linking_test.go` |
| Trascrizione e auto-trascrizione | Robusta | `internal/transcribe/*_test.go` |
| Config, migrazione, validazione avvio | Robusta | `internal/config/*_test.go`, `cmd/megachatd/validation_test.go` |
| Adapter socket JSON-RPC | Robusta | `internal/rpc/rpc_test.go`, `internal/rpc/interop_test.go` |
| Adapter HTTP/WebSocket | Robusta | `internal/web/*_test.go` |
| Wiring completo del binario (fake backend) | Coperta | `cmd/megachatd/main_test.go`, `integrations_test.go`, `web_wiring_test.go` |

## 2. Contratto black-box (`tests/contract/`)

| Area | Stato | Evidenze |
|---|---|---|
| Operazioni sul socket, handshake, errori | Robusta | `test_socket_ops.py` (44) |
| Route REST, auth, blob, upload | Robusta | `test_http_routes.py` (21) |
| WebSocket legacy e v2, resume, gap | Robusta | `test_websocket.py` (10) |
| Id dei messaggi inviati / echo | Coperta | `test_send_message_ids.py` (6) |
| Validazione all'avvio | Parziale | `test_startup_validation.py` (1) |

## 3. Client Python e TUI

| Area | Stato | Evidenze |
|---|---|---|
| Regole di import e isolamento del client | Robusta | `test_client_import_rules.py` |
| Attach/find-or-spawn, entry point, errori di connessione | Robusta | `test_signal_tui_startup.py`, `test_megachat_entry.py`, `test_signal_tui_cli.py` |
| Merge a backend pronto (snapshot + contatti) | Coperta | `test_backend_connect.py`, `test_backend_ready_prefetch.py` |
| Invio ottimistico / edit visti lato client | Coperta | `test_tui_send_ops.py`, `test_tui_edit_ops.py` |
| Dispatch eventi e aggiornamento vista | Parziale | coperto indirettamente da `test_refresh_chat.py`, `test_tui_integration.py`; i vecchi test dedicati sono in `DELETED_TESTS.md` (mc-gux) |
| Lista contatti, grouping, filtri, unread | Robusta | `test_contact_grouping*.py`, `test_unread_filter.py`, `test_status_backend_unread.py` |
| Picker, device link, emoji | Robusta | `test_contact_picker.py`, `test_device_link_screen.py`, `test_emoji_picker.py` |
| Chat view: finestra, refresh, quote | Robusta | `test_refresh_chat.py`, `test_quote_widget.py`, `test_reply_media.py` |
| Widget di basso livello | Robusta | `test_ui_components.py` |
| Immagini native kitty | Robusta | `test_kitty_renderer.py`, `test_image_modal.py`, `test_chat_view_images.py`, `test_native_*` |
| SPA web (JS letto dai test Python) | Coperta | `test_web_*.py` |

## 4. Installazione e operatività

| Area | Stato | Evidenze |
|---|---|---|
| `install.sh` | Robusta | `test_install_script.py` (33) |
| Launcher Python (alternativa agli script) | Robusta | `test_launcher_*.py` (197) |
| Servizio systemd/OpenRC | Robusta | `test_service_script.py` (19) |
| Handover locale ↔ server | Coperta | `test_handover_scripts.py` |
| Setup Gmail | Coperta | `test_gmail_setup.py`, `test_gmail_refresh_wiring.py` |

## 5. Lacune principali

1. **Flussi TUI sul client** — invio/edit/eventi/lista/chat view hanno perso i test dedicati col core in-process; vanno ricostruiti su uno stub `ChatClient` (mc-gux).
2. **Riconnessione reale del `SocketClient`** — backoff, replay `since`, `gap` e `client_connection` sono coperti dai test Go del bus/adapter e dal contract, ma pochi test Python esercitano un server che cade e ritorna.
3. **Resa su terminale reale** — il rendering kitty è testato a livello di protocollo; la verifica visiva resta manuale.
4. **Test live** — eliminati con il server Python; non esistono test che parlano con account reali.
5. **Carico** — liste molto grandi, render progressivo e concorrenza di molti client non hanno test dedicati.
6. **Gmail reale** — IMAP/SMTP è testato con un fake; i casi di rete reale (timeout, quota) restano scoperti.

## 6. Metriche

- Python unit/UI: **~921** casi raccolti in 64 file (987 con `pytest --collect-only` in questo worktree, SPA inclusa).
- Contratto: **226** casi in 5 file.
- Go: suite per package (`go test ./...`) più una run finale con `-race`.
- Gate: coverage Python `fail_under = 68`; nessun gate di coverage Go configurato.
