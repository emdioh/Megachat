# Documentazione — Megachat

Documentazione tecnica del sistema attuale: server Go `megachatd`, client TUI Python (`client/` + `tui/`) e SPA web servita dal server. I documenti in `docs/` sono note storiche e non fanno testo (vedi [docs/README.md](../docs/README.md)).

Per installazione e uso quotidiano vedere il [README del progetto](../README.md).

## Struttura

```
documentation/
├── README.md                        ← questo indice
├── architecture/
│   ├── ARCHITECTURE_OVERVIEW.md     ← processo, trasporti, flussi, pattern
│   └── BACKEND_COMPONENTS.md        ← package Go interni e package client Python
├── design/
│   ├── DESIGN_OVERVIEW.md           ← principi UI/UX, composizione Textual, data flow
│   ├── DESIGN_MESSAGE_IDENTITY_AND_CACHE.md  ← identità messaggi, cache client, dedup, batching
│   ├── DESIGN_OUTGOING_MESSAGE_STATUS.md     ← pending→sent→delivered→read→failed
│   ├── DESIGN_EDIT_MESSAGES.md      ← editing ottimistico con rollback, edit ricevuti
│   └── DESIGN_NATIVE_IMAGES.md      ← miniature native kitty + modal hi-res, fallback catimg
├── api-contracts/
│   ├── API_OVERVIEW.md              ← superfici: operazioni, socket JSON-RPC, REST, WebSocket, eventi
│   └── CONTRACTS.md                 ← contratti di dati sul filo e nello store SQLite
└── test-suite/
    ├── TEST_OVERVIEW.md             ← mappa e conteggi delle suite attuali + DELETED_TESTS.md
    ├── TEST_COVERAGE.md             ← aree coperte vs lacune
    ├── TEST_GUIDELINES.md           ← convenzioni per scrivere nuovi test
    └── DELETED_TESTS.md             ← elenco dei test eliminati col server Python (4.4)
```

## Indice dei documenti

### Architettura (`architecture/`)

| Documento | Descrizione | Fonti principali |
|---|---|---|
| [ARCHITECTURE_OVERVIEW.md](architecture/ARCHITECTURE_OVERVIEW.md) | Processo server e client, trasporti (Unix socket, HTTP, WebSocket), flussi di messaggio in ingresso/uscita, event bus, persistenza SQLite, thread model e decisioni architetturali. | `megachatd/cmd`, `megachatd/internal/{api,bus,core,ops,rpc,store,proto,web}`, `client/`, `tui/` |
| [BACKEND_COMPONENTS.md](architecture/BACKEND_COMPONENTS.md) | Responsabilità e relazioni dei package Go (`core`, `store`, `proto/*`, `gmail`, `media`, `messages`, `linking`, `transcribe`, `web`, `rpc`) e dei package Python `client/` e `tui/`. | `megachatd/internal/*`, `client/*`, `tui/*` |

### Design (`design/`)

| Documento | Descrizione | Fonti principali |
|---|---|---|
| [DESIGN_OVERVIEW.md](design/DESIGN_OVERVIEW.md) | Principi di design, composizione dell'app per mixin, albero widget, convenzioni CSS, widget custom, data flow eventi/thread, stato della UI. | `tui/app.py`, `tui/css.py`, `tui/events.py`, `ui_components.py`, `models.py` |
| [DESIGN_MESSAGE_IDENTITY_AND_CACHE.md](design/DESIGN_MESSAGE_IDENTITY_AND_CACHE.md) | Identità `(protocol, key, ts, text)`, cache client e cache server, regole di dedup, merge add-only e batching del rendering. | `tui/chat_view.py`, `tui/events.py`, `client/events_api.py`, `megachatd/internal/store`, `megachatd/internal/core` |
| [DESIGN_OUTGOING_MESSAGE_STATUS.md](design/DESIGN_OUTGOING_MESSAGE_STATUS.md) | Macchina a stati dei messaggi inviati (pending/sent/delivered/read/**failed**), invio ottimistico, transizioni, fallback e retry. | `tui/send.py`, `tui/events.py`, `megachatd/internal/core`, `megachatd/internal/store`, `ui_components.py` |
| [DESIGN_EDIT_MESSAGES.md](design/DESIGN_EDIT_MESSAGES.md) | Editing dei messaggi: gate Alt+E/Alt+click, submit ottimistico con rollback, chirurgia dell'identità, update in-place degli edit ricevuti. | `tui/edit.py`, `tui/send.py`, `tui/events.py`, `megachatd/internal/core/ops_edit.go` |
| [DESIGN_NATIVE_IMAGES.md](design/DESIGN_NATIVE_IMAGES.md) | Immagini native: miniature inline via kitty graphics protocol (`tui/images/`), modal hi-res, fallback catimg/OFF. | `tui/images/*`, `tui/app.py`, `ui_components.py::ImageWidget` |

### Code API & Contracts (`api-contracts/`)

| Documento | Descrizione | Fonti principali |
|---|---|---|
| [API_OVERVIEW.md](api-contracts/API_OVERVIEW.md) | Superfici del sistema: registry delle 68 operazioni, trasporto Unix socket JSON-RPC, REST + WebSocket, event bus, `ChatClient`/`SocketClient`, store SQLite, configurazione. | `megachatd/internal/api`, `internal/ops`, `internal/rpc`, `internal/web`, `client/socket_client.py` |
| [CONTRACTS.md](api-contracts/CONTRACTS.md) | Contratti di dati: `ChatContact`/`ChatMessage` sul filo, payload eventi, schema `messages` SQLite, stati, edit, receipt, grouping/unread, quote media. | `models.py`, `client/events_api.py`, `megachatd/internal/store`, `megachatd/internal/messages` |

### Test Suite (`test-suite/`)

| Documento | Descrizione | Fonti principali |
|---|---|---|
| [TEST_OVERVIEW.md](test-suite/TEST_OVERVIEW.md) | Come eseguire i test (Makefile, pytest, `.venv-test`, contratto Go, CI 3.12–3.15), conteggi attuali (~921 unit Python, 226 contract) e collegamento a `DELETED_TESTS.md`. | `pyproject.toml`, `Makefile`, `.github/workflows/ci.yml`, `tests/` |
| [TEST_COVERAGE.md](test-suite/TEST_COVERAGE.md) | Analisi qualitativa di copertura per area e lacune concrete. | `tests/*`, suite Go `megachatd/internal/**/*_test.go` |
| [TEST_GUIDELINES.md](test-suite/TEST_GUIDELINES.md) | Convenzioni per scrivere test: fixture, stub `ChatClient`, naming, pattern ricorrenti. | `tests/conftest.py`, `tests/client_harness.py`, `pyproject.toml` |

## Nota metodologica

- I contenuti sono ricavati dal codice attuale: `megachatd/` (server), `client/` e `tui/` (client Python), `web/static/` (SPA).
- Il server Python (`server/`, `protocols/`, `transcription/`, `web/*.py`) è stato eliminato il 2026-11-08 in mc-zao.4.4; i test che lo importavano sono elencati in [test-suite/DELETED_TESTS.md](test-suite/DELETED_TESTS.md).
- Dove un comportamento è documentato anche nelle note storiche `docs/DESIGN_*.md`, qui vale **solo** ciò che il codice fa oggi.
