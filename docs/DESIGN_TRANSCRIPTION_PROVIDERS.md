# DESIGN: Provider di trascrizione (OpenAI, OpenRouter, Gemini)

Stato: **implementato**
Estende `docs/DESIGN_AUTO_TRANSCRIBE.md`: il servizio di trascrizione resta uno
(`TranscriptionHost`), ma il client cloud dipende dal provider scelto.

## 1. Configurazione

Stesso formato di botty. Precedenza `_get`: variabile d'ambiente > `config.json`
> default; scritture con `_update_config` (sotto lock).

- `ai_provider` / `AI_PROVIDER`: `openai` (default) | `openrouter` | `gemini`;
  un valore sconosciuto ricade su `openai`.
- Chiavi: `openai_api_key`, `openrouter_api_key`, `gemini_api_key` (env
  `OPENAI_API_KEY`, `OPENROUTER_API_KEY`, `GEMINI_API_KEY`, con fallback
  `GOOGLE_API_KEY`; fallback `.env` come prima).
- Modello e base URL per provider: `<provider>_transcription_model`,
  `<provider>_base_url` (OpenAI: `transcription_base_url` invariata; il modello
  OpenAI legge `openai_transcription_model` e ricade su `transcription_model`,
  quindi nessuna migrazione). Default: `gpt-transcribe`,
  `openai/whisper-large-v3`, `gemini-flash-latest`.
- OpenRouter: `openrouter_http_referer` e `openrouter_app_title` (header
  `HTTP-Referer` / `X-OpenRouter-Title`, inviati solo se valorizzati).
- Lingua e timeout condivisi. "Configurata" = il provider scelto ha la chiave.
- DeepSeek escluso (nessun input audio), come Google Cloud Speech.

## 2. Client (`transcription/client.py`)

`build_client(provider, api_key, base_url=, model=, language=, timeout=,
extra_headers=)`.

- **OpenAI**: multipart `POST {base}/audio/transcriptions` (invariato).
- **OpenRouter**: sottoclasse del client OpenAI (stesso multipart, campo
  `language`), con gli header opzionali.
- **Gemini**: `POST {base}/models/{model}:generateContent`, header
  `x-goog-api-key`, audio inline base64 + prompt in inglese ("verbatim, solo
  testo, nessun commento, niente se non c'e' parlato", con la lingua se
  impostata), `temperature: 0`. Mime: wav, mp3, aiff, aac, ogg (anche
  `.oga`/`.opus`), flac; altri formati via ffmpeg in mp3. Limite 15 MB di audio
  (la richiesta inline arriva a circa 20 MB in base64). Risposta: testo di
  `candidates[0].content.parts[*]`; risposta vuota = testo vuoto; blocchi
  (`promptFeedback.blockReason`, `finishReason` SAFETY...) e errori HTTP/rete
  diventano `TranscriptionError` in italiano. La chiave non compare mai in
  errori o log.

## 3. Operazioni e API

- `transcribe.settings`: `provider`, `providers` (per provider `api_key_set`,
  `model`, `base_url`; OpenRouter anche `http_referer`, `app_title`), campi
  top-level `model`/`base_url`/`api_key_set` del provider scelto, `language`,
  `timeout`, `env_overrides` (`provider`, `language`, `timeout`,
  `<provider>.model`, `<provider>.base_url`, `openrouter.http_referer`,
  `openrouter.app_title`). Mai le chiavi.
- `transcribe.set_settings`: `provider`, `providers: {nome: {model?, base_url?,
  http_referer?, app_title?}}`, `language`, `timeout`; `model`/`base_url`
  top-level valgono per il provider scelto. Validazione: Gemini
  `[a-z0-9.-]+` (prefisso `models/` rimosso), OpenRouter con `/`, referer URL
  http(s), titolo una riga <= 100 caratteri. Tutto o niente.
- `transcribe.set_api_key {provider, api_key}` (`PUT /api/transcribe/api-key`):
  OpenAI `sk-`, OpenRouter `sk-or-`, Gemini >= 20 caratteri; niente spazi.
  `config.set_openai_key` delega.
- Dopo ogni modifica il servizio viene ricreato; l'hook `on_done` resta.

## 4. Web

Impostazioni -> Trascrizione: select Provider, riga chiave del provider
(Configurata / Non configurata, Salva / Cambia), Modello, Base URL, (solo
OpenRouter) HTTP-Referer e Titolo app, Lingua, Timeout. Cambiare provider
aggiorna i campi senza salvare (bozze per provider); "Salva" invia provider e
solo i campi cambiati. I campi forzati da variabili d'ambiente sono disabilitati.
