import { useCallback, useEffect, useRef, useState, type FormEvent } from "react";
import { Link } from "react-router";
import { useTranslation } from "react-i18next";
import { CloudDownload, FastForward, Pause, Play, RotateCcw, Rocket, Trash2, Upload, XCircle } from "lucide-react";
import { api, del, get, post, put, type AgentRelease, type AgentUpdateSettings, type AgentUpdates as Data, type Rollout } from "../../api";
import { Alert, Badge, Button, Card, Field, Input, Modal, PageHeader, Spinner, Switch, formatDate, useErrorText } from "../../components/ui";

const statusTone = { active: "primary", paused: "warning", completed: "success", cancelled: "neutral", superseded: "neutral" } as const;

// Agent updates: releases from GitHub or uploads, and their rollouts ring by ring.
export function AgentUpdates() {
  const { t, i18n } = useTranslation();
  const errorText = useErrorText();
  const [data, setData] = useState<Data | null>(null);
  const [msg, setMsg] = useState<{ tone: "success" | "danger" | "warning"; text: string } | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const [starting, setStarting] = useState<AgentRelease | null>(null);
  const [deleting, setDeleting] = useState<AgentRelease | null>(null);
  const fileRef = useRef<HTMLInputElement>(null);

  const load = useCallback(() => {
    get<Data>("/agent/updates")
      .then(setData)
      .catch((e) => setMsg({ tone: "danger", text: errorText(e) }));
  }, []);
  useEffect(() => {
    load();
    const id = setInterval(load, 15000);
    return () => clearInterval(id);
  }, [load]);

  const run = async (key: string, fn: () => Promise<Data>, ok?: string) => {
    setBusy(key);
    setMsg(null);
    try {
      const d = await fn();
      setData(d);
      if (ok) setMsg({ tone: "success", text: ok });
    } catch (e) {
      setMsg({ tone: "danger", text: errorText(e) });
    } finally {
      setBusy(null);
    }
  };

  const check = () =>
    run("check", async () => {
      const d = await post<Data>("/agent/updates/check");
      if (d.settings.lastError) setMsg({ tone: "warning", text: t("agentUpdates.checkFailed", { error: d.settings.lastError }) });
      else setMsg({ tone: "success", text: t("agentUpdates.checked") });
      return d;
    });

  const upload = (file: File) => {
    const form = new FormData();
    form.append("file", file);
    run("upload", () => api<Data>("POST", "/agent/releases", form), t("agentUpdates.uploaded", { name: file.name }));
  };

  const action = (r: Rollout, a: string) => run(`${r.id}-${a}`, () => post<Data>(`/agent/rollouts/${r.id}/${a}`));

  const lang = i18n.language;
  const open = data?.rollouts.filter((r) => r.status === "active" || r.status === "paused") ?? [];
  const history = data?.rollouts.filter((r) => r.status !== "active" && r.status !== "paused").slice(0, 8) ?? [];

  return (
    <div className="space-y-6">
      <PageHeader
        title={t("agentUpdates.title")}
        description={t("agentUpdates.subtitle")}
        actions={
          <>
            <input
              ref={fileRef}
              type="file"
              accept=".msi,.deb"
              className="hidden"
              onChange={(e) => {
                const f = e.target.files?.[0];
                e.target.value = "";
                if (f) upload(f);
              }}
            />
            <Button variant="secondary" icon={<Upload className="size-4" />} loading={busy === "upload"} onClick={() => fileRef.current?.click()}>
              {t("agentUpdates.upload")}
            </Button>
            <Button icon={<CloudDownload className="size-4" />} loading={busy === "check"} onClick={check}>
              {t("agentUpdates.checkNow")}
            </Button>
          </>
        }
      />
      {msg && <Alert tone={msg.tone}>{msg.text}</Alert>}
      {!data ? (
        <Spinner />
      ) : (
        <>
          <Card title={t("agentUpdates.rollouts")} description={t("agentUpdates.rolloutsHint")}>
            {open.length === 0 ? (
              <p className="text-sm text-muted">{t("agentUpdates.noOpenRollout")}</p>
            ) : (
              <div className="space-y-6">
                {open.map((r) => (
                  <RolloutPanel key={r.id} r={r} busy={busy} onAction={action} lang={lang} />
                ))}
              </div>
            )}
            {history.length > 0 && (
              <details className="mt-5 text-sm">
                <summary className="cursor-pointer text-muted select-none">{t("agentUpdates.history")}</summary>
                <ul className="mt-3 divide-y divide-line">
                  {history.map((r) => (
                    <li key={r.id} className="flex flex-wrap items-center gap-x-3 gap-y-1 py-2">
                      <span className="font-medium">{r.version}</span>
                      <Badge tone={statusTone[r.status]}>{t(`agentUpdates.status.${r.status}`)}</Badge>
                      <span className="text-xs text-muted">
                        {formatDate(r.createdAt, lang)} · {r.automatic ? t("agentUpdates.automatic") : r.createdBy}
                      </span>
                      {r.message && <span className="text-xs text-muted">{r.message}</span>}
                    </li>
                  ))}
                </ul>
              </details>
            )}
          </Card>

          {data.problems.length > 0 && (
            <Card title={t("agentUpdates.problems")} description={t("agentUpdates.problemsHint")}>
              <ul className="divide-y divide-line text-sm">
                {data.problems.map((p) => (
                  <li key={p.deviceId} className="flex flex-wrap items-baseline gap-x-3 gap-y-1 py-2">
                    <Link to={`/devices/${p.deviceId}`} className="font-medium hover:text-primary">
                      {p.hostname}
                    </Link>
                    <span className="text-muted">
                      {p.from} → {p.version}
                    </span>
                    <Badge tone="danger">{t(`agentUpdates.state.${p.state}`)}</Badge>
                    <span className="basis-full text-xs text-muted">{p.message}</span>
                  </li>
                ))}
              </ul>
            </Card>
          )}

          <Card title={t("agentUpdates.releases")} description={t("agentUpdates.releasesHint", { channel: t(`info.channels.${data.channel}`) })}>
            {data.releases.length === 0 ? (
              <p className="text-sm text-muted">{t("agentUpdates.noReleases")}</p>
            ) : (
              <div className="-mx-5 -my-5 overflow-x-auto">
                <table className="w-full min-w-[720px] text-sm">
                  <thead className="border-b border-line text-left text-xs text-muted uppercase">
                    <tr>
                      <th className="px-5 py-3 font-medium">{t("agentUpdates.version")}</th>
                      <th className="px-3 py-3 font-medium">{t("agentUpdates.source")}</th>
                      <th className="px-3 py-3 font-medium">{t("agentUpdates.packages")}</th>
                      <th className="px-3 py-3 font-medium">{t("agentUpdates.devices")}</th>
                      <th className="px-5 py-3 text-right font-medium">
                        <span className="sr-only">{t("common.actions")}</span>
                      </th>
                    </tr>
                  </thead>
                  <tbody className="divide-y divide-line">
                    {data.releases.map((rel) => (
                      <tr key={rel.version}>
                        <td className="px-5 py-3">
                          <div className="flex items-center gap-2 font-medium">
                            {rel.version}
                            <Badge tone={rel.prerelease ? "warning" : "success"}>{t(`info.channels.${rel.prerelease ? "test" : "production"}`)}</Badge>
                          </div>
                          <div className="mt-0.5 text-xs text-muted">{formatDate(rel.added, lang)}</div>
                        </td>
                        <td className="px-3 py-3">{t(`agentUpdates.sources.${rel.source}`)}</td>
                        <td className="px-3 py-3">
                          <div className="flex flex-wrap gap-1">
                            {rel.packages.map((p) => (
                              <span key={p.name} title={`${p.name}\nSHA-256 ${p.sha256}`} className="rounded bg-surface-muted px-1.5 py-0.5 text-xs">
                                {p.kind === "msi" ? "Windows" : `Linux ${p.arch}`}
                              </span>
                            ))}
                          </div>
                        </td>
                        <td className="px-3 py-3">{rel.devices}</td>
                        <td className="px-5 py-3">
                          <div className="flex justify-end gap-1">
                            <Button size="sm" variant="secondary" icon={<Rocket className="size-4" />} onClick={() => setStarting(rel)}>
                              {t("agentUpdates.startRollout")}
                            </Button>
                            {rel.deletable && (
                              <Button size="sm" variant="ghost" aria-label={t("common.delete")} icon={<Trash2 className="size-4" />} onClick={() => setDeleting(rel)} />
                            )}
                          </div>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </Card>

          <SettingsCard settings={data.settings} lang={lang} onSaved={setData} />
        </>
      )}

      <StartRollout
        release={starting}
        defaults={data?.settings}
        onClose={() => setStarting(null)}
        onStarted={(d) => {
          setData(d);
          setStarting(null);
          setMsg({ tone: "success", text: t("agentUpdates.started") });
        }}
      />
      <Modal
        open={!!deleting}
        onClose={() => setDeleting(null)}
        title={t("agentUpdates.deleteTitle")}
        footer={
          <>
            <Button variant="secondary" onClick={() => setDeleting(null)}>
              {t("common.cancel")}
            </Button>
            <Button
              variant="danger"
              loading={busy === "delete"}
              onClick={() => {
                const v = deleting!.version;
                setDeleting(null);
                run("delete", () => del<Data>(`/agent/releases/${encodeURIComponent(v)}`));
              }}
            >
              {t("common.delete")}
            </Button>
          </>
        }
      >
        <p className="text-sm">{t("agentUpdates.deleteConfirm", { version: deleting?.version })}</p>
      </Modal>
    </div>
  );
}

function RolloutPanel({ r, busy, onAction, lang }: { r: Rollout; busy: string | null; onAction: (r: Rollout, a: string) => void; lang: string }) {
  const { t } = useTranslation();
  const btn = (a: string, icon: React.ReactNode, variant: "secondary" | "ghost" = "secondary") => (
    <Button size="sm" variant={variant} icon={icon} loading={busy === `${r.id}-${a}`} onClick={() => onAction(r, a)}>
      {t(`agentUpdates.actions.${a}`)}
    </Button>
  );
  const failed = r.rings.reduce((n, x) => n + x.failed, 0);
  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center gap-3">
        <span className="text-lg font-semibold">{r.version}</span>
        <Badge tone={statusTone[r.status]}>{t(`agentUpdates.status.${r.status}`)}</Badge>
        <span className="text-xs text-muted">
          {r.automatic ? t("agentUpdates.automatic") : r.createdBy} · {formatDate(r.createdAt, lang)} ·{" "}
          {t("agentUpdates.soakSummary", { hours: r.soakHours, failures: r.maxFailures })}
        </span>
        <div className="ml-auto flex flex-wrap gap-1">
          {r.status === "active" && btn("pause", <Pause className="size-4" />)}
          {r.status === "paused" && btn("resume", <Play className="size-4" />)}
          {r.status === "active" && btn("advance", <FastForward className="size-4" />)}
          {failed > 0 && btn("retry", <RotateCcw className="size-4" />)}
          {btn("cancel", <XCircle className="size-4" />, "ghost")}
        </div>
      </div>
      {r.message && <Alert tone={r.status === "paused" ? "warning" : "info"}>{r.message}</Alert>}
      <ol className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
        {r.rings.map((ring) => {
          const current = ring.ring === r.ring && r.status === "active";
          const reached = ring.ring <= r.ring;
          const pct = ring.devices ? Math.round((ring.updated / ring.devices) * 100) : 0;
          return (
            <li key={ring.ring} className={`rounded-lg border p-3 ${current ? "border-primary ring-1 ring-primary" : "border-line"} ${reached ? "" : "opacity-60"}`}>
              <div className="flex items-center justify-between text-sm font-medium">
                {t(`groups.rings.${ring.ring}`)}
                {current && r.soakEndsAt && ring.devices > 0 && (
                  <span className="text-xs font-normal text-muted">{t("agentUpdates.nextRing", { date: formatDate(r.soakEndsAt, lang) })}</span>
                )}
              </div>
              <div className="mt-1 truncate text-xs text-muted" title={ring.groups.join(", ")}>
                {ring.groups.length ? ring.groups.join(", ") : t("agentUpdates.noGroups")}
              </div>
              <div className="mt-3 h-2 overflow-hidden rounded-full bg-surface-muted" role="progressbar" aria-valuenow={pct} aria-valuemin={0} aria-valuemax={100}>
                <div className="h-full bg-success" style={{ width: `${pct}%` }} />
              </div>
              <div className="mt-2 flex flex-wrap gap-x-3 text-xs text-muted">
                <span>{t("agentUpdates.updatedOf", { updated: ring.updated, total: ring.devices })}</span>
                {ring.inProgress > 0 && <span>{t("agentUpdates.inProgress", { count: ring.inProgress })}</span>}
                {ring.failed > 0 && <span className="text-danger">{t("agentUpdates.failed", { count: ring.failed })}</span>}
              </div>
            </li>
          );
        })}
      </ol>
    </div>
  );
}

function StartRollout({ release, defaults, onClose, onStarted }: { release: AgentRelease | null; defaults?: AgentUpdateSettings; onClose: () => void; onStarted: (d: Data) => void }) {
  const { t } = useTranslation();
  const errorText = useErrorText();
  const [soak, setSoak] = useState("24");
  const [failures, setFailures] = useState("2");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => {
    if (!release) return;
    setSoak(String(defaults?.soakHours ?? 24));
    setFailures(String(defaults?.maxFailures ?? 2));
    setError(null);
  }, [release]);
  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    try {
      onStarted(await post<Data>("/agent/rollouts", { version: release!.version, soakHours: Number(soak), maxFailures: Number(failures) }));
    } catch (err) {
      setError(errorText(err));
    } finally {
      setBusy(false);
    }
  };
  return (
    <Modal
      open={!!release}
      onClose={onClose}
      title={t("agentUpdates.startTitle", { version: release?.version })}
      footer={
        <>
          <Button variant="secondary" onClick={onClose}>
            {t("common.cancel")}
          </Button>
          <Button type="submit" form="start-rollout" loading={busy}>
            {t("agentUpdates.startRollout")}
          </Button>
        </>
      }
    >
      <form id="start-rollout" onSubmit={submit} className="space-y-4">
        {error && <Alert tone="danger">{error}</Alert>}
        <p className="text-sm text-muted">{t("agentUpdates.startHint")}</p>
        <div className="grid gap-4 sm:grid-cols-2">
          <Field label={t("agentUpdates.soakHours")} hint={t("agentUpdates.soakHint")}>
            {(id) => <Input id={id} type="number" min={0} max={720} value={soak} onChange={(e) => setSoak(e.target.value)} required />}
          </Field>
          <Field label={t("agentUpdates.maxFailures")} hint={t("agentUpdates.maxFailuresHint")}>
            {(id) => <Input id={id} type="number" min={0} value={failures} onChange={(e) => setFailures(e.target.value)} required />}
          </Field>
        </div>
      </form>
    </Modal>
  );
}

function SettingsCard({ settings, lang, onSaved }: { settings: AgentUpdateSettings; lang: string; onSaved: (d: Data) => void }) {
  const { t } = useTranslation();
  const errorText = useErrorText();
  const [s, setS] = useState(settings);
  const [msg, setMsg] = useState<{ tone: "success" | "danger"; text: string } | null>(null);
  const [busy, setBusy] = useState(false);
  useEffect(() => setS(settings), [settings.lastCheck]);
  const save = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setMsg(null);
    try {
      onSaved(await put<Data>("/agent/updates/settings", { ...s, soakHours: Number(s.soakHours), maxFailures: Number(s.maxFailures), keepReleases: Number(s.keepReleases) }));
      setMsg({ tone: "success", text: t("common.saved") });
    } catch (err) {
      setMsg({ tone: "danger", text: errorText(err) });
    } finally {
      setBusy(false);
    }
  };
  const lastCheck = settings.lastCheck && !settings.lastCheck.startsWith("0001") ? formatDate(settings.lastCheck, lang) : t("agentUpdates.never");
  return (
    <Card title={t("agentUpdates.settings")}>
      <form onSubmit={save} className="max-w-2xl space-y-4">
        {msg && <Alert tone={msg.tone}>{msg.text}</Alert>}
        <Switch checked={s.autoDownload} onChange={(v) => setS({ ...s, autoDownload: v })} label={t("agentUpdates.autoDownload")} description={t("agentUpdates.autoDownloadHint")} />
        <Switch checked={s.autoRollout} onChange={(v) => setS({ ...s, autoRollout: v })} label={t("agentUpdates.autoRollout")} description={t("agentUpdates.autoRolloutHint")} />
        <div className="grid gap-4 sm:grid-cols-3">
          <Field label={t("agentUpdates.soakHours")} hint={t("agentUpdates.soakHint")}>
            {(id) => <Input id={id} type="number" min={0} max={720} value={s.soakHours} onChange={(e) => setS({ ...s, soakHours: Number(e.target.value) })} />}
          </Field>
          <Field label={t("agentUpdates.maxFailures")} hint={t("agentUpdates.maxFailuresHint")}>
            {(id) => <Input id={id} type="number" min={0} value={s.maxFailures} onChange={(e) => setS({ ...s, maxFailures: Number(e.target.value) })} />}
          </Field>
          <Field label={t("agentUpdates.keepReleases")} hint={t("agentUpdates.keepReleasesHint")}>
            {(id) => <Input id={id} type="number" min={1} max={50} value={s.keepReleases} onChange={(e) => setS({ ...s, keepReleases: Number(e.target.value) })} />}
          </Field>
        </div>
        <p className="text-xs text-muted">
          {t("agentUpdates.lastCheck", { date: lastCheck })}
          {settings.lastError && <span className="text-danger"> · {settings.lastError}</span>}
        </p>
        <Button type="submit" loading={busy}>
          {t("common.save")}
        </Button>
      </form>
    </Card>
  );
}
