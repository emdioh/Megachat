# Linee guida per i test

Convenzioni ricavate dalla suite attuale (`tests/`, `tests/contract/`, `megachatd/internal/**/*_test.go`). Vale per i test Python del client/TUI e per quelli Go del server.

## 1. Principi

1. **Nessun I/O reale**: niente daemon signal-cli, WhatsApp/Telegram, socket in ascolto o scritture nella home. Tutto passa da stub, monkeypatch e `tmp_path`.
2. **La TUI si testa sul client**: nella suite unit/UI il server non esiste nel processo di test. Iniettare uno `StubClient` (`tests/client_harness.py`) o un `MagicMock` con risposte programmate; solo `tests/contract/` avvia il binario `megachatd`, e lo fa tramite il proprio harness.
3. **Test headless**: usare `App.run_test()` di Textual solo dove serve il DOM; per i mixin usare `view_app`/`inline_client_calls` e neutralizzare `run_worker`.
4. **Determinismo**: nessun `sleep` arbitrario; i worker di sfondo vanno eseguiti inline o neutralizzati.
5. **Nomi espliciti**: file `test_<area>[_<dettaglio>].py`; classi `Test<Cosa>`; nomi dei test che descrivono il comportamento.

## 2. Struttura di un test Python

```python
"""
<Descrizione dell'area coperta.>
"""

from __future__ import annotations

from unittest.mock import patch

from models import ChatContact
from tests.client_harness import StubClient, view_app


class TestArea:
    """🧱 <cosa copre la classe>."""

    def test_comportamento_atteso(self):
        client = StubClient({"ok": True})
        app, contact = view_app(client)
        ...
```

`pyproject.toml` imposta `pythonpath = ["."]`, quindi i moduli del progetto sono importabili direttamente.

Convenzioni osservate:

- docstring di modulo che dichiara scopo e garanzie;
- classi `Test*` per raggruppare;
- fixture di dati (`sample_messages`, envelope) invece di mock complessi;
- marker `integration` per i test che lanciano davvero la TUI headless; marker `smoke` per il boot+exit (escluso dal default);
- i marker `live`/`live-manual` restano registrati ma non ci sono test live: non aggiungerne senza un piano per le credenziali.

## 3. Fixture e helper disponibili

### `tests/conftest.py`

| Fixture | Fornisce |
|---|---|
| `fail_on_unclosed_sqlite` (autouse) | ogni connessione `sqlite3.connect` aperta nel test deve essere chiusa |
| `isolate_server_socket` (autouse) | `MEGACHAT_SOCKET` punta a `tmp_path/megachat-run/server.sock` |
| `isolate_instance_lock` (autouse) | `MEGACHAT_LOCK_FILE` punta a `tmp_path` |
| `isolate_environment` (autouse) | rimuove `SIGNAL_TUI_WEB_TOKEN` |
| `sample_messages` | cache in memoria di esempio con timestamp recenti |

### `tests/client_harness.py`

| Helper | Fornisce |
|---|---|
| `StubClient(*answers)` | registra `call`/timeout e risponde da una sequenza (valore o eccezione) |
| `view_app(client, protocol, cid)` | `SignalTUI` con DOM/worker stub-ati e client iniettato |
| `run_workers(app)` | esegue inline le callable passate a `run_worker` |
| `inline_client_calls(app, *mixins)` | `_offload`/`_ui`/`_call_op` inline, per mixin su `SimpleNamespace` |

### `tests/contract/`

- `conftest.py` costruisce o riusa `$MEGACHATD_BIN` (deve essere compilato con `-tags contracttest` quando si usano i test di invio) e semina un database con lo schema reale;
- `harness.py` avvia il server, espone `Server`, `SocketClient` e helper HTTP;
- `routes_table.py` e `seed_db.py` mantengono la tabella delle route e i dati di seed.

## 4. Pattern ricorrenti

- **Client scriptato**: `StubClient(answer1, answer2, ...)`, poi verificare `client.calls` (nome operazione, payload, timeout).
- **Esiti ignoti**: simulare `ServerUnavailable`/`OperationTimeout` e verificare che la vista si risincronizzi o mostri un errore, senza crash.
- **Widget isolato**: costruire il widget e chiamarne i metodi senza montare l'app quando possibile.
- **Payload come dati**: usare dict letterali fedeli al filo (`CacheMessage`, `ContactWire`) invece di mock profondi.
- **Thread**: preferire funzioni pure e code; se serve un thread, joinare con timeout e non dipendere dai tempi reali.

## 5. Test Go (`megachatd`)

- package `_test` o test nello stesso package quando servono dettagli interni;
- usare un database temporaneo (`t.TempDir()`), mai la home reale;
- i backend hanno fake locali (`internal/proto/fake`) e stub di store (`helpers_test.go`);
- i test di contratto del filo stanno in `tests/contract/`, non duplicarli in Go;
- eseguire `go vet ./...`, `go test ./...` durante lo sviluppo e `go test -race ./...` una volta alla fine, riportando l'esito nel hand-off.

## 6. Checklist prima di aggiungere un test

- [ ] Il test gira offline e non tocca `~/.local/share/signal-tui-client/` né `/tmp/signal-tui.lock`?
- [ ] Usa gli helper esistenti (`StubClient`, fixture, `tmp_path`) invece di reinventare il setup?
- [ ] La TUI parla solo con un client stub, mai con un server reale?
- [ ] Il nome di file/classe/test descrive area e comportamento?
- [ ] Non introduce marker non registrati né dipendenze nuove?
- [ ] `make check` (lint + suite) e, per il server, `go test ./...` passano?
