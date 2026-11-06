#!/bin/sh
# Fake signal-cli for the subprocess fallback tests.
# Logs every invocation (args joined by '|') to $FAKE_SIGNAL_LOG.
[ -n "$FAKE_SIGNAL_LOG" ] && printf '%s\n' "$(IFS='|'; echo "$*")" >> "$FAKE_SIGNAL_LOG"
[ "$1" = "-u" ] && shift 2
case "$1" in
  listContacts)
    printf 'Number:+391234567890 Name:Mario Rossi ACI:uuid-123\n'
    printf 'Number:+391111111111 Name:Anna Maria Bianchi\n'
    printf 'garbage line\n'
    ;;
  send)
    if [ -n "$FAKE_SIGNAL_SEND_OUT" ]; then printf '%s\n' "$FAKE_SIGNAL_SEND_OUT"; else printf '1700000000123\n'; fi
    ;;
  daemon)
    if [ -n "$FAKE_SIGNAL_DAEMON_FAIL" ]; then
      echo "Error: $FAKE_SIGNAL_DAEMON_FAIL" >&2
      exit "${FAKE_SIGNAL_DAEMON_EXIT:-3}"
    fi
    exec sleep 2
    ;;
  fail)
    echo "boom on stderr" >&2
    exit 3
    ;;
  *)
    echo "unknown command $1" >&2
    exit 2
    ;;
esac
