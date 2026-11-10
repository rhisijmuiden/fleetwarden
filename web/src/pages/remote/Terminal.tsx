import { useCallback, useEffect, useRef, useState } from "react";
import { useParams, useSearchParams } from "react-router";
import { useTranslation } from "react-i18next";
import { Terminal as XTerm } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import "@xterm/xterm/css/xterm.css";
import { ClipboardCopy, ClipboardPaste, LogOut, Maximize, Minimize, Minus, Plus, RefreshCw, ShieldAlert, SquareTerminal } from "lucide-react";
import { get, post, type Device, type RemoteSession } from "../../api";
import { useBranding } from "../../branding";
import { Button, useErrorText } from "../../components/ui";
import { Overlay, Tool, finished, type Phase, type StatusMsg } from "./Viewer";
import { TermMsg, decodeJSON, input, resize, type TermExit, type TermInfo } from "./terminalProtocol";

type Shell = "powershell" | "cmd";

const fontKey = "fw-terminal-font";

function storedFont(): number {
  try {
    const n = Number(localStorage.getItem(fontKey));
    return n >= 10 && n <= 24 ? n : 14;
  } catch {
    return 14;
  }
}

// TerminalViewer is the full-window remote terminal, opened in its own tab
// from a device: a root shell on Linux, PowerShell or Command Prompt as
// SYSTEM on Windows.
export function TerminalViewer() {
  const { deviceId = "" } = useParams();
  const [params, setParams] = useSearchParams();
  const shell: Shell = params.get("shell") === "cmd" ? "cmd" : "powershell";
  const { t } = useTranslation();
  const errorText = useErrorText();
  const { branding } = useBranding();
  const [device, setDevice] = useState<Device | null>(null);
  const [session, setSession] = useState<RemoteSession | null>(null);
  const [phase, setPhase] = useState<Phase>("starting");
  const [reason, setReason] = useState("");
  const [startError, setStartError] = useState<string | null>(null);
  const [info, setInfo] = useState<TermInfo | null>(null);
  const [exitCode, setExitCode] = useState<number | null>(null);
  const [attempt, setAttempt] = useState(0);
  const [full, setFull] = useState(false);
  const [font, setFont] = useState(storedFont);
  const [notice, setNotice] = useState<string | null>(null);

  const ws = useRef<WebSocket | null>(null);
  const host = useRef<HTMLDivElement | null>(null);
  const term = useRef<XTerm | null>(null);
  const fit = useRef<FitAddon | null>(null);
  const started = useRef(-1);
  const platform = useRef<string>("");

  const send = useCallback((m: ArrayBuffer) => {
    const s = ws.current;
    if (s && s.readyState === WebSocket.OPEN) s.send(m);
  }, []);

  useEffect(() => {
    get<Device>(`/devices/${deviceId}`)
      .then(setDevice)
      .catch(() => {});
  }, [deviceId]);

  useEffect(() => {
    document.title = `${device?.hostname ?? t("remote.term.title")} – ${t("remote.term.title")} – ${branding.productName}`;
  }, [device, branding.productName, t]);

  const showNotice = (text: string) => {
    setNotice(text);
    setTimeout(() => setNotice((n) => (n === text ? null : n)), 3000);
  };

  const copySelection = useCallback(() => {
    const sel = term.current?.getSelection();
    if (!sel) return false;
    navigator.clipboard?.writeText(sel).then(
      () => showNotice(t("remote.term.copied")),
      () => {},
    );
    return true;
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const pasteClipboard = useCallback(() => {
    navigator.clipboard
      ?.readText()
      .then((text) => text && term.current?.paste(text))
      .catch(() => showNotice(t("remote.term.pasteBlocked")));
    term.current?.focus();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // The terminal itself lives as long as the tab.
  useEffect(() => {
    const el = host.current;
    if (!el) return;
    const styles = getComputedStyle(document.documentElement);
    const accent = styles.getPropertyValue("--fw-primary").trim() || "#3b82f6";
    const x = new XTerm({
      cursorBlink: true,
      fontSize: storedFont(),
      fontFamily: "ui-monospace, 'Cascadia Mono', 'JetBrains Mono', 'SF Mono', Menlo, Consolas, 'Liberation Mono', monospace",
      scrollback: 10000,
      allowProposedApi: false,
      theme: { background: "#0b0f17", foreground: "#e5e7eb", cursor: accent, cursorAccent: "#0b0f17", selectionBackground: `${accent}55` },
    });
    const f = new FitAddon();
    x.loadAddon(f);
    x.open(el);
    f.fit();
    term.current = x;
    fit.current = f;
    // Copy and paste like Windows Terminal: Ctrl+Shift+C and Ctrl+Shift+V
    // everywhere; on Windows also Ctrl+C with a selection, and Ctrl+V.
    x.attachCustomKeyEventHandler((e) => {
      if (e.type !== "keydown" || !e.ctrlKey || e.altKey) return true;
      const win = platform.current === "windows";
      if (e.code === "KeyC" && (e.shiftKey || (win && x.hasSelection()))) {
        copySelection();
        x.clearSelection();
        e.preventDefault();
        return false;
      }
      if (e.code === "KeyV" && (e.shiftKey || win)) {
        return false; // the browser fires "paste", which xterm sends on
      }
      return true;
    });
    const data = x.onData((d) => send(input(d)));
    const bin = x.onBinary((d) => {
      const b = new Uint8Array(d.length + 1);
      b[0] = TermMsg.Input;
      for (let i = 0; i < d.length; i++) b[i + 1] = d.charCodeAt(i) & 0xff;
      send(b.buffer);
    });
    const size = x.onResize(({ cols, rows }) => send(resize(cols, rows)));
    const ro = new ResizeObserver(() => {
      try {
        f.fit();
      } catch {
        // not laid out yet
      }
    });
    ro.observe(el);
    return () => {
      ro.disconnect();
      data.dispose();
      bin.dispose();
      size.dispose();
      x.dispose();
      term.current = null;
    };
  }, [send, copySelection]);

  useEffect(() => {
    const x = term.current;
    if (!x) return;
    x.options.fontSize = font;
    fit.current?.fit();
    try {
      localStorage.setItem(fontKey, String(font));
    } catch {
      // per-viewer convenience only
    }
  }, [font]);

  // Start a session and open the socket (again after "Reconnect").
  useEffect(() => {
    if (started.current === attempt) return; // React runs effects twice in development
    started.current = attempt;
    let closed = false;
    setPhase("starting");
    setReason("");
    setStartError(null);
    setExitCode(null);
    const x = term.current;
    if (attempt > 0) x?.write(`\r\n\x1b[2m── ${t("remote.term.newSession")} ──\x1b[0m\r\n`);
    post<RemoteSession>(`/devices/${deviceId}/remote`, { kind: "terminal", shell, cols: x?.cols ?? 0, rows: x?.rows ?? 0 })
      .then((s) => {
        if (closed) return;
        setSession(s);
        setPhase(s.state);
        const proto = location.protocol === "https:" ? "wss" : "ws";
        const sock = new WebSocket(`${proto}://${location.host}/api/v1/remote/sessions/${s.id}/ws`);
        sock.binaryType = "arraybuffer";
        ws.current = sock;
        let opened = false;
        sock.addEventListener("open", () => (opened = true));
        sock.onopen = () => {
          const tx = term.current;
          if (tx) sock.send(resize(tx.cols, tx.rows));
        };
        sock.onmessage = (ev) => {
          if (typeof ev.data === "string") {
            const m = JSON.parse(ev.data) as StatusMsg;
            if (m.type === "status") {
              setPhase(m.state);
              if (m.reason) setReason(m.reason);
            }
            return;
          }
          const buf = ev.data as ArrayBuffer;
          const type = new Uint8Array(buf, 0, 1)[0];
          switch (type) {
            case TermMsg.Output:
              term.current?.write(new Uint8Array(buf, 1));
              break;
            case TermMsg.Info: {
              const i = decodeJSON<TermInfo>(buf);
              platform.current = i.platform;
              setInfo(i);
              setPhase((p) => (finished(p) ? p : "active"));
              term.current?.focus();
              break;
            }
            case TermMsg.Exit:
              setExitCode(decodeJSON<TermExit>(buf).code);
              break;
            case TermMsg.Event: {
              const e = decodeJSON<{ type: string; code?: string; message?: string }>(buf);
              if (e.type === "error" && e.message) term.current?.write(`\r\n\x1b[31m${e.message}\x1b[0m\r\n`);
              break;
            }
          }
        };
        sock.onclose = () => {
          setPhase((p) => (finished(p) ? p : "ended"));
          // Never opened: the server or a proxy in between refused the WebSocket.
          setReason((r) => r || (opened ? "connection_lost" : "websocket_refused"));
        };
      })
      .catch((e) => {
        setStartError(errorText(e));
        setPhase("failed");
      });
    return () => {
      closed = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [deviceId, attempt]);

  useEffect(() => () => ws.current?.close(), []);

  useEffect(() => {
    const onFs = () => setFull(document.fullscreenElement != null);
    document.addEventListener("fullscreenchange", onFs);
    return () => document.removeEventListener("fullscreenchange", onFs);
  }, []);

  const endSession = () => {
    setReason("operator_ended");
    setPhase("ended");
    const close = () => ws.current?.close();
    if (session) post(`/remote/sessions/${session.id}/end`).then(close, close);
    else close();
  };

  const restart = (next?: Shell) => {
    if (!finished(phase)) endSession();
    if (next) setParams(next === "powershell" ? {} : { shell: next }, { replace: true });
    setAttempt((a) => a + 1);
  };

  const active = phase === "active";
  const done = finished(phase);
  const hostname = device?.hostname ?? info?.hostname ?? "";
  const windows = (info?.platform ?? device?.platform) === "windows";

  return (
    <div className="flex h-full flex-col bg-[#0b0f17] text-fg">
      <header className="flex h-12 shrink-0 items-center gap-2 border-b border-line bg-surface px-3">
        <div className="flex min-w-0 items-center gap-2.5">
          <span className="grid size-7 place-items-center rounded-md bg-primary/10 text-primary">
            <SquareTerminal className="size-4" />
          </span>
          <div className="min-w-0 leading-tight">
            <p className="truncate text-sm font-semibold">{hostname || t("remote.term.title")}</p>
            <p className="truncate text-xs text-muted">
              {t(`remote.states.${phase}`)}
              {info ? ` · ${info.user} · ${info.shell}` : ""}
            </p>
          </div>
          {active && info && (
            <span
              className="ml-1 hidden items-center gap-1 rounded-sm bg-warning/10 px-1.5 py-0.5 text-xs font-medium text-warning md:inline-flex"
              title={t("remote.term.privilegedHint", { user: info.user })}
            >
              <ShieldAlert className="size-3.5" /> {t("remote.term.privileged")}
            </span>
          )}
        </div>
        <div className="ml-auto flex items-center gap-1">
          {windows && (
            <select
              aria-label={t("remote.term.shell")}
              className="h-8 rounded-md border border-line bg-surface px-2 text-xs"
              value={shell}
              onChange={(e) => restart(e.target.value as Shell)}
            >
              <option value="powershell">{t("remote.term.shells.powershell")}</option>
              <option value="cmd">{t("remote.term.shells.cmd")}</option>
            </select>
          )}
          <Tool label={t("remote.term.copy")} onClick={() => copySelection() || showNotice(t("remote.term.nothingSelected"))}>
            <ClipboardCopy className="size-4" />
          </Tool>
          <Tool label={t("remote.term.paste")} onClick={pasteClipboard}>
            <ClipboardPaste className="size-4" />
          </Tool>
          <Tool label={t("remote.term.smaller")} onClick={() => setFont((f) => Math.max(10, f - 1))}>
            <Minus className="size-4" />
          </Tool>
          <Tool label={t("remote.term.larger")} onClick={() => setFont((f) => Math.min(24, f + 1))}>
            <Plus className="size-4" />
          </Tool>
          <Tool
            label={full ? t("remote.exitFullscreen") : t("remote.fullscreen")}
            onClick={() => (full ? document.exitFullscreen() : document.documentElement.requestFullscreen()).catch(() => {})}
          >
            {full ? <Minimize className="size-4" /> : <Maximize className="size-4" />}
          </Tool>
          {done ? (
            <Button size="sm" icon={<RefreshCw className="size-4" />} onClick={() => restart()} className="ml-1">
              <span className="hidden sm:inline">{t("remote.reconnect")}</span>
            </Button>
          ) : (
            phase !== "starting" && (
              <Button size="sm" variant="danger" icon={<LogOut className="size-4" />} onClick={endSession} className="ml-1">
                <span className="hidden sm:inline">{t("remote.end")}</span>
              </Button>
            )
          )}
        </div>
      </header>

      <div className="relative min-h-0 flex-1">
        <div ref={host} className="absolute inset-0 p-2" aria-label={t("remote.term.screen", { host: hostname })} />

        {notice && (
          <div role="status" className="absolute bottom-4 left-1/2 -translate-x-1/2 rounded-md border border-line bg-surface px-4 py-2 text-sm shadow-xl">
            {notice}
          </div>
        )}

        {done && info && (
          <div
            role="status"
            className="absolute right-4 bottom-4 left-4 mx-auto flex max-w-xl flex-wrap items-center gap-3 rounded-md border border-line bg-surface px-4 py-3 text-sm shadow-2xl"
          >
            <p className="min-w-0 flex-1">
              {exitCode !== null
                ? t("remote.term.exited", { code: exitCode })
                : (startError ?? t(`remote.reasons.${reason || "unknown"}`, { defaultValue: t("remote.reasons.unknown") }))}
            </p>
            <Button size="sm" icon={<RefreshCw className="size-4" />} onClick={() => restart()}>
              {t("remote.term.newSession")}
            </Button>
          </div>
        )}

        {!info && (
          <div className="absolute inset-0 grid place-items-center bg-[#0b0f17] p-6">
            <div className="w-[min(440px,100%)] rounded-lg border border-line bg-surface p-6 text-center shadow-2xl">
              <Overlay
                phase={phase}
                reason={reason}
                startError={startError}
                consentLeft={0}
                hostname={hostname}
                onRetry={() => restart()}
                onClose={() => window.close()}
              />
            </div>
          </div>
        )}
      </div>
    </div>
  );
}
