import { useTranslation } from "react-i18next";
import { Code2, Globe, ListTree, type LucideIcon } from "lucide-react";
import type { ConfigState, ConfigType, Configuration, DeviceConfigState } from "../../api";
import { Badge } from "../../components/ui";

export const typeIcons: Record<ConfigType, LucideIcon> = { browser: Globe, registry: ListTree, script: Code2 };

export function TypeBadge({ type }: { type: ConfigType }) {
  const { t } = useTranslation();
  const Icon = typeIcons[type];
  return (
    <span className="inline-flex items-center gap-1 rounded-sm bg-primary/10 px-1.5 py-0.5 text-xs font-medium text-primary">
      <Icon className="size-3.5" /> {t(`configs.types.${type}`)}
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
