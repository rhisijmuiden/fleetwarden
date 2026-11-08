import { useCallback, useEffect, useState } from "react";
import { Link, useSearchParams } from "react-router";
import { useTranslation } from "react-i18next";
import { BellRing, Check, CheckCircle2, HardDrive, Pencil, Plus, Trash2, WifiOff } from "lucide-react";
import { del, get, post, put, type AlertRule, type DeviceKind, type FleetAlert } from "../../api";
import { useAuth } from "../../auth";
import { useGroups } from "../../components/GroupScope";
import { formatInterval, IntervalInput } from "../../components/IntervalInput";
import { Alert, Badge, Button, Card, Field, Input, Modal, PageHeader, Select, Spinner, Switch, formatDate, useErrorText } from "../../components/ui";
import { timeAgo } from "../devices/util";

type Tab = "open" | "history" | "rules";

export function Alerts() {
  const { t } = useTranslation();
  const { can, me } = useAuth();
  const [params, setParams] = useSearchParams();
  const rules = can("alerts.manage") && !me?.scoped;
  const tabs: Tab[] = ["open", "history", ...(rules ? (["rules"] as Tab[]) : [])];
  const tab = (tabs.includes(params.get("tab") as Tab) ? params.get("tab") : "open") as Tab;
  const setTab = (k: Tab) => setParams(k === "open" ? {} : { tab: k });

  return (
    <div className="space-y-6">
      <PageHeader title={t("alerts.title")} description={t("alerts.subtitle")} />
      <div className="flex gap-1 overflow-x-auto border-b border-line" role="tablist">
        {tabs.map((k) => (
          <button
            key={k}
            role="tab"
            aria-selected={tab === k}
            onClick={() => setTab(k)}
            className={`-mb-px border-b-2 px-4 py-2 text-sm font-medium whitespace-nowrap ${tab === k ? "border-primary text-fg" : "border-transparent text-muted hover:text-fg"}`}
          >
            {t(`alerts.tabs.${k}`)}
          </button>
        ))}
      </div>
      {tab === "rules" ? <Rules /> : <AlertList history={tab === "history"} />}
    </div>
  );
}

function KindIcon({ kind, resolved }: { kind: FleetAlert["kind"]; resolved: boolean }) {
  const tone = resolved ? "bg-success/10 text-success" : kind === "offline" ? "bg-danger/10 text-danger" : "bg-warning/10 text-warning";
  return (
    <span className={`grid size-9 shrink-0 place-items-center rounded-md ${tone}`}>
      {resolved ? <CheckCircle2 className="size-4" /> : kind === "offline" ? <WifiOff className="size-4" /> : <HardDrive className="size-4" />}
    </span>
  );
}

function AlertList({ history }: { history: boolean }) {
  const { t, i18n } = useTranslation();
  const { can } = useAuth();
  const errorText = useErrorText();
  const [list, setList] = useState<FleetAlert[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState<number | null>(null);

  const load = useCallback(() => {
    get<{ alerts: FleetAlert[] }>(`/alerts${history ? "?state=all" : ""}`)
      .then((r) => {
        setList(r.alerts);
        setError(null);
      })
      .catch((e) => setError(errorText(e)));
  }, [history]);
  useEffect(() => {
    setList(null);
    load();
    const id = setInterval(load, 60_000);
    return () => clearInterval(id);
  }, [load]);

  const acknowledge = (a: FleetAlert) => {
    setBusy(a.id);
    post(`/alerts/${a.id}/acknowledge`)
      .then(() => {
        load();
        window.dispatchEvent(new Event("fw:alerts"));
      })
      .catch((e) => setError(errorText(e)))
      .finally(() => setBusy(null));
  };

  if (!list) return error ? <Alert tone="danger">{error}</Alert> : <Spinner />;
  return (
    <div className="space-y-4">
      {error && <Alert tone="danger">{error}</Alert>}
      <Card title={history ? t("alerts.historyTitle") : t("alerts.openTitle", { count: list.length })}>
        {list.length === 0 ? (
          <div className="grid place-items-center rounded-md border border-dashed border-line px-6 py-10 text-center">
            <CheckCircle2 className="size-8 text-success" strokeWidth={1.5} />
            <p className="mt-3 max-w-sm text-sm text-muted">{history ? t("alerts.noHistory") : t("alerts.none")}</p>
          </div>
        ) : (
          <ul className="-mx-5 -my-5 divide-y divide-line">
            {list.map((a) => (
              <li key={a.id} className="flex flex-wrap items-start gap-4 px-5 py-4">
                <KindIcon kind={a.kind} resolved={!!a.resolvedAt} />
                <div className="min-w-0 flex-1 space-y-1">
                  <div className="flex flex-wrap items-center gap-2">
                    <Link to={`/devices/${a.deviceId}`} className="font-medium hover:text-primary">
                      {a.hostname}
                    </Link>
                    <span className="text-sm text-muted">{a.groupName}</span>
                    <Badge tone={a.resolvedAt ? "success" : a.kind === "offline" ? "danger" : "warning"}>{a.ruleName}</Badge>
                    {a.acknowledgedAt && !a.resolvedAt && <Badge>{t("alerts.acknowledged")}</Badge>}
                  </div>
                  <p className="text-sm">{a.text}</p>
                  <p className="text-xs text-muted">
                    {t("alerts.opened", { when: timeAgo(a.openedAt, i18n.language), date: formatDate(a.openedAt, i18n.language) })}
                    {a.resolvedAt && ` · ${t("alerts.resolved", { date: formatDate(a.resolvedAt, i18n.language) })}`}
                    {a.acknowledgedAt && ` · ${t("alerts.acknowledgedBy", { user: a.acknowledgedBy, date: formatDate(a.acknowledgedAt, i18n.language) })}`}
                  </p>
                </div>
                {!a.resolvedAt && !a.acknowledgedAt && can("alerts.manage") && (
                  <Button size="sm" variant="secondary" icon={<Check className="size-4" />} loading={busy === a.id} onClick={() => acknowledge(a)}>
                    {t("alerts.acknowledge")}
                  </Button>
                )}
              </li>
            ))}
          </ul>
        )}
      </Card>
    </div>
  );
}

type Draft = Omit<AlertRule, "id" | "openAlerts" | "createdBy"> & { id?: number };

const emptyDraft = (lang: string): Draft => ({
  name: "",
  enabled: true,
  kind: "offline",
  threshold: 60,
  groupIds: [],
  deviceKinds: ["server", "hypervisor"],
  recipients: [],
  language: lang === "nl" ? "nl" : "en",
  notifyResolved: true,
});

const deviceKinds: DeviceKind[] = ["workstation", "server", "hypervisor"];

function Rules() {
  const { t, i18n } = useTranslation();
  const errorText = useErrorText();
  const groups = useGroups();
  const [list, setList] = useState<AlertRule[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [draft, setDraft] = useState<Draft | null>(null);

  const load = useCallback(() => {
    get<{ rules: AlertRule[] }>("/alerts/rules")
      .then((r) => setList(r.rules))
      .catch((e) => setError(errorText(e)));
  }, []);
  useEffect(load, [load]);

  const remove = (r: AlertRule) => {
    if (!confirm(t("alerts.rules.confirmDelete", { name: r.name }))) return;
    del(`/alerts/rules/${r.id}`)
      .then(load)
      .catch((e) => setError(errorText(e)));
  };
  const condition = (r: Pick<AlertRule, "kind" | "threshold">) =>
    r.kind === "offline" ? t("alerts.rules.offlineFor", { time: formatInterval(r.threshold, t) }) : t("alerts.rules.diskBelow", { percent: r.threshold });
  const scope = (r: AlertRule) => {
    const kinds = r.deviceKinds.length ? r.deviceKinds.map((k) => t(`alerts.kinds.${k}`)).join(", ") : t("alerts.kinds.all");
    const names = r.groupIds.length ? r.groupIds.map((id) => groups?.find((g) => g.id === id)?.name ?? `#${id}`).join(", ") : t("reports.allGroups");
    return `${kinds} · ${names}`;
  };

  if (!list) return error ? <Alert tone="danger">{error}</Alert> : <Spinner />;
  return (
    <div className="space-y-4">
      {error && <Alert tone="danger">{error}</Alert>}
      <Card
        title={t("alerts.rules.title")}
        description={t("alerts.rules.subtitle")}
        actions={
          <Button icon={<Plus className="size-4" />} onClick={() => setDraft(emptyDraft(i18n.language))}>
            {t("alerts.rules.new")}
          </Button>
        }
      >
        {list.length === 0 ? (
          <div className="grid place-items-center rounded-md border border-dashed border-line px-6 py-10 text-center">
            <BellRing className="size-8 text-muted" strokeWidth={1.5} />
            <p className="mt-3 max-w-sm text-sm text-muted">{t("alerts.rules.empty")}</p>
          </div>
        ) : (
          <ul className="-mx-5 -my-5 divide-y divide-line">
            {list.map((r) => (
              <li key={r.id} className="flex flex-wrap items-start gap-4 px-5 py-4">
                <span className="grid size-9 shrink-0 place-items-center rounded-md bg-primary/10 text-primary">
                  {r.kind === "offline" ? <WifiOff className="size-4" /> : <HardDrive className="size-4" />}
                </span>
                <div className="min-w-0 flex-1 space-y-1">
                  <div className="flex flex-wrap items-center gap-2">
                    <span className="font-medium">{r.name}</span>
                    {!r.enabled && <Badge>{t("alerts.rules.paused")}</Badge>}
                    {r.openAlerts > 0 && <Badge tone="danger">{t("alerts.rules.open", { count: r.openAlerts })}</Badge>}
                  </div>
                  <p className="text-sm">{condition(r)}</p>
                  <p className="text-xs text-muted">
                    {scope(r)} · {r.recipients.length ? r.recipients.join(", ") : t("alerts.rules.noRecipients")}
                  </p>
                </div>
                <div className="flex gap-1">
                  <Button
                    size="sm"
                    variant="ghost"
                    icon={<Pencil className="size-4" />}
                    aria-label={t("common.edit")}
                    title={t("common.edit")}
                    onClick={() => setDraft({ ...r })}
                  />
                  <Button
                    size="sm"
                    variant="ghost"
                    icon={<Trash2 className="size-4" />}
                    aria-label={t("common.delete")}
                    title={t("common.delete")}
                    onClick={() => remove(r)}
                  />
                </div>
              </li>
            ))}
          </ul>
        )}
      </Card>
      <RuleForm draft={draft} onClose={() => setDraft(null)} onSaved={load} />
    </div>
  );
}

function RuleForm({ draft: initial, onClose, onSaved }: { draft: Draft | null; onClose: () => void; onSaved: () => void }) {
  const { t } = useTranslation();
  const errorText = useErrorText();
  const groups = useGroups();
  const [d, setD] = useState<Draft | null>(initial);
  const [rcpt, setRcpt] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => {
    setD(initial);
    setRcpt(initial?.recipients.join(", ") ?? "");
    setError(null);
  }, [initial]);
  if (!d) return null;
  const set = (p: Partial<Draft>) => setD({ ...d, ...p });
  const toggle = <T,>(list: T[], v: T, on: boolean) => (on ? [...list.filter((x) => x !== v), v] : list.filter((x) => x !== v));
  const setKind = (kind: Draft["kind"]) => set({ kind, threshold: kind === "offline" ? 60 : 10 });

  const save = () => {
    setBusy(true);
    setError(null);
    const body = { ...d, recipients: rcpt.split(/[,;\s]+/).filter(Boolean) };
    (d.id ? put(`/alerts/rules/${d.id}`, body) : post("/alerts/rules", body))
      .then(() => {
        onSaved();
        onClose();
      })
      .catch((e) => setError(errorText(e)))
      .finally(() => setBusy(false));
  };

  return (
    <Modal
      open
      wide
      onClose={onClose}
      title={d.id ? t("alerts.rules.edit") : t("alerts.rules.new")}
      footer={
        <>
          <Button variant="secondary" onClick={onClose}>
            {t("common.cancel")}
          </Button>
          <Button onClick={save} loading={busy}>
            {t("common.save")}
          </Button>
        </>
      }
    >
      {error && <Alert tone="danger">{error}</Alert>}
      <Field label={t("alerts.rules.name")}>
        {(id) => <Input id={id} value={d.name} onChange={(e) => set({ name: e.target.value })} placeholder={t("alerts.rules.namePlaceholder")} />}
      </Field>
      <div className="grid gap-4 sm:grid-cols-2">
        <Field label={t("alerts.rules.kind")}>
          {(id) => (
            <Select id={id} value={d.kind} onChange={(e) => setKind(e.target.value as Draft["kind"])}>
              <option value="offline">{t("alerts.rules.kindOffline")}</option>
              <option value="disk">{t("alerts.rules.kindDisk")}</option>
            </Select>
          )}
        </Field>
        {d.kind === "offline" ? (
          <Field label={t("alerts.rules.offlineAfter")} hint={t("alerts.rules.offlineHint")}>
            {(id) => <IntervalInput id={id} value={d.threshold} onChange={(v) => set({ threshold: v })} />}
          </Field>
        ) : (
          <Field label={t("alerts.rules.diskPercent")} hint={t("alerts.rules.diskHint")}>
            {(id) => <Input id={id} type="number" min={1} max={90} value={d.threshold} onChange={(e) => set({ threshold: Number(e.target.value) })} />}
          </Field>
        )}
      </div>
      <Field label={t("alerts.rules.deviceKinds")} hint={d.deviceKinds.length === 0 ? t("alerts.kinds.all") : undefined}>
        {() => (
          <div className="flex flex-wrap gap-4">
            {deviceKinds.map((k) => (
              <label key={k} className="flex items-center gap-2 text-sm">
                <input
                  type="checkbox"
                  className="size-4 accent-primary"
                  checked={d.deviceKinds.includes(k)}
                  onChange={(e) => set({ deviceKinds: toggle(d.deviceKinds, k, e.target.checked) })}
                />
                {t(`alerts.kinds.${k}`)}
              </label>
            ))}
          </div>
        )}
      </Field>
      <Field label={t("reports.schedules.groups")} hint={d.groupIds.length === 0 ? t("reports.allGroups") : undefined}>
        {() => (
          <div className="grid max-h-40 gap-1 overflow-y-auto rounded-md border border-line p-2 sm:grid-cols-2">
            {(groups ?? []).map((g) => (
              <label key={g.id} className="flex cursor-pointer items-center gap-2 rounded-md px-2 py-1 text-sm hover:bg-surface-muted">
                <input
                  type="checkbox"
                  className="size-4 accent-primary"
                  checked={d.groupIds.includes(g.id)}
                  onChange={(e) => set({ groupIds: toggle(d.groupIds, g.id, e.target.checked) })}
                />
                <span className="truncate">{g.name}</span>
              </label>
            ))}
          </div>
        )}
      </Field>
      <Field label={t("alerts.rules.recipients")} hint={t("alerts.rules.recipientsHint")}>
        {(id) => <Input id={id} value={rcpt} onChange={(e) => setRcpt(e.target.value)} placeholder="it@example.com" />}
      </Field>
      <Field label={t("reports.schedules.language")}>
        {(id) => (
          <Select id={id} value={d.language} onChange={(e) => set({ language: e.target.value as Draft["language"] })}>
            <option value="en">English</option>
            <option value="nl">Nederlands</option>
          </Select>
        )}
      </Field>
      <Switch checked={d.notifyResolved} onChange={(v) => set({ notifyResolved: v })} label={t("alerts.rules.notifyResolved")} />
      <Switch checked={d.enabled} onChange={(v) => set({ enabled: v })} label={t("alerts.rules.enabled")} />
    </Modal>
  );
}
