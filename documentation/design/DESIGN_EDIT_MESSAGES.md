# Design — editing dei messaggi (ottimistico con rollback, update in-place)

Come si modifica un messaggio proprio già inviato e come viene applicato un edit ricevuto da altri, su tutti e tre i protocolli. Ricavato da `tui/edit.py`, `tui/send.py`, `tui/events.py`, `tui/client_calls.py`, `megachatd/internal/core/ops_edit.go` e dai backend di protocollo (`internal/proto/*/send.go`).

## 1. Scope e gate di ingresso

Modificabili i **messaggi propri testuali già inviati**; gli edit ricevuti sono **update in place** della bolla esistente (mai una nuova). Fuori scope: edit di media/caption/sticker, cronologia multi-versione (il testo viene sovrascritto), delete-for-everyone.

Ingresso (Alt+click o Alt+E su una bolla → `MessageWidget.EditRequested`, gestito da `tui/edit.py::on_message_widget_edit_requested`). Il gate è a cascata; ogni rifiuto produce un messaggio in status bar e non tocca lo stato:

| Guardia | Effetto |
|---|---|
| download mode attivo (Ctrl+D) | richiesta ignorata (i click servono i file) |
| `is_mine=False` | ❌ "You can only edit your own messages" |
| `status ∈ {pending, failed}` | ❌ "Message not sent yet — cannot edit" |
| entry assente nella cache UI (match per `is_mine` + `timestamp`) | ❌ "Message not found in cache" |
| `msg_type != "text"` | ❌ "Only text messages can be edited" |
| id server assente (WhatsApp/Telegram) | ❌ "Server message ID unavailable — reopen the chat" |
| id server assente (Signal) | ⚠️ avviso non bloccante: `message_id = str(ts)` e "la modifica potrebbe non propagarsi" (5 s) — per Signal l'id di edit È il timestamp originale |

A valle del gate:

- **mutua esclusione reply↔edit**: aprire un edit cancella la reply pendente (`_cancel_reply`) e viceversa;
- stato `_editing_message`: snapshot `{protocol, contact_id, cache_key, timestamp, message_id, old_text, _widget}`; la bolla target è evidenziata, l'input è precaricato col testo vecchio (cursore a fine) e la reply bar mostra `✏️ Modifica: <testo>`;
- il submit passa dal normale flusso Enter: `tui/send.py::on_message_text_area_submitted` vede `_editing_message` valorizzato e devia su `_submit_edit(message)` (dopo alias-emoji e strip).

## 2. Operazione `messages.edit`

Il server è l'unico che applica la modifica (backend + SQLite + cache del core). Request:

```json
{"protocol": "signal|whatsapp|telegram", "contact_id": "...", "message_id": "...", "new_text": "..."}
```

Semantica di `message_id` per protocollo:

- **signal**: timestamp (ms) del messaggio originale, come stringa — `editTimestamp` sul filo;
- **telegram**: id server (intero come stringa);
- **whatsapp**: id messaggio stabile (whatsmeow).

Response ed evento `message_edit` portano `EditInfo`:

```python
{
    "message_id": str,
    "timestamp": int,   # ts della entry, MAI modificato
    "old_text": str,
    "text": str,
    "is_mine": bool,
}
```

Vincoli: il target deve esistere, essere testuale e proprio; un errore del server è un `ApiError` con codice e messaggio (il worker fa rollback). Il testo identico è un no-op lato server.

## 3. Flusso ottimistico con rollback (`tui/edit.py`)

```
submit (UI thread)                worker thread                     UI thread
──────────────────                ─────────────                     ──────────
_submit_edit(new_text)
  ├ testo identico → solo cancel
  ├ _apply_local_edit(snap)   ─►  _edit_message_worker
  │   (solo vista: cache UI,      ├ esito ignoto (timeout/conn) ──► mantiene il testo, avvisa
  │    identità, widget)          ├ errore server ───────────────► _restore_local_edit
  └ _cancel_edit()                └ ok  ─────────────────────────► status "✏️ Message edited"
```

- **Applica locale** (`_apply_local_edit`): aggiorna solo la vista — entry della cache UI (`text`, `edited=True`), identità di render, `widget.update_text(new_text)`. **Nessuna** scrittura di backend/DB sul thread UI.
- **Worker** (`_edit_message_worker`, thread): chiama `messages.edit`. Un errore certo torna alla UI con `call_from_thread(self._restore_local_edit, ...)`; un esito ignoto (timeout o connessione persa) **non** fa rollback: il testo resta e la risincronizzazione mostra ciò che il server ha davvero.
- **Rollback** (`_restore_local_edit`): ripristino speculare e completo — entry della cache UI al testo vecchio con `edited=False`, chirurgia identità inversa (new→old), widget con `update_text(old_text, edited=False)`, status `❌ Edit failed: <causa>`.

## 4. Chirurgia dell'identità (`_rewrite_message_identity`)

L'identità di render è `(protocol, cache_key, ts, text)` (+ variante `(protocol, cache_key, id, text)`); un edit cambia SOLO il testo, quindi senza intervento `_refresh_chat` rimonterebbe il messaggio modificato come NUOVO (duplicato) e `_shown_in_log` non lo riconoscerebbe più.

```python
for s in (self._seen_message_ids, self._shown_in_log):
    s.discard((protocol, cache_key, int(ts), old_text))
    s.add((protocol, cache_key, int(ts), new_text))
    if message_id:
        s.discard((protocol, cache_key, str(message_id), old_text))
        s.add((protocol, cache_key, str(message_id), new_text))
# _seen_timestamps NON si tocca: il timestamp non cambia mai.
```

La stessa funzione è riusata dall'handler degli edit ricevuti e, in forma inversa, dal rollback.

## 5. Edit ricevuti: update in place (`tui/events.py::_view_edit`)

L'evento `message_edit` porta `{protocol, contact_id, contact, info: EditInfo, text}`; il core ha già applicato la modifica a backend e SQLite. Il dispatch UI ordina typing → receipt → **message_edit** → message → sent-mirror → contact_update.

1. Match nella cache UI **per id** (`info.message_id`), poi fallback `(timestamp, is_mine, text == old_text)`.
2. Su hit: `_rewrite_message_identity(...)` + `target["text"] = new_text`, `target["edited"] = True`.
3. Widget solo se la chat è aperta (`_update_edited_widget`): match per `_message_id`, poi `(_msg_timestamp, _msg_text == old_text)`; `update_text(new_text)` riscrive la bolla ESISTENTE — mai mount di una bolla nuova, mai bump unread.

### Edit riferito a un messaggio mai visto

- **Signal / Telegram**: l'evento arriva; se il target non è nella cache di vista la TUI non crea bolle finché un fetch storico non porta il messaggio (già editato). Il server ha applicato la modifica alla riga se presente.
- **WhatsApp**: il server emette l'edit quando riconosce il target; la vista lo applica se la riga esiste.

## 6. Marker "(modificato)" e persistenza

- Rendering: `MessageWidget._build_content` appende `" (modificato)"` quando `_edited`; `update_text(new_text, edited=True)` aggiorna testo+flag in place.
- Persistenza: colonna `edited INTEGER NOT NULL DEFAULT 0` in `messages`; il merge della vista marca `edited=True` quando la riconciliazione aggiorna un testo in place.
- Un doppio edit consecutivo è supportato: il secondo gate rilegge la cache aggiornata.

## 7. Limiti noti

1. **Nessuna atomicità tra i layer**: cache UI, identità e widget sono mutazioni sequenziali; un crash a metà lascia stati divergenti fino al reload della chat.
2. **Edit su messaggio mai visto**: nessuna bolla su Signal/Telegram finché non arriva lo storico; il DB resta coerente.
3. **Signal senza id server**: l'edit parte con `message_id = str(ts ottimistico)`; se signal-cli aveva assegnato un ts diverso, la modifica può non propagarsi (avviso esplicito in status bar).
4. **Solo ultima versione**: il testo precedente è perso (nessuna cronologia edit, scelta di scope).
5. **Esito ignoto**: dopo un timeout non c'è rollback; la vista può mostrare il nuovo testo finché la risincronizzazione non riallinea.

## Test che documentano questo design

- `tests/test_tui_edit_ops.py` — invio di `messages.edit`, rollback su errore, esito ignoto.
- `tests/test_ui_components.py` — suffisso "(modificato)", `update_text`, Alt+click/Alt+E.
- `tests/test_refresh_chat.py` — nessun duplicato dopo un edit (identità di render).
- `megachatd/internal/core/ops_edit_reactions_test.go` e i test di protocollo — applicazione dell'edit lato server.
