# DESIGN: Trascrizione automatica delle note vocali e comandi `!`

Stato: **implementato**
Scope: WhatsApp, Signal e Telegram (Gmail escluso). Trascrive le note vocali
e posta il testo nella stessa chat come risposta citata dall'account
dell'utente; comandi `!` solo per la trascrizione. Porting della funzione del
bot WhatsApp "botty".

---

## 1. Impostazioni

Persistite in `config.json` (helper in `transcription/config.py`, scrittura
sotto lock: si carica l'intero dict, si aggiorna, `json.dump(indent=2)`):

```json
{
  "auto_transcribe": {"whatsapp": true, "signal": false, "telegram": false},
  "auto_transcribe_chats": {"whatsapp:39333@c.us": false, "telegram:42": true}
}
```

- interruttore **globale per protocollo** (default spento);
- **override per chat** `true`/`false`/assente (chiave `"<protocollo>:<contact_id>"`);
- valore effettivo = override se presente, altrimenti il globale del protocollo.

Stesse funzioni per i comandi di chat e per le operazioni server
`autotranscribe.config`, `autotranscribe.set_global`, `autotranscribe.chat`,
`autotranscribe.set_chat` (`server/ops/transcribe.py`), così le due vie restano
allineate (usate dalla futura UI web).

## 2. Flusso

`server/autotranscribe.py` (`AutoTranscriber`) viene avviato da
`ChatCore.start` e fermato da `ChatCore.shutdown`. Un thread daemon legge
l'`EventBus` (un evento `GAP` viene solo loggato) e reagisce ai nuovi eventi
`message` (`added is True`). Il lavoro bloccante (download lazy via
`media.attachment`, lookup DB, invio) gira in thread daemon dedicati: mai sul
pump del core.

- **Finestra di freschezza**: si gestiscono solo messaggi con timestamp non
  più vecchio di `RECENT_WINDOW_S` (10 min) e non anteriore all'avvio del core
  meno `START_MARGIN_S` (60 s). Le risincronizzazioni (`_resync_wa_history`)
  riemettono eventi `message` di storico: restano fuori.
- **Voce** (`media_kind == "voice"`, in arrivo o propria): se l'impostazione
  effettiva è attiva e il servizio è configurato, `TranscriptionService.submit`
  (percorso risolto come `transcribe.submit`). Senza chiave OpenAI: nessuna
  azione (log una sola volta).
- **Completamento**: `TranscriptionService` accetta `on_done(protocol,
  attachment_id, row)`, invocata dal worker dopo `ok`/`failed` (cablata in
  `TranscriptionHost.init`). Se il record è già `ok` si posta subito.
- **Risposta**: `messages.send` (senza `optimistic`) con testo
  `"🎤 Trascrizione:\n\n" + testo`, solo testo semplice. Errore: breve messaggio
  italiano. Una sola risposta per nota (`(protocollo, attachment_id)`, anche
  tra `!t` e auto); un fallimento di invio o di trascrizione permette un
  nuovo tentativo.

### Citazione per protocollo

| Protocollo | Campi di `messages.send` |
|---|---|
| WhatsApp, Telegram | `reply_to_message_id` = id del messaggio citato |
| Signal | `quote_timestamp` (ms) + `quote_author` (= contact id, come la TUI) |

### Comandi (solo messaggi propri, `is_mine`, testo che inizia con `!`, finestra di freschezza)

- `!t` in risposta a una nota vocale: trascrive la nota e risponde citando la
  nota (non il comando). Il messaggio citato si trova nel DB: per
  WhatsApp/Telegram da `reply_to_message_id` (`msg_id`), per Signal da
  `quote_timestamp`. Senza citazione o se non è vocale: suggerimento in italiano.
- `!transcription`: stato (globale, chat, effettivo) e uso.
- `!transcription on|off`: override della chat.
- `!transcription global on|off`: globale del protocollo.
- Argomenti non validi: messaggio d'uso. Le risposte ai comandi citano il comando.

### Sicurezza ed effetti collaterali

- Le risposte del bot rientrano come `message` con `is_mine` (Signal via
  `sent_mirror`): non iniziano mai con `!` e non sono note vocali, quindi non
  vengono rielaborate; ogni evento è anche deduplicato.
- Nessuna chiamata a `mark_read`/`sendSeen` e nessun cambio del flag `read`
  della nota: l'unico chiamante è l'operazione `chats.mark_read`.

## 3. Limiti noti

- Signal: `quote_author` è il contact id anche per note vocali proprie (come
  la TUI); il numero dell'account non è disponibile nel core. Non si invia il
  testo sintetico della nota nella citazione.
- La deduplicazione è in memoria: dopo un riavvio vale la finestra di
  freschezza.

## 4. File toccati

- `server/autotranscribe.py` (nuovo), `server/core.py`
- `server/transcribe.py`, `transcription/service.py` (callback `on_done`)
- `transcription/config.py` (getter/setter), `server/ops/transcribe.py`,
  `server/models_api.py` (operazioni `autotranscribe.*`)
- `tests/test_autotranscribe.py`
