# Project Instructions for AI Agents

This file provides instructions and context for AI coding agents working on this project.

<!-- BEGIN BEADS INTEGRATION v:1 profile:minimal hash:1105d646 -->
## Beads Issue Tracker

This project uses **bd (beads)** for issue tracking. Run `bd prime` to see full workflow context and commands.

### Quick Reference

```bash
bd ready              # Find available work
bd show <id>          # View issue details
bd update <id> --claim  # Claim work
bd close <id>         # Complete work
```

### Rules

- Use `bd` for ALL task tracking — do NOT use TodoWrite, TaskCreate, or markdown TODO lists
- Run `bd prime` for detailed command reference and session close protocol
- Use `bd remember` for persistent knowledge — do NOT use MEMORY.md files

**Architecture in one line:** issues live in a local Dolt DB; sync uses `refs/dolt/data` on your git remote; `.beads/issues.jsonl` is a passive export. See https://github.com/gastownhall/beads/blob/main/docs/core-concepts/sync-concepts.md for details and anti-patterns.

## Agent Context Profiles

The managed Beads block is task-tracking guidance, not permission to override repository, user, or orchestrator instructions.

- **Conservative (default)**: Use `bd` for task tracking. Do not run git commits, git pushes, or Dolt remote sync unless explicitly asked. At handoff, report changed files, validation, and suggested next commands.
- **Minimal**: Keep tool instruction files as pointers to `bd prime`; use the same conservative git policy unless active instructions say otherwise.
- **Team-maintainer**: Only when the repository explicitly opts in, agents may close beads, run quality gates, commit, and push as part of session close. A current "do not commit" or "do not push" instruction still wins.

## Session Completion

This protocol applies when ending a Beads implementation workflow. It is subordinate to explicit user, repository, and orchestrator instructions.

1. **File issues for remaining work** - Create beads for anything that needs follow-up
2. **Run quality gates** (if code changed) - Tests, linters, builds
3. **Update issue status** - Close finished work, update in-progress items
4. **Handle git/sync by active profile**:
   ```bash
   # Conservative/minimal/default: report status and proposed commands; wait for approval.
   git status

   # Team-maintainer opt-in only, unless current instructions forbid it:
   git pull --rebase
   git push
   git status
   ```
5. **Hand off** - Summarize changes, validation, issue status, and any blocked sync/commit/push step

**Critical rules:**
- Explicit user or orchestrator instructions override this Beads block.
- Do not commit or push without clear authority from the active profile or the current user request.
- If a required sync or push is blocked, stop and report the exact command and error.
<!-- END BEADS INTEGRATION -->


## Build & Test

```bash
./install.sh                       # venv + deps + signal-cli + megachatd (needs Go, or an existing megachatd binary)
make megachatd                     # build megachatd/bin/megachatd (cd megachatd && go build -o bin/megachatd ./cmd/megachatd)
pytest tests/ --ignore=tests/contract -q -m "not live and not live-manual and not integration"   # Python unit tests (needs the venv; ~10s)
make contract-test                 # black-box contract tests of megachatd (builds it with -tags contracttest; or uses $MEGACHATD_BIN, which must carry that tag)
cd megachatd && go vet ./... && go test ./...                            # Go unit tests
python3 launcher.py server start|status|stop                            # run megachatd in the background
sudo scripts/megachat-service.sh install|uninstall|status               # megachatd as a systemd / OpenRC service
```

## Architecture Overview

- `megachatd/` is the only server (Go): chat core + event bus + SQLite store, one package per protocol (Signal via signal-cli, WhatsApp native via whatsmeow, Telegram reusing the Telethon session). It also serves the web SPA (`web/static/`). The legacy Python server was deleted on 2026-11-08 (mc-zao.4.4); `docs/` design notes that mention `server/`, `protocols/`, `web/*.py` or WAHA describe that old code.
- The Python side is clients only: the Textual TUI (`signal_tui.py`, `tui/`) and the launcher (`launcher.py`, `launcher/`), both built on `client/` (socket client, attach-or-spawn, lock, wire types). They speak the wire contract in `megachatd/CONTRACTS.md` (operations + events) over the Unix socket; the SPA uses HTTP/WebSocket.
- The single-instance lock (`/tmp/signal-tui.lock`, holds the pid) and the socket + runtime file are owned by the server; `launcher/server.py` (status/stop/start) and `client/attach.py` (TUI find-or-spawn) use them. Clients look on the default socket (`$MEGACHAT_SOCKET`, else `$XDG_RUNTIME_DIR/megachat/server.sock`) and then on `~/.local/state/megachat/server.sock`, where the system service listens (`client.paths.candidate_socket_paths`).
- Spawning goes through `launcher/serverbin.py`: `megachatd serve ... --project-dir <repo>` (binary: `$MEGACHATD_BIN`, `megachatd/bin/megachatd`, PATH).
- Contract tests (`tests/contract/`) drive the real binary only through socket/HTTP/WS; device linking uses the `link.*` ops (`link_whatsapp.py`, `device_link_screen.py`). Tests deleted with the Python server are listed in `documentation/test-suite/DELETED_TESTS.md` (rebuild: mc-gux).

## Conventions & Patterns

_Add your project-specific conventions here_
