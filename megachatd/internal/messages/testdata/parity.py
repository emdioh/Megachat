"""Run the Python message builder (server/messages.py) on a fixture database.

usage: python3 -I parity.py <repo-root> <spec.json>

The spec (written by parity_test.go) names the SQLite file, the directories,
the contacts of the stub backends and the calls to make.  The result is a JSON
object {call name: result} printed compactly, the way the Go test serialises
its own results, so the two can be compared byte for byte.
"""

import json
import sys
from pathlib import Path
from types import SimpleNamespace

root, spec_path = sys.argv[1], sys.argv[2]
sys.path.insert(0, root)

spec = json.loads(Path(spec_path).read_text(encoding="utf-8"))

import protocols.db as db  # noqa: E402
import protocols.rpc as rpc  # noqa: E402
from models import ChatContact  # noqa: E402
import server.messages as messages  # noqa: E402

db.DB_FILE = Path(spec["db"])
db.CACHE_DIR = Path(spec["cache_dir"])
rpc.SIGNAL_CLI_ATTACHMENTS_DIR = Path(spec["signal_attachments"])
messages._lazy_media_enabled = lambda: spec["lazy"]


def contacts_of(key, protocol):
    return [
        ChatContact(
            id=c["id"],
            display_name=c["display_name"],
            protocol=c["protocol"],
            extras=dict(c.get("extras") or {}),
        )
        for c in spec.get(key, {}).get(protocol, [])
    ]


class Backend:
    def __init__(self, protocol):
        self.protocol = protocol
        self.contacts = contacts_of("contacts", protocol)
        self._cache = None

    @property
    def cache(self):
        if self._cache is None:
            self._cache = db._load_cache(self.protocol)
        return self._cache

    def find_contact(self, contact_id):
        for c in self.contacts:
            if c.id == contact_id:
                return c
        for c in self.contacts:
            if c.extras.get("lid") == contact_id or (
                c.extras.get("phone") and f"{c.extras['phone']}@c.us" == contact_id
            ):
                return c
        return None

    def _jid_to_phone(self, jid):
        return spec.get("jid_phone", {}).get(jid, "")


class Manager:
    def __init__(self):
        self.backends = {p: Backend(p) for p in ("signal", "whatsapp", "telegram")}

    def get(self, protocol):
        return self.backends[protocol]

    def list_contacts(self):
        return [c for b in self.backends.values() for c in b.contacts]

    def list_address_book_sync(self, protocols=None, force=False):
        out = []
        for protocol in protocols or []:
            out.extend(contacts_of("address_book", protocol))
        return out


manager = Manager()
results = {}
for call in spec["calls"]:
    op = call["op"]
    if op == "page":
        results[call["name"]] = messages.messages_page(
            manager,
            call["protocol"],
            call["contact_id"],
            refresh=call.get("refresh", False),
            limit=call.get("limit"),
            since=call.get("since"),
        )
    elif op == "window":
        rows, refreshed = messages.messages_window(
            manager, call["protocol"], call["contact_id"]
        )
        results[call["name"]] = {"messages": rows, "refreshed": refreshed}
    elif op == "snapshot":
        results[call["name"]] = messages.messages_snapshot(manager, call["protocol"])

sys.stdout.write(json.dumps(results, ensure_ascii=False, separators=(",", ":")))
