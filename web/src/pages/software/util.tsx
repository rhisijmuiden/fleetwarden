import { useTranslation } from "react-i18next";
import type { JobCounts, JobState, PackageSource } from "../../api";
import { Badge } from "../../components/ui";

const stateTone: Record<JobState, "neutral" | "primary" | "success" | "warning" | "danger"> = {
  pending: "neutral",
  sent: "primary",
  downloading: "primary",
  installing: "primary",
  uninstalling: "primary",
  waiting: "warning",
  completed: "success",
  skipped: "neutral",
  failed: "danger",
  cancelled: "neutral",
  expired: "warning",
};

export function JobStateBadge({ state, message }: { state: JobState; message?: string }) {
  const { t } = useTranslation();
  return (
    <span title={message || undefined}>
      <Badge tone={stateTone[state] ?? "neutral"}>{t(`software.state.${state}`)}</Badge>
    </span>
  );
}

export function SourceBadge({ source }: { source: PackageSource }) {
  const { t } = useTranslation();
  const tone = source === "winget" ? "primary" : source === "upload" ? "warning" : "neutral";
  return <Badge tone={tone}>{t(`software.sources.${source}`)}</Badge>;
}

// Progress shows the outcome of a deployment's jobs as one bar.
export function Progress({ counts }: { counts: JobCounts }) {
  const { t } = useTranslation();
  const total = Math.max(counts.total, 1);
  const parts: { n: number; cls: string; label: string }[] = [
    { n: counts.completed, cls: "bg-success", label: t("software.counts.completed", { count: counts.completed }) },
    { n: counts.skipped, cls: "bg-success/50", label: t("software.counts.skipped", { count: counts.skipped }) },
    { n: counts.running, cls: "bg-primary", label: t("software.counts.running", { count: counts.running }) },
    { n: counts.failed, cls: "bg-danger", label: t("software.counts.failed", { count: counts.failed }) },
    { n: counts.cancelled, cls: "bg-muted/40", label: t("software.counts.cancelled", { count: counts.cancelled }) },
  ];
  const summary = parts
    .filter((p) => p.n > 0)
    .map((p) => p.label)
    .concat(counts.pending ? [t("software.counts.pending", { count: counts.pending })] : [])
    .join(" · ");
  return (
    <div className="min-w-[140px] space-y-1">
      <div className="flex h-2 overflow-hidden rounded-full bg-surface-muted" role="img" aria-label={summary || t("software.counts.none")}>
        {parts.map((p, i) => (p.n ? <div key={i} className={p.cls} style={{ width: `${(p.n / total) * 100}%` }} /> : null))}
      </div>
      <div className="text-xs text-muted">{counts.total === 0 ? t("software.counts.none") : summary}</div>
    </div>
  );
}

export function formatSize(bytes: number, lang: string) {
  if (!bytes) return "";
  const units = ["B", "KB", "MB", "GB"];
  let v = bytes;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${new Intl.NumberFormat(lang, { maximumFractionDigits: i ? 1 : 0 }).format(v)} ${units[i]}`;
}
