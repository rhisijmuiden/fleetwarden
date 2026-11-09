import { useEffect, useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";
import { Network } from "lucide-react";
import { get, put, type ScanSettings } from "../../api";
import { IntervalInput } from "../../components/IntervalInput";
import { Updates } from "../../components/Updates";
import { Alert, Button, Card, Field, PageHeader, Spinner, Switch, useErrorText } from "../../components/ui";

type General = { publicUrl: string; detectedUrl: string };

export function Settings() {
  const { t } = useTranslation();
  const errorText = useErrorText();
  const [g, setG] = useState<General | null>(null);
  const [msg, setMsg] = useState<{ tone: "success" | "danger"; text: string } | null>(null);
  useEffect(() => {
    get<General>("/settings/general").then(setG).catch((e) => setMsg({ tone: "danger", text: errorText(e) }));
  }, []);

  return (
    <div className="space-y-6">
      <PageHeader title={t("settings.title")} description={t("settings.subtitle")} />
      <Card title={t("settings.general")}>
        {!g ? (
          !msg && <Spinner />
        ) : (
          <div className="flex flex-wrap items-center justify-between gap-4">
            <div className="min-w-0 text-sm">
              <p className="font-medium">{t("settings.publicUrl")}</p>
              <p className="font-mono break-all text-muted">{g.publicUrl || t("settings.publicUrlDetected", { url: g.detectedUrl })}</p>
              <p className="mt-1 text-xs text-muted">{t("settings.proxyHint")}</p>
            </div>
            <Link to="/admin/reverse-proxy" className="inline-flex h-9 items-center gap-2 rounded-md border border-line bg-surface px-4 text-sm font-medium hover:bg-surface-muted">
              <Network className="size-4" />
              {t("settings.openProxy")}
            </Link>
          </div>
        )}
        {msg && <Alert tone={msg.tone}>{msg.text}</Alert>}
      </Card>
      <AgentScans />
      <RemoteSettings />
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

type RemoteConfig = { direct: boolean; stunServers: string[] };

function RemoteSettings() {
  const { t } = useTranslation();
  const errorText = useErrorText();
  const [c, setC] = useState<RemoteConfig | null>(null);
  const [stun, setStun] = useState("");
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<{ tone: "success" | "danger"; text: string } | null>(null);
  useEffect(() => {
    get<RemoteConfig>("/settings/remote")
      .then((r) => {
        setC(r);
        setStun(r.stunServers.join("\n"));
      })
      .catch((e) => setMsg({ tone: "danger", text: errorText(e) }));
  }, []);

  const save = async (e: FormEvent) => {
    e.preventDefault();
    if (!c) return;
    setBusy(true);
    setMsg(null);
    try {
      const stunServers = stun
        .split(/[\s,]+/)
        .map((s) => s.trim())
        .filter(Boolean);
      const r = await put<RemoteConfig>("/settings/remote", { ...c, stunServers });
      setC(r);
      setStun(r.stunServers.join("\n"));
      setMsg({ tone: "success", text: `${t("common.saved")} ${t("settings.remote.applied")}` });
    } catch (err) {
      setMsg({ tone: "danger", text: errorText(err) });
    } finally {
      setBusy(false);
    }
  };

  return (
    <Card title={t("settings.remote.title")} description={t("settings.remote.subtitle")}>
      {!c ? (
        msg ? (
          <Alert tone={msg.tone}>{msg.text}</Alert>
        ) : (
          <Spinner />
        )
      ) : (
        <form onSubmit={save} className="max-w-xl space-y-4">
          {msg && <Alert tone={msg.tone}>{msg.text}</Alert>}
          <Switch checked={c.direct} onChange={(v) => setC({ ...c, direct: v })} label={t("settings.remote.direct")} description={t("settings.remote.directHint")} />
          <Field label={t("settings.remote.stun")} hint={t("settings.remote.stunHint")}>
            {(id) => (
              <textarea
                id={id}
                rows={3}
                disabled={!c.direct}
                value={stun}
                placeholder="stun:stun.example.org:3478"
                onChange={(e) => setStun(e.target.value)}
                className="w-full rounded-md border border-line bg-surface px-3 py-2 font-mono text-sm text-fg placeholder:text-muted/70 focus-visible:ring-2 focus-visible:ring-primary focus-visible:outline-none disabled:opacity-60"
              />
            )}
          </Field>
          <Button type="submit" loading={busy}>
            {t("common.save")}
          </Button>
        </form>
      )}
    </Card>
  );
}
