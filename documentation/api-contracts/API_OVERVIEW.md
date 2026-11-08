# API e contratti interni — panoramica

Superfici programmatiche del sistema attuale, ricavate dal codice. Il contratto completo del filo, byte per byte, è in [`megachatd/CONTRACTS.md`](../../megachatd/CONTRACTS.md); qui c'è la mappa. Per i contratti di dati (modelli, payload, schema SQLite) vedere [CONTRACTS.md](CONTRACTS.md).

Indice:

1. [Registry delle operazioni](#1-registry-delle-operazioni)
2. [Trasporto Unix socket (JSON-RPC 2.0)](#2-trasporto-unix-socket-json-rpc-20)
3. [REST e WebSocket](#3-rest-e-websocket)
4. [Event bus](#4-event-bus)
5. [Client Python `ChatClient` / `SocketClient`](#5-client-python-chatclient--socketclient)
6. [Store SQLite](#6-store-sqlite)
7. [Configurazione](#7-configurazione)
8. [Errori](#8-errori)

---

## 1. Registry delle operazioni

`internal/api/opnames.go` elenca **68 operazioni**, organizzate per dominio:

| Dominio | Operazioni | Cosa copre |
|---|---|---|
| `contacts.*` | `list`, `for_protocol`, `book`, `address_book`, `register`, `check_number`, `avatar` | elenchi contatti, rubrica, registrazione, avatar |
| `messages.*` | `page`, `window`, `snapshot`, `send`, `send.check`, `edit` | lettura (SPA/TUI), invio, modifica |
| `chats.*` | `mark_read`, `archive` | stato della chat |
| `media.*` | `attachment`, `thumbnail`, `quote`, `quoted_attachment` | allegati, thumbnail, quote media |
| `reactions.*` | `set`, `available` | reazioni |
| `download.*` | `serve_text`, `serve_attachment` | download temporaneo (porta 10042) |
| `links.*` | `preview`, `preview_image` | anteprima Open Graph |
| `link.*` | `status`, `required`, `start`, `poll`, `finalize`, `cancel`, `submit_2fa`, `reconnect` | pairing QR |
| `server.*` | `status`, `clients`, `config`, `logs`, `logs_clear`, `client_log`, `shutdown` | stato, log, shutdown |
| `emoji.list` | elenco emoji | SPA |
| `gmail.*` | `status`, `unread`, `address_book`, `messages.*`, `attachment`, `image` | tab Gmail |
| `transcribe.*`, `autotranscribe.*` | stato, impostazioni, provider, submit, override per chat | trascrizione |

Ogni operazione dichiara request, response e trasporti ammessi in `internal/api/ops.go`; `internal/ops.Registry` esegue l'handler, valida i parametri (modo lax: `"10"` vale per un int, `"true"` per un bool) e converte gli errori in codici neutri. Le operazioni possono bloccare per secondi, non vengono cancellate se il chiamante va in timeout e possono essere invocate in concorrenza.

## 2. Trasporto Unix socket (JSON-RPC 2.0)

- Metodo = nome dell'operazione; `params` oggetto JSON; risposta `result` oppure `error` con codice JSON-RPC.
- Metodi builtin: `rpc.hello` (versione `api_version = 1`, elenco operazioni, versione server), `server.ping`, `events.subscribe`, `events.unsubscribe`.
- Dopo la sottoscrizione il server scrive notifiche `event` (envelope del bus) e `heartbeat`.
- Limiti: 16 MiB per linea, 600 s di timeout massimo per chiamata, 32 connessioni, 64 richieste in volo per connessione, heartbeat ogni 5 s, timeout client 15 s.
- Percorso: `--socket`, `$MEGACHAT_SOCKET`, `$XDG_RUNTIME_DIR/megachat/server.sock`, `~/.local/state/megachat/server.sock`; file runtime `server.json` accanto al socket.
- Il client rifiuta un `api_version` diverso (`IncompatibleServer`).

Dettaglio dei codici e dei frame in [`CONTRACTS.md`](../../megachatd/CONTRACTS.md) §3.

## 3. REST e WebSocket

- REST: tutte le route sotto `/api/` richiedono il Bearer token (`Authorization: Bearer`, token da `config.json` `web.token` o `WEB_TOKEN`); eccezioni: `/health`, `/gmail-image` e la SPA statica.
- La tabella completa (metodo, path, operazione, auth, blob) è `internal/api/routes.go`; l'adapter è `internal/web`. Le route sono traduzioni sottili: la logica vive nelle operazioni.
- WebSocket: `/ws` (frame legacy `{"event": {...}}`) e `/ws/v2` (eventi tipizzati con `seq`, subscribe con `since`, replay dal ring e `gap`). Il token si può passare come header o come sottoprotocollo `signal-tui-token.<base64url>`; un client non autorizzato riceve HTTP 403.
- Upload: multipart su `/api/send` e `/api/gmail/send`, con limiti per tipo e per file applicati durante la copia.
- `--web-no-auth` disattiva il token (solo reti fidate): la SPA salta il login, chiunque raggiunga host/porta ha accesso completo.

## 4. Event bus

`internal/bus` pubblica envelope `{seq, type, payload, ts}`. `seq` parte da 1 ed è monotono; il ring tiene 2000 eventi e ogni sottoscrittore ha una coda di 1000. Se il `since` richiesto è fuori dal ring arriva un evento `gap` con `reason: "buffer"`; se la coda trabocca, `gap` con `reason: "overflow"`.

Tipi (`internal/api/events.go`, lato client `client/events_api.py`):

| Tipo | Significato |
|---|---|
| `backend_connecting`, `backend_ready`, `backend_done`, `backend_status` | avanzamento connessione dei backend (persistente per `backend_status`) |
| `message`, `message_edit`, `reaction_update`, `receipt`, `typing`, `contact_update`, `sent_mirror` | dominio |
| `lid_warmup_done` | risoluzione `@lid` completata (WhatsApp) |
| `web_push` | frame legacy per la SPA |
| `gap` | eventi persi: il client deve risincronizzare |

Il client `SocketClient` aggiunge un evento sintetico `client_connection` (`lost`/`restored`) che non avanza `seq`.

## 5. Client Python `ChatClient` / `SocketClient`

`client/client.py` definisce due interfacce:

- `ChatClient.call(name, data)` / `acall(name, data)`: operazioni con dati JSON, errori come `client.errors.ApiError`.
- `ChatClient.subscribe(since=None) -> ClientSubscription`: `get(timeout)`, `drain()`, `aget(timeout)`, `close()`, `closed`.

`client/socket_client.py` implementa il trasporto Unix socket:

- un thread supervisore legge il socket, distribuisce risposte per id e instrada gli eventi;
- riconnessione con backoff limitato e jitter; al ritorno verifica la versione, ri-sottoscrive con `since` e riproduce gli eventi persi;
- errori: `ServerUnavailable`, `OperationTimeout` (l'operazione può comunque essere andata a buon fine), `IncompatibleServer`;
- gli allegati si passano come path assoluti in `attachments`; il server li copia.

La TUI usa esclusivamente queste interfacce (`tui/client_calls.py` centralizza worker, timeout e mapping errori).

## 6. Store SQLite

`internal/store` apre `~/.local/share/signal-tui-client/messages.db` e migra in-place lo schema storico fino a `PRAGMA user_version = 5`. Tabelle:

- `messages`: riga per messaggio, con `protocol`, `contact_number`, testo, direzione, timestamp, campi quote/media (`content_type`, `media_kind`, `quote_attachment_*`), stato (`status`, `read`), id server (`msg_id`), flag `edited`, e `batch_id`/`batch_index` per i messaggi multi-allegato;
- `reactions`: emoji per `(protocol, contact_number, target_msg_id/target_timestamp, author_key)`, con conteggio;
- `transcriptions`: stato/testo/modello per `(protocol, attachment_id)`.

Lo store è sicuro per l'uso concorrente: le statement girano su una connessione del pool e le operazioni multi-statement usano una transazione `IMMEDIATE`. Le API principali coprono ingest (`AddMessage`, `UpdateMessageID`, `UpdateMessageStatus*`, `UpdateMessageText`), lettura (`LoadCache`, `ListMessages`), unread/read, reazioni, trascrizioni e pruning. La dedup è in `dedup.go`: esatta per `(protocol, contact, timestamp, text, is_mine)` e per id `(protocol, contact, msg_id, text, attachment_id)`, tenendo la riga col rank di stato più alto.

## 7. Configurazione

`internal/config` legge due sorgenti:

1. `config.json` nella project dir (`--project-dir`, `$MEGACHAT_PROJECT_DIR`, altrimenti l'antenato dell'eseguibile con `megachat.py`/`pyproject.toml`), un oggetto JSON per dominio (`signal`, `whatsapp`, `telegram`, `gmail`, `web`, `transcription`, `openai`, `openrouter`, `gemini`, `messages`, `address_book`, `dynamic_refresh`, `picker`, `image`, `thumbnail`). Lo schema completo è `config.example.json`.
2. Variabili `<DOMAIN>_<SETTING>` (es. `SIGNAL_ENABLED`, `WHATSAPP_MEDIA_DIR`, `TRANSCRIPTION_PROVIDER`). Una variabile non vuota vince; altrimenti il valore in `config.json`; altrimenti il default.

`.env` non è più letto: `megachatd config migrate` converte il formato piatto e il vecchio `.env`. Le variabili di runtime (`MEGACHAT_PROJECT_DIR`, `MEGACHAT_SOCKET`, `MEGACHAT_LOCK_FILE`, `MEGACHAT_LOG_FILE`, `MEGACHAT_FAKE_BACKEND`, `HOME`, `XDG_*`, `SSH_CONNECTION`) non sono impostazioni. All'avvio le integrazioni abilitate vengono validate: se mancano credenziali il server esce con codice 1 elencando tutti i problemi (`internal/config/requirements.go`).

## 8. Errori

Codici neutri (`internal/api/errors.go`): `invalid_request`, `not_found`, `unauthorized`, `forbidden`, `too_large`, `conflict`, `unprocessable`, `not_supported`, `backend_down`, `backend_error`, `internal`. Ogni codice ha un mapping HTTP e JSON-RPC fisso (vedi `CONTRACTS.md` §2). Il messaggio è il testo mostrato all'utente (a volte in italiano) e va mantenuto verbatim; `details` è JSON opzionale.

Convenzioni lato client: `ApiError` per gli errori del server; gli errori di trasporto sono distinti e un timeout non implica che la mutazione non sia avvenuta (la TUI risincronizza).
