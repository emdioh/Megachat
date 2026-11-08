# Design — protocollo di stato dei messaggi in uscita

Come una bolla inviata passa da `pending` a `sent`/`delivered`/`read` (o `failed`), su quali chiavi avvengono le transizioni e come si evitano downgrade e doppioni. Ricavato da `tui/send.py`, `tui/events.py`, `tui/chat_view.py`, `tui/backend_connect.py`, `megachatd/internal/core/ops_send.go`, `megachatd/internal/store` e `ui_components.py::MessageWidget`.

## 1. Macchina a stati

```
            send submit                 server OK                 receipt delivery        receipt read
 (UI thread) ────────►  PENDING  ─────►  SENT  ───────────►  DELIVERED  ───────────►  READ
                            │
                            └── errore server / rifiuto / backend assente ──►  FAILED
```

- Valori ammessi (`models.ChatMessage.status`): `"pending"`, `"failed"`, `"sent"`, `"delivered"`, `"read"` (default `"sent"`).
- Rank canonico: `pending=0 failed=0 sent=1 delivered=2 read=3`; nessun downgrade. Il rank è applicato dallo store Go (`internal/store`) e, lato vista, dai merge in `tui/chat_view.py` e `tui/backend_connect.py` (`_STATUS_RANK`). La costante è duplicata in pochi punti del client: se si aggiunge uno stato, aggiornarli tutti.
- Rendering (`MessageWidget._apply_status_style`, solo per `is_mine=True`):
  - `pending` → dim + classe `msg-pending`;
  - `failed` → bold + classe `msg-failed` (bolla cliccabile per il retry);
  - `sent` → *italic*;
  - `delivered` → **bold**;
  - `read` → stile normale.

## 2. Flusso ottimistico (tui/send.py + server)

1. **Submit** (UI thread): `ts = int(time.time()*1000)`; la bolla nasce subito `pending` nella cache di vista; `_promote_contact_after_send(contact, ts)` avanza `last_message_ts` e riordina la lista immediatamente, senza aspettare il server.
2. **Worker**: `messages.send` con `{protocol, contact_id, text, client_msg_id, quote_*, reply_to_message_id, optimistic: {timestamp, resend, …}}` (`resend=false` al primo invio). Il **core** semina e persiste la riga pending, invoca il backend e la porta a `sent`/`failed`: nessuna scrittura di backend/DB sul thread UI.
3. **Risposta**: se `status_applied` è true la vista porta la bolla a `sent` (expected `pending`); `message_id` restituito viene specchiato nella cache e sul widget (`_update_outgoing_message_id`) così i receipt matchano per id. L'echo arriva anche come evento `message` (`added=false`, `changed=true` per l'upgrade dell'id).
4. **Timeout/connessione persa** (`OperationTimeout`, `ServerUnavailable`): l'esito è **ignoto**. La bolla resta `pending` (mai `failed`: un retry potrebbe inviare due volte), la status bar avvisa e la risincronizzazione decide: la riga del server comanda; una bolla di cui il server non ha traccia viene marcata `failed`.
5. **Errore certo**: la bolla va a `failed` (expected `pending`), il messaggio d'errore va in status bar e il dettaglio nel log.

## 3. Chiavi delle transizioni e fallback

Lo store identifica la riga per `(protocol, contact_number, timestamp)` e, dove serve, per testo o `msg_id`:

1. **timestamp** — identità della riga ottimistica;
2. **by-text** — riga outgoing più recente con lo stesso testo (fallback quando l'echo ha già sostituito il timestamp ottimistico con quello server);
3. **by-id** — `msg_id` per i receipt che identificano i messaggi per id server.

Il rank guard rifiuta i downgrade. Lato vista `_apply_outgoing_status`/`_update_message_widgets_status` usano la stessa cascata (by-id → (ts,text) → fuzzy legato al testo entro ±2000 ms) per aggiornare le bolle senza toccare righe di altre chat.

### Limiti noti

1. **`sent` → `failed`**: il rank guard non lo consente. Se il server risponde OK e poi l'invio fallisce internamente, la riga può restare `sent`; una decisione esplicita (per esempio consentire `sent→failed` solo dal server) sarebbe necessaria per cambiarlo.
2. **Fallback by-text ambiguo su testi ripetuti**: con più "ok" inviati può essere aggiornata la riga sbagliata.
3. **Aggiornamento non atomico lato vista**: cache UI → widget sono passi sequenziali; un crash a metà lascia stati divergenti fino al reload.
4. **Esito ignoto**: dopo un timeout la bolla resta `pending` finché la risincronizzazione non la riconcilia; non c'è retry automatico.

## 4. Receipt in arrivo

Il server applica il receipt allo store e pubblica `receipt` con `updated: [CacheMessage]` (le righe il cui status è avanzato).

- **Signal**: envelope `receiptMessage` con `isDelivery`/`isRead` e lista `timestamps`; matching fuzzy `±1000 ms` sui timestamp propri, rank guard (`internal/proto/signal`).
- **WhatsApp**: eventi whatsmeow (ack/read) matchati per id del messaggio, con fallback per timestamp/testo; rank guard (`internal/proto/whatsapp`).
- **Telegram**: update di lettura per `msg_id` del messaggio (`internal/proto/telegram`).

La UI (`tui/events.py::_view_receipt`) specchia gli status nella cache di vista (match per id, poi timestamp, poi fuzzy per testo entro ±2000 ms con self-heal dell'id) e, se la chat è aperta, aggiorna i widget con `_update_message_widgets_status`.

## 5. Fallimenti e retry

- Backend assente per il protocollo → `failed` immediato + status bar persistente.
- Eccezione del server → `failed` + status bar con l'errore.
- Reply Telegram senza `reply_to_message_id` valido e reply WhatsApp senza id → rifiuto PRIMA di creare la bolla (nessun pending orfano).
- Click su bolla `failed` → `_retry_failed_message(timestamp, text)`: la bolla torna subito `pending` (expected `failed`) e il worker richiama `messages.send` con `optimistic.resend=true`; il core ri-arma la propria riga. Guardie:
  - reply Telegram il cui id originale non è più disponibile → rifiuto esplicito;
  - reply il cui metadata di quote è andato perso dopo un reload (`quote_text` senza `quote_timestamp`) → rifiuto;
  - se il server rifiuta il re-send, la bolla torna `failed` e non sembra "in corso";
  - una reply media ricostruisce i metadati dell'allegato (`content_type`/`attachment_id` persistiti) così il Signal quote viene ricomposto identico al primo invio (vedi [CONTRACTS.md §11](../api-contracts/CONTRACTS.md#11-quote-media-reply-con-immagine)).

## Test che documentano questo design

- `tests/test_tui_send_ops.py` — invio via `messages.send`, esiti, timeout, rollback.
- `tests/test_refresh_chat.py` — merge dello status e nessun downgrade nella vista.
- `tests/test_backend_connect.py` — merge dello snapshot con rank guard.
- `tests/test_ui_components.py` — stili visuali per status.
- `megachatd/internal/core/ops_send_test.go`, `internal/store/messages_test.go` e i test di protocollo — transizioni, dedup e receipt lato server.
