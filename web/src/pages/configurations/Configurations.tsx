import { useCallback, useEffect, useMemo, useState } from "react";
import { Link, useNavigate, useParams, useSearchParams } from "react-router";
import { useTranslation } from "react-i18next";
import { AlertTriangle, ArrowLeft, Pencil, Play, Plus, Search, SlidersHorizontal, Trash2 } from "lucide-react";
import {
  del,
  get,
  post,
  put,
  type BrowserSettings,
  type ConfigList,
  type ConfigRun,
  type ConfigSettings,
  type ConfigType,
  type ConfigView,
  type Configuration,
  type DeviceConfigState,
  type Platform,
  type RegistrySettings,
  type ScriptSettings,
  type ScriptShell,
} from "../../api";
import { useAuth } from "../../auth";
import { useGroups } from "../../components/GroupScope";
import { Alert, Badge, Button, Card, Field, Input, Modal, PageHeader, Select, Spinner, Switch, formatDate, useErrorText } from "../../components/ui";
import { PlatformBadge, StatusDot, timeAgo } from "../devices/util";
import { BrowserForm, RegistryForm, ScriptForm, defaultSettings } from "./forms";
import { ConfigProgress, ConfigStateBadge, TypeBadge, effectiveState, typeIcons } from "./util";

function EmptyState({ onNew, canManage }: { onNew: () => void; canManage: boolean }) {
  const { t } = useTranslation();
  return (
    <div className="flex flex-col items-center gap-3 py-10 text-center">
      <span className="grid size-12 place-items-center rounded-full bg-primary/10 text-primary">
        <SlidersHorizontal className="size-6" />
      </span>
      <p className="font-medium">{t("configs.emptyTitle")}</p>
      <p className="max-w-md text-sm text-muted">{t("configs.emptyText")}</p>
      {canManage && (
        <Button icon={<Plus className="size-4" />} onClick={onNew}>
          {t("configs.new")}
        </Button>
      )}
    </div>
  );
}

// NewDialog picks the platform and type of a new configuration.
function NewDialog({ open, onClose, types }: { open: boolean; onClose: () => void; types: ConfigList["types"] | undefined }) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [platform, setPlatform] = useState<Platform>("windows");
  return (
    <Modal open={open} onClose={onClose} title={t("configs.new")} wide>
      <div className="flex gap-2" role="radiogroup" aria-label={t("configs.platform")}>
        {(["windows", "linux"] as Platform[]).map((p) => (
          <button
            key={p}
            type="button"
            role="radio"
            aria-checked={platform === p}
            onClick={() => setPlatform(p)}
            className={`rounded-md border px-3 py-1.5 text-sm font-medium ${platform === p ? "border-primary bg-primary/10 text-primary" : "border-line text-muted hover:text-fg"}`}
          >
            {t(`configs.platforms.${p}`)}
          </button>
        ))}
      </div>
      <div className="grid gap-3 sm:grid-cols-2">
        {(types?.[platform] ?? []).map((type) => {
          const Icon = typeIcons[type];
          return (
            <button
              key={type}
              type="button"
              onClick={() => navigate(`/configurations/new?platform=${platform}&type=${type}`)}
              className="flex items-start gap-3 rounded-lg border border-line p-4 text-left transition-colors hover:border-primary hover:bg-primary/5"
            >
              <span className="grid size-9 shrink-0 place-items-center rounded-md bg-primary/10 text-primary">
                <Icon className="size-5" />
              </span>
              <span>
                <span className="block text-sm font-medium">{t(`configs.types.${type}`)}</span>
                <span className="block text-xs text-muted">{t(`configs.typeHints.${type}`)}</span>
              </span>
            </button>
          );
        })}
      </div>
      <p className="text-xs text-muted">{t("configs.comingSoon")}</p>
    </Modal>
  );
}

export function Configurations() {
  const { t, i18n } = useTranslation();
  const errorText = useErrorText();
  const { can } = useAuth();
  const manage = can("config.manage");
  const groups = useGroups();
  const [list, setList] = useState<ConfigList | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [newOpen, setNewOpen] = useState(false);
  const [filter, setFilter] = useState("");
  const load = useCallback(() => {
    get<ConfigList>("/configurations")
      .then(setList)
      .catch((e) => setError(errorText(e)));
  }, []);
  useEffect(() => {
    load();
    const id = setInterval(load, 15000);
    return () => clearInterval(id);
  }, [load]);
  const groupName = (id: number) => groups?.find((g) => g.id === id)?.name ?? `#${id}`;
  const items = useMemo(() => {
    const f = filter.trim().toLowerCase();
    return (list?.items ?? []).filter((c) => !f || c.name.toLowerCase().includes(f) || c.description.toLowerCase().includes(f));
  }, [list, filter]);

  return (
    <div className="space-y-6">
      <PageHeader
        title={t("configs.title")}
        description={t("configs.subtitle", { minutes: list?.reapplyMinutes ?? 30 })}
        actions={
          manage && (
            <Button icon={<Plus className="size-4" />} onClick={() => setNewOpen(true)}>
              {t("configs.new")}
            </Button>
          )
        }
      />
      {error && <Alert tone="danger">{error}</Alert>}
      <Card>
        {!list ? (
          <Spinner />
        ) : list.items.length === 0 ? (
          <EmptyState onNew={() => setNewOpen(true)} canManage={manage} />
        ) : (
          <div className="space-y-4">
            <div className="relative max-w-sm">
              <Search className="pointer-events-none absolute top-2.5 left-3 size-4 text-muted" />
              <Input className="pl-9" placeholder={t("configs.search")} value={filter} onChange={(e) => setFilter(e.target.value)} aria-label={t("configs.search")} />
            </div>
            <div className="-mx-5 -mb-5 overflow-x-auto">
              <table className="w-full min-w-[820px] text-sm">
                <thead className="border-y border-line text-left text-xs text-muted uppercase">
                  <tr>
                    <th className="px-5 py-3 font-medium">{t("configs.cols.name")}</th>
                    <th className="px-3 py-3 font-medium">{t("configs.cols.groups")}</th>
                    <th className="px-3 py-3 font-medium">{t("configs.cols.state")}</th>
                    <th className="px-3 py-3 font-medium">{t("configs.cols.order")}</th>
                    <th className="px-5 py-3 font-medium">{t("configs.cols.changed")}</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-line">
                  {items.map((c) => (
                    <tr key={c.id} className="hover:bg-surface-muted/50">
                      <td className="px-5 py-3">
                        <Link to={`/configurations/${c.id}`} className="font-medium hover:text-primary">
                          {c.name}
                        </Link>
                        <div className="mt-1 flex flex-wrap items-center gap-2">
                          <TypeBadge type={c.type} />
                          <PlatformBadge platform={c.platform} />
                          {c.repaired > 0 && (
                            <span className="inline-flex items-center gap-1 text-xs text-warning" title={t("configs.repairedHint")}>
                              <AlertTriangle className="size-3.5" /> {t("configs.repairedDevices", { count: c.repaired })}
                            </span>
                          )}
                        </div>
                      </td>
                      <td className="max-w-[240px] px-3 py-3 text-muted">
                        <div className="truncate">{c.groupIds.length ? c.groupIds.map(groupName).join(", ") : t("configs.noGroups")}</div>
                      </td>
                      <td className="px-3 py-3">
                        <ConfigProgress c={c} />
                      </td>
                      <td className="px-3 py-3 text-muted tabular-nums">{c.priority}</td>
                      <td className="px-5 py-3 whitespace-nowrap text-muted">
                        <div>{formatDate(c.updatedAt, i18n.language)}</div>
                        <div className="text-xs">{c.updatedBy || c.createdBy}</div>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </div>
        )}
      </Card>
      <NewDialog open={newOpen} onClose={() => setNewOpen(false)} types={list?.types} />
    </div>
  );
}

// ---- editor ----

function useShells(): Record<Platform, ScriptShell[]> | null {
  const [shells, setShells] = useState<Record<Platform, ScriptShell[]> | null>(null);
  useEffect(() => {
    get<ConfigList>("/configurations")
      .then((l) => setShells(l.shells))
      .catch(() => setShells({ windows: ["powershell", "cmd"], linux: ["bash", "sh"] }));
  }, []);
  return shells;
}

export function ConfigEditor() {
  const { id } = useParams();
  const [params] = useSearchParams();
  const { t } = useTranslation();
  const navigate = useNavigate();
  const errorText = useErrorText();
  const groups = useGroups();
  const shells = useShells();
  const [cfg, setCfg] = useState<Configuration | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    if (id) {
      get<ConfigView>(`/configurations/${id}`)
        .then((v) => setCfg(v.configuration))
        .catch((e) => setError(errorText(e)));
      return;
    }
    const platform = (params.get("platform") === "linux" ? "linux" : "windows") as Platform;
    const type = (["browser", "registry", "script"].includes(params.get("type") ?? "") ? params.get("type") : "browser") as ConfigType;
    setCfg({
      id: 0,
      name: "",
      description: "",
      type,
      platform,
      settings: defaultSettings(type, platform),
      enabled: true,
      groupIds: [],
      priority: 100,
      revision: 0,
      createdBy: "",
      updatedBy: "",
      createdAt: "",
      updatedAt: "",
      devices: 0,
      applied: 0,
      failed: 0,
      repaired: 0,
    });
  }, [id]);

  if (error && !cfg) return <Alert tone="danger">{error}</Alert>;
  if (!cfg || !shells) return <Spinner />;
  const set = (patch: Partial<Configuration>) => setCfg({ ...cfg, ...patch });
  const setSettings = (s: ConfigSettings) => set({ settings: s });

  const save = async () => {
    setBusy(true);
    setError(null);
    const body = {
      name: cfg.name,
      description: cfg.description,
      type: cfg.type,
      platform: cfg.platform,
      settings: cfg.settings,
      enabled: cfg.enabled,
      groupIds: cfg.groupIds,
      priority: cfg.priority,
    };
    try {
      const saved = id ? await put<Configuration>(`/configurations/${id}`, body) : await post<Configuration>("/configurations", body);
      navigate(`/configurations/${saved.id}`);
    } catch (e) {
      setError(errorText(e));
      window.scrollTo({ top: 0, behavior: "smooth" });
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="space-y-6">
      <Link to={id ? `/configurations/${id}` : "/configurations"} className="inline-flex items-center gap-1.5 text-sm text-muted hover:text-fg">
        <ArrowLeft className="size-4" /> {id ? cfg.name : t("configs.title")}
      </Link>
      <div className="flex flex-wrap items-center gap-3">
        <h1 className="text-2xl font-semibold tracking-tight">{id ? t("configs.edit") : t("configs.newOf", { type: t(`configs.types.${cfg.type}`) })}</h1>
        <TypeBadge type={cfg.type} />
        <PlatformBadge platform={cfg.platform} />
      </div>
      {error && <Alert tone="danger">{error}</Alert>}
      <div className="grid gap-6 xl:grid-cols-[1fr_360px]">
        <Card title={t(`configs.types.${cfg.type}`)} description={t(`configs.typeHints.${cfg.type}`)}>
          {cfg.settings === null ? (
            <Alert tone="warning">{t("configs.noSettingsAccess")}</Alert>
          ) : cfg.type === "browser" ? (
            <BrowserForm value={cfg.settings as BrowserSettings} onChange={setSettings} />
          ) : cfg.type === "registry" ? (
            <RegistryForm value={cfg.settings as RegistrySettings} onChange={setSettings} />
          ) : (
            <ScriptForm value={cfg.settings as ScriptSettings} onChange={setSettings} platform={cfg.platform} shells={shells[cfg.platform]} />
          )}
        </Card>
        <div className="space-y-6">
          <Card title={t("configs.general")}>
            <div className="space-y-4">
              <Field label={t("configs.name")}>{(fid) => <Input id={fid} value={cfg.name} onChange={(e) => set({ name: e.target.value })} maxLength={200} />}</Field>
              <Field label={t("configs.description")}>
                {(fid) => <Input id={fid} value={cfg.description} onChange={(e) => set({ description: e.target.value })} maxLength={1000} />}
              </Field>
              <Field label={t("configs.priority")} hint={t("configs.priorityHint")}>
                {(fid) => <Input id={fid} type="number" min={0} max={1000} value={cfg.priority} onChange={(e) => set({ priority: Number(e.target.value) })} />}
              </Field>
              <Switch checked={cfg.enabled} onChange={(enabled) => set({ enabled })} label={t("configs.enabled")} description={t("configs.enabledHint")} />
            </div>
          </Card>
          <Card title={t("configs.assign")} description={t("configs.assignHint")}>
            <div className="grid max-h-64 gap-1 overflow-y-auto">
              {(groups ?? []).map((g) => (
                <label key={g.id} className="flex cursor-pointer items-center gap-2 rounded-md px-2 py-1.5 text-sm hover:bg-surface-muted">
                  <input
                    type="checkbox"
                    className="size-4 accent-primary"
                    checked={cfg.groupIds.includes(g.id)}
                    onChange={(e) => set({ groupIds: e.target.checked ? [...cfg.groupIds, g.id] : cfg.groupIds.filter((x) => x !== g.id) })}
                  />
                  <span className="flex-1 truncate">{g.name}</span>
                  <span className="text-xs text-muted tabular-nums">{g.deviceCount}</span>
                </label>
              ))}
            </div>
            {cfg.groupIds.length === 0 && <p className="mt-2 text-xs text-warning">{t("configs.noGroupsHint")}</p>}
          </Card>
          <div className="flex justify-end gap-2">
            <Button variant="secondary" onClick={() => navigate(id ? `/configurations/${id}` : "/configurations")}>
              {t("common.cancel")}
            </Button>
            <Button loading={busy} onClick={save} disabled={cfg.settings === null}>
              {t("common.save")}
            </Button>
          </div>
        </div>
      </div>
    </div>
  );
}

// ---- detail ----

const stateFilters = ["", "applied", "pending", "waiting", "failed", "unsupported", "repaired"] as const;

export function ConfigDetail() {
  const { id } = useParams();
  const { t, i18n } = useTranslation();
  const navigate = useNavigate();
  const errorText = useErrorText();
  const { can } = useAuth();
  const manage = can("config.manage");
  const groups = useGroups();
  const [view, setView] = useState<ConfigView | null>(null);
  const [runs, setRuns] = useState<ConfigRun[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [filter, setFilter] = useState("");
  const [state, setState] = useState<(typeof stateFilters)[number]>("");
  const [shown, setShown] = useState<DeviceConfigState | null>(null);

  const load = useCallback(() => {
    get<ConfigView>(`/configurations/${id}`)
      .then((v) => {
        setView(v);
        if (v.configuration.type === "script") get<ConfigRun[]>(`/configurations/${id}/runs?limit=100`).then(setRuns).catch(() => {});
      })
      .catch((e) => setError(errorText(e)));
  }, [id]);
  useEffect(() => {
    load();
    const tm = setInterval(load, 10000);
    return () => clearInterval(tm);
  }, [load]);

  const devices = useMemo(() => {
    const f = filter.trim().toLowerCase();
    return (view?.devices ?? []).filter((d) => {
      if (f && !d.hostname.toLowerCase().includes(f)) return false;
      if (state === "repaired") return d.repairedTotal > 0;
      return !state || effectiveState(d) === state;
    });
  }, [view, filter, state]);

  if (error && !view) return <Alert tone="danger">{error}</Alert>;
  if (!view) return <Spinner />;
  const c = view.configuration;
  const groupName = (gid: number) => groups?.find((g) => g.id === gid)?.name ?? `#${gid}`;
  const repairedDomain = view.devices.some((d) => d.repairedTotal > 0 && d.domainJoined);

  const apply = async () => {
    setBusy(true);
    setError(null);
    try {
      const r = await post<{ sent: number }>(`/configurations/${id}/apply`);
      setNotice(t("configs.applySent", { count: r.sent }));
      setTimeout(load, 3000);
    } catch (e) {
      setError(errorText(e));
    } finally {
      setBusy(false);
    }
  };
  const remove = async () => {
    setBusy(true);
    try {
      await del(`/configurations/${id}`);
      navigate("/configurations");
    } catch (e) {
      setError(errorText(e));
      setBusy(false);
    }
  };

  return (
    <div className="space-y-6">
      <Link to="/configurations" className="inline-flex items-center gap-1.5 text-sm text-muted hover:text-fg">
        <ArrowLeft className="size-4" /> {t("configs.title")}
      </Link>
      <div className="flex flex-wrap items-start justify-between gap-4">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight">{c.name}</h1>
          {c.description && <p className="mt-1 text-sm text-muted">{c.description}</p>}
          <div className="mt-2 flex flex-wrap items-center gap-2 text-sm text-muted">
            <TypeBadge type={c.type} />
            <PlatformBadge platform={c.platform} />
            {!c.enabled && <Badge>{t("configs.disabled")}</Badge>}
            <span>{t("configs.revision", { revision: c.revision })}</span>
            <span>·</span>
            <span>{c.groupIds.length ? c.groupIds.map(groupName).join(", ") : t("configs.noGroups")}</span>
          </div>
        </div>
        {manage && (
          <div className="flex flex-wrap gap-2">
            <Button variant="secondary" icon={<Play className="size-4" />} loading={busy} onClick={apply} disabled={!c.enabled || c.devices === 0}>
              {t("configs.applyNow")}
            </Button>
            <Link to={`/configurations/${id}/edit`}>
              <Button variant="secondary" icon={<Pencil className="size-4" />}>
                {t("common.edit")}
              </Button>
            </Link>
            <Button variant="ghost" icon={<Trash2 className="size-4" />} onClick={() => setConfirmDelete(true)}>
              {t("common.delete")}
            </Button>
          </div>
        )}
      </div>
      {error && <Alert tone="danger">{error}</Alert>}
      {notice && <Alert tone="success">{notice}</Alert>}
      {c.repaired > 0 && (
        <Alert tone="warning" title={t("configs.repairedTitle", { count: c.repaired })}>
          {repairedDomain ? t("configs.repairedDomain") : t("configs.repairedHint")}
        </Alert>
      )}

      <div className="grid gap-4 sm:grid-cols-4">
        {[
          { label: t("configs.stats.devices"), value: c.devices },
          { label: t("configs.stats.applied"), value: c.applied, tone: "text-success" },
          { label: t("configs.stats.failed"), value: c.failed, tone: c.failed ? "text-danger" : "" },
          { label: t("configs.stats.repaired"), value: c.repaired, tone: c.repaired ? "text-warning" : "" },
        ].map((s) => (
          <div key={s.label} className="rounded-lg border border-line bg-surface px-5 py-4">
            <p className="text-xs text-muted">{s.label}</p>
            <p className={`mt-1 text-2xl font-semibold tabular-nums ${s.tone ?? ""}`}>{s.value}</p>
          </div>
        ))}
      </div>

      <Card title={t("configs.devicesTitle")}>
        <div className="mb-4 flex flex-wrap gap-2">
          <div className="relative w-full max-w-xs">
            <Search className="pointer-events-none absolute top-2.5 left-3 size-4 text-muted" />
            <Input className="pl-9" placeholder={t("configs.searchDevices")} value={filter} onChange={(e) => setFilter(e.target.value)} aria-label={t("configs.searchDevices")} />
          </div>
          <div className="w-60">
            <Select value={state} onChange={(e) => setState(e.target.value as (typeof stateFilters)[number])} aria-label={t("configs.cols.state")}>
              {stateFilters.map((s) => (
                <option key={s} value={s}>
                  {s === "" ? t("configs.allStates") : s === "repaired" ? t("configs.filterRepaired") : t(`configs.state.${s}`)}
                </option>
              ))}
            </Select>
          </div>
        </div>
        {devices.length === 0 ? (
          <p className="py-6 text-center text-sm text-muted">{view.devices.length === 0 ? t("configs.noDevicesHint") : t("configs.noMatch")}</p>
        ) : (
          <div className="-mx-5 -mb-5 overflow-x-auto">
            <DeviceStateTable rows={devices} onShow={setShown} lang={i18n.language} by="device" />
          </div>
        )}
      </Card>

      {c.type === "script" && <RunsCard runs={runs} />}

      <StateDialog row={shown} onClose={() => setShown(null)} />
      <Modal
        open={confirmDelete}
        onClose={() => setConfirmDelete(false)}
        title={t("configs.deleteTitle")}
        footer={
          <>
            <Button variant="secondary" onClick={() => setConfirmDelete(false)}>
              {t("common.cancel")}
            </Button>
            <Button variant="danger" loading={busy} onClick={remove}>
              {t("common.delete")}
            </Button>
          </>
        }
      >
        <p className="text-sm">{t("configs.deleteText", { name: c.name })}</p>
      </Modal>
    </div>
  );
}

// DeviceStateTable lists states per device (on a configuration) or per
// configuration (on a device).
export function DeviceStateTable({ rows, onShow, lang, by }: { rows: DeviceConfigState[]; onShow: (r: DeviceConfigState) => void; lang: string; by: "device" | "configuration" }) {
  const { t } = useTranslation();
  return (
    <table className="w-full min-w-[760px] text-sm">
      <thead className="border-y border-line text-left text-xs text-muted uppercase">
        <tr>
          <th className="px-5 py-3 font-medium">{by === "device" ? t("configs.cols.device") : t("configs.cols.name")}</th>
          <th className="px-3 py-3 font-medium">{t("configs.cols.state")}</th>
          <th className="px-3 py-3 font-medium">{t("configs.cols.message")}</th>
          <th className="px-5 py-3 font-medium">{t("configs.cols.checked")}</th>
        </tr>
      </thead>
      <tbody className="divide-y divide-line">
        {rows.map((d) => {
          const st = effectiveState(d);
          return (
            <tr key={d.deviceId + "-" + d.configurationId} className="cursor-pointer hover:bg-surface-muted/50" onClick={() => onShow(d)}>
              <td className="px-5 py-3">
                {by === "device" ? (
                  <span className="flex items-center gap-2">
                    <StatusDot online={d.online} />
                    <Link to={`/devices/${d.deviceId}`} onClick={(e) => e.stopPropagation()} className="font-medium hover:text-primary">
                      {d.hostname}
                    </Link>
                  </span>
                ) : (
                  <span className="flex flex-wrap items-center gap-2">
                    <Link to={`/configurations/${d.configurationId}`} onClick={(e) => e.stopPropagation()} className="font-medium hover:text-primary">
                      {d.configurationName}
                    </Link>
                    <TypeBadge type={d.type} />
                  </span>
                )}
              </td>
              <td className="px-3 py-3">
                <div className="flex flex-wrap items-center gap-1.5">
                  <ConfigStateBadge state={st} />
                  {d.repairedTotal > 0 && (
                    <span className="inline-flex items-center gap-1 text-xs text-warning" title={d.domainJoined ? t("configs.repairedDomain") : t("configs.repairedHint")}>
                      <AlertTriangle className="size-3.5" /> {t("configs.repairedCount", { count: d.repairedTotal })}
                    </span>
                  )}
                </div>
              </td>
              <td className="max-w-[320px] px-3 py-3 text-muted">
                <div className="truncate">
                  {d.runAt ? t("configs.lastRun", { code: d.runExitCode ?? "–", when: timeAgo(d.runAt, lang) }) + (d.message ? " · " : "") : ""}
                  {d.message || (!d.runAt && "–")}
                </div>
              </td>
              <td className="px-5 py-3 whitespace-nowrap text-muted">{d.reportedAt ? timeAgo(d.reportedAt, lang) : "–"}</td>
            </tr>
          );
        })}
      </tbody>
    </table>
  );
}

export function StateDialog({ row, onClose }: { row: DeviceConfigState | null; onClose: () => void }) {
  const { t, i18n } = useTranslation();
  return (
    <Modal open={!!row} onClose={onClose} title={row ? `${row.configurationName} · ${row.hostname}` : ""} wide>
      {row && (
        <div className="space-y-3 text-sm">
          <div className="flex flex-wrap items-center gap-2">
            <ConfigStateBadge state={effectiveState(row)} />
            <span className="text-muted">{t("configs.revisionOf", { revision: row.revision, current: row.currentRevision })}</span>
          </div>
          <dl className="grid grid-cols-[160px_1fr] gap-x-4 gap-y-1.5">
            <dt className="text-muted">{t("configs.appliedAt")}</dt>
            <dd>{formatDate(row.appliedAt, i18n.language)}</dd>
            <dt className="text-muted">{t("configs.reportedAt")}</dt>
            <dd>{formatDate(row.reportedAt, i18n.language)}</dd>
            {row.repairedTotal > 0 && (
              <>
                <dt className="text-muted">{t("configs.stats.repaired")}</dt>
                <dd>
                  {t("configs.repairedCount", { count: row.repairedTotal })} · {formatDate(row.repairedAt, i18n.language)}
                </dd>
              </>
            )}
            {row.runAt && (
              <>
                <dt className="text-muted">{t("configs.runAt")}</dt>
                <dd>
                  {formatDate(row.runAt, i18n.language)} · {t("configs.exitCode", { code: row.runExitCode ?? "–" })}
                </dd>
              </>
            )}
          </dl>
          {row.repairedTotal > 0 && <Alert tone="warning">{row.domainJoined ? t("configs.repairedDomain") : t("configs.repairedHint")}</Alert>}
          {row.message && <pre className="max-h-40 overflow-auto rounded-md bg-surface-muted p-3 text-xs whitespace-pre-wrap">{row.message}</pre>}
          {row.runError && <Alert tone="danger">{row.runError}</Alert>}
          {row.runOutput && (
            <div className="space-y-1">
              <p className="text-xs font-medium text-muted uppercase">{t("configs.output")}</p>
              <pre className="max-h-72 overflow-auto rounded-md bg-surface-muted p-3 font-mono text-xs whitespace-pre-wrap">{row.runOutput}</pre>
            </div>
          )}
        </div>
      )}
    </Modal>
  );
}

export function RunsCard({ runs, configName }: { runs: ConfigRun[]; configName?: (id: number) => string }) {
  const { t, i18n } = useTranslation();
  const [open, setOpen] = useState<ConfigRun | null>(null);
  return (
    <Card title={t("configs.runsTitle")} description={t("configs.runsHint")}>
      {runs.length === 0 ? (
        <p className="py-4 text-center text-sm text-muted">{t("configs.noRuns")}</p>
      ) : (
        <div className="-mx-5 -my-5 overflow-x-auto">
          <table className="w-full min-w-[640px] text-sm">
            <thead className="border-b border-line text-left text-xs text-muted uppercase">
              <tr>
                <th className="px-5 py-3 font-medium">{t("configs.cols.when")}</th>
                <th className="px-3 py-3 font-medium">{configName ? t("configs.cols.name") : t("configs.cols.device")}</th>
                <th className="px-3 py-3 font-medium">{t("configs.cols.user")}</th>
                <th className="px-3 py-3 font-medium">{t("configs.cols.result")}</th>
                <th className="px-5 py-3 font-medium">{t("configs.cols.duration")}</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-line">
              {runs.map((r) => (
                <tr key={r.id} className="cursor-pointer hover:bg-surface-muted/50" onClick={() => setOpen(r)}>
                  <td className="px-5 py-2.5 whitespace-nowrap">{formatDate(r.at, i18n.language)}</td>
                  <td className="px-3 py-2.5">{configName ? configName(r.configurationId) : r.hostname}</td>
                  <td className="px-3 py-2.5 text-muted">{r.user || t("configs.script.system")}</td>
                  <td className="px-3 py-2.5">
                    {r.error ? <Badge tone="danger">{t("configs.state.failed")}</Badge> : <Badge tone={r.exitCode === 0 ? "success" : "danger"}>{t("configs.exitCode", { code: r.exitCode })}</Badge>}
                  </td>
                  <td className="px-5 py-2.5 text-muted tabular-nums">{(r.durationMs / 1000).toFixed(1)} s</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      <Modal open={!!open} onClose={() => setOpen(null)} title={open ? `${open.hostname} · ${formatDate(open.at, i18n.language)}` : ""} wide>
        {open && (
          <div className="space-y-3">
            {open.error && <Alert tone="danger">{open.error}</Alert>}
            <p className="text-sm text-muted">{t("configs.exitCode", { code: open.exitCode })}</p>
            <pre className="max-h-96 overflow-auto rounded-md bg-surface-muted p-3 font-mono text-xs whitespace-pre-wrap">{open.output || t("configs.noOutput")}</pre>
          </div>
        )}
      </Modal>
    </Card>
  );
}
