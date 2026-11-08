# Historical design notes

**Historical design notes. Most predate megachatd; paths like `server/`, `protocols/`, `web/*.py` and WAHA refer to the Python server deleted on 2026-11-08 (mc-zao.4.4).**

These files are kept as context for decisions and bug analyses. They are **not** a description of the current system and are not maintained: where they disagree with the code, the code wins.

Current documentation lives in [`../documentation/`](../documentation/README.md) and [`../megachatd/CONTRACTS.md`](../megachatd/CONTRACTS.md).

A few files are operational notes rather than design snapshots, but they too contain Python-era details:

| File | Note |
|---|---|
| `ALIASES.md` | Shell aliases; the source of truth is `launcher/aliases.py` (`./install.sh --aliases`). Mentions of `MEGACHAT_SERVER=python` are obsolete. |
| `HANDOVER.md` | Local ↔ server handover; the commands still apply, the server is now `megachatd`. |
| `BUGS.md`, `TEST_REPORT.md`, `PERF_ANALYSIS.md` | Dated snapshots from the Python-server era. |
| `DESIGN_*.md`, `PLAN_*.md` | Dated design/planning notes. |
