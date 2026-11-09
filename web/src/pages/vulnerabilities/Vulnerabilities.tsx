import { useCallback, useEffect, useState } from "react";
import { Link, useSearchParams } from "react-router";
import { useTranslation } from "react-i18next";
import {
  Bug,
  Flame,
  Monitor,
  RefreshCw,
  Rocket,
  Search,
  ShieldCheck,
  Trash2,
  Wrench,
} from "lucide-react";
import {
  del,
  get,
  post,
  put,
  type DeviceGroup,
  type VulnDeviceSummary,
  type VulnException,
  type VulnSettings,
  type VulnStatus,
  type VulnSummary,
  type Vulnerability,
} from "../../api";
import { useAuth } from "../../auth";
import {
  Alert,
  Badge,
  Button,
  Card,
  Field,
  Input,
  PageHeader,
  Select,
  Spinner,
  Switch,
  formatDate,
  useErrorText,
} from "../../components/ui";
import { PlatformBadge, timeAgo } from "../devices/util";
import {
  FixDialog,
  KEVBadge,
  SeverityBadge,
  severities,
  severityDot,
} from "./util";

type Tab = "cves" | "devices" | "accepted" | "sources";
const tabs: Tab[] = ["cves", "devices", "accepted", "sources"];

export function Vulnerabilities() {
  const { t } = useTranslation();
  const [params, setParams] = useSearchParams();
  const tab = (
    tabs.includes(params.get("tab") as Tab) ? params.get("tab") : "cves"
  ) as Tab;
  const [n, setN] = useState(0);
  const refresh = () => setN((x) => x + 1);
  return (
    <div className="space-y-6">
      <PageHeader title={t("vulns.title")} description={t("vulns.subtitle")} />
      <Summary refresh={n} />
      <div
        className="flex gap-1 overflow-x-auto border-b border-line"
        role="tablist"
      >
        {tabs.map((k) => (
          <button
            key={k}
            role="tab"
            aria-selected={tab === k}
            onClick={() => setParams(k === "cves" ? {} : { tab: k })}
            className={`-mb-px border-b-2 px-4 py-2 text-sm font-medium whitespace-nowrap ${tab === k ? "border-primary text-fg" : "border-transparent text-muted hover:text-fg"}`}
          >
            {t(`vulns.tabs.${k}`)}
          </button>
        ))}
      </div>
      {tab === "cves" && <CVEList key="open" onChanged={refresh} />}
      {tab === "devices" && <DeviceList />}
      {tab === "accepted" && <Accepted onChanged={refresh} />}
      {tab === "sources" && <Sources onChanged={refresh} />}
    </div>
  );
}

function Stat({
  label,
  value,
  sub,
  tone,
  icon,
}: {
  label: string;
  value: number | string;
  sub?: React.ReactNode;
  tone?: string;
  icon: React.ReactNode;
}) {
  return (
    <div className="rounded-lg border border-line bg-surface p-4">
      <div className="flex items-center gap-2 text-xs font-medium text-muted uppercase">
        {icon}
        {label}
      </div>
      <div className={`mt-2 text-2xl font-semibold tabular-nums ${tone ?? ""}`}>
        {value}
      </div>
      {sub && <div className="mt-1 text-xs text-muted">{sub}</div>}
    </div>
  );
}

function Summary({ refresh }: { refresh: number }) {
  const { t, i18n } = useTranslation();
  const [sum, setSum] = useState<VulnSummary | null>(null);
  const load = useCallback(() => {
    get<VulnSummary>("/vulnerabilities/summary")
      .then(setSum)
      .catch(() => {});
  }, []);
  useEffect(() => {
    load();
    const id = setInterval(load, 15000);
    return () => clearInterval(id);
  }, [load, refresh]);
  if (!sum) return null;
  const synced = sum.status.sources.filter((s) => s.syncedAt);
  const last = synced
    .map((s) => s.syncedAt!)
    .sort()
    .at(-1);
  const failed = sum.status.sources.filter((s) => s.error);
  const crit = sum.bySeverity.critical ?? 0;
  const high = sum.bySeverity.high ?? 0;
  return (
    <div className="space-y-3">
      <div className="grid grid-cols-2 gap-4 md:grid-cols-4">
        <Stat
          label={t("vulns.stats.open")}
          value={sum.cves}
          sub={t("vulns.stats.critHigh", { critical: crit, high })}
          tone={crit ? "text-danger" : high ? "text-warning" : ""}
          icon={<Bug className="size-4" />}
        />
        <Stat
          label={t("vulns.stats.kev")}
          value={sum.kev}
          sub={t("vulns.stats.kevSub")}
          tone={sum.kev ? "text-danger" : ""}
          icon={<Flame className="size-4" />}
        />
        <Stat
          label={t("vulns.stats.devices")}
          value={sum.devices}
          sub={t("vulns.stats.devicesSub", {
            critical: sum.criticalDevices,
            checked: sum.checked,
          })}
          icon={<Monitor className="size-4" />}
        />
        <Stat
          label={t("vulns.stats.fixable")}
          value={sum.fixable}
          sub={t("vulns.stats.fixableSub")}
          tone={sum.fixable ? "text-success" : ""}
          icon={<Wrench className="size-4" />}
        />
      </div>
      <p className="text-xs text-muted">
        {!sum.status.enabled
          ? t("vulns.disabled")
          : sum.status.running
            ? t("vulns.syncing", {
                step: t(`vulns.sources.${sum.status.step.split(" ")[0]}`, {
                  defaultValue: sum.status.step,
                }),
              })
            : last
              ? t("vulns.lastSync", { when: timeAgo(last, i18n.language) })
              : t("vulns.neverSynced")}
        {sum.accepted > 0 &&
          ` · ${t("vulns.acceptedCount", { count: sum.accepted })}`}
      </p>
      {sum.status.enabled && !last && !sum.status.running && (
        <Alert tone="info">{t("vulns.firstSyncHint")}</Alert>
      )}
      {failed.length > 0 && (
        <Alert tone="warning">
          {t("vulns.sourceFailed", {
            sources: failed
              .map((s) => t(`vulns.sources.${s.source}`))
              .join(", "),
          })}
        </Alert>
      )}
    </div>
  );
}

// ---- CVEs ----

export function CVEList({
  onChanged,
  deviceId,
}: {
  onChanged?: () => void;
  deviceId?: string;
}) {
  const { t, i18n } = useTranslation();
  const { can } = useAuth();
  const errorText = useErrorText();
  const [list, setList] = useState<Vulnerability[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [search, setSearch] = useState("");
  const [severity, setSeverity] = useState("");
  const [kind, setKind] = useState("");
  const [group, setGroup] = useState("");
  const [groups, setGroups] = useState<DeviceGroup[]>([]);
  const [kev, setKev] = useState(false);
  const [fixable, setFixable] = useState(false);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [fixing, setFixing] = useState<string[] | null>(null);
  const [n, setN] = useState(0);
  const manage = can("devices.manage");

  useEffect(() => {
    get<DeviceGroup[]>("/groups")
      .then(setGroups)
      .catch(() => {});
  }, []);
  useEffect(() => {
    const q = new URLSearchParams();
    if (search.trim()) q.set("search", search.trim());
    if (severity) q.set("severity", severity);
    if (kind) q.set("kind", kind);
    if (group) q.set("group", group);
    if (kev) q.set("kev", "1");
    if (fixable) q.set("fixable", "1");
    const ctl = new AbortController();
    const timer = setTimeout(() => {
      get<Vulnerability[]>(`/vulnerabilities?${q}`, ctl.signal)
        .then((l) => {
          setList(l);
          setError(null);
        })
        .catch((e) => !ctl.signal.aborted && setError(errorText(e)));
    }, 250);
    return () => {
      clearTimeout(timer);
      ctl.abort();
    };
  }, [search, severity, kind, group, kev, fixable, n]);

  const toggle = (id: string) =>
    setSelected((s) => {
      const x = new Set(s);
      if (x.has(id)) x.delete(id);
      else x.add(id);
      return x;
    });
  const fixableSelected = (list ?? []).filter(
    (v) => selected.has(v.cve) && v.fixable > 0,
  );

  return (
    <Card
      title={t("vulns.listTitle")}
      description={t("vulns.listHint")}
      actions={
        manage &&
        selected.size > 0 && (
          <Button
            size="sm"
            icon={<Rocket className="size-4" />}
            disabled={fixableSelected.length === 0}
            onClick={() => setFixing(fixableSelected.map((v) => v.cve))}
          >
            {t("vulns.fixSelected", { count: fixableSelected.length })}
          </Button>
        )
      }
    >
      <div className="space-y-4">
        {error && <Alert tone="danger">{error}</Alert>}
        <div className="flex flex-wrap gap-3">
          <div className="relative min-w-56 flex-1">
            <Search className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted" />
            <Input
              className="pl-9"
              placeholder={t("vulns.search")}
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              aria-label={t("vulns.search")}
            />
          </div>
          <div className="w-48">
            <Select
              value={severity}
              onChange={(e) => setSeverity(e.target.value)}
              aria-label={t("vulns.severityCol")}
            >
              <option value="">{t("vulns.allSeverities")}</option>
              {severities.map((s) => (
                <option key={s} value={s}>
                  {t(`vulns.severity.${s}`)}
                </option>
              ))}
            </Select>
          </div>
          <div className="w-44">
            <Select
              value={kind}
              onChange={(e) => setKind(e.target.value)}
              aria-label={t("vulns.kindCol")}
            >
              <option value="">{t("vulns.allKinds")}</option>
              {(["os", "update", "program", "package"] as const).map((k) => (
                <option key={k} value={k}>
                  {t(`vulns.kinds.${k}`)}
                </option>
              ))}
            </Select>
          </div>
          {!deviceId && groups.length > 1 && (
            <div className="w-44">
              <Select
                value={group}
                onChange={(e) => setGroup(e.target.value)}
                aria-label={t("vulns.group")}
              >
                <option value="">{t("vulns.allGroups")}</option>
                {groups.map((g) => (
                  <option key={g.id} value={g.id}>
                    {g.name}
                  </option>
                ))}
              </Select>
            </div>
          )}
        </div>
        <div className="flex flex-wrap gap-x-6 gap-y-2 text-sm">
          <label className="inline-flex items-center gap-2">
            <input
              type="checkbox"
              className="size-4 accent-primary"
              checked={kev}
              onChange={(e) => setKev(e.target.checked)}
            />
            {t("vulns.onlyKev")}
          </label>
          <label className="inline-flex items-center gap-2">
            <input
              type="checkbox"
              className="size-4 accent-primary"
              checked={fixable}
              onChange={(e) => setFixable(e.target.checked)}
            />
            {t("vulns.onlyFixable")}
          </label>
        </div>
        {!list ? (
          <Spinner />
        ) : list.length === 0 ? (
          <p className="rounded-md border border-dashed border-line px-4 py-6 text-center text-sm text-muted">
            {search || severity || kind || kev || fixable || group
              ? t("vulns.emptyFiltered")
              : t("vulns.empty")}
          </p>
        ) : (
          <div className="-mx-5 -mb-5 overflow-x-auto">
            <table className="w-full min-w-[860px] text-sm">
              <thead className="border-y border-line text-left text-xs text-muted uppercase">
                <tr>
                  {manage && (
                    <th className="w-10 px-5 py-3">
                      <input
                        type="checkbox"
                        className="size-4 accent-primary"
                        checked={
                          selected.size > 0 && selected.size === list.length
                        }
                        onChange={(e) =>
                          setSelected(
                            e.target.checked
                              ? new Set(list.map((v) => v.cve))
                              : new Set(),
                          )
                        }
                        aria-label={t("vulns.selectAll")}
                      />
                    </th>
                  )}
                  <th
                    className={`${manage ? "px-3" : "px-5"} py-3 font-medium`}
                  >
                    {t("vulns.cveCol")}
                  </th>
                  <th className="px-3 py-3 font-medium">
                    {t("vulns.severityCol")}
                  </th>
                  <th className="px-3 py-3 font-medium">
                    {t("vulns.productsCol")}
                  </th>
                  <th className="px-3 py-3 text-right font-medium">
                    {t("vulns.devicesCol")}
                  </th>
                  <th className="px-3 py-3 font-medium">{t("vulns.fixCol")}</th>
                  <th className="px-5 py-3 font-medium">
                    {t("vulns.firstSeen")}
                  </th>
                </tr>
              </thead>
              <tbody className="divide-y divide-line">
                {list.map((v) => (
                  <tr
                    key={v.cve}
                    className="align-top hover:bg-surface-muted/50"
                  >
                    {manage && (
                      <td className="px-5 py-2.5">
                        <input
                          type="checkbox"
                          className="size-4 accent-primary"
                          checked={selected.has(v.cve)}
                          onChange={() => toggle(v.cve)}
                          aria-label={v.cve}
                        />
                      </td>
                    )}
                    <td
                      className={`${manage ? "px-3" : "px-5"} max-w-md py-2.5`}
                    >
                      <div className="flex flex-wrap items-center gap-2">
                        <Link
                          to={`/vulnerabilities/${v.cve}`}
                          className="font-mono font-medium hover:text-primary"
                        >
                          {v.cve}
                        </Link>
                        {v.kev && <KEVBadge ransomware={v.ransomware} />}
                        {!v.kev && v.exploited && (
                          <Badge tone="danger">{t("vulns.exploited")}</Badge>
                        )}
                      </div>
                      <p className="mt-0.5 line-clamp-2 text-xs text-muted">
                        {v.title || v.summary || t("vulns.noDescription")}
                      </p>
                    </td>
                    <td className="px-3 py-2.5">
                      <SeverityBadge severity={v.severity} score={v.score} />
                    </td>
                    <td className="max-w-56 px-3 py-2.5 text-muted">
                      <span className="line-clamp-2">
                        {v.products.join(", ")}
                      </span>
                    </td>
                    <td className="px-3 py-2.5 text-right tabular-nums">
                      {v.devices}
                    </td>
                    <td className="px-3 py-2.5 whitespace-nowrap">
                      {v.fixable > 0 ? (
                        <span className="text-success">
                          {t("vulns.fixableOf", {
                            count: v.fixable,
                            total: v.devices,
                          })}
                        </span>
                      ) : (
                        <span className="text-muted">
                          {t("vulns.noFixYet")}
                        </span>
                      )}
                    </td>
                    <td className="px-5 py-2.5 whitespace-nowrap text-muted">
                      {timeAgo(v.firstSeen, i18n.language)}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
        {list && list.length >= 500 && (
          <p className="text-xs text-muted">{t("vulns.limited")}</p>
        )}
      </div>
      <FixDialog
        open={fixing !== null}
        cves={fixing ?? []}
        deviceIds={deviceId ? [deviceId] : undefined}
        onClose={() => setFixing(null)}
        onDone={() => {
          setSelected(new Set());
          setN((x) => x + 1);
          onChanged?.();
        }}
      />
    </Card>
  );
}

// ---- devices ----

export function SeverityCounts({ counts }: { counts: Record<string, number> }) {
  const { t } = useTranslation();
  return (
    <span className="inline-flex flex-wrap items-center gap-3">
      {(["critical", "high", "medium", "low"] as const).map((s) => (
        <span
          key={s}
          className={`inline-flex items-center gap-1.5 tabular-nums ${counts[s] ? "" : "text-muted/60"}`}
          title={t(`vulns.severity.${s}`)}
        >
          <span className={`size-2 rounded-full ${severityDot[s]}`} />
          {counts[s] ?? 0}
        </span>
      ))}
    </span>
  );
}

function DeviceList() {
  const { t, i18n } = useTranslation();
  const errorText = useErrorText();
  const [list, setList] = useState<VulnDeviceSummary[] | null>(null);
  const [search, setSearch] = useState("");
  const [error, setError] = useState<string | null>(null);
  useEffect(() => {
    const ctl = new AbortController();
    const timer = setTimeout(() => {
      get<VulnDeviceSummary[]>(
        `/vulnerabilities/devices?search=${encodeURIComponent(search.trim())}`,
        ctl.signal,
      )
        .then(setList)
        .catch((e) => !ctl.signal.aborted && setError(errorText(e)));
    }, 250);
    return () => {
      clearTimeout(timer);
      ctl.abort();
    };
  }, [search]);
  return (
    <Card title={t("vulns.devicesTitle")} description={t("vulns.devicesHint")}>
      <div className="space-y-4">
        {error && <Alert tone="danger">{error}</Alert>}
        <div className="relative max-w-sm">
          <Search className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted" />
          <Input
            className="pl-9"
            placeholder={t("vulns.searchDevices")}
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            aria-label={t("vulns.searchDevices")}
          />
        </div>
        {!list ? (
          <Spinner />
        ) : list.length === 0 ? (
          <p className="rounded-md border border-dashed border-line px-4 py-6 text-center text-sm text-muted">
            {t("vulns.noDevices")}
          </p>
        ) : (
          <div className="-mx-5 -mb-5 overflow-x-auto">
            <table className="w-full min-w-[820px] text-sm">
              <thead className="border-y border-line text-left text-xs text-muted uppercase">
                <tr>
                  <th className="px-5 py-3 font-medium">
                    {t("vulns.deviceCol")}
                  </th>
                  <th className="px-3 py-3 font-medium">{t("vulns.group")}</th>
                  <th className="px-3 py-3 font-medium">
                    {t("vulns.bySeverity")}
                  </th>
                  <th className="px-3 py-3 text-right font-medium">
                    {t("vulns.kev")}
                  </th>
                  <th className="px-3 py-3 text-right font-medium">
                    {t("vulns.fixCol")}
                  </th>
                  <th className="px-5 py-3 font-medium">
                    {t("vulns.checked")}
                  </th>
                </tr>
              </thead>
              <tbody className="divide-y divide-line">
                {list.map((d) => (
                  <tr key={d.deviceId} className="hover:bg-surface-muted/50">
                    <td className="px-5 py-2.5">
                      <Link
                        to={`/devices/${d.deviceId}?tab=vulnerabilities`}
                        className="font-medium hover:text-primary"
                      >
                        {d.hostname}
                      </Link>
                      <div className="mt-0.5 flex items-center gap-2 text-xs text-muted">
                        <PlatformBadge platform={d.platform} />
                        <span className="truncate">{d.osName}</span>
                      </div>
                    </td>
                    <td className="px-3 py-2.5 text-muted">{d.groupName}</td>
                    <td className="px-3 py-2.5">
                      {d.checkedAt ? (
                        <SeverityCounts counts={d.counts} />
                      ) : (
                        <span className="text-xs text-muted">
                          {t("vulns.notChecked")}
                        </span>
                      )}
                    </td>
                    <td
                      className={`px-3 py-2.5 text-right tabular-nums ${d.kev ? "font-semibold text-danger" : "text-muted"}`}
                    >
                      {d.kev}
                    </td>
                    <td className="px-3 py-2.5 text-right tabular-nums">
                      {d.fixable}
                    </td>
                    <td className="px-5 py-2.5 whitespace-nowrap text-muted">
                      {timeAgo(d.checkedAt, i18n.language)}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </Card>
  );
}

// ---- accepted risks ----

function Accepted({ onChanged }: { onChanged: () => void }) {
  const { t, i18n } = useTranslation();
  const { can } = useAuth();
  const errorText = useErrorText();
  const [list, setList] = useState<VulnException[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [n, setN] = useState(0);
  useEffect(() => {
    get<VulnException[]>("/vulnerabilities/exceptions")
      .then(setList)
      .catch((e) => setError(errorText(e)));
  }, [n]);
  const remove = (id: number) =>
    del(`/vulnerabilities/exceptions/${id}`)
      .then(() => {
        setN((x) => x + 1);
        onChanged();
      })
      .catch((e) => setError(errorText(e)));
  return (
    <Card
      title={t("vulns.acceptedTitle")}
      description={t("vulns.acceptedHint")}
    >
      <div className="space-y-4">
        {error && <Alert tone="danger">{error}</Alert>}
        {!list ? (
          <Spinner />
        ) : list.length === 0 ? (
          <p className="rounded-md border border-dashed border-line px-4 py-6 text-center text-sm text-muted">
            {t("vulns.noAccepted")}
          </p>
        ) : (
          <div className="-mx-5 -mb-5 overflow-x-auto">
            <table className="w-full min-w-[760px] text-sm">
              <thead className="border-y border-line text-left text-xs text-muted uppercase">
                <tr>
                  <th className="px-5 py-3 font-medium">{t("vulns.cveCol")}</th>
                  <th className="px-3 py-3 font-medium">
                    {t("vulns.appliesTo")}
                  </th>
                  <th className="px-3 py-3 font-medium">{t("vulns.reason")}</th>
                  <th className="px-3 py-3 font-medium">
                    {t("vulns.expires")}
                  </th>
                  <th className="px-5 py-3" />
                </tr>
              </thead>
              <tbody className="divide-y divide-line">
                {list.map((x) => {
                  const expired =
                    x.expiresAt && new Date(x.expiresAt).getTime() < Date.now();
                  return (
                    <tr
                      key={x.id}
                      className="align-top hover:bg-surface-muted/50"
                    >
                      <td className="px-5 py-2.5">
                        <Link
                          to={`/vulnerabilities/${x.cve}`}
                          className="font-mono font-medium hover:text-primary"
                        >
                          {x.cve}
                        </Link>
                        <div className="mt-1">
                          <SeverityBadge severity={x.severity} />
                        </div>
                      </td>
                      <td className="px-3 py-2.5">
                        {x.deviceId ? x.hostname : t("vulns.allDevices")}
                      </td>
                      <td className="max-w-xs px-3 py-2.5">
                        <p>{x.reason}</p>
                        <p className="mt-0.5 text-xs text-muted">
                          {t("vulns.acceptedBy", {
                            user: x.createdBy,
                            when: formatDate(x.createdAt, i18n.language),
                          })}
                        </p>
                      </td>
                      <td className="px-3 py-2.5 whitespace-nowrap">
                        {x.expiresAt ? (
                          <span
                            className={expired ? "text-muted line-through" : ""}
                          >
                            {formatDate(x.expiresAt, i18n.language)}
                          </span>
                        ) : (
                          <span className="text-muted">{t("vulns.never")}</span>
                        )}
                      </td>
                      <td className="px-5 py-2.5 text-right">
                        {can("vulns.manage") && (
                          <Button
                            variant="ghost"
                            size="sm"
                            icon={<Trash2 className="size-4" />}
                            onClick={() => remove(x.id)}
                          >
                            {t("vulns.removeAccept")}
                          </Button>
                        )}
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </Card>
  );
}

// ---- sources and settings ----

function Sources({ onChanged }: { onChanged: () => void }) {
  const { t, i18n } = useTranslation();
  const { can } = useAuth();
  const errorText = useErrorText();
  const [st, setSt] = useState<VulnStatus | null>(null);
  const [form, setForm] = useState<VulnSettings | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [saved, setSaved] = useState(false);
  const [busy, setBusy] = useState("");
  const manage = can("vulns.manage");
  const load = useCallback(() => {
    get<VulnStatus>("/vulnerabilities/settings")
      .then((s) => {
        setSt(s);
        setForm((f) => f ?? s.settings ?? null);
      })
      .catch((e) => setError(errorText(e)));
  }, []);
  useEffect(() => {
    load();
    const id = setInterval(load, 5000);
    return () => clearInterval(id);
  }, [load]);
  if (!st || !form)
    return error ? <Alert tone="danger">{error}</Alert> : <Spinner />;
  const save = () => {
    setBusy("save");
    setError(null);
    put<VulnStatus>("/vulnerabilities/settings", form)
      .then((s) => {
        setSt(s);
        setSaved(true);
        setTimeout(() => setSaved(false), 2500);
        onChanged();
      })
      .catch((e) => setError(errorText(e)))
      .finally(() => setBusy(""));
  };
  const sync = () => {
    setBusy("sync");
    setError(null);
    post("/vulnerabilities/sync")
      .then(load)
      .catch((e) => setError(errorText(e)))
      .finally(() => setBusy(""));
  };
  const set = (k: keyof VulnSettings, v: boolean | number) =>
    setForm({ ...form, [k]: v });
  return (
    <div className="space-y-6">
      {error && <Alert tone="danger">{error}</Alert>}
      <Card
        title={t("vulns.sourcesTitle")}
        description={t("vulns.sourcesHint")}
        actions={
          manage && (
            <Button
              size="sm"
              variant="secondary"
              icon={
                <RefreshCw
                  className={`size-4 ${st.running ? "animate-spin" : ""}`}
                />
              }
              loading={busy === "sync"}
              disabled={st.running || !st.enabled}
              onClick={sync}
            >
              {st.running ? t("vulns.syncRunning") : t("vulns.syncNow")}
            </Button>
          )
        }
      >
        <div className="grid gap-3 md:grid-cols-2">
          {st.sources.map((s) => (
            <div key={s.source} className="rounded-md border border-line p-4">
              <div className="flex items-center justify-between gap-2">
                <p className="font-medium">{t(`vulns.sources.${s.source}`)}</p>
                {st.running && st.step.startsWith(s.source) ? (
                  <Badge tone="primary">{t("vulns.reading")}</Badge>
                ) : s.error ? (
                  <Badge tone="danger">{t("vulns.sourceError")}</Badge>
                ) : s.syncedAt ? (
                  <Badge tone="success">{t("vulns.sourceOk")}</Badge>
                ) : (
                  <Badge>{t("vulns.sourceNever")}</Badge>
                )}
              </div>
              <p className="mt-1 text-xs text-muted">
                {t(`vulns.sourceInfo.${s.source}`)}
              </p>
              <p className="mt-2 text-xs text-muted">
                {s.syncedAt
                  ? t("vulns.sourceSynced", {
                      when: timeAgo(s.syncedAt, i18n.language),
                      count: s.items,
                    })
                  : t("vulns.sourceNotYet")}
              </p>
              {s.error && (
                <p className="mt-1 text-xs break-words text-danger">
                  {s.error}
                </p>
              )}
            </div>
          ))}
        </div>
      </Card>
      <Card
        title={t("vulns.settingsTitle")}
        description={t("vulns.settingsHint")}
      >
        <div className="space-y-4">
          <Switch
            checked={form.enabled}
            onChange={(v) => set("enabled", v)}
            label={t("vulns.enabled")}
            description={t("vulns.enabledHint")}
          />
          <div
            className={`grid gap-4 md:grid-cols-2 ${form.enabled ? "" : "pointer-events-none opacity-50"}`}
          >
            {(["nvd", "kev", "msrc", "debian"] as const).map((k) => (
              <Switch
                key={k}
                checked={form[k]}
                onChange={(v) => set(k, v)}
                label={t(`vulns.sources.${k}`)}
                description={t(`vulns.sourceToggle.${k}`)}
              />
            ))}
          </div>
          <Field label={t("vulns.msrcMonths")} hint={t("vulns.msrcMonthsHint")}>
            {(id) => (
              <Input
                id={id}
                type="number"
                min={3}
                max={36}
                className="w-28"
                value={form.msrcMonths}
                onChange={(e) => set("msrcMonths", Number(e.target.value))}
                disabled={!form.enabled || !form.msrc}
              />
            )}
          </Field>
          {manage && (
            <div className="flex items-center gap-3">
              <Button loading={busy === "save"} onClick={save}>
                {t("common.save")}
              </Button>
              {saved && (
                <span className="inline-flex items-center gap-1 text-sm text-success">
                  <ShieldCheck className="size-4" />
                  {t("common.saved")}
                </span>
              )}
            </div>
          )}
        </div>
      </Card>
      <Card
        title={t("vulns.programsTitle")}
        description={t("vulns.programsHint")}
      >
        <div className="flex flex-wrap gap-2">
          {(st.products ?? []).map((p) => (
            <span
              key={p.cpe}
              className="rounded-md border border-line px-2 py-1 text-xs"
              title={p.cpe}
            >
              {p.name}
            </span>
          ))}
        </div>
      </Card>
    </div>
  );
}
