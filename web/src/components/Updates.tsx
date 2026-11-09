import { useCallback, useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { ArrowUpCircle, ExternalLink, Loader2, RefreshCw } from "lucide-react";
import { get, post, put, type UpdateView } from "../api";
import { Alert, Badge, Button, Field, Select, formatDate, useErrorText } from "./ui";

const running = ["downloading", "backup", "installing", "verifying", "rolling-back"];

// Update channel, update check and "Update now", with live progress while the
// updater runs. Used in the About dialog and on the settings page.
export function Updates({ compact }: { compact?: boolean }) {
  const { t, i18n } = useTranslation();
  const errorText = useErrorText();
  const [v, setV] = useState<UpdateView | null>(null);
  const [busy, setBusy] = useState<"check" | "channel" | "apply" | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [confirm, setConfirm] = useState(false);
  const startVersion = useRef<string | null>(null);

  const load = useCallback(() => get<UpdateView>("/system/update").then(setV), []);
  useEffect(() => {
    load().catch((e) => setError(errorText(e)));
  }, [load]);

  const inProgress = !!v && (v.pending || running.includes(v.status.state));
  // While an update runs the server restarts; keep polling and reload the UI
  // once the new version answers.
  useEffect(() => {
    if (!inProgress) return;
    const id = setInterval(() => {
      get<UpdateView>("/system/update")
        .then((next) => {
          setV(next);
          if (startVersion.current && next.current.version !== startVersion.current && next.status.state === "completed") {
            window.location.reload();
          }
        })
        .catch(() => undefined);
    }, 3000);
    return () => clearInterval(id);
  }, [inProgress]);

  const run = async (kind: "check" | "channel" | "apply", fn: () => Promise<UpdateView>) => {
    setBusy(kind);
    setError(null);
    try {
      setV(await fn());
    } catch (e) {
      setError(errorText(e));
    } finally {
      setBusy(null);
    }
  };

  if (!v) return error ? <Alert tone="danger">{error}</Alert> : <Loader2 className="size-5 animate-spin text-muted" />;
  const st = v.status;
  return (
    <div className="space-y-4">
      {error && <Alert tone="danger">{error}</Alert>}
      <div className={`grid gap-4 ${compact ? "" : "md:grid-cols-2"}`}>
        <Field label={t("updates.channel")} hint={t("updates.channelHint")}>
          {(id) => (
            <Select id={id} value={v.channel} disabled={busy !== null || inProgress} onChange={(e) => void run("channel", () => put<UpdateView>("/system/channel", { channel: e.target.value }))}>
              <option value="production">{t("info.channels.production")}</option>
              <option value="test">{t("info.channels.test")}</option>
            </Select>
          )}
        </Field>
        <div className="space-y-1.5">
          <p className="text-sm font-medium">{t("updates.latest")}</p>
          <div className="flex h-9 items-center gap-2 text-sm">
            {v.latest ? (
              <>
                <span className="font-medium">{v.latest.version}</span>
                {v.updateAvailable ? <Badge tone="primary">{t("updates.available")}</Badge> : <Badge tone="success">{t("updates.upToDate")}</Badge>}
                <a href={v.latest.url} target="_blank" rel="noreferrer" className="text-primary" aria-label={t("updates.releaseNotes")} title={t("updates.releaseNotes")}>
                  <ExternalLink className="size-3.5" />
                </a>
              </>
            ) : (
              <span className="text-muted">{v.checkError ? t("updates.checkFailed") : t("updates.none")}</span>
            )}
          </div>
        </div>
      </div>
      {v.checkError && <p className="text-xs break-all text-danger">{v.checkError}</p>}
      {!v.updateAvailable && v.latest && v.channel === "production" && v.current.version.includes("-test.") && (
        <Alert tone="info">{t("updates.switchToProductionHint")}</Alert>
      )}

      {inProgress && (
        <Alert tone="info" title={t("updates.inProgress", { version: st.toVersion || v.latest?.version })}>
          <span className="inline-flex items-center gap-2">
            <Loader2 className="size-4 animate-spin" />
            {st.state ? t(`updates.states.${st.state}`) : t("updates.states.pending")}
          </span>
        </Alert>
      )}
      {!inProgress && st.state && st.state !== "completed" && (
        <Alert tone="danger" title={t(`updates.states.${st.state}`)}>
          {st.message} · {formatDate(st.updatedAt, i18n.language)}
        </Alert>
      )}
      {!inProgress && st.state === "completed" && !compact && (
        <p className="text-xs text-muted">
          {t("updates.lastUpdate", { from: st.fromVersion, to: st.toVersion, when: formatDate(st.updatedAt, i18n.language) })}
        </p>
      )}

      <div className="flex flex-wrap gap-2">
        <Button variant="secondary" icon={<RefreshCw className="size-4" />} loading={busy === "check"} disabled={inProgress} onClick={() => void run("check", () => post<UpdateView>("/system/update/check"))}>
          {t("info.checkUpdates")}
        </Button>
        {v.updateAvailable && v.latest && !confirm && (
          <Button icon={<ArrowUpCircle className="size-4" />} disabled={inProgress} onClick={() => setConfirm(true)}>
            {t("updates.updateNow", { version: v.latest.version })}
          </Button>
        )}
      </div>
      {confirm && v.latest && (
        <div className="space-y-3 rounded-md border border-line bg-surface-muted/60 p-4">
          <p className="text-sm">{t("updates.confirm", { version: v.latest.version })}</p>
          <div className="flex gap-2">
            <Button
              loading={busy === "apply"}
              onClick={() => {
                startVersion.current = v.current.version;
                setConfirm(false);
                void run("apply", () => post<UpdateView>("/system/update/apply", { version: v.latest!.version }));
              }}
            >
              {t("updates.confirmButton")}
            </Button>
            <Button variant="secondary" onClick={() => setConfirm(false)}>
              {t("common.cancel")}
            </Button>
          </div>
        </div>
      )}
    </div>
  );
}
