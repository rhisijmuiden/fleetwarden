import { useEffect, useState } from "react";
import { Link } from "react-router";
import { useTranslation } from "react-i18next";
import { Monitor, Plus } from "lucide-react";
import { get, type DeviceStats } from "../api";
import { useAuth } from "../auth";

export function Dashboard() {
  const { t } = useTranslation();
  const { can } = useAuth();
  const [stats, setStats] = useState<DeviceStats | null>(null);
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
    { label: t("dashboard.missingPatches"), value: "–", sub: t("dashboard.phase", { phase: 4 }) },
    { label: t("dashboard.outdatedSoftware"), value: "–", sub: t("dashboard.phase", { phase: 7 }) },
  ];
  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">{t("dashboard.title")}</h1>
        <p className="mt-1 text-sm text-muted">{t("dashboard.subtitle")}</p>
      </div>
      <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
        {tiles.map((s) => {
          const body = (
            <>
              <p className="text-sm text-muted">{s.label}</p>
              <p className="mt-2 text-3xl font-semibold tabular-nums">{s.value}</p>
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
