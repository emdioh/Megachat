# Megachat wire contract

Reference for the Go server (`megachatd`). The TUI (`tui/` plus `client/`) and
the web SPA (`web/static/`) are its clients and speak this contract; every byte
documented here is the current protocol. Sources: `internal/api` (operations,
models, routes, events), `internal/ops` (registry), `internal/rpc` (Unix
socket), `internal/web` (HTTP/WS), `internal/core`, `internal/store`,
`internal/proto/*`, and `client/socket_client.py` for the client half.
Type and field names below are the **legacy Python names**, kept on the wire for
compatibility; the machine-checked Go twin is `internal/api` (`Ops`, `Routes`,
struct tags).

Contents: 1 Architecture - 2 Errors - 3 Socket transport - 4 Operations -
5 Bus events - 6 HTTP transport - 7 REST routes - 8 WebSockets - 9 Config and files -
10 Quirks to preserve.

---

## 1. Architecture

One registry of **68 operations** (`internal/api/opnames.go`, validated in
`internal/api/ops.go`). Each is `(api, request) -> response`; adapters only translate:

* **Unix socket** (TUI): JSON-RPC 2.0, method = operation name (section 3).
* **HTTP** (SPA): REST routes that call the same operations (section 7), plus
  two WebSockets (section 8).
* **Event bus**: one in-process bus (ring of 2000 events, per-subscriber queue
  1000). Both adapters stream it (section 5).

Request validation is **lax** unless a field is documented strict: `"10"` is
accepted for an int, `"true"` for a bool, unknown members are ignored. A
validation failure is always `invalid_request` / `"Invalid request"` (details =
the field errors). Result serialisation never drops nulls, and a list/dict result
is never `null`.

Operation behaviours that look like sync vs async do not matter to the wire; what
matters is: they may block for seconds (timeouts in 3.4), they outlive a timed-out
or disconnected caller (never cancelled), and the same op may be called concurrently.

---

## 2. Errors

### 2.1 Neutral codes (`ApiError.code`)

`invalid_request`, `not_found`, `unauthorized`, `forbidden`, `too_large`, `conflict`,
`unprocessable`, `not_supported`, `backend_down` (backend missing/unavailable),
`backend_error` (backend answered but failed), `internal`. The *message* is exactly
user-facing (the SPA shows `detail`, the TUI shows `message`); keep the texts
verbatim (some are Italian). `details` is optional extra JSON.

| code | HTTP | JSON-RPC |
|---|---|---|
| invalid_request | 400 | -32602 |
| unauthorized | 401 | -32002 |
| forbidden | 403 | -32003 |
| not_found | 404 | -32001 |
| conflict | 409 | -32005 |
| too_large | 413 | -32004 |
| unprocessable | 422 | -32006 |
| not_supported | 501 | -32007 |
| internal | 500 | -32603 |
| backend_error | 502 | -32009 |
| backend_down | 503 | -32008 |

(`api.HTTPStatusFor`, `api.RPCCodeFor`.)

### 2.2 Shapes

* HTTP: `{"detail": "<message>"}` (`HTTPError`). Details are dropped.
  FastAPI's own validation 422 (bad query/path param, JSON body that is not an
  object) is `{"detail":[{"type","loc":["query","proto"],"msg","input",...}]}`
  (`HTTPValidationError`); any list-shaped detail is acceptable but the status must
  be 422.
* JSON-RPC: `{"jsonrpc":"2.0","id":ID,"error":{"code":N,"message":M,"data":{"code":
  "<neutral>","message":M,"details":D}}}`. `data.details` is always present for
  errors that come from an operation (may be `null`). For `invalid_request` it is
  a list of `{"type","loc","msg"}` (pydantic `type`/`loc`/`msg`, e.g.
  `{"type":"missing","loc":["protocol"],"msg":"Field required"}`); the offending
  input is stripped. Protocol-level errors below have no `details`.

### 2.3 Protocol-level JSON-RPC errors

| code | when | message | data |
|---|---|---|---|
| -32700 | line is not valid UTF-8/JSON | `Parse error` | none; id `null`; connection stays open |
| -32600 | batch (array) | `Batch requests are not supported` | |
| -32600 | not an object | `Invalid request` | |
| -32600 | `id` present but bool/null/float/other | `id must be a string or an integer` (id echoed as null) | |
| -32600 | `jsonrpc != "2.0"` | `jsonrpc must be '2.0'` | |
| -32600 | `method` not a string | `method must be a string` | |
| -32600 | `timeout` not a number in (0, 600] | `timeout must be a number in (0, 600]` | |
| -32602 | `params` present and not an object/null | `params must be an object` | |
| -32601 | unknown method | `Unknown method '<m>'` | `{"code":"method_not_found","message":...}` |
| -32020 | call exceeded its time limit | `<m> timed out after <t:g>s` | `{"code":"timeout","message":"<m> timed out"}` |
| -32030 | >64 requests in flight | `Too many requests in flight` | id echoed if valid str/int |
| -32031 | 33rd connection | `Too many connections` | id `null`, then connection closed |
| -32004 | line > 16 MiB | `Request line exceeds 16777216 bytes` | `{"code":"too_large","message":"Request line too large"}`; id null; half-close, discard input up to 1 s / 16 MiB, close |
| -32603 | unexpected exception | `Internal error` | `{"code":"internal","message":"Internal error"}` |

Notification rule: a request without `id` is executed and **never answered**, except
that envelope errors (-32600/-32700) are still sent (with id null).

---

## 3. Unix-socket transport

### 3.1 Location, permissions, runtime file

* Path: `--socket PATH`, else `$MEGACHAT_SOCKET` (with `~` expansion), else
  `$XDG_RUNTIME_DIR/megachat/server.sock` (only if absolute), else
  `~/.local/state/megachat/server.sock` (`api.DefaultSocketPath`). The absolute
  path must be <= 103 bytes (else startup error). `--no-socket` disables it.
* Parent dir is created `0700`; a pre-existing dir owned by another uid is an
  error; if it is group/other accessible and was created now or is named
  `megachat` it is `chmod 0700`, otherwise only a warning. The socket is bound
  under `umask 0177` then `chmod 0600`.
* On Linux each accepted connection is checked with `SO_PEERCRED`; a peer with a
  different uid than the server's euid is dropped silently. (`-1` = lookup failed
  -> refuse; platforms without it rely on permissions.)
* Startup with an existing path: not a socket or other owner -> refuse; a socket
  nobody accepts on -> unlink it and the runtime file; a socket that accepts ->
  refuse ("another server is already listening", exit 1).
* Runtime file `server.json` next to the socket (`server.sock` -> `server.json`,
  mode `0600`, written atomically via temp file + rename **after** the socket
  listens, removed on shutdown only if still ours; `RuntimeFile`):
  `{"pid":int,"socket":str,"version":"0.2.1 (abc1234)","api_version":1,"started_at":float}`.
  The Python client uses `pid`/`started_at` to detect a server restart.
  `version` (also `rpc.hello` `server_version`, `GET /health` `version`, `megachatd version`) is the release
  version embedded from `megachatd/VERSION` (or `-ldflags "-X main.Version=..."`), followed by the short git
  commit when the binary was built in a checkout: `"0.2.1 (abc1234)"`. Informational: clients must not parse it.
* Single-instance lock: `/tmp/signal-tui.lock` (or `$MEGACHAT_LOCK_FILE`), holds the
  pid, guarded by `flock` on `<lock>.guard`.

### 3.2 Framing

Newline-delimited JSON, UTF-8, one object per line. The server writes compact
JSON (`separators=(",",":")`), **ASCII only** (`ensure_ascii`: non-ASCII as
`\uXXXX`, surrogate pairs for astral chars) and `allow_nan=False`. A blank line
is ignored. A partial last line at EOF is dropped; if the peer half-closes, the
requests already received are still answered, then the server closes. Request
lines up to 16 MiB. Write timeout 30 s (a stuck reader is dropped). Max 32
connections, max 64 in-flight requests per connection; requests of one connection
run concurrently and may be answered in any order (match by `id`).

### 3.3 Envelope

Request: `{"jsonrpc":"2.0","id":<str|int>,"method":"<op>","params":{...},"timeout":<sec>}`
(`RPCRequest`). `params` absent/null = `{}`. `timeout` is a non-standard member:
server-side limit in seconds, `0 < t <= 600`, default 120. Success:
`{"jsonrpc":"2.0","id":ID,"result":<json>}`. The result is the operation's response
serialised through its response type (exactly the bodies in section 4).

A timeout does **not** cancel the operation: its result is discarded and the caller must
treat a timed-out mutation as unknown. Do not tie op execution to the connection.

### 3.4 Methods

Built-ins (cannot be shadowed by an operation):

| method | params | result |
|---|---|---|
| `rpc.hello` | - | `{"api_version":1,"server_version":"0.2.1 (abc1234)","operations":[sorted names]}` (`HelloResult`). Clients call it first and refuse an `api_version` they do not know (`IncompatibleServer`). |
| `server.ping` | - | `{"pong":true,"ts":<float>}` |
| `events.subscribe` | `{"since"?:int>=0}` | `{"subscribed":true,"last_seq":int}`; error -32602 `since must be an integer >= 0` (bool/negative/non-int); `not_supported` "This server has no event bus" without a bus |
| `events.unsubscribe` | - | `{"subscribed":false}` (idempotent) |

`events.subscribe` semantics: one subscription per connection; a second call restarts
the stream (old subscription closed). After the response line is written the server
starts writing notifications (never before). Events with `seq > since` still in
the ring are replayed first (`since` > last seq is clamped to last seq); if `since < oldest_seq - 1`
a `gap` event `{"reason":"buffer","since":N}` (envelope seq = bus last seq) precedes the
replay. Without `since` the stream is live only. A subscriber whose queue (size
max(1000, ring size)) overflows has its queue **cleared** and gets a single
`gap {"reason":"overflow"}` (seq = the event that overflowed); the event
pump writes one event at a time and waits for the write, so a slow reader backs up into the bus subscription.

Notifications (no `id`):

```
{"jsonrpc":"2.0","method":"event","params":{"seq":12,"type":"message","payload":{...},"ts":1700000000.5}}
{"jsonrpc":"2.0","method":"heartbeat","params":{"ts":1700000000.5}}   // idle > 5 s
```
(`EventNotification`, `HeartbeatNotification`; the event `params` equals the /ws/v2 frame.)

Client-side behaviour you must stay compatible with (`SocketClient`): liveness
timeout 15 s without any byte (3 heartbeats); an idle un-subscribed client pings
every 5 s; reconnect with backoff 0.1-5 s and re-subscribe `since=<last seq seen>`;
if `last_seq` goes backwards or `server.json` pid/`started_at` changed it synthesises
`gap {"reason":"restart"}` itself. The synthetic `client_connection` event is never sent
by the server. Every other registered operation is a method named by the registry
(section 4), plus the attachment staging rule below.

### 3.5 Attachment staging (`messages.send`, `gmail.messages.send`)

Socket clients send **absolute local paths** in `attachments[].path`. Before the
operation runs, the adapter copies each file ("copy, do not trust"):

1. `slots = attachments` validated as `list[AttachmentIn]` (else invalid_request).
2. Run the pre-check op (`messages.send.check` / `gmail.messages.send.check`) with the
   params minus `attachments` plus `attachment_count=len(slots)` and
   `slots_ok = all(slot.path or slot.rejected)`; its error is returned as the call's error
   before any file is touched.
3. For each slot with a path: filename defaults to `basename(path)`; open with
   `O_NOFOLLOW|O_NONBLOCK`, must be a regular file (no symlink as last component,
   FIFO, device, dir), path absolute; copy into `<CACHE_DIR>/web-uploads/` (0600) while
   applying the size/kind guard (`validate_upload`: sniffed kind, per-kind limit
   image 20 MiB / video 100 MiB / audio 50 MiB / document 50 MiB, extension coherent with
   MIME), total <= 250 MiB (`MAX_STAGED_TOTAL_BYTES`). Gmail: no media policy, per file
   <= 25 MiB. Too large -> `too_large` "Upload too large"; any other failure
   (including non-absolute path / OSError) -> `invalid_request` "Invalid request".
   Slots with empty path / `rejected` are passed through.
4. The operation receives the copies; they are deleted when the call ends.
5. `messages.send.check` / `gmail.messages.send.check` called directly over the socket
   have their `attachments` emptied (no client path reaches them).

The HTTP adapter never accepts `attachments` from a body: it overwrites the field with
files it stored itself (section 7.2).

---

## 4. Operations

Param/result types are in `internal/api` (`requests.go`, `responses.go`, `ops.go`).
"Clients": T = Textual TUI over the socket, W = web SPA through REST (route in section 7),
- = no client uses it (still callable over the socket).
Every operation additionally fails with `invalid_request` "Invalid request" (details = pydantic
errors) when its params do not validate. (An unknown operation name is JSON-RPC -32601 on the socket; the
HTTP adapter only calls registered names.)
Pydantic "Stripped" string fields (`str(value or "").strip()`): `ChatsMarkReadRequest.*`,
`ChatsArchiveRequest.protocol/contact_id`, `ContactsForProtocolRequest.protocol`,
`ContactsRegisterRequest.id/protocol`, `ContactsCheckNumberRequest.phone`.

### 4.1 server / meta

| op | params -> result | notes / errors | cl |
|---|---|---|---|
| `server.status` | `{}` -> `{"signal":bool,"whatsapp":bool,"telegram":bool}` (`ServerStatus`) | `is_connected` of each backend; unregistered = false | T W |
| `server.config` | `{}` -> `ServerConfigOut {thumbnail_max_lines(12), thumbnail_max_cols(60), picker_max_results(50), picker_preferred_backend(""), wa_lazy_media(true)}` | values from config (section 9); the wire field names are unchanged by the nested schema (`thumbnail.max_lines`, `thumbnail.max_cols`, `picker.max_results`, `picker.preferred_backend`, `whatsapp.lazy_media`) | T |
| `server.clients` | `{}` -> `{"connections":N,"self_included":bool,"web":N}` | connections = socket count incl. caller (`self_included` true when the socket adapter exists); web = browser WebSockets (`/ws` + `/ws/v2`) | T |
| `server.shutdown` | `{}` -> `{"status":"ok"}` | answers at once, then ~0.2 s later stops the server as SIGINT does (the socket adapter flushes pending output before aborting connections). `not_supported` "This server cannot be shut down remotely" if no hook | T |
| `server.client_log` | `{message?:any, source:"client"}` -> `{"status":"ok"}` | logs `[source] str(message or "")[:500]` at INFO. Web route forces source `web-client` | W |
| `server.logs` | `{after:0, limit:500, level:""}` -> `ServerLogsOut {records[], last_seq, since}` | ring buffer of 2000 records `{seq,time,level,logger,message}`; `limit` outside 1..2000 -> 500; `after` clamped >= 0; `level` is a logging level name (case-insens., unknown -> 0 = all) filtering `>=`; returns the **last** `limit` matches; `last_seq` = last returned seq else `max(after,0)`; `since` = buffer install time (float) | W |
| `server.logs_clear` | `{}` -> `{"status":"ok"}` | | W |
| `emoji.list` | `{}` -> `[EmojiCategoryOut{category,icon,emojis[],aliases{emoji:shortcode}}]` | static data from `emoji_data.PREDEFINED_CATEGORIES`; `aliases[char]` = English CLDR name without colons (`""` if none) | W |
| `reactions.available` | `{protocol}` -> `{"emojis":[...]}` | Telegram only (backend `get_available_reactions`); every other protocol/failure -> `[]` | W |

### 4.2 contacts

| op | params -> result | notes / errors | cl |
|---|---|---|---|
| `contacts.list` | `{q?}` -> `[ContactOut]` | active chats; `ContactOut{id,display_name,protocol,extras,last_message_ts,unread}`; `last_message_ts = int(extras.last_message_ts or 0)`; `unread` = count of `messages` rows `is_mine=0 AND read=0` summed over all storage keys of the contact (`contact_storage_keys`); `q` = case-insens. substring over name/id/phone; runs WhatsApp `reconcile_unread` first (best effort) | W |
| `contacts.book` | `{q, force:false}` -> `[ContactOut]` | blank q -> `[]`; searches the aggregated address book (slow) | W |
| `contacts.for_protocol` | `{protocol}` -> `[ContactWire]` | snapshot of backend contacts; `backend_down` "Backend unavailable" if no backend | T |
| `contacts.register` | `ContactsRegisterRequest{id,display_name,protocol,extras?}` -> `ContactWire` | open-or-create ghost: sets `extras.ghost=true`, `backend.register_contact`; `invalid_request` if protocol unknown or id blank; `backend_down` | T |
| `contacts.address_book` | `{protocols?:[...], force:false}` -> `{contacts:[ContactWire],errors:{protocol:message}}` | starts the WhatsApp LID resolver first; never fails for a bad backend | T |
| `contacts.check_number` | `{phone}` -> `{"exists":bool\|null}` | WhatsApp (whatsmeow `IsOnWhatsApp`); null when unknown/blank/no backend/failure | T |
| `contacts.avatar` | `{protocol,contact_id}` -> `BlobOut` | `not_found` "Not Found" (unknown protocol), `invalid_request` "Invalid request" (blank id), `not_found` "No profile picture" | W |

`ContactWire` = `{id,display_name,protocol,cache_key("<protocol>:<id>"),last_message_ts(0),extras}`;
`extras` is arbitrary JSON (set -> sorted list, Path/unknown -> str, bytes -> base64);
well-known keys: `phone` (E.164 no `+`), `last_message_ts` (ms), `address_book`,
`ghost`, `lid`, `is_group`, `archived`.

### 4.3 chats / messages

| op | params -> result | notes / errors | cl |
|---|---|---|---|
| `chats.mark_read` | `{protocol,contact_id}` -> `{"status":"ok"}` | `invalid_request` if protocol not in signal/whatsapp/telegram or id blank. Backend failure is logged and swallowed (always ok). Persists `read=1` in SQLite | T W |
| `chats.archive` | `{protocol,contact_id,archived:strict bool}` -> `{"archived":bool}` | only `whatsapp`; `invalid_request`; `backend_down` "Backend unavailable"; `backend_error` "Archive failed" | W |
| `messages.page` | `{protocol(Literal),contact_id,refresh:false,limit?,since?}` -> `[MessageRow]` | see 4.3.1. `invalid_request` "Invalid limit" if limit not in 1..2000 | W |
| `messages.window` | `{protocol,contact_id,refresh:false,refresh_limit:50 (1..200)}` -> `{"messages":[CacheMessage],"refreshed":bool}` | core cache rows (TUI shape, includes unpersisted rows, WhatsApp union of storage keys); refresh downloads remote history first; `[]`,false for unknown backend | T |
| `messages.snapshot` | `{protocol}` -> `{contact_id:[CacheMessage]}` | every chat of the protocol from the cache; WhatsApp chats appear once under canonical id | T |
| `messages.edit` | `MessagesEditRequest{protocol,contact_id,message_id,new_text}` strict strs -> `{"ok":true}` | 4.3.3 | T W |
| `messages.send` | `MessagesSendRequest` -> `{"ok":true,"message_ids":[...]}` / dup / optimistic | 4.3.2 | T W |
| `messages.send.check` | `MessagesSendCheckRequest` -> `{"ok":true}` | pre-validation, sends nothing (adapter use, section 3.5) | - |
| `reactions.set` | `{protocol,contact_id,message_id,emoji}` strict -> `{"ok":true,"reactions":[Reaction]}` | 4.3.4 | W |
| `media.*`, `download.*` | see 4.4 | | |

#### 4.3.1 `messages.page` rows (`MessageRow`)

Oldest first, from SQLite (columns `id,msg_id,text,is_mine,timestamp,contact_number,
sender,attachment_id,attachment_info,content_type,media_kind,protocol,msg_type,
quote_text,quote_timestamp,quote_author,quote_attachment_id,quote_content_type,
quote_attachment_path,status,edited,read,reply_to_message_id,batch_id,batch_index`).
Always-present members: `id` (`msg_id or str(row id)`), `text`, `direction` ("out" if
is_mine else "in"), `sender` (`""` if null), `timestamp`, `attachment` (object or null),
`quote_text`, `quote_timestamp`, `quote_author` (see below, always a string),
`quote_attachment_id`, `quote_content_type`, `quote_thumb_url`, `quote_media_placeholder`
(bool), `status` (`row.status or "sent"` for outgoing, **null** for incoming), `read`,
`edited`, `edit_id`, `batch_id`, `batch_index`. Conditional members (omitted otherwise):
`media_placeholder` (string), `system: true`, `reactions: [Reaction]` (only when
non-empty), `is_group: true` (group chats only).

* `attachment` = `{attachment_id, name, type, media_kind[, version][, pending:true]}`;
  `name = attachment_display_name(attachment_info, attachment_id)`;
  `type = content_type or ("image/*" if msg_type=="image") or inferred from an image extension`
  (null otherwise). `version = "<mtime_ns>-<size>"` of the local file (cache buster,
  best effort, omitted on any error). WhatsApp lazy media (`whatsapp.lazy_media`): a media row
  without `attachment_id` gets `{attachment_id:<lazy token>,name:<label>,type:<content_type or
  "image/*"|"video/*"|"audio/*">,media_kind,pending:true}` or, with no resolvable token,
  `media_placeholder` = "Immagine"/"GIF"/"Video"/"Audio"/"Messaggio vocale"/"Sticker"/"Documento"/"Allegato".
* Caption cleanup: for image rows and other media the `text` is blanked when it equals the
  attachment info / a typed media placeholder (`MEDIA_QUOTE_PLACEHOLDERS`) / WhatsApp synthetic
  media text; `attachment_info` is used as caption when it looks like one. See
  `internal/messages` (the Go implementation; treat its output as normative).
* `quote_thumb_url` (relative URL, no leading slash): `api/quote-media/<proto>/<row id>?w=96`
  when `quote_attachment_path` lives in `<CACHE_DIR>/quote-thumbs`; else
  `api/media/<proto>/<url-quoted path segments>?w=96` for an existing image/video path, or from
  `quote_attachment_id` + image/video content type or extension, or a fallback search of the
  quoted media in the same chat (timestamp, filename in `quote_text`, `reply_to_message_id`); else null.
* `quote_media_placeholder` = `quote_text` is a typed media placeholder (or "filename - placeholder" form).
* `quote_author`: 1:1 chats -> always `""`; group chats -> resolved display name (via the
  address book; falls back to the sender of the quoted message); `sender` is also resolved in groups.
  A chat is a group if the id ends with `@g.us` or the known contact has `extras.is_group`.
* Dedup: cross-key (WhatsApp `@c.us`/`@lid` twins, canonical key first) and intra-key; `since` keeps
  `timestamp >= since`; `limit` keeps the newest N (`ORDER BY timestamp DESC, id DESC LIMIT N+50`
  then trim after dedup).
* History refresh: when `refresh` is true or the protocol is telegram, `fetch_history(canonical_id, 20)`
  is called first (errors swallowed). A WhatsApp ghost id is canonicalised through
  `backend.find_contact`.

#### 4.3.2 `messages.send`

Params (`MessagesSendRequest`): `protocol`, `contact_id`, `text` (strict str|null), then lenient
`client_msg_id, quote_timestamp, quote_author, quote_message, reply_to_message_id,
quote_content_type, quote_attachment_id, batch_id` (any JSON, type-checked below),
`attachments:[AttachmentIn]`, `retry:true`, `optimistic?:OptimisticSend`.
Validation order (all `invalid_request` "Invalid request" unless stated; this is
`_prepare` and must be reproduced in order):

1. protocol not in {signal,whatsapp,telegram}; contact_id not a non-blank str.
2. text not a str or `len(text) > 65536`; no attachments and `text.strip()==""`.
3. `attachment_count > 10` -> "Too many attachments"; `slots_ok` false; `optimistic` with attachments.
4. `client_msg_id`: used if a non-blank str of <= 128 chars (stripped), else a fresh uuid4.
5. Quote fields: `quote_content_type`/`quote_attachment_id` are ignored (None) unless protocol is signal;
   str fields are stripped (blank -> None); `quote_message` is stripped (non-optimistic only) and
   becomes None if blank unless `quote_content_type` is set; `quote_timestamp` must be an int (not bool,
   not 1000.0); the string-ish fields must be str|null.
6. Telegram reply (`any quote_* / reply_to_message_id` set): `int(reply_to_message_id) > 0` required.
   WhatsApp reply: `reply_to_message_id` required.
7. Backend (`manager.get(protocol)`) missing -> `not_found` "Not Found"; `backend.find_contact(contact_id)`
   None -> `not_found` "Not Found".
8. Signal: `quote_attachment_id` must be an attachment id of that chat (via `messages._messages`) else
   invalid; `quote_content_type` without `quote_attachment_id` is invalid unless optimistic (then
   `quote_attachments=[content_type]`); with the id: resolved path -> `["<ct>:<name>:<path>"]` else `[<ct>]`.
9. Backend kwargs: `quote_timestamp, quote_author, quote_message[, quote_attachments]`;
   `reply_to_message_id` only for whatsapp/telegram (or optimistic).

Execution: a contact the backend does not list is registered with `extras.ghost=true`. Text sends
claim an idempotency entry keyed `(protocol, contact_id, client_msg_id)` (TTL: sent 600 s, inflight 120 s)
with a SHA-256 fingerprint of `{protocol,contact_id,text,quote_timestamp,quote_author,quote_message,
reply_to_message_id,quote_content_type,quote_attachment_id}` (JSON, sorted keys, compact,
`ensure_ascii=False`). Replay with the same fingerprint -> `{"ok":true,"duplicate":true}`; different fingerprint
-> `unprocessable` "Client message id conflict"; inflight -> `conflict` "Send in progress". Entry is
released on any failure. Attachment sends are neither deduplicated nor retried: files are validated in
order (first failure wins: `too_large` "Upload too large", `invalid_request` "Unsupported media type"),
sent through `send_attachments_sync(protocol, contact_id, paths, batch_id=<str or None>,
captions=[text or None, None...], mime_types, media_kinds, filenames, **kwargs)`; text captions the first
file. Text sends retry up to 3 attempts (1 only if `retry=false`) with delay `1.0*2^(attempt-1) + U(0,1)` s
for retryable errors (`classify_send_error`); before each retry a `send_retry` web-push frame is published
(section 8.3). Failure -> `backend_error` "Message send failed" (`not_supported` "Attachment send not
supported" for NotImplementedError on attachments). Success publishes web-push `message`
`{"protocol","contact_id","is_mine":true}` and returns `{"ok":true,"message_ids":[...]}`: the real backend ids
(text: `[id]` when the backend returned one, else `[]`; attachments: the ids returned by the attachment send,
empty ids dropped). The idempotent duplicate replay stays `{"ok":true,"duplicate":true}` with no `message_ids`.

Optimistic flavour (`optimistic.timestamp` = client ms timestamp = row identity): publishes nothing via
`sent_mirror`; seeds a `pending` cache row (`ingest_message`) and persists it **before** the network
send, bumps recency, publishes web-push `message` `{id,protocol,contact_id,timestamp,is_mine:true}` only if the row was
newly added, sends straight through `backend.send_message_sync`, then `pending -> sent` and attaches the server id. Result
`{"ok":true,"status":"sent","status_applied":bool,"message_id":str|null}`. On failure `pending -> failed` and
the error carries `details {"status_applied":bool,"error":str|null}`. `resend:true` re-arms a `failed` row
(`failed -> pending`, nothing seeded); if the row is not failed -> `conflict` "Message is not in a failed
state" with `details {"status_applied":false,"error":null}`.

#### 4.3.3 `messages.edit`

Validation: protocol known, `contact_id`/`message_id`/`new_text` non-blank, `len(new_text) <= 65536` ->
else `invalid_request`. Row lookup (cross-key union for WhatsApp) -> `not_found` "Not Found"; row without edit
id (not our own text message) -> `invalid_request` "Message not editable". Backend edit
(`manager.edit_message_sync`) falsy or raising -> `backend_error` "Message edit failed" with
`details {"error": "<exception text>"}` (details are only shown by local frontends). On success: SQLite update,
`backend.apply_edit(..., is_mine=True)` (errors swallowed), web-push `message_edit`
`{protocol,contact_id,message_id,timestamp,old_text,text,is_mine:true}` (timestamp = original row ts).

#### 4.3.4 `reactions.set`

`invalid_request` unless protocol known, ids non-blank, `emoji` non-empty, <= 8 chars and
`emoji.is_emoji`; `not_found` "Backend not found" / "Message not found"; Signal translates the id to the
row timestamp and `target_author` (our number if `is_mine` else the contact); `backend.send_reaction_sync(...)`
false/raise -> `backend_error` "Reaction send failed". Local aggregate via `backend.apply_reaction(contact_id, payload)`
(`mode` "snapshot" for telegram else "delta", `author_key:"me"`, `author:"You"`), then web-push
`reaction_update {protocol,contact_id,message_id,timestamp,reactions}`; result carries the aggregate
`reactions` (`[]` if none).

### 4.4 media / blobs / download / link preview

All blob ops return a `BlobOut {path, content_type|null, size|null, version|null, immutable}`:
`path` is an absolute path on the server machine (HTTP streams it, the TUI reads it directly), `version` =
`"<st_mtime_ns>-<st_size>"`, `immutable` true for generated thumbnails.

| op | params -> result | notes / errors | cl |
|---|---|---|---|
| `media.attachment` | `{protocol,attachment_id}` -> `BlobOut` | resolved through the backend (lazy download), file must be a regular file inside the protocol's media root (symlinks/`..` resolved) else `not_found` "Not Found"; unknown protocol -> `not_found` "Not Found". `.webm` is audio only if the persisted media_kind says so | T W |
| `media.thumbnail` | `{protocol,attachment_id,width}` -> `BlobOut` | width in {96,240,480} -> JPEG thumbnail (`immutable:true`, content-addressed cache); other width, unsupported/animated image -> the original blob; videos via ffmpeg (WhatsApp/Telegram: head/tail chunk thumbnail); `unprocessable` "Video thumbnail unavailable" | W |
| `media.quote` | `{protocol,message_row_id,width?}` -> `BlobOut` | file from `messages.quote_attachment_path` confined to `<CACHE_DIR>/quote-thumbs` (parent == root) else `not_found`; width in 96/240/480 -> thumbnail; `internal` "Database error" | W |
| `media.quoted_attachment` | `{protocol,contact_key,quote_timestamp?,reply_to_message_id?,quote_text:"",content_type?}` -> `{"attachment_id":str\|null}` | attachment id of the image a reply quotes (exact timestamp match first, then +-2 s etc., see `internal/media/paths.go::QuotedAttachmentID`) | T |
| `download.serve_attachment` | `{protocol,attachment_id}` -> `{"url"}` | serves the file on the temporary HTTP server (port 10042, one file at a time, URL `http://<ip>:10042/<name>`); `not_found` "ERROR: Attachment file not found (id=<first 80 chars>)"; failures `backend_error` with the `ERROR: ...` string as message | T |
| `download.serve_text` | `{text,filename:"message.txt"}` -> `{"url"}` | filename must be a plain name (non-empty, not `.`/`..`, no `/`, no NUL) else `invalid_request`; `backend_error` "ERROR: Cannot serve file: ..." | T |
| `links.preview` | `{url}` -> `LinkPreviewOut{url,title,description,site_name,has_image}` | SSRF-guarded Open Graph fetch (`internal/media/linkpreview.go`); `invalid_request` "URL not allowed"; `not_found` "No preview" | W |
| `links.preview_image` | `{url}` -> `BlobOut` | only the raster image advertised by the page's own preview (bytes sniffed, no SVG), stored content-addressed; `invalid_request` "URL not allowed"; `not_found` "No preview image" | W |

### 4.5 device link

State machine in `internal/linking/service.go` (passive: the frontend polls every ~2 s; sessions abandoned for 900 s
are reaped; `QR_REFRESH_AFTER_S`=60). `LinkQrOut{kind: url|png|text|info|error, data}`.

| op | params -> result | notes / errors | cl |
|---|---|---|---|
| `link.status` | `{}` -> `{"protocols":{p:{"available":bool,"account":str,"linked":bool\|null}}}` | all three protocols; no I/O; signal `linked=bool(account)`, whatsapp `linked=null`, telegram `linked=null` if absent else `not needs_pairing` | T W |
| `link.required` | `{}` -> `{"protocols":{"whatsapp":bool}}` | WhatsApp session guard says a new QR is needed | W |
| `link.start` | `{protocol(Literal),phone:"",device_name:"Signal-TUI-Client"}` -> `{"qr":LinkQrOut}` | cancels a link already open for the protocol; Signal spawns `signal-cli link -n <device_name>` and extracts the `sgnl://` URL; WhatsApp starts a whatsmeow QR pairing; Telegram starts the QR login. `backend_down` (protocol unavailable), `backend_error` (message shown to the user) | T W |
| `link.poll` | `{protocol}` -> `{"state":"waiting"\|"finalize"\|"done","needs_2fa":bool,"qr":LinkQrOut\|null}` | `conflict` "No link in progress"; signal -> `finalize` when the subprocess exited 0 (else waiting); whatsapp -> `done` when the session is linked/working with an identity; telegram -> needs_2fa once when a password is required; QR renewed when older than 60 s | T W |
| `link.finalize` | `{protocol:"signal",phone:""}` -> `{"account":str}` | resolves the new account via `signal-cli listAccounts` and persists `signal.user_number` to config.json (atomic temp file + rename, other keys preserved; refuses an invalid existing config.json, or `SIGNAL_USER_NUMBER` selecting a different account) | T W |
| `link.submit_2fa` | `{password}` -> `{"ok":bool}` | Telegram; `ok:false` = wrong password; never log the password | W |
| `link.cancel` | `{protocol}` -> `{"ok":true}` | kills the subprocess; idempotent | T W |
| `link.reconnect` | `{protocols:[...]}` -> `{"ok":true}` | non-blocking reconnect of those backends; `not_supported` "This server cannot reconnect backends" | T W |

Mapping of link errors: `NoLinkInProgress` -> `conflict`, `LinkUnavailable` -> `backend_down`, any other
exception -> `backend_error` with `str(exc)`.
**HTTP only:** `/api/link/start` and `/api/link/poll` convert a `qr` of kind `url`/`text` into
`{"kind":"png","data":<base64 PNG>}` (QR error-correction M, box 8, border 2); the socket answers the raw kind.

### 4.6 transcription

Messages are Italian. `backend_down` "Trascrizione non configurata" = no API key for the selected provider.

| op | params -> result | notes / errors | cl |
|---|---|---|---|
| `transcribe.config` | `{}` -> `{"enabled":bool}` | service exists (selected provider has a key) | W |
| `transcribe.status` | `{protocol,attachment_id}` -> `TranscribeStatusOut` or `{"status":"unknown"}` | stored record `{status:"ok"\|"pending"\|"error",text,error,model,updated_at}`; `backend_down` | W |
| `transcribe.submit` | `{protocol:any,attachment_id:any}` -> `{"status":"done","text"}` / `{"status":"pending"}` | `invalid_request` (protocol not str/known, attachment_id not str/blank after strip); status `ok` record -> done; `pending` -> pending; else resolves the file via `media.attachment` (`not_found` "Allegato non trovato") and queues it | W |
| `transcribe.set_api_key` / `config.set_openai_key` | `{provider,api_key}` / `{api_key}` -> `{"status":"ok"}` | `invalid_request` "Provider non valido" / "Chiave non valida" (not str, blank, any whitespace, > 500 chars; openai must start `sk-`, openrouter `sk-or-`, gemini >= 20 chars); `internal` "Errore di salvataggio" / "Errore di inizializzazione". Writes `config.json` `<provider>.api_key` and rebuilds the service | W |
| `transcribe.settings` | `{}` -> `TranscribeSettingsOut` | never contains a key; `providers.<name>` = `{api_key_set,model,base_url}` (+`http_referer`,`app_title` for openrouter); top-level `api_key_set/model/base_url` = selected provider's; `language` "" if unset; `timeout` default 120; `env_overrides` | W |
| `transcribe.set_settings` | `TranscribeSettingsRequest` (partial) -> `TranscribeSettingsOut` | only keys present are applied (`model_fields_set`); top-level `model`/`base_url` go to the provider selected by this request's `provider` else the current; validators and exact messages in `internal/transcribe/settings.go::validatedSettings` (`Provider non valido`, `Modello non valido`, `Modello troppo lungo (massimo 100 caratteri)`, `Modello Gemini non valido (es. gemini-flash-latest)`, `Modello OpenRouter non valido (formato fornitore/modello)`, `URL non valido`, `URL non valido: deve iniziare con http:// o https://`, `Referer non valido...`, `Titolo non valido...`, `Lingua non valida...` (`[a-z]{2,3}` or empty), `Timeout non valido` (int 5..600, not bool)); `""` for language/base_url restores the default | W |
| `transcribe.diagnostics` | `{}` -> `TranscribeDiagnosticsOut` | no key; `recent` = last 10 records; `[{"status":"db_error","error":...}]` if the store fails | W |
| `transcribe.check` | `{}` -> `TranscribeCheckOut` | GET to the provider (openrouter `<base>/key` Bearer, gemini `<base>/models?pageSize=1` `x-goog-api-key`, openai `<base>/models` Bearer; timeout 20 s); missing key -> `{"ok":false,"provider","detail":"Chiave API mancante"}`; network error -> `{"ok":false,"provider","url","detail":"Errore di rete: <Type>: <msg>"}`; else `{"ok":status==200,"provider","url","http_status","detail":<redacted first 500 chars>}` | W |
| `autotranscribe.config` | `{}` -> `{"configured":bool,"globals":{whatsapp,signal,telegram}}` | | W |
| `autotranscribe.set_global` | `{protocol,enabled:strict bool}` -> `{whatsapp,signal,telegram}` bools | writes `config.json` `transcription.auto.<protocol>`; `internal` "Errore di salvataggio" | W |
| `autotranscribe.chat` | `{protocol,contact_id(min 1)}` -> `{"global","override":bool\|null,"effective"}` | override from `transcription.auto_chats["<protocol>:<contact_id>"]` | W |
| `autotranscribe.set_chat` | `{protocol,contact_id,enabled:bool\|null}` -> same as `.chat` | null removes the override; `enabled` must be present | W |

### 4.7 Gmail

All but `gmail.image` and `gmail.messages.get` block on IMAP/SMTP. Error mapping (`internal/gmail/ops.go::mapErr`):
not configured -> `backend_down` "Gmail non configurato"; validation -> `invalid_request` (413 -> `too_large`) with
the validator's Italian text; not found -> `not_found` "Risorsa non trovata"; upstream/transport ->
`backend_error` "Errore di comunicazione con Gmail"; anything else -> `backend_error` "Errore Gmail". Credentials
and upstream detail never reach messages or logs. The IMAP/SMTP logic itself is `internal/gmail/` (not restated here).

| op | params -> result | notes |
|---|---|---|
| `gmail.status` | `{}` -> `GmailStatusOut{configured,address,folder,connected:null,limits{...}}` | no network; `address` "" unless configured |
| `gmail.unread` | `{}` -> `{"configured":bool,"unread":int}` | not configured -> `{false,0}`; else the unread watcher's last value or one `fetch_unread_status` |
| `gmail.messages.list` | `{folder?,offset:0,limit:50,refresh:false}` -> `GmailListOut` | window = newest 200 UIDs sorted `date_ts` desc (None last) then uid; cached per (folder, account) for 15 s (`_GMAIL_LIST_CACHE_TTL_S`), `refresh` bypasses |
| `gmail.messages.get` | `{uid,folder?}` -> `GmailMessageDetail` | sanitized HTML with remote images rewritten to `gmail-image?token=<HMAC>`; also prefetches those images in the background (`GMAIL_IMAGE_PREFETCH=0` disables) |
| `gmail.messages.delete` | `{uid,folder?}` -> `{"ok":true,"trashed":true}` | move to Trash; `not_found` if gone |
| `gmail.messages.bulk_delete` | `{uids:any,folder:any}` -> `{"ok":true,"trashed":int,"missing":[int]}` | `uids` list of 1..200 ints (bool rejected, deduped) else `invalid_request` ("Lista UID non valida", "Lista UID vuota", "Troppi UID (max 200)", "UID non valido"); `folder` non-str -> "Cartella non valida" |
| `gmail.messages.mark_read` / `mark_unread` / `archive` | `{uid,folder?}` -> `{"ok":true}` | each pokes the unread watcher |
| `gmail.address_book` | `{}` -> `{"contacts":[{email,name,score,last_ts}]}` | best first (score desc, last_ts desc), cached 15 min |
| `gmail.messages.send` | `GmailMessagesSendRequest` -> `{"ok":true,"message_id":str}` | checks (`Troppi allegati`, `Richiesta non valida`, `Allegato troppo grande`, `Allegati troppo grandi`; max 10 files, 25 MiB each and total) |
| `gmail.messages.send.check` | `GmailMessagesSendCheckRequest` -> `{"ok":true}` | adapter pre-check |
| `gmail.attachment` | `{uid,index,folder?}` -> `GmailAttachmentOut` (`BlobOut` + `filename`) | temporary file; the adapter deletes it after delivery |
| `gmail.image` | `{token}` -> `BlobOut` | HMAC-verified token else `not_found` "Immagine non trovata"; remote URLs via the SSRF-safe fetcher (8 MiB, 25 Mpx, raster only), `cid:` parts from the inline cache; `backend_error` "Immagine non disponibile" |

---

## 5. Bus events

Envelope everywhere: `{"seq":int,"type":str,"payload":object,"ts":float}` (`Event`). `seq` starts at 1,
monotonic per bus. Payloads are produced from pydantic models with `exclude_unset=True` and
`extra="allow"`: a key the producer did not set is absent. Producers in this code set the keys below
always; keys marked (opt) are conditional. An event whose payload fails schema validation is still
published with the coerced raw data.

`ContactWire` objects appear wherever a `ChatContact` was; `jsonable()` coerces any contact found anywhere
in a payload (including inside the raw message dict) to a `ContactWire`.

| type | payload (Go type) |
|---|---|
| `backend_connecting` | `{protocol}` |
| `backend_status` | `{protocol,text,persistent}` (`persistent`: keep the text until replaced) |
| `backend_ready` | `{protocol,contacts:int}` |
| `backend_done` | `{protocol,ok:false}` (failures, skips, timeouts) |
| `message` | `{protocol,contact_id,contact:ContactWire,was_placeholder,message:CacheMessage,timestamp,added,changed}` - emitted for every ingested message; `added` = new, `changed` = existing identity upgraded (optimistic -> echo) |
| `message_edit` | `{protocol,contact_id,contact,info:{message_id,timestamp(ORIGINAL),old_text,text,is_mine},text}` |
| `reaction_update` | `{protocol,contact_id,info:{message_id,timestamp,reactions:[Reaction]}}` |
| `receipt` | `{protocol,contact_id,updated:[CacheMessage]}` (rows whose status moved) |
| `typing` | `{protocol,contact_id,action:"STARTED"\|"STOPPED"}` (other actions are dropped) |
| `contact_update` | `{protocol,contact_id,contact:ContactWire,display_name,phone}` + `merged_into` (opt, WhatsApp: `contact_id` was folded into `merged_into`, `contact` is then the survivor) |
| `sent_mirror` | `{protocol,contact_id,contact,id:str,timestamp,batch_id:str\|null}` (batch send notification; rows already stored) |
| `lid_warmup_done` | `{protocol:"whatsapp",contact_id:""}` |
| `web_push` | `{event:{type,payload}}` - a legacy `/ws` frame carried on the bus (8.3) |
| `gap` | `{reason:"buffer",since:N}` or `{reason:"overflow"}` |

`CacheMessage` (the raw message dict, TUI "cache entry" shape): always `id` (null for optimistic rows), `text`,
`is_mine`, `sender` ("You" for outgoing), `timestamp` (ms), `quote_text`, `msg_type` (`text`|`image`|`sticker`|
`attachment`|`system`...), `attachment_info`, `attachment_id` (Telegram also `tgref:<chat>:<msg>`), `content_type`,
`read`, `status` (`pending|failed|sent|delivered|read`); conditional `quote_timestamp`, `quote_author`,
`reply_to_message_id`, `edited`, `protocol`, `is_group`, `media_kind`, `batch_id`, `batch_index`,
`quote_attachment_id`, `quote_attachment_path`, `quote_content_type`, `contact` and any backend-specific key
(kept in `Extra`). The TUI reads `p["contact"]` (message, message_edit, sent_mirror), `p["message"]`, `p["added"]`,
`p["changed"]`, `p["info"]`, `p["updated"]`, `p["action"]` - those keys must exist.

Status rank guard: `pending=failed=0 < sent=1 < delivered=2 < read=3`; never downgrade.

---

## 6. HTTP transport

Server: `127.0.0.1:4242` by default (`web.host` may be a comma list or JSON list = several listeners;
`web.port` 1..65535; `--web-host`, `--web-port`, `--no-web`). Everything is relative-URL based in the SPA
(works behind a path-prefix reverse proxy).

* **Auth scheme**: a single static Bearer token. Source: `WEB_TOKEN` env, else `web.token` in
  config.json (the legacy `SIGNAL_TUI_WEB_TOKEN` is reported but no longer read). Without a token (and
  without `--web-no-auth`) the web server refuses to start. Header
  `Authorization: Bearer <token>`; scheme matched case-insensitively, exactly one space, non-empty token,
  compared in constant time. **No cookies, no sessions, no CORS headers.** Every path that starts with `/api/`
  (including unknown ones: auth runs before routing) answers `401 {"detail":"Unauthorized"}` with header
  `WWW-Authenticate: Bearer` when the header is missing/wrong. `/health`, `/gmail-image`, `/ws*` (own check) and the
  static files are not behind the middleware. `--web-no-auth` removes all checks (SPA then uses the literal token
  `no-auth-required`).
* **CSRF** (`POST /api/send` only): if an `Origin` header is present, `urlsplit(origin).netloc.lower()` must equal
  the effective host or answer `403 {"detail":"Forbidden"}`. Effective host = `Host` header lowercased, except
  that `X-Forwarded-Host` (first value, lowercased) is trusted only when `Origin` is https and
  `X-Forwarded-Proto` is absent or starts with https. Requests without `Origin` pass.
* **Static SPA**: `/` serves the contents of `web/static/` with `html=True` (directory -> `index.html`); `GET /`
  and `/index.html` carry `Cache-Control: no-cache`; other static files use default caching. Files: `index.html`,
  `app.js`, `gmail.js`, `message_cache.js`, `outbox.js`, `reconcile.js`, `logs.js`, `style.css`, `gmail.css`,
  `logs.css`, `teams-skin.css`, `manifest.json`, `icon.svg`.
* `GET /health` (no auth): `{"status":"ok","port":<int>,"auth_required":<bool>,"version":<str>}` (`version` = the server version, see below; the SPA shows it in Impostazioni).
* Ops errors -> `{"detail":message}` with the status of section 2.1. Typed query/path params that fail FastAPI
  validation -> `422` (bad `proto` outside signal|whatsapp|telegram, non-int `limit`/`since`/`uid`/`w`, missing
  required query param, non-bool `refresh`; bools accept `true/false/1/0/yes/no/on/off`).
* JSON bodies declared `dict` (`payload: dict[str, Any]`) answer 422 when the body is not a JSON object; those
  routes then pass the dict to the op, where pydantic failures become 400 "Invalid request".

---

## 7. REST routes (55)

Column "op": operation called (`api.Routes`). Auth = Bearer required (all `/api/*`). JSON responses are the
operation result **unchanged** unless noted.

### 7.1 JSON routes

| method path | query/body -> op | response / notes |
|---|---|---|
| GET `/api/emoji` | - -> `emoji.list` | `[EmojiCategoryOut]`, `Cache-Control: public, max-age=3600` |
| GET `/api/reactions` | `protocol` (query, required, any string) -> `reactions.available` | `{"emojis":[...]}` |
| GET `/api/status` | - -> `server.status` | `{"signal":bool,...}` |
| GET `/api/contacts` | `q?` -> `contacts.list` | `[ContactOut]` |
| GET `/api/contacts/book` | `q` (required), `force=false` -> `contacts.book` | `[ContactOut]` |
| GET `/api/link-preview` | `url` (required) -> `links.preview` | `LinkPreviewOut`, `Cache-Control: private, max-age=3600` |
| POST `/api/log` | body `{message}` -> `server.client_log` (source `web-client`) | `{"status":"ok"}` |
| POST `/api/transcribe` | body `{protocol,attachment_id}` -> `transcribe.submit` | `{"status":"pending"}` or `{"status":"done","text"}` |
| POST `/api/config/openai-key` | body `{api_key}` -> `config.set_openai_key` | `{"status":"ok"}` |
| GET `/api/logs` | `after=0,limit=500,level=""` -> `server.logs` | `ServerLogsOut` |
| DELETE `/api/logs` | - -> `server.logs_clear` | `{"status":"ok"}` |
| GET `/api/transcribe/diagnostics` | -> `transcribe.diagnostics` | |
| POST `/api/transcribe/check` | -> `transcribe.check` | |
| GET `/api/transcribe/config` | -> `transcribe.config` | `{"enabled":bool}` |
| GET `/api/transcribe/settings` | -> `transcribe.settings` | |
| PUT `/api/transcribe/settings` | body: subset of `provider,providers,model,language,base_url,timeout` (other keys dropped) -> `transcribe.set_settings` | `TranscribeSettingsOut` |
| PUT `/api/transcribe/api-key` | body `{provider,api_key}` -> `transcribe.set_api_key` | `{"status":"ok"}` |
| GET `/api/autotranscribe` | -> `autotranscribe.config` | |
| PUT `/api/autotranscribe/global/{proto}` | path `proto` (signal\|whatsapp\|telegram, else 422), body `{enabled}` -> `autotranscribe.set_global` | `{whatsapp,signal,telegram}` |
| GET `/api/autotranscribe/chat` | `proto`, `contact_id` (both required; ids never in the path) -> `autotranscribe.chat` | |
| PUT `/api/autotranscribe/chat` | `proto`, `contact_id`, body `{enabled}` (key mandatory: absent -> `422 {"detail":"enabled richiesto"}`; null clears) -> `autotranscribe.set_chat` | |
| GET `/api/transcribe/status?protocol=&attachment_id=` | -> `transcribe.status` | Same as below; the form the SPA uses (WhatsApp ids are URLs whose `//` reverse proxies merge in a path). Both query params required (422). |
| GET `/api/transcribe/{proto}/{attachment_id:path}` | -> `transcribe.status` | `TranscribeStatusOut` / `{"status":"unknown"}` |
| GET `/api/messages` | `proto`, `contact_id` (required), `refresh=false`, `limit?`, `since?` -> `messages.page` (`proto` -> `protocol`) | `[MessageRow]`; limit outside 1..2000 -> `400 {"detail":"Invalid limit"}` |
| POST `/api/messages/read` | body `{protocol,contact_id}` -> `chats.mark_read` | `{"status":"ok"}` |
| POST `/api/chats/archive` | body `{protocol,contact_id,archived}` -> `chats.archive` | `{"archived":bool}` |
| POST `/api/messages/edit` | body `{protocol,contact_id,message_id,new_text}` -> `messages.edit` | `{"ok":true}` |
| POST `/api/messages/reaction` | body `{protocol,contact_id,message_id,emoji}` -> `reactions.set` | `{"ok":true,"reactions":[...]}` |
| GET `/api/link/status` | -> `link.status` | |
| GET `/api/link/required` | -> `link.required` | |
| POST `/api/link/start` | body: `protocol`, `phone`, `device_name` (only these keys forwarded) -> `link.start` | `{"qr":{"kind","data"}}` with url/text converted to **png** |
| POST `/api/link/poll` | body `{protocol}` -> `link.poll` | `LinkPollOut`; its `qr` (when non-null) converted to png the same way |
| POST `/api/link/finalize` | body `{phone}` -> `link.finalize` (`protocol` forced `signal`, phone `""` if falsy) | `{"account"}` |
| POST `/api/link/cancel` | body `{protocol}` -> `link.cancel` | `{"ok":true}` |
| POST `/api/link/submit_2fa` | body `{password}` -> `link.submit_2fa` | `{"ok":bool}` |
| POST `/api/link/reconnect` | body `{protocols}` -> `link.reconnect` | `{"ok":true}` |
| GET `/api/gmail/status` | -> `gmail.status` | |
| GET `/api/gmail/unread` | -> `gmail.unread` | |
| GET `/api/gmail/address-book` | -> `gmail.address_book` | |
| GET `/api/gmail/messages` | `folder?,offset=0,limit=50,refresh=false` -> `gmail.messages.list` | |
| POST `/api/gmail/messages/bulk-delete` | JSON body `{uids,folder}` -> `gmail.messages.bulk_delete`; malformed/non-object JSON -> `400 {"detail":"Richiesta non valida"}` | |
| DELETE `/api/gmail/messages/{uid}` | `uid:int` path, `folder?` -> `gmail.messages.delete` | `{"ok":true,"trashed":true}` |
| GET `/api/gmail/messages/{uid}` | `folder?` -> `gmail.messages.get` | |
| POST `/api/gmail/messages/{uid}/read` \| `/unread` \| `/archive` | `folder?` -> `gmail.messages.mark_read` \| `mark_unread` \| `archive` | `{"ok":true}` |
| POST `/api/gmail/send` | multipart, see 7.3 -> `gmail.messages.send` | `{"ok":true,"message_id"}` |

### 7.2 `POST /api/send` (JSON or multipart)

Transport duties, then `messages.send`:

1. CSRF/Origin check (section 6).
2. `multipart/form-data` (detected by `Content-Type` prefix, case-insensitive): reject `Content-Length >
   250 MiB + 1 MiB` with `413 "Upload too large"`; parse with `max_files = 20`, `max_fields = 20`
   (Starlette "Too many files" -> `400 "Too many attachments"`); form fields -> payload dict (strings);
   file parts are the repeated field **`file`** (non-file fields named `file` are "slots that are not a file");
   other failures -> `400 "Invalid request"`. Otherwise body is JSON; unparsable or not an object -> `400 "Invalid request"`.
3. `optimistic` and `retry` keys are **removed** from the payload (never settable from the web).
4. Multipart `quote_timestamp`: `""` -> null, digit string -> int, anything else passed through (the op rejects it).
5. If files: call `messages.send.check` first with `attachments:[]`, `attachment_count=len(files)`,
   `slots_ok=all are files` (cheap 400/404 before any disk write).
6. Store each file to a private temp file (`<CACHE_DIR>/web-uploads/upload-*<ext>`, 0600, extension kept only if
   allowed) with the request-wide 250 MiB budget (remaining budget per file; once exceeded that slot and all later
   ones are `rejected:"too_large"`); more than 10 files stores none (empty slots; the op answers "Too many attachments");
   non-file field -> empty slot. Each slot = `{path,filename (client's),content_type (client's)}`. A storing failure
   -> `502 {"detail":"Message send failed"}`.
7. Call `messages.send` with `{**payload, "attachments": slots}`; delete temp files afterwards (always).
Response `{"ok":true,"message_ids":[...]}` (or `{"ok":true,"duplicate":true}`; the optimistic flavour keeps its own
shape; the body is passed through unchanged). JSON bodies produced by the SPA:
`{protocol,contact_id,text,client_msg_id,quote_timestamp?,quote_author?,quote_message?,reply_to_message_id?
(non-signal) | quote_content_type?,quote_attachment_id? (signal)}`; multipart additionally `batch_id`.
Upload pre-pruning: files older than 1 h in the upload dir are removed at startup.

### 7.3 `POST /api/gmail/send` (multipart only)

Form parse with `max_files = 11`, `max_fields = 20`; failures -> `400 {"detail":"Richiesta non valida"}`
(413 kept if Starlette says 413). Fields `to,cc,bcc,subject,text` (strings, "" if absent), `reply_to_uid`,
`forward_uid` (digits, `400 "<name> non valido"` if not an int), `reply_to_folder`, `forward_folder` ("" -> null).
Files: repeated field **`file[]`**. If any: `gmail.messages.send.check` first. Files stored with the Gmail
per-file cap (25 MiB, no media policy); the first over-limit file becomes a `rejected:"too_large"` slot and stops
storing; storing also stops once the running total exceeds 25 MiB. Storing failure -> `502 {"detail":"Errore Gmail"}`.
Filename default `attachment`. Temp files deleted afterwards.

### 7.4 Blob routes (not JSON)

| method path | -> op | response |
|---|---|---|
| GET `/api/media/{proto}/{attachment_id:path}` | `w?` int; no `w` -> `media.attachment`, with `w` -> `media.thumbnail{width:w}`; `v` (cache buster) ignored | file stream (Starlette `FileResponse`: `Content-Type` = blob type, `Content-Length`, `ETag`, `Last-Modified`, `Accept-Ranges: bytes`, Range requests -> 206; **video playback needs Range**). `Cache-Control: private, max-age=31536000, immutable` if `blob.immutable` else `private, max-age=86400` |
| GET `/api/quote-media/{proto}/{message_row_id}` | `w?` -> `media.quote` | same as above |
| GET `/api/contact-avatar` | `proto`, `contact_id` -> `contacts.avatar` | whole file body, `Content-Type` = blob type, `Cache-Control: private, max-age=86400` |
| GET `/api/link-preview/image` | `url` -> `links.preview_image` | whole body, blob type, `Cache-Control: private, max-age=86400`, `X-Content-Type-Options: nosniff` |
| GET `/api/gmail/messages/{uid}/attachments/{index}` | `folder?` -> `gmail.attachment` | whole body, `Content-Type` blob type, `Content-Disposition: attachment; filename="<ascii name, non-ASCII->?, " -> _>"; filename*=UTF-8''<percent-encoded name>`, `Content-Length`, `X-Content-Type-Options: nosniff`; the temp file is deleted after reading |
| GET `/gmail-image` | `token` (**no auth**; `<img src>` cannot send headers) -> `gmail.image` | whole body, blob type, `Cache-Control: private, max-age=3600`, `Content-Security-Policy: default-src 'none'`, `X-Content-Type-Options: nosniff`; temp file deleted; at most 4 concurrent fetches per app |

`proto` in blob routes is validated as a Literal (422 otherwise); `message_row_id` int.

---

## 8. WebSockets

Both sockets are mounted on the HTTP server (`/ws`, `/ws/v2`; the SPA connects relative to the page URL).

### 8.1 Auth

Token from, in order: `Authorization: Bearer <token>` header, else the `Sec-WebSocket-Protocol` list: the
browser offers `["signal-tui-bearer", "signal-tui-token.<base64url(token)>"]` (base64url, padding optional;
invalid base64/UTF-8 -> unauthorised). The server accepts the connection **with subprotocol `signal-tui-bearer`**
when the header form was used and that protocol was offered (none otherwise). Unauthorised: the server
answers the handshake with HTTP 403 (equivalent to closing *before* accepting with code 1008; the
SPA treats 1008 as "ask for the token" but otherwise reconnects; either behaviour is accepted).
`--web-no-auth` skips the check. `/ws/v2` additionally rejects an invalid `?since=` with 1008.

### 8.2 `/ws/v2` (typed events with resume)

* Client -> server (optional, text JSON): `{"type":"subscribe","since":N}`. `since` int >= 0 or digit string;
  anything else (bool, negative, float, junk) answers `{"type":"error","payload":{"reason":"invalid_subscribe"}}` and
  the frame is ignored; non-subscribe or non-JSON frames are ignored silently. A valid subscribe restarts the stream
  (close the old subscription, `bus.subscribe(since)`). Up to 16 unread client frames are buffered, extras dropped.
* `?since=N` on the URL = subscribe immediately. Without it the server first subscribes live (events keep buffering),
  waits up to **0.5 s** for a subscribe frame, then streams; a received subscribe replaces the subscription.
* Server -> client: every bus event as `{"seq","type","payload","ts"}` (including `gap` and `web_push`), same
  replay/gap/overflow rules as the socket; when idle for 5 s `{"type":"heartbeat"}` (no payload). Poll interval
  0.25 s. Frames that are not JSON-serialisable are dropped with a warning.

### 8.3 `/ws` (legacy thin frames, what the SPA consumes)

One shared bus subscription fans out to every `/ws` client (no resume, live only). Each bus event is mapped by
`internal/web/legacy.go::toLegacy`; events without a mapping are skipped (and count as idle). An idle gap of >= 5 s
sends `{"type":"heartbeat"}`. Per-client send timeout 0.5 s; a client that fails is dropped and closed. The SPA
watchdog treats 10 s without any traffic as a dead socket. Client messages are read and ignored.

Frame = `{"type":T,"payload":P}` (`WebFrame`):

| bus event | frame |
|---|---|
| `web_push` | `payload.event` verbatim (only if it is an object) |
| `message` (only if `added` or `changed`) | `message` `{id:<message.id or null>,protocol,contact_id,timestamp,is_mine:bool(message.is_mine)}` (`LegacyMessagePayload`) |
| `message_edit` | `message_edit` `{protocol,contact_id,message_id:str,timestamp:int,old_text:str,text,is_mine:bool}` |
| `reaction_update` | `reaction_update` `{protocol,contact_id,message_id:str,timestamp:int,reactions}` |
| `receipt` | `receipt` `{protocol,contact_id,updates:[{id:str\|null,timestamp:int,status:str("sent" default),text:str}]}` |
| `typing` | `typing` `{protocol,contact_id,action}` |
| `contact_update` | `contact_update` `{protocol,contact_id,display_name,phone}`; for a WhatsApp merge also `merged_into` (before display_name) |
| `lid_warmup_done` | `lid_warmup_done` `{protocol}` (default `whatsapp`) |
| `backend_*`, `sent_mirror`, `gap` | no frame |

Frames produced directly as `web_push` events by operations/workers (they travel as `web_push` on the bus, hence
also on `/ws/v2` and the socket):

| type | payload | producer |
|---|---|---|
| `message` | `{protocol,contact_id,is_mine:true}` | `messages.send` success (non-optimistic) |
| `message` | `{id,protocol,contact_id,timestamp,is_mine:true}` | optimistic send, row newly added |
| `message_edit` | `{protocol,contact_id,message_id,timestamp,old_text,text,is_mine:true}` | `messages.edit` |
| `reaction_update` | `{protocol,contact_id,message_id,timestamp,reactions}` | `reactions.set` |
| `send_retry` | `{client_msg_id,protocol,contact_id,attempt,max_attempts:3,error}` | `messages.send` before each retry (`attempt` = the upcoming attempt, error <= 200 chars) |
| `link_required` | `{protocol:"whatsapp",required:bool}` | WhatsApp session guard |
| `gmail_unread` | `{unread:int,new_mail:bool}` | Gmail watcher (poll every `GMAIL_UNREAD_POLL_S`, default 60, `0` disables; only on change; `new_mail` = UIDNEXT grew) |

What the SPA does with them (app.js `connectSocket`): `message`/`contact_update`/`lid_warmup_done` trigger refetches
(`/api/contacts`, `/api/messages`); `receipt`/`message_edit`/`reaction_update`/`typing`/`send_retry`/`link_required`/
`gmail_unread` are applied in place; frames with no `payload` are ignored; unknown types are ignored.

---

## 9. Config, environment and files

### 9.1 `config.json`

`internal/config/schema.go` is the single schema table: it defines every setting, its `config.json`
location (`domain.setting`), the environment override derived from it (`UPPER(domain_setting)`) and the
legacy names it replaced. The startup warnings and `megachatd config migrate` read that table; the
per-domain tables below are generated from it.

`config.json` lives in the project directory: `--project-dir`, else `$MEGACHAT_PROJECT_DIR`, else the
repository containing the executable, else the working directory. Missing/invalid JSON or a non-object
value is treated as `{}`. It is re-read on every getter call (no cache). Writers rewrite the whole file
with two-space indentation and a trailing newline, preserving the other keys and writing the nested
shape: `transcribe.set_settings`, `transcribe.set_api_key` (`<provider>.api_key`), `autotranscribe.*`
(`transcription.auto.<protocol>`, `transcription.auto_chats["<protocol>:<contact_id>"]`) and
`link.finalize` (`signal.user_number`, atomic temp file + rename).

Resolution rule for every getter:

1. the environment variable `UPPER(domain_setting)`, when set and non-blank;
2. else `config.json[domain][setting]`, when present and not `null`;
3. else the default below.

A value that cannot be coerced to the setting's type falls through to the next source. Types: `string`
(a JSON string or any scalar stringified; the trimmed getters treat a whitespace-only value as unset),
`int` (JSON number or integer string), `bool` (JSON bool, `0`/`1`, or the spellings `1/true/yes/on` and
`0/false/no/off`, case-insensitive), `float` (JSON number or numeric string) and `map` (object only).
Map settings have no environment override. Getters keep their clamps: positive / non-negative values,
ports 1..65535, base URLs without a trailing `/`, `gmail.unread_poll_s` = max(0, value), and
`web.host` may be a string or a JSON array of hosts joined with commas.

The project `.env` is never a settings source (it is inspected only for the startup warning and
`config migrate`). Variables that do not appear in the table are *not* settings: `MEGACHAT_PROJECT_DIR`,
`MEGACHAT_SOCKET`, `MEGACHAT_LOCK_FILE`, `MEGACHAT_LOG_FILE`, `MEGACHAT_FAKE_BACKEND`, `HOME`, `XDG_*`,
`SSH_CONNECTION`.

**Startup validation.** Before it opens the store or registers a backend, `Build` validates every
*enabled* integration (`<domain>.enabled`, env override wins). If any is incomplete, `megachatd
serve` releases the instance lock, never listens, exits 1 and prints one `❌ ...` error on stderr
that lists every problem at once (all integrations, all missing settings), as the `config.json`
path and its environment override, plus how to proceed:

| Integration | Required settings |
|---|---|
| `telegram` | `telegram.api_id` (non-zero) and `telegram.api_hash` |
| `gmail` | `gmail.address` and an App Password from `gmail.app_password` (non-empty), `gmail.app_password_file` (file exists and is non-empty) or `gmail.app_password_cmd` (resolves to a non-empty password) |
| `whatsapp` | none (paired by QR through `link.*`) |
| `signal` | none (may start unlinked; `link.finalize` writes `signal.user_number`) |

A disabled integration is never validated. Example:

```text
telegram is enabled but missing: telegram.api_id (TELEGRAM_API_ID), telegram.api_hash (TELEGRAM_API_HASH)
gmail is enabled but missing: gmail.address (GMAIL_ADDRESS), one of gmail.app_password / gmail.app_password_file / gmail.app_password_cmd
Fill in the missing settings in config.json (or set their environment overrides), or set telegram.enabled=false / gmail.enabled=false in config.json to disable them.
```

The table lives next to the schema in `internal/config/requirements.go`. `launcher.py server start`
and the TUI's find-or-spawn surface this `❌` block instead of a generic spawn failure.

#### `signal`

| Setting | Env override | Type | Default | Legacy config.json key / env |
|---|---|---|---|---|
| `signal.enabled` | `SIGNAL_ENABLED` | bool | false | `signal_enabled` |
| `signal.user_number` | `SIGNAL_USER_NUMBER` | string | "" | `user_number` |

#### `whatsapp`

| Setting | Env override | Type | Default | Legacy config.json key / env |
|---|---|---|---|---|
| `whatsapp.enabled` | `WHATSAPP_ENABLED` | bool | false | `whatsapp_enabled` |
| `whatsapp.session_name` | `WHATSAPP_SESSION_NAME` | string | "default" | `whatsapp_session_name` |
| `whatsapp.media_dir` | `WHATSAPP_MEDIA_DIR` | string | "" (then `<data>/whatsapp-media`) | `whatsapp_media_dir` |
| `whatsapp.browser` | `WHATSAPP_BROWSER` | string | "" (Chrome) | `whatsapp_browser` |
| `whatsapp.browser_version` | `WHATSAPP_BROWSER_VERSION` | string | "" (Chrome stable) | `whatsapp_browser_version` |
| `whatsapp.browser_os` | `WHATSAPP_BROWSER_OS` | string | "" (`Linux`) | `whatsapp_browser_os` |
| `whatsapp.lazy_media` | `WHATSAPP_LAZY_MEDIA` | bool | true | `wa_lazy_media`<br>env `WA_LAZY_MEDIA` |
| `whatsapp.lazy_media_negative_ttl_s` | `WHATSAPP_LAZY_MEDIA_NEGATIVE_TTL_S` | int | 30 | `wa_lazy_media_negative_ttl_s`<br>env `WA_LAZY_MEDIA_NEGATIVE_TTL_S` |
| `whatsapp.lid_cache_ttl_days` | `WHATSAPP_LID_CACHE_TTL_DAYS` | int | 30 | `wa_lid_cache_ttl_days`<br>env `WA_LID_CACHE_TTL_DAYS` |
| `whatsapp.lid_retry_cooldown_s` | `WHATSAPP_LID_RETRY_COOLDOWN_S` | int | 300 | `wa_lid_retry_cooldown_s`<br>env `WA_LID_RETRY_COOLDOWN_S` |
| `whatsapp.resync_download_media` | `WHATSAPP_RESYNC_DOWNLOAD_MEDIA` | bool | false | `wa_resync_download_media`<br>env `WA_RESYNC_DOWNLOAD_MEDIA` |
| `whatsapp.resync_max_chats` | `WHATSAPP_RESYNC_MAX_CHATS` | int | 20 (0 = unlimited) | `wa_resync_max_chats`<br>env `WA_RESYNC_MAX_CHATS` |
| `whatsapp.resync_limit` | `WHATSAPP_RESYNC_LIMIT` | int | 30 | `wa_resync_limit`<br>env `WA_RESYNC_LIMIT` |
| `whatsapp.resync_workers` | `WHATSAPP_RESYNC_WORKERS` | int | 2 | `wa_resync_workers`<br>env `WA_RESYNC_WORKERS` |
| `whatsapp.session_guard` | `WHATSAPP_SESSION_GUARD` | bool | true | `wa_session_guard`<br>env `WA_SESSION_GUARD` |
| `whatsapp.session_poll_s` | `WHATSAPP_SESSION_POLL_S` | int | 60 | `wa_session_poll_s`<br>env `WA_SESSION_POLL_S` |
| `whatsapp.session_restart_timeout_s` | `WHATSAPP_SESSION_RESTART_TIMEOUT_S` | int | 120 | `wa_session_restart_timeout_s`<br>env `WA_SESSION_RESTART_TIMEOUT_S` |
| `whatsapp.session_max_restarts_per_hour` | `WHATSAPP_SESSION_MAX_RESTARTS_PER_HOUR` | int | 3 (0 = never) | `wa_session_max_restarts_per_hour`<br>env `WA_SESSION_MAX_RESTARTS_PER_HOUR` |
| `whatsapp.session_backoff_s` | `WHATSAPP_SESSION_BACKOFF_S` | string | "5,30,120,300" | `wa_session_backoff_s`<br>env `WA_SESSION_BACKOFF_S` |
| `whatsapp.session_stable_reset_s` | `WHATSAPP_SESSION_STABLE_RESET_S` | int | 600 | `wa_session_stable_reset_s`<br>env `WA_SESSION_STABLE_RESET_S` |
| `whatsapp.session_failed_grace_s` | `WHATSAPP_SESSION_FAILED_GRACE_S` | int | 10 | `wa_session_failed_grace_s`<br>env `WA_SESSION_FAILED_GRACE_S` |
| `whatsapp.session_unreachable_after` | `WHATSAPP_SESSION_UNREACHABLE_AFTER` | int | 5 | `wa_session_unreachable_after`<br>env `WA_SESSION_UNREACHABLE_AFTER` |
| `whatsapp.presence_subscribe` | `WHATSAPP_PRESENCE_SUBSCRIBE` | bool | false | env `WAHA_PRESENCE_SUBSCRIBE` |

#### `telegram`

| Setting | Env override | Type | Default | Legacy config.json key / env |
|---|---|---|---|---|
| `telegram.enabled` | `TELEGRAM_ENABLED` | bool | false | `telegram_enabled` |
| `telegram.api_id` | `TELEGRAM_API_ID` | int | 0 | `telegram_api_id` |
| `telegram.api_hash` | `TELEGRAM_API_HASH` | string | "" | `telegram_api_hash` |
| `telegram.reactions_notify` | `TELEGRAM_REACTIONS_NOTIFY` | bool | true | — |

#### `gmail`

| Setting | Env override | Type | Default | Legacy config.json key / env |
|---|---|---|---|---|
| `gmail.enabled` | `GMAIL_ENABLED` | bool | false (also needs credentials) | `gmail_enabled` |
| `gmail.address` | `GMAIL_ADDRESS` | string | "" | `gmail_address` |
| `gmail.app_password` | `GMAIL_APP_PASSWORD` | string | "" | `gmail_app_password` |
| `gmail.app_password_file` | `GMAIL_APP_PASSWORD_FILE` | string | "" | `gmail_app_password_file` |
| `gmail.app_password_cmd` | `GMAIL_APP_PASSWORD_CMD` | string | "" | `gmail_app_password_cmd` |
| `gmail.imap_host` | `GMAIL_IMAP_HOST` | string | "imap.gmail.com" | `gmail_imap_host` |
| `gmail.imap_port` | `GMAIL_IMAP_PORT` | int | 993 | `gmail_imap_port` |
| `gmail.smtp_host` | `GMAIL_SMTP_HOST` | string | "smtp.gmail.com" | `gmail_smtp_host` |
| `gmail.smtp_port` | `GMAIL_SMTP_PORT` | int | 587 | `gmail_smtp_port` |
| `gmail.folder` | `GMAIL_FOLDER` | string | "INBOX" | `gmail_folder` |
| `gmail.timeout_s` | `GMAIL_TIMEOUT_S` | int | 30 | `gmail_timeout_s` |
| `gmail.unread_poll_s` | `GMAIL_UNREAD_POLL_S` | float | 60 (negative -> 0) | — |
| `gmail.html_styles` | `GMAIL_HTML_STYLES` | bool | true | — |
| `gmail.html_inline_css` | `GMAIL_HTML_INLINE_CSS` | bool | true | — |
| `gmail.image_prefetch` | `GMAIL_IMAGE_PREFETCH` | bool | true | — |

#### `web`

| Setting | Env override | Type | Default | Legacy config.json key / env |
|---|---|---|---|---|
| `web.enabled` | `WEB_ENABLED` | bool | false | — |
| `web.host` | `WEB_HOST` | string | "127.0.0.1" | — |
| `web.port` | `WEB_PORT` | int | 4242 | — |
| `web.token` | `WEB_TOKEN` | string | "" | env `SIGNAL_TUI_WEB_TOKEN` |

#### `transcription`

| Setting | Env override | Type | Default | Legacy config.json key / env |
|---|---|---|---|---|
| `transcription.provider` | `TRANSCRIPTION_PROVIDER` | string | "openai" | `ai_provider`<br>env `AI_PROVIDER` |
| `transcription.language` | `TRANSCRIPTION_LANGUAGE` | string | "" (automatic) | `transcription_language` |
| `transcription.timeout` | `TRANSCRIPTION_TIMEOUT` | int | 120 | `transcription_timeout` |
| `transcription.auto` | — (map) | map | {} (per-protocol, unset = false) | `auto_transcribe` |
| `transcription.auto_chats` | — (map) | map | {} | `auto_transcribe_chats` |

#### `openai`

| Setting | Env override | Type | Default | Legacy config.json key / env |
|---|---|---|---|---|
| `openai.api_key` | `OPENAI_API_KEY` | string | "" | `openai_api_key` |
| `openai.transcription_model` | `OPENAI_TRANSCRIPTION_MODEL` | string | "gpt-transcribe" | `openai_transcription_model`, `transcription_model`<br>env `TRANSCRIPTION_MODEL` |
| `openai.base_url` | `OPENAI_BASE_URL` | string | "https://api.openai.com/v1" | `transcription_base_url`<br>env `TRANSCRIPTION_BASE_URL` |

#### `openrouter`

| Setting | Env override | Type | Default | Legacy config.json key / env |
|---|---|---|---|---|
| `openrouter.api_key` | `OPENROUTER_API_KEY` | string | "" | `openrouter_api_key` |
| `openrouter.transcription_model` | `OPENROUTER_TRANSCRIPTION_MODEL` | string | "openai/whisper-large-v3" | `openrouter_transcription_model` |
| `openrouter.base_url` | `OPENROUTER_BASE_URL` | string | "https://openrouter.ai/api/v1" | `openrouter_base_url` |
| `openrouter.http_referer` | `OPENROUTER_HTTP_REFERER` | string | "" | `openrouter_http_referer` |
| `openrouter.app_title` | `OPENROUTER_APP_TITLE` | string | "" | `openrouter_app_title` |

#### `gemini`

| Setting | Env override | Type | Default | Legacy config.json key / env |
|---|---|---|---|---|
| `gemini.api_key` | `GEMINI_API_KEY` | string | "" | `gemini_api_key`<br>env `GOOGLE_API_KEY` |
| `gemini.transcription_model` | `GEMINI_TRANSCRIPTION_MODEL` | string | "gemini-flash-latest" | `gemini_transcription_model` |
| `gemini.base_url` | `GEMINI_BASE_URL` | string | "https://generativelanguage.googleapis.com/v1beta" | `gemini_base_url` |

#### `messages`

| Setting | Env override | Type | Default | Legacy config.json key / env |
|---|---|---|---|---|
| `messages.retention_per_contact` | `MESSAGES_RETENTION_PER_CONTACT` | int | 300 (0 disables) | `message_retention_per_contact`<br>env `MESSAGE_RETENTION_PER_CONTACT` |

#### `address_book`

| Setting | Env override | Type | Default | Legacy config.json key / env |
|---|---|---|---|---|
| `address_book.ttl_s` | `ADDRESS_BOOK_TTL_S` | int | 300 | `address_book_ttl_s` |

#### `dynamic_refresh`

| Setting | Env override | Type | Default | Legacy config.json key / env |
|---|---|---|---|---|
| `dynamic_refresh.interval_s` | `DYNAMIC_REFRESH_INTERVAL_S` | int | 300 | `dynamic_refresh_interval_s` |
| `dynamic_refresh.cooldown_s` | `DYNAMIC_REFRESH_COOLDOWN_S` | int | 60 | `dynamic_refresh_cooldown_s` |

#### `picker`

| Setting | Env override | Type | Default | Legacy config.json key / env |
|---|---|---|---|---|
| `picker.max_results` | `PICKER_MAX_RESULTS` | int | 50 | `picker_max_results` |
| `picker.preferred_backend` | `PICKER_PREFERRED_BACKEND` | string | "" | `picker_preferred_backend` |

#### `image`

| Setting | Env override | Type | Default | Legacy config.json key / env |
|---|---|---|---|---|
| `image.protocol` | `IMAGE_PROTOCOL` | string | "auto" | `image_protocol` |

#### `thumbnail`

| Setting | Env override | Type | Default | Legacy config.json key / env |
|---|---|---|---|---|
| `thumbnail.max_lines` | `THUMBNAIL_MAX_LINES` | int | 12 | `thumbnail_max_lines` |
| `thumbnail.max_cols` | `THUMBNAIL_MAX_COLS` | int | 60 | `thumbnail_max_cols` |

#### Legacy names and the project `.env`

Legacy names are recorded in `schema.go` and reported, but **not read**: a flat `config.json` key or a
legacy environment variable never changes the value of a setting.

* A legacy flat key present in `config.json` is ignored and produces one `Warn` plus one Italian
  `App.Notices` entry naming the new `domain.setting` (and its env variable) and
  `megachatd config migrate`. A removed WAHA-only key is ignored with a notice asking to run the
  command to clean the file.
* A legacy environment variable that is set is logged once with its new name (removed ones are reported
  as ignored). These are warnings only, not notices.
* If `<project>/.env` exists, startup logs one `Warn` and appends one Italian notice listing its key
  names only (never values) and pointing at `megachatd config migrate`.

Removed settings (no Go code reads them any more):

| Removed setting | Legacy config.json key | Legacy env |
|---|---|---|
| WAHA API URL | `whatsapp_api_url` | `WHATSAPP_API_URL` |
| WAHA API key | `whatsapp_api_key` | `WHATSAPP_API_KEY`, `WAHA_API_KEY` |
| WAHA webhook/port | — | `WHATSAPP_API_PORT`, `CLIENT_WEBHOOK_PORT`, `WAHA_WEBHOOK_URL` |

#### `megachatd config migrate`

`megachatd config migrate [--project-dir DIR] [--dry-run]` converts a legacy project in place: it reads
`config.json` and `.env`, moves every legacy flat key to its `domain.setting` (removed WAHA-only keys
are dropped) and folds in every `.env` key that maps to a setting (the nested `config.json` value wins
when both set the same setting; unknown `.env` keys are listed, not copied). It keeps a backup
`config.json.pre-nested.bak` (never overwritten), writes `config.json` with mode `0600` (it now holds
secrets) and renames `.env` to `.env.migrated` only when every key was migrated or reported.
`--dry-run` prints the resulting JSON with secret values masked and writes nothing. The command is
idempotent: running it on an already nested config changes nothing.

Other environment variables read by the server (runtime, not settings): `MEGACHAT_PROJECT_DIR`,
`MEGACHAT_SOCKET`, `MEGACHAT_LOCK_FILE`, `MEGACHAT_LOG_FILE`, `XDG_RUNTIME_DIR`, `XDG_DATA_HOME`,
`XDG_CACHE_HOME`, `HOME`, `SSH_CONNECTION`.

CLI (`megachat serve`): `--debug`, `--web-port`, `--web-host`, `--web-no-auth`, `--no-web`, `--no-socket`, `--socket PATH`
(`--socket` and `--no-socket` are mutually exclusive; `--web-port` 1..65535). Exit code 1 if the lock is taken or an
enabled adapter fails to start. Shutdown order: stop socket adapter, then web, then backends, prune.

### 9.2 Files and ports

* Cache dir `~/.local/share/signal-tui-client/` (`CACHE_DIR`): `messages.db` (SQLite), `web-uploads/` (staged
  uploads, pruned after 1 h), `quote-thumbs/`, thumbnail caches, avatar cache.
* Log: `/tmp/signal-tui.log` (append); in-memory ring of 2000 records feeds `server.logs`
  (redaction regexes: `sk-[A-Za-z0-9_-]{6,}` -> `sk-***`, `AIza...` -> `AIza***`, `Bearer token: X` / `Bearer X(8+)` -> `***`).
* signal-cli data: `~/.local/share/signal-cli/{attachments,avatars}`; local `bin/signal-cli-*/bin/signal-cli`.
* Ports: web 4242, download server 10042. The legacy WAHA ports (8088/3005) are no longer used.

---

## 10. Quirks to preserve

1. **Nulls are data.** Operation results keep `null` for Optional members (`BlobOut.content_type`, `status: null`
   on incoming rows, `qr: null` ...). Event payloads are the opposite: `exclude_unset`, so unset keys are absent
   while explicitly-set `None` values (e.g. `phone`, `batch_id`, `quote_text` inside `message`) are `null`.
2. **Lists/maps are never null** (`[]` / `{}`): see `EmptyIfNil` and the `MarshalJSON` normalisers in `internal/api`.
3. **`proto` (HTTP query/path) vs `protocol` (ops/bodies)**: only `/api/messages`, `/api/contact-avatar`,
   `/api/media`, `/api/quote-media`, `/api/transcribe/{proto}`, `/api/autotranscribe/*` use `proto`; POST bodies use `protocol`.
4. **`transcribe.status` vs `transcribe.submit`** disagree on the success word: stored `"ok"` vs reply `"done"`.
5. **`gmail.messages.delete` returns `trashed: true` (bool) while `bulk_delete` returns an int.**
6. **Italian vs English messages**: Gmail/transcription errors are Italian, the rest English; copy texts verbatim.
7. **`ensure_ascii` on the socket only**; HTTP/WS JSON is UTF-8 (FastAPI `ensure_ascii=False`). Either decodes the same.
8. **`ts` fields are float seconds**; message `timestamp`s are integer **milliseconds**; Gmail `date_ts` is integer seconds.
9. **Operation errors on the socket carry `data.details: null`** when there are none; protocol errors omit `details`.
10. **Link QR conversion happens only in the HTTP adapter.** TUI renders `url`/`text` itself.
11. **`messages.send` over HTTP can never be optimistic or single-attempt** (`optimistic`/`retry` stripped); over the
    socket the TUI passes both (`retry:false`, `optimistic:{timestamp,...}`).
12. **Timed-out socket calls keep running** and their result is discarded. Never cancel an operation because the
    caller went away.
13. **The `message` event is emitted even if nothing was stored** (`added:false,changed:false`); `/ws` drops those,
    `/ws/v2` and the socket do not.
14. **`/ws/v2` heartbeat is `{"type":"heartbeat"}`; the socket heartbeat is a JSON-RPC notification.**
15. **WhatsApp ids**: contacts have twin ids (`@c.us` ghost + `@lid` canonical); `contact_storage_keys` unions DB rows and
    unread counts, `messages.*` canonicalise ghost ids. `unread` in `ContactOut` sums across the keys.
16. **Signal quote placeholders are display-only** (`MEDIA_QUOTE_PLACEHOLDERS`: "🖼️ Immagine", "🎨 Sticker", "📎 File",
    "🎵 Audio", "🎬 Video") and must never be sent as `quoteMessage`; media replies send `quote_message:""` with
    `quote_content_type`/`quote_attachment_id` instead.
17. **Pydantic lax coercion** means the SPA/TUI may send `"true"`/`"10"`/`10.0` in non-strict fields; decode leniently
    for the non-strict fields listed in section 4 and strictly for the `Strict*` ones.
18. **`messages.send.check`, `gmail.messages.send.check` are real ops** (visible in `rpc.hello`) but meant for adapters.
    `contacts.touch`, mentioned in an older design doc, does not exist.
