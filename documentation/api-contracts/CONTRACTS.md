# Contratti di dati

Contratti concreti ricavati dal codice attuale e fissati dai test. Tipi Go/Python; timestamp in millisecondi Unix salvo indicazione diversa. Il contratto del filo completo (operazioni, route, codici) è in [`megachatd/CONTRACTS.md`](../../megachatd/CONTRACTS.md).

Indice:

1. [Modelli condivisi (`models.py`)](#1-modelli-condivisi-modelspy)
2. [Messaggio sul filo (`CacheMessage`)](#2-messaggio-sul-filo-cachemessage)
3. [Persistenza SQLite](#3-persistenza-sqlite)
4. [Eventi del bus](#4-eventi-del-bus)
5. [Stato dei messaggi inviati](#5-stato-dei-messaggi-inviati)
6. [Contratto di edit dei messaggi](#6-contratto-di-edit-dei-messaggi)
7. [Receipt per protocollo](#7-receipt-per-protocollo)
8. [Contatti, raggruppamento e unread](#8-contatti-raggruppamento-e-unread)
9. [Ingresso lato server](#9-ingresso-lato-server)
10. [Errori e convenzioni di ritorno](#10-errori-e-convenzioni-di-ritorno)
11. [Quote media (reply con immagine)](#11-quote-media-reply-con-immagine)

---

## 1. Modelli condivisi (`models.py`)

Sono i modelli del client Python (nessuna dipendenza Textual). Il server usa le proprie struct (`internal/api`: `ContactWire`, `CacheMessage`); la conversione avviene in `client/events_api.py` e nei mapper dello store.

### 1.1 `ChatContact`

| Campo | Tipo | Semantica |
|---|---|---|
| `id` | `str` | identificativo unico nello scope del protocollo (numero Signal, JID WhatsApp, id Telegram) |
| `display_name` | `str` | nome mostrato (fallback: `id`) |
| `protocol` | `str` | `"signal"` \| `"whatsapp"` \| `"telegram"` |
| `extras` | `dict[str, Any]` | metadati: `phone` (E.164 senza `+`, chiave di grouping cross-backend), `last_message_ts` (ms), `address_book` (marker rubrica), `ghost`, `lid`, ecc. |

Derivati: `cache_key = f"{protocol}:{id}"`; property `phone`, `last_message_ts`.
Sul filo (`ContactWire`): `{id, display_name, protocol, cache_key, last_message_ts, extras}`; `ContactOut` (web) sostituisce `cache_key` con `unread`.

### 1.2 `ChatMessage`

| Campo | Tipo | Default | Semantica |
|---|---|---|---|
| `id` | `str` | — | id messaggio scope-protocollo (timestamp per Signal, id server Telegram, id WhatsApp) |
| `contact_id` / `protocol` | `str` | — | appartenenza |
| `text` | `str` | — | corpo (vuoto per media puri) |
| `is_mine` | `bool` | — | direzione |
| `sender` | `str` | — | autore visualizzato (`"You"` per gli outgoing) |
| `timestamp` | `int` | — | ms |
| `quote_text` | `str \| None` | `None` | testo citato (display) |
| `msg_type` | `str` | `"text"` | `text` \| `image` \| `sticker` \| `attachment` \| `system` |
| `attachment_info` / `attachment_id` | `str \| None` | `None` | dettagli/id attachment |
| `content_type` | `str \| None` | `None` | mime dell'attachment |
| `media_kind` | `str \| None` | `None` | classificazione media (`image`, `video`, `audio`, …) |
| `status` | `str` | `"sent"` | vedi §5 |
| `reply_to_message_id` | `str \| None` | `None` | id server del messaggio citato |
| `quote_attachment_id` / `quote_content_type` | `str \| None` | `None` | metadati della thumbnail citata, persistiti dal server |
| `quote_attachment_path` | `str \| None` | `None` | path risolto lato UI, **non** persistito |

### 1.3 Costanti di protocollo

`PROTOCOL_SIGNAL="signal"`, `PROTOCOL_WHATSAPP="whatsapp"`, `PROTOCOL_TELEGRAM="telegram"`; mappe `PROTOCOL_EMOJI` (`📱 💬 📨`) e `PROTOCOL_NAMES`; `protocol_emoji()` fallback `"💬"`.

## 2. Messaggio sul filo (`CacheMessage`)

Forma usata dagli eventi, da `messages.*` e dalla cache di vista (`internal/api/models.go::CacheMessage`, mappata in dict dalla TUI). Le chiavi note:

```python
{
    "id": str | None,               # id server/stabile (None nelle righe ottimistiche)
    "text": str,
    "is_mine": bool,
    "sender": str,                  # "You" per gli outgoing
    "timestamp": int,               # ms
    "quote_text": str | None,
    "msg_type": "text" | "image" | "sticker" | "attachment" | "system",
    "attachment_info": str | None,
    "attachment_id": str | None,    # per Telegram anche "tgref:<chat_id>:<msg_id>"
    "content_type": str | None,
    "media_kind": str | None,
    "read": bool,                   # incoming letti (outgoing sempre True)
    "status": "pending" | "failed" | "sent" | "delivered" | "read",
    # opzionali:
    "quote_timestamp": int | None,
    "quote_author": str | None,     # contact id, non display name
    "reply_to_message_id": str | None,
    "edited": bool,                 # flag "(modificato)"
    "batch_id": str | None,         # messaggi multi-allegato
    "batch_index": int | None,
    "quote_attachment_id": str | None,
    "quote_attachment_path": str | None,
    "quote_content_type": str | None,
    "is_group": bool,               # solo alcuni eventi WhatsApp
    "protocol": str,                # presente nelle righe caricate dal DB
    "contact": ContactWire | None,  # presente negli eventi
}
```

I membri sconosciuti vengono conservati (`Extra`) e rimandati indietro invariati; i membri non impostati dal produttore sono assenti (`omitempty`/`exclude_unset`).

Vincoli verificati:

- l'ingest è idempotente: un messaggio identico non produce una seconda riga (`added=false`);
- le righe ottimistiche nascono `status="pending"`, `id=None`;
- placeholder quote media: `quote_text` di un messaggio che cita media senza caption è uno dei 5 valori canonici di `MEDIA_QUOTE_PLACEHOLDERS` (`models.py`), display-only, mai inviati sul filo (vedi §11).

## 3. Persistenza SQLite

### 3.1 Schema

`internal/store/schema.go`, `PRAGMA user_version = 5`.

```
messages
  id INTEGER PK AUTOINCREMENT
  protocol TEXT NOT NULL DEFAULT 'signal'
  contact_number TEXT NOT NULL       -- id del contatto senza prefisso protocollo
  text TEXT · is_mine INTEGER NOT NULL DEFAULT 0 · sender TEXT · timestamp INTEGER NOT NULL
  quote_text TEXT · msg_type TEXT DEFAULT 'text'
  attachment_info TEXT · attachment_id TEXT · content_type TEXT · media_kind TEXT
  quote_attachment_id TEXT · quote_attachment_path TEXT · quote_content_type TEXT
  read INTEGER DEFAULT 0 · status TEXT DEFAULT 'read'
  msg_id TEXT · quote_timestamp INTEGER · quote_author TEXT · reply_to_message_id TEXT
  edited INTEGER NOT NULL DEFAULT 0
  batch_id TEXT · batch_index INTEGER
  -- indice (protocol, contact_number, timestamp)

reactions
  id INTEGER PK AUTOINCREMENT · protocol · contact_number
  target_msg_id TEXT · target_timestamp INTEGER
  emoji TEXT NOT NULL · author_key TEXT NOT NULL DEFAULT ''
  author TEXT NOT NULL DEFAULT '' · is_mine INTEGER · count INTEGER DEFAULT 1
  timestamp INTEGER NOT NULL
  -- unique (protocol, contact_number, IFNULL(target_msg_id,''), IFNULL(target_timestamp,0), author_key)
  -- index (protocol, contact_number)

transcriptions
  protocol TEXT · attachment_id TEXT · status TEXT DEFAULT 'pending'
  text TEXT · error TEXT · model TEXT · updated_at REAL NOT NULL
  -- PRIMARY KEY (protocol, attachment_id)
```

Migrazioni additive e idempotenti: le colonne attese sono aggiunte se mancano; la parte legacy (`protocol`/`msg_id`, ricostruzione indice, backfill `media_kind`) è gate-ata da `user_version >= 4`.

### 3.2 Regole transazionali

- default status all'INSERT: `"sent"` se `is_mine` else `"read"`; `read = is_mine`.
- rank guard su ogni update di status: `pending=0 failed=0 < sent=1 < delivered=2 < read=3` (mai downgrade).
- echo: una riga senza `msg_id` può assorbire l'id reale di un echo entro `EchoMatchWindowMs = 600000`; `UpdateMessageID` aggiorna solo la riga id-less corrispondente (protocollo, contatto, testo, direzione, timestamp).
- dedup esatta: `(protocol, contact, timestamp, text, is_mine)`, tiene la rowid più bassa.
- dedup per id: `(protocol, contact, msg_id, text, attachment_id)`; sopravvive la riga col rank di stato più alto (poi rowid più bassa), assorbendo i campi quote/media mancanti; partizioni con timestamp divergenti oltre la finestra echo vengono lasciate intatte e loggate.
- pruning: `messages.retention_per_contact` righe più recenti per `(protocol, contact_number)`, con minimo `MinPruneLimit = 100`.

Test: suite Go `internal/store/*_test.go` (`schema_test.go`, `dedup_test.go`, `messages_test.go`, `prune_test.go`).

## 4. Eventi del bus

Envelope: `{seq: int, type: str, payload: dict, ts: float}` (`internal/bus`). `seq` monotono; il client riprende da `since` e riceve `gap` se il replay non è possibile. I payload (`internal/api/events.go`) sono:

| type | Payload | Note |
|---|---|---|
| `message` | `{protocol, contact_id, contact: ContactWire, was_placeholder: bool, message: CacheMessage, timestamp, added: bool, changed: bool}` | emesso per ogni messaggio ingerito, anche se invariato |
| `message_edit` | `{protocol, contact_id, contact: ContactWire, info: EditInfo, text}` dove `EditInfo = {message_id, timestamp (ORIGINALE), old_text, text, is_mine}` | update in place |
| `reaction_update` | `{protocol, contact_id, info: {message_id, timestamp, reactions: [Reaction]}}` | `reactions` è `[]`, mai null |
| `receipt` | `{protocol, contact_id, updated: [CacheMessage]}` | righe il cui status è avanzato; `updated` è `[]`, mai null |
| `typing` | `{protocol, contact_id, action: "STARTED"\|"STOPPED"}` | effimero |
| `contact_update` | `{protocol, contact_id, contact, display_name, phone, merged_into?}` | `display_name`/`phone` sempre presenti (null se ignoti) |
| `sent_mirror` | `{protocol, contact_id, contact, id, timestamp, batch_id}` | invio multiplo; le righe sono già nello store |
| `lid_warmup_done` | `{protocol, contact_id: ""}` | WhatsApp |
| `backend_connecting` / `backend_ready` / `backend_done` / `backend_status` | stato dei backend | `backend_status` è persistente finché non viene sostituito |
| `web_push` | `{event: WebFrame}` | frame legacy della SPA |
| `gap` | `{reason: "buffer"\|"overflow", since?}` | il client deve risincronizzare |

Il client aggiunge `client_connection` (`lost`/`restored`), sintetico, con il `seq` dell'ultimo evento visto.

## 5. Stato dei messaggi inviati

Valori e ordine (rank): `pending`(0) · `failed`(0) · `sent`(1) · `delivered`(2) · `read`(3). Transizioni solo in avanti (rank guard).

- bolla ottimistica: `pending` alla submit;
- il server crea e persiste la riga, invia, risponde; la TUI porta la bolla a `sent` e aggancia l'id server;
- errore o backend assente → `failed` (bolla cliccabile per retry);
- receipt delivery/read → `delivered`/`read`.

Rendering (`MessageWidget`): pending dim, failed bold+classe `msg-failed`, sent italic, delivered bold, read normale.

Dettaglio: [../design/DESIGN_OUTGOING_MESSAGE_STATUS.md](../design/DESIGN_OUTGOING_MESSAGE_STATUS.md).

## 6. Contratto di edit dei messaggi

Lato server l'operazione è `messages.edit`; lato TUI il gate e il rollback sono in `tui/edit.py`.

- `messages.edit` request: `{protocol, contact_id, message_id, text}` (la semantica di `message_id` dipende dal protocollo: Signal = timestamp originale come stringa, Telegram = id server, WhatsApp = id messaggio).
- Il server risponde con il messaggio aggiornato e pubblica `message_edit`; un target inesistente/non testuale/non proprio è un errore dell'operazione.
- `apply_edit`/`EditInfo` (`internal/api/events.go`) ritorna:
  ```python
  {
      "message_id": str,
      "timestamp": int,  # ts della entry, MAI modificato
      "old_text": str,
      "text": str,
      "is_mine": bool,
  }
  ```
- `timestamp` non cambia mai: cambia solo il testo (e `edited`).
- Vincoli UI: solo messaggi propri, non `pending`/`failed`, solo testo; rollback completo (testo, flag `edited`, identità di render, widget) se il server rifiuta.

### 6.1 Edit riferito a un messaggio mai visto

Comportamento attuale:

- **Signal / Telegram**: l'evento `message_edit` arriva; se il target non è nella cache di vista la TUI scarta l'evento e non crea bolle finché un fetch storico non porta il messaggio già editato; il server ha comunque applicato la modifica alla riga se presente.
- **WhatsApp**: il server emette l'edit quando riconosce il target; l'evento porta comunque il testo aggiornato e la vista lo applica se la riga esiste.

## 7. Receipt per protocollo

| Protocollo | Origine | Matching verso i messaggi | Test |
|---|---|---|---|
| Signal | envelope `receiptMessage` (`isDelivery`/`isRead`, lista `timestamps`) | fuzzy `±1000 ms` sui timestamp propri, rank guard; il server pubblica le righe aggiornate | `internal/proto/signal/*_test.go`, `tests/contract/test_socket_ops.py` |
| WhatsApp | eventi whatsmeow di ack/read | confronto per id del messaggio (id stabile whatsmeow) e fallback per timestamp/testo; rank guard | `internal/proto/whatsapp/*_test.go` |
| Telegram | update di lettura / `readHistoryInbox` | update per `msg_id` del messaggio | `internal/proto/telegram/*_test.go` |

Effetti comuni: upgrade dello status nello store e pubblicazione dell'evento `receipt` con le righe aggiornate; la UI aggiorna i widget se la chat è aperta.

## 8. Contatti, raggruppamento e unread

- **Grouping**: `group_by_person(contacts)` (`contact_picker.py`) raggruppa per persona usando `extras["phone"]` (E.164 senza `+`); `PickerEntry.key` identifica il gruppo (`phone:<numero>` oppure `raw:<protocol>:<id>` — gruppi/canali, `@lid` irrisolti e id Telegram non sono chiavi di phone-grouping); ordine gruppi = recency del membro di default, membri in ordine fisso Signal→WhatsApp→Telegram.
- **Rubrica e picker (Ctrl+S)**: la picker mostra subito le chat attive mentre un worker chiama `contacts.address_book` (fan-out server-side, errori parziali in status bar); il token `_address_book_token` invalida i worker in volo. La ricerca filtra per nome/id/phone su qualunque membro; selezione multi-backend senza filtro → `BackendChoiceScreen`. **Open-or-create**: un contatto sconosciuto diventa un "ghost" (`extras["ghost"]`), viene registrato (`contacts.register`) e — per WhatsApp — verificato (`contacts.check_number`).
- **Unread**: la SPA legge i conteggi da `contacts.list`; la TUI li ricalcola dai propri dati di vista (`not is_mine AND not read`, dedup per `(timestamp, text)`, chiave `cache_key`), aggiornati dagli eventi. Badge `*N` sull'header aggregato (vista All) o sul membro filtrato; il contatto selezionato non mostra badge ed è "pinned" sotto filtro unread; totali per backend nella status bar (`📱 N 💬 N 📨 N`, `-` se 0).
- **Status bar clickabile**: click su un segmento di protocollo → filtro protocollo + vista unread **solo se** quel backend ha unread > 0, altrimenti filtro semplice; ri-click sul segmento attivo in modalità unread disattiva la vista unread (mantenendo il filtro). Nessun effetto mentre uno screen modale è aperto.
- **Ordinamento e auto-selezione**: `contact_sort_key` (messaggi per `last_message_ts` desc → con nome alfabetici → solo numero); l'invio ottimistico avanza subito `last_message_ts` e ri-renderizza la lista; al boot l'auto-selezione parte quando tutti i backend attesi hanno riportato esito (`backend_status`/`backend_ready`/`backend_done`).
- **Typing**: `✍️` mentre `STARTED` è attivo (auto-expire 10 s → `💭` per 60 s); un messaggio reale del contatto sposta in mumbling; `STOPPED` → mumbling.

## 9. Ingresso lato server

Non esiste più un webhook HTTP in ingresso: i backend sono interni al server.

- **Signal**: daemon `signal-cli` → SSE (`GET /api/v1/events`); ogni envelope viene classificato da `internal/proto/signal/envelope.go` in `message`, `message_edit`, `typing`, `receipt`.
- **WhatsApp**: whatsmeow consegna gli eventi (messaggi, ack, presenza, app-state); `internal/proto/whatsapp/convert.go` li normalizza, `ingest.go` applica dedup e history sync.
- **Telegram**: gotd consegna update (nuovi messaggi, edit, letture, typing); `convert.go` li normalizza.
- **Gmail**: polling IMAP (`gmail.unread_poll_s`, default 60) con `SELECT` read-only e `SEARCH UNSEEN` sulla finestra mostrata; apertura/cancellazione ricontrollano subito.

## 10. Errori e convenzioni di ritorno

| Superficie | Convenzione |
|---|---|
| Operazioni | `*ops.Error{Code, Message, Details}`; il messaggio è il testo utente (a volte italiano) e va mantenuto verbatim |
| HTTP | `{"detail": "<message>"}`; validazione richiesta/JSON non-oggetto → 422 con detail a lista |
| JSON-RPC | codici fissi per `ApiError.code` (vedi `megachatd/CONTRACTS.md` §2) |
| `SocketClient.call` | `ApiError` per errori del server; `ServerUnavailable`/`OperationTimeout`/`IncompatibleServer` per il trasporto |
| Allegati/blob | `BlobOut{path, content_type, size, version, immutable}`; path assoluto lato server |
| Download temporaneo | URL completo oppure errore; porta 10042 |
| `contacts.address_book` | risultato parziale ammesso, errori per protocollo nel payload |
| Config | default senza raise; le integrazioni abilitate ma incomplete fermano l'avvio con codice 1 |

## 11. Quote media (reply con immagine)

Contratto della reply a un messaggio media (bug #37, fix V2 + piano B).

### 11.1 Display vs filo

- **Display** (bolla quote, reply bar): per un media senza caption la `quote_text` è un segnaposto tipizzato canonico — `MEDIA_QUOTE_PLACEHOLDERS = {image: "🖼️ Immagine", sticker: "🎨 Sticker", attachment: "📎 File", audio: "🎵 Audio", video: "🎬 Video"}`; con caption/filename reale vince il dettaglio (`media_quote_placeholder(msg_type, detail)`). I segnaposto sono **display-only**: non devono mai viaggiare come `quoteMessage` Signal.
- **Filo**: il body fedele viaggia nella chiave separata `quote_wire_body` di `_reply_to` (caption reale o `""`) e — per Signal — nei parametri `quoteTimestamp`/`quoteAuthor`/`quoteMessage` + `quoteAttachments`.
- Predicato `is_media_quote_placeholder(text)`: riconosce SOLO i 5 valori canonici (mai la forma composta); usato dal retry.

### 11.2 Ingresso

| Protocollo | Come arriva la quote |
|---|---|
| Signal | `dataMessage.quote`: testo reale se presente; altrimenti placeholder tipizzato da `quote.attachments[0].contentType`, con prefisso filename se noto |
| WhatsApp / Telegram | quote nativa lato server via `reply_to` (id del messaggio); il testo citato locale è letto dallo store |

Rendering locale: bolla `▎ <testo>` sopra il messaggio (classi `msg-quote` / `msg-quote-right`).

### 11.3 Uscita (creazione dalla TUI)

1. **Cattura target**: Alt+click o Alt+R su un'immagine → `ImageWidget.ReplyRequested(...)` → `_reply_to` con `quote_wire_body` (caption), `attachment_id`, `content_type`. La reply bar mostra il testo di display.
2. **Guardie pre-bolla**: WhatsApp senza `reply_to_message_id` → rifiuto; Telegram con id assente/non intero positivo → rifiuto.
3. **Invio Signal**: `messages.send` porta `quote_message = quote_wire_body or ""` (mai il segnaposto, mai omesso); se la riga ha un mime noto (`content_type` persistito) il server costruisce `quoteAttachments` risolvendo l'allegato locale — è ciò che rende VISIBILE la thumbnail sul destinatario. File locale assente → descrittore `"contentType"` da solo (quote senza thumbnail).
4. **WhatsApp / Telegram**: nessun testo di quote sul filo — solo `reply_to`.
5. **Persistenza e retry**: la riga inviata salva `attachment_id` + `content_type` (e i campi `quote_attachment_*`); il retry di una reply fallita ricostruisce `reply_data` dalla riga (mime marker → `quote_wire_body` = caption o `""`). Righe legacy senza mime (`content_type NULL`) degradano al comportamento V2: quote testuale corretta, senza thumbnail.

Test: `internal/proto/signal/*quote*_test.go`, `internal/store/media_quote.go`, test di vista della TUI (`tests/test_quote_widget.py`, `tests/test_reply_media.py` dove ancora presenti).
