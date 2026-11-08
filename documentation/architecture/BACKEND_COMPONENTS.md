# Componenti del server e del client

Descrizione dei package che compongono `megachatd` (Go) e dei package Python `client/` e `tui/`. Ricavato dai sorgenti attuali. Per il quadro d'insieme vedere [ARCHITECTURE_OVERVIEW.md](ARCHITECTURE_OVERVIEW.md).

## 1. Server — `megachatd/cmd/megachatd`

- `main.go`: entry point; sottocomandi `serve`, `version`, `config migrate`.
- `app.go`: `Build()` collega config, logger, store, bus, manager dei backend, core, registry delle operazioni e servizi (linking, transcription, Gmail, media, download); `Start()`/`Stop()` gestiscono lock, socket, HTTP e segnali.
- `adapters.go`: monta l'adapter Unix socket (`internal/rpc`) e quello HTTP/WS (`internal/web`).
- `staging.go` / `download.go`: copia degli allegati inviati e server di download temporaneo (porta 10042).
- `fakebackend_*.go`: backend finto dietro il build tag `contracttest` per la suite di contratto.

## 2. Server — package interni

### 2.1 `internal/api` — contratto del filo

- `opnames.go` / `ops.go`: le **68 operazioni** (`<dominio>.<azione>`) con request/response tipizzate e client ammessi (TUI `T`, web `W`).
- `models.go`, `requests.go`, `responses.go`: struct JSON con i nomi di campo storici; i tag sono il contratto.
- `routes.go`: tabella REST completa (metodo, path, operazione, auth, blob) e route WebSocket.
- `events.go`: tipi e payload degli eventi del bus.
- `errors.go`: codici neutri, mapping HTTP e JSON-RPC.
- `rpc.go`: costanti del trasporto socket (`api_version = 1`, limiti, heartbeat, metodi builtin `rpc.hello`, `server.ping`, `events.subscribe`, `events.unsubscribe`) e path di default.
- `web.go`: costanti di autenticazione e protocolli WebSocket.

### 2.2 `internal/ops` — registry

`Registry` implementa `Call`/`Has`/`Names`: valida i parametri, invoca l'handler e converte gli errori in `*ops.Error` (codice + messaggio utente). Gli adapter dipendono solo da `Dispatcher` ed `EventSource`, mai da `core`.

### 2.3 `internal/core` — chat core

- `core.go`: struttura `Core`, ciclo di vita dei backend, registrazione delle operazioni.
- `ingest.go`: ingestione di un messaggio con dedup e upgrade della riga ottimistica; `IngestAdded/Changed/Unchanged`.
- `ops_*.go`: handler delle operazioni (`contacts`, `chats`, `send`, `edit`, `reactions`, `meta`).
- `events.go`: costruzione e pubblicazione degli eventi del bus.
- `contactstate.go`, `addressbook.go`: stato dei contatti, rubrica con TTL e fan-out per protocollo.
- `retry.go`, `sendregistry.go`, `uploads.go`: invii, retry e upload.
- `lock.go`, `logbuffer.go`: lock di istanza e buffer dei log esposto da `server.logs`.

Le capability opzionali dei backend sono interfacce dichiarate qui (`MessageIngester`, `ReceiptProcessor`, `ContactRegistrar`, `UnreadReconciler`, …): un backend le implementa se serve, altrimenti il core usa il fallback su store.

### 2.4 `internal/store` — SQLite

- `store.go`: apertura del database condiviso, pool serializzato, transazioni `IMMEDIATE`, migrazioni in-place (`PRAGMA user_version = 5`).
- `schema.go` / `schema_sql.go`: tabelle `messages`, `reactions`, `transcriptions` e migrazioni additive (`protocol`, `msg_id`, colonne quote/media, `batch_id`/`batch_index`).
- `messages.go`, `queries.go`, `rowmap.go`: CRUD, finestre/paginazione, mapping riga → wire.
- `dedup.go`, `survivor.go`, `repair_test.go`: dedup esatta e per id, scelta della riga "sopravvissuta" col rank di stato più alto.
- `reactions.go`, `transcriptions.go`, `media_quote.go`, `prune_test.go`: reazioni, trascrizioni, riferimenti alle quote media, retention.
- `models.go`, `models_consts.go`: tipi interni e costanti di stato/protocollo.

### 2.5 `internal/bus` — eventi

Bus in-process: `seq` monotono, ring di 2000 eventi, coda per sottoscrittore (1000), sottoscrizione con `since`, `gap` (`buffer`/`overflow`), chiusura idempotente.

### 2.6 `internal/rpc` — Unix socket

Adapter JSON-RPC 2.0 su socket Unix: framing per linea, richieste concorrenti per connessione (max 64), `events.subscribe`/`events.unsubscribe`, notifiche `event` e `heartbeat`, limite di 16 MiB per linea, controllo peer-credential dove disponibile, lock del socket e file runtime.

### 2.7 `internal/web` — HTTP e WebSocket

Router chi con le route di `api.Routes`, autenticazione Bearer (tranne `/health`, `/gmail-image` e la SPA), upload multipart con guardie per tipo/dimensione, blob, SPA statica, `/ws` legacy e `/ws/v2` tipizzato con resume. Le route sono traduzioni sottili verso le operazioni.

### 2.8 `internal/proto` — backend di protocollo

- `backend.go`: contratto `Backend` (`Protocol`, `Connect`, `Disconnect`, `IsConnected`, `ListContacts`, `SendMessage`, …) e interfacce capability.
- `manager.go`: registry per protocollo, stati (`idle/connecting/connected/failed/stopped`), connessioni concorrenti, restart per protocollo, fan-out rubrica (3 worker, timeout 25 s).
- `types.go`: `Contact`, `Message`, risultati di ingest e capability.
- `signal/`: daemon `signal-cli` JSON-RPC/SSE (`rpc.go`), parsing envelope (`envelope.go`), ingest (`ingest.go`), invio/edit/reaction (`send.go`), quote (`quote_thumb_webp_test.go`), coda eventi.
- `whatsapp/`: client whatsmeow, pairing QR, conversione eventi (`convert.go`), ingest e history sync, app-state, unread, media, quote, reazioni, guardie di sessione, resolver `@lid`.
- `telegram/`: gotd/MTProto, import della sessione Telethon (`session.go`), conversione update, contatti, cronologia, media, invio, reazioni, migrazione degli id grezzi → marcati.
- `fake/`: backend deterministico per test di core e contratto.

### 2.9 Altri servizi

- `internal/gmail/`: IMAP/SMTP, parsing MIME, sanitizzazione HTML e CSS, proxy immagini (prefetch, token firmati), watcher unread.
- `internal/media/`: risoluzione allegati, thumbnail (immagini e video via ffmpeg), upload, link preview SSRF-guardata, avatar.
- `internal/messages/`: viste dei messaggi per TUI/SPA (paginazione, finestre, grouping, cleanup delle caption, campi quote).
- `internal/linking/`: macchina a stati del pairing QR (`link.start/poll/finalize/cancel/submit_2fa`), sessioni abbandonate dopo 900 s, refresh QR dopo 60 s.
- `internal/transcribe/`: provider OpenAI/OpenRouter/Gemini, validazione impostazioni, coda di trascrizione, auto-trascrizione per chat.
- `internal/config/`: lettura `config.json` + variabili `<DOMAIN>_<SETTING>`, schema e default, path del progetto, migrazione dal formato piatto, validazione delle integrazioni all'avvio.

## 3. Client — package Python `client/`

- `client.py`: interfacce astratte `ChatClient` e `ClientSubscription` (operazioni + eventi), indipendenti dal trasporto.
- `socket_client.py`: trasporto Unix socket; thread supervisore con riconnessione e backoff, heartbeat, match richiesta/risposta per id, `ServerUnavailable`/`OperationTimeout`/`IncompatibleServer`, ripresa con `since`, evento sintetico `client_connection`.
- `bus.py`: envelope `Event(seq, type, payload, ts)`, `GAP`.
- `events_api.py`: schemi wire degli eventi, conversione `ContactWire` ↔ `ChatContact`.
- `attach.py`: find-or-spawn del server; `lock.py`, `paths.py`: lock, socket, file runtime.
- `config.py`: impostazioni del client (protocollo immagini, opzioni web passate a un server avviato); `domain.py`: helper di ricerca/raggruppamento; `errors.py`, `version.py`.

`client/` non importa Textual, `tui/` né `web/`.

## 4. Client — package Python `tui/`

Mixin composti in `SignalTUI(App)` (`tui/app.py`): `ChatViewMixin`, `EventHandlingMixin`, `ContactListMixin`, `BackendConnectMixin`, `PollingMixin`, `SendMixin`, `EditMessageMixin`, `UnreadReplyMixin`, `DownloadModeMixin`, `PickerMixin`, `ClientCallsMixin`, `ExitFlowMixin`.

| Modulo | Responsabilità |
|---|---|
| `client_calls.py` | `_call_op` in worker, timeout espliciti, mapping degli errori di trasporto, risincronizzazione |
| `polling.py` | consumatore unico della sottoscrizione eventi (batch + dispatch) |
| `events.py` | aggiorna cache di vista, lista contatti e widget dai payload wire |
| `backend_connect.py` | a backend pronto: `messages.snapshot` + `contacts.for_protocol`, merge add-only |
| `chat_view.py` | finestra di chat, mount atomico, load-more, immagini |
| `contacts.py` | ordinamento, grouping, filtri, open-or-create, picker |
| `send.py` / `edit.py` | invio ed edit ottimistici con rollback (`messages.send`, `messages.edit`) |
| `unread_reply.py` | badge unread e reply bar |
| `download.py`, `blobs.py` | download mode e risoluzione blob/thumbnail tramite il server |
| `exit_flow.py` | dialog Detach/Quit e `server.shutdown` |
| `images/` | rilevamento terminale, celle, renderer kitty |
| `css.py` | foglio di stile Textual |

## 5. Relazioni (sintesi)

```
tui/ ──► client/ ──► Unix socket ──► rpc adapter ─┐
SPA  ──► HTTP/WS ─────────────────► web adapter ──┤
                                                  ▼
                                          ops.Registry
                                                  │
                                          core.Core ──► bus.Bus
                                             │  ▲
                                             ▼  │
                                        store.Store
                                             ▲
                                        proto.Manager ──► signal / whatsapp / telegram / fake
                                                  │
                                        gmail / media / messages / linking / transcribe
```

Ogni backend possiede la propria coda eventi e la propria connessione; la UI non accede mai direttamente a un servizio esterno o al database.
