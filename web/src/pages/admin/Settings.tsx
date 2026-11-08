import { useEffect, useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { get, put, type ScanSettings } from "../../api";
import { IntervalInput } from "../../components/IntervalInput";
import { Updates } from "../../components/Updates";
import { Alert, Button, Card, Field, Input, PageHeader, Spinner, useErrorText } from "../../components/ui";

type General = { publicUrl: string; detectedUrl: string };

export function Settings() {
  const { t } = useTranslation();
  const errorText = useErrorText();
  const [g, setG] = useState<General | null>(null);
  const [msg, setMsg] = useState<{ tone: "success" | "danger"; text: string } | null>(null);
  useEffect(() => {
    get<General>("/settings/general").then(setG).catch((e) => setMsg({ tone: "danger", text: errorText(e) }));
  }, []);

  const save = async (e: FormEvent) => {
    e.preventDefault();
    if (!g) return;
    setMsg(null);
    try {
      setG(await put<General>("/settings/general", { publicUrl: g.publicUrl }));
      setMsg({ tone: "success", text: t("common.saved") });
    } catch (err) {
      setMsg({ tone: "danger", text: errorText(err) });
    }
  };

  return (
    <div className="space-y-6">
      <PageHeader title={t("settings.title")} description={t("settings.subtitle")} />
      <Card title={t("settings.general")}>
        {!g ? (
          <Spinner />
        ) : (
          <form onSubmit={save} className="max-w-xl space-y-4">
            {msg && <Alert tone={msg.tone}>{msg.text}</Alert>}
            <Field label={t("settings.publicUrl")} hint={t("settings.publicUrlHint", { url: g.detectedUrl })}>
              {(id) => <Input id={id} type="url" value={g.publicUrl} placeholder={g.detectedUrl} onChange={(e) => setG({ ...g, publicUrl: e.target.value })} />}
            </Field>
            <Button type="submit">{t("common.save")}</Button>
          </form>
        )}
      </Card>
      <AgentScans />
      <Card title={t("updates.title")} description={t("updates.subtitle")}>
        <Updates />
      </Card>
    </div>
  );
}

function AgentScans() {
  const { t } = useTranslation();
  const errorText = useErrorText();
  const [c, setC] = useState<ScanSettings | null>(null);
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<{
    tone: "success" | "danger";
    text: string;
  } | null>(null);
  useEffect(() => {
    get<ScanSettings>("/settings/scans")
      .then(setC)
      .catch((e) => setMsg({ tone: "danger", text: errorText(e) }));
  }, []);

  const save = async (e: FormEvent) => {
    e.preventDefault();
    if (!c) return;
    setBusy(true);
    setMsg(null);
    try {
      setC(await put<ScanSettings>("/settings/scans", c));
      setMsg({
        tone: "success",
        text: `${t("common.saved")} ${t("settings.scansApplied")}`,
      });
    } catch (err) {
      setMsg({ tone: "danger", text: errorText(err) });
    } finally {
      setBusy(false);
    }
  };

  return (
    <Card title={t("settings.scans")} description={t("settings.scansSubtitle")}>
      {!c ? (
        msg ? (
          <Alert tone={msg.tone}>{msg.text}</Alert>
        ) : (
          <Spinner />
        )
      ) : (
        <form onSubmit={save} className="max-w-xl space-y-4">
          {msg && <Alert tone={msg.tone}>{msg.text}</Alert>}
          <Field label={t("settings.inventoryInterval")} hint={t("settings.inventoryHint")}>
            {(id) => <IntervalInput id={id} value={c.inventoryMinutes} onChange={(v) => setC({ ...c, inventoryMinutes: v })} />}
          </Field>
          <Field label={t("settings.patchScanInterval")} hint={t("settings.patchScanHint")}>
            {(id) => <IntervalInput id={id} value={c.patchScanMinutes} onChange={(v) => setC({ ...c, patchScanMinutes: v })} />}
          </Field>
          <Button type="submit" loading={busy}>
            {t("common.save")}
          </Button>
        </form>
      )}
    </Card>
  );
}
