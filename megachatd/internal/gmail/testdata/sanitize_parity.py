"""Reference side of the sanitizer parity test (sanitize_parity_test.go).

Reads a JSON list of {"name", "html", "cids"} from stdin, applies the real
``server.gmail.sanitize_html`` (nh3 + tinycss2 + css-inline) to every case and
prints {"name": output} as JSON.  Image proxy tokens are replaced by the URL
they sign, because every process signs with its own random key.

Usage: python3 sanitize_parity.py <repo root> < cases.json
"""
import json
import re
import sys

sys.path.insert(0, sys.argv[1])

import server.gmail as gmail  # noqa: E402

TOKEN = re.compile(r'(data-(?:src|bg)="gmail-image\?token=)([^"&]+)([^"]*")')


def normalise(html: str) -> str:
    def repl(m):
        try:
            url = gmail.verify_image_token(m.group(2)) or "<invalid token>"
        except Exception:
            url = "<invalid token>"
        return m.group(1) + "URL:" + url + m.group(3)

    return TOKEN.sub(repl, html)


def main() -> None:
    cases = json.load(sys.stdin)
    out = {}
    for case in cases:
        got = gmail.sanitize_html(case["html"], case.get("cids") or None)
        out[case["name"]] = normalise(got)
    json.dump(out, sys.stdout)


main()
