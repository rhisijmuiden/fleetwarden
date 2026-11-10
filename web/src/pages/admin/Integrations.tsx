// Administration > Integrations: service desk connectors and custom API calls.
import { useEffect, useMemo, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Link, useNavigate, useParams, useSearchParams } from "react-router";
import {
  ArrowLeft,
  CheckCircle2,
  Headset,
  Plug,
  Plus,
  Send,
  Trash2,
  Webhook,
  XCircle,
  Zap,
} from "lucide-react";
import { del, get, post, put, type AlertRule } from "../../api";
import {
  Alert,
  Badge,
  Button,
  Card,
  Field,
  Input,
  Modal,
  PageHeader,
  Select,
  Spinner,
  Switch,
  formatDate,
  useErrorText,
} from "../../components/ui";

export type IntegrationField = {
  key: string;
  type: "text" | "url" | "email" | "secret" | "switch" | "select";
  required: boolean;
  options?: string[];
  placeholder?: string;
  default?: string;
};
export type IntegrationKind = {
  kind: string;
  name: string;
  serviceDesk: boolean;
  fields: IntegrationField[];
};
type Kinds = {
  kinds: IntegrationKind[];
  variables: string[];
  events: string[];
};

export type CustomAPI = {
  method: string;
  url: string;
  auth: "none" | "bearer" | "basic" | "apikey" | "oauth2" | "hmac";
  username: string;
  headerName: string;
  tokenUrl: string;
  clientId: string;
  scope: string;
  headers: { name: string; value: string }[];
  contentType: "json" | "form" | "xml" | "text";
  body: string;
  numberPath: string;
  urlPath: string;
  timeoutSec: number;
};

export type Integration = {
  id: number;
  name: string;
  kind: string;
  enabled: boolean;
  settings: Record<string, string>;
  custom: CustomAPI | null;
  events: string[];
  alertRuleIds: number[];
  createdBy: string;
  createdAt: string;
  updatedAt: string;
  lastOk: boolean | null;
  lastError: string;
  lastAt: string | null;
  secretsSet: Record<string, boolean>;
  serviceDesk: boolean;
};

type Call = {
  id: number;
  createdAt: string;
  event: string;
  method: string;
  url: string;
  status: number;
  ok: boolean;
  durationMs: number;
  error: string;
  ticketNumber: string;
  ticketUrl: string;
};

type TestResult = {
  ok: boolean;
  error: string;
  calls: {
    method: string;
    url: string;
    status: number;
    ok: boolean;
    durationMs: number;
    error: string;
  }[];
  ticket: { id: string; number: string; url: string };
};

// Colours of the initials on the service desk tiles.
const deskColors: Record<string, string> = {
  topdesk: "#0b7a75",
  freshservice: "#2f5bea",
  jsm: "#1d4ed8",
  servicenow: "#14532d",
  zendesk: "#0f766e",
  halo: "#7c3aed",
  zammad: "#b45309",
  glpi: "#be123c",
};

function Initials({ name, kind }: { name: string; kind: string }) {
  const letters = name
    .split(/\s+/)
    .map((w) => w[0])
    .join("")
    .slice(0, 2);
  return (
    <span
      className="grid size-10 shrink-0 place-items-center rounded-lg text-sm font-semibold text-white"
      style={{ background: deskColors[kind] ?? "#5b6578" }}
    >
      {letters}
    </span>
  );
}

function StatusBadge({ i }: { i: Integration }) {
  const { t } = useTranslation();
  if (!i.enabled) return <Badge>{t("integrations.disabled")}</Badge>;
  if (i.lastOk === null)
    return <Badge tone="primary">{t("integrations.notUsedYet")}</Badge>;
  if (i.lastOk)
    return <Badge tone="success">{t("integrations.connected")}</Badge>;
  return (
    <span title={i.lastError}>
      <Badge tone="danger">{t("integrations.failing")}</Badge>
    </span>
  );
}

export function Integrations() {
  const { t, i18n } = useTranslation();
  const errorText = useErrorText();
  const navigate = useNavigate();
  const [list, setList] = useState<Integration[] | null>(null);
  const [kinds, setKinds] = useState<Kinds | null>(null);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => {
    Promise.all([
      get<Integration[]>("/integrations"),
      get<Kinds>("/integrations/kinds"),
    ])
      .then(([l, k]) => {
        setList(l);
        setKinds(k);
      })
      .catch((e) => setError(errorText(e)));
  }, []);
  if (!list || !kinds)
    return error ? <Alert tone="danger">{error}</Alert> : <Spinner />;
  const kindName = (k: string) =>
    k === "custom"
      ? t("integrations.custom")
      : (kinds.kinds.find((x) => x.kind === k)?.name ?? k);
  const failing = list.filter((i) => i.enabled && i.lastOk === false).length;
  return (
    <div className="space-y-6">
      <PageHeader
        title={t("integrations.title")}
        description={t("integrations.subtitle")}
        actions={
          <Button
            icon={<Plus className="size-4" />}
            onClick={() => navigate("/admin/integrations/new?kind=custom")}
          >
            {t("integrations.newCustom")}
          </Button>
        }
      />
      <Card
        title={t("integrations.configured")}
        description={
          list.length
            ? t("integrations.configuredCount", { count: list.length, failing })
            : undefined
        }
      >
        {list.length === 0 ? (
          <p className="text-sm text-muted">{t("integrations.none")}</p>
        ) : (
          <div className="-mx-5 -my-5 overflow-x-auto">
            <table className="w-full min-w-[760px] text-sm">
              <thead className="border-b border-line bg-surface-muted/60 text-left text-xs font-medium text-muted">
                <tr>
                  <th className="px-5 py-2.5">{t("integrations.name")}</th>
                  <th className="px-3 py-2.5">{t("integrations.kind")}</th>
                  <th className="px-3 py-2.5">{t("integrations.status")}</th>
                  <th className="px-3 py-2.5">{t("integrations.usedBy")}</th>
                  <th className="px-5 py-2.5">
                    {t("integrations.lastActivity")}
                  </th>
                </tr>
              </thead>
              <tbody>
                {list.map((i) => (
                  <tr
                    key={i.id}
                    className="border-b border-line last:border-0 hover:bg-surface-muted/50"
                  >
                    <td className="px-5 py-3">
                      <Link
                        to={`/admin/integrations/${i.id}`}
                        className="flex items-center gap-3"
                      >
                        <span className="grid size-8 place-items-center rounded-md bg-primary/10 text-primary">
                          {i.serviceDesk ? (
                            <Headset className="size-4" />
                          ) : (
                            <Webhook className="size-4" />
                          )}
                        </span>
                        <span className="min-w-0">
                          <span className="block font-medium text-primary">
                            {i.name}
                          </span>
                          <span className="block truncate font-mono text-xs text-muted">
                            {hostOf(i)}
                          </span>
                        </span>
                      </Link>
                    </td>
                    <td className="px-3 py-3 text-muted">{kindName(i.kind)}</td>
                    <td className="px-3 py-3">
                      <StatusBadge i={i} />
                    </td>
                    <td className="px-3 py-3">{usedBy(i, t)}</td>
                    <td className="px-5 py-3 text-muted">
                      {i.lastAt ? formatDate(i.lastAt, i18n.language) : "–"}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Card>
      <Card
        title={t("integrations.serviceDesks")}
        description={t("integrations.serviceDesksHint")}
      >
        <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
          {kinds.kinds
            .filter((k) => k.serviceDesk)
            .map((k) => {
              const have = list.filter((i) => i.kind === k.kind);
              return (
                <div
                  key={k.kind}
                  className="flex flex-col gap-3 rounded-lg border border-line p-4"
                >
                  <div className="flex items-center gap-3">
                    <Initials name={k.name} kind={k.kind} />
                    <p className="leading-tight font-medium">{k.name}</p>
                  </div>
                  <p className="flex-1 text-xs text-muted">
                    {t(`integrations.deskHint.${k.kind}`)}
                  </p>
                  {have.length > 0 ? (
                    <Button
                      variant="secondary"
                      size="sm"
                      icon={<CheckCircle2 className="size-4 text-success" />}
                      onClick={() =>
                        navigate(`/admin/integrations/${have[0].id}`)
                      }
                    >
                      {t("integrations.configuredOne")}
                    </Button>
                  ) : (
                    <Button
                      variant="secondary"
                      size="sm"
                      icon={<Plug className="size-4" />}
                      onClick={() =>
                        navigate(`/admin/integrations/new?kind=${k.kind}`)
                      }
                    >
                      {t("integrations.connect")}
                    </Button>
                  )}
                </div>
              );
            })}
        </div>
      </Card>
      <Card
        title={t("integrations.customTitle")}
        description={t("integrations.customHint")}
        actions={
          <Button
            icon={<Webhook className="size-4" />}
            onClick={() => navigate("/admin/integrations/new?kind=custom")}
          >
            {t("integrations.newCustom")}
          </Button>
        }
      >
        <div className="grid gap-3 text-sm sm:grid-cols-2 lg:grid-cols-4">
          {(["trigger", "request", "auth", "response"] as const).map((k) => (
            <div key={k} className="rounded-md bg-surface-muted p-3">
              <p className="text-xs font-medium tracking-wide text-muted uppercase">
                {t(`integrations.customParts.${k}`)}
              </p>
              <p className="mt-1">{t(`integrations.customParts.${k}Text`)}</p>
            </div>
          ))}
        </div>
      </Card>
    </div>
  );
}

function hostOf(i: Integration): string {
  const u = i.kind === "custom" ? i.custom?.url : i.settings.url;
  if (!u) return "";
  try {
    return new URL(u.replace(/\{\{[^}]*\}\}/g, "x")).host;
  } catch {
    return u;
  }
}

function usedBy(i: Integration, t: (k: string) => string): string {
  const parts: string[] = [];
  if (i.events.includes("alert.opened"))
    parts.push(t("integrations.events.alert.opened"));
  if (i.events.includes("device.enrolled"))
    parts.push(t("integrations.events.device.enrolled"));
  return parts.length ? parts.join(", ") : "–";
}

// ---- editor ----

const emptyCustom: CustomAPI = {
  method: "POST",
  url: "",
  auth: "none",
  username: "",
  headerName: "",
  tokenUrl: "",
  clientId: "",
  scope: "",
  headers: [],
  contentType: "json",
  body: '{\n  "title": "[{{device.hostname}}] {{ticket.subject}}",\n  "requester": "{{user.email}}",\n  "description": {{report.html | json}},\n  "link": "{{device.url}}"\n}',
  numberPath: "",
  urlPath: "",
  timeoutSec: 15,
};

type Form = {
  name: string;
  enabled: boolean;
  settings: Record<string, string>;
  secrets: Record<string, string>;
  clear: string[];
  custom: CustomAPI;
  events: string[];
  alertRuleIds: number[];
};

const ticketKeys = [
  "fallbackRequester",
  "subject",
  "category",
  "callType",
  "entryType",
  "operatorGroup",
  "groupId",
  "workspaceId",
  "assignmentGroup",
  "ticketTypeId",
  "team",
  "group",
  "categoryId",
  "serviceDeskId",
  "requestTypeId",
];
const priorityKeys = ["priorityLow", "priorityNormal", "priorityHigh"];
const syncKeys = ["syncStatus", "notes"];

export function IntegrationEditor() {
  const { t, i18n } = useTranslation();
  const errorText = useErrorText();
  const navigate = useNavigate();
  const { id } = useParams();
  const [params] = useSearchParams();
  const isNew = id === undefined;
  const [kinds, setKinds] = useState<Kinds | null>(null);
  const [cur, setCur] = useState<Integration | null>(null);
  const [form, setForm] = useState<Form | null>(null);
  const [rules, setRules] = useState<AlertRule[]>([]);
  const [calls, setCalls] = useState<Call[]>([]);
  const [msg, setMsg] = useState<{
    tone: "success" | "danger";
    text: string;
  } | null>(null);
  const [test, setTest] = useState<TestResult | null>(null);
  const [busy, setBusy] = useState<"" | "save" | "test" | "ticket">("");
  const [confirmDelete, setConfirmDelete] = useState(false);

  const kindKey = cur?.kind ?? params.get("kind") ?? "custom";
  const kind = kinds?.kinds.find((k) => k.kind === kindKey);

  const loadCalls = (iid: number) =>
    get<Call[]>(`/integrations/${iid}/calls`)
      .then(setCalls)
      .catch(() => {});

  useEffect(() => {
    const load = async () => {
      const k = await get<Kinds>("/integrations/kinds");
      setKinds(k);
      get<{ rules: AlertRule[] }>("/alerts/rules")
        .then((r) => setRules(r.rules))
        .catch(() => {});
      if (isNew) {
        const info = k.kinds.find((x) => x.kind === kindKey);
        const settings: Record<string, string> = {};
        info?.fields.forEach((f) => {
          if (f.default && f.type !== "secret") settings[f.key] = f.default;
        });
        setForm({
          name: info && info.kind !== "custom" ? info.name : "",
          enabled: true,
          settings,
          secrets: {},
          clear: [],
          custom: { ...emptyCustom },
          events: [],
          alertRuleIds: [],
        });
        return;
      }
      const i = await get<Integration>(`/integrations/${id}`);
      setCur(i);
      setForm({
        name: i.name,
        enabled: i.enabled,
        settings: { ...i.settings },
        secrets: {},
        clear: [],
        custom: i.custom
          ? { ...emptyCustom, ...i.custom, headers: i.custom.headers ?? [] }
          : { ...emptyCustom },
        events: i.events,
        alertRuleIds: i.alertRuleIds,
      });
      void loadCalls(i.id);
    };
    load().catch((e) => setMsg({ tone: "danger", text: errorText(e) }));
  }, [id]);

  if (!form || !kinds)
    return msg ? <Alert tone={msg.tone}>{msg.text}</Alert> : <Spinner />;
  if (!kind)
    return <Alert tone="danger">{t("integrations.unknownKind")}</Alert>;
  const custom = kind.kind === "custom";

  const body = () => ({
    id: cur?.id ?? 0,
    kind: kind.kind,
    ...form,
    custom: custom ? form.custom : null,
  });
  const setSetting = (k: string, v: string) =>
    setForm({ ...form, settings: { ...form.settings, [k]: v } });
  const setSecret = (k: string, v: string) =>
    setForm({
      ...form,
      secrets: { ...form.secrets, [k]: v },
      clear: form.clear.filter((c) => c !== k),
    });
  const setCustom = (c: Partial<CustomAPI>) =>
    setForm({ ...form, custom: { ...form.custom, ...c } });

  const save = async () => {
    setBusy("save");
    setMsg(null);
    try {
      const out = cur
        ? await put<Integration>(`/integrations/${cur.id}`, body())
        : await post<Integration>("/integrations", body());
      setMsg({ tone: "success", text: t("common.saved") });
      if (!cur) {
        navigate(`/admin/integrations/${out.id}`, { replace: true });
        return;
      }
      setCur(out);
      setForm({ ...form, secrets: {}, clear: [] });
    } catch (e) {
      setMsg({ tone: "danger", text: errorText(e) });
    } finally {
      setBusy("");
    }
  };
  const runTest = async () => {
    setBusy("test");
    setTest(null);
    setMsg(null);
    try {
      setTest(await post<TestResult>("/integrations/test", body()));
      if (cur) void loadCalls(cur.id);
    } catch (e) {
      setMsg({ tone: "danger", text: errorText(e) });
    } finally {
      setBusy("");
    }
  };
  const testTicket = async () => {
    if (!cur) return;
    setBusy("ticket");
    setTest(null);
    setMsg(null);
    try {
      setTest(await post<TestResult>(`/integrations/${cur.id}/test-ticket`));
      void loadCalls(cur.id);
    } catch (e) {
      setMsg({ tone: "danger", text: errorText(e) });
    } finally {
      setBusy("");
    }
  };
  const remove = async () => {
    if (!cur) return;
    try {
      await del(`/integrations/${cur.id}`);
      navigate("/admin/integrations");
    } catch (e) {
      setConfirmDelete(false);
      setMsg({ tone: "danger", text: errorText(e) });
    }
  };

  const fieldFor = (f: IntegrationField) => {
    const label = t(`integrations.fields.${f.key}`) + (f.required ? " *" : "");
    const hint =
      t(`integrations.hints.${f.key}`, { defaultValue: "" }) || undefined;
    if (f.type === "switch") {
      return (
        <Switch
          key={f.key}
          checked={(form.settings[f.key] ?? f.default) === "true"}
          onChange={(v) => setSetting(f.key, v ? "true" : "false")}
          label={label}
          description={hint}
        />
      );
    }
    if (f.type === "secret") {
      return (
        <SecretField
          key={f.key}
          label={label}
          hint={hint}
          set={!!cur?.secretsSet[f.key] && !form.clear.includes(f.key)}
          value={form.secrets[f.key] ?? ""}
          onChange={(v) => setSecret(f.key, v)}
          onClear={
            f.required
              ? undefined
              : () =>
                  setForm({
                    ...form,
                    secrets: { ...form.secrets, [f.key]: "" },
                    clear: [...form.clear, f.key],
                  })
          }
        />
      );
    }
    return (
      <Field key={f.key} label={label} hint={hint}>
        {(fid) => (
          <Input
            id={fid}
            type={f.type === "email" ? "email" : "text"}
            value={form.settings[f.key] ?? ""}
            placeholder={f.placeholder ?? f.default}
            onChange={(e) => setSetting(f.key, e.target.value)}
            className={f.key === "subject" ? "font-mono text-xs" : ""}
          />
        )}
      </Field>
    );
  };

  const connFields = kind.fields.filter(
    (f) =>
      !ticketKeys.includes(f.key) &&
      !priorityKeys.includes(f.key) &&
      !syncKeys.includes(f.key),
  );
  const tickFields = kind.fields.filter((f) => ticketKeys.includes(f.key));
  const prioFields = kind.fields.filter((f) => priorityKeys.includes(f.key));
  const syncFields = kind.fields.filter((f) => syncKeys.includes(f.key));

  return (
    <div className="space-y-6">
      <Link
        to="/admin/integrations"
        className="inline-flex items-center gap-1.5 text-sm text-muted hover:text-fg"
      >
        <ArrowLeft className="size-4" />
        {t("integrations.title")}
      </Link>
      <PageHeader
        title={
          cur ? cur.name : custom ? t("integrations.newCustom") : kind.name
        }
        description={
          custom
            ? t("integrations.customEditorHint")
            : t("integrations.deskEditorHint", { name: kind.name })
        }
        actions={
          <>
            <Button
              variant="secondary"
              icon={<Zap className="size-4" />}
              loading={busy === "test"}
              onClick={runTest}
            >
              {custom
                ? t("integrations.sendTestCall")
                : t("integrations.testConnection")}
            </Button>
            {cur && (
              <Button
                variant="secondary"
                icon={<Send className="size-4" />}
                loading={busy === "ticket"}
                onClick={testTicket}
              >
                {t("integrations.sendTestTicket")}
              </Button>
            )}
            <Button loading={busy === "save"} onClick={save}>
              {t("common.save")}
            </Button>
          </>
        }
      />
      {msg && <Alert tone={msg.tone}>{msg.text}</Alert>}
      {test && <TestOutcome r={test} />}

      <Card title={t("integrations.general")}>
        <div className="grid gap-4 sm:grid-cols-2">
          <Field label={t("integrations.name") + " *"}>
            {(fid) => (
              <Input
                id={fid}
                value={form.name}
                maxLength={100}
                onChange={(e) => setForm({ ...form, name: e.target.value })}
              />
            )}
          </Field>
          <div className="pt-7">
            <Switch
              checked={form.enabled}
              onChange={(v) => setForm({ ...form, enabled: v })}
              label={t("integrations.enabled")}
              description={t("integrations.enabledHint")}
            />
          </div>
        </div>
      </Card>

      {custom ? (
        <CustomForm
          c={form.custom}
          set={setCustom}
          variables={kinds.variables}
          secretSet={!!cur?.secretsSet.secret && !form.clear.includes("secret")}
          secret={form.secrets.secret ?? ""}
          setSecret={(v) => setSecret("secret", v)}
        />
      ) : (
        <>
          <div className="grid gap-6 lg:grid-cols-2">
            <Card title={t("integrations.connection")}>
              <div className="space-y-4">{connFields.map(fieldFor)}</div>
            </Card>
            <Card title={t("integrations.newTickets")}>
              <div className="space-y-4">{tickFields.map(fieldFor)}</div>
            </Card>
          </div>
          {prioFields.length > 0 && (
            <Card
              title={t("integrations.priorityTitle")}
              description={t("integrations.priorityHint", { name: kind.name })}
            >
              <div className="grid gap-4 sm:grid-cols-3">
                {prioFields.map(fieldFor)}
              </div>
            </Card>
          )}
          <Card title={t("integrations.sync")}>
            <div className="space-y-4">{syncFields.map(fieldFor)}</div>
          </Card>
        </>
      )}

      <Card
        title={t("integrations.eventsTitle")}
        description={t("integrations.eventsHint")}
      >
        <div className="space-y-4">
          {kinds.events
            .filter((e) => custom || e !== "device.enrolled")
            .map((e) => (
              <div key={e} className="space-y-3">
                <Switch
                  checked={form.events.includes(e)}
                  onChange={(v) =>
                    setForm({
                      ...form,
                      events: v
                        ? [...form.events, e]
                        : form.events.filter((x) => x !== e),
                    })
                  }
                  label={t(`integrations.eventLabel.${e}`)}
                  description={t(`integrations.eventHint.${e}`)}
                />
                {e === "alert.opened" &&
                  form.events.includes("alert.opened") && (
                    <div className="ml-12 space-y-2">
                      <p className="text-sm font-medium">
                        {t("integrations.alertRules")}
                      </p>
                      {rules.length === 0 ? (
                        <p className="text-xs text-muted">
                          {t("integrations.noAlertRules")}
                        </p>
                      ) : (
                        <div className="grid gap-2 sm:grid-cols-2">
                          {rules.map((r) => (
                            <label
                              key={r.id}
                              className="flex items-center gap-2 text-sm"
                            >
                              <input
                                type="checkbox"
                                className="size-4 accent-[var(--fw-primary)]"
                                checked={form.alertRuleIds.includes(r.id)}
                                onChange={(ev) =>
                                  setForm({
                                    ...form,
                                    alertRuleIds: ev.target.checked
                                      ? [...form.alertRuleIds, r.id]
                                      : form.alertRuleIds.filter(
                                          (x) => x !== r.id,
                                        ),
                                  })
                                }
                              />
                              {r.name}
                            </label>
                          ))}
                        </div>
                      )}
                      <p className="text-xs text-muted">
                        {t("integrations.alertRulesHint")}
                      </p>
                    </div>
                  )}
              </div>
            ))}
        </div>
      </Card>

      {cur && (
        <Card
          title={t("integrations.calls")}
          description={t("integrations.callsHint")}
        >
          {calls.length === 0 ? (
            <p className="text-sm text-muted">{t("integrations.noCalls")}</p>
          ) : (
            <div className="-mx-5 -my-5 overflow-x-auto">
              <table className="w-full min-w-[640px] text-sm">
                <tbody>
                  {calls.map((c) => (
                    <tr
                      key={c.id}
                      className="border-b border-line last:border-0"
                    >
                      <td className="px-5 py-2.5 whitespace-nowrap text-muted tabular-nums">
                        {formatDate(c.createdAt, i18n.language)}
                      </td>
                      <td className="px-3 py-2.5">
                        {t(`integrations.callEvent.${c.event}`, {
                          defaultValue: c.event,
                        })}
                      </td>
                      <td
                        className="max-w-[320px] truncate px-3 py-2.5 font-mono text-xs text-muted"
                        title={c.error || c.url}
                      >
                        {c.method} {c.url}
                      </td>
                      <td className="px-5 py-2.5 text-right whitespace-nowrap">
                        {c.ok ? (
                          <Badge tone="success">
                            {c.status || "OK"}
                            {c.ticketNumber ? ` · ${c.ticketNumber}` : ""}
                          </Badge>
                        ) : (
                          <span title={c.error}>
                            <Badge tone="danger">
                              {c.status || t("integrations.failed")}
                            </Badge>
                          </span>
                        )}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </Card>
      )}

      {cur && (
        <div className="flex justify-between">
          <Button
            variant="ghost"
            icon={<Trash2 className="size-4" />}
            onClick={() => setConfirmDelete(true)}
          >
            {t("common.delete")}
          </Button>
          <Button loading={busy === "save"} onClick={save}>
            {t("common.save")}
          </Button>
        </div>
      )}
      <Modal
        open={confirmDelete}
        onClose={() => setConfirmDelete(false)}
        title={t("integrations.deleteTitle")}
        footer={
          <>
            <Button variant="secondary" onClick={() => setConfirmDelete(false)}>
              {t("common.cancel")}
            </Button>
            <Button variant="danger" onClick={remove}>
              {t("common.delete")}
            </Button>
          </>
        }
      >
        <p className="text-sm">
          {t("integrations.deleteText", { name: cur?.name ?? "" })}
        </p>
      </Modal>
    </div>
  );
}

function SecretField({
  label,
  hint,
  set,
  value,
  onChange,
  onClear,
}: {
  label: string;
  hint?: string;
  set: boolean;
  value: string;
  onChange: (v: string) => void;
  onClear?: () => void;
}) {
  const { t } = useTranslation();
  return (
    <Field
      label={label}
      hint={
        hint ??
        (set ? t("integrations.secretKept") : t("integrations.secretStored"))
      }
    >
      {(fid) => (
        <div className="flex gap-2">
          <Input
            id={fid}
            type="password"
            autoComplete="new-password"
            value={value}
            placeholder={set ? "••••••••••••" : ""}
            onChange={(e) => onChange(e.target.value)}
          />
          {set && onClear && (
            <Button variant="secondary" onClick={onClear}>
              {t("integrations.clearSecret")}
            </Button>
          )}
        </div>
      )}
    </Field>
  );
}

function TestOutcome({ r }: { r: TestResult }) {
  const { t } = useTranslation();
  return (
    <Alert
      tone={r.ok ? "success" : "danger"}
      title={
        r.ok
          ? r.ticket?.number
            ? t("integrations.ticketCreated", { number: r.ticket.number })
            : t("integrations.testOk")
          : t("integrations.testFailed")
      }
    >
      <div className="space-y-1">
        {r.error && <p className="font-mono text-xs break-all">{r.error}</p>}
        {r.ticket?.url && (
          <a
            href={r.ticket.url}
            target="_blank"
            rel="noreferrer"
            className="text-primary underline"
          >
            {r.ticket.url}
          </a>
        )}
        {r.calls.map((c, n) => (
          <p
            key={n}
            className="flex items-center gap-1.5 font-mono text-xs text-muted"
          >
            {c.ok ? (
              <CheckCircle2 className="size-3.5 text-success" />
            ) : (
              <XCircle className="size-3.5 text-danger" />
            )}
            {c.method} {c.url} · {c.status || "–"} · {c.durationMs} ms
          </p>
        ))}
      </div>
    </Alert>
  );
}

function CustomForm({
  c,
  set,
  variables,
  secretSet,
  secret,
  setSecret,
}: {
  c: CustomAPI;
  set: (c: Partial<CustomAPI>) => void;
  variables: string[];
  secretSet: boolean;
  secret: string;
  setSecret: (v: string) => void;
}) {
  const { t } = useTranslation();
  const secretLabel = useMemo(() => {
    switch (c.auth) {
      case "bearer":
        return t("integrations.customForm.token");
      case "basic":
        return t("integrations.fields.password");
      case "apikey":
        return t("integrations.fields.apiKey");
      case "oauth2":
        return t("integrations.fields.clientSecret");
      case "hmac":
        return t("integrations.customForm.hmacKey");
    }
    return "";
  }, [c.auth, t]);
  const row = (children: ReactNode) => (
    <div className="grid gap-3 sm:grid-cols-2">{children}</div>
  );
  return (
    <div className="grid gap-6 lg:grid-cols-[1fr_300px]">
      <div className="space-y-6">
        <Card title={t("integrations.customForm.request")}>
          <div className="space-y-4">
            <div className="grid gap-3 sm:grid-cols-[130px_1fr]">
              <Field label={t("integrations.customForm.method")}>
                {(fid) => (
                  <Select
                    id={fid}
                    value={c.method}
                    onChange={(e) => set({ method: e.target.value })}
                  >
                    {["GET", "POST", "PUT", "PATCH", "DELETE"].map((m) => (
                      <option key={m}>{m}</option>
                    ))}
                  </Select>
                )}
              </Field>
              <Field label="URL *" hint={t("integrations.customForm.urlHint")}>
                {(fid) => (
                  <Input
                    id={fid}
                    value={c.url}
                    placeholder="https://helpdesk.example.com/api/tickets"
                    onChange={(e) => set({ url: e.target.value })}
                    className="font-mono text-xs"
                  />
                )}
              </Field>
            </div>
            {row(
              <>
                <Field label={t("integrations.customForm.auth")}>
                  {(fid) => (
                    <Select
                      id={fid}
                      value={c.auth}
                      onChange={(e) =>
                        set({ auth: e.target.value as CustomAPI["auth"] })
                      }
                    >
                      {(
                        [
                          "none",
                          "bearer",
                          "basic",
                          "apikey",
                          "oauth2",
                          "hmac",
                        ] as const
                      ).map((a) => (
                        <option key={a} value={a}>
                          {t(`integrations.customForm.authKinds.${a}`)}
                        </option>
                      ))}
                    </Select>
                  )}
                </Field>
                {c.auth === "basic" && (
                  <Field label={t("integrations.fields.username")}>
                    {(fid) => (
                      <Input
                        id={fid}
                        value={c.username}
                        onChange={(e) => set({ username: e.target.value })}
                      />
                    )}
                  </Field>
                )}
                {c.auth === "apikey" && (
                  <Field label={t("integrations.customForm.headerName")}>
                    {(fid) => (
                      <Input
                        id={fid}
                        value={c.headerName}
                        placeholder="X-API-Key"
                        onChange={(e) => set({ headerName: e.target.value })}
                        className="font-mono text-xs"
                      />
                    )}
                  </Field>
                )}
                {c.auth === "oauth2" && (
                  <Field label={t("integrations.customForm.tokenUrl")}>
                    {(fid) => (
                      <Input
                        id={fid}
                        value={c.tokenUrl}
                        onChange={(e) => set({ tokenUrl: e.target.value })}
                        className="font-mono text-xs"
                      />
                    )}
                  </Field>
                )}
                {c.auth === "oauth2" && (
                  <Field label={t("integrations.fields.clientId")}>
                    {(fid) => (
                      <Input
                        id={fid}
                        value={c.clientId}
                        onChange={(e) => set({ clientId: e.target.value })}
                      />
                    )}
                  </Field>
                )}
                {c.auth === "oauth2" && (
                  <Field label={t("integrations.customForm.scope")}>
                    {(fid) => (
                      <Input
                        id={fid}
                        value={c.scope}
                        onChange={(e) => set({ scope: e.target.value })}
                      />
                    )}
                  </Field>
                )}
                {c.auth !== "none" && (
                  <SecretField
                    label={secretLabel}
                    set={secretSet}
                    value={secret}
                    onChange={setSecret}
                    hint={
                      c.auth === "hmac"
                        ? t("integrations.customForm.hmacHint")
                        : undefined
                    }
                  />
                )}
              </>,
            )}
            <Field label={t("integrations.customForm.headers")}>
              {() => (
                <div className="space-y-2">
                  {c.headers.map((h, n) => (
                    <div
                      key={n}
                      className="grid grid-cols-[1fr_1fr_auto] gap-2"
                    >
                      <Input
                        value={h.name}
                        placeholder={t("integrations.customForm.headerName")}
                        aria-label={t("integrations.customForm.headerName")}
                        onChange={(e) =>
                          set({
                            headers: c.headers.map((x, m) =>
                              m === n ? { ...x, name: e.target.value } : x,
                            ),
                          })
                        }
                        className="font-mono text-xs"
                      />
                      <Input
                        value={h.value}
                        placeholder={t("integrations.customForm.headerValue")}
                        aria-label={t("integrations.customForm.headerValue")}
                        onChange={(e) =>
                          set({
                            headers: c.headers.map((x, m) =>
                              m === n ? { ...x, value: e.target.value } : x,
                            ),
                          })
                        }
                        className="font-mono text-xs"
                      />
                      <Button
                        variant="ghost"
                        aria-label={t("common.delete")}
                        icon={<Trash2 className="size-4" />}
                        onClick={() =>
                          set({ headers: c.headers.filter((_, m) => m !== n) })
                        }
                      />
                    </div>
                  ))}
                  {c.headers.length < 20 && (
                    <Button
                      variant="secondary"
                      size="sm"
                      icon={<Plus className="size-4" />}
                      onClick={() =>
                        set({
                          headers: [...c.headers, { name: "", value: "" }],
                        })
                      }
                    >
                      {t("integrations.customForm.addHeader")}
                    </Button>
                  )}
                </div>
              )}
            </Field>
            <Field label={t("integrations.customForm.contentType")}>
              {(fid) => (
                <Select
                  id={fid}
                  value={c.contentType}
                  onChange={(e) =>
                    set({
                      contentType: e.target.value as CustomAPI["contentType"],
                    })
                  }
                  className="sm:w-60"
                >
                  <option value="json">JSON</option>
                  <option value="form">Form (x-www-form-urlencoded)</option>
                  <option value="xml">XML</option>
                  <option value="text">
                    {t("integrations.customForm.text")}
                  </option>
                </Select>
              )}
            </Field>
            <Field
              label={t("integrations.customForm.body")}
              hint={t("integrations.customForm.bodyHint")}
            >
              {(fid) => (
                <textarea
                  id={fid}
                  rows={10}
                  value={c.body}
                  spellCheck={false}
                  onChange={(e) => set({ body: e.target.value })}
                  className="w-full rounded-md border border-line bg-surface px-3 py-2 font-mono text-xs leading-5 outline-none focus-visible:ring-2 focus-visible:ring-primary"
                />
              )}
            </Field>
          </div>
        </Card>
        <Card title={t("integrations.customForm.response")}>
          <div className="grid gap-4 sm:grid-cols-3">
            <Field
              label={t("integrations.customForm.numberPath")}
              hint={t("integrations.customForm.numberPathHint")}
            >
              {(fid) => (
                <Input
                  id={fid}
                  value={c.numberPath}
                  placeholder="$.ticket.number"
                  onChange={(e) => set({ numberPath: e.target.value })}
                  className="font-mono text-xs"
                />
              )}
            </Field>
            <Field label={t("integrations.customForm.urlPath")}>
              {(fid) => (
                <Input
                  id={fid}
                  value={c.urlPath}
                  placeholder="$.ticket.url"
                  onChange={(e) => set({ urlPath: e.target.value })}
                  className="font-mono text-xs"
                />
              )}
            </Field>
            <Field label={t("integrations.customForm.timeout")}>
              {(fid) => (
                <Input
                  id={fid}
                  type="number"
                  min={1}
                  max={120}
                  value={c.timeoutSec}
                  onChange={(e) =>
                    set({ timeoutSec: Number(e.target.value) || 15 })
                  }
                />
              )}
            </Field>
          </div>
        </Card>
      </div>
      <Card
        title={t("integrations.customForm.variables")}
        description={t("integrations.customForm.variablesHint")}
      >
        <div className="flex flex-wrap gap-1.5">
          {variables.map((v) => (
            <button
              key={v}
              type="button"
              onClick={() => set({ body: c.body + `{{${v}}}` })}
              className="rounded-sm bg-primary/10 px-1.5 py-0.5 font-mono text-xs text-primary hover:bg-primary/20"
            >{`{{${v}}}`}</button>
          ))}
        </div>
        <p className="mt-4 text-xs text-muted">
          {t("integrations.customForm.filters")}
        </p>
      </Card>
    </div>
  );
}
