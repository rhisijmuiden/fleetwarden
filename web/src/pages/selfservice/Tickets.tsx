import { useEffect, useState, type ReactNode } from "react";
import { Link } from "react-router";
import { useTranslation } from "react-i18next";
import { Cpu, ExternalLink, Eye, LifeBuoy, Mail, Monitor, Package, Plug, RefreshCw, Server, ShieldCheck, SquareTerminal, MonitorPlay, TriangleAlert } from "lucide-react";
import { get, post, type Device, type DeviceList } from "../../api";
import { useAuth } from "../../auth";
import { Alert, Badge, Button, Card, Field, Input, Modal, Select, Spinner, Switch, formatDate, useErrorText } from "../../components/ui";
import { RemoteButton, TerminalButton } from "../remote/Remote";
import type { Integration } from "../admin/Integrations";
import type { HelpRequest, SelfServiceSettings, TicketDestination, TicketReport } from "./types";

export const reportGroups: { key: string; parts: string[] }[] = [
  { key: "basics", parts: ["user", "time", "network", "os"] },
  { key: "hardware", parts: ["hardware", "disk", "uptime"] },
  { key: "status", parts: ["reboot", "updates", "software", "security", "alerts"] },
];
const linkActions = ["remote", "terminal", "scan"];

function Choice({ on, onClick, icon, title, hint, children }: { on: boolean; onClick: () => void; icon: ReactNode; title: string; hint: string; children?: ReactNode }) {
  return (
    <div
      role="radio"
      aria-checked={on}
      tabIndex={0}
      onClick={onClick}
      onKeyDown={(e) => (e.key === " " || e.key === "Enter") && (e.preventDefault(), onClick())}
      className={`cursor-pointer rounded-lg border p-4 outline-none focus-visible:ring-2 focus-visible:ring-primary ${on ? "border-primary bg-primary/5 ring-1 ring-primary" : "border-line hover:border-primary/50"}`}
    >
      <div className="flex items-start gap-3">
        <span className={`mt-0.5 grid size-4 shrink-0 place-items-center rounded-full border ${on ? "border-primary" : "border-line"}`}>{on && <span className="size-2 rounded-full bg-primary" />}</span>
        <div className="min-w-0 flex-1">
          <p className="flex items-center gap-2 text-sm font-medium">
            <span className="text-muted">{icon}</span>
            {title}
          </p>
          <p className="mt-1 text-xs text-muted">{hint}</p>
          {on && children && (
            <div className="mt-3 space-y-3" onClick={(e) => e.stopPropagation()}>
              {children}
            </div>
          )}
        </div>
      </div>
    </div>
  );
}

function Check({ on, onChange, label, hint }: { on: boolean; onChange: (v: boolean) => void; label: string; hint?: string }) {
  return (
    <label className="flex cursor-pointer items-start gap-2.5 text-sm">
      <input type="checkbox" checked={on} onChange={(e) => onChange(e.target.checked)} className="mt-0.5 size-4 accent-primary" />
      <span>
        <span className="font-medium">{label}</span>
        {hint && <span className="block text-xs text-muted">{hint}</span>}
      </span>
    </label>
  );
}

const textarea = "w-full rounded-md border border-line bg-surface px-3 py-2 text-sm outline-none focus-visible:ring-2 focus-visible:ring-primary";

/** The ticket part of the self-service settings: destination, form, report and link. */
export function TicketSettings({ s, setS }: { s: SelfServiceSettings; setS: (s: SelfServiceSettings) => void }) {
  const { t, i18n } = useTranslation();
  const [integrations, setIntegrations] = useState<Integration[] | null>(null);
  const [cats, setCats] = useState(s.categories.join(", "));
  useEffect(() => {
    get<Integration[]>("/integrations")
      .then((l) => setIntegrations(l.filter((i) => i.serviceDesk || i.kind === "custom")))
      .catch(() => setIntegrations([]));
  }, []);
  useEffect(() => setCats(s.categories.join(", ")), [s.categories]);
  const dest = (d: TicketDestination) => {
    const next = { ...s, destination: d };
    if (d === "integration" && !s.ticketIntegration && integrations?.length) next.ticketIntegration = integrations[0].id;
    setS(next);
  };
  const toggle = (list: string[], v: string, on: boolean) => (on ? [...list.filter((x) => x !== v), v] : list.filter((x) => x !== v));
  const address = (
    <Field label={t("tickets.address")} hint={i18n.exists(`tickets.addressHint.${s.destination}`) ? t(`tickets.addressHint.${s.destination}`) : undefined}>
      {(id) => <Input id={id} type="email" value={s.ticketAddress} onChange={(e) => setS({ ...s, ticketAddress: e.target.value })} placeholder="servicedesk@example.com" />}
    </Field>
  );
  const chosen = integrations?.find((i) => i.id === s.ticketIntegration);
  return (
    <>
      <Card title={t("tickets.destTitle")} description={t("tickets.destHint")}>
        <div role="radiogroup" className="grid gap-3 md:grid-cols-2">
          <Choice on={s.destination === "fleetwarden"} onClick={() => dest("fleetwarden")} icon={<LifeBuoy className="size-4" />} title={t("tickets.dest.fleetwarden")} hint={t("tickets.destText.fleetwarden")} />
          <Choice on={s.destination === "outlook"} onClick={() => dest("outlook")} icon={<Mail className="size-4" />} title={t("tickets.dest.outlook")} hint={t("tickets.destText.outlook")}>
            {address}
          </Choice>
          <Choice on={s.destination === "mail"} onClick={() => dest("mail")} icon={<Server className="size-4" />} title={t("tickets.dest.mail")} hint={t("tickets.destText.mail")}>
            {address}
          </Choice>
          <Choice on={s.destination === "integration"} onClick={() => dest("integration")} icon={<Plug className="size-4" />} title={t("tickets.dest.integration")} hint={t("tickets.destText.integration")}>
            {!integrations ? (
              <Spinner />
            ) : integrations.length === 0 ? (
              <p className="text-sm">
                {t("tickets.noIntegrations")}{" "}
                <Link to="/admin/integrations" className="font-medium text-primary hover:underline">
                  {t("tickets.addIntegration")}
                </Link>
              </p>
            ) : (
              <>
                <Select value={s.ticketIntegration} onChange={(e) => setS({ ...s, ticketIntegration: Number(e.target.value) })} aria-label={t("tickets.dest.integration")}>
                  {integrations.map((i) => (
                    <option key={i.id} value={i.id}>
                      {i.name}
                      {i.settings.url ? ` (${i.settings.url.replace(/^https?:\/\//, "")})` : ""}
                      {i.enabled ? "" : ` – ${t("tickets.disabled")}`}
                    </option>
                  ))}
                </Select>
                {chosen && !chosen.enabled && <Alert tone="warning">{t("tickets.integrationOff")}</Alert>}
                <Switch checked={s.fallbackMail} onChange={(v) => setS({ ...s, fallbackMail: v })} label={t("tickets.fallback")} description={t("tickets.fallbackHint")} />
                {s.fallbackMail && (
                  <Field label={t("tickets.address")} hint={t("tickets.addressHint.fallback")}>
                    {(id) => <Input id={id} type="email" value={s.ticketAddress} onChange={(e) => setS({ ...s, ticketAddress: e.target.value })} placeholder="servicedesk@example.com" />}
                  </Field>
                )}
              </>
            )}
          </Choice>
        </div>
        <p className="mt-4 text-xs text-muted">{t("tickets.alwaysKept")}</p>
      </Card>

      <Card title={t("tickets.formTitle")} description={t("tickets.formHint")}>
        <div className="grid gap-x-8 gap-y-4 md:grid-cols-2">
          <div className="space-y-3">
            <Switch checked={s.requireMessage} onChange={(v) => setS({ ...s, requireMessage: v, minMessage: v && !s.minMessage ? 20 : s.minMessage })} label={t("tickets.requireMessage")} description={t("tickets.requireMessageHint")} />
            {s.requireMessage && (
              <Field label={t("tickets.minMessage")}>
                {(id) => <Input id={id} type="number" min={1} max={500} value={s.minMessage} onChange={(e) => setS({ ...s, minMessage: Math.max(0, Number(e.target.value) || 0) })} className="w-32" />}
              </Field>
            )}
          </div>
          <Switch checked={s.askUrgency} onChange={(v) => setS({ ...s, askUrgency: v })} label={t("tickets.askUrgency")} description={t("tickets.askUrgencyHint")} />
          <div className="space-y-3">
            <Switch checked={s.askCategory} onChange={(v) => setS({ ...s, askCategory: v })} label={t("tickets.askCategory")} description={t("tickets.askCategoryHint")} />
            {s.askCategory && (
              <Field label={t("tickets.categories")} hint={t("tickets.categoriesHint")}>
                {(id) => (
                  <Input
                    id={id}
                    value={cats}
                    onChange={(e) => setCats(e.target.value)}
                    onBlur={() => setS({ ...s, categories: cats.split(",").map((c) => c.trim()).filter(Boolean) })}
                    placeholder={t("tickets.categoriesPlaceholder")}
                  />
                )}
              </Field>
            )}
          </div>
          <Switch checked={s.askContact} onChange={(v) => setS({ ...s, askContact: v })} label={t("tickets.askContact")} description={t("tickets.askContactHint")} />
          <Switch checked={s.showSent} onChange={(v) => setS({ ...s, showSent: v })} label={t("tickets.showSent")} description={t("tickets.showSentHint")} />
        </div>
        <Field className="mt-5" label={t("selfservice.settings.helpText")} hint={t("selfservice.settings.helpTextHint")}>
          {(id) => <textarea id={id} value={s.helpText} maxLength={500} rows={2} onChange={(e) => setS({ ...s, helpText: e.target.value })} className={textarea} />}
        </Field>
      </Card>

      <Card title={t("tickets.reportTitle")} description={t("tickets.reportHint")}>
        <div className="grid gap-6 md:grid-cols-3">
          {reportGroups.map((g) => (
            <div key={g.key} className="space-y-3">
              <p className="text-xs font-medium tracking-wide text-muted uppercase">{t(`tickets.groups.${g.key}`)}</p>
              {g.parts.map((p) => (
                <Check key={p} on={s.report.includes(p)} onChange={(v) => setS({ ...s, report: toggle(s.report, p, v) })} label={t(`tickets.parts.${p}`)} hint={i18n.exists(`tickets.partHints.${p}`) ? t(`tickets.partHints.${p}`) : undefined} />
              ))}
            </div>
          ))}
        </div>
        <p className="mt-5 border-t border-line pt-4 text-xs text-muted">{t("tickets.reportNote")}</p>
      </Card>

      <Card title={t("tickets.linkTitle")} description={t("tickets.linkHint")}>
        <div className="space-y-4">
          <Switch checked={s.linkDevice} onChange={(v) => setS({ ...s, linkDevice: v })} label={t("tickets.linkDevice")} description={t("tickets.linkDeviceHint")} />
          {s.linkDevice && (
            <>
              <p className="text-sm font-medium">{t("tickets.linkActions")}</p>
              <div className="grid gap-3 sm:grid-cols-3">
                {linkActions.map((a) => (
                  <Check key={a} on={s.linkActions.includes(a)} onChange={(v) => setS({ ...s, linkActions: toggle(s.linkActions, a, v) })} label={t(`tickets.actions.${a}`)} hint={t(`tickets.actionHints.${a}`)} />
                ))}
              </div>
              <Alert tone="info">{t("tickets.linkSafe")}</Alert>
            </>
          )}
        </div>
      </Card>
    </>
  );
}

// ---- report ----

const toneOf = (tone?: string) => (tone === "success" || tone === "warning" || tone === "danger" ? tone : undefined);
const sectionIcon: Record<string, ReactNode> = {
  device: <Monitor className="size-4" />,
  hardware: <Cpu className="size-4" />,
  status: <ShieldCheck className="size-4" />,
  software: <Package className="size-4" />,
  alerts: <TriangleAlert className="size-4" />,
};
const linkIcon: Record<string, ReactNode> = {
  device: <ExternalLink className="size-4" />,
  remote: <MonitorPlay className="size-4" />,
  terminal: <SquareTerminal className="size-4" />,
  scan: <RefreshCw className="size-4" />,
};

/** ReportView shows the device report of a ticket as the service desk sees it. */
export function ReportView({ report, links = true }: { report: TicketReport; links?: boolean }) {
  return (
    <div className="space-y-5">
      {links && report.links.length > 0 && (
        <div className="flex flex-wrap gap-2 rounded-lg bg-surface-muted p-3">
          {report.links.map((l, i) => (
            <a
              key={l.action}
              href={l.url}
              className={`inline-flex h-9 items-center gap-2 rounded-md px-4 text-sm font-medium ${i === 0 ? "bg-primary text-primary-contrast" : "border border-line bg-surface hover:bg-surface-muted"}`}
            >
              {linkIcon[l.action]}
              {l.label}
            </a>
          ))}
        </div>
      )}
      {report.summary.length > 0 && (
        <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
          {report.summary.map((l) => (
            <div key={l.label} className="rounded-md border border-line p-3">
              <p className="text-xs text-muted">{l.label}</p>
              <div className="mt-1">{toneOf(l.tone) ? <Badge tone={toneOf(l.tone)}>{l.value}</Badge> : <span className="text-sm font-medium">{l.value}</span>}</div>
            </div>
          ))}
        </div>
      )}
      <div className="grid gap-6 lg:grid-cols-2">
        {report.sections.map((sec) => (
          <div key={sec.key}>
            <p className="mb-2 flex items-center gap-2 text-sm font-semibold">
              <span className="text-primary">{sectionIcon[sec.key] ?? <Monitor className="size-4" />}</span>
              {sec.title}
            </p>
            <table className="w-full text-sm">
              <tbody>
                {sec.lines.map((l, i) => (
                  <tr key={i} className="border-b border-line last:border-0">
                    <td className="w-44 py-1.5 pr-4 align-top text-muted">{l.label}</td>
                    <td className="py-1.5 break-words">{toneOf(l.tone) && l.tone !== "success" ? <Badge tone={toneOf(l.tone)}>{l.value}</Badge> : l.value}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        ))}
      </div>
    </div>
  );
}

// ---- preview ----

type Preview = { hostname: string; subject: string; destination: TicketDestination; target: string; report: TicketReport; html: string; text: string; message: string; publicUrl: boolean };

/** TicketPreview shows the ticket a real device would send with the settings in the form. */
export function TicketPreview({ settings, onClose }: { settings: SelfServiceSettings; onClose: () => void }) {
  const { t } = useTranslation();
  const errorText = useErrorText();
  const [devices, setDevices] = useState<Device[]>([]);
  const [device, setDevice] = useState("");
  const [p, setP] = useState<Preview | null>(null);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => {
    get<DeviceList>("/devices?limit=50")
      .then((r) => setDevices(r.items ?? []))
      .catch(() => {});
  }, []);
  useEffect(() => {
    setP(null);
    post<Preview>("/selfservice/ticket-preview", { settings, deviceId: device })
      .then((v) => {
        setP(v);
        setError(null);
      })
      .catch((e) => setError(errorText(e)));
  }, [device]);
  return (
    <Modal
      open
      wide
      onClose={onClose}
      title={t("tickets.previewTitle")}
      footer={
        <Button variant="secondary" onClick={onClose}>
          {t("common.close")}
        </Button>
      }
    >
      <div className="flex flex-wrap items-end justify-between gap-3">
        <p className="max-w-md text-sm text-muted">{p ? t(`tickets.previewTo.${p.destination}`, { target: p.target || "–" }) : t("tickets.previewHint")}</p>
        {devices.length > 0 && (
          <Select value={device} onChange={(e) => setDevice(e.target.value)} aria-label={t("tickets.previewDevice")} className="w-60">
            <option value="">{t("tickets.previewAny")}</option>
            {devices.map((d) => (
              <option key={d.id} value={d.id}>
                {d.hostname}
                {d.loggedInUser ? ` (${d.loggedInUser})` : ""}
              </option>
            ))}
          </Select>
        )}
      </div>
      {error && <Alert tone="danger">{error}</Alert>}
      {!p && !error && <Spinner />}
      {p && (
        <section className="rounded-lg border border-line bg-surface">
          <header className="border-b border-line px-5 py-4">
            <h2 className="text-base font-semibold">{p.subject}</h2>
          </header>
          <div className="space-y-5 px-5 py-4">
            <p className="text-sm whitespace-pre-line">{p.message}</p>
            {!p.publicUrl && settings.linkDevice && <Alert tone="warning">{t("tickets.noPublicUrl")}</Alert>}
            <ReportView report={p.report} />
          </div>
        </section>
      )}
    </Modal>
  );
}

export function PreviewButton({ settings }: { settings: SelfServiceSettings }) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  return (
    <>
      <Button variant="secondary" icon={<Eye className="size-4" />} onClick={() => setOpen(true)}>
        {t("tickets.preview")}
      </Button>
      {open && <TicketPreview settings={settings} onClose={() => setOpen(false)} />}
    </>
  );
}

// ---- device page opened from a ticket ----

/**
 * TicketBanner shows on the device page when a technician opens the link in a
 * ticket: what the user reported, and the action the link was for. Actions
 * start only on a click (consent and pop-up blockers).
 */
export function TicketBanner({ device, ticket, action, onNotice }: { device: Device; ticket: number; action: string; onNotice: (m: string) => void }) {
  const { t, i18n } = useTranslation();
  const { can } = useAuth();
  const errorText = useErrorText();
  const [help, setHelp] = useState<HelpRequest | null>(null);
  useEffect(() => {
    if (!ticket) return;
    get<{ items: HelpRequest[] }>(`/selfservice/help?device=${encodeURIComponent(device.id)}&limit=50`)
      .then((r) => setHelp(r.items.find((h) => h.id === ticket) ?? null))
      .catch(() => {});
  }, [ticket, device.id]);
  const scan = () =>
    Promise.all([post(`/devices/${device.id}/inventory`), device.platform === "windows" ? post(`/devices/${device.id}/patches/scan`).catch(() => {}) : null])
      .then(() => onNotice(t("tickets.scanStarted")))
      .catch((e) => onNotice(errorText(e)));
  return (
    <section className="rounded-lg border border-primary/40 bg-primary/5 p-4">
      <div className="flex flex-wrap items-start justify-between gap-4">
        <div className="min-w-0 flex-1 space-y-1">
          <p className="flex flex-wrap items-center gap-2 text-sm font-medium">
            <LifeBuoy className="size-4 text-primary" />
            {help ? t("tickets.fromTicket", { id: help.id }) : t("tickets.fromTicketUnknown")}
            {help?.ticketNumber &&
              (help.ticketUrl ? (
                <a href={help.ticketUrl} target="_blank" rel="noreferrer">
                  <Badge tone="primary">{help.ticketNumber}</Badge>
                </a>
              ) : (
                <Badge tone="primary">{help.ticketNumber}</Badge>
              ))}
            {help?.urgency === "high" && <Badge tone="danger">{t("tickets.urgency.high")}</Badge>}
          </p>
          {help && (
            <>
              <p className="font-semibold">{help.subject}</p>
              {help.message && <p className="line-clamp-3 text-sm whitespace-pre-line">{help.message}</p>}
              <p className="text-xs text-muted">
                {[help.username || t("selfservice.unknownUser"), help.email, formatDate(help.createdAt, i18n.language)].filter(Boolean).join(" · ")}
              </p>
            </>
          )}
        </div>
        <div className="flex flex-wrap gap-2">
          {action === "remote" && can("remote.control") && device.platform === "windows" && <RemoteButton device={device} />}
          {action === "terminal" && <TerminalButton device={device} variant="primary" />}
          {action === "scan" && can("devices.manage") && (
            <Button icon={<RefreshCw className="size-4" />} disabled={!device.online} title={!device.online ? t("devices.offlineHint") : undefined} onClick={scan}>
              {t("tickets.actions.scan")}
            </Button>
          )}
          <Link to={`/selfservice?tab=help`} className="inline-flex h-9 items-center rounded-md border border-line bg-surface px-4 text-sm font-medium hover:bg-surface-muted">
            {t("tickets.openHelp")}
          </Link>
        </div>
      </div>
      {!device.online && <p className="mt-3 text-xs text-muted">{t("tickets.offline")}</p>}
    </section>
  );
}
