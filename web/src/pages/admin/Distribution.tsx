import { useCallback, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { ArrowUpCircle, ChevronRight, HardDriveDownload, KeyRound, Laptop, Pencil, Plus, Power, Server, Trash2, Warehouse } from "lucide-react";
import { del, get, post, put, type DistributionList, type DistributionServer, type DistributionToken } from "../../api";
import { useAuth } from "../../auth";
import { useGroups } from "../../components/GroupScope";
import { Alert, Badge, Button, Card, CopyField, Field, Input, Modal, PageHeader, Spinner, formatDate, useErrorText } from "../../components/ui";
import { formatBytes } from "./backup/util";

const stateTone = { online: "success", offline: "danger", pending: "warning", disabled: "neutral" } as const;

// Admin > Distribution servers: caches in branch offices that keep approved
// software, drivers and agent updates close to the agents.
export function Distribution() {
  const { t, i18n } = useTranslation();
  const errorText = useErrorText();
  const [data, setData] = useState<DistributionList | null>(null);
  const [msg, setMsg] = useState<{ tone: "success" | "danger" | "warning"; text: string } | null>(null);
  const [editing, setEditing] = useState<DistributionServer | "new" | null>(null);
  const [install, setInstall] = useState<DistributionToken | null>(null);
  const [deleting, setDeleting] = useState<DistributionServer | null>(null);
  const [busy, setBusy] = useState<string | null>(null);

  const load = useCallback(() => {
    get<DistributionList>("/distribution")
      .then(setData)
      .catch((e) => setMsg({ tone: "danger", text: errorText(e) }));
  }, []);
  useEffect(() => {
    load();
    const id = setInterval(load, 5000);
    return () => clearInterval(id);
  }, [load]);

  const act = async (key: string, fn: () => Promise<unknown>, ok?: string) => {
    setBusy(key);
    try {
      await fn();
      if (ok) setMsg({ tone: "success", text: ok });
      load();
    } catch (e) {
      setMsg({ tone: "danger", text: errorText(e) });
    } finally {
      setBusy(null);
    }
  };

  const toggle = (d: DistributionServer) =>
    act(`toggle-${d.id}`, () => put(`/distribution/${d.id}`, { name: d.name, url: d.url, networks: d.networks, groupIds: d.groupIds, bandwidthKbps: d.bandwidthKbps, enabled: !d.enabled }));
  const newToken = (d: DistributionServer) =>
    act(`token-${d.id}`, async () => setInstall(await post<DistributionToken>(`/distribution/${d.id}/token`)));
  const update = (d: DistributionServer) =>
    act(`update-${d.id}`, () => post(`/distribution/${d.id}/update`), t("distribution.updateRequested", { name: d.name, version: data?.version ?? "" }));
  const remove = () =>
    deleting &&
    act(
      "delete",
      async () => {
        await del(`/distribution/${deleting.id}`);
        setDeleting(null);
      },
      t("distribution.deleted", { name: deleting.name }),
    );

  return (
    <div className="space-y-6">
      <PageHeader
        title={t("distribution.title")}
        description={t("distribution.subtitle")}
        actions={
          <Button icon={<Plus className="size-4" />} onClick={() => setEditing("new")}>
            {t("distribution.add")}
          </Button>
        }
      />
      {msg && <Alert tone={msg.tone}>{msg.text}</Alert>}
      <Flow />
      {!data ? (
        <Spinner />
      ) : (
        <>
          <Card title={t("distribution.content.title")} description={t("distribution.content.subtitle")}>
            <dl className="grid gap-4 sm:grid-cols-3">
              <Stat label={t("distribution.content.files")} value={String(data.manifestFiles)} />
              <Stat label={t("distribution.content.size")} value={formatBytes(data.manifestBytes, i18n.language)} />
              <Stat label={t("distribution.content.servers")} value={String(data.servers.filter((d) => d.state === "online").length) + " / " + data.servers.length} />
            </dl>
          </Card>
          {data.servers.length === 0 ? (
            <Empty onAdd={() => setEditing("new")} port={data.port} />
          ) : (
            <div className="space-y-4">
              {data.servers.map((d) => (
                <DistCard
                  key={d.id}
                  d={d}
                  list={data}
                  busy={busy}
                  onEdit={() => setEditing(d)}
                  onToggle={() => toggle(d)}
                  onToken={() => newToken(d)}
                  onUpdate={() => update(d)}
                  onDelete={() => setDeleting(d)}
                />
              ))}
            </div>
          )}
        </>
      )}

      <DistForm
        open={editing !== null}
        server={editing === "new" ? null : editing}
        defaultPort={data?.port ?? 8445}
        onClose={() => setEditing(null)}
        onSaved={(res) => {
          setEditing(null);
          if (res) setInstall(res);
          else setMsg({ tone: "success", text: t("common.saved") });
          load();
        }}
      />
      <InstallModal info={install} onClose={() => setInstall(null)} />
      <Modal
        open={!!deleting}
        onClose={() => setDeleting(null)}
        title={t("distribution.deleteTitle", { name: deleting?.name ?? "" })}
        footer={
          <>
            <Button variant="secondary" onClick={() => setDeleting(null)}>
              {t("common.cancel")}
            </Button>
            <Button variant="danger" loading={busy === "delete"} onClick={remove}>
              {t("common.delete")}
            </Button>
          </>
        }
      >
        <p className="text-sm">{t("distribution.deleteText")}</p>
      </Modal>
    </div>
  );
}

function Stat({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <dt className="text-xs text-muted">{label}</dt>
      <dd className="mt-0.5 text-xl font-semibold">{value}</dd>
    </div>
  );
}

// How files travel: once over the slow link, then locally to many agents.
function Flow() {
  const { t } = useTranslation();
  const node = (icon: React.ReactNode, title: string, hint: string, tone: string) => (
    <div className={`flex min-w-0 flex-1 items-center gap-3 rounded-lg border px-4 py-3 ${tone}`}>
      <span className="grid size-9 shrink-0 place-items-center rounded-md bg-surface">{icon}</span>
      <span className="min-w-0">
        <span className="block text-sm font-semibold">{title}</span>
        <span className="block text-xs text-muted">{hint}</span>
      </span>
    </div>
  );
  const arrow = (label: string) => (
    <div className="flex shrink-0 flex-col items-center justify-center gap-1 px-1 text-[11px] text-muted md:w-36">
      <span className="flex w-full items-center text-primary" aria-hidden>
        <span className="h-0.5 flex-1 rounded-full bg-primary/40" />
        <ChevronRight className="-ml-1.5 size-4 shrink-0" />
      </span>
      <span className="text-center">{label}</span>
    </div>
  );
  return (
    <section className="rounded-lg border border-line bg-surface p-4">
      <div className="flex flex-col items-stretch gap-2 md:flex-row md:items-center">
        {node(<Server className="size-5 text-primary" />, t("distribution.flow.server"), t("distribution.flow.serverHint"), "border-line bg-surface-muted/40")}
        {arrow(t("distribution.flow.once"))}
        {node(<Warehouse className="size-5 text-primary" />, t("distribution.flow.dist"), t("distribution.flow.distHint"), "border-primary/40 bg-primary/5")}
        {arrow(t("distribution.flow.local"))}
        {node(<Laptop className="size-5 text-primary" />, t("distribution.flow.agents"), t("distribution.flow.agentsHint"), "border-line bg-surface-muted/40")}
      </div>
      <p className="mt-3 text-xs text-muted">{t("distribution.flow.explain")}</p>
    </section>
  );
}

function Empty({ onAdd, port }: { onAdd: () => void; port: number }) {
  const { t } = useTranslation();
  return (
    <Card title={t("distribution.setup.title")} description={t("distribution.setup.subtitle")}>
      <ol className="space-y-3 text-sm">
        <li>1. {t("distribution.setup.machine")}</li>
        <li>2. {t("distribution.setup.add")}</li>
        <li>3. {t("distribution.setup.install")}</li>
        <li>4. {t("distribution.setup.firewall", { port })}</li>
      </ol>
      <div className="mt-4">
        <Button icon={<Plus className="size-4" />} onClick={onAdd}>
          {t("distribution.add")}
        </Button>
      </div>
    </Card>
  );
}

function Progress({ value, total }: { value: number; total: number }) {
  const pct = total > 0 ? Math.min(100, Math.round((value / total) * 100)) : 100;
  return (
    <div className="h-2 w-full overflow-hidden rounded-full bg-surface-muted" role="progressbar" aria-valuenow={pct} aria-valuemin={0} aria-valuemax={100}>
      <div className="h-full rounded-full bg-primary transition-all" style={{ width: `${pct}%` }} />
    </div>
  );
}

function DistCard({
  d,
  list,
  busy,
  onEdit,
  onToggle,
  onToken,
  onUpdate,
  onDelete,
}: {
  d: DistributionServer;
  list: DistributionList;
  busy: string | null;
  onEdit: () => void;
  onToggle: () => void;
  onToken: () => void;
  onUpdate: () => void;
  onDelete: () => void;
}) {
  const { t, i18n } = useTranslation();
  const { can } = useAuth();
  const groups = useGroups();
  const lang = i18n.language;
  const st = d.status;
  const version = st?.version || d.version;
  const bytes = (n: number) => formatBytes(n, lang);
  const synced = st ? st.manifest === list.manifest && st.pendingFiles === 0 : false;
  const groupNames = (groups ?? []).filter((g) => d.groupIds.includes(g.id)).map((g) => g.name);
  const row = (label: string, value: React.ReactNode) => (
    <div className="min-w-0">
      <dt className="text-xs text-muted">{label}</dt>
      <dd className="mt-0.5 truncate text-sm">{value}</dd>
    </div>
  );
  return (
    <Card
      title={
        <span className="flex flex-wrap items-center gap-2">
          <Warehouse className="size-4 text-primary" />
          {d.name}
          <Badge tone={stateTone[d.state]}>{t(`distribution.state.${d.state}`)}</Badge>
          {d.state === "online" && <Badge tone={synced ? "success" : "primary"}>{synced ? t("distribution.synced") : t("distribution.syncing")}</Badge>}
        </span>
      }
      description={d.url || t("distribution.noUrl")}
      actions={
        <>
          <Button variant="secondary" size="sm" icon={<Pencil className="size-4" />} onClick={onEdit}>
            {t("common.edit")}
          </Button>
          <Button variant="secondary" size="sm" icon={<KeyRound className="size-4" />} loading={busy === `token-${d.id}`} onClick={onToken}>
            {d.enrolled ? t("distribution.reinstall") : t("distribution.installCommand")}
          </Button>
          {d.enrolled && (
            <Button variant="ghost" size="sm" icon={<Power className="size-4" />} loading={busy === `toggle-${d.id}`} onClick={onToggle}>
              {d.enabled ? t("distribution.disable") : t("distribution.enable")}
            </Button>
          )}
          <Button variant="ghost" size="sm" icon={<Trash2 className="size-4" />} onClick={onDelete} aria-label={t("common.delete")} />
        </>
      }
    >
      <div className="space-y-4">
        {d.state === "pending" && <Alert tone="warning">{t("distribution.pendingHint", { when: formatDate(d.tokenExpiresAt, lang) })}</Alert>}
        {d.state === "offline" && <Alert tone="danger">{t("distribution.offlineHint", { when: formatDate(d.lastSeenAt, lang) })}</Alert>}
        {st?.syncError && d.state === "online" && <Alert tone="warning" title={t("distribution.syncErrorTitle")}>{st.syncError}</Alert>}
        {version && list.version && version !== list.version && d.state === "online" && (
          <Alert tone="warning">
            {t(st?.updatable ? "distribution.versionMismatchUpdate" : "distribution.versionMismatch", { dist: version, server: list.version })}
            {st?.updatable && can("system.update") && (
              <div className="mt-2">
                <Button size="sm" icon={<ArrowUpCircle className="size-4" />} loading={busy === `update-${d.id}`} onClick={onUpdate}>
                  {t("distribution.update", { version: list.version })}
                </Button>
              </div>
            )}
          </Alert>
        )}
        {st?.update && st.update.state !== "completed" && (
          <Alert tone={st.update.state === "failed" ? "danger" : "info"} title={t(`gateways.updateState.${st.update.state}`, { defaultValue: st.update.state })}>
            {st.update.message}
          </Alert>
        )}
        {st && d.state !== "pending" && (
          <div className="space-y-1.5">
            <div className="flex flex-wrap items-baseline justify-between gap-2 text-sm">
              <span className="font-medium">{t("distribution.stored", { files: st.files, total: st.files + st.pendingFiles })}</span>
              <span className="text-xs text-muted">
                {bytes(st.bytes)}
                {st.pendingBytes > 0 && ` · ${t("distribution.toGo", { size: bytes(st.pendingBytes) })}`}
              </span>
            </div>
            <Progress value={st.bytes} total={st.bytes + st.pendingBytes} />
            {st.downloading && (
              <p className="flex items-center gap-1 text-xs text-muted">
                <HardDriveDownload className="size-3" /> {t("distribution.downloading", { sha: st.downloading.slice(0, 12) })}
              </p>
            )}
          </div>
        )}
        <dl className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
          {row(t("distribution.agents"), <span className="font-semibold">{d.agents}</span>)}
          {row(t("distribution.served"), st ? t("distribution.servedValue", { files: st.servedFiles, size: bytes(st.servedBytes) }) : "–")}
          {row(t("distribution.disk"), st && st.diskTotal > 0 ? t("distribution.diskValue", { free: bytes(st.diskFree), total: bytes(st.diskTotal) }) : "–")}
          {row(t("distribution.lastSeen"), formatDate(d.lastSeenAt, lang))}
          {row(t("distribution.networks"), d.networks.length ? <span className="font-mono text-xs">{d.networks.join(", ")}</span> : "–")}
          {row(t("distribution.groups"), groupNames.length ? groupNames.join(", ") : "–")}
          {row(t("distribution.bandwidth"), d.bandwidthKbps > 0 ? t("distribution.bandwidthValue", { mbit: (d.bandwidthKbps / 1000).toLocaleString(lang) }) : t("distribution.unlimited"))}
          {row(t("distribution.version"), version || "–")}
          {st && row(t("distribution.host"), st.hostname)}
          {d.certSha256 && row(t("distribution.certificate"), <span className="font-mono text-xs" title={d.certSha256}>{d.certSha256.slice(0, 16)}…</span>)}
          {st && row(t("distribution.misses"), t("distribution.missesValue", { misses: st.misses, refused: st.refused }))}
        </dl>
        {d.networks.length === 0 && d.groupIds.length === 0 && <Alert tone="info">{t("distribution.noScope")}</Alert>}
      </div>
    </Card>
  );
}

function DistForm({
  open,
  server,
  defaultPort,
  onClose,
  onSaved,
}: {
  open: boolean;
  server: DistributionServer | null;
  defaultPort: number;
  onClose: () => void;
  onSaved: (created: DistributionToken | null) => void;
}) {
  const { t } = useTranslation();
  const errorText = useErrorText();
  const groups = useGroups();
  const [name, setName] = useState("");
  const [url, setUrl] = useState("");
  const [networks, setNetworks] = useState("");
  const [groupIds, setGroupIds] = useState<number[]>([]);
  const [mbit, setMbit] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!open) return;
    setError(null);
    setName(server?.name ?? "");
    setUrl(server?.url ?? "");
    setNetworks((server?.networks ?? []).join("\n"));
    setGroupIds(server?.groupIds ?? []);
    setMbit(server && server.bandwidthKbps > 0 ? String(server.bandwidthKbps / 1000) : "");
  }, [open, server]);

  const save = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(null);
    const body = {
      name,
      url,
      networks: networks.split(/[\s,;]+/).filter(Boolean),
      groupIds,
      bandwidthKbps: mbit ? Math.round(parseFloat(mbit.replace(",", ".")) * 1000) || 0 : 0,
      enabled: server?.enabled ?? true,
    };
    try {
      if (server) {
        await put(`/distribution/${server.id}`, body);
        onSaved(null);
      } else {
        onSaved(await post<DistributionToken>("/distribution", body));
      }
    } catch (err) {
      setError(errorText(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Modal
      open={open}
      onClose={onClose}
      wide
      title={server ? t("distribution.editTitle", { name: server.name }) : t("distribution.addTitle")}
      footer={
        <>
          <Button variant="secondary" onClick={onClose}>
            {t("common.cancel")}
          </Button>
          <Button type="submit" form="dist-form" loading={busy} disabled={!name}>
            {server ? t("common.save") : t("distribution.create")}
          </Button>
        </>
      }
    >
      <form id="dist-form" onSubmit={save} className="space-y-4">
        {error && <Alert tone="danger">{error}</Alert>}
        <Field label={t("distribution.name")}>{(id) => <Input id={id} value={name} onChange={(e) => setName(e.target.value)} maxLength={100} placeholder={t("distribution.namePlaceholder")} required />}</Field>
        <Field label={t("distribution.networks")} hint={t("distribution.networksHint")}>
          {(id) => (
            <textarea
              id={id}
              value={networks}
              onChange={(e) => setNetworks(e.target.value)}
              rows={3}
              placeholder={"10.20.0.0/16\n203.0.113.7"}
              className="w-full rounded-md border border-line bg-surface px-3 py-2 font-mono text-sm focus:border-primary focus:outline-none"
            />
          )}
        </Field>
        <div className="space-y-1.5">
          <p className="text-sm font-medium">{t("distribution.groups")}</p>
          <p className="text-xs text-muted">{t("distribution.groupsHint")}</p>
          <div className="grid max-h-40 gap-1 overflow-y-auto rounded-md border border-line p-2 sm:grid-cols-2">
            {(groups ?? []).map((g) => (
              <label key={g.id} className="flex cursor-pointer items-center gap-2 rounded-md px-2 py-1.5 text-sm hover:bg-surface-muted">
                <input
                  type="checkbox"
                  checked={groupIds.includes(g.id)}
                  onChange={(e) => setGroupIds((ids) => (e.target.checked ? [...ids, g.id] : ids.filter((x) => x !== g.id)))}
                  className="accent-primary"
                />
                {g.name}
              </label>
            ))}
          </div>
        </div>
        <Field label={t("distribution.url")} hint={server?.enrolled ? t("distribution.urlHint") : t("distribution.urlHintNew", { port: defaultPort })}>
          {(id) => <Input id={id} value={url} onChange={(e) => setUrl(e.target.value)} placeholder={`https://10.20.0.5:${defaultPort}`} required={!!server?.enrolled} />}
        </Field>
        <Field label={t("distribution.bandwidth")} hint={t("distribution.bandwidthHint")}>
          {(id) => <Input id={id} inputMode="decimal" value={mbit} onChange={(e) => setMbit(e.target.value)} placeholder={t("distribution.unlimited")} />}
        </Field>
      </form>
    </Modal>
  );
}

function InstallModal({ info, onClose }: { info: DistributionToken | null; onClose: () => void }) {
  const { t, i18n } = useTranslation();
  return (
    <Modal
      open={!!info}
      onClose={onClose}
      wide
      title={t("distribution.installTitle", { name: info?.server.name ?? "" })}
      footer={<Button onClick={onClose}>{t("distribution.done")}</Button>}
    >
      {info && (
        <div className="space-y-4 text-sm">
          <p>{t("distribution.installText")}</p>
          <CopyField value={info.command} label={t("distribution.installCommand")} />
          <Alert tone="warning">{t("distribution.tokenOnce", { when: formatDate(info.expires, i18n.language) })}</Alert>
          <ul className="list-disc space-y-1 pl-5 text-muted">
            <li>{t("distribution.installNeeds")}</li>
            <li>{t("distribution.installFirewall", { port: 8445 })}</li>
          </ul>
        </div>
      )}
    </Modal>
  );
}
