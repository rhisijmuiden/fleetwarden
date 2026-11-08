import { useCallback, useEffect, useMemo, useState } from "react";
import { Link, useSearchParams } from "react-router";
import { useTranslation } from "react-i18next";
import { CheckCircle2, Download, EyeOff, FileSpreadsheet, FileText, HardDrive, Package, RefreshCw, ShieldAlert, WifiOff } from "lucide-react";
import { get, post, type ComplianceReport, type PatchesReport, type ReportKind, type SoftwarePackage, type SoftwareReport } from "../../api";
import { useAuth } from "../../auth";
import { useGroups } from "../../components/GroupScope";
import { Alert, Badge, Button, Card, PageHeader, Select, Spinner, Switch, formatDate, useErrorText } from "../../components/ui";
import { PlatformBadge, StatusDot, timeAgo } from "../devices/util";
import { CategoryBadge } from "../patches/util";
import { DeployDialog, type DeployTarget } from "../software/DeployDialog";
import { Schedules } from "./Schedules";
import { ReportSettingsCard } from "./Settings";

type Tab = ReportKind | "schedules" | "settings";

export function Reports() {
  const { t } = useTranslation();
  const { can } = useAuth();
  const [params, setParams] = useSearchParams();
  const manage = can("reports.manage");
  const tabs: Tab[] = ["compliance", "patches", "software", ...(manage ? (["schedules", "settings"] as Tab[]) : [])];
  const tab = (tabs.includes(params.get("tab") as Tab) ? params.get("tab") : "compliance") as Tab;
  const group = params.get("group") ?? "";
  const groups = useGroups();
  const isReport = tab === "compliance" || tab === "patches" || tab === "software";

  const setTab = (k: Tab) => {
    const next = new URLSearchParams(params);
    if (k === "compliance") next.delete("tab");
    else next.set("tab", k);
    setParams(next);
  };
  const setGroup = (g: string) => {
    const next = new URLSearchParams(params);
    if (g) next.set("group", g);
    else next.delete("group");
    setParams(next);
  };
  const query = group ? `?groups=${group}` : "";
  const exportURL = (format: "pdf" | "csv") => `/api/v1/reports/${tab}/export?format=${format}${group ? `&groups=${group}` : ""}`;

  return (
    <div className="space-y-6">
      <PageHeader
        title={t("reports.title")}
        description={t("reports.subtitle")}
        actions={
          isReport && (
            <div className="flex flex-wrap items-center gap-2">
              <div className="w-48">
                <Select value={group} onChange={(e) => setGroup(e.target.value)} aria-label={t("reports.group")}>
                  <option value="">{t("reports.allGroups")}</option>
                  {(groups ?? []).map((g) => (
                    <option key={g.id} value={g.id}>
                      {g.name}
                    </option>
                  ))}
                </Select>
              </div>
              <a
                href={exportURL("pdf")}
                download
                className="inline-flex h-9 items-center gap-2 rounded-md border border-line bg-surface px-3 text-sm font-medium hover:bg-surface-muted"
              >
                <FileText className="size-4" /> PDF
              </a>
              <a
                href={exportURL("csv")}
                download
                className="inline-flex h-9 items-center gap-2 rounded-md border border-line bg-surface px-3 text-sm font-medium hover:bg-surface-muted"
              >
                <FileSpreadsheet className="size-4" /> CSV
              </a>
            </div>
          )
        }
      />
      <div className="flex gap-1 overflow-x-auto border-b border-line" role="tablist">
        {tabs.map((k) => (
          <button
            key={k}
            role="tab"
            aria-selected={tab === k}
            onClick={() => setTab(k)}
            className={`-mb-px border-b-2 px-4 py-2 text-sm font-medium whitespace-nowrap ${tab === k ? "border-primary text-fg" : "border-transparent text-muted hover:text-fg"}`}
          >
            {t(`reports.tabs.${k}`)}
          </button>
        ))}
      </div>
      {tab === "compliance" && <Compliance query={query} />}
      {tab === "patches" && <Patches query={query} />}
      {tab === "software" && <Software query={query} />}
      {tab === "schedules" && <Schedules />}
      {tab === "settings" && <ReportSettingsCard />}
    </div>
  );
}

function useReport<T>(kind: ReportKind, query: string) {
  const errorText = useErrorText();
  const [data, setData] = useState<T | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [n, setN] = useState(0);
  useEffect(() => {
    const ctl = new AbortController();
    get<T>(`/reports/${kind}${query}`, ctl.signal)
      .then((d) => {
        setData(d);
        setError(null);
      })
      .catch((e) => !ctl.signal.aborted && setError(errorText(e)));
    return () => ctl.abort();
  }, [kind, query, n]);
  const reload = useCallback(() => setN((x) => x + 1), []);
  return { data, error, reload };
}

function Tile({
  label,
  value,
  sub,
  tone,
  icon,
  to,
}: {
  label: string;
  value: string | number;
  sub?: string;
  tone?: string;
  icon?: React.ReactNode;
  to?: string;
}) {
  const body = (
    <>
      <div className="flex items-center gap-2 text-xs font-medium text-muted uppercase">
        {icon}
        {label}
      </div>
      <div className={`mt-2 text-2xl font-semibold tabular-nums ${tone ?? ""}`}>{value}</div>
      {sub && <div className="mt-1 text-xs text-muted">{sub}</div>}
    </>
  );
  return to ? (
    <Link to={to} className="rounded-lg border border-line bg-surface p-4 transition-colors hover:border-primary/40">
      {body}
    </Link>
  ) : (
    <div className="rounded-lg border border-line bg-surface p-4">{body}</div>
  );
}

const scoreTone = (s: number) => (s >= 90 ? "text-success" : s >= 70 ? "text-warning" : "text-danger");
const barTone = (s: number) => (s >= 90 ? "bg-success" : s >= 70 ? "bg-warning" : "bg-danger");
const warn = (n: number) => (n > 0 ? "text-warning" : "");

function Generated({ at }: { at: string }) {
  const { t, i18n } = useTranslation();
  return <p className="text-xs text-muted">{t("reports.generated", { date: formatDate(at, i18n.language) })}</p>;
}

function Compliance({ query }: { query: string }) {
  const { t, i18n } = useTranslation();
  const { data: r, error, reload } = useReport<ComplianceReport>("compliance", query);
  const [all, setAll] = useState(false);
  if (error && !r) return <Alert tone="danger">{error}</Alert>;
  if (!r) return <Spinner />;
  const rows = r.rows.filter((d) => all || !d.compliant);
  return (
    <div className="space-y-6">
      {!r.catalog && <Alert tone="warning">{t("reports.noCatalog")}</Alert>}
      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-5">
        <div className="rounded-lg border border-line bg-surface p-4 lg:col-span-1">
          <div className="text-xs font-medium text-muted uppercase">{t("reports.compliance.score")}</div>
          <div className={`mt-2 text-3xl font-semibold tabular-nums ${scoreTone(r.score)}`}>{r.devices ? `${r.score}%` : "–"}</div>
          <div className="mt-2 h-2 overflow-hidden rounded-full bg-surface-muted">
            <div className={`h-full ${barTone(r.score)}`} style={{ width: `${r.score}%` }} />
          </div>
          <div className="mt-1 text-xs text-muted">{t("reports.ofDevices", { compliant: r.compliant, count: r.devices })}</div>
        </div>
        <Tile label={t("reports.compliance.patches")} value={r.patches} tone={r.patches ? "text-danger" : ""} icon={<ShieldAlert className="size-4" />} />
        <Tile label={t("reports.compliance.software")} value={r.software} tone={warn(r.software)} icon={<Package className="size-4" />} />
        <Tile label={t("reports.compliance.disk")} value={r.disk} tone={warn(r.disk)} icon={<HardDrive className="size-4" />} />
        <Tile
          label={t("reports.compliance.stale", {
            days: r.settings.offlineDays,
          })}
          value={r.stale}
          tone={warn(r.stale)}
          icon={<WifiOff className="size-4" />}
        />
      </div>
      <p className="text-sm text-muted">
        {t("reports.compliance.rule", {
          offline: r.settings.offlineDays,
          grace: r.settings.patchGraceDays,
          disk: r.settings.diskFreePercent,
        })}
      </p>

      <Card title={t("reports.compliance.byGroup")}>
        {r.groups.length === 0 ? (
          <p className="text-sm text-muted">{t("reports.noDevices")}</p>
        ) : (
          <div className="-mx-5 -my-5 overflow-x-auto">
            <table className="w-full min-w-[720px] text-sm">
              <thead className="border-b border-line text-left text-xs text-muted uppercase">
                <tr>
                  <th className="px-5 py-3 font-medium">{t("reports.cols.group")}</th>
                  <th className="w-56 px-3 py-3 font-medium">{t("reports.cols.score")}</th>
                  <th className="px-3 py-3 text-right font-medium">{t("reports.cols.patches")}</th>
                  <th className="px-3 py-3 text-right font-medium">{t("reports.cols.software")}</th>
                  <th className="px-3 py-3 text-right font-medium">{t("reports.cols.disk")}</th>
                  <th className="px-5 py-3 text-right font-medium">{t("reports.cols.stale")}</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-line">
                {r.groups.map((g) => (
                  <tr key={g.groupId}>
                    <td className="px-5 py-2.5 font-medium">{g.name}</td>
                    <td className="px-3 py-2.5">
                      <div className="flex items-center gap-3">
                        <div className="h-2 flex-1 overflow-hidden rounded-full bg-surface-muted">
                          <div className={`h-full ${barTone(g.score)}`} style={{ width: `${g.score}%` }} />
                        </div>
                        <span className={`w-10 text-right font-medium tabular-nums ${scoreTone(g.score)}`}>{g.score}%</span>
                      </div>
                      <div className="text-xs text-muted">{t("reports.ofDevices", { compliant: g.compliant, count: g.devices })}</div>
                    </td>
                    <td className={`px-3 py-2.5 text-right tabular-nums ${g.patches ? "text-danger" : "text-muted"}`}>{g.patches}</td>
                    <td className={`px-3 py-2.5 text-right tabular-nums ${g.software ? "text-warning" : "text-muted"}`}>{g.software}</td>
                    <td className={`px-3 py-2.5 text-right tabular-nums ${g.disk ? "text-warning" : "text-muted"}`}>{g.disk}</td>
                    <td className={`px-5 py-2.5 text-right tabular-nums ${g.stale ? "text-warning" : "text-muted"}`}>{g.stale}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Card>

      <Card
        title={all ? t("reports.compliance.allDevices") : t("reports.compliance.nonCompliant")}
        description={<Generated at={r.generated} />}
        actions={
          <Button variant="secondary" icon={<RefreshCw className="size-4" />} onClick={reload} aria-label={t("common.refresh")} title={t("common.refresh")} />
        }
      >
        <div className="mb-4">
          <Switch checked={all} onChange={setAll} label={t("reports.compliance.showAll")} />
        </div>
        {rows.length === 0 ? (
          <div className="flex items-center gap-2 text-sm text-success">
            <CheckCircle2 className="size-4" /> {t("reports.compliance.allCompliant")}
          </div>
        ) : (
          <div className="-mx-5 -mb-5 max-h-[640px] overflow-auto border-t border-line">
            <table className="w-full min-w-[760px] text-sm">
              <thead className="sticky top-0 border-b border-line bg-surface text-left text-xs text-muted uppercase">
                <tr>
                  <th className="px-5 py-3 font-medium">{t("reports.cols.device")}</th>
                  <th className="px-3 py-3 font-medium">{t("reports.cols.lastSeen")}</th>
                  <th className="px-5 py-3 font-medium">{t("reports.cols.reasons")}</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-line">
                {rows.map((d) => (
                  <tr key={d.id}>
                    <td className="px-5 py-2">
                      <div className="flex items-center gap-2">
                        <Link to={`/devices/${d.id}`} className="font-medium hover:text-primary">
                          {d.hostname}
                        </Link>
                        <PlatformBadge platform={d.platform} />
                      </div>
                      <div className="text-xs text-muted">{d.groupName}</div>
                    </td>
                    <td className="px-3 py-2 whitespace-nowrap">
                      {d.online ? <StatusDot online /> : <span className="text-muted">{timeAgo(d.lastSeenAt, i18n.language)}</span>}
                    </td>
                    <td className="px-5 py-2">
                      <div className="flex flex-wrap gap-1">
                        {d.compliant && <Badge tone="success">{t("reports.compliance.ok")}</Badge>}
                        {d.stale && <Badge tone="warning">{t("reports.reasons.stale")}</Badge>}
                        {d.notScanned && <Badge tone="danger">{t("reports.reasons.notScanned")}</Badge>}
                        {d.security > 0 && (
                          <Badge tone="danger">
                            {t("reports.reasons.security", {
                              count: d.security,
                            })}
                          </Badge>
                        )}
                        {d.outdated > 0 && (
                          <span title={(d.software ?? []).join("\n")}>
                            <Badge tone="warning">
                              {t("reports.reasons.software", {
                                count: d.outdated,
                              })}
                            </Badge>
                          </span>
                        )}
                        {d.lowDisk.map((v) => (
                          <Badge key={v.mount} tone="warning">
                            {t("reports.reasons.disk", {
                              mount: v.mount,
                              percent: v.freePercent,
                            })}
                          </Badge>
                        ))}
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Card>
    </div>
  );
}

function Patches({ query }: { query: string }) {
  const { t, i18n } = useTranslation();
  const { data: r, error, reload } = useReport<PatchesReport>("patches", query);
  if (error && !r) return <Alert tone="danger">{error}</Alert>;
  if (!r) return <Spinner />;
  return (
    <div className="space-y-6">
      <div className="grid grid-cols-2 gap-4 lg:grid-cols-4">
        <Tile
          label={t("reports.patches.security")}
          value={r.devicesSecurity}
          sub={t("reports.ofDevices", { compliant: r.devicesSecurity, count: r.devices })}
          tone={r.devicesSecurity ? "text-danger" : ""}
          icon={<ShieldAlert className="size-4" />}
        />
        <Tile label={t("reports.patches.missing")} value={r.devicesMissing} tone={warn(r.devicesMissing)} />
        <Tile label={t("reports.patches.reboot")} value={r.rebootPending} tone={warn(r.rebootPending)} />
        <Tile label={t("reports.patches.upToDate")} value={r.upToDate} sub={t("reports.patches.scanned", { count: r.scanned })} tone="text-success" />
      </div>
      <Card title={t("reports.patches.updates", { count: r.updates.length })} description={<Generated at={r.generated} />}>
        {r.updates.length === 0 ? (
          <p className="text-sm text-muted">{t("reports.patches.none")}</p>
        ) : (
          <div className="-mx-5 -my-5 max-h-[520px] overflow-auto">
            <table className="w-full min-w-[720px] text-sm">
              <thead className="sticky top-0 border-b border-line bg-surface text-left text-xs text-muted uppercase">
                <tr>
                  <th className="px-5 py-3 font-medium">{t("reports.cols.update")}</th>
                  <th className="px-3 py-3 font-medium">{t("reports.cols.category")}</th>
                  <th className="px-3 py-3 font-medium">{t("reports.cols.released")}</th>
                  <th className="px-5 py-3 text-right font-medium">{t("reports.cols.devices")}</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-line">
                {r.updates.map((u) => (
                  <tr key={u.updateId}>
                    <td className="px-5 py-2">
                      <div className="font-medium">{u.title}</div>
                      {u.kb && <div className="text-xs text-muted">{u.kb}</div>}
                    </td>
                    <td className="px-3 py-2">
                      <CategoryBadge category={u.category} />
                    </td>
                    <td className="px-3 py-2 whitespace-nowrap text-muted">{u.releasedAt ? new Date(u.releasedAt).toLocaleDateString(i18n.language) : "–"}</td>
                    <td className="px-5 py-2 text-right tabular-nums">{u.devices}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Card>
      <Card
        title={t("reports.patches.devices", { count: r.rows.length })}
        actions={
          <Button variant="secondary" icon={<RefreshCw className="size-4" />} onClick={reload} aria-label={t("common.refresh")} title={t("common.refresh")} />
        }
      >
        {r.rows.length === 0 ? (
          <p className="text-sm text-muted">{t("reports.patches.none")}</p>
        ) : (
          <div className="-mx-5 -my-5 max-h-[520px] overflow-auto">
            <table className="w-full min-w-[640px] text-sm">
              <thead className="sticky top-0 border-b border-line bg-surface text-left text-xs text-muted uppercase">
                <tr>
                  <th className="px-5 py-3 font-medium">{t("reports.cols.device")}</th>
                  <th className="px-3 py-3 text-right font-medium">{t("reports.cols.missing")}</th>
                  <th className="px-3 py-3 text-right font-medium">{t("reports.cols.security")}</th>
                  <th className="px-3 py-3 font-medium">{t("reports.cols.reboot")}</th>
                  <th className="px-5 py-3 font-medium">{t("reports.cols.lastScan")}</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-line">
                {r.rows.map((d) => (
                  <tr key={d.id}>
                    <td className="px-5 py-2">
                      <Link to={`/devices/${d.id}?tab=patches`} className="font-medium hover:text-primary">
                        {d.hostname}
                      </Link>
                      <div className="text-xs text-muted">{d.groupName}</div>
                    </td>
                    <td className="px-3 py-2 text-right tabular-nums">{d.missing}</td>
                    <td className={`px-3 py-2 text-right tabular-nums ${d.security ? "font-medium text-danger" : "text-muted"}`}>{d.security}</td>
                    <td className="px-3 py-2">{d.rebootPending && <Badge tone="warning">{t("reports.patches.rebootBadge")}</Badge>}</td>
                    <td className="px-5 py-2 whitespace-nowrap text-muted">{timeAgo(d.patchScanAt, i18n.language)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Card>
    </div>
  );
}

function Software({ query }: { query: string }) {
  const { t } = useTranslation();
  const { can } = useAuth();
  const errorText = useErrorText();
  const { data: r, error, reload } = useReport<SoftwareReport>("software", query);
  const [target, setTarget] = useState<DeployTarget | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);
  const [open, setOpen] = useState<string | null>(null);
  const devicesOf = useMemo(() => {
    const m = new Map<string, string[]>();
    for (const d of r?.rows ?? []) for (const p of d.programs) m.set(p.packageId, [...(m.get(p.packageId) ?? []), d.id]);
    return m;
  }, [r]);
  if (error && !r) return <Alert tone="danger">{error}</Alert>;
  if (!r) return <Spinner />;
  // Puts the winget package in the library (newest version) and opens the deploy dialog for it.
  const addAndUpdate = (p: SoftwareReport["packages"][number]) =>
    post<SoftwarePackage>("/software/packages", {
      name: p.name,
      source: "winget",
      platform: "windows",
      wingetId: p.packageId,
      version: "",
    })
      .then((pkg) => {
        reload();
        setTarget({
          action: "install",
          packageId: pkg.id,
          name: pkg.name,
          platform: "windows",
          deviceIds: devicesOf.get(p.packageId) ?? [],
        });
      })
      .catch((e) => setActionError(errorText(e)));
  const ignore = (packageId: string) =>
    post("/reports/software/ignore", { packageId, ignore: true })
      .then(reload)
      .catch((e) => setActionError(errorText(e)));

  return (
    <div className="space-y-6">
      {r.catalog.packages === 0 && <Alert tone="warning">{t("reports.noCatalog")}</Alert>}
      {actionError && <Alert tone="danger">{actionError}</Alert>}
      <div className="grid grid-cols-2 gap-4 lg:grid-cols-4">
        <Tile
          label={t("reports.software.devices")}
          value={r.devicesOutdated}
          sub={t("reports.ofDevices", { compliant: r.devicesOutdated, count: r.devices })}
          tone={warn(r.devicesOutdated)}
          icon={<Package className="size-4" />}
        />
        <Tile label={t("reports.software.programs")} value={r.packages.length} tone={warn(r.packages.length)} />
        <Tile label={t("reports.software.installs")} value={r.installs} tone={warn(r.installs)} />
        <Tile label={t("reports.software.matched")} value={r.matched} sub={t("reports.software.matchedHint")} />
      </div>
      <Card
        title={t("reports.software.byProgram")}
        description={t("reports.software.note")}
        actions={
          <Button variant="secondary" icon={<RefreshCw className="size-4" />} onClick={reload} aria-label={t("common.refresh")} title={t("common.refresh")} />
        }
      >
        {r.packages.length === 0 ? (
          <div className="flex items-center gap-2 text-sm text-success">
            <CheckCircle2 className="size-4" /> {t("reports.software.none")}
          </div>
        ) : (
          <div className="-mx-5 -my-5 overflow-x-auto">
            <table className="w-full min-w-[760px] text-sm">
              <thead className="border-b border-line text-left text-xs text-muted uppercase">
                <tr>
                  <th className="px-5 py-3 font-medium">{t("reports.cols.program")}</th>
                  <th className="px-3 py-3 font-medium">{t("reports.cols.installed")}</th>
                  <th className="px-3 py-3 font-medium">{t("reports.cols.latest")}</th>
                  <th className="px-3 py-3 text-right font-medium">{t("reports.cols.devices")}</th>
                  <th className="px-5 py-3 text-right font-medium">
                    <span className="sr-only">{t("common.actions")}</span>
                  </th>
                </tr>
              </thead>
              <tbody className="divide-y divide-line">
                {r.packages.map((p) => (
                  <tr key={p.packageId} className="align-top">
                    <td className="px-5 py-2">
                      <button
                        type="button"
                        className="text-left font-medium hover:text-primary"
                        onClick={() => setOpen(open === p.packageId ? null : p.packageId)}
                      >
                        {p.name}
                      </button>
                      <div className="font-mono text-xs text-muted">{p.packageId}</div>
                      {open === p.packageId && (
                        <ul className="mt-2 space-y-0.5 text-xs">
                          {r.rows
                            .filter((d) => d.programs.some((x) => x.packageId === p.packageId))
                            .map((d) => (
                              <li key={d.id}>
                                <Link to={`/devices/${d.id}`} className="hover:text-primary">
                                  {d.hostname}
                                </Link>{" "}
                                <span className="text-muted">{d.programs.find((x) => x.packageId === p.packageId)?.version}</span>
                              </li>
                            ))}
                        </ul>
                      )}
                    </td>
                    <td className="px-3 py-2">
                      <div className="flex flex-wrap gap-1">
                        {p.versions.map((v) => (
                          <Badge key={v} tone="warning">
                            <span className="font-mono">{v}</span>
                          </Badge>
                        ))}
                      </div>
                    </td>
                    <td className="px-3 py-2 font-mono text-xs">{p.latest}</td>
                    <td className="px-3 py-2 text-right tabular-nums">{p.devices}</td>
                    <td className="px-5 py-2">
                      <div className="flex justify-end gap-1">
                        {can("software.deploy") &&
                          (p.libraryId ? (
                            <Button
                              size="sm"
                              icon={<Download className="size-4" />}
                              onClick={() =>
                                setTarget({
                                  action: "install",
                                  packageId: p.libraryId!,
                                  name: p.name,
                                  platform: "windows",
                                  deviceIds: devicesOf.get(p.packageId) ?? [],
                                })
                              }
                            >
                              {t("reports.software.update")}
                            </Button>
                          ) : (
                            can("software.manage") && (
                              <Button
                                size="sm"
                                variant="secondary"
                                icon={<Download className="size-4" />}
                                title={t("reports.software.addHint")}
                                onClick={() => addAndUpdate(p)}
                              >
                                {t("reports.software.update")}
                              </Button>
                            )
                          ))}
                        {can("reports.manage") && (
                          <Button
                            size="sm"
                            variant="ghost"
                            icon={<EyeOff className="size-4" />}
                            onClick={() => ignore(p.packageId)}
                            title={t("reports.software.ignore")}
                            aria-label={t("reports.software.ignore")}
                          />
                        )}
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Card>
      {r.ignored.length > 0 && <p className="text-xs text-muted">{t("reports.software.ignoredCount", { count: r.ignored.length })}</p>}
      <DeployDialog target={target} onClose={() => setTarget(null)} />
    </div>
  );
}
