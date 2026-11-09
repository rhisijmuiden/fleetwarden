import { useCallback, useEffect, useMemo, useState, type FormEvent } from "react";
import { Link, useSearchParams } from "react-router";
import { useTranslation } from "react-i18next";
import { CalendarClock, Pencil, Play, Plus, Search, Trash2, Wifi, WifiOff, Zap } from "lucide-react";
import {
  del,
  get,
  post,
  put,
  type PowerAction,
  type PowerActionKind,
  type PowerDevice,
  type PowerSchedule,
  type PowerSchedules,
  type WakeSettings,
} from "../../api";
import { useAuth } from "../../auth";
import { useGroups } from "../../components/GroupScope";
import { Alert, Badge, Button, Card, Field, Input, Modal, PageHeader, Select, Spinner, Switch, formatDate, useErrorText } from "../../components/ui";
import { PlatformBadge, StatusDot, timeAgo } from "../devices/util";
import { formatInZone, minutesToTime, timeToMinutes, weekOrder, weekdayNames } from "../patches/util";
import { PowerDialog, actionIcon } from "./PowerDialog";
import { PowerStateBadge } from "./util";

type Tab = "devices" | "schedules" | "history" | "settings";
const tabs: Tab[] = ["devices", "schedules", "history", "settings"];

export function PowerPage() {
  const { t } = useTranslation();
  const [params, setParams] = useSearchParams();
  const tab = (tabs.includes(params.get("tab") as Tab) ? params.get("tab") : "devices") as Tab;
  return (
    <div className="space-y-6">
      <PageHeader title={t("power.title")} description={t("power.subtitle")} />
      <div className="flex gap-1 overflow-x-auto border-b border-line" role="tablist">
        {tabs.map((k) => (
          <button
            key={k}
            role="tab"
            aria-selected={tab === k}
            onClick={() => setParams(k === "devices" ? {} : { tab: k })}
            className={`-mb-px border-b-2 px-4 py-2 text-sm font-medium whitespace-nowrap ${tab === k ? "border-primary text-fg" : "border-transparent text-muted hover:text-fg"}`}
          >
            {t(`power.tabs.${k}`)}
          </button>
        ))}
      </div>
      {tab === "devices" && <DevicesTab />}
      {tab === "schedules" && <SchedulesTab />}
      {tab === "history" && <HistoryTab />}
      {tab === "settings" && <SettingsTab />}
    </div>
  );
}

// ---- devices ----

function DevicesTab() {
  const { t, i18n } = useTranslation();
  const { can } = useAuth();
  const errorText = useErrorText();
  const groups = useGroups();
  const [devices, setDevices] = useState<PowerDevice[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [search, setSearch] = useState("");
  const [filter, setFilter] = useState<"all" | "online" | "offline">("all");
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [action, setAction] = useState<PowerActionKind | null>(null);
  const control = can("power.control");

  const load = useCallback(() => {
    get<PowerDevice[]>("/power/devices")
      .then(setDevices)
      .catch((e) => setError(errorText(e)));
  }, []);
  useEffect(() => {
    load();
    const id = setInterval(load, 15000);
    return () => clearInterval(id);
  }, [load]);

  const groupName = useMemo(() => new Map((groups ?? []).map((g) => [g.id, g.name])), [groups]);
  const shown = useMemo(() => {
    const q = search.trim().toLowerCase();
    return (devices ?? []).filter(
      (d) => (filter === "all" || (filter === "online") === d.online) && (!q || d.hostname.toLowerCase().includes(q) || d.macs.some((m) => m.includes(q))),
    );
  }, [devices, search, filter]);

  if (error && !devices) return <Alert tone="danger">{error}</Alert>;
  if (!devices) return <Spinner />;
  const online = devices.filter((d) => d.online).length;
  const wakeable = devices.filter((d) => !d.online && d.macs.length > 0 && d.relays.length > 0).length;
  const noRoute = devices.filter((d) => !d.online && (d.macs.length === 0 || d.relays.length === 0)).length;
  const chosen = shown.filter((d) => selected.has(d.id));
  const toggle = (id: string) =>
    setSelected((s) => {
      const n = new Set(s);
      if (n.has(id)) n.delete(id);
      else n.add(id);
      return n;
    });

  return (
    <div className="space-y-6">
      {notice && <Alert tone="success">{notice}</Alert>}
      {error && <Alert tone="danger">{error}</Alert>}
      <div className="grid grid-cols-2 gap-4 md:grid-cols-4">
        <Stat label={t("power.stats.devices")} value={devices.length} />
        <Stat label={t("power.stats.online")} value={online} icon={<Wifi className="size-4" />} />
        <Stat label={t("power.stats.wakeable")} value={wakeable} icon={<Zap className="size-4" />} />
        <Stat label={t("power.stats.noRoute")} value={noRoute} tone={noRoute ? "text-warning" : ""} icon={<WifiOff className="size-4" />} />
      </div>
      <Card
        title={t("power.devicesTitle")}
        description={t("power.devicesHint")}
        actions={
          control && (
            <>
              {(["wake", "restart", "shutdown"] as PowerActionKind[]).map((a) => {
                const Icon = actionIcon[a];
                return (
                  <Button key={a} size="sm" variant="secondary" icon={<Icon className="size-4" />} disabled={chosen.length === 0} onClick={() => setAction(a)}>
                    {t(`power.actions.${a}`)}
                  </Button>
                );
              })}
            </>
          )
        }
      >
        <div className="mb-4 flex flex-wrap gap-3">
          <div className="relative min-w-56 flex-1">
            <Search className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted" />
            <Input className="pl-9" placeholder={t("power.search")} value={search} onChange={(e) => setSearch(e.target.value)} aria-label={t("power.search")} />
          </div>
          <div className="w-44">
            <Select value={filter} onChange={(e) => setFilter(e.target.value as typeof filter)} aria-label={t("power.filter")}>
              <option value="all">{t("power.filters.all")}</option>
              <option value="online">{t("power.filters.online")}</option>
              <option value="offline">{t("power.filters.offline")}</option>
            </Select>
          </div>
        </div>
        {shown.length === 0 ? (
          <p className="rounded-md border border-dashed border-line px-4 py-6 text-center text-sm text-muted">{t("power.noDevices")}</p>
        ) : (
          <div className="-mx-5 -mb-5 overflow-x-auto">
            <table className="w-full min-w-[820px] text-sm">
              <thead className="border-y border-line text-left text-xs text-muted uppercase">
                <tr>
                  {control && (
                    <th className="w-10 px-5 py-3">
                      <input
                        type="checkbox"
                        aria-label={t("power.selectAll")}
                        checked={chosen.length > 0 && chosen.length === shown.length}
                        onChange={(e) => setSelected(e.target.checked ? new Set(shown.map((d) => d.id)) : new Set())}
                      />
                    </th>
                  )}
                  <th className={`${control ? "px-3" : "px-5"} py-3 font-medium`}>{t("power.device")}</th>
                  <th className="px-3 py-3 font-medium">{t("power.group")}</th>
                  <th className="px-3 py-3 font-medium">{t("power.status")}</th>
                  <th className="px-3 py-3 font-medium">{t("power.network")}</th>
                  <th className="px-5 py-3 font-medium">{t("power.wakeRoute")}</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-line">
                {shown.map((d) => (
                  <tr key={d.id} className={selected.has(d.id) ? "bg-primary/5" : ""}>
                    {control && (
                      <td className="px-5 py-2">
                        <input type="checkbox" aria-label={d.hostname} checked={selected.has(d.id)} onChange={() => toggle(d.id)} />
                      </td>
                    )}
                    <td className={`${control ? "px-3" : "px-5"} py-2`}>
                      <Link to={`/devices/${d.id}`} className="font-medium hover:text-primary">
                        {d.hostname}
                      </Link>
                      <div className="mt-0.5">
                        <PlatformBadge platform={d.platform} />
                      </div>
                    </td>
                    <td className="px-3 py-2 text-muted">{groupName.get(d.groupId) ?? "–"}</td>
                    <td className="px-3 py-2">
                      <StatusDot online={d.online} />
                      {!d.online && <div className="text-xs text-muted">{timeAgo(d.lastSeenAt, i18n.language)}</div>}
                      {d.online && d.loggedInUser && <div className="text-xs text-muted">{d.loggedInUser}</div>}
                    </td>
                    <td className="px-3 py-2 font-mono text-xs text-muted">
                      {d.macs.length === 0 ? <span className="font-sans">{t("power.noMac")}</span> : d.macs.map((m) => <div key={m}>{m}</div>)}
                      {d.networks.map((n) => (
                        <div key={n}>{n}</div>
                      ))}
                    </td>
                    <td className="px-5 py-2">
                      {d.online ? (
                        <span className="text-muted">–</span>
                      ) : d.macs.length === 0 ? (
                        <Badge tone="warning">{t("power.route.noMac")}</Badge>
                      ) : d.relays.length === 0 ? (
                        <Badge tone="warning">{t("power.route.noRelay")}</Badge>
                      ) : (
                        <span title={d.relays.join(", ")}>
                          <Badge tone="success">{t("power.route.via", { names: d.relays.join(", ") })}</Badge>
                        </span>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Card>
      <PowerDialog
        action={action}
        deviceIds={chosen.map((d) => d.id)}
        label={chosen.length === 1 ? chosen[0].hostname : t("power.devicesCount", { count: chosen.length })}
        onClose={() => setAction(null)}
        onDone={(m) => {
          setAction(null);
          setNotice(m);
          setSelected(new Set());
          setTimeout(load, 3000);
        }}
      />
    </div>
  );
}

function Stat({ label, value, tone, icon }: { label: string; value: number | string; tone?: string; icon?: React.ReactNode }) {
  return (
    <div className="rounded-lg border border-line bg-surface p-4">
      <div className="flex items-center gap-2 text-xs font-medium text-muted uppercase">
        {icon}
        {label}
      </div>
      <div className={`mt-2 text-2xl font-semibold tabular-nums ${tone ?? ""}`}>{value}</div>
    </div>
  );
}

// ---- schedules ----

export function scheduleDays(days: number, lang: string, t: (k: string) => string) {
  const names = weekdayNames(lang);
  const list = weekOrder.filter((d) => days & (1 << d));
  if (list.length === 7) return t("patches.everyDay");
  if (list.length === 0) return t("patches.noDays");
  if (days === 0b0111110) return t("power.weekdays");
  return list.map((d) => names[d]).join(", ");
}

function SchedulesTab() {
  const { t, i18n } = useTranslation();
  const { can } = useAuth();
  const errorText = useErrorText();
  const [data, setData] = useState<PowerSchedules | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [editing, setEditing] = useState<PowerSchedule | "new" | null>(null);
  const [deleting, setDeleting] = useState<PowerSchedule | null>(null);
  const manage = can("power.manage");

  const load = useCallback(() => {
    get<PowerSchedules>("/power/schedules")
      .then(setData)
      .catch((e) => setError(errorText(e)));
  }, []);
  useEffect(load, [load]);

  if (error && !data) return <Alert tone="danger">{error}</Alert>;
  if (!data) return <Spinner />;
  const runNow = (s: PowerSchedule) =>
    post<{ sent: number }>(`/power/schedules/${s.id}/run`)
      .then((r) => {
        setNotice(t("power.ranNow", { name: s.name, count: r.sent }));
        load();
      })
      .catch((e) => setError(errorText(e)));

  return (
    <div className="space-y-6">
      {notice && <Alert tone="success">{notice}</Alert>}
      {error && <Alert tone="danger">{error}</Alert>}
      <Card
        title={t("power.schedules")}
        description={t("power.schedulesHint")}
        actions={
          manage && (
            <Button icon={<Plus className="size-4" />} onClick={() => setEditing("new")}>
              {t("power.addSchedule")}
            </Button>
          )
        }
      >
        {data.schedules.length === 0 ? (
          <p className="rounded-md border border-dashed border-line px-4 py-6 text-center text-sm text-muted">{t("power.noSchedules")}</p>
        ) : (
          <div className="divide-y divide-line">
            {data.schedules.map((s) => {
              const Icon = actionIcon[s.action];
              return (
                <div key={s.id} className="flex flex-wrap items-center gap-4 py-3 first:pt-0 last:pb-0">
                  <span className="grid size-10 shrink-0 place-items-center rounded-lg bg-primary/10 text-primary">
                    <Icon className="size-5" />
                  </span>
                  <div className="min-w-56 flex-1">
                    <div className="flex flex-wrap items-center gap-2">
                      <span className="font-medium">{s.name}</span>
                      <Badge tone="primary">{t(`power.actions.${s.action}`)}</Badge>
                      {!s.enabled && <Badge>{t("patches.disabled")}</Badge>}
                    </div>
                    <div className="mt-0.5 text-sm text-muted">
                      {scheduleDays(s.days, i18n.language, t)} · {minutesToTime(s.atMinute)}
                      {s.timeZone && ` (${s.timeZone})`}
                      {" · "}
                      {s.groups.length ? s.groups.map((g) => g.name).join(", ") : t("power.noGroups")}
                    </div>
                    {s.nextRun && (
                      <div className="mt-0.5 flex items-center gap-1 text-xs text-muted">
                        <CalendarClock className="size-3" />
                        {t("power.nextRun", { when: formatInZone(s.nextRun, i18n.language, s.timeZone || data.serverTimeZone) })}
                      </div>
                    )}
                  </div>
                  {manage && (
                    <div className="flex gap-2">
                      <Button size="sm" variant="secondary" icon={<Play className="size-4" />} onClick={() => runNow(s)} disabled={s.groups.length === 0}>
                        {t("power.runNow")}
                      </Button>
                      <Button size="sm" variant="ghost" icon={<Pencil className="size-4" />} onClick={() => setEditing(s)} aria-label={t("common.edit")} />
                      <Button size="sm" variant="ghost" icon={<Trash2 className="size-4" />} onClick={() => setDeleting(s)} aria-label={t("common.delete")} />
                    </div>
                  )}
                </div>
              );
            })}
          </div>
        )}
      </Card>
      <Alert tone="info" title={t("power.patchWindowTitle")}>
        {t("power.patchWindowText")}{" "}
        <Link to="/patches?tab=policies" className="font-medium text-primary hover:underline">
          {t("power.patchWindowLink")}
        </Link>
      </Alert>
      <Alert tone="info" title={t("power.plansTitle")}>
        {t("power.plansText")}{" "}
        <Link to="/configurations" className="font-medium text-primary hover:underline">
          {t("power.plansLink")}
        </Link>
      </Alert>
      <ScheduleForm
        editing={editing}
        defaults={data.defaults}
        serverTimeZone={data.serverTimeZone}
        onClose={() => setEditing(null)}
        onSaved={() => {
          setEditing(null);
          load();
        }}
      />
      <Modal
        open={!!deleting}
        onClose={() => setDeleting(null)}
        title={t("power.deleteSchedule")}
        footer={
          <>
            <Button variant="secondary" onClick={() => setDeleting(null)}>
              {t("common.cancel")}
            </Button>
            <Button
              variant="danger"
              onClick={() =>
                deleting &&
                del(`/power/schedules/${deleting.id}`)
                  .then(() => {
                    setDeleting(null);
                    load();
                  })
                  .catch((e) => setError(errorText(e)))
              }
            >
              {t("common.delete")}
            </Button>
          </>
        }
      >
        <p className="text-sm">{t("power.deleteScheduleConfirm", { name: deleting?.name })}</p>
      </Modal>
    </div>
  );
}

type Draft = Omit<PowerSchedule, "id" | "createdAt" | "updatedAt" | "groups" | "nextRun" | "lastRunAt">;

function toDraft(p: PowerSchedule): Draft {
  const { id: _i, createdAt: _c, updatedAt: _u, groups: _g, nextRun: _n, lastRunAt: _l, ...rest } = p;
  return { ...rest, groupIds: p.groups?.map((g) => g.id) ?? p.groupIds ?? [] };
}

function ScheduleForm({
  editing,
  defaults,
  serverTimeZone,
  onClose,
  onSaved,
}: {
  editing: PowerSchedule | "new" | null;
  defaults: PowerSchedule;
  serverTimeZone: string;
  onClose: () => void;
  onSaved: () => void;
}) {
  const { t, i18n } = useTranslation();
  const errorText = useErrorText();
  const groups = useGroups();
  const schedule = editing && editing !== "new" ? editing : null;
  const [d, setD] = useState<Draft>(() => toDraft(defaults));
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const zones = useMemo(() => {
    try {
      return (Intl as unknown as { supportedValuesOf(k: string): string[] }).supportedValuesOf("timeZone");
    } catch {
      return [] as string[];
    }
  }, []);
  const days = weekdayNames(i18n.language);

  useEffect(() => {
    if (!editing) return;
    setD(toDraft(schedule ?? { ...defaults, name: "" }));
    setError(null);
  }, [editing]);

  const set = <K extends keyof Draft>(k: K, v: Draft[K]) => setD((x) => ({ ...x, [k]: v }));
  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      if (schedule) await put(`/power/schedules/${schedule.id}`, d);
      else await post("/power/schedules", d);
      onSaved();
    } catch (err) {
      setError(errorText(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Modal
      open={!!editing}
      onClose={onClose}
      wide
      title={schedule ? t("power.editSchedule") : t("power.addSchedule")}
      footer={
        <>
          <Button variant="secondary" onClick={onClose}>
            {t("common.cancel")}
          </Button>
          <Button type="submit" form="power-schedule-form" loading={busy}>
            {t("common.save")}
          </Button>
        </>
      }
    >
      <form id="power-schedule-form" onSubmit={submit} className="space-y-6">
        {error && <Alert tone="danger">{error}</Alert>}
        <div className="grid gap-4 sm:grid-cols-2">
          <Field label={t("power.scheduleName")}>
            {(id) => <Input id={id} required maxLength={100} value={d.name} onChange={(e) => set("name", e.target.value)} />}
          </Field>
          <Field label={t("power.action")}>
            {(id) => (
              <Select id={id} value={d.action} onChange={(e) => set("action", e.target.value as PowerActionKind)}>
                {(["shutdown", "restart", "wake"] as PowerActionKind[]).map((a) => (
                  <option key={a} value={a}>
                    {t(`power.actions.${a}`)}
                  </option>
                ))}
              </Select>
            )}
          </Field>
        </div>
        <Field label={t("groups.description")}>
          {(id) => <Input id={id} maxLength={500} value={d.description} onChange={(e) => set("description", e.target.value)} />}
        </Field>
        <Switch checked={d.enabled} onChange={(v) => set("enabled", v)} label={t("power.enabled")} description={t("power.enabledHint")} />

        <section className="space-y-3">
          <h3 className="text-sm font-semibold">{t("power.when")}</h3>
          <div className="flex flex-wrap gap-2" role="group" aria-label={t("patches.windowDays")}>
            {weekOrder.map((day) => {
              const on = (d.days & (1 << day)) !== 0;
              return (
                <button
                  key={day}
                  type="button"
                  aria-pressed={on}
                  onClick={() => set("days", d.days ^ (1 << day))}
                  className={`h-8 min-w-12 rounded-md border px-3 text-xs font-medium capitalize transition-colors ${on ? "border-primary bg-primary text-primary-contrast" : "border-line bg-surface text-muted hover:text-fg"}`}
                >
                  {days[day]}
                </button>
              );
            })}
          </div>
          <div className="grid gap-4 sm:grid-cols-2">
            <Field label={t("power.at")}>
              {(id) => <Input id={id} type="time" required value={minutesToTime(d.atMinute)} onChange={(e) => set("atMinute", timeToMinutes(e.target.value))} />}
            </Field>
            <Field label={t("patches.timeZone")}>
              {(id) => (
                <Select id={id} value={d.timeZone} onChange={(e) => set("timeZone", e.target.value)}>
                  <option value="">{t("patches.serverTimeZone", { zone: serverTimeZone })}</option>
                  {zones.map((z) => (
                    <option key={z} value={z}>
                      {z}
                    </option>
                  ))}
                </Select>
              )}
            </Field>
          </div>
        </section>

        <section className="space-y-3">
          <h3 className="text-sm font-semibold">{t("power.groups")}</h3>
          <p className="text-xs text-muted">{t("power.groupsHint")}</p>
          <div className="grid max-h-48 gap-1 overflow-y-auto rounded-md border border-line p-2 sm:grid-cols-2">
            {(groups ?? []).map((g) => (
              <label key={g.id} className="flex cursor-pointer items-center gap-2 rounded-md px-2 py-1.5 text-sm hover:bg-surface-muted">
                <input
                  type="checkbox"
                  checked={d.groupIds.includes(g.id)}
                  onChange={(e) => set("groupIds", e.target.checked ? [...d.groupIds, g.id] : d.groupIds.filter((x) => x !== g.id))}
                />
                {g.name}
              </label>
            ))}
          </div>
        </section>

        {d.action === "wake" ? (
          <p className="text-sm text-muted">{t("power.wakeScheduleHint")}</p>
        ) : (
          <section className="space-y-3">
            <h3 className="text-sm font-semibold">{t("power.users")}</h3>
            <Switch
              checked={d.skipIfUserLoggedOn}
              onChange={(v) => set("skipIfUserLoggedOn", v)}
              label={t("power.skipIfUser")}
              description={t("power.skipIfUserHint")}
            />
            {d.action === "restart" && (
              <Switch
                checked={d.onlyIfRebootPending}
                onChange={(v) => set("onlyIfRebootPending", v)}
                label={t("power.onlyIfReboot")}
                description={t("power.onlyIfRebootHint")}
              />
            )}
            <Switch checked={d.notifyUsers} onChange={(v) => set("notifyUsers", v)} label={t("power.notifyUsers")} description={t("power.notifyUsersHint")} />
            {d.notifyUsers && (
              <div className="grid gap-4 sm:grid-cols-3">
                <Field label={t("patches.countdown")}>
                  {(id) => (
                    <Input id={id} type="number" min={30} max={3600} value={d.countdownSeconds} onChange={(e) => set("countdownSeconds", Number(e.target.value) || 30)} />
                  )}
                </Field>
                <Field label={t("patches.maxPostpones")}>
                  {(id) => <Input id={id} type="number" min={0} max={20} value={d.maxPostpones} onChange={(e) => set("maxPostpones", Number(e.target.value) || 0)} />}
                </Field>
                <Field label={t("patches.postponeMinutes")}>
                  {(id) => (
                    <Input id={id} type="number" min={5} max={1440} value={d.postponeMinutes} onChange={(e) => set("postponeMinutes", Number(e.target.value) || 5)} />
                  )}
                </Field>
              </div>
            )}
          </section>
        )}
      </form>
    </Modal>
  );
}

// ---- history ----

export function PowerActionsTable({ actions, showDevice }: { actions: PowerAction[]; showDevice?: boolean }) {
  const { t, i18n } = useTranslation();
  return (
    <div className="-mx-5 -my-5 overflow-x-auto">
      <table className="w-full min-w-[720px] text-sm">
        <thead className="border-b border-line text-left text-xs text-muted uppercase">
          <tr>
            <th className="px-5 py-3 font-medium">{t("power.when")}</th>
            {showDevice && <th className="px-3 py-3 font-medium">{t("power.device")}</th>}
            <th className="px-3 py-3 font-medium">{t("power.action")}</th>
            <th className="px-3 py-3 font-medium">{t("power.trigger")}</th>
            <th className="px-3 py-3 font-medium">{t("power.status")}</th>
            <th className="px-5 py-3 font-medium">{t("power.details")}</th>
          </tr>
        </thead>
        <tbody className="divide-y divide-line">
          {actions.map((a) => (
            <tr key={a.id}>
              <td className="px-5 py-2 whitespace-nowrap">{formatDate(a.createdAt, i18n.language)}</td>
              {showDevice && (
                <td className="px-3 py-2">
                  <Link to={`/devices/${a.deviceId}`} className="font-medium hover:text-primary">
                    {a.hostname}
                  </Link>
                </td>
              )}
              <td className="px-3 py-2">{t(`power.actions.${a.action}`)}</td>
              <td className="px-3 py-2 text-muted">
                {t(`power.triggers.${a.trigger}`)}
                {a.scheduleName && ` · ${a.scheduleName}`}
                {a.requestedBy && ` · ${a.requestedBy}`}
              </td>
              <td className="px-3 py-2">
                <PowerStateBadge action={a} />
                {a.postpones > 0 && <span className="ml-2 text-xs text-muted">{t("patches.postponedCount", { count: a.postpones })}</span>}
              </td>
              <td className="px-5 py-2 text-muted">
                {a.relays.length > 0 && <div className="text-xs">{t("power.relays", { names: a.relays.map((r) => (r === "server" ? t("power.theServer") : r)).join(", ") })}</div>}
                {a.message && <div className="text-xs">{a.message}</div>}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function HistoryTab() {
  const { t } = useTranslation();
  const errorText = useErrorText();
  const [actions, setActions] = useState<PowerAction[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const load = useCallback(() => {
    get<PowerAction[]>("/power/actions?limit=200")
      .then(setActions)
      .catch((e) => setError(errorText(e)));
  }, []);
  useEffect(() => {
    load();
    const id = setInterval(load, 15000);
    return () => clearInterval(id);
  }, [load]);
  if (error && !actions) return <Alert tone="danger">{error}</Alert>;
  if (!actions) return <Spinner />;
  return (
    <Card title={t("power.history")} description={t("power.historyHint")}>
      {actions.length === 0 ? (
        <p className="rounded-md border border-dashed border-line px-4 py-6 text-center text-sm text-muted">{t("power.noActions")}</p>
      ) : (
        <PowerActionsTable actions={actions} showDevice />
      )}
    </Card>
  );
}

// ---- settings ----

function SettingsTab() {
  const { t } = useTranslation();
  const { can } = useAuth();
  const errorText = useErrorText();
  const [cfg, setCfg] = useState<WakeSettings | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [saved, setSaved] = useState(false);
  const [busy, setBusy] = useState(false);
  const manage = can("power.manage");
  useEffect(() => {
    get<WakeSettings>("/power/settings")
      .then(setCfg)
      .catch((e) => setError(errorText(e)));
  }, []);
  if (error && !cfg) return <Alert tone="danger">{error}</Alert>;
  if (!cfg) return <Spinner />;
  const set = <K extends keyof WakeSettings>(k: K, v: WakeSettings[K]) => {
    setSaved(false);
    setCfg({ ...cfg, [k]: v });
  };
  const save = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      setCfg(await put<WakeSettings>("/power/settings", cfg));
      setSaved(true);
    } catch (err) {
      setError(errorText(err));
    } finally {
      setBusy(false);
    }
  };
  return (
    <form onSubmit={save} className="space-y-6">
      {error && <Alert tone="danger">{error}</Alert>}
      {saved && <Alert tone="success">{t("common.saved")}</Alert>}
      <Card title={t("power.wolTitle")} description={t("power.wolHint")}>
        <div className="space-y-4">
          <Switch checked={cfg.prepareWake} onChange={(v) => manage && set("prepareWake", v)} label={t("power.prepareWake")} description={t("power.prepareWakeHint")} />
          <Switch checked={cfg.serverWake} onChange={(v) => manage && set("serverWake", v)} label={t("power.serverWake")} description={t("power.serverWakeHint")} />
          <div className="max-w-xs">
            <Field label={t("power.port")} hint={t("power.portHint")}>
              {(id) => (
                <Select id={id} value={cfg.port} disabled={!manage} onChange={(e) => set("port", Number(e.target.value))}>
                  <option value={9}>9 (discard)</option>
                  <option value={7}>7 (echo)</option>
                </Select>
              )}
            </Field>
          </div>
        </div>
      </Card>
      <Card title={t("power.requirementsTitle")}>
        <ul className="list-disc space-y-1 pl-5 text-sm text-muted">
          <li>{t("power.requirements.bios")}</li>
          <li>{t("power.requirements.relay")}</li>
          <li>{t("power.requirements.wired")}</li>
          <li>{t("power.requirements.vlan")}</li>
        </ul>
      </Card>
      {manage && (
        <div className="flex justify-end">
          <Button type="submit" loading={busy}>
            {t("common.save")}
          </Button>
        </div>
      )}
    </form>
  );
}
