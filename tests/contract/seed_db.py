"""Seed a Megachat data directory with the REAL Python schema.

usage: python -I seed_db.py <repo-root> <home>

Run it as a subprocess with ``HOME`` pointing at the (temporary) home to
seed: ``protocols.db`` computes its paths from ``Path.home()`` at import time,
so the DB lands in ``<home>/.local/share/signal-tui-client/messages.db``, the
same file both the Python and the Go server open.  Messages come from signal,
whatsapp (1:1 and group) and telegram contacts, with reactions, quotes,
transcriptions and attachments (one with a real file on disk).
"""

from __future__ import annotations

import json
import sqlite3
import sys
import time
from pathlib import Path

root, home = sys.argv[1], sys.argv[2]
sys.path.insert(0, root)

import protocols.db as db  # noqa: E402

assert str(db.DB_FILE).startswith(home), (db.DB_FILE, home)

NOW = int(time.time() * 1000) - 3_600_000
SIGNAL = "+15550100001"
WA = "393331110001@c.us"
WA_GROUP = "120363000000000001@g.us"
TG = "424242"


def ts(n: int) -> int:
    return NOW + n * 1000


db._init_db()
add = db._add_message_to_cache

# -- signal: text, image attachment (real file), quote, voice note, reaction
att_dir = Path(home) / ".local" / "share" / "signal-cli" / "attachments"
att_dir.mkdir(parents=True, exist_ok=True)
# 1x1 PNG
(att_dir / "sigimg1").write_bytes(
    bytes.fromhex(
        "89504e470d0a1a0a0000000d49484452000000010000000108040000"
        "00b51c0c020000000b4944415478da63fcff1f0005fe02fea75b5ec3"
        "0000000049454e44ae426082"
    )
)
(att_dir / "sigvoice1").write_bytes(b"OggS" + b"\0" * 64)

add(SIGNAL, "Ciao Mario", False, "Mario", ts(1), protocol="signal", msg_id=str(ts(1)), status=None)
add(SIGNAL, "Ciao! Come va?", True, "Me", ts(2), protocol="signal", msg_id=str(ts(2)), status="read")
add(
    SIGNAL, "", False, "Mario", ts(3), msg_type="image", attachment_info="foto.png",
    attachment_id="sigimg1", content_type="image/png", protocol="signal", msg_id=str(ts(3)),
    media_kind="image",
)
add(
    SIGNAL, "Bellissima", True, "Me", ts(4), quote_text="foto.png", protocol="signal",
    msg_id=str(ts(4)), status="delivered", quote_timestamp=ts(3), quote_author=SIGNAL,
    quote_attachment_id="sigimg1", quote_content_type="image/png",
)
add(
    SIGNAL, "", False, "Mario", ts(5), msg_type="attachment", attachment_info="voice.ogg",
    attachment_id="sigvoice1", content_type="audio/ogg", protocol="signal",
    msg_id=str(ts(5)), media_kind="voice",
)
add(SIGNAL, "Testo con emoji \U0001F600 e accenti: perché", False, "Mario", ts(6), protocol="signal", msg_id=str(ts(6)))
add(SIGNAL, "failed one", True, "Me", ts(7), protocol="signal", msg_id=str(ts(7)), status="failed")
db._apply_reaction_delta("signal", SIGNAL, str(ts(1)), ts(1), "\U0001F44D", f"signal:{SIGNAL}", "Mario", False, False, ts(8))
db._apply_reaction_delta("signal", SIGNAL, str(ts(2)), ts(2), "❤️", "me", "You", True, False, ts(9))

# -- whatsapp: 1:1 with reply, a group with several senders
add(WA, "Hello from WhatsApp", False, "Anna", ts(10), protocol="whatsapp", msg_id="WAID1", status=None)
add(WA, "Reply here", True, "Me", ts(11), protocol="whatsapp", msg_id="WAID2", status="sent",
    quote_text="Hello from WhatsApp", quote_timestamp=ts(10), quote_author=WA, reply_to_message_id="WAID1")
add(WA, "", False, "Anna", ts(12), msg_type="image", attachment_info="pic.jpg", attachment_id="wa-pic.jpg",
    content_type="image/jpeg", protocol="whatsapp", msg_id="WAID3", media_kind="image")
add(WA_GROUP, "Group hello", False, "Bruno", ts(13), protocol="whatsapp", msg_id="WAG1")
add(WA_GROUP, "Hi all", True, "Me", ts(14), protocol="whatsapp", msg_id="WAG2", status="sent")
add(WA_GROUP, "Quoted in group", False, "Carla", ts(15), protocol="whatsapp", msg_id="WAG3",
    quote_text="Group hello", quote_timestamp=ts(13), quote_author="Bruno", reply_to_message_id="WAG1")
db._apply_reaction_delta("whatsapp", WA, "WAID1", ts(10), "\U0001F602", "me", "You", True, False, ts(16))
db._apply_reaction_delta("whatsapp", WA_GROUP, "WAG1", ts(13), "\U0001F525", "393339990000", "Carla", False, False, ts(17))

# -- telegram
add(TG, "Telegram hi", False, "Luca", ts(20), protocol="telegram", msg_id="501", status=None)
add(TG, "Telegram answer", True, "Me", ts(21), protocol="telegram", msg_id="502", status="sent",
    quote_text="Telegram hi", quote_timestamp=ts(20), quote_author="Luca", reply_to_message_id="501")
add(TG, "", False, "Luca", ts(22), msg_type="attachment", attachment_info="doc.pdf", attachment_id="tg-doc-1",
    content_type="application/pdf", protocol="telegram", msg_id="503", media_kind="document")
db._replace_reactions_snapshot(
    "telegram", TG, "501", ts(20),
    [{"emoji": "\U0001F44D", "count": 2, "author_key": "__agg__\U0001F44D", "author": "", "is_mine": False}],
    ts(23),
)

# -- transcriptions (direct SQL: written by the transcription worker)
conn = sqlite3.connect(db.DB_FILE)
try:
    conn.executemany(
        "INSERT OR REPLACE INTO transcriptions "
        "(protocol, attachment_id, status, text, error, model, updated_at) VALUES (?,?,?,?,?,?,?)",
        [
            ("signal", "sigvoice1", "done", "Questo è un vocale", None, "whisper-1", 1700000000.5),
            ("telegram", "tg-doc-1", "error", None, "boom", None, 1700000001.0),
        ],
    )
    conn.commit()
finally:
    conn.close()

print(json.dumps({"db": str(db.DB_FILE), "now": NOW}))
