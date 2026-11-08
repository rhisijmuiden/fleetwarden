import { useEffect, useState } from "react";
import { NavLink } from "react-router";
import { useTranslation } from "react-i18next";
import { BatteryCharging, BrickWall, Code2, Globe, HardDrive, ListTree, Monitor, Printer, ShieldUser, type LucideIcon } from "lucide-react";
import { get, type ConfigProfile, type ConfigState, type ConfigType, type Configuration, type DeviceConfigState } from "../../api";
import { Badge } from "../../components/ui";

export const typeIcons: Record<ConfigType, LucideIcon> = { browser: Globe, registry: ListTree, script: Code2, drives: HardDrive, printers: Printer, desktop: Monitor, power: BatteryCharging, admins: ShieldUser, firewall: BrickWall };

// TypeBadge shows the type of a configuration, or its name with the type's icon.
export function TypeBadge({ type, label }: { type: ConfigType; label?: string }) {
  const { t } = useTranslation();
  const Icon = typeIcons[type];
  return (
    <span className="inline-flex max-w-full items-center gap-1 rounded-sm bg-primary/10 px-1.5 py-0.5 text-xs font-medium text-primary">
      <Icon className="size-3.5 shrink-0" /> <span className="truncate">{label ?? t(`configs.types.${type}`)}</span>
    </span>
  );
}

const stateTone: Record<ConfigState, "neutral" | "primary" | "success" | "warning" | "danger"> = {
  pending: "neutral",
  applied: "success",
  failed: "danger",
  waiting: "primary",
  unsupported: "warning",
  disabled: "neutral",
};

// The state on a device; an older revision than the current one is still pending.
export function effectiveState(d: DeviceConfigState): ConfigState {
  if (d.state === "disabled") return "disabled";
  if (d.state !== "pending" && d.revision < d.currentRevision) return "pending";
  return d.state;
}

export function ConfigStateBadge({ state }: { state: ConfigState }) {
  const { t } = useTranslation();
  return <Badge tone={stateTone[state]}>{t(`configs.state.${state}`)}</Badge>;
}

// Summary of a configuration over its devices: "12 of 14 applied".
export function ConfigProgress({ c }: { c: Configuration }) {
  const { t } = useTranslation();
  if (!c.enabled) return <Badge>{t("configs.disabled")}</Badge>;
  if (c.devices === 0) return <span className="text-xs text-muted">{t("configs.noDevices")}</span>;
  const pct = Math.round((c.applied / c.devices) * 100);
  return (
    <div className="w-40 space-y-1">
      <div className="h-1.5 overflow-hidden rounded-full bg-surface-muted">
        <div className={`h-full ${c.failed ? "bg-danger" : "bg-success"}`} style={{ width: `${pct}%` }} />
      </div>
      <p className="text-xs text-muted">
        {t("configs.appliedOf", { applied: c.applied, total: c.devices })}
        {c.failed > 0 && <span className="text-danger"> · {t("configs.failedCount", { count: c.failed })}</span>}
      </p>
    </div>
  );
}

export function useProfiles(): ConfigProfile[] | null {
  const [profiles, setProfiles] = useState<ConfigProfile[] | null>(null);
  useEffect(() => {
    get<ConfigProfile[]>("/config-profiles")
      .then(setProfiles)
      .catch(() => setProfiles([]));
  }, []);
  return profiles;
}

// ConfigTabs switches between configurations and profiles.
export function ConfigTabs() {
  const { t } = useTranslation();
  const tab = ({ isActive }: { isActive: boolean }) =>
    `-mb-px border-b-2 px-1 pb-2.5 text-sm font-medium ${isActive ? "border-primary text-primary" : "border-transparent text-muted hover:text-fg"}`;
  return (
    <nav className="flex gap-6 border-b border-line">
      <NavLink to="/configurations" end className={tab}>
        {t("configs.tabs.items")}
      </NavLink>
      <NavLink to="/configurations/profiles" className={tab}>
        {t("configs.tabs.profiles")}
      </NavLink>
    </nav>
  );
}
