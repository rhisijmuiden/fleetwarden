import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { X } from "lucide-react";
import { get, put, type ReportSettings } from "../../api";
import { Alert, Button, Card, Field, Input, Spinner, useErrorText } from "../../components/ui";

// ReportSettingsCard sets when a device counts as compliant and which
// programs the outdated software report leaves out.
export function ReportSettingsCard() {
  const { t } = useTranslation();
  const errorText = useErrorText();
  const [rs, setRs] = useState<ReportSettings | null>(null);
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<{
    tone: "success" | "danger";
    text: string;
  } | null>(null);
  useEffect(() => {
    get<ReportSettings>("/reports/settings")
      .then(setRs)
      .catch((e) => setMsg({ tone: "danger", text: errorText(e) }));
  }, []);
  if (!rs) return msg ? <Alert tone={msg.tone}>{msg.text}</Alert> : <Spinner />;
  const num = (k: keyof Omit<ReportSettings, "ignoredSoftware">) => (e: React.ChangeEvent<HTMLInputElement>) => setRs({ ...rs, [k]: Number(e.target.value) });
  const save = (next: ReportSettings) => {
    setBusy(true);
    setMsg(null);
    put<ReportSettings>("/reports/settings", next)
      .then((r) => {
        setRs(r);
        setMsg({ tone: "success", text: t("common.saved") });
      })
      .catch((e) => setMsg({ tone: "danger", text: errorText(e) }))
      .finally(() => setBusy(false));
  };
  return (
    <div className="space-y-6">
      {msg && <Alert tone={msg.tone}>{msg.text}</Alert>}
      <Card title={t("reports.settings.title")} description={t("reports.settings.subtitle")}>
        <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
          <Field label={t("reports.settings.offlineDays")} hint={t("reports.settings.offlineHint")}>
            {(id) => <Input id={id} type="number" min={1} max={365} value={rs.offlineDays} onChange={num("offlineDays")} />}
          </Field>
          <Field label={t("reports.settings.graceDays")} hint={t("reports.settings.graceHint")}>
            {(id) => <Input id={id} type="number" min={0} max={365} value={rs.patchGraceDays} onChange={num("patchGraceDays")} />}
          </Field>
          <Field label={t("reports.settings.diskPercent")} hint={t("reports.settings.diskHint")}>
            {(id) => <Input id={id} type="number" min={1} max={90} value={rs.diskFreePercent} onChange={num("diskFreePercent")} />}
          </Field>
          <Field label={t("reports.settings.expiringDays")} hint={t("reports.settings.expiringHint")}>
            {(id) => <Input id={id} type="number" min={1} max={730} value={rs.expiringDays} onChange={num("expiringDays")} />}
          </Field>
        </div>
        <div className="mt-4 flex justify-end">
          <Button onClick={() => save(rs)} loading={busy}>
            {t("common.save")}
          </Button>
        </div>
      </Card>
      <Card title={t("reports.settings.ignored")} description={t("reports.settings.ignoredHint")}>
        {rs.ignoredSoftware.length === 0 ? (
          <p className="text-sm text-muted">{t("reports.settings.noneIgnored")}</p>
        ) : (
          <ul className="flex flex-wrap gap-2">
            {rs.ignoredSoftware.map((id) => (
              <li key={id} className="inline-flex items-center gap-1 rounded-md border border-line bg-surface-muted py-1 pr-1 pl-2.5 font-mono text-xs">
                {id}
                <button
                  type="button"
                  className="grid size-5 place-items-center rounded text-muted hover:bg-surface hover:text-fg"
                  aria-label={t("reports.settings.unignore", { id })}
                  title={t("reports.settings.unignore", { id })}
                  onClick={() =>
                    save({
                      ...rs,
                      ignoredSoftware: rs.ignoredSoftware.filter((x) => x !== id),
                    })
                  }
                >
                  <X className="size-3.5" />
                </button>
              </li>
            ))}
          </ul>
        )}
      </Card>
    </div>
  );
}
