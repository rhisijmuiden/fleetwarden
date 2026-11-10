import { useCallback, useEffect, useRef, useState, type ReactNode } from "react";
import { useParams } from "react-router";
import { useTranslation } from "react-i18next";
import {
  ClipboardCopy,
  ClipboardPaste,
  Expand,
  Keyboard,
  Loader2,
  LogOut,
  Maximize,
  Minimize,
  Monitor as MonitorIcon,
  MonitorOff,
  RefreshCw,
  Scan,
  ShieldAlert,
  ShieldCheck,
  X,
  Zap,
} from "lucide-react";
import { get, post, type Device, type RemoteSession, type RemoteState } from "../../api";
import { useBranding } from "../../branding";
import { Button, useErrorText } from "../../components/ui";
import {
  Mouse,
  Msg,
  ack,
  chunks,
  combos,
  control,
  decodeFrame,
  decodeJSON,
  decodeText,
  key,
  mouse,
  rtcSignal,
  scanCodes,
  setClipboard,
  type Info,
  type Quality,
  type RemoteEvent,
  type RTCSignal,
} from "./protocol";

export type Phase = RemoteState | "starting";
export type StatusMsg = { type: "status"; state: RemoteState; reason?: string; consent?: string };

export const finished = (p: Phase) => p === "ended" || p === "denied" || p === "failed";

// RemoteViewer is the full-window remote control page, opened in its own
// tab from a device. It starts a session, shows the remote screen and sends
// mouse and keyboard input.
export function RemoteViewer() {
  const { deviceId = "" } = useParams();
  const { t } = useTranslation();
  const errorText = useErrorText();
  const { branding } = useBranding();
  const [device, setDevice] = useState<Device | null>(null);
  const [session, setSession] = useState<RemoteSession | null>(null);
  const [phase, setPhase] = useState<Phase>("starting");
  const [reason, setReason] = useState("");
  const [startError, setStartError] = useState<string | null>(null);
  const [info, setInfo] = useState<Info | null>(null);
  const [desktop, setDesktop] = useState("Default");
  const [notice, setNotice] = useState<string | null>(null);
  const [remoteClip, setRemoteClip] = useState("");
  const [fit, setFit] = useState(true);
  const [panel, setPanel] = useState<"none" | "clipboard" | "keys">("none");
  const [full, setFull] = useState(false);
  const [attempt, setAttempt] = useState(0);
  const [consentLeft, setConsentLeft] = useState(30);
  const [path, setPath] = useState<"relay" | "direct">("relay");

  const ws = useRef<WebSocket | null>(null);
  const canvas = useRef<HTMLCanvasElement | null>(null);
  const queue = useRef<Promise<void>>(Promise.resolve());
  const started = useRef(-1);

  // The direct path (a WebRTC data channel to the agent), when it opened.
  const dc = useRef<RTCDataChannel | null>(null);
  const pc = useRef<RTCPeerConnection | null>(null);
  const rtc = useRef<{ iceServers: string[] } | null>(null);
  const rtcAsked = useRef(false);

  const send = useCallback((m: ArrayBuffer) => {
    const d = dc.current;
    if (d && d.readyState === "open") {
      for (const c of chunks(m)) d.send(c);
      return;
    }
    const s = ws.current;
    if (s && s.readyState === WebSocket.OPEN) s.send(m);
  }, []);

  const closeDirect = () => {
    dc.current = null;
    pc.current?.close();
    pc.current = null;
    setPath("relay");
  };

  // startDirect answers the agent's offer. The screen moves to the data
  // channel once it opens; the relay stays as the fallback.
  async function startDirect(sdp: string) {
    closeDirect();
    const servers = rtc.current?.iceServers ?? [];
    const conn = new RTCPeerConnection({ iceServers: servers.length ? [{ urls: servers }] : [] });
    pc.current = conn;
    let parts: Uint8Array[] = [];
    conn.ondatachannel = (e) => {
      const d = e.channel;
      d.binaryType = "arraybuffer";
      d.onopen = () => {
        if (pc.current === conn) dc.current = d;
      };
      d.onclose = () => {
        if (dc.current === d) {
          dc.current = null;
          setPath("relay");
        }
      };
      d.onmessage = (ev) => {
        const b = new Uint8Array(ev.data as ArrayBuffer);
        parts.push(b.subarray(1));
        if (b[0] !== 1) return;
        const total = parts.reduce((n, p) => n + p.length, 0);
        const msg = new Uint8Array(total);
        let off = 0;
        for (const p of parts) {
          msg.set(p, off);
          off += p.length;
        }
        parts = [];
        handleBinary(msg.buffer);
      };
    };
    try {
      await conn.setRemoteDescription({ type: "offer", sdp });
      await conn.setLocalDescription(await conn.createAnswer());
      // Send the answer with all candidates (the agent does not trickle).
      await new Promise<void>((resolve) => {
        if (conn.iceGatheringState === "complete") return resolve();
        const timer = setTimeout(resolve, 3000);
        conn.addEventListener("icegatheringstatechange", () => {
          if (conn.iceGatheringState === "complete") {
            clearTimeout(timer);
            resolve();
          }
        });
      });
      if (pc.current !== conn) return;
      send(rtcSignal({ type: "answer", sdp: conn.localDescription?.sdp ?? "" }));
    } catch {
      if (pc.current === conn) closeDirect();
      send(rtcSignal({ type: "failed" }));
    }
  }

  useEffect(() => {
    get<Device>(`/devices/${deviceId}`)
      .then(setDevice)
      .catch(() => {});
  }, [deviceId]);

  useEffect(() => {
    document.title = `${device?.hostname ?? t("remote.title")} – ${branding.productName}`;
  }, [device, branding.productName, t]);

  // Start a session and open the viewer socket (again after "Reconnect").
  useEffect(() => {
    if (started.current === attempt) return; // React runs effects twice in development
    started.current = attempt;
    let closed = false;
    setPhase("starting");
    setReason("");
    setStartError(null);
    setInfo(null);
    closeDirect();
    rtcAsked.current = false;
    post<RemoteSession & { rtc?: { iceServers: string[] } }>(`/devices/${deviceId}/remote`, { kind: "desktop" })
      .then((s) => {
        if (closed) return;
        rtc.current = s.rtc ?? null;
        setSession(s);
        setPhase(s.state);
        const proto = location.protocol === "https:" ? "wss" : "ws";
        const sock = new WebSocket(`${proto}://${location.host}/api/v1/remote/sessions/${s.id}/ws`);
        sock.binaryType = "arraybuffer";
        ws.current = sock;
        let opened = false;
        sock.addEventListener("open", () => (opened = true));
        sock.onmessage = (ev) => {
          if (typeof ev.data === "string") {
            const m = JSON.parse(ev.data) as StatusMsg;
            if (m.type === "status") {
              setPhase(m.state);
              if (m.reason) setReason(m.reason);
              if (m.state === "consent") setConsentLeft(30);
            }
            return;
          }
          handleBinary(ev.data as ArrayBuffer);
        };
        sock.onclose = () => {
          closeDirect();
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

  // Close the socket when the tab goes away.
  useEffect(
    () => () => {
      pc.current?.close();
      ws.current?.close();
    },
    [],
  );

  useEffect(() => {
    if (phase !== "consent") return;
    const id = setInterval(() => setConsentLeft((s) => Math.max(0, s - 1)), 1000);
    return () => clearInterval(id);
  }, [phase]);

  const showNotice = (text: string) => {
    setNotice(text);
    setTimeout(() => setNotice((n) => (n === text ? null : n)), 4000);
  };

  function handleBinary(buf: ArrayBuffer) {
    const type = new Uint8Array(buf, 0, 1)[0];
    switch (type) {
      case Msg.Info: {
        const i = decodeJSON<Info>(buf);
        queue.current = queue.current.then(() => {
          const c = canvas.current;
          if (c && (c.width !== i.width || c.height !== i.height)) {
            c.width = i.width;
            c.height = i.height;
          }
          setInfo(i);
          setDesktop(i.desktop);
          setPhase((p) => (finished(p) ? p : "active")); // the screen is here, whatever the status said
          // Ask the agent for the direct path once the screen shows.
          if (!rtcAsked.current && rtc.current && typeof RTCPeerConnection !== "undefined") {
            rtcAsked.current = true;
            send(rtcSignal({ type: "request" }));
          }
        });
        break;
      }
      case Msg.Frame: {
        queue.current = queue.current
          .then(async () => {
            const { seq, tiles } = decodeFrame(buf);
            const images = await Promise.all(tiles.map((tl) => createImageBitmap(new Blob([tl.data], { type: tl.enc === 2 ? "image/png" : "image/jpeg" }))));
            const ctx = canvas.current?.getContext("2d");
            images.forEach((img, i) => {
              ctx?.drawImage(img, tiles[i].x, tiles[i].y);
              img.close();
            });
            send(ack(seq));
          })
          .catch(() => {});
        break;
      }
      case Msg.Clipboard: {
        const text = decodeText(buf);
        setRemoteClip(text);
        // Copy to this computer too, when the browser allows it.
        navigator.clipboard?.writeText(text).catch(() => {});
        break;
      }
      case Msg.RTC: {
        const sig = decodeJSON<RTCSignal>(buf);
        if (sig.type === "offer" && sig.sdp) void startDirect(sig.sdp);
        break;
      }
      case Msg.Event: {
        const ev = decodeJSON<RemoteEvent>(buf);
        if (ev.type === "path") setPath(ev.code === "direct" ? "direct" : "relay");
        if (ev.type === "desktop" && ev.desktop) setDesktop(ev.desktop);
        if (ev.type === "notice" && ev.code) showNotice(t(`remote.notices.${ev.code}`, { defaultValue: ev.code }));
        break;
      }
    }
  }

  // ---- input ----
  const pressed = useRef(new Set<number>());
  const pendingMove = useRef<[number, number] | null>(null);
  const wheelAcc = useRef(0);
  const pendingPaste = useRef<number | null>(null);

  const toRemote = (e: { clientX: number; clientY: number }): [number, number] => {
    const c = canvas.current!;
    const r = c.getBoundingClientRect();
    return [((e.clientX - r.left) * c.width) / r.width, ((e.clientY - r.top) * c.height) / r.height];
  };

  const flushMove = () => {
    const m = pendingMove.current;
    if (m) {
      pendingMove.current = null;
      send(mouse(Mouse.Move, 0, m[0], m[1]));
    }
  };

  const sendKey = (down: boolean, scan: number) => {
    if (down) pressed.current.add(scan);
    else pressed.current.delete(scan);
    send(key(down, scan));
  };

  const releaseAll = () => {
    for (const s of pressed.current) send(key(false, s));
    pressed.current.clear();
  };

  const pressCombo = (name: string) => {
    const keys = combos[name].map((c) => scanCodes[c]);
    keys.forEach((k) => send(key(true, k)));
    [...keys].reverse().forEach((k) => send(key(false, k)));
    canvas.current?.focus();
  };

  const active = phase === "active";

  useEffect(() => {
    const c = canvas.current;
    if (!c || !active) return;
    const buttonOf = (b: number) => [0, 1, 2, 3, 4][b] ?? 0;
    const onMove = (e: PointerEvent) => {
      const first = pendingMove.current === null;
      pendingMove.current = toRemote(e);
      if (first) requestAnimationFrame(flushMove);
    };
    const onDown = (e: PointerEvent) => {
      e.preventDefault();
      c.focus();
      c.setPointerCapture(e.pointerId);
      flushMove();
      const [x, y] = toRemote(e);
      send(mouse(Mouse.Down, buttonOf(e.button), x, y));
    };
    const onUp = (e: PointerEvent) => {
      e.preventDefault();
      flushMove();
      const [x, y] = toRemote(e);
      send(mouse(Mouse.Up, buttonOf(e.button), x, y));
    };
    const onWheel = (e: WheelEvent) => {
      e.preventDefault();
      const [x, y] = toRemote(e);
      // One notch is 100 pixels in most browsers, 3 lines in Firefox.
      wheelAcc.current += e.deltaMode === 1 ? e.deltaY / 3 : e.deltaY / 100;
      const notches = Math.trunc(wheelAcc.current);
      if (notches !== 0) {
        wheelAcc.current -= notches;
        send(mouse(Mouse.Wheel, 0, x, y, -notches * 120));
      }
      if (Math.abs(e.deltaX) >= 50) send(mouse(Mouse.HWheel, 0, x, y, Math.sign(e.deltaX) * 120));
    };
    const onKey = (e: KeyboardEvent) => {
      const scan = scanCodes[e.code];
      if (scan === undefined) return;
      const down = e.type === "keydown";
      if (down && e.code === "KeyV" && e.ctrlKey && !e.altKey) {
        // Let the browser fire "paste" so the local clipboard goes along.
        pendingPaste.current = window.setTimeout(() => {
          pendingPaste.current = null;
          sendKey(true, scan);
        }, 200);
        return;
      }
      e.preventDefault();
      sendKey(down, scan);
    };
    const onPaste = (e: ClipboardEvent) => {
      if (pendingPaste.current === null) return;
      e.preventDefault();
      clearTimeout(pendingPaste.current);
      pendingPaste.current = null;
      const text = e.clipboardData?.getData("text/plain") ?? "";
      if (text) send(setClipboard(text));
      sendKey(true, scanCodes.KeyV);
    };
    const onContext = (e: Event) => e.preventDefault();
    c.addEventListener("pointermove", onMove);
    c.addEventListener("pointerdown", onDown);
    c.addEventListener("pointerup", onUp);
    c.addEventListener("wheel", onWheel, { passive: false });
    c.addEventListener("keydown", onKey);
    c.addEventListener("keyup", onKey);
    c.addEventListener("contextmenu", onContext);
    c.addEventListener("blur", releaseAll);
    window.addEventListener("paste", onPaste);
    c.focus();
    return () => {
      c.removeEventListener("pointermove", onMove);
      c.removeEventListener("pointerdown", onDown);
      c.removeEventListener("pointerup", onUp);
      c.removeEventListener("wheel", onWheel);
      c.removeEventListener("keydown", onKey);
      c.removeEventListener("keyup", onKey);
      c.removeEventListener("contextmenu", onContext);
      c.removeEventListener("blur", releaseAll);
      window.removeEventListener("paste", onPaste);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [active]);

  useEffect(() => {
    const onFs = () => {
      const on = document.fullscreenElement != null;
      setFull(on);
      // In full screen Chrome lets the page take keys such as Win and Alt+Tab.
      const kb = (navigator as Navigator & { keyboard?: { lock?: () => Promise<void>; unlock?: () => void } }).keyboard;
      if (on) kb?.lock?.().catch(() => {});
      else kb?.unlock?.();
    };
    document.addEventListener("fullscreenchange", onFs);
    return () => document.removeEventListener("fullscreenchange", onFs);
  }, []);

  const endSession = () => {
    setReason("operator_ended");
    setPhase("ended");
    // Close the socket only after the server took the reason.
    const close = () => ws.current?.close();
    if (session) post(`/remote/sessions/${session.id}/end`).then(close, close);
    else close();
  };

  const secure = desktop.toLowerCase() === "winlogon";
  const hostname = device?.hostname ?? info?.hostname ?? "";

  return (
    <div className="flex h-full flex-col bg-[#0b0f17] text-fg">
      <header className="flex h-12 shrink-0 items-center gap-2 border-b border-line bg-surface px-3">
        <div className="flex min-w-0 items-center gap-2.5">
          <span className="grid size-7 place-items-center rounded-md bg-primary/10 text-primary">
            <MonitorIcon className="size-4" />
          </span>
          <div className="min-w-0 leading-tight">
            <p className="truncate text-sm font-semibold">{hostname || t("remote.title")}</p>
            <p className="truncate text-xs text-muted">
              <StateLabel phase={phase} />
              {active && info?.user ? ` · ${info.user}` : ""}
            </p>
          </div>
          {active && secure && (
            <span
              className="ml-1 inline-flex items-center gap-1 rounded-sm bg-warning/10 px-1.5 py-0.5 text-xs font-medium text-warning"
              title={t("remote.secureDesktopHint")}
            >
              <ShieldAlert className="size-3.5" /> {t("remote.secureDesktop")}
            </span>
          )}
          {active && info && (
            <span
              className={`ml-1 hidden items-center gap-1 rounded-sm px-1.5 py-0.5 text-xs font-medium sm:inline-flex ${path === "direct" ? "bg-success/10 text-success" : "bg-surface-muted text-muted"}`}
              title={t(`remote.paths.${path}Hint`)}
            >
              {path === "direct" && <Zap className="size-3.5" />} {t(`remote.paths.${path}`)}
            </span>
          )}
          {session && session.consent !== "direct" && active && (
            <span
              className="ml-1 hidden items-center gap-1 rounded-sm bg-success/10 px-1.5 py-0.5 text-xs font-medium text-success sm:inline-flex"
              title={t(`remote.consentHint.${session.consent}`)}
            >
              <ShieldCheck className="size-3.5" /> {t(`remote.consentShort.${session.consent}`)}
            </span>
          )}
        </div>
        <div className="ml-auto flex items-center gap-1">
          {active && info && (
            <>
              {info.monitors.length > 1 && (
                <select
                  aria-label={t("remote.monitor")}
                  className="h-8 rounded-md border border-line bg-surface px-2 text-xs"
                  value={info.monitor}
                  onChange={(e) => send(control({ op: "monitor", monitor: Number(e.target.value) }))}
                >
                  {info.monitors.map((m) => (
                    <option key={m.index} value={m.index}>
                      {t("remote.monitorN", { n: m.index + 1 })} ({m.width}×{m.height}){m.primary ? ` · ${t("remote.primary")}` : ""}
                    </option>
                  ))}
                  <option value={-1}>{t("remote.allMonitors")}</option>
                </select>
              )}
              <select
                aria-label={t("remote.quality")}
                className="hidden h-8 rounded-md border border-line bg-surface px-2 text-xs sm:block"
                value={info.quality}
                onChange={(e) => send(control({ op: "quality", quality: e.target.value as Quality }))}
              >
                {(["low", "medium", "high"] as Quality[]).map((q) => (
                  <option key={q} value={q}>
                    {t(`remote.qualities.${q}`)}
                  </option>
                ))}
              </select>
              <Tool label={t("remote.cad")} onClick={() => send(control({ op: "cad" }))}>
                <span className="text-[11px] font-semibold">Ctrl+Alt+Del</span>
              </Tool>
              <Tool label={t("remote.keys")} pressed={panel === "keys"} onClick={() => setPanel(panel === "keys" ? "none" : "keys")}>
                <Keyboard className="size-4" />
              </Tool>
              <Tool label={t("remote.clipboard")} pressed={panel === "clipboard"} onClick={() => setPanel(panel === "clipboard" ? "none" : "clipboard")}>
                <ClipboardPaste className="size-4" />
              </Tool>
              <Tool label={t("remote.refresh")} onClick={() => send(control({ op: "refresh" }))}>
                <RefreshCw className="size-4" />
              </Tool>
              <Tool label={fit ? t("remote.actualSize") : t("remote.fit")} onClick={() => setFit(!fit)}>
                {fit ? <Scan className="size-4" /> : <Expand className="size-4" />}
              </Tool>
              <Tool
                label={full ? t("remote.exitFullscreen") : t("remote.fullscreen")}
                onClick={() => (full ? document.exitFullscreen() : document.documentElement.requestFullscreen()).catch(() => {})}
              >
                {full ? <Minimize className="size-4" /> : <Maximize className="size-4" />}
              </Tool>
            </>
          )}
          {!finished(phase) && phase !== "starting" && (
            <Button size="sm" variant="danger" icon={<LogOut className="size-4" />} onClick={endSession} className="ml-1">
              <span className="hidden sm:inline">{t("remote.end")}</span>
            </Button>
          )}
        </div>
      </header>

      <div className="relative flex min-h-0 flex-1">
        <div className={`min-w-0 flex-1 ${fit ? "grid place-items-center overflow-hidden p-2" : "overflow-auto"}`}>
          <canvas
            ref={canvas}
            tabIndex={0}
            aria-label={t("remote.screen", { host: hostname })}
            width={1}
            height={1}
            className={`touch-none outline-none ${active && info ? "" : "invisible"} ${fit ? "max-h-full max-w-full" : ""}`}
            style={{ cursor: "default" }}
          />
        </div>

        {panel !== "none" && active && (
          <aside className="absolute top-2 right-2 w-[min(360px,calc(100%-16px))] rounded-lg border border-line bg-surface p-4 shadow-2xl">
            <div className="mb-3 flex items-center justify-between">
              <h2 className="text-sm font-semibold">{panel === "keys" ? t("remote.keys") : t("remote.clipboard")}</h2>
              <button
                type="button"
                aria-label={t("common.close")}
                className="grid size-7 place-items-center rounded-md text-muted hover:bg-surface-muted"
                onClick={() => setPanel("none")}
              >
                <X className="size-4" />
              </button>
            </div>
            {panel === "keys" ? (
              <div className="grid grid-cols-2 gap-2">
                {Object.keys(combos).map((c) => (
                  <Button key={c} size="sm" variant="secondary" onClick={() => pressCombo(c)}>
                    {t(`remote.combos.${c}`)}
                  </Button>
                ))}
              </div>
            ) : (
              <ClipboardPanel
                remoteText={remoteClip}
                onSend={(text) => {
                  send(setClipboard(text));
                  showNotice(t("remote.clipboardSent"));
                }}
                onType={(text) => {
                  send(control({ op: "type", text }));
                  canvas.current?.focus();
                }}
              />
            )}
          </aside>
        )}

        {notice && (
          <div role="status" className="absolute bottom-4 left-1/2 -translate-x-1/2 rounded-md border border-line bg-surface px-4 py-2 text-sm shadow-xl">
            {notice}
          </div>
        )}

        {!(active && info) && (
          <div className="absolute inset-0 grid place-items-center p-6">
            <div className="w-[min(440px,100%)] rounded-lg border border-line bg-surface p-6 text-center shadow-2xl">
              <Overlay
                phase={phase}
                reason={reason}
                startError={startError}
                consentLeft={consentLeft}
                hostname={hostname}
                onRetry={() => setAttempt((a) => a + 1)}
                onClose={() => window.close()}
              />
            </div>
          </div>
        )}
      </div>
    </div>
  );
}

export function Tool({ label, onClick, pressed, children }: { label: string; onClick: () => void; pressed?: boolean; children: ReactNode }) {
  return (
    <button
      type="button"
      title={label}
      aria-label={label}
      aria-pressed={pressed}
      onClick={onClick}
      className={`inline-flex h-8 min-w-8 items-center justify-center rounded-md px-2 text-muted transition-colors hover:bg-surface-muted hover:text-fg focus-visible:ring-2 focus-visible:ring-primary focus-visible:outline-none ${pressed ? "bg-surface-muted text-fg" : ""}`}
    >
      {children}
    </button>
  );
}

function StateLabel({ phase }: { phase: Phase }) {
  const { t } = useTranslation();
  return <>{t(`remote.states.${phase}`)}</>;
}

export function Overlay(props: {
  phase: Phase;
  reason: string;
  startError: string | null;
  consentLeft: number;
  hostname: string;
  onRetry: () => void;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  const { phase, reason } = props;
  if (finished(phase)) {
    const denied = phase === "denied";
    return (
      <>
        <span
          className={`mx-auto grid size-11 place-items-center rounded-full ${denied ? "bg-warning/10 text-warning" : phase === "failed" ? "bg-danger/10 text-danger" : "bg-surface-muted text-muted"}`}
        >
          <MonitorOff className="size-5" />
        </span>
        <h1 className="mt-4 text-base font-semibold">{t(`remote.finished.${phase}`)}</h1>
        <p className="mt-1.5 text-sm text-muted">
          {props.startError ?? t(`remote.reasons.${reason || "unknown"}`, { defaultValue: t("remote.reasons.unknown") })}
        </p>
        <div className="mt-5 flex justify-center gap-2">
          <Button variant="secondary" onClick={props.onClose}>
            {t("remote.closeWindow")}
          </Button>
          <Button icon={<RefreshCw className="size-4" />} onClick={props.onRetry}>
            {t("remote.reconnect")}
          </Button>
        </div>
      </>
    );
  }
  return (
    <>
      <Loader2 className="mx-auto size-7 animate-spin text-primary" />
      <h1 className="mt-4 text-base font-semibold">{t(`remote.waiting.${phase}`, { host: props.hostname })}</h1>
      {phase === "consent" ? (
        <>
          <p className="mt-1.5 text-sm text-muted">{t("remote.consentWait", { seconds: props.consentLeft })}</p>
          <div className="mx-auto mt-4 h-1 w-full overflow-hidden rounded-full bg-surface-muted">
            <div
              className="h-full rounded-full bg-primary transition-[width] duration-1000 ease-linear"
              style={{ width: `${(props.consentLeft / 30) * 100}%` }}
            />
          </div>
        </>
      ) : (
        <p className="mt-1.5 text-sm text-muted">{t("remote.waitingHint")}</p>
      )}
    </>
  );
}

function ClipboardPanel({ remoteText, onSend, onType }: { remoteText: string; onSend: (t: string) => void; onType: (t: string) => void }) {
  const { t } = useTranslation();
  const [text, setText] = useState("");
  return (
    <div className="space-y-4 text-sm">
      <div className="space-y-1.5">
        <p className="font-medium">{t("remote.fromRemote")}</p>
        {remoteText ? (
          <div className="flex gap-2">
            <pre className="max-h-24 min-w-0 flex-1 overflow-auto rounded-md border border-line bg-surface-muted p-2 text-xs whitespace-pre-wrap">
              {remoteText}
            </pre>
            <Button
              size="sm"
              variant="secondary"
              aria-label={t("common.copy")}
              title={t("common.copy")}
              icon={<ClipboardCopy className="size-4" />}
              onClick={() => navigator.clipboard?.writeText(remoteText)}
            />
          </div>
        ) : (
          <p className="text-xs text-muted">{t("remote.fromRemoteEmpty")}</p>
        )}
      </div>
      <div className="space-y-1.5">
        <label className="block font-medium" htmlFor="fw-remote-clip">
          {t("remote.toRemote")}
        </label>
        <textarea
          id="fw-remote-clip"
          rows={4}
          value={text}
          onChange={(e) => setText(e.target.value)}
          className="w-full rounded-md border border-line bg-surface p-2 text-xs focus-visible:ring-2 focus-visible:ring-primary focus-visible:outline-none"
        />
        <p className="text-xs text-muted">{t("remote.toRemoteHint")}</p>
        <div className="flex justify-end gap-2">
          <Button size="sm" variant="secondary" disabled={!text} onClick={() => onType(text)}>
            {t("remote.typeIt")}
          </Button>
          <Button size="sm" disabled={!text} onClick={() => onSend(text)}>
            {t("remote.sendClipboard")}
          </Button>
        </div>
      </div>
    </div>
  );
}
