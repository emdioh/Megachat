# Design — identità dei messaggi, cache e batching

Come il sistema identifica i messaggi, quanti livelli di cache esistono, come funziona la dedup e perché il rendering è differito/raggruppato. Ricavato da `models.py`, `tui/events.py`, `tui/chat_view.py`, `tui/edit.py`, `tui/unread_reply.py`, `tui/backend_connect.py`, `client/events_api.py`, `megachatd/internal/core` e `megachatd/internal/store`.

## 1. Il problema: l'identità non è un timestamp

I timestamp hanno granularità al secondo (e alcuni protocolli riscrivono il timestamp client con quello server). Due messaggi distinti nello stesso secondo sono indistinguibili se si usa solo `ts`; un edit cambia il testo mantenendo ts; un echo può arrivare prima o dopo la risposta dell'invio. Da qui le scelte di design:

- **Chiave contatto namespaced**: `contact_cache_key(protocol, id) = f"{protocol}:{id}"` — lo stesso numero su Signal e WhatsApp è un contatto diverso.
- **Identità di rendering**: `(protocol, cache_key, timestamp, text)`; in aggiunta viene tracciata anche `(protocol, cache_key, message_id, text)` quando esiste un id.
- **Identità temporale immutabile**: un edit cambia SOLO il testo (e la flag `edited`) — mai `timestamp` né `id`.

Tre set di dedup convivono nella UI:

| Set | Chiave | Scopo |
|---|---|---|
| `_seen_timestamps` | `(protocol, key, ts)` | filtro "solo più nuovi" di `_refresh_chat` |
| `_seen_message_ids` | `(protocol,key,ts,text)` + `(protocol,key,id,text)` | dedup stessa-secondo e stabilità via id |
| `_shown_in_log` | come sopra | guardia finale dentro `_add_message`: mai montare due volte la stessa bolla nella vista corrente |

All'edit, `_rewrite_message_identity()` scarta le vecchie identità `(…, old_text)` e registra quelle nuove `(…, new_text)` nei set, altrimenti `_refresh_chat` rimonterebbe il messaggio modificato come nuovo duplicato. `_seen_timestamps` non si tocca (il ts non cambia).

## 2. I livelli di cache

```
backend di protocollo (signal/whatsapp/telegram)
      │  ingest nel core (dedup, upgrade id, persistenza)
      ▼
[1] store SQLite  ~/.local/share/signal-tui-client/messages.db  (verità tra sessioni)
      │  operazioni messages.snapshot / messages.window / messages.page
      ▼
[2] cache di vista della TUI  self._cache {cache_key "proto:id": [msg_dict]}
      │  eventi del bus (message, receipt, message_edit) + merge add-only
      ▼
[3] widget nel #chat-log        bolle MessageWidget/ImageWidget (identità in _shown_in_log)
```

- **Livello [1]** è il database del server: schema `user_version = 5`, dedup esatta e per id, rank guard, pruning per contatto. È la fonte di verità: più TUI/SPA condividono lo stesso store.
- **Livello [2]** è una copia indipendente (`messages.snapshot` e `messages.window` restituiscono **copie** wire): alimenta lista contatti (unread, `last_message_ts`), finestra di chat e reload. Il merge è add-only verso il server (`_merge_window_rows` non può "restringere" la vista).
- **Livello [3]** è volatile: svuotato a ogni cambio contatto (`_clear_chat` resetta anche i set).

Il core mantiene inoltre la propria cache in memoria per chat, usata dagli handler delle operazioni; non è visibile al client e non va confusa con `self._cache`.

## 3. Regole di dedup

### Store (server, cross-sessione)

- ingest idempotente: un messaggio identico non produce una riga nuova (`added=false`);
- una riga id-less può essere aggiornata da un echo con id reale entro `EchoMatchWindowMs = 600000`, senza cambiare il timestamp;
- dedup esatta `(protocol, contact, timestamp, text, is_mine)` e dedup per `(protocol, contact, msg_id, text, attachment_id)`: sopravvive la riga col rank di stato più alto (poi rowid più bassa), assorbendo i campi quote/media mancanti;
- partizioni con timestamp divergenti oltre la finestra echo non vengono fuse: sono loggate e lasciate intatte.

### Vista (TUI)

- match id-first, anche per gli edit (l'id resta, il testo cambia — il confronto sul testo deve venire dopo);
- incoming senza id affidabile: testo + `±5 s`; outgoing con id: testo + finestra echo 10 min;
- un esistente testuale con testo diverso viene aggiornato in place e marcato `edited=True` (riconciliazione dell'edit);
- gli status salgono mai scendono (`_status_rank`);
- `_merge_window_rows` ritorna `changed` solo se sono stati aggiunti messaggi o modificato un testo: un upgrade di soli status NON causa re-render.

Le finestre sono **euristiche, non garanzie**: due messaggi identici nella stessa finestra collassano (costo accettato per assorbire ridelivery ed eco).

## 4. Batching del rendering ("debounce")

Le scritture SQLite sono incrementali e l'evento è già il risultato dell'ingest; il "debounce" sopravvive a livello di **rendering della lista contatti**:

- durante un batch di eventi, nessuna mutazione visiva immediata oltre alla bolla della chat aperta;
- flag `_contact_list_dirty` + `_dirty_contact_keys` (set dei cache_key toccati);
- a fine giro del poll worker (`tui/polling.py`):
  - unread ricalcolato **nei soli dati** (`_recompute_unread(key)` O(M) per contatto toccato; se i contatti toccati superano `_CONTACT_UPDATE_BATCH_MAX = 4`, ricalcolo completo);
  - UN solo sort + render (`_reorder_contact_list`);
  - refresh della status bar solo se nessun messaggio transiente è in mostra.
- il conteggio unread deduplica per `(timestamp, text)` (`tui/unread_reply.py::_recompute_unread`) così le ridelivery non gonfiano i badge.

## 5. Finestra di chat e reload

- All'apertura: render immediato delle ultime **20** voci dalla cache di vista (fase 1); per WhatsApp segue `messages.window` con `refresh=true` e `refresh_limit=50` per riconciliare lo storico remoto (fase 2), con retry breve; il merge è add-only e un eventuale re-render avviene una sola volta se qualcosa è cambiato.
- Il banner "load more" appare quando la cache ha più di 20 messaggi; il click carica tutta la cache (`_load_all_messages`).
- `_refresh_chat` ordina per timestamp prima di slice-are la finestra e aggiunge solo messaggi più nuovi (o distinti nello stesso secondo): evita che la chiusura di picker/modali ri-appenda la storia.
- I worker verificano `_is_stale()` (token + contatto corrente) prima di ogni mount: nessuna bolla atterra nella chat sbagliata.

## 6. Limiti noti

1. **Stesso testo nella finestra**: due messaggi distinti con identico testo che cadono nella finestra di dedup della vista collidono in un'unica entry.
2. **Fallback per testo ambigua**: l'aggiornamento di status/receipt per testo può colpire la riga sbagliata con testi ripetuti; il match per id è preferito ovunque.
3. **Gate `ts=0`**: un evento con `timestamp=0` (payload malformato) finisce nella cache ma non viene montato live (`if ts and …`), quindi la vista resta indietro finché non si riapre la chat.
4. **Nessuna atomicità tra vista e server**: cache di vista, identità e widget sono passi sequenziali; un crash a metà lascia stati divergenti fino al reload.
5. **Copie, non riferimenti**: le righe della vista sono copie dei payload wire; un evento perso (gap) richiede una risincronizzazione (`messages.snapshot` + `contacts.for_protocol`).

## Test che documentano questo design

- `tests/test_refresh_chat.py` — finestra 20, stessi-secondo, ordine stabile, no ri-append.
- `tests/test_pick_survivor_and_merge.py` — scelta della riga sopravvissuta e merge.
- `tests/test_backend_connect.py` — merge snapshot + contatti a backend pronto.
- `tests/test_tui_edit_ops.py` — chirurgia dell'identità all'edit.
- `megachatd/internal/store/dedup_test.go`, `messages_test.go` — dedup e semantica dello store.
