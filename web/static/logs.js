// "Log" dialog: recent server log lines (GET /api/logs) and the transcription
// pipeline status (GET /api/transcribe/diagnostics), so problems can be
// debugged from the browser without access to the server's log files.
// Uses apiFetch / closeMainMenu from app.js.
(() => {
  "use strict";

  const dialog = document.querySelector("#logs-dialog");
  if (!dialog) return;
  const $ = (selector) => dialog.querySelector(selector);
  const list = $("#logs-list");
  const statusLine = $("#logs-status");
  const filterSelect = $("#logs-filter");
  const autoRefresh = $("#logs-auto-refresh");
  const diag = $("#logs-diag");
  const recentBox = $("#logs-recent");
  const checkResult = $("#logs-check-result");

  const MAX_LINES = 2000;
  const POLL_MS = 3000;
  const TRANSCRIPTION_LOGGERS = [
    "transcription",
    "server.autotranscribe",
    "server.transcribe",
    "server.ops.transcribe",
  ];

  let records = [];
  let lastSeq = 0;
  let timer = null;
  let loading = false;

  function isTranscription(record) {
    const name = record.logger || "";
    if (TRANSCRIPTION_LOGGERS.some((prefix) => name === prefix || name.startsWith(prefix + "."))) {
      return true;
    }
    return /trascri|transcri/i.test(record.message || "");
  }

  function visible(record) {
    const filter = filterSelect.value;
    if (filter === "errors") return record.level === "WARNING" || record.level === "ERROR" || record.level === "CRITICAL";
    if (filter === "transcription") return isTranscription(record);
    return true;
  }

  function timeText(seconds) {
    const date = new Date(seconds * 1000);
    const pad = (n, width = 2) => String(n).padStart(width, "0");
    return `${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}.${pad(date.getMilliseconds(), 3)}`;
  }

  function lineText(record) {
    return `${timeText(record.time)} ${record.level} ${record.logger}: ${record.message}`;
  }

  function render() {
    const atBottom = list.scrollHeight - list.scrollTop - list.clientHeight < 40;
    const shown = records.filter(visible);
    const fragment = document.createDocumentFragment();
    for (const record of shown) {
      const row = document.createElement("div");
      row.className = `logs-line logs-level-${(record.level || "").toLowerCase()}`;
      row.textContent = lineText(record);
      fragment.append(row);
    }
    list.replaceChildren(fragment);
    if (!shown.length) {
      const empty = document.createElement("div");
      empty.className = "logs-empty";
      empty.textContent = records.length
        ? "Nessuna riga per questo filtro."
        : "Nessun log ancora. Invia o ricevi una nota vocale e premi Aggiorna.";
      list.append(empty);
    }
    if (atBottom) list.scrollTop = list.scrollHeight;
    statusLine.textContent = `${shown.length} righe mostrate su ${records.length}`;
  }

  async function loadLogs() {
    if (loading) return;
    loading = true;
    try {
      const response = await apiFetch(`api/logs?after=${lastSeq}&limit=2000`);
      const data = await response.json();
      const fresh = Array.isArray(data.records) ? data.records : [];
      if (fresh.length) {
        records = records.concat(fresh).slice(-MAX_LINES);
        lastSeq = data.last_seq || lastSeq;
      }
      render();
    } catch (error) {
      statusLine.textContent = error.status === 404
        ? "Il server non supporta ancora i log: aggiorna e riavvia Megachat."
        : `Impossibile leggere i log (${error.message}).`;
    } finally {
      loading = false;
    }
  }

  function addDiag(label, value, bad = false) {
    const dt = document.createElement("dt");
    dt.textContent = label;
    const dd = document.createElement("dd");
    dd.textContent = value;
    if (bad) dd.className = "logs-bad";
    diag.append(dt, dd);
  }

  function yesNo(value) {
    return value ? "sì" : "no";
  }

  function renderRecent(rows) {
    recentBox.replaceChildren();
    const title = document.createElement("div");
    title.className = "logs-recent-title";
    title.textContent = "Ultime trascrizioni";
    recentBox.append(title);
    if (!rows.length) {
      const empty = document.createElement("div");
      empty.className = "logs-empty";
      empty.textContent = "Nessuna trascrizione registrata: nessuna nota vocale è arrivata al servizio.";
      recentBox.append(empty);
      return;
    }
    for (const row of rows) {
      const line = document.createElement("div");
      line.className = `logs-line logs-status-${row.status}`;
      const when = row.updated_at ? timeText(row.updated_at) : "";
      let detail = "";
      if (row.status === "failed") detail = ` — ${row.error || "errore sconosciuto"}`;
      else if (row.status === "ok") detail = ` — "${(row.text || "").slice(0, 80)}"`;
      line.textContent = `${when} [${row.status}] ${row.protocol || ""} ${row.model || ""}${detail}`;
      recentBox.append(line);
    }
  }

  async function loadDiagnostics() {
    try {
      const response = await apiFetch("api/transcribe/diagnostics");
      const data = await response.json();
      diag.replaceChildren();
      addDiag("Provider", data.provider);
      addDiag("Chiave API", data.api_key_set ? "configurata" : "MANCANTE", !data.api_key_set);
      addDiag("Modello", data.model);
      addDiag("Base URL", data.base_url);
      addDiag("Servizio attivo", data.service_active
        ? `sì (${data.service_client}, ${data.service_model})`
        : "NO", !data.service_active);
      if (data.service_active) {
        addDiag("Worker", data.worker_alive ? `attivo, in coda: ${data.queue_size}` : "FERMO", !data.worker_alive);
      }
      addDiag("Ascolto note vocali", data.auto_listener ? "attivo" : "NON ATTIVO", !data.auto_listener);
      const auto = data.auto_transcribe || {};
      const autoText = Object.entries(auto).map(([proto, on]) => `${proto}: ${on ? "on" : "off"}`).join(", ");
      addDiag("Trascrizione automatica", autoText || "—", !Object.values(auto).some(Boolean));
      addDiag("ffmpeg", yesNo(data.ffmpeg));
      if (data.env_overrides && data.env_overrides.length) {
        addDiag("Forzati da variabili d'ambiente", data.env_overrides.join(", "));
      }
      renderRecent(Array.isArray(data.recent) ? data.recent : []);
    } catch (error) {
      diag.replaceChildren();
      addDiag("Errore", error.status === 404
        ? "Il server non supporta ancora la diagnostica: aggiorna e riavvia Megachat."
        : `Impossibile leggere lo stato (${error.message}).`, true);
    }
  }

  async function checkKey() {
    const button = $("#logs-check-key");
    button.disabled = true;
    checkResult.hidden = false;
    checkResult.className = "logs-check-result";
    checkResult.textContent = "Verifica in corso…";
    try {
      const response = await apiFetch("api/transcribe/check", { method: "POST" });
      const data = await response.json();
      checkResult.classList.add(data.ok ? "logs-ok" : "logs-bad");
      const status = data.http_status ? `HTTP ${data.http_status}` : "";
      checkResult.textContent = `${data.ok ? "✅ Chiave valida" : "❌ Verifica fallita"} (${data.provider}) ${status}\n${data.url || ""}\n${data.detail || ""}`;
    } catch (error) {
      checkResult.classList.add("logs-bad");
      checkResult.textContent = `❌ Errore: ${error.message}`;
    } finally {
      button.disabled = false;
      loadLogs();
    }
  }

  function startPolling() {
    stopPolling();
    if (!autoRefresh.checked) return;
    timer = setInterval(() => {
      if (!dialog.open) {
        stopPolling();
        return;
      }
      loadLogs();
    }, POLL_MS);
  }

  function stopPolling() {
    if (timer !== null) clearInterval(timer);
    timer = null;
  }

  function refreshAll() {
    loadDiagnostics();
    loadLogs();
  }

  document.querySelector("#open-logs")?.addEventListener("click", () => {
    if (typeof closeMainMenu === "function") closeMainMenu();
    if (!dialog.open) dialog.showModal();
    refreshAll();
    startPolling();
  });
  $("#close-logs-dialog").addEventListener("click", () => dialog.close());
  dialog.addEventListener("close", stopPolling);
  $("#logs-refresh").addEventListener("click", refreshAll);
  $("#logs-check-key").addEventListener("click", checkKey);
  filterSelect.addEventListener("change", render);
  autoRefresh.addEventListener("change", startPolling);
  $("#logs-clear").addEventListener("click", async () => {
    try {
      await apiFetch("api/logs", { method: "DELETE" });
    } catch (error) {
      statusLine.textContent = `Impossibile svuotare (${error.message}).`;
      return;
    }
    records = [];
    render();
  });
  $("#logs-copy").addEventListener("click", async () => {
    const text = records.filter(visible).map(lineText).join("\n");
    try {
      await navigator.clipboard.writeText(text);
      statusLine.textContent = "Copiato negli appunti.";
    } catch {
      // Fallback (HTTP origin without clipboard permission): select the text.
      const range = document.createRange();
      range.selectNodeContents(list);
      const selection = window.getSelection();
      selection.removeAllRanges();
      selection.addRange(range);
      statusLine.textContent = "Testo selezionato: copialo manualmente.";
    }
  });
})();
