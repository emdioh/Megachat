# Notes for Claude Code

## Testing: run only the relevant tests

Do NOT run the whole suite (`make test` / bare `pytest`, ~4 minutes) after every
change. Run only the tests that cover what you touched, plus the fast checks:

```bash
python3 -m pytest -q -p no:cacheprovider tests/test_<area>*.py   # relevant tests only
ruff check . && ruff format --check .                              # always (seconds)
```

Find the relevant tests with `grep -rln "<module or function>" tests/`. Examples:

| Changed | Tests |
|---|---|
| `scripts/megachat_instance.sh`, `scripts/instance_config.py` | `tests/test_instance_script.py` |
| `instance.py`, `server/paths.py`, `server/lock.py` | `tests/test_instance.py` |
| `server/status_page.py` | `tests/test_status_page.py` |

Run the full suite only when the user asks, or once before opening a PR for a
change that touches shared code (`protocols/`, `server/core.py`, `models.py`).

Known pre-existing failure: `tests/test_web_multi_host.py::test_listens_on_every_host`
fails where IPv6 is unavailable (e.g. cloud containers); it is not a regression.
