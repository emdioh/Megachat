"""Drive the Python reference implementation (protocols/db.py) for the Go tests.

Usage:
    python3 -I pydb.py init  <db>                 # run _init_db, dump
    python3 -I pydb.py run   <db> <func> [json]   # run db._<func>(*args, **kwargs), dump
    python3 -I pydb.py dump  <db>                 # dump only
    python3 -I pydb.py predicates < strings.json  # models.py predicate results

The dump is JSON: schema rows of sqlite_master (sorted by name), user_version
and every row of messages / reactions / transcriptions ordered by rowid.
"""

import json
import sqlite3
import sys
from pathlib import Path

REPO = Path(__file__).resolve().parents[4]
sys.path.insert(0, str(REPO))

import protocols.db as d  # noqa: E402


def dump(path):
    conn = sqlite3.connect(path)
    try:
        out = {
            "schema": [
                list(r)
                for r in conn.execute(
                    "SELECT type, name, tbl_name, sql FROM sqlite_master ORDER BY name"
                )
            ],
            "user_version": conn.execute("PRAGMA user_version").fetchone()[0],
            "tables": {},
        }
        for table in ("messages", "reactions", "transcriptions"):
            cur = conn.execute(f"SELECT * FROM {table} ORDER BY rowid")
            names = [c[0] for c in cur.description]
            out["tables"][table] = [dict(zip(names, row)) for row in cur.fetchall()]
        return out
    finally:
        conn.close()


def predicates():
    """stdin: JSON list of strings -> results of the models.py predicates."""
    import models

    out = []
    for text in json.load(sys.stdin):
        out.append(
            {
                "caption": models.is_caption_like(text),
                "synthetic": models.is_whatsapp_synthetic_media_text(text),
                "synthetic_strict_x": models.is_whatsapp_synthetic_media_text(
                    text, attachment_id="x", require_attachment_match=True
                ),
                "mirror": models.is_sent_mirror_attachment_id(text),
                "dup": models.is_duplicate_media_identity(text, text),
            }
        )
    json.dump(out, sys.stdout)


def main():
    if sys.argv[1] == "predicates":
        return predicates()
    cmd, db = sys.argv[1], Path(sys.argv[2]).resolve()
    d.DB_FILE = db
    d.CACHE_DIR = db.parent
    result = None
    if cmd == "init":
        d._init_db()
    elif cmd == "run":
        spec = json.loads(sys.argv[4]) if len(sys.argv) > 4 else []
        # ``[a, b]`` -> positional; ``{"args": [...], "kwargs": {...}}`` -> both.
        args, kwargs = (spec["args"], spec.get("kwargs", {})) if isinstance(spec, dict) else (spec, {})
        result = getattr(d, "_" + sys.argv[3])(*args, **kwargs)
    elif cmd != "dump":
        raise SystemExit("unknown command")
    out = dump(db)
    out["result"] = result
    json.dump(out, sys.stdout, sort_keys=True, default=str)


main()
