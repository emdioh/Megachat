"""The REST route table (megachatd/CONTRACTS.md section 7) with one sample
request per route that needs no real backend.

Each entry: (method, path, json body or None).  Paths are what a client would
send, query string included.  ``AUTH_ROUTES`` is every (method, path pattern)
under ``/api/``; the samples below are concrete instances of them.
"""

from __future__ import annotations

SIG = "+15550100001"
SIGQ = "%2B15550100001"  # url-quoted: a raw + in a query string is a space
WA = "393331110001@c.us"
TG = "424242"

#: (method, concrete path, json body) -- every REST route of the contract.
SAMPLES: list[tuple[str, str, object]] = [
    ("GET", "/api/emoji", None),
    ("GET", "/api/reactions?protocol=signal", None),
    ("POST", "/api/send", {"protocol": "signal", "contact_id": SIG, "text": ""}),
    ("GET", "/api/status", None),
    ("GET", "/api/contacts", None),
    ("GET", "/api/contacts/book?q=zz", None),
    ("GET", "/api/contact-avatar?proto=signal&contact_id=nobody", None),
    ("GET", "/api/link-preview?url=notaurl", None),
    ("GET", "/api/link-preview/image?url=notaurl", None),
    ("POST", "/api/log", {"message": "contract test"}),
    ("POST", "/api/transcribe", {"protocol": "signal", "attachment_id": ""}),
    ("POST", "/api/config/openai-key", {"api_key": ""}),
    ("GET", "/api/logs?limit=5", None),
    ("DELETE", "/api/logs", None),
    ("GET", "/api/transcribe/diagnostics", None),
    ("POST", "/api/transcribe/check", {}),
    ("GET", "/api/transcribe/config", None),
    ("GET", "/api/transcribe/settings", None),
    ("PUT", "/api/transcribe/settings", {"language": "it"}),
    ("PUT", "/api/transcribe/api-key", {"provider": "openai", "api_key": ""}),
    ("GET", "/api/autotranscribe", None),
    ("PUT", "/api/autotranscribe/global/signal", {"enabled": False}),
    ("GET", f"/api/autotranscribe/chat?proto=signal&contact_id={SIGQ}", None),
    ("PUT", f"/api/autotranscribe/chat?proto=signal&contact_id={SIGQ}", {"enabled": None}),
    ("GET", "/api/transcribe/signal/sigvoice1", None),
    ("GET", f"/api/messages?proto=signal&contact_id={SIGQ}", None),
    ("POST", "/api/messages/read", {"protocol": "signal", "contact_id": SIG}),
    ("POST", "/api/chats/archive", {"protocol": "signal", "contact_id": SIG, "archived": True}),
    ("POST", "/api/messages/edit", {"protocol": "signal", "contact_id": SIG, "message_id": "x", "new_text": "y"}),
    ("POST", "/api/messages/reaction", {"protocol": "signal", "contact_id": SIG, "message_id": "x", "emoji": "x"}),
    ("GET", "/api/link/status", None),
    ("GET", "/api/link/required", None),
    ("POST", "/api/link/start", {"protocol": "bogus"}),
    ("POST", "/api/link/poll", {"protocol": "bogus"}),
    ("POST", "/api/link/finalize", {"phone": ""}),
    ("POST", "/api/link/cancel", {"protocol": "bogus"}),
    ("POST", "/api/link/submit_2fa", {"password": "x"}),
    ("POST", "/api/link/reconnect", {"protocols": []}),
    ("GET", "/api/media/signal/does-not-exist", None),
    ("GET", "/api/quote-media/signal/999999", None),
    ("GET", "/api/gmail/status", None),
    ("GET", "/api/gmail/unread", None),
    ("GET", "/api/gmail/address-book", None),
    ("GET", "/api/gmail/messages", None),
    ("POST", "/api/gmail/messages/bulk-delete", {"uids": [1]}),
    ("DELETE", "/api/gmail/messages/1", None),
    ("GET", "/api/gmail/messages/1", None),
    ("GET", "/api/gmail/messages/1/attachments/0", None),
    ("POST", "/api/gmail/messages/1/read", None),
    ("POST", "/api/gmail/messages/1/unread", None),
    ("POST", "/api/gmail/messages/1/archive", None),
    ("POST", "/api/gmail/send", None),
    ("GET", "/gmail-image?token=nope", None),
]

#: Routes that are not behind the Bearer token.
PUBLIC = {"/health", "/gmail-image"}


def is_public(path: str) -> bool:
    return path.split("?", 1)[0] in PUBLIC or not path.startswith("/api/")
