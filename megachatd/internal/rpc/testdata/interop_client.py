"""Drives client/socket_client.py (the TUI's socket client) against the Go rpc server.

usage: interop_client.py REPO_ROOT SOCKET_PATH
Prints one JSON object per line; the line {"step":"subscribed"} tells the Go
test to publish bus events.  Exit 3 when the imports are unavailable.
"""
import json
import sys

repo, sock = sys.argv[1], sys.argv[2]
sys.path.insert(0, repo)
try:
    from client.errors import ApiError
    from client.socket_client import SocketClient
except Exception as exc:  # pragma: no cover
    print("import failed: %r" % (exc,), file=sys.stderr)
    sys.exit(3)


def out(**kw):
    print(json.dumps(kw), flush=True)


client = SocketClient.connect(sock, timeout=5, reconnect=False)
try:
    out(step="hello", info=client.server_info)
    out(step="call", result=client.call("test.echo", {"text": "héllo \U0001f600"}))
    try:
        client.call("test.fail", {})
        out(step="error", raised=False)
    except ApiError as exc:
        out(step="error", raised=True, code=exc.code, message=exc.message, details=exc.details)
    try:
        client.call("test.nope", {})
    except ApiError as exc:
        out(step="unknown", code=exc.code)
    sub = client.subscribe()
    out(step="subscribed")
    got = []
    for _ in range(3):
        ev = sub.get(5)
        if ev is None:
            break
        got.append({"seq": ev.seq, "type": ev.type, "payload": ev.payload})
    out(step="events", events=got)
finally:
    client.close()
