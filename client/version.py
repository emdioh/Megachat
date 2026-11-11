"""Version identifiers a server announces to its clients (``rpc.hello``,
the runtime file)."""

# The server's own version (``server_version`` in ``rpc.hello``, ``version`` in
# the runtime file) comes from megachatd/VERSION: it is informational and has no
# client-side copy to keep in sync.

#: Wire protocol version of the Unix-socket adapter; bumped on any
#: incompatible change of its framing, methods or error mapping.
API_VERSION = 1
