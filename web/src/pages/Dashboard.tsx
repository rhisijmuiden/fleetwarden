import { useEffect, useState } from "react";
import { Link } from "react-router";
import { useTranslation } from "react-i18next";
import { Bug, Monitor, Plus } from "lucide-react";
import { get, type ComplianceReport, type DeviceStats, type VulnSummary, type Vulnerability } from "../api";
import { KEVBadge, SeverityBadge } from "./vulnerabilities/util";
import { useAuth } from "../auth";

export function Dashboard() {
  const { t } = useTranslation();
  const { can } = useAuth();
  const [stats, setStats] = useState<DeviceStats | null>(null);
  const [comp, setComp] = useState<ComplianceReport | null>(null);
  const [alerts, setAlerts] = useState<number | null>(null);
  const [vulns, setVulns] = useState<{ sum: VulnSummary; top: Vulnerability[] } | null>(null);
  useEffect(() => {
    if (!can("devices.view")) return;
    const load = () =>
      Promise.all([get<VulnSummary>("/vulnerabilities/summary"), get<Vulnerability[]>("/vulnerabilities?limit=5")])
        .then(([sum, top]) => setVulns({ sum, top }))
        .catch(() => undefined);
    load();
    const id = setInterval(load, 300000);
    return () => clearInterval(id);
  }, []);
  useEffect(() => {
    if (!can("devices.view")) return;
    const load = () =>
      get<{ open: number }>("/alerts/count")
        .then((r) => setAlerts(r.open))
        .catch(() => undefined);
    load();
    const id = setInterval(load, 60000);
    return () => clearInterval(id);
  }, []);
  useEffect(() => {
    if (!can("devices.view")) return;
    const load = () =>
      get<ComplianceReport>("/reports/compliance")
        .then(setComp)
        .catch(() => undefined);
    load();
    const id = setInterval(load, 300000);
    return () => clearInterval(id);
  }, []);
  useEffect(() => {
    if (!can("devices.view")) return;
    const load = () =>
      get<DeviceStats>("/devices/stats")
        .then(setStats)
        .catch(() => undefined);
    load();
    const id = setInterval(load, 30000);
    return () => clearInterval(id);
  }, []);
  const tiles = [
    {
      label: t("dashboard.devices"),
      value: stats?.total ?? 0,
      sub: stats ? `${stats.platform.windows ?? 0} Windows · ${stats.platform.linux ?? 0} Linux` : "",
      to: "/devices",
    },
    {
      label: t("dashboard.online"),
      value: stats?.online ?? 0,
      sub: stats?.total ? `${Math.round(((stats.online ?? 0) / stats.total) * 100)}%` : "",
      to: "/devices?status=online",
    },
    {
      label: t("dashboard.compliance"),
      value: comp && comp.devices ? `${comp.score}%` : "–",
      sub: comp ? t("dashboard.compliantOf", { count: comp.compliant, total: comp.devices }) : "",
      tone: comp && comp.devices ? (comp.score >= 90 ? "text-success" : comp.score >= 70 ? "text-warning" : "text-danger") : "",
      to: "/reports",
    },
    {
      label: t("dashboard.missingPatches"),
      value: comp?.patches ?? "–",
      sub: t("dashboard.securityDevices"),
      tone: comp?.patches ? "text-danger" : "",
      to: "/reports?tab=patches",
    },
    {
      label: t("dashboard.outdatedSoftware"),
      value: comp?.software ?? "–",
      sub: t("dashboard.outdatedDevices"),
      tone: comp?.software ? "text-warning" : "",
      to: "/reports?tab=software",
    },
    {
      label: t("dashboard.openAlerts"),
      value: alerts ?? "–",
      sub: alerts === 0 ? t("dashboard.noAlerts") : "",
      tone: alerts ? "text-danger" : "",
      to: "/alerts",
    },
  ];
  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">{t("dashboard.title")}</h1>
        <p className="mt-1 text-sm text-muted">{t("dashboard.subtitle")}</p>
      </div>
      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3 2xl:grid-cols-6">
        {tiles.map((s) => {
          const body = (
            <>
              <p className="text-sm text-muted">{s.label}</p>
              <p className={`mt-2 text-3xl font-semibold tabular-nums ${"tone" in s ? s.tone : ""}`}>{s.value}</p>
              {s.sub && <p className="mt-1 text-xs text-muted">{s.sub}</p>}
            </>
          );
          return s.to ? (
            <Link key={s.label} to={s.to} className="rounded-lg border border-line bg-surface p-5 transition-colors hover:border-primary/40">
              {body}
            </Link>
          ) : (
            <div key={s.label} className="rounded-lg border border-line bg-surface p-5">
              {body}
            </div>
          );
        })}
      </div>
      {vulns && vulns.top.length > 0 && (
        <section className="rounded-lg border border-line bg-surface">
          <div className="flex flex-wrap items-center justify-between gap-2 border-b border-line px-5 py-4">
            <div className="flex items-center gap-2">
              <Bug className="size-4 text-muted" />
              <h2 className="text-base font-semibold">{t("dashboard.vulnerabilities")}</h2>
            </div>
            <p className="text-sm text-muted">
              {t("dashboard.vulnSummary", {
                cves: vulns.sum.cves,
                critical: vulns.sum.bySeverity.critical ?? 0,
                kev: vulns.sum.kev,
                devices: vulns.sum.devices,
              })}
            </p>
          </div>
          <ul className="divide-y divide-line">
            {vulns.top.map((v) => (
              <li key={v.cve} className="flex flex-wrap items-center gap-x-4 gap-y-1 px-5 py-3 text-sm">
                <Link to={`/vulnerabilities/${v.cve}`} className="font-mono font-medium hover:text-primary">
                  {v.cve}
                </Link>
                <SeverityBadge severity={v.severity} score={v.score} />
                {v.kev && <KEVBadge ransomware={v.ransomware} />}
                <span className="min-w-0 flex-1 truncate text-muted">{v.title || v.products.join(", ")}</span>
                <span className="text-muted tabular-nums">{t("dashboard.vulnDevices", { count: v.devices })}</span>
              </li>
            ))}
          </ul>
          <div className="border-t border-line px-5 py-3 text-sm">
            <Link to="/vulnerabilities" className="text-primary hover:underline">
              {t("dashboard.allVulnerabilities")}
            </Link>
          </div>
        </section>
      )}
      {stats && stats.total === 0 && (
        <div className="grid place-items-center rounded-lg border border-dashed border-line bg-surface px-6 py-16 text-center">
          <Monitor className="size-10 text-muted" strokeWidth={1.5} />
          <p className="mt-3 max-w-sm text-sm text-muted">{t("dashboard.empty")}</p>
          {can("devices.manage") && (
            <Link
              to="/devices"
              className="mt-4 inline-flex h-9 items-center gap-2 rounded-md bg-primary px-4 text-sm font-medium text-primary-contrast hover:bg-primary-hover"
            >
              <Plus className="size-4" /> {t("addClient.button")}
            </Link>
          )}
        </div>
      )}
    </div>
  );
}
