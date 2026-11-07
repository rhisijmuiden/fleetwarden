import { useTranslation } from "react-i18next";
import { RefreshCw } from "lucide-react";
import type { BackupRun } from "../../../api";
import { Badge, Button, Card, Spinner, formatDate } from "../../../components/ui";
import { RunBadge } from "./Locations";
import { formatBytes } from "./util";

function duration(r: BackupRun): string {
  if (!r.finishedAt) return "–";
  const s = Math.max(0, Math.round((new Date(r.finishedAt).getTime() - new Date(r.startedAt).getTime()) / 1000));
  return s < 60 ? `${s}s` : `${Math.floor(s / 60)}m ${s % 60}s`;
}

export function History({ runs, onRefresh }: { runs: BackupRun[] | null; onRefresh: () => void }) {
  const { t, i18n } = useTranslation();
  return (
    <Card
      title={t("backup.history")}
      description={t("backup.historyHint")}
      actions={<Button variant="ghost" size="sm" icon={<RefreshCw className="size-4" />} onClick={onRefresh} aria-label={t("common.refresh")} />}
    >
      {!runs ? (
        <Spinner />
      ) : runs.length === 0 ? (
        <p className="py-6 text-center text-sm text-muted">{t("backup.historyEmpty")}</p>
      ) : (
        <div className="-mx-5 -my-5 overflow-x-auto">
          <table className="w-full min-w-[760px] text-sm">
            <thead className="border-b border-line text-left text-xs text-muted uppercase">
              <tr>
                <th className="px-5 py-3 font-medium">{t("backup.col.started")}</th>
                <th className="px-3 py-3 font-medium">{t("backup.col.location")}</th>
                <th className="px-3 py-3 font-medium">{t("backup.col.kind")}</th>
                <th className="px-3 py-3 font-medium">{t("backup.col.size")}</th>
                <th className="px-3 py-3 font-medium">{t("backup.col.uploaded")}</th>
                <th className="px-3 py-3 font-medium">{t("backup.col.duration")}</th>
                <th className="px-5 py-3 font-medium">{t("backup.col.status")}</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-line">
              {runs.map((r) => (
                <tr key={r.id}>
                  <td className="px-5 py-3 whitespace-nowrap">
                    {formatDate(r.startedAt, i18n.language)}
                    <p className="text-xs text-muted">{r.trigger === "schedule" ? t("backup.trigger.schedule") : t("backup.trigger.manual", { who: r.actor })}</p>
                  </td>
                  <td className="px-3 py-3">{r.locationName}</td>
                  <td className="px-3 py-3">{r.kind && r.kind !== "auto" ? <Badge tone={r.kind === "full" ? "primary" : "neutral"}>{t(`backup.kinds.${r.kind}`)}</Badge> : "–"}</td>
                  <td className="px-3 py-3 whitespace-nowrap">{r.sizeBytes ? formatBytes(r.sizeBytes, i18n.language) : "–"}</td>
                  <td className="px-3 py-3 whitespace-nowrap">{r.uploadedBytes ? formatBytes(r.uploadedBytes, i18n.language) : "–"}</td>
                  <td className="px-3 py-3 whitespace-nowrap">{duration(r)}</td>
                  <td className="px-5 py-3">
                    <RunBadge status={r.status} />
                    {r.error && <p className="mt-1 max-w-xs text-xs break-words text-danger">{r.error}</p>}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </Card>
  );
}
