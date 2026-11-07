import { useCallback, useEffect, useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { ExternalLink, FlaskConical, Plus, Trash2 } from "lucide-react";
import { del, get, post, put, type AuthSettings, type Role, type RoleMapping } from "../../api";
import { GroupScope, useGroups } from "../../components/GroupScope";
import { Alert, Badge, Button, Card, CopyField, Field, Input, PageHeader, Select, Spinner, Switch, useErrorText } from "../../components/ui";

const guideUrl = (lang: string) =>
  `https://github.com/rhisijmuiden/fleetwarden/blob/test/docs/${lang === "nl" ? "entra-sso.nl.md" : "entra-sso.md"}`;

export function Authentication() {
  const { t, i18n } = useTranslation();
  const errorText = useErrorText();
  const [s, setS] = useState<AuthSettings | null>(null);
  const [secret, setSecret] = useState("");
  const [networks, setNetworks] = useState("");
  const [busy, setBusy] = useState<"save" | "test" | null>(null);
  const [msg, setMsg] = useState<{ tone: "success" | "danger"; text: string } | null>(null);

  const apply = (v: AuthSettings) => {
    setS(v);
    setNetworks(v.breakGlassNetworks.join(", "));
    setSecret("");
  };
  useEffect(() => {
    get<AuthSettings>("/settings/authentication").then(apply).catch((e) => setMsg({ tone: "danger", text: errorText(e) }));
  }, []);

  if (!s) return <Spinner />;
  const body = () => ({
    ...s,
    clientSecret: secret || undefined,
    breakGlassNetworks: networks.split(/[\s,]+/).filter(Boolean),
  });

  const save = async (e: FormEvent) => {
    e.preventDefault();
    setBusy("save");
    setMsg(null);
    try {
      apply(await put<AuthSettings>("/settings/authentication", body()));
      setMsg({ tone: "success", text: t("common.saved") });
    } catch (err) {
      setMsg({ tone: "danger", text: errorText(err) });
    } finally {
      setBusy(null);
    }
  };
  const test = async () => {
    setBusy("test");
    setMsg(null);
    try {
      await post("/settings/authentication/test", body());
      setMsg({ tone: "success", text: t("auth.testOk") });
    } catch (err) {
      setMsg({ tone: "danger", text: errorText(err) });
    } finally {
      setBusy(null);
    }
  };

  return (
    <div className="space-y-6">
      <PageHeader title={t("auth.title")} description={t("auth.subtitle")} />
      <form onSubmit={save} className="space-y-6">
        {msg && <Alert tone={msg.tone}>{msg.text}</Alert>}
        <Card
          title={t("auth.entraTitle")}
          description={t("auth.entraSubtitle")}
          actions={
            <a href={guideUrl(i18n.language)} target="_blank" rel="noreferrer" className="inline-flex items-center gap-1.5 text-sm font-medium text-primary hover:underline">
              {t("auth.guide")}
              <ExternalLink className="size-3.5" />
            </a>
          }
        >
          <div className="space-y-5">
            <Switch checked={s.ssoEnabled} onChange={(v) => setS({ ...s, ssoEnabled: v })} label={t("auth.enable")} description={t("auth.enableHint")} />
            <div className="grid gap-4 md:grid-cols-2">
              <Field label={t("auth.tenantId")} hint={t("auth.tenantIdHint")}>
                {(id) => <Input id={id} value={s.tenantId} onChange={(e) => setS({ ...s, tenantId: e.target.value })} placeholder="00000000-0000-0000-0000-000000000000" className="font-mono" />}
              </Field>
              <Field label={t("auth.clientId")} hint={t("auth.clientIdHint")}>
                {(id) => <Input id={id} value={s.clientId} onChange={(e) => setS({ ...s, clientId: e.target.value })} placeholder="00000000-0000-0000-0000-000000000000" className="font-mono" />}
              </Field>
            </div>
            <Field label={t("auth.clientSecret")} hint={s.clientSecretSet ? t("common.secretKept") : t("auth.clientSecretHint")}>
              {(id) => <Input id={id} type="password" autoComplete="off" value={secret} onChange={(e) => setSecret(e.target.value)} placeholder={s.clientSecretSet ? "••••••••" : ""} />}
            </Field>
            <Field label={t("auth.redirectUri")} hint={t("auth.redirectUriHint")}>
              {() => <CopyField value={s.redirectUri} label={t("auth.redirectUri")} />}
            </Field>
            <div className="flex flex-wrap gap-2">
              <Button type="submit" loading={busy === "save"}>
                {t("common.save")}
              </Button>
              <Button variant="secondary" icon={<FlaskConical className="size-4" />} loading={busy === "test"} onClick={() => void test()} disabled={!s.tenantId || !s.clientId}>
                {t("auth.test")}
              </Button>
            </div>
          </div>
        </Card>

        <Card title={t("auth.breakGlassTitle")} description={t("auth.breakGlassSubtitle")}>
          <div className="max-w-xl space-y-4">
            <Field label={t("auth.networks")} hint={t("auth.networksHint", { ip: s.clientIp })}>
              {(id) => <Input id={id} value={networks} onChange={(e) => setNetworks(e.target.value)} placeholder="192.168.1.0/24, 10.0.0.0/8" />}
            </Field>
            <Button type="submit" loading={busy === "save"}>
              {t("common.save")}
            </Button>
          </div>
        </Card>
      </form>
      <Mappings />
    </div>
  );
}

const roles: Role[] = ["administrator", "technician", "readonly"];

function Mappings() {
  const { t } = useTranslation();
  const errorText = useErrorText();
  const [list, setList] = useState<RoleMapping[] | null>(null);
  const [claim, setClaim] = useState<"group" | "role">("group");
  const [value, setValue] = useState("");
  const [role, setRole] = useState<Role>("technician");
  const [scoped, setScoped] = useState(false);
  const [scopeGroups, setScopeGroups] = useState<number[]>([]);
  const [error, setError] = useState<string | null>(null);
  const groups = useGroups();
  const groupName = (id: number) => groups?.find((g) => g.id === id)?.name ?? `#${id}`;

  const load = useCallback(() => {
    get<RoleMapping[]>("/settings/role-mappings").then(setList).catch((e) => setError(errorText(e)));
  }, []);
  useEffect(load, [load]);

  const add = async (e: FormEvent) => {
    e.preventDefault();
    setError(null);
    try {
      await post("/settings/role-mappings", { claim, value, role, scoped: scoped && role !== "administrator", groups: scopeGroups });
      setValue("");
      setScoped(false);
      setScopeGroups([]);
      load();
    } catch (err) {
      setError(errorText(err));
    }
  };

  return (
    <Card title={t("auth.mappingsTitle")} description={t("auth.mappingsSubtitle")}>
      <div className="space-y-5">
        {error && <Alert tone="danger">{error}</Alert>}
        <form onSubmit={add} className="grid gap-3 md:grid-cols-[160px_1fr_180px_auto] md:items-end">
          <Field label={t("auth.claim")}>
            {(id) => (
              <Select id={id} value={claim} onChange={(e) => setClaim(e.target.value as "group" | "role")}>
                <option value="group">{t("auth.claims.group")}</option>
                <option value="role">{t("auth.claims.role")}</option>
              </Select>
            )}
          </Field>
          <Field label={claim === "group" ? t("auth.groupId") : t("auth.appRole")}>
            {(id) => (
              <Input id={id} required value={value} onChange={(e) => setValue(e.target.value)} placeholder={claim === "group" ? "00000000-0000-0000-0000-000000000000" : "FleetWarden.Admin"} className="font-mono" />
            )}
          </Field>
          <Field label={t("users.role")}>
            {(id) => (
              <Select id={id} value={role} onChange={(e) => setRole(e.target.value as Role)}>
                {roles.map((r) => (
                  <option key={r} value={r}>
                    {t(`roles.${r}`)}
                  </option>
                ))}
              </Select>
            )}
          </Field>
          <Button type="submit" icon={<Plus className="size-4" />}>
            {t("common.add")}
          </Button>
          <div className="md:col-span-4">
            <GroupScope
              scoped={scoped && role !== "administrator"}
              groups={scopeGroups}
              disabled={role === "administrator"}
              onChange={(sc, g) => {
                setScoped(sc);
                setScopeGroups(g);
              }}
            />
          </div>
        </form>
        {!list ? (
          <Spinner />
        ) : list.length === 0 ? (
          <p className="rounded-md border border-dashed border-line px-4 py-6 text-center text-sm text-muted">{t("auth.noMappings")}</p>
        ) : (
          <ul className="divide-y divide-line rounded-md border border-line">
            {list.map((m) => (
              <li key={m.id} className="flex flex-wrap items-center gap-3 px-4 py-2.5 text-sm">
                <Badge tone={m.claim === "group" ? "neutral" : "primary"}>{t(`auth.claims.${m.claim}`)}</Badge>
                <code className="min-w-0 flex-1 truncate font-mono text-xs">{m.value}</code>
                <span className="text-muted">→</span>
                <span className="font-medium">{t(`roles.${m.role}`)}</span>
                {m.scoped && <Badge tone="primary">{m.groups.length ? m.groups.map(groupName).join(", ") : t("scope.noneSelected")}</Badge>}
                <Button
                  variant="ghost"
                  size="sm"
                  aria-label={t("common.delete")}
                  icon={<Trash2 className="size-4" />}
                  onClick={() => void del(`/settings/role-mappings/${m.id}`).then(load)}
                />
              </li>
            ))}
          </ul>
        )}
        <p className="text-xs text-muted">{t("auth.mappingsNote")}</p>
      </div>
    </Card>
  );
}
