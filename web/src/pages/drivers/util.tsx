import { useTranslation } from "react-i18next";
import { Cpu, MemoryStick } from "lucide-react";
import type { DriverKind, DriverState, DriverUpdate } from "../../api";
import { Badge } from "../../components/ui";

export function DriverStateBadge({ state }: { state: DriverState }) {
  const { t } = useTranslation();
  const tone = { available: "neutral", queued: "primary", downloading: "primary", ready: "success", failed: "danger", rejected: "neutral" } as const;
  return <Badge tone={tone[state]}>{t(`drivers.states.${state}`)}</Badge>;
}

export function KindBadge({ kind }: { kind: DriverKind }) {
  const { t } = useTranslation();
  const Icon = kind === "firmware" ? MemoryStick : Cpu;
  return (
    <span className={`inline-flex items-center gap-1 rounded-sm px-1.5 py-0.5 text-xs font-medium ${kind === "firmware" ? "bg-warning/15 text-warning" : "bg-primary/10 text-primary"}`}>
      <Icon className="size-3" />
      {t(`drivers.kinds.${kind}`)}
    </span>
  );
}

export function sourceName(u: Pick<DriverUpdate, "source">, t: (k: string) => string) {
  return t(`drivers.sources.${u.source}`);
}

// One line under the title: vendor, class, version.
export function driverMeta(u: Pick<DriverUpdate, "vendor" | "class" | "version">) {
  return [u.vendor, u.class, u.version].filter(Boolean).join(" · ");
}
