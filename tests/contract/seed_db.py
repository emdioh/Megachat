"""Seed a Megachat data directory for the contract tests.

usage: python -I seed_db.py <repo-root> <home>

Writes ``<home>/.local/share/signal-tui-client/messages.db`` (the file megachatd
opens) and two signal-cli attachment files.  The schema and rows are a frozen
copy of what the legacy Python server's ``protocols.db`` produced (schema
version 5, captured 2026-11-08 before that code was deleted): messages from
signal, whatsapp (1:1 and group) and telegram contacts, with reactions, quotes,
transcriptions and attachments (one with a real file on disk).  Timestamps are
relative to "one hour ago", as before.
"""

from __future__ import annotations

import json
import sqlite3
import sys
import time
from pathlib import Path

_root, home = sys.argv[1], sys.argv[2]

NOW = int(time.time() * 1000) - 3_600_000


def ts(n: int) -> int:
    return NOW + n * 1000


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

SCHEMA_AND_ROWS = """CREATE TABLE messages (
                    id INTEGER PRIMARY KEY AUTOINCREMENT,
                    protocol TEXT NOT NULL DEFAULT 'signal',
                    contact_number TEXT NOT NULL,
                    text TEXT,
                    is_mine INTEGER NOT NULL DEFAULT 0,
                    sender TEXT,
                    timestamp INTEGER NOT NULL,
                    quote_text TEXT,
                    msg_type TEXT DEFAULT 'text',
                    attachment_info TEXT,
                    attachment_id TEXT,
                    content_type TEXT,
                    media_kind TEXT,
                    quote_attachment_id TEXT,
                    quote_attachment_path TEXT,
                    quote_content_type TEXT,
                    read INTEGER DEFAULT 0,
                    status TEXT DEFAULT 'read',
                    msg_id TEXT,
                    quote_timestamp INTEGER,
                    quote_author TEXT,
                    reply_to_message_id TEXT,
                    edited INTEGER NOT NULL DEFAULT 0,
                    batch_id TEXT,
                    batch_index INTEGER
                );
INSERT INTO messages VALUES(1,'signal','+15550100001','Ciao Mario',0,'Mario',{ts1},NULL,'text',NULL,NULL,NULL,NULL,NULL,NULL,NULL,0,'read','{ts1}',NULL,NULL,NULL,0,NULL,NULL);
INSERT INTO messages VALUES(2,'signal','+15550100001','Ciao! Come va?',1,'Me',{ts2},NULL,'text',NULL,NULL,NULL,NULL,NULL,NULL,NULL,1,'read','{ts2}',NULL,NULL,NULL,0,NULL,NULL);
INSERT INTO messages VALUES(3,'signal','+15550100001','',0,'Mario',{ts3},NULL,'image','foto.png','sigimg1','image/png','image',NULL,NULL,NULL,0,'read','{ts3}',NULL,NULL,NULL,0,NULL,NULL);
INSERT INTO messages VALUES(4,'signal','+15550100001','Bellissima',1,'Me',{ts4},'foto.png','text',NULL,NULL,NULL,NULL,'sigimg1',NULL,'image/png',1,'delivered','{ts4}',{ts3},'+15550100001',NULL,0,NULL,NULL);
INSERT INTO messages VALUES(5,'signal','+15550100001','',0,'Mario',{ts5},NULL,'attachment','voice.ogg','sigvoice1','audio/ogg','voice',NULL,NULL,NULL,0,'read','{ts5}',NULL,NULL,NULL,0,NULL,NULL);
INSERT INTO messages VALUES(6,'signal','+15550100001','Testo con emoji 😀 e accenti: perché',0,'Mario',{ts6},NULL,'text',NULL,NULL,NULL,NULL,NULL,NULL,NULL,0,'read','{ts6}',NULL,NULL,NULL,0,NULL,NULL);
INSERT INTO messages VALUES(7,'signal','+15550100001','failed one',1,'Me',{ts7},NULL,'text',NULL,NULL,NULL,NULL,NULL,NULL,NULL,1,'failed','{ts7}',NULL,NULL,NULL,0,NULL,NULL);
INSERT INTO messages VALUES(8,'whatsapp','393331110001@c.us','Hello from WhatsApp',0,'Anna',{ts10},NULL,'text',NULL,NULL,NULL,NULL,NULL,NULL,NULL,0,'read','WAID1',NULL,NULL,NULL,0,NULL,NULL);
INSERT INTO messages VALUES(9,'whatsapp','393331110001@c.us','Reply here',1,'Me',{ts11},'Hello from WhatsApp','text',NULL,NULL,NULL,NULL,NULL,NULL,NULL,1,'sent','WAID2',{ts10},'393331110001@c.us','WAID1',0,NULL,NULL);
INSERT INTO messages VALUES(10,'whatsapp','393331110001@c.us','',0,'Anna',{ts12},NULL,'image','pic.jpg','wa-pic.jpg','image/jpeg','image',NULL,NULL,NULL,0,'read','WAID3',NULL,NULL,NULL,0,NULL,NULL);
INSERT INTO messages VALUES(11,'whatsapp','120363000000000001@g.us','Group hello',0,'Bruno',{ts13},NULL,'text',NULL,NULL,NULL,NULL,NULL,NULL,NULL,0,'read','WAG1',NULL,NULL,NULL,0,NULL,NULL);
INSERT INTO messages VALUES(12,'whatsapp','120363000000000001@g.us','Hi all',1,'Me',{ts14},NULL,'text',NULL,NULL,NULL,NULL,NULL,NULL,NULL,1,'sent','WAG2',NULL,NULL,NULL,0,NULL,NULL);
INSERT INTO messages VALUES(13,'whatsapp','120363000000000001@g.us','Quoted in group',0,'Carla',{ts15},'Group hello','text',NULL,NULL,NULL,NULL,NULL,NULL,NULL,0,'read','WAG3',{ts13},'Bruno','WAG1',0,NULL,NULL);
INSERT INTO messages VALUES(14,'telegram','424242','Telegram hi',0,'Luca',{ts20},NULL,'text',NULL,NULL,NULL,NULL,NULL,NULL,NULL,0,'read','501',NULL,NULL,NULL,0,NULL,NULL);
INSERT INTO messages VALUES(15,'telegram','424242','Telegram answer',1,'Me',{ts21},'Telegram hi','text',NULL,NULL,NULL,NULL,NULL,NULL,NULL,1,'sent','502',{ts20},'Luca','501',0,NULL,NULL);
INSERT INTO messages VALUES(16,'telegram','424242','',0,'Luca',{ts22},NULL,'attachment','doc.pdf','tg-doc-1','application/pdf','document',NULL,NULL,NULL,0,'read','503',NULL,NULL,NULL,0,NULL,NULL);
CREATE TABLE reactions (
                    id INTEGER PRIMARY KEY AUTOINCREMENT,
                    protocol TEXT NOT NULL,
                    contact_number TEXT NOT NULL,
                    target_msg_id TEXT,
                    target_timestamp INTEGER,
                    emoji TEXT NOT NULL,
                    author_key TEXT NOT NULL DEFAULT '',
                    author TEXT NOT NULL DEFAULT '',
                    is_mine INTEGER NOT NULL DEFAULT 0,
                    count INTEGER NOT NULL DEFAULT 1,
                    timestamp INTEGER NOT NULL
                );
INSERT INTO reactions VALUES(1,'signal','+15550100001','{ts1}',{ts1},'👍','signal:+15550100001','Mario',0,1,{ts8});
INSERT INTO reactions VALUES(2,'signal','+15550100001','{ts2}',{ts2},'❤️','me','You',1,1,{ts9});
INSERT INTO reactions VALUES(3,'whatsapp','393331110001@c.us','WAID1',{ts10},'😂','me','You',1,1,{ts16});
INSERT INTO reactions VALUES(4,'whatsapp','120363000000000001@g.us','WAG1',{ts13},'🔥','393339990000','Carla',0,1,{ts17});
INSERT INTO reactions VALUES(5,'telegram','424242','501',{ts20},'👍','__agg__👍','',0,2,{ts23});
CREATE TABLE transcriptions (
                    protocol TEXT NOT NULL,
                    attachment_id TEXT NOT NULL,
                    status TEXT NOT NULL DEFAULT 'pending',
                    text TEXT,
                    error TEXT,
                    model TEXT,
                    updated_at REAL NOT NULL,
                    PRIMARY KEY (protocol, attachment_id)
                );
INSERT INTO transcriptions VALUES('signal','sigvoice1','done','Questo è un vocale',NULL,'whisper-1',1700000000.5);
INSERT INTO transcriptions VALUES('telegram','tg-doc-1','error',NULL,'boom',NULL,1700000001.0);
CREATE UNIQUE INDEX idx_reactions_identity
                    ON reactions(protocol, contact_number,
                                 IFNULL(target_msg_id, ''),
                                 IFNULL(target_timestamp, 0), author_key)
                ;
CREATE INDEX idx_reactions_contact
                    ON reactions(protocol, contact_number)
                ;
CREATE INDEX idx_messages_contact ON messages(protocol, contact_number, timestamp);
"""

db_dir = Path(home) / ".local" / "share" / "signal-tui-client"
db_dir.mkdir(parents=True, exist_ok=True)
DB_FILE = db_dir / "messages.db"
conn = sqlite3.connect(DB_FILE)
try:
    conn.execute("PRAGMA journal_mode=WAL")
    conn.executescript(SCHEMA_AND_ROWS.format(**{f"ts{n}": ts(n) for n in range(100)}))
    conn.execute("PRAGMA user_version = 5")
    conn.commit()
finally:
    conn.close()

print(json.dumps({"db": str(DB_FILE), "now": NOW}))
