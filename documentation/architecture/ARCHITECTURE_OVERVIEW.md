# Architettura — Megachat

Documento ricavato dal codice attuale (`megachatd/`, `client/`, `tui/`, `web/static/`, `models.py`). Descrive cosa il sistema fa oggi. Il server Python (`server/`, `protocols/`, `web/*.py`) è stato eliminato il 2026-11-08 (mc-zao.4.4); le note storiche restano in `docs/`.

## 1. Visione d'insieme

Megachat è un'architettura **client/server** con un unico processo che possiede i protocolli:

- **`megachatd`** (Go): server headless. Apre il database SQLite, registra i backend di protocollo, esegue il *chat core* (ingest, invio, edit, receipt, reazioni, unread) e pubblica eventi su un bus. Espone lo stesso dominio attraverso tre trasporti: Unix socket JSON-RPC 2.0 (TUI), REST `/api/...` più WebSocket `/ws` e `/ws/v2` (SPA), e la SPA statica da `web/static/`.
- **TUI** (Python/Textual, package `tui/`): client del server. Il package `client/` fornisce il trasporto (`SocketClient`), il find-or-spawn (`attach.py`), la sottoscrizione agli eventi e la conversione dei payload wire in `ChatContact`.
- **SPA web** (JavaScript vanilla in `web/static/`): client HTTP/WebSocket dello stesso server.

La separazione è netta: la UI non conosce envelope di protocollo, non apre servizi esterni e non scrive SQLite. Ogni mutazione passa da un'operazione del server; ogni dato in ingresso arriva come evento tipizzato del bus.

| Protocollo | Implementazione | Servizio esterno | Ricezione |
|---|---|---|---|
| Signal | `megachatd/internal/proto/signal` | `signal-cli` daemon (JVM), JSON-RPC HTTP + SSE; fallback subprocess one-shot | stream SSE in goroutine dedicata |
| WhatsApp | `megachatd/internal/proto/whatsapp` | nessuno: whatsmeow (WebSocket multi-device nativo, `whatsmeow.db`) | eventi whatsmeow |
| Telegram | `megachatd/internal/proto/telegram` | nessuno: gotd/MTProto; importa una sessione Telethon esistente | update handler gotd |
| Gmail | `megachatd/internal/gmail` | IMAP/SMTP Gmail | polling dell'inbox (`gmail.unread_poll_s`) |

Persistenza: SQLite (`~/.local/share/signal-tui-client/messages.db`, schema `PRAGMA user_version = 5`) con tabelle `messages`, `reactions`, `transcriptions`. Lo store serializza le operazioni su una singola connessione del pool e usa transazioni `IMMEDIATE` per le mutazioni multi-statement; il file è lo stesso che usava il server Python, quindi la migrazione è in-place.

## 2. Processi e trasporti

```
┌──────────────────────────────┐        Unix socket (JSON-RPC 2.0)
│ TUI Textual (Python)         │◄──────────────────────────────────┐
│  tui/  ── client/ ───────────┼── SocketClient.call/subscribe     │
└──────────────────────────────┘                                   │
                                                                   ▼
┌──────────────────────────────┐        HTTP REST + WS      ┌───────────────────────────┐
│ SPA (web/static)             │◄──────────────────────────►│ megachatd (Go)            │
│  fetch/WebSocket             │                            │  rpc adapter  web adapter │
└──────────────────────────────┘                            │        │          │       │
                                                            │        ▼          ▼       │
                                                            │   ops.Registry (68 ops)   │
                                                            │        │                  │
                                                            │        ▼                  │
                                                            │   core.Core ──► bus.Bus   │
                                                            │        │                  │
                                                            │        ▼                  │
                                                            │   store.Store (SQLite)    │
                                                            │        ▲                  │
                                                            │  proto.Manager            │
                                                            │   ├── signal              │
                                                            │   ├── whatsapp            │
                                                            │   └── telegram            │
                                                            │  gmail / media / linking  │
                                                            │  messages / transcribe    │
                                                            └───────────────────────────┘
```

Posizioni e file runtime:

- socket: `--socket PATH`, altrimenti `$MEGACHAT_SOCKET`, altrimenti `$XDG_RUNTIME_DIR/megachat/server.sock`, altrimenti `~/.local/state/megachat/server.sock`;
- file runtime accanto al socket (`server.sock` → `server.json`): `pid`, `socket`, `version`, `api_version`, `started_at`;
- lock di istanza: `$MEGACHAT_LOCK_FILE` (default `/tmp/signal-tui.lock`), posseduto dal server;
- log del server: `$MEGACHAT_LOG_FILE`, default `/tmp/signal-tui.log`.

Il client TUI cerca il server sul socket di default e poi sul socket del servizio di sistema (`~/.local/state/megachat/server.sock`); se nessuno risponde avvia `megachatd serve` in background e attende il socket fino a 30 s (`client/attach.py`).

## 3. Flussi principali

### 3.1 Messaggio in arrivo

```
backend di chat (signal/whatsapp/telegram)
   └─ evento normalizzato (map wire)
      └─ core.Core.process / ingest
           ├─ store: INSERT/merge della riga (dedup, rank guard)
           └─ bus.Publish(type, payload)
                ├─ rpc adapter  → notifica "event" ai client socket
                └─ web adapter  → frame su /ws e /ws/v2
                     └─ TUI: tui/polling.py consuma la sottoscrizione
                        └─ tui/events.py dispatcha: message, message_edit,
                           reaction_update, receipt, typing, contact_update
```

Il core è l'unico punto di ingresso dei dati: i backend non pubblicano direttamente sulla UI, e gli eventi hanno una forma JSON stabile (`internal/api/events.go`, lato client `client/events_api.py`).

### 3.2 Messaggio in uscita (invio ottimistico)

1. La TUI aggiorna subito la cache locale e mostra la bolla `pending`, poi chiama in un worker thread `messages.send` (socket).
2. Il core crea/persiste la riga, invoca il backend di protocollo e attende l'esito.
3. Al successo il server risponde con il messaggio aggiornato e pubblica l'echo; la TUI porta la riga a `sent` e aggancia l'id server. I receipt successivi (`delivered`/`read`) arrivano come eventi `receipt`.
4. Un fallimento porta la bolla a `failed` (cliccabile per retry); un timeout di trasporto è trattato come esito ignoto e la vista si risincronizza.

### 3.3 Avvio del server (`megachatd serve`)

1. parsing dei flag e del project dir (`--project-dir`, `$MEGACHAT_PROJECT_DIR`, altrimenti l'antenato dell'eseguibile con `megachat.py`/`pyproject.toml`);
2. lock di istanza (uscita 1 se già posseduto);
3. validazione delle integrazioni abilitate (Telegram/Gmail senza credenziali → uscita 1 con tutti i problemi elencati);
4. apertura dello store e migrazione schema;
5. registrazione dei backend abilitati, dei servizi (`core`, `media`, `messages`, `linking`, `transcribe`, `gmail`) e delle operazioni;
6. bind del socket e avvio di HTTP/WebSocket; i backend si connettono in parallelo e riportano lo stato con `backend_connecting` / `backend_ready` / `backend_done`;
7. SIGINT/SIGTERM o `server.shutdown` chiudono il tutto in modo ordinato.

## 4. Pattern architetturali

| Pattern | Dove | Scopo |
|---|---|---|
| Registry di operazioni | `internal/ops`, `internal/api/ops.go` | un'unica tabella `nome → handler/request/response`; i trasporti traducono soltanto |
| Adapter di trasporto | `internal/rpc`, `internal/web` | socket JSON-RPC e REST/WS chiamano le stesse operazioni |
| Backend contract + capability opzionali | `internal/proto/backend.go` | `Backend` minimo; ingest, receipt, editor, reactor, address book, linking sono interfacce scoperte con type assertion |
| Manager per protocollo | `internal/proto/manager.go` | registry, connessioni concorrenti, stato, restart, fan-out rubrica |
| Event bus con replay | `internal/bus` | ring di 2000 eventi, `seq` monotono, coda per sottoscrittore (1000), evento `gap` quando il replay non è possibile |
| Client transport-agnostic | `client/client.py`, `client/socket_client.py` | `call`/`subscribe` con riconnessione, heartbeat, version check, ripresa da `since` |
| Ottimismo con rollback | `tui/send.py`, `tui/edit.py` | bolla immediata, riconciliazione con l'esito del server, ripristino su errore |
| Mixin composition | `tui/app.py` | spezza l'app Textual in mixin per concerns |
| Worker + UI thread | `tui/client_calls.py`, `tui/polling.py` | nessuna chiamata al server sul thread Textual; risultati applicati con `call_from_thread` |

## 5. Decisioni architetturali chiave

1. **Il server possiede i protocolli**: TUI e SPA sono client sostituibili; un solo database, una sola sessione per protocollo, più client collegati contemporaneamente.
2. **Un contratto unico per tre trasporti**: aggiungere una route o un'operazione significa toccare solo `internal/api` e l'adapter; il comportamento resta identico su socket, REST e WebSocket.
3. **Compatibilità del filo**: nomi di campi e codici errore sono quelli storici (spesso con nomi Python) per non rompere client esistenti; il contratto è in [`megachatd/CONTRACTS.md`](../../megachatd/CONTRACTS.md).
4. **Signal daemon-first**: il backend prova il daemon JSON-RPC/SSE e degrada a chiamate subprocess one-shot quando il daemon non è disponibile.
5. **WhatsApp nativo**: whatsmeow elimina Docker e WAHA; una sessione WAHA non è migrabile e va rifatto il pairing QR (avviso una tantum).
6. **Telegram con sessione importata**: gotd usa un formato di sessione proprio; `internal/proto/telegram/session.go` converte la sessione Telethon (SQLite o stringa) al primo avvio.
7. **Eventi con resume**: `seq` monotono, replay dal ring e `gap` esplicito; i client risincronizzano invece di indovinare.
8. **Unread**: il server fornisce i conteggi a `contacts.list` (SPA) e segnala i cambi con gli eventi; la TUI li ricalcola dai propri dati di vista (dedup per timestamp+testo).
9. **Configurazione a due sorgenti**: `config.json` (un oggetto per dominio) e variabili `<DOMAIN>_<SETTING>`; `.env` non è più letto, `megachatd config migrate` converte i file vecchi.

## 6. Dipendenze tra moduli

- `tui/*` → `client/*`, `models.py`, `ui_components.py`, `contact_picker.py`, `emoji_picker.py`, `device_link_screen.py`, `qr_utils.py`.
- `client/*` → solo stdlib, `pydantic` (schemi eventi) e `models.py`; **non** importa Textual, `tui/` o `web/`.
- `megachatd/internal/core` → `api`, `ops`, `bus`, `store`, `proto`, `config`.
- `megachatd/internal/proto/*` → `proto`, `store`, `api`; non importa `core` (le capability del core sono interfacce definite in `core` e implementate dai backend).
- `megachatd/internal/web`, `internal/rpc` → `api`, `ops`, `bus`, `config`; non importano `core`.
- `internal/store` → solo stdlib + `modernc.org/sqlite`.

## 7. Punti di estensione

- **Nuova operazione**: dichiararla in `internal/api` (request/response + `Ops`) e registrare l'handler in `internal/core` o nel servizio competente; i trasporti la espongono automaticamente.
- **Nuovo protocollo**: implementare `proto.Backend` e le capability opzionali in `internal/proto/<nome>`, registrarlo in `cmd/megachatd`; la UI funziona senza modifiche perché riceve gli stessi eventi.
- **Nuova route REST**: aggiungerla a `api.Routes` e al router in `internal/web`; l'autenticazione Bearer copre tutto ciò che sta sotto `/api/`.
- **Nuovo client**: implementare `ChatClient` (operazioni + sottoscrizione) come fa `client/socket_client.py`.

## Documenti collegati

- [BACKEND_COMPONENTS.md](BACKEND_COMPONENTS.md) — dettaglio dei package server e client.
- [../api-contracts/API_OVERVIEW.md](../api-contracts/API_OVERVIEW.md) — operazioni, socket, REST, WebSocket, eventi.
- [../../megachatd/CONTRACTS.md](../../megachatd/CONTRACTS.md) — contratto del filo completo.
- [../design/DESIGN_OVERVIEW.md](../design/DESIGN_OVERVIEW.md) — principi di design della TUI.
