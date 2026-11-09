import { useState } from "react";
import { Link } from "react-router";
import { useTranslation } from "react-i18next";
import { CheckCircle2, Flame, Rocket, Skull } from "lucide-react";
import {
  post,
  type VulnFix,
  type VulnFixResult,
  type VulnKind,
  type VulnSeverity,
} from "../../api";
import { Alert, Badge, Button, Modal, useErrorText } from "../../components/ui";

export const severities: VulnSeverity[] = [
  "critical",
  "high",
  "medium",
  "low",
  "none",
];

const severityTone: Record<
  VulnSeverity,
  "danger" | "warning" | "primary" | "neutral"
> = {
  critical: "danger",
  high: "warning",
  medium: "primary",
  low: "neutral",
  none: "neutral",
};

export function SeverityBadge({
  severity,
  score,
}: {
  severity: VulnSeverity;
  score?: number;
}) {
  const { t } = useTranslation();
  return (
    <span className="inline-flex items-center gap-1.5 whitespace-nowrap">
      <Badge tone={severityTone[severity] ?? "neutral"}>
        {t(`vulns.severity.${severity}`)}
      </Badge>
      {score ? (
        <span className="text-xs text-muted tabular-nums">
          {score.toFixed(1)}
        </span>
      ) : null}
    </span>
  );
}

// Dot colours for the per-device counts.
export const severityDot: Record<VulnSeverity, string> = {
  critical: "bg-danger",
  high: "bg-warning",
  medium: "bg-primary",
  low: "bg-muted",
  none: "bg-line",
};

export function KEVBadge({ ransomware }: { ransomware?: boolean }) {
  const { t } = useTranslation();
  return (
    <span
      className="inline-flex items-center gap-1 rounded-sm bg-danger/10 px-1.5 py-0.5 text-xs font-medium text-danger"
      title={t("vulns.kevHint")}
    >
      {ransomware ? <Skull className="size-3" /> : <Flame className="size-3" />}
      {ransomware ? t("vulns.ransomware") : t("vulns.kev")}
    </span>
  );
}

export function FixBadge({ fix }: { fix: VulnFix }) {
  const { t } = useTranslation();
  const tone =
    fix === "patch" || fix === "software"
      ? "success"
      : fix === "update"
        ? "warning"
        : "neutral";
  return <Badge tone={tone}>{t(`vulns.fix.${fix}`)}</Badge>;
}

export function kindLabel(kind: VulnKind, t: (k: string) => string) {
  return t(`vulns.kinds.${kind}`);
}

// FixDialog rolls out the fixes of CVEs, optionally on some devices only.
export function FixDialog({
  open,
  cves,
  deviceIds,
  onClose,
  onDone,
}: {
  open: boolean;
  cves: string[];
  deviceIds?: string[];
  onClose: () => void;
  onDone?: () => void;
}) {
  const { t } = useTranslation();
  const errorText = useErrorText();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [result, setResult] = useState<VulnFixResult | null>(null);
  const close = () => {
    setResult(null);
    setError(null);
    onClose();
  };
  const run = () => {
    setBusy(true);
    setError(null);
    post<VulnFixResult>("/vulnerabilities/fix", { cves, deviceIds })
      .then((r) => {
        setResult(r);
        onDone?.();
      })
      .catch((e) => setError(errorText(e)))
      .finally(() => setBusy(false));
  };
  // Group the skipped devices by reason.
  const skipped = new Map<string, string[]>();
  for (const s of result?.skipped ?? []) {
    const list = skipped.get(s.reason) ?? [];
    if (!list.includes(s.hostname)) list.push(s.hostname);
    skipped.set(s.reason, list);
  }
  return (
    <Modal
      open={open}
      onClose={close}
      title={t("vulns.fixTitle", { count: cves.length })}
      footer={
        result ? (
          <Button onClick={close}>{t("common.close")}</Button>
        ) : (
          <>
            <Button variant="secondary" onClick={close}>
              {t("common.cancel")}
            </Button>
            <Button
              icon={<Rocket className="size-4" />}
              loading={busy}
              onClick={run}
            >
              {t("vulns.fixRun")}
            </Button>
          </>
        )
      }
    >
      {error && <Alert tone="danger">{error}</Alert>}
      {!result ? (
        <div className="space-y-3 text-sm">
          <p>{t("vulns.fixIntro")}</p>
          <ul className="list-disc space-y-1 pl-5 text-muted">
            <li>{t("vulns.fixPatch")}</li>
            <li>{t("vulns.fixSoftware")}</li>
            <li>{t("vulns.fixNone")}</li>
          </ul>
          <p className="font-mono text-xs break-words text-muted">
            {cves.slice(0, 12).join(", ")}
            {cves.length > 12 && ` +${cves.length - 12}`}
          </p>
        </div>
      ) : (
        <div className="space-y-4 text-sm">
          <Alert
            tone={
              result.patchRuns + result.deployments.length > 0
                ? "success"
                : "warning"
            }
            title={t("vulns.fixDone")}
          >
            {t("vulns.fixSummary", {
              runs: result.patchRuns,
              deployments: result.deployments.length,
            })}
          </Alert>
          {result.deployments.length > 0 && (
            <ul className="space-y-1">
              {result.deployments.map((d) => (
                <li key={d.id} className="flex items-center gap-2">
                  <CheckCircle2 className="size-4 text-success" />
                  <Link
                    to={`/deployments/${d.id}`}
                    className="font-medium hover:text-primary"
                  >
                    {d.name}
                  </Link>
                  <span className="text-muted">
                    {t("vulns.devicesCount", { count: d.devices })}
                  </span>
                </li>
              ))}
            </ul>
          )}
          {skipped.size > 0 && (
            <div className="space-y-2">
              <p className="font-medium">{t("vulns.skippedTitle")}</p>
              <ul className="space-y-1 text-muted">
                {[...skipped.entries()].map(([reason, hosts]) => (
                  <li key={reason}>
                    <span className="text-fg">
                      {t(`vulns.skipped.${reason}`, {
                        defaultValue: reason,
                      })}
                    </span>
                    {": "}
                    {hosts.slice(0, 8).join(", ")}
                    {hosts.length > 8 && ` +${hosts.length - 8}`}
                  </li>
                ))}
              </ul>
            </div>
          )}
        </div>
      )}
    </Modal>
  );
}
