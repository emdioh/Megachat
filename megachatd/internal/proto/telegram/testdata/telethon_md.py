"""Run Telethon's markdown parse/unparse on cases read as JSON from stdin."""
import json
import sys

from telethon.extensions import markdown as md
from telethon.tl import types

NAMES = {
    types.MessageEntityBold: "bold",
    types.MessageEntityItalic: "italic",
    types.MessageEntityStrike: "strike",
    types.MessageEntityCode: "code",
    types.MessageEntityPre: "pre",
    types.MessageEntityTextUrl: "url",
    types.MessageEntityMentionName: "mention",
}
BUILD = {
    "bold": lambda o, l, x: types.MessageEntityBold(o, l),
    "italic": lambda o, l, x: types.MessageEntityItalic(o, l),
    "strike": lambda o, l, x: types.MessageEntityStrike(o, l),
    "code": lambda o, l, x: types.MessageEntityCode(o, l),
    "pre": lambda o, l, x: types.MessageEntityPre(o, l, x or ""),
    "url": lambda o, l, x: types.MessageEntityTextUrl(o, l, x),
    "mention": lambda o, l, x: types.MessageEntityMentionName(o, l, int(x)),
    "spoiler": lambda o, l, x: types.MessageEntitySpoiler(o, l),
}


def ent_out(e):
    extra = ""
    if isinstance(e, types.MessageEntityTextUrl):
        extra = e.url
    elif isinstance(e, types.MessageEntityMentionName):
        extra = str(e.user_id)
    elif isinstance(e, types.MessageEntityPre):
        extra = e.language
    return [NAMES.get(type(e), type(e).__name__), e.offset, e.length, extra]


cases = json.load(sys.stdin)
out = {"parse": [], "unparse": []}
for s in cases["parse"]:
    text, ents = md.parse(s)
    out["parse"].append([text, [ent_out(e) for e in ents]])
for text, ents in cases["unparse"]:
    out["unparse"].append(md.unparse(text, [BUILD[k](o, l, x) for k, o, l, x in ents]))
json.dump(out, sys.stdout)
