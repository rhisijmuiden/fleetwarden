import { useCallback, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { ArrowLeftRight, ArrowUpCircle, CheckCircle2, ChevronLeft, ChevronRight, Cloud, KeyRound, Laptop, Pencil, Plus, Power, Server, ShieldHalf, Stethoscope, Trash2, XCircle } from "lucide-react";
import { del, get, post, put, type Gateway, type GatewayCheck, type GatewaysView } from "../../api";
import { useAuth } from "../../auth";
import { Alert, Badge, Button, Card, CopyField, Field, Input, Modal, PageHeader, Spinner, formatDate, useErrorText } from "../../components/ui";

const stateTone = { connected: "success", connecting: "warning", error: "danger", disabled: "neutral" } as const;

// Splits a stored link error ("code: detail") so the code can be translated.
function useLinkError() {
  const { t } = useTranslation();
  return (raw: string) => {
    if (!raw) return "";
    const [code, ...rest] = raw.split(": ");
    const text = t(`gateways.linkErrors.${code}`, { defaultValue: "" });
    return text ? (rest.length ? `${text} (${rest.join(": ")})` : text) : raw;
  };
}

function installCommand(channel: string) {
  const branch = channel === "production" ? "main" : "test";
  return `curl -fsSL https://raw.githubusercontent.com/rhisijmuiden/fleetwarden/${branch}/installer/install-gateway.sh | sudo bash -s -- --channel ${channel}`;
}

// Admin > Secure gateway: relays in the DMZ so agents outside the network
// reach the management server without exposing it to the internet.
export function Gateways() {
  const { t } = useTranslation();
  const errorText = useErrorText();
  const [data, setData] = useState<GatewaysView | null>(null);
  const [msg, setMsg] = useState<{ tone: "success" | "danger" | "warning"; text: string } | null>(null);
  const [adding, setAdding] = useState(false);
  const [editing, setEditing] = useState<Gateway | null>(null);
  const [repairing, setRepairing] = useState<Gateway | null>(null);
  const [deleting, setDeleting] = useState<Gateway | null>(null);
  const [tests, setTests] = useState<Record<number, GatewayCheck[] | "busy">>({});
  const [busy, setBusy] = useState<string | null>(null);

  const load = useCallback(() => {
    get<GatewaysView>("/gateways")
      .then(setData)
      .catch((e) => setMsg({ tone: "danger", text: errorText(e) }));
  }, []);
  useEffect(() => {
    load();
    const id = setInterval(load, 5000);
    return () => clearInterval(id);
  }, [load]);

  const test = async (g: Gateway) => {
    setTests((m) => ({ ...m, [g.id]: "busy" }));
    try {
      const r = await post<{ checks: GatewayCheck[] }>(`/gateways/${g.id}/test`);
      setTests((m) => ({ ...m, [g.id]: r.checks }));
    } catch (e) {
      setTests((m) => ({ ...m, [g.id]: [] }));
      setMsg({ tone: "danger", text: errorText(e) });
    }
  };

  const toggle = async (g: Gateway) => {
    setBusy(`toggle-${g.id}`);
    try {
      await put(`/gateways/${g.id}`, { name: g.name, address: g.address, publicUrl: g.publicUrl, enabled: !g.enabled });
      load();
    } catch (e) {
      setMsg({ tone: "danger", text: errorText(e) });
    } finally {
      setBusy(null);
    }
  };

  const update = async (g: Gateway) => {
    setBusy(`update-${g.id}`);
    try {
      await post(`/gateways/${g.id}/update`);
      setMsg({ tone: "success", text: t("gateways.updateRequested", { name: g.name, version: data?.version ?? "" }) });
      load();
    } catch (e) {
      setMsg({ tone: "danger", text: errorText(e) });
    } finally {
      setBusy(null);
    }
  };

  const remove = async () => {
    if (!deleting) return;
    setBusy("delete");
    try {
      await del(`/gateways/${deleting.id}`);
      setMsg({ tone: "success", text: t("gateways.deleted", { name: deleting.name }) });
      setDeleting(null);
      load();
    } catch (e) {
      setMsg({ tone: "danger", text: errorText(e) });
    } finally {
      setBusy(null);
    }
  };

  return (
    <div className="space-y-6">
      <PageHeader
        title={t("gateways.title")}
        description={t("gateways.subtitle")}
        actions={
          <Button icon={<Plus className="size-4" />} onClick={() => setAdding(true)}>
            {t("gateways.add")}
          </Button>
        }
      />
      {msg && <Alert tone={msg.tone}>{msg.text}</Alert>}
      <Flow />
      {!data ? (
        <Spinner />
      ) : (
        <>
          {data.gateways.length === 0 ? (
            <SetupSteps data={data} onAdd={() => setAdding(true)} />
          ) : (
            <div className="space-y-4">
              {data.gateways.map((g) => (
                <GatewayCard
                  key={g.id}
                  g={g}
                  serverVersion={data.version}
                  checks={tests[g.id]}
                  busy={busy}
                  onTest={() => test(g)}
                  onEdit={() => setEditing(g)}
                  onRepair={() => setRepairing(g)}
                  onToggle={() => toggle(g)}
                  onDelete={() => setDeleting(g)}
                  onUpdate={() => update(g)}
                />
              ))}
            </div>
          )}
          <Endpoints data={data} />
          {data.gateways.length > 0 && <SetupSteps data={data} compact />}
        </>
      )}

      <GatewayForm open={adding} onClose={() => setAdding(false)} onSaved={(name) => { setAdding(false); setMsg({ tone: "success", text: t("gateways.paired", { name }) }); load(); }} defaultPort={data?.tunnelPort ?? 8444} />
      <GatewayForm open={!!editing} gateway={editing} onClose={() => setEditing(null)} onSaved={() => { setEditing(null); setMsg({ tone: "success", text: t("common.saved") }); load(); }} defaultPort={data?.tunnelPort ?? 8444} />
      <RepairForm gateway={repairing} onClose={() => setRepairing(null)} onSaved={(name) => { setRepairing(null); setMsg({ tone: "success", text: t("gateways.paired", { name }) }); load(); }} />
      <Modal
        open={!!deleting}
        onClose={() => setDeleting(null)}
        title={t("gateways.deleteTitle", { name: deleting?.name ?? "" })}
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
        <p className="text-sm">{t("gateways.deleteText", { count: deleting?.agents ?? 0 })}</p>
      </Modal>
    </div>
  );
}

// A small picture of the traffic: agents go in, the server dials out.
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
  const arrow = (label: string, reverse?: boolean) => (
    <div className="flex shrink-0 flex-col items-center justify-center gap-1 px-1 text-[11px] text-muted md:w-36">
      <span className="flex w-full items-center text-primary" aria-hidden>
        {reverse && <ChevronLeft className="-mr-1.5 size-4 shrink-0" />}
        <span className="h-0.5 flex-1 rounded-full bg-primary/40" />
        {!reverse && <ChevronRight className="-ml-1.5 size-4 shrink-0" />}
      </span>
      <span className="text-center">{label}</span>
    </div>
  );
  return (
    <section className="rounded-lg border border-line bg-surface p-4">
      <div className="flex flex-col items-stretch gap-2 md:flex-row md:items-center">
        {node(<Laptop className="size-5 text-primary" />, t("gateways.flow.agents"), t("gateways.flow.agentsHint"), "border-line bg-surface-muted/40")}
        {arrow(t("gateways.flow.https"))}
        {node(<ShieldHalf className="size-5 text-primary" />, t("gateways.flow.gateway"), t("gateways.flow.gatewayHint"), "border-primary/40 bg-primary/5")}
        {arrow(t("gateways.flow.tunnel"), true)}
        {node(<Server className="size-5 text-primary" />, t("gateways.flow.server"), t("gateways.flow.serverHint"), "border-line bg-surface-muted/40")}
      </div>
      <p className="mt-3 text-xs text-muted">{t("gateways.flow.explain")}</p>
    </section>
  );
}

function SetupSteps({ data, onAdd, compact }: { data: GatewaysView; onAdd?: () => void; compact?: boolean }) {
  const { t } = useTranslation();
  const steps = (
    <ol className="space-y-5 text-sm">
      <li className="space-y-2">
        <p className="font-medium">1. {t("gateways.setup.install")}</p>
        <p className="text-muted">{t("gateways.setup.installHint")}</p>
        <CopyField value={installCommand(data.channel)} label={t("gateways.setup.install")} />
      </li>
      <li className="space-y-2">
        <p className="font-medium">2. {t("gateways.setup.firewall")}</p>
        <ul className="list-disc space-y-1 pl-5 text-muted">
          <li>{t("gateways.setup.firewallIn")}</li>
          <li>{t("gateways.setup.firewallTunnel", { port: data.tunnelPort })}</li>
          <li>{t("gateways.setup.firewallNone")}</li>
        </ul>
      </li>
      <li className="space-y-2">
        <p className="font-medium">3. {t("gateways.setup.pair")}</p>
        <p className="text-muted">{t("gateways.setup.pairHint")}</p>
        <CopyField value="sudo fleetwarden-gateway pairing-code" label={t("gateways.setup.pair")} />
        {onAdd && (
          <Button icon={<Plus className="size-4" />} onClick={onAdd}>
            {t("gateways.add")}
          </Button>
        )}
      </li>
    </ol>
  );
  if (compact) {
    return (
      <Card title={t("gateways.setup.title")}>
        <details>
          <summary className="cursor-pointer text-sm text-muted select-none">{t("gateways.setup.show")}</summary>
          <div className="mt-4">{steps}</div>
        </details>
      </Card>
    );
  }
  return (
    <Card title={t("gateways.setup.title")} description={t("gateways.setup.subtitle")}>
      {steps}
    </Card>
  );
}

function GatewayCard({
  g,
  serverVersion,
  checks,
  busy,
  onTest,
  onEdit,
  onRepair,
  onToggle,
  onDelete,
  onUpdate,
}: {
  g: Gateway;
  serverVersion: string;
  checks?: GatewayCheck[] | "busy";
  busy: string | null;
  onTest: () => void;
  onEdit: () => void;
  onRepair: () => void;
  onToggle: () => void;
  onDelete: () => void;
  onUpdate: () => void;
}) {
  const { t, i18n } = useTranslation();
  const { can } = useAuth();
  const linkError = useLinkError();
  const lang = i18n.language;
  const version = g.status?.version || g.version;
  const keyProblem = g.state === "error" && /^(gateway_key_changed|paired_with_other_server|not_paired|bad_signature)/.test(g.error);
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
          <ShieldHalf className="size-4 text-primary" />
          {g.name}
          <Badge tone={stateTone[g.state]}>{t(`gateways.state.${g.state}`)}</Badge>
        </span>
      }
      description={g.publicUrl}
      actions={
        <>
          <Button variant="secondary" size="sm" icon={<Stethoscope className="size-4" />} loading={checks === "busy"} disabled={!g.enabled} onClick={onTest}>
            {t("gateways.test")}
          </Button>
          <Button variant="secondary" size="sm" icon={<Pencil className="size-4" />} onClick={onEdit}>
            {t("common.edit")}
          </Button>
          <Button variant="secondary" size="sm" icon={<KeyRound className="size-4" />} onClick={onRepair}>
            {t("gateways.repair")}
          </Button>
          <Button variant="ghost" size="sm" icon={<Power className="size-4" />} loading={busy === `toggle-${g.id}`} onClick={onToggle}>
            {g.enabled ? t("gateways.disable") : t("gateways.enable")}
          </Button>
          <Button variant="ghost" size="sm" icon={<Trash2 className="size-4" />} onClick={onDelete} aria-label={t("common.delete")} />
        </>
      }
    >
      <div className="space-y-4">
        {g.state === "error" && g.error && (
          <Alert tone="danger" title={t("gateways.errorTitle")}>
            {linkError(g.error)}
            {keyProblem && <p className="mt-1">{t("gateways.repairHint")}</p>}
          </Alert>
        )}
        {version && serverVersion && version !== serverVersion && g.state === "connected" && (
          <Alert tone="warning">
            {t(g.status?.updatable ? "gateways.versionMismatchUpdate" : "gateways.versionMismatch", { gateway: version, server: serverVersion })}
            {g.status?.updatable && can("system.update") && (
              <div className="mt-2">
                <Button size="sm" icon={<ArrowUpCircle className="size-4" />} loading={busy === `update-${g.id}`} onClick={onUpdate}>
                  {t("gateways.update", { version: serverVersion })}
                </Button>
              </div>
            )}
          </Alert>
        )}
        {g.status?.update && g.status.update.state !== "completed" && (
          <Alert tone={g.status.update.state === "failed" ? "danger" : "info"} title={t(`gateways.updateState.${g.status.update.state}`, { defaultValue: g.status.update.state })}>
            {g.status.update.message}
          </Alert>
        )}
        <dl className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
          {row(t("gateways.agents"), <span className="font-semibold">{g.agents}</span>)}
          {row(t("gateways.address"), <span className="font-mono text-xs">{g.address}</span>)}
          {row(t("gateways.version"), version || "–")}
          {row(
            t("gateways.lastConnected"),
            g.state === "connected" && g.stateSince ? t("gateways.connectedSince", { when: formatDate(g.stateSince, lang) }) : formatDate(g.lastConnectedAt, lang),
          )}
          {row(t("gateways.fingerprint"), <span className="font-mono text-xs" title={g.fingerprint}>{g.fingerprint}</span>)}
          {g.status && row(t("gateways.host"), g.status.hostname)}
          {g.status && row(t("gateways.tlsMode"), t(`gateways.tls.${g.status.tlsMode}`, { defaultValue: g.status.tlsMode }))}
          {g.status && row(t("gateways.requests"), t("gateways.requestsValue", { requests: g.status.requests, sockets: g.status.activeSockets }))}
        </dl>
        {Array.isArray(checks) && checks.length > 0 && (
          <ul className="space-y-2 rounded-md border border-line bg-surface-muted/40 p-3 text-sm" aria-live="polite">
            {checks.map((c) => (
              <li key={c.name} className="flex items-start gap-2">
                {c.ok ? <CheckCircle2 className="mt-0.5 size-4 shrink-0 text-success" /> : <XCircle className="mt-0.5 size-4 shrink-0 text-danger" />}
                <span>
                  <span className="font-medium">{t(`gateways.checks.${c.name}`)}</span>
                  {": "}
                  {c.ok ? t(`gateways.checks.${c.name}Ok`) : t(`gateways.checkCodes.${c.code}`, { defaultValue: c.code })}
                  {!c.ok && c.detail && <span className="block text-xs text-muted">{c.detail}</span>}
                </span>
              </li>
            ))}
          </ul>
        )}
      </div>
    </Card>
  );
}

function Endpoints({ data }: { data: GatewaysView }) {
  const { t } = useTranslation();
  const via = data.gateways.reduce((n, g) => n + g.agents, 0);
  return (
    <Card title={t("gateways.endpoints.title")} description={t("gateways.endpoints.subtitle")}>
      <div className="grid gap-4 md:grid-cols-[1fr_auto]">
        {data.endpoints.length === 0 ? (
          <p className="text-sm text-muted">{t("gateways.endpoints.none")}</p>
        ) : (
          <ol className="space-y-2 text-sm">
            {data.endpoints.map((u, i) => (
              <li key={u} className="flex items-center gap-2">
                <span className="grid size-6 shrink-0 place-items-center rounded-full bg-primary/10 text-xs font-semibold text-primary">{i + 1}</span>
                {i === 0 && data.serverUrl && u === data.serverUrl.replace(/\/$/, "") ? <Server className="size-4 text-muted" /> : <Cloud className="size-4 text-muted" />}
                <span className="font-mono text-xs">{u}</span>
              </li>
            ))}
          </ol>
        )}
        <div className="flex gap-6 text-sm">
          <div>
            <p className="text-xs text-muted">{t("gateways.endpoints.direct")}</p>
            <p className="text-xl font-semibold">{data.directAgents}</p>
          </div>
          <div>
            <p className="flex items-center gap-1 text-xs text-muted">
              <ArrowLeftRight className="size-3" /> {t("gateways.endpoints.viaGateway")}
            </p>
            <p className="text-xl font-semibold">{via}</p>
          </div>
        </div>
      </div>
      {!data.serverUrl && data.gateways.length > 0 && <div className="mt-4"><Alert tone="warning">{t("gateways.endpoints.noServerUrl")}</Alert></div>}
    </Card>
  );
}

function GatewayForm({ open, gateway, onClose, onSaved, defaultPort }: { open: boolean; gateway?: Gateway | null; onClose: () => void; onSaved: (name: string) => void; defaultPort: number }) {
  const { t } = useTranslation();
  const errorText = useErrorText();
  const [name, setName] = useState("");
  const [address, setAddress] = useState("");
  const [publicUrl, setPublicUrl] = useState("");
  const [code, setCode] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const editing = !!gateway;

  useEffect(() => {
    if (!open) return;
    setError(null);
    setName(gateway?.name ?? "");
    setAddress(gateway?.address ?? "");
    setPublicUrl(gateway?.publicUrl ?? "https://");
    setCode("");
  }, [open, gateway]);

  const save = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      if (gateway) await put(`/gateways/${gateway.id}`, { name, address, publicUrl, enabled: gateway.enabled });
      else await post("/gateways", { name, address, publicUrl, pairingCode: code });
      onSaved(name);
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
      title={editing ? t("gateways.editTitle", { name: gateway?.name }) : t("gateways.addTitle")}
      footer={
        <>
          <Button variant="secondary" onClick={onClose}>
            {t("common.cancel")}
          </Button>
          <Button type="submit" form="gateway-form" loading={busy} disabled={!name || !address || !publicUrl || (!editing && !code)}>
            {editing ? t("common.save") : t("gateways.pair")}
          </Button>
        </>
      }
    >
      <form id="gateway-form" onSubmit={save} className="space-y-4">
        {error && <Alert tone="danger">{error}</Alert>}
        <Field label={t("gateways.name")}>{(id) => <Input id={id} value={name} onChange={(e) => setName(e.target.value)} maxLength={100} placeholder="DMZ" required />}</Field>
        <Field label={t("gateways.address")} hint={t("gateways.addressHint", { port: defaultPort })}>
          {(id) => <Input id={id} value={address} onChange={(e) => setAddress(e.target.value)} placeholder={`10.0.20.5:${defaultPort}`} required />}
        </Field>
        <Field label={t("gateways.publicUrl")} hint={t("gateways.publicUrlHint")}>
          {(id) => <Input id={id} type="url" value={publicUrl} onChange={(e) => setPublicUrl(e.target.value)} placeholder="https://agents.example.com" required />}
        </Field>
        {!editing && (
          <Field label={t("gateways.pairingCode")} hint={t("gateways.pairingCodeHint")}>
            {(id) => <Input id={id} value={code} onChange={(e) => setCode(e.target.value.toUpperCase())} placeholder="XXXX-XXXX-XXXX-XXXX" autoComplete="off" className="font-mono" required />}
          </Field>
        )}
        {editing && <p className="text-xs text-muted">{t("gateways.editHint")}</p>}
      </form>
    </Modal>
  );
}

function RepairForm({ gateway, onClose, onSaved }: { gateway: Gateway | null; onClose: () => void; onSaved: (name: string) => void }) {
  const { t } = useTranslation();
  const errorText = useErrorText();
  const [code, setCode] = useState("");
  const [address, setAddress] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => {
    setCode("");
    setError(null);
    setAddress(gateway?.address ?? "");
  }, [gateway]);
  const save = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!gateway) return;
    setBusy(true);
    setError(null);
    try {
      await post(`/gateways/${gateway.id}/pair`, { pairingCode: code, address });
      onSaved(gateway.name);
    } catch (err) {
      setError(errorText(err));
    } finally {
      setBusy(false);
    }
  };
  return (
    <Modal
      open={!!gateway}
      onClose={onClose}
      title={t("gateways.repairTitle", { name: gateway?.name ?? "" })}
      footer={
        <>
          <Button variant="secondary" onClick={onClose}>
            {t("common.cancel")}
          </Button>
          <Button type="submit" form="repair-form" loading={busy} disabled={!code}>
            {t("gateways.pair")}
          </Button>
        </>
      }
    >
      <form id="repair-form" onSubmit={save} className="space-y-4">
        {error && <Alert tone="danger">{error}</Alert>}
        <p className="text-sm text-muted">{t("gateways.repairText")}</p>
        <CopyField value="sudo fleetwarden-gateway reset-pairing" label={t("gateways.repair")} />
        <Field label={t("gateways.address")}>{(id) => <Input id={id} value={address} onChange={(e) => setAddress(e.target.value)} />}</Field>
        <Field label={t("gateways.pairingCode")}>
          {(id) => <Input id={id} value={code} onChange={(e) => setCode(e.target.value.toUpperCase())} placeholder="XXXX-XXXX-XXXX-XXXX" autoComplete="off" className="font-mono" required />}
        </Field>
      </form>
    </Modal>
  );
}
