# Test suite — panoramica e mappa

Stato attuale del repository. Il server Python e i suoi test sono stati eliminati il 2026-11-08 (mc-zao.4.4): l'elenco completo dei file e delle funzioni rimosse è in [DELETED_TESTS.md](DELETED_TESTS.md). La ricostruzione dei flussi TUI che giravano sul core in-process è tracciata dal bead **mc-gux**.

**Conteggi attuali** (casi raccolti, parametrizzazioni incluse): **~921 test unit/UI Python** in `tests/` (987 con `pytest --collect-only` in questo worktree, 98 dei quali test Python della SPA) e **226 test di contratto** in `tests/contract/` (black-box contro il binario `megachatd`). Il server ha inoltre la propria suite Go (`go test ./...`) e i test con `-race`.

## 1. Come eseguire i test

### Configurazione (`pyproject.toml`)

- `testpaths = ["tests"]` — `pytest` parte dalla root del repo.
- `pythonpath = ["."]`.
- `addopts = "-ra --tb=short --strict-markers --basetemp=/tmp/t -m \"not smoke\""` — la smoke test della TUI è esclusa dal run default.
- Marker registrati: `integration` (TUI headless, più lento), `live`/`live-manual` (storici; oggi non ci sono test live), `allow_unserializable_events`, `smoke`.
- `asyncio_mode = "auto"`.
- Coverage: `source = ["."]`, branch on, gate `fail_under = 68`.

### Comandi

```bash
# sola suite unit/UI (nessun server, nessun demone)
.venv/bin/python -m pytest --ignore=tests/contract

# intera suite Python definita da testpaths: include anche tests/contract
make test PYTHON=.venv-test/bin/python     # oppure: source .venv-test/bin/activate && make test
make lint                                  # ruff check
make format-check                          # ruff format --check
make typecheck                             # basedpyright
make coverage                              # pytest --cov + coverage.xml

# sola suite di contratto (richiede Go; costruisce il binario con -tags contracttest)
make contract-test

# suite Go del server
cd megachatd && go vet ./... && go test ./...
cd megachatd && go test -race ./...
```

La smoke test headless (`tests/test_smoke.py`) è esclusa dal default:

```bash
python -m pytest tests/test_smoke.py -m smoke
```

### CI (`.github/workflows/ci.yml`)

- job `lint`: `ruff check` + `ruff format --check`;
- job `typecheck`: `basedpyright`;
- job `test`: matrice Python **3.12/3.13/3.14/3.15**; la gamba 3.13 esegue `make coverage` con gate 68 e carica `coverage.xml` su Codecov, le altre eseguono `make test`;
- trigger: push su `master` e pull request; `cancel-in-progress` sullo stesso ref.

`make contract-test` e i test Go non fanno parte di questa workflow: vanno eseguiti esplicitamente (e sono il gate delle modifiche a `megachatd/`).

## 2. Mappa dei test Python (`tests/`)

I numeri indicano le funzioni `test_*` definite nel file (prima delle parametrizzazioni).

### 2.1 Client, trasporto e avvio della TUI

| File | N. | Cosa copre |
|---|---|---|
| `tests/test_client_import_rules.py` | 3 | `client/` non importa Textual/`tui/`; dipendenze del trasporto |
| `tests/test_backend_connect.py` | 3 | merge a backend pronto: `messages.snapshot` + `contacts.for_protocol`, dedup per id+attachment |
| `tests/test_backend_ready_prefetch.py` | 3 | prefetch delle anteprime quando il backend diventa pronto |
| `tests/test_signal_tui_cli.py` | 3 | parsing dei flag della TUI (`--connect`, `--web-*`) |
| `tests/test_signal_tui_startup.py` | 10 | avvio/attach, spawn del server, errori di connessione |
| `tests/test_megachat_entry.py` | 7 | entry point `megachat serve` / TUI / exec di `megachatd` |
| `tests/test_tui_integration.py` | 18 | app headless con client stub |
| `tests/test_tui_no_backend_access.py` | 2 | la TUI non tocca backend/servizi esterni |
| `tests/test_tui_send_ops.py` | 11 | invio ottimistico via `messages.send` (esiti, timeout, rollback) |
| `tests/test_tui_edit_ops.py` | 3 | edit via `messages.edit` (rollback, esito ignoto) |
| `tests/test_tui_sigint_loop.py` | 2 | SIGINT/loop di polling |
| `tests/test_smoke.py` | 1 | boot + render + exit headless (marker `smoke`) |

### 2.2 Contatti, lista e filtri

| File | N. | Cosa copre |
|---|---|---|
| `tests/test_contact_grouping.py` | 58 | grouping per persona, header/member, collapse |
| `tests/test_contact_grouping_integration.py` | 5 | grouping integrato nella lista reale |
| `tests/test_contact_picker.py` | 49 | picker Ctrl+S, ricerca, `BackendChoiceScreen` |
| `tests/test_unread_filter.py` | 59 | filtro Ctrl+U/Ctrl+W/A, pinning, auto-selezione |
| `tests/test_status_backend_unread.py` | 14 | status bar per-backend clickabile |
| `tests/test_refresh_chat.py` | 31 | finestra 20 messaggi, dedup stessa-secondo, merge |

### 2.3 Chat, messaggi e media

| File | N. | Cosa copre |
|---|---|---|
| `tests/test_models.py` | 22 | modelli condivisi, placeholder quote media, predicati |
| `tests/test_quote_widget.py` | 34 | rendering delle quote, modalità display/filo |
| `tests/test_reply_media.py` | 8 | reply a un'immagine: cattura target, reply bar, mutua esclusione con edit |
| `tests/test_ui_components.py` | 39 | widget custom (`MessageWidget`, `MessageTextArea`, status) |
| `tests/test_pick_survivor_and_merge.py` | 16 | scelta della riga sopravvissuta e merge add-only |
| `tests/test_chat_view_images.py` | 9 | ramo nativo delle immagini in chat |
| `tests/test_image_modal.py` | 11 | `ImageModalScreen`, strategia nativa e OFF |
| `tests/test_kitty_renderer.py` | 52 | protocollo kitty: DCS, chunk, source rect, cell size |
| `tests/test_native_architect_fix.py` / `test_native_radical_fix.py` / `test_native_shutdown_safety.py` | 5 / 5 / 3 | regressioni del rendering nativo e dello shutdown |

### 2.4 Picker e device link

| File | N. | Cosa copre |
|---|---|---|
| `tests/test_device_link_screen.py` | 68 | flusso Ctrl+L QR (Signal/WhatsApp/Telegram, 2FA) |
| `tests/test_emoji_picker.py` | 23 | picker emoji, categorie, completamento alias |
| `tests/test_qr_ascii.py` | 4 | rendering QR ASCII (`qr_utils.py`) |
| `tests/test_link_whatsapp.py` | 6 | script di linking WhatsApp |

### 2.5 SPA web (test Python sul JavaScript)

| File | N. | Cosa copre |
|---|---|---|
| `tests/test_web_account_link_signal.py` / `test_web_account_link_ui.py` | 6 / 19 | flusso di linking dal web |
| `tests/test_web_multi_attachment_ui.py` | 14 | allegati multipli nella SPA |
| `tests/test_web_reactions_ui.py` / `test_web_status_edit.py` | 14 / 4 | reazioni e stato/edit nella UI |
| `tests/test_web_voice_record_ui.py` | 16 | registrazione vocale |
| `tests/test_web_message_cache.py`, `test_web_reconcile_server_id.py`, `test_web_relative_urls.py`, `test_web_linkify.py`, `test_web_contact_search_dedup.py` | 4 / 4 / 3 / 2 / 1 | cache, riconciliazione id, URL relativi, linkify, ricerca contatti |

### 2.6 Installazione, launcher e servizio

| File | N. | Cosa copre |
|---|---|---|
| `tests/test_install_script.py` | 33 | `install.sh`: opzioni, download signal-cli, build |
| `tests/test_launcher_*.py` (15 file) | 197 | CLI, install, alias, server start/stop/status, build di megachatd, handover, profilo, test runner |
| `tests/test_handover_scripts.py` | 11 | handover locale ↔ server |
| `tests/test_service_script.py` | 19 | `scripts/megachat-service.sh` (systemd/OpenRC) |
| `tests/test_gmail_setup.py` / `test_gmail_refresh_wiring.py` | 14 / 3 | setup Gmail e refresh del tab |

## 3. Suite di contratto (`tests/contract/`)

Black-box: fanno parte di `tests/` e girano anche con `make test`; il binario `megachatd` viene guidato solo dal filo (socket via `SocketClient`, HTTP, WebSocket), su un database SQLite seminato con lo schema reale. Richiede Go; il binario è costruito con `-tags contracttest` per abilitare l'hook del backend finto usato dai test di invio.

| File | N. | Cosa copre |
|---|---|---|
| `tests/contract/test_socket_ops.py` | 44 | handshake, operazioni sul socket, errori, eventi |
| `tests/contract/test_http_routes.py` | 21 | route REST, auth, blob, upload |
| `tests/contract/test_websocket.py` | 10 | `/ws` e `/ws/v2`, subscribe/resume, auth |
| `tests/contract/test_send_message_ids.py` | 6 | id dei messaggi inviati e echo |
| `tests/contract/test_startup_validation.py` | 1 | validazione delle integrazioni all'avvio |

Supporto: `conftest.py` (build/seeding), `harness.py` (avvio server, `SocketClient`, HTTP), `routes_table.py`, `seed_db.py`.

## 4. Fixture condivise

`tests/conftest.py`:

- autouse `fail_on_unclosed_sqlite`: ogni connessione SQLite aperta nel test deve essere chiusa (altrimenti il test fallisce);
- autouse `isolate_server_socket`, `isolate_instance_lock`, `isolate_environment`: socket, lock e `SIGNAL_TUI_WEB_TOKEN` non toccano mai il sistema reale;
- `sample_messages` e altre fixture di dati per i test di vista.

`tests/client_harness.py`:

- `StubClient`: registra `call` e risponde da una sequenza (valore o eccezione);
- `view_app`: `SignalTUI` con le parti DOM stub-ate e un client iniettato;
- `inline_client_calls`: `_offload`/`_ui`/`_call_op` eseguiti inline per testare i mixin senza thread.

## 5. Test eliminati e da ricostruire

- [DELETED_TESTS.md](DELETED_TESTS.md) elenca **187 file di test** eliminati e le funzioni rimosse da 15 file mantenuti (3685 funzioni in totale). Comprende i test del server Python (`server/`, `protocols/`, `web/*.py`, `transcription/`), i test di parità Go↔Python e i test live.
- I flussi TUI che giravano sul core in-process (invio, edit, eventi, lista contatti, chat view) sono coperti solo in parte: **mc-gux** li ricostruisce su uno stub `ChatClient`.
- I marker `live`/`live-manual` restano registrati per compatibilità, ma oggi non esistono test live nel repository.

## Documenti collegati

- [TEST_COVERAGE.md](TEST_COVERAGE.md) — aree coperte vs lacune.
- [TEST_GUIDELINES.md](TEST_GUIDELINES.md) — convenzioni per scrivere nuovi test.
- [DELETED_TESTS.md](DELETED_TESTS.md) — inventario dei test eliminati.
