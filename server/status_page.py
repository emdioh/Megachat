"""Minimal status page: WhatsApp link state, the pairing QR, transcription.

A tiny stdlib HTTP server (no FastAPI, no web UI) meant for the people who
own a secondary instance (``scripts/megachat_instance.sh``): they open one
bookmarked URL and see whether their WhatsApp is linked, scan the QR (or ask
for a pairing code when the page is open on the phone itself) and check that
transcription is configured.

Enabled by the ``status_page`` section of ``config.json``::

    "status_page": {"enabled": true, "host": "0.0.0.0", "port": 4301,
                    "token": "<random>"}

Every URL lives under the secret ``/<token>/`` prefix (anything else is a
404), so the token is the only credential: serve it over a VPN / tunnel /
reverse proxy with TLS when it leaves the LAN.  Routes:

* ``GET  /<token>/``            the HTML page (polls status.json)
* ``GET  /<token>/status.json`` the same data as JSON
* ``GET  /<token>/qr.png``      the pairing QR (only while waiting for a scan)
* ``POST /<token>/start``       start (or restart a FAILED) WhatsApp session
* ``POST /<token>/pair``        pairing code for ``phone`` (form field)

It talks to WAHA directly (its own short-timeout requests), so it keeps
working while the core is still connecting.  Stdlib + ``qrcode`` only.
"""

from __future__ import annotations

import hmac
import html
import json
import logging
import re
import threading
import time
from collections.abc import Callable
from dataclasses import dataclass
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from typing import Any
from urllib.parse import parse_qs

logger = logging.getLogger(__name__)

__all__ = [
    "StatusPageHandle",
    "collect_status",
    "normalize_phone",
    "render_page",
    "start",
    "stop",
]

#: Seconds between two pairing-code requests (WhatsApp rate-limits them).
PAIR_COOLDOWN_S = 30
_REFRESH_S = 15
_WAHA_TIMEOUT_S = 5

_SCAN_STATES = {"scan_qr", "scan_qr_code", "unpaired", "pending"}
_LINKED_STATES = {"working", "connected", "authenticated", "ready", "open"}


def _default_rest() -> Any:
    from protocols.config import resolve_whatsapp_api_url
    from protocols.whatsapp_rest import WhatsAppRESTClient

    return WhatsAppRESTClient(resolve_whatsapp_api_url())


def _transcription() -> dict[str, Any]:
    from transcription import config as tconfig

    provider = tconfig.get_transcription_provider()
    return {
        "provider": provider,
        "configured": tconfig.get_api_key(provider) is not None,
        "auto_whatsapp": tconfig.get_auto_transcribe_global("whatsapp"),
    }


def collect_status(rest: Any) -> dict[str, Any]:
    """WhatsApp state from WAHA, normalised to one ``state`` keyword.

    ``linked`` | ``qr`` | ``starting`` | ``stopped`` | ``failed`` |
    ``unreachable`` | ``unknown``.
    """
    raw = rest._request(
        "GET",
        f"/api/sessions/{rest.session_name}",
        timeout=_WAHA_TIMEOUT_S,
        log_level=logging.DEBUG,
    )
    info: dict[str, Any] = {"session": rest.session_name, "account": None}
    if raw is None:
        code = rest.last_status
        error = " ".join(str(rest.last_error or "").split())[:200]
        url = getattr(rest, "base_url", "")
        if code == 404:
            info["state"] = "stopped"
        elif code in (401, 403):
            info["state"] = "unknown"
            info["detail"] = (
                f"WAHA ({url}) rifiuta la chiave API (HTTP {code}): WAHA_API_KEY "
                "nel .env dell'istanza non corrisponde a quella del container"
            )
        elif code:
            info["state"] = "unknown"
            info["detail"] = f"WAHA ({url}) risponde HTTP {code}: {error}"
        else:
            info["state"] = "unreachable"
            info["detail"] = f"{url}: {error or 'nessuna risposta'}"
        return info
    if not isinstance(raw, dict):
        return {**info, "state": "unknown", "detail": "risposta inattesa da WAHA"}
    waha_state = str(raw.get("status") or "").lower()
    info["waha_status"] = waha_state.upper()
    me = raw.get("me") if isinstance(raw.get("me"), dict) else None
    if me:
        number = str(me.get("id") or "").split("@", 1)[0]
        info["account"] = {"name": me.get("pushName") or "", "number": number}
    if waha_state in _LINKED_STATES and me:
        info["state"] = "linked"
    elif waha_state in _SCAN_STATES:
        info["state"] = "qr"
    elif waha_state == "starting":
        info["state"] = "starting"
    elif waha_state in ("stopped", "stop", ""):
        info["state"] = "stopped"
    elif waha_state == "failed":
        info["state"] = "failed"
    else:
        info["state"] = "unknown"
    return info


def normalize_phone(raw: str) -> str | None:
    """Digits of an international number (``+39 333…``/``0039…``), or ``None``."""
    text = raw.strip()
    text = text.removeprefix("00")
    digits = re.sub(r"[\s\-().+]", "", text)
    if not digits.isdigit() or not 8 <= len(digits) <= 15:
        return None
    return digits


# ─── HTML ────────────────────────────────────────────────────────────────────

_STATE_TEXT = {
    "linked": ("✅", "WhatsApp collegato"),
    "qr": ("📱", "In attesa di collegamento"),
    "starting": ("⏳", "Avvio della sessione in corso…"),
    "stopped": ("⏸️", "Sessione WhatsApp ferma"),
    "failed": ("⚠️", "Sessione WhatsApp in errore"),
    "unreachable": ("❌", "Servizio WhatsApp (WAHA) non raggiungibile"),
    "unknown": ("❓", "Stato sconosciuto"),
}

_CSS = """
:root{--bg:#f6f7f9;--card:#fff;--fg:#1c1e21;--muted:#65676b;--ok:#1a7f37;
--warn:#b35900;--bad:#c62828;--accent:#128c7e;--border:#dfe1e5}
@media (prefers-color-scheme:dark){:root{--bg:#111b21;--card:#1f2c33;
--fg:#e9edef;--muted:#8696a0;--ok:#4ade80;--warn:#fbbf24;--bad:#f87171;
--accent:#25d366;--border:#2a3942}}
*{box-sizing:border-box}
body{margin:0;background:var(--bg);color:var(--fg);
font:16px/1.45 system-ui,-apple-system,"Segoe UI",Roboto,sans-serif}
main{max-width:520px;margin:0 auto;padding:16px}
h1{font-size:1.25rem;margin:8px 0 16px}
.card{background:var(--card);border:1px solid var(--border);border-radius:12px;
padding:16px;margin-bottom:16px}
.state{font-size:1.15rem;font-weight:600}
.muted{color:var(--muted);font-size:.9rem}
.ok{color:var(--ok)}.warn{color:var(--warn)}.bad{color:var(--bad)}
.qr{display:block;width:100%;max-width:320px;margin:12px auto;background:#fff;
padding:8px;border-radius:8px;image-rendering:pixelated}
ol{padding-left:20px;margin:8px 0}
form{margin-top:12px;display:flex;gap:8px;flex-wrap:wrap}
input{flex:1 1 180px;padding:10px;border-radius:8px;border:1px solid var(--border);
background:var(--bg);color:var(--fg);font-size:1rem}
button{padding:10px 16px;border:0;border-radius:8px;background:var(--accent);
color:#fff;font-size:1rem;cursor:pointer}
.code{font:700 1.8rem/1.2 ui-monospace,monospace;letter-spacing:.12em;
text-align:center;margin:12px 0}
dl{display:grid;grid-template-columns:auto 1fr;gap:4px 12px;margin:0}
dt{color:var(--muted)}dd{margin:0}
"""


#: Polls status.json: reloads the page when the WhatsApp state changes,
#: otherwise only renews the QR image (so a phone number being typed is kept).
#: Without JavaScript the <noscript> meta refresh does a plain reload.
_POLL_JS = """
(function(){var s=document.body.dataset.state,q=document.querySelector('.qr');
setInterval(function(){fetch('status.json',{cache:'no-store'}).then(function(r){
return r.json()}).then(function(d){var n=(d.whatsapp||{}).state;
if(n!==s){location.reload();return}if(q){q.src='qr.png?t='+Date.now()}})
.catch(function(){})},%d);})();
"""


def _page(title: str, body: str, refresh: int | None = None, state: str = "") -> str:
    meta = (
        f'<noscript><meta http-equiv="refresh" content="{refresh}"></noscript>'
        if refresh
        else ""
    )
    script = f"<script>{_POLL_JS % (refresh * 1000)}</script>" if refresh else ""
    return (
        "<!doctype html><html lang=it><head><meta charset=utf-8>"
        '<meta name=viewport content="width=device-width,initial-scale=1">'
        f'<meta name="robots" content="noindex">{meta}'
        f"<title>{html.escape(title)}</title><style>{_CSS}</style></head>"
        f'<body data-state="{html.escape(state)}"><main>{body}</main>{script}'
        "</body></html>"
    )


def _whatsapp_card(wa: dict[str, Any]) -> str:
    state = wa.get("state", "unknown")
    icon, label = _STATE_TEXT.get(state, _STATE_TEXT["unknown"])
    css = {"linked": "ok", "qr": "warn", "starting": "warn"}.get(state, "bad")
    parts = [f'<div class="state {css}">{icon} {html.escape(label)}</div>']
    account = wa.get("account")
    if state == "linked" and account:
        who = " · ".join(
            html.escape(str(v))
            for v in (account.get("name"), account.get("number"))
            if v
        )
        parts.append(f'<p class="muted">Account: {who}</p>')
    if state == "qr":
        parts.append(
            "<ol><li>Apri WhatsApp sul telefono</li>"
            "<li>Impostazioni → <b>Dispositivi collegati</b> → Collega un dispositivo</li>"
            "<li>Inquadra il codice qui sotto (si aggiorna da solo)</li></ol>"
            f'<img class="qr" src="qr.png?t={int(time.time())}" alt="QR code">'
            '<p class="muted">Stai guardando questa pagina dal telefono stesso? '
            "Fatti dare un codice: in WhatsApp scegli "
            "<b>Collega con il numero di telefono</b> e inserisci il codice.</p>"
            '<form method=post action="pair">'
            "<input name=phone inputmode=tel autocomplete=tel "
            'placeholder="+39 333 1234567" required>'
            "<button>Ottieni codice</button></form>"
        )
    elif state in ("stopped", "failed"):
        parts.append(
            '<form method=post action="start"><button>'
            f"{'Riavvia' if state == 'failed' else 'Avvia'} la sessione"
            "</button></form>"
        )
    elif state == "unreachable":
        parts.append(
            '<p class="muted">Il container WhatsApp di questa istanza non risponde: '
            "chiedi a chi gestisce il server di avviarlo "
            "(<code>megachat_instance.sh doctor</code>).</p>"
        )
    if wa.get("detail"):
        parts.append(
            f'<p class="muted"><code>{html.escape(str(wa["detail"]))}</code></p>'
        )
    return (
        '<section class="card"><h2 class="muted">WhatsApp</h2>'
        + "".join(parts)
        + "</section>"
    )


def _transcription_card(tr: dict[str, Any]) -> str:
    if tr.get("error"):
        return ""
    key = (
        '<span class="ok">configurata</span>'
        if tr.get("configured")
        else '<span class="bad">mancante</span>'
    )
    auto = (
        '<span class="ok">attiva</span>'
        if tr.get("auto_whatsapp")
        else '<span class="warn">spenta</span>'
    )
    return (
        '<section class="card"><h2 class="muted">Trascrizione note vocali</h2><dl>'
        f"<dt>Provider</dt><dd>{html.escape(str(tr.get('provider', '')))}</dd>"
        f"<dt>Chiave API</dt><dd>{key}</dd>"
        f"<dt>Automatica</dt><dd>{auto}</dd></dl>"
        '<p class="muted">Da WhatsApp, in una chat: <b>!transcription off</b> / '
        "<b>on</b> (solo quella chat), <b>!transcription global off</b> / <b>on</b> "
        "(tutte), oppure rispondi <b>!t</b> a una nota vocale.</p></section>"
    )


def render_page(status: dict[str, Any]) -> str:
    """The status HTML for a :func:`collect_status`-like dict."""
    wa = status.get("whatsapp", {})
    started = status.get("started_at")
    since = time.strftime("%d/%m/%Y %H:%M", time.localtime(started)) if started else "?"
    body = (
        f"<h1>Megachat · {html.escape(str(status.get('name') or 'stato'))}</h1>"
        + _whatsapp_card(wa)
        + _transcription_card(status.get("transcription", {}))
        + f'<p class="muted">Server attivo dal {since} · la pagina si aggiorna '
        f"ogni {_REFRESH_S} s.</p>"
    )
    return _page(
        "Megachat – stato", body, refresh=_REFRESH_S, state=wa.get("state", "")
    )


def _message_page(title: str, body_html: str) -> str:
    return _page(
        title,
        f'<h1>{html.escape(title)}</h1><section class="card">{body_html}'
        '<p><a href="./">← Torna allo stato</a></p></section>',
    )


# ─── HTTP server ─────────────────────────────────────────────────────────────


@dataclass
class StatusPageHandle:
    server: ThreadingHTTPServer
    thread: threading.Thread
    url: str


class _State:
    def __init__(self, token: str, name: str, rest_factory: Callable[[], Any]) -> None:
        self.token = token
        self.name = name
        self.rest_factory = rest_factory
        self.started_at = time.time()
        self.last_pair = 0.0
        self.lock = threading.Lock()

    def status(self) -> dict[str, Any]:
        rest = self.rest_factory()
        try:
            wa = collect_status(rest)
        except Exception as exc:
            logger.warning("Status page: WAHA status failed", exc_info=True)
            wa = {"state": "unknown", "detail": f"errore interno: {exc!r}"[:300]}
        try:
            tr = _transcription()
        except Exception:
            logger.debug("Status page: transcription config failed", exc_info=True)
            tr = {"error": True}
        return {
            "name": self.name,
            "started_at": self.started_at,
            "whatsapp": wa,
            "transcription": tr,
        }


def _make_handler(state: _State) -> type[BaseHTTPRequestHandler]:
    class Handler(BaseHTTPRequestHandler):
        server_version = "megachat-status"
        sys_version = ""

        def log_message(self, format: str, *args: Any) -> None:
            logger.debug("status page: " + format, *args)

        # -- helpers --------------------------------------------------------

        def _route(self) -> str | None:
            """The path after ``/<token>``, or ``None`` when the token is wrong."""
            path = self.path.split("?", 1)[0]
            parts = path.split("/", 2)  # ["", token, rest]
            if len(parts) < 2 or not hmac.compare_digest(
                parts[1].encode(), state.token.encode()
            ):
                return None
            if len(parts) == 2:
                return ""  # "/<token>" → redirect to "/<token>/"
            return "/" + parts[2]

        def _send(
            self, code: int, body: bytes, ctype: str, extra: dict | None = None
        ) -> None:
            self.send_response(code)
            self.send_header("Content-Type", ctype)
            self.send_header("Content-Length", str(len(body)))
            self.send_header("Cache-Control", "no-store")
            self.send_header("Referrer-Policy", "no-referrer")
            self.send_header("X-Content-Type-Options", "nosniff")
            self.send_header("X-Frame-Options", "DENY")
            for key, value in (extra or {}).items():
                self.send_header(key, value)
            self.end_headers()
            if self.command != "HEAD":
                self.wfile.write(body)

        def _html(self, code: int, text: str) -> None:
            self._send(code, text.encode("utf-8"), "text/html; charset=utf-8")

        def _not_found(self) -> None:
            self._send(404, b"Not found\n", "text/plain; charset=utf-8")

        def _redirect_home(self) -> None:
            self._send(303, b"", "text/plain", {"Location": f"/{state.token}/"})

        # -- GET ------------------------------------------------------------

        def do_HEAD(self) -> None:
            self.do_GET()

        def do_GET(self) -> None:
            route = self._route()
            if route is None:
                return self._not_found()
            if route == "":
                return self._redirect_home()
            if route == "/":
                return self._html(200, render_page(state.status()))
            if route == "/status.json":
                body = json.dumps(state.status(), ensure_ascii=False).encode()
                return self._send(200, body, "application/json")
            if route == "/qr.png":
                return self._qr()
            return self._not_found()

        def _qr(self) -> None:
            rest = state.rest_factory()
            if collect_status(rest).get("state") != "qr":
                return self._not_found()
            raw = rest.get_session_qr()
            if isinstance(raw, str) and raw:
                import base64

                from server.link import qr_as_png

                png = qr_as_png({"kind": "text", "data": raw})
                raw = base64.b64decode(png["data"]) if png["kind"] == "png" else None
            if not isinstance(raw, bytes) or not raw:
                return self._not_found()
            self._send(200, raw, "image/png")

        # -- POST -----------------------------------------------------------

        def _form(self) -> dict[str, str]:
            try:
                length = min(int(self.headers.get("Content-Length") or 0), 4096)
            except ValueError:
                length = 0
            raw = self.rfile.read(length).decode("utf-8", "replace") if length else ""
            return {k: v[0] for k, v in parse_qs(raw).items() if v}

        def do_POST(self) -> None:
            route = self._route()
            if route is None:
                return self._not_found()
            if route == "/start":
                return self._start()
            if route == "/pair":
                return self._pair(self._form().get("phone", ""))
            return self._not_found()

        def _start(self) -> None:
            rest = state.rest_factory()
            current = collect_status(rest).get("state")
            if current == "failed":
                logger.info("Status page: restarting the FAILED WhatsApp session")
                rest.restart_session()
            elif current == "stopped":
                logger.info("Status page: starting the WhatsApp session")
                rest.start_session()
                rest.configure_webhook()
            self._redirect_home()

        def _pair(self, phone_raw: str) -> None:
            phone = normalize_phone(phone_raw)
            if phone is None:
                return self._html(
                    400,
                    _message_page(
                        "Numero non valido",
                        "<p>Scrivi il numero con il prefisso internazionale, "
                        "per esempio <b>+39 333 1234567</b>.</p>",
                    ),
                )
            with state.lock:
                wait = PAIR_COOLDOWN_S - (time.monotonic() - state.last_pair)
                if state.last_pair and wait > 0:
                    return self._html(
                        429,
                        _message_page(
                            "Attendi un momento",
                            f"<p>Riprova tra {int(wait) + 1} secondi.</p>",
                        ),
                    )
                state.last_pair = time.monotonic()
            rest = state.rest_factory()
            if collect_status(rest).get("state") != "qr":
                return self._redirect_home()
            code = rest.request_pairing_code(phone)
            if not code:
                return self._html(
                    502,
                    _message_page(
                        "Codice non disponibile",
                        "<p>WhatsApp non ha restituito un codice. Riprova tra poco "
                        "oppure usa il QR code.</p>",
                    ),
                )
            logger.info("Status page: pairing code requested")
            self._html(
                200,
                _message_page(
                    "Codice di collegamento",
                    f'<div class="code">{html.escape(code)}</div>'
                    "<ol><li>WhatsApp → Impostazioni → <b>Dispositivi collegati</b>"
                    "</li><li>Collega un dispositivo → <b>Collega con il numero di "
                    "telefono</b></li><li>Inserisci il codice qui sopra</li></ol>",
                ),
            )

    return Handler


def start(
    host: str,
    port: int,
    token: str,
    *,
    name: str = "",
    rest_factory: Callable[[], Any] | None = None,
) -> StatusPageHandle:
    """Serve the status page in a daemon thread; raises ``OSError`` on bind."""
    if not token:
        raise ValueError("status page token is empty")
    state = _State(token, name, rest_factory or _default_rest)
    server = ThreadingHTTPServer((host, port), _make_handler(state))
    server.daemon_threads = True
    thread = threading.Thread(
        target=server.serve_forever, name="megachat-status-page", daemon=True
    )
    thread.start()
    shown = "localhost" if host in ("0.0.0.0", "", "::") else host
    bound_port = server.server_address[1]
    return StatusPageHandle(server, thread, f"http://{shown}:{bound_port}/{token}/")


def stop(handle: StatusPageHandle | None) -> None:
    if handle is None:
        return
    try:
        handle.server.shutdown()
        handle.server.server_close()
    except Exception:
        logger.debug("Status page stop failed", exc_info=True)
    handle.thread.join(timeout=5)
