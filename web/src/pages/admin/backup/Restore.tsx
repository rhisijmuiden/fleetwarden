import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { History as HistoryIcon, Loader2, RotateCcw, Search } from "lucide-react";
import { del, get, post, type BackupLocation, type RestoreView, type Snapshot, type SnapshotDetails } from "../../../api";
import { Alert, Badge, Button, Card, Field, Input, Modal, Select, formatDate, useErrorText } from "../../../components/ui";
import { LocationForm, draftBody, emptyDraft, type LocationDraft } from "./LocationForm";
import { formatBytes } from "./util";

type Source = { locationId?: number; location?: ReturnType<typeof draftBody>; passphrase?: string };

export function Restore({ locations, restore, onRestoreChange }: { locations: BackupLocation[]; restore: RestoreView | null; onRestoreChange: (r: RestoreView) => void }) {
  const { t, i18n } = useTranslation();
  const errorText = useErrorText();
  const [choice, setChoice] = useState<string>(locations[0] ? String(locations[0].id) : "other");
  const [other, setOther] = useState<LocationDraft>({ ...emptyDraft(), path: "fleetwarden" });
  const [passphrase, setPassphrase] = useState("");
  const [snaps, setSnaps] = useState<Snapshot[] | null>(null);
  const [source, setSource] = useState<Source | null>(null);
  const [selected, setSelected] = useState<SnapshotDetails | null>(null);
  const [busy, setBusy] = useState<"find" | "details" | "restore" | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [confirm, setConfirm] = useState("");
  const [confirmOpen, setConfirmOpen] = useState(false);

  useEffect(() => {
    if (choice !== "other" && !locations.some((l) => String(l.id) === choice)) setChoice(locations[0] ? String(locations[0].id) : "other");
  }, [locations]);

  const currentSource = (): Source => (choice === "other" ? { location: draftBody(other), passphrase } : { locationId: Number(choice) });

  const find = async () => {
    setBusy("find");
    setError(null);
    setSnaps(null);
    setSelected(null);
    const src = currentSource();
    try {
      setSnaps(await post<Snapshot[]>("/backup/snapshots", src));
      setSource(src);
    } catch (e) {
      setError(errorText(e));
    } finally {
      setBusy(null);
    }
  };

  const pick = async (s: Snapshot) => {
    if (!source) return;
    setBusy("details");
    setError(null);
    try {
      setSelected(await post<SnapshotDetails>("/backup/snapshots/details", { ...source, chain: s.chain, snapshot: s.id }));
    } catch (e) {
      setError(errorText(e));
    } finally {
      setBusy(null);
    }
  };

  const start = async () => {
    if (!source || !selected) return;
    setBusy("restore");
    setError(null);
    try {
      onRestoreChange(await post<RestoreView>("/backup/restore", { ...source, chain: selected.chain, snapshot: selected.id, confirm }));
      setConfirmOpen(false);
    } catch (e) {
      setError(errorText(e));
      setConfirmOpen(false);
    } finally {
      setBusy(null);
      setConfirm("");
    }
  };

  const active = restore && (restore.state === "preparing" || restore.state === "requested");
  return (
    <div className="space-y-6">
      {restore && <RestoreProgress restore={restore} onChange={onRestoreChange} />}
      <Card title={t("backup.restore.title")} description={t("backup.restore.subtitle")}>
        <div className="space-y-4">
          {error && <Alert tone="danger">{error}</Alert>}
          <Field label={t("backup.restore.source")}>
            {(id) => (
              <Select
                id={id}
                value={choice}
                onChange={(e) => {
                  setChoice(e.target.value);
                  setSnaps(null);
                  setSelected(null);
                }}
              >
                {locations.map((l) => (
                  <option key={l.id} value={l.id}>
                    {l.name}
                  </option>
                ))}
                <option value="other">{t("backup.restore.other")}</option>
              </Select>
            )}
          </Field>
          {choice === "other" && (
            <div className="space-y-4 rounded-md border border-line bg-surface-muted/40 p-4">
              <p className="text-sm text-muted">{t("backup.restore.otherHint")}</p>
              <LocationForm draft={other} onChange={setOther} bare />
              <Field label={t("backup.pass.label")} hint={t("backup.restore.passHint")}>
                {(id) => <Input id={id} type="password" autoComplete="off" value={passphrase} onChange={(e) => setPassphrase(e.target.value)} />}
              </Field>
            </div>
          )}
          <Button icon={<Search className="size-4" />} loading={busy === "find"} disabled={!!active} onClick={() => void find()}>
            {t("backup.restore.find")}
          </Button>
        </div>
      </Card>

      {snaps && (
        <Card title={t("backup.restore.snapshots")} description={t("backup.restore.snapshotsHint")}>
          {snaps.length === 0 ? (
            <p className="py-6 text-center text-sm text-muted">{t("backup.restore.none")}</p>
          ) : (
            <div className="-mx-5 -my-5 max-h-[420px] overflow-auto">
              <table className="w-full min-w-[560px] text-sm">
                <thead className="sticky top-0 border-b border-line bg-surface text-left text-xs text-muted uppercase">
                  <tr>
                    <th className="px-5 py-3 font-medium">{t("backup.col.time")}</th>
                    <th className="px-3 py-3 font-medium">{t("backup.col.kind")}</th>
                    <th className="px-3 py-3 font-medium">{t("backup.col.server")}</th>
                    <th className="px-5 py-3" />
                  </tr>
                </thead>
                <tbody className="divide-y divide-line">
                  {snaps.map((s) => {
                    const isSel = selected?.chain === s.chain && selected?.id === s.id;
                    return (
                      <tr key={s.chain + s.id} className={isSel ? "bg-primary/5" : ""}>
                        <td className="px-5 py-3 whitespace-nowrap">{formatDate(s.time, i18n.language)}</td>
                        <td className="px-3 py-3">
                          <Badge tone={s.kind === "full" ? "primary" : "neutral"}>{t(`backup.kinds.${s.kind}`)}</Badge>
                        </td>
                        <td className="px-3 py-3 font-mono text-xs text-muted">{s.instance}</td>
                        <td className="px-5 py-3 text-right">
                          <Button size="sm" variant={isSel ? "primary" : "secondary"} loading={busy === "details" && !selected} onClick={() => void pick(s)}>
                            {t("backup.restore.choose")}
                          </Button>
                        </td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>
          )}
        </Card>
      )}

      {selected && (
        <Card title={t("backup.restore.selected")}>
          <dl className="grid gap-x-6 gap-y-3 text-sm sm:grid-cols-2 lg:grid-cols-4">
            <div>
              <dt className="text-xs text-muted">{t("backup.col.time")}</dt>
              <dd>{formatDate(selected.time, i18n.language)}</dd>
            </div>
            <div>
              <dt className="text-xs text-muted">{t("backup.col.kind")}</dt>
              <dd>{t(`backup.kinds.${selected.kind}`)}</dd>
            </div>
            <div>
              <dt className="text-xs text-muted">{t("backup.col.version")}</dt>
              <dd>{selected.version || "–"}</dd>
            </div>
            <div>
              <dt className="text-xs text-muted">{t("backup.col.size")}</dt>
              <dd>
                {formatBytes(selected.size, i18n.language)} · {selected.host}
              </dd>
            </div>
          </dl>
          <div className="mt-5 space-y-3 border-t border-line pt-5">
            {!selected.restorable && <Alert tone="danger">{t("backup.restore.newer", { version: selected.version })}</Alert>}
            <Button variant="danger" icon={<RotateCcw className="size-4" />} disabled={!selected.restorable || !!active} onClick={() => setConfirmOpen(true)}>
              {t("backup.restore.start")}
            </Button>
          </div>
        </Card>
      )}

      <Modal
        open={confirmOpen}
        onClose={() => setConfirmOpen(false)}
        title={t("backup.restore.confirmTitle")}
        footer={
          <>
            <Button variant="secondary" onClick={() => setConfirmOpen(false)}>
              {t("common.cancel")}
            </Button>
            <Button variant="danger" loading={busy === "restore"} disabled={confirm !== "RESTORE"} onClick={() => void start()}>
              {t("backup.restore.start")}
            </Button>
          </>
        }
      >
        <Alert tone="warning">{t("backup.restore.confirmBody")}</Alert>
        <Field label={t("backup.restore.confirmType")}>{(id) => <Input id={id} value={confirm} onChange={(e) => setConfirm(e.target.value)} autoComplete="off" placeholder="RESTORE" />}</Field>
      </Modal>
    </div>
  );
}

// Shows a restore in progress, then waits for the server to come back.
function RestoreProgress({ restore, onChange }: { restore: RestoreView; onChange: (r: RestoreView) => void }) {
  const { t, i18n } = useTranslation();
  const errorText = useErrorText();
  const [waited, setWaited] = useState(0);
  const wentDown = useRef(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (restore.state === "preparing") {
      const id = setInterval(() => get<RestoreView>("/backup/restore").then(onChange).catch(() => undefined), 1500);
      return () => clearInterval(id);
    }
    if (restore.state === "requested") {
      // The helper stops and starts the server; when it is back, everyone signs in again.
      const id = setInterval(() => {
        setWaited((w) => w + 2);
        fetch("/healthz", { cache: "no-store" })
          .then((r) => {
            if (!r.ok) throw new Error("down");
            if (wentDown.current) window.location.href = "/login";
            return get<RestoreView>("/backup/restore").then(onChange);
          })
          .catch(() => {
            wentDown.current = true;
          });
      }, 2000);
      return () => clearInterval(id);
    }
  }, [restore.state]);

  const cancel = async () => {
    try {
      await del("/backup/restore");
      onChange(await get<RestoreView>("/backup/restore"));
    } catch (e) {
      setError(errorText(e));
    }
  };

  if (restore.state === "preparing") {
    const pct = restore.total ? Math.min(100, Math.round((restore.done / restore.total) * 100)) : 0;
    return (
      <Alert tone="info" title={t("backup.restore.preparing")}>
        <div className="mt-2 h-2 w-full max-w-md overflow-hidden rounded-full bg-line">
          <div className="h-full bg-primary transition-all" style={{ width: `${pct}%` }} />
        </div>
        <p className="mt-1 text-xs text-muted">
          {formatBytes(restore.done, i18n.language)} / {formatBytes(restore.total, i18n.language)}
        </p>
      </Alert>
    );
  }
  if (restore.state === "requested") {
    return (
      <Alert tone="warning" title={t("backup.restore.applying")}>
        <p className="flex items-center gap-2">
          <Loader2 className="size-4 animate-spin" /> {t("backup.restore.applyingBody")}
        </p>
        {waited > 60 && (
          <div className="mt-2 space-y-2">
            <p className="text-xs">{t("backup.restore.stuck")}</p>
            <code className="block rounded bg-surface-muted px-2 py-1 font-mono text-xs">sudo fleetwarden-server restore-apply</code>
            <Button size="sm" variant="secondary" onClick={() => void cancel()}>
              {t("backup.restore.cancel")}
            </Button>
          </div>
        )}
        {error && <p className="mt-1 text-danger">{error}</p>}
      </Alert>
    );
  }
  if (restore.state === "failed") return <Alert tone="danger" title={t("backup.restore.prepareFailed")}>{restore.message}</Alert>;
  if (restore.last) {
    const ok = restore.last.state === "completed";
    return (
      <Alert tone={ok ? "success" : "danger"} title={ok ? t("backup.restore.lastOk") : t("backup.restore.lastFailed")}>
        <span className="inline-flex items-center gap-1.5">
          <HistoryIcon className="size-3.5" /> {formatDate(restore.last.updatedAt, i18n.language)} · {restore.last.message}
        </span>
      </Alert>
    );
  }
  return null;
}
