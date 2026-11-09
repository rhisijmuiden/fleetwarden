import { useTranslation } from "react-i18next";
import { Monitor, Server, Boxes } from "lucide-react";
import type { Device } from "../../api";

export { formatBytes } from "../admin/backup/util";

// "3 minutes ago" in the user's language.
export function timeAgo(iso: string | null | undefined, lang: string): string {
  if (!iso) return "–";
  const diff = (new Date(iso).getTime() - Date.now()) / 1000;
  const rtf = new Intl.RelativeTimeFormat(lang === "nl" ? "nl" : "en", { numeric: "auto" });
  const steps: [number, Intl.RelativeTimeFormatUnit][] = [
    [60, "second"],
    [3600, "minute"],
    [86400, "hour"],
    [86400 * 30, "day"],
    [86400 * 365, "month"],
  ];
  const abs = Math.abs(diff);
  if (abs < 45) return rtf.format(0, "second");
  for (let i = 1; i < steps.length; i++) {
    if (abs < steps[i][0]) return rtf.format(Math.round(diff / steps[i - 1][0]), steps[i][1]);
  }
  return rtf.format(Math.round(diff / (86400 * 365)), "year");
}

export function StatusDot({ online }: { online: boolean }) {
  const { t } = useTranslation();
  return (
    <span className="inline-flex items-center gap-1.5 text-xs" title={online ? t("devices.online") : t("devices.offline")}>
      <span className={`size-2 rounded-full ${online ? "bg-success shadow-[0_0_0_3px] shadow-success/20" : "bg-line"}`} />
      <span className={online ? "text-success" : "text-muted"}>{online ? t("devices.online") : t("devices.offline")}</span>
    </span>
  );
}

export function KindIcon({ device, className = "size-4" }: { device: Pick<Device, "kind">; className?: string }) {
  const Icon = device.kind === "hypervisor" ? Boxes : device.kind === "server" ? Server : Monitor;
  return <Icon className={className} strokeWidth={1.8} />;
}

export function PlatformBadge({ platform }: { platform: string }) {
  return (
    <span className="inline-flex items-center rounded-sm bg-surface-muted px-1.5 py-0.5 text-xs font-medium text-muted">
      {platform === "windows" ? "Windows" : "Linux"}
    </span>
  );
}
