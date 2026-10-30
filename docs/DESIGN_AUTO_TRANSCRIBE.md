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

## 2b. Interfaccia web

Endpoint REST (`web/api.py`, stesse operazioni `autotranscribe.*`, stesso token):

| Metodo e percorso | Operazione |
|---|---|
| `GET /api/autotranscribe` | `autotranscribe.config` |
| `PUT /api/autotranscribe/global/{proto}` `{enabled}` | `autotranscribe.set_global` |
| `GET /api/autotranscribe/chat?proto=&contact_id=` | `autotranscribe.chat` |
| `PUT /api/autotranscribe/chat?proto=&contact_id=` `{enabled: bool\|null}` | `autotranscribe.set_chat` (`null` cancella l'override) |

Il `contact_id` e' un query parameter (come `/api/messages`), non un segmento di
percorso. Protocollo non valido: 422; valori non validi: 400.

- **Impostazioni** (dialog accanto alla chiave OpenAI): sezione "Trascrizione
  automatica" con un interruttore per protocollo collegato; nota se la
  trascrizione non e' configurata.
- **Chat** (Gmail escluso): icona microfono nell'intestazione (stile del pulsante Archivia) che apre un piccolo menu a comparsa (stesso schema del menu principale, chiusura con clic esterno o Esc) con le opzioni "Predefinito
  (globale: attiva/disattiva)", "Attiva", "Disattiva" (spunta su quella
  corrente) e una riga con lo stato effettivo; l'icona è piena se la
  trascrizione è attiva, attenuata e barrata se disattiva, con un puntino
  se la chat ha un override. Si carica all'apertura della chat e salva alla
  scelta. Nessuna chiamata a `mark_read`.

## 2c. Impostazioni del servizio di trascrizione (web)

Operazioni `transcribe.settings` (lettura) e `transcribe.set_settings`
(aggiornamento parziale, stessa forma di risposta), in `server/ops/transcribe.py`;
scrittura con `_update_config` (stesso lock). Risposta: `{configured, api_key_set,
model, language, base_url, timeout, env_overrides}` (valori effettivi; la chiave
API non viene mai restituita). `env_overrides` elenca i campi forzati da variabili
d'ambiente (`TRANSCRIPTION_MODEL|LANGUAGE|BASE_URL|TIMEOUT`), che hanno la
precedenza su `config.json`; la UI li mostra disabilitati ("impostato da
variabile d'ambiente").

| Campo | Validazione |
|---|---|
| `model` | stringa non vuota, max 100 caratteri |
| `language` | `""` (auto) oppure 2-3 lettere minuscole (es. `it`) |
| `base_url` | `""` (predefinito) oppure URL `http(s)://host...` (max 300; `/` finale rimosso) |
| `timeout` | intero 5..600 (secondi) |

I campi omessi restano invariati; `language`/`base_url` vuoti rimuovono la chiave
(valore predefinito). Valore non valido: `invalid_request` (400) con messaggio
italiano, senza salvare nulla. Dopo il salvataggio si richiama
`api.transcription.init()`; `on_done` vive su `TranscriptionHost` (non sul
servizio), quindi il servizio ricostruito mantiene il callback
dell'auto-trascrizione (coperto da test). REST: `GET|PUT /api/transcribe/settings`
(percorso a un solo segmento: nessun conflitto con
`GET /api/transcribe/{proto}/{attachment_id}`).

UI: nel dialog "Collega backend" la sezione "Trascrizione" raggruppa chiave
OpenAI, i campi Modello / Lingua / Base URL API / Timeout (s) con "Salva" e
feedback inline, e i selettori "Trascrizione automatica".

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
- `tests/test_autotranscribe.py`, `tests/test_transcribe_settings.py` (impostazioni del servizio)
- `web/api.py`, `web/static/{index.html,app.js,style.css}`, `tests/test_web_autotranscribe.py` (interfaccia web)
