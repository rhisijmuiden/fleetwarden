import { useCallback, useEffect, useState } from "react";
import { Link, useSearchParams } from "react-router";
import { useTranslation } from "react-i18next";
import { History, MonitorPlay, Search, Square, SquareTerminal } from "lucide-react";
import { useAuth } from "../../auth";
import { get, post, type Device, type DeviceList, type RemoteSession, type RemoteSessionList } from "../../api";
import { Alert, Badge, Button, Card, Input, PageHeader, Spinner, formatDate, useErrorText } from "../../components/ui";
import { KindIcon, StatusDot } from "../devices/util";

// openRemote opens the viewer for a device in its own tab; the tab starts
// the session.
export function openRemote(deviceId: string) {
  window.open(`/remote/connect/${deviceId}`, `fw-remote-${deviceId}`);
}

export function canRemote(d: Pick<Device, "platform" | "online">) {
  return d.platform === "windows" && d.online;
}

// openTerminal opens the terminal for a device in its own tab.
export function openTerminal(deviceId: string) {
  window.open(`/remote/terminal/${deviceId}`, `fw-terminal-${deviceId}`);
}

export function canTerminal(d: Pick<Device, "platform" | "online">) {
  return (d.platform === "windows" || d.platform === "linux") && d.online;
}

// TerminalButton opens a terminal on the device: a root shell on Linux,
// PowerShell as SYSTEM on Windows. It is hidden without remote.terminal.
export function TerminalButton({ device, size = "md", variant = "secondary" }: { device: Device; size?: "sm" | "md"; variant?: "primary" | "secondary" }) {
  const { t } = useTranslation();
  const { can } = useAuth();
  if (!can("remote.terminal")) return null;
  const supported = device.platform === "windows" || device.platform === "linux";
  return (
    <Button
      size={size}
      variant={variant}
      icon={<SquareTerminal className="size-4" />}
      disabled={!canTerminal(device)}
      title={!supported ? t("remote.term.unsupported") : !device.online ? t("devices.offlineHint") : t(`remote.term.hint.${device.platform}`)}
      onClick={() => openTerminal(device.id)}
    >
      {t("remote.term.open")}
    </Button>
  );
}

export function RemoteButton({ device, size = "md" }: { device: Device; size?: "sm" | "md" }) {
  const { t } = useTranslation();
  const ok = canRemote(device);
  return (
    <Button
      size={size}
      icon={<MonitorPlay className="size-4" />}
      disabled={!ok}
      title={device.platform !== "windows" ? t("remote.windowsOnly") : !device.online ? t("devices.offlineHint") : t("remote.connectHint")}
      onClick={() => openRemote(device.id)}
    >
      {t("remote.connect")}
    </Button>
  );
}

function duration(s: RemoteSession, lang: string): string {
  if (!s.connectedAt) return "–";
  const end = s.endedAt ? new Date(s.endedAt).getTime() : Date.now();
  const secs = Math.max(0, Math.round((end - new Date(s.connectedAt).getTime()) / 1000));
  const h = Math.floor(secs / 3600);
  const m = Math.floor((secs % 3600) / 60);
  const sec = secs % 60;
  const fmt = new Intl.NumberFormat(lang === "nl" ? "nl-NL" : "en-GB", { minimumIntegerDigits: 2 });
  return h > 0 ? `${h}:${fmt.format(m)}:${fmt.format(sec)}` : `${m}:${fmt.format(sec)}`;
}

export function SessionState({ s }: { s: RemoteSession }) {
  const { t } = useTranslation();
  const tone = s.state === "active" ? "success" : s.state === "failed" ? "danger" : s.state === "denied" ? "warning" : s.endedAt ? "neutral" : "primary";
  return (
    <div>
      <Badge tone={tone}>{t(`remote.states.${s.state}`)}</Badge>
      {s.reason && <div className="mt-1 text-xs text-muted">{t(`remote.reasons.${s.reason}`, { defaultValue: s.reason })}</div>}
    </div>
  );
}

function SessionTable({ items, onEnd, showDevice = true }: { items: RemoteSession[]; onEnd?: (s: RemoteSession) => void; showDevice?: boolean }) {
  const { t, i18n } = useTranslation();
  return (
    <div className="-mx-5 -my-5 overflow-x-auto">
      <table className="w-full min-w-[860px] text-sm">
        <thead className="border-b border-line text-left text-xs text-muted uppercase">
          <tr>
            {showDevice && <th className="px-5 py-3 font-medium">{t("remote.cols.device")}</th>}
            <th className="px-3 py-3 font-medium">{t("remote.cols.kind")}</th>
            <th className="px-3 py-3 font-medium">{t("remote.cols.operator")}</th>
            <th className="px-3 py-3 font-medium">{t("remote.cols.started")}</th>
            <th className="px-3 py-3 font-medium">{t("remote.cols.duration")}</th>
            <th className="px-3 py-3 font-medium">{t("remote.cols.consent")}</th>
            <th className="px-3 py-3 font-medium">{t("remote.cols.result")}</th>
            {onEnd && <th className="px-5 py-3" />}
          </tr>
        </thead>
        <tbody className="divide-y divide-line">
          {items.map((s) => (
            <tr key={s.id} className="hover:bg-surface-muted/50">
              {showDevice && (
                <td className="px-5 py-3">
                  <Link to={`/devices/${s.deviceId}`} className="font-medium hover:text-primary">
                    {s.hostname}
                  </Link>
                </td>
              )}
              <td className="px-3 py-3">
                <span className="inline-flex items-center gap-1.5 text-muted">
                  {s.kind === "terminal" ? <SquareTerminal className="size-4" /> : <MonitorPlay className="size-4" />}
                  {t(`remote.kinds.${s.kind}`)}
                </span>
              </td>
              <td className="px-3 py-3">
                <div>{s.operator}</div>
                <div className="text-xs text-muted">{s.remoteIp}</div>
              </td>
              <td className="px-3 py-3 whitespace-nowrap text-muted">{formatDate(s.createdAt, i18n.language)}</td>
              <td className="px-3 py-3 font-mono text-xs tabular-nums">{duration(s, i18n.language)}</td>
              <td className="px-3 py-3 text-muted">{t(`remote.consentShort.${s.consent}`)}</td>
              <td className="px-3 py-3">
                <SessionState s={s} />
              </td>
              {onEnd && (
                <td className="px-5 py-3 text-right">
                  <Button size="sm" variant="secondary" icon={<Square className="size-3.5" />} onClick={() => onEnd(s)}>
                    {t("remote.end")}
                  </Button>
                </td>
              )}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

export function Remote() {
  const { t } = useTranslation();
  const errorText = useErrorText();
  const [params, setParams] = useSearchParams();
  const deviceFilter = params.get("device") ?? "";
  const [search, setSearch] = useState("");
  const [devices, setDevices] = useState<Device[] | null>(null);
  const [open, setOpen] = useState<RemoteSession[]>([]);
  const [history, setHistory] = useState<RemoteSessionList | null>(null);
  const [error, setError] = useState<string | null>(null);

  const loadSessions = useCallback(() => {
    get<RemoteSessionList>("/remote/sessions?open=true&limit=50")
      .then((l) => setOpen(l.items))
      .catch((e) => setError(errorText(e)));
    get<RemoteSessionList>(`/remote/sessions?limit=100${deviceFilter ? `&device=${encodeURIComponent(deviceFilter)}` : ""}`)
      .then(setHistory)
      .catch((e) => setError(errorText(e)));
  }, [deviceFilter]);

  useEffect(() => {
    loadSessions();
    const id = setInterval(loadSessions, 5000);
    return () => clearInterval(id);
  }, [loadSessions]);

  useEffect(() => {
    const ctl = new AbortController();
    const q = new URLSearchParams({ status: "online", limit: "24" });
    if (search.trim()) q.set("search", search.trim());
    const id = setTimeout(() => {
      get<DeviceList>(`/devices?${q}`, ctl.signal)
        .then((l) => setDevices(l.items.filter((d) => d.platform === "windows" || d.platform === "linux").slice(0, 12)))
        .catch(() => {});
    }, 200);
    return () => {
      clearTimeout(id);
      ctl.abort();
    };
  }, [search]);

  const end = (s: RemoteSession) =>
    post(`/remote/sessions/${s.id}/end`)
      .then(loadSessions)
      .catch((e) => setError(errorText(e)));

  return (
    <div className="space-y-6">
      <PageHeader title={t("remote.title")} description={t("remote.subtitle")} />
      {error && <Alert tone="danger">{error}</Alert>}

      <Card title={t("remote.connectTitle")} description={t("remote.connectText")}>
        <div className="relative mb-4 max-w-md">
          <Search className="pointer-events-none absolute top-2.5 left-3 size-4 text-muted" />
          <Input
            className="pl-9"
            placeholder={t("remote.searchPlaceholder")}
            aria-label={t("remote.searchPlaceholder")}
            value={search}
            onChange={(e) => setSearch(e.target.value)}
          />
        </div>
        {!devices ? (
          <Spinner />
        ) : devices.length === 0 ? (
          <p className="text-sm text-muted">{t("remote.noDevices")}</p>
        ) : (
          <ul className="grid gap-2 md:grid-cols-2 2xl:grid-cols-3">
            {devices.map((d) => (
              <li key={d.id} className="flex items-center gap-3 rounded-md border border-line px-3 py-2.5">
                <span className="grid size-9 shrink-0 place-items-center rounded-md bg-primary/10 text-primary">
                  <KindIcon device={d} className="size-4" />
                </span>
                <div className="min-w-0 flex-1">
                  <p className="truncate text-sm font-medium">{d.hostname}</p>
                  <p className="flex items-center gap-1.5 truncate text-xs text-muted">
                    <StatusDot online={d.online} /> {d.loggedInUser || t("devices.nobody")}
                  </p>
                </div>
                <div className="flex shrink-0 gap-1.5">
                  {d.platform === "windows" && (
                    <Button size="sm" variant="secondary" icon={<MonitorPlay className="size-4" />} onClick={() => openRemote(d.id)}>
                      {t("remote.connect")}
                    </Button>
                  )}
                  <TerminalButton device={d} size="sm" />
                </div>
              </li>
            ))}
          </ul>
        )}
      </Card>

      {open.length > 0 && (
        <Card title={t("remote.activeTitle")}>
          <SessionTable items={open} onEnd={end} />
        </Card>
      )}

      <Card
        title={t("remote.historyTitle")}
        description={t("remote.historyText")}
        actions={
          deviceFilter ? (
            <Button size="sm" variant="ghost" onClick={() => setParams({})}>
              {t("remote.allDevices")}
            </Button>
          ) : undefined
        }
      >
        {!history ? (
          <Spinner />
        ) : history.items.length === 0 ? (
          <div className="flex flex-col items-center gap-3 py-8 text-center">
            <span className="grid size-12 place-items-center rounded-full bg-primary/10 text-primary">
              <History className="size-6" />
            </span>
            <p className="text-sm text-muted">{t("remote.historyEmpty")}</p>
          </div>
        ) : (
          <SessionTable items={history.items} />
        )}
      </Card>
    </div>
  );
}
