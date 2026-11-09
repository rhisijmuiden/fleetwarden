import { useCallback, useEffect, useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { RefreshCw, RotateCcw, Send } from "lucide-react";
import { get, post, put, type MailConfig, type MailLogEntry } from "../../api";
import { useAuth } from "../../auth";
import { Alert, Badge, Button, Card, Field, Input, PageHeader, Select, Spinner, Switch, formatDate, useErrorText } from "../../components/ui";

const defaultPorts = { starttls: 587, tls: 465, none: 25 } as const;

export function Mail() {
  const { t } = useTranslation();
  const { me } = useAuth();
  const errorText = useErrorText();
  const [c, setC] = useState<MailConfig | null>(null);
  const [password, setPassword] = useState("");
  const [oauthSecret, setOauthSecret] = useState("");
  const [to, setTo] = useState("");
  const [busy, setBusy] = useState<"save" | "test" | null>(null);
  const [msg, setMsg] = useState<{ tone: "success" | "danger"; text: string } | null>(null);
  const [logKey, setLogKey] = useState(0);

  useEffect(() => {
    get<MailConfig>("/settings/mail").then(setC).catch((e) => setMsg({ tone: "danger", text: errorText(e) }));
    setTo(me?.email ?? "");
  }, []);
  if (!c) return <Spinner />;

  const body = () => ({ ...c, password: password || undefined, oauthSecret: oauthSecret || undefined });
  const save = async (e: FormEvent) => {
    e.preventDefault();
    setBusy("save");
    setMsg(null);
    try {
      setC(await put<MailConfig>("/settings/mail", body()));
      setPassword("");
      setOauthSecret("");
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
      await post("/settings/mail/test", { ...body(), to });
      setMsg({ tone: "success", text: t("mail.testOk", { to }) });
    } catch (err) {
      setMsg({ tone: "danger", text: errorText(err) });
    } finally {
      setBusy(null);
      setLogKey((k) => k + 1);
    }
  };

  const needsPassword = c.auth === "plain" || c.auth === "login";
  return (
    <div className="space-y-6">
      <PageHeader title={t("mail.title")} description={t("mail.subtitle")} />
      <form onSubmit={save} className="space-y-6">
        {msg && <Alert tone={msg.tone}>{msg.text}</Alert>}
        <Card title={t("mail.server")}>
          <div className="space-y-5">
            <Switch checked={c.enabled} onChange={(v) => setC({ ...c, enabled: v })} label={t("mail.enable")} description={t("mail.enableHint")} />
            <div className="grid gap-4 md:grid-cols-[1fr_110px_240px]">
              <Field label={t("mail.host")}>{(id) => <Input id={id} value={c.host} onChange={(e) => setC({ ...c, host: e.target.value })} placeholder="smtp.office365.com" />}</Field>
              <Field label={t("mail.port")}>
                {(id) => <Input id={id} type="number" min={1} max={65535} value={c.port} onChange={(e) => setC({ ...c, port: Number(e.target.value) })} />}
              </Field>
              <Field label={t("mail.security")}>
                {(id) => (
                  <Select
                    id={id}
                    value={c.security}
                    onChange={(e) => {
                      const sec = e.target.value as MailConfig["security"];
                      setC({ ...c, security: sec, port: c.port === defaultPorts[c.security] ? defaultPorts[sec] : c.port });
                    }}
                  >
                    <option value="starttls">{t("mail.securityStarttls")}</option>
                    <option value="tls">{t("mail.securityTls")}</option>
                    <option value="none">{t("mail.securityNone")}</option>
                  </Select>
                )}
              </Field>
            </div>
            {c.security === "none" && <Alert tone="warning">{t("mail.noneWarning")}</Alert>}
            <div className="grid gap-4 md:grid-cols-2">
              <Field label={t("mail.auth")}>
                {(id) => (
                  <Select id={id} value={c.auth} onChange={(e) => setC({ ...c, auth: e.target.value as MailConfig["auth"] })}>
                    <option value="plain">{t("mail.authPlain")}</option>
                    <option value="login">{t("mail.authLogin")}</option>
                    <option value="xoauth2">{t("mail.authXoauth2")}</option>
                    <option value="none">{t("mail.authNone")}</option>
                  </Select>
                )}
              </Field>
              {c.auth !== "none" && (
                <Field label={t("mail.username")}>{(id) => <Input id={id} value={c.username} autoComplete="off" onChange={(e) => setC({ ...c, username: e.target.value })} />}</Field>
              )}
            </div>
            {needsPassword && (
              <Field label={t("mail.password")} hint={c.passwordSet ? t("common.secretKept") : undefined}>
                {(id) => <Input id={id} type="password" autoComplete="new-password" value={password} placeholder={c.passwordSet ? "••••••••" : ""} onChange={(e) => setPassword(e.target.value)} />}
              </Field>
            )}
            {c.auth === "xoauth2" && (
              <div className="space-y-4 rounded-md border border-line bg-surface-muted/50 p-4">
                <p className="text-sm text-muted">{t("mail.oauthHint")}</p>
                <div className="grid gap-4 md:grid-cols-2">
                  <Field label={t("auth.tenantId")}>{(id) => <Input id={id} value={c.oauthTenant} className="font-mono" onChange={(e) => setC({ ...c, oauthTenant: e.target.value })} />}</Field>
                  <Field label={t("auth.clientId")}>{(id) => <Input id={id} value={c.oauthClientId} className="font-mono" onChange={(e) => setC({ ...c, oauthClientId: e.target.value })} />}</Field>
                </div>
                <Field label={t("auth.clientSecret")} hint={c.oauthSecretSet ? t("common.secretKept") : undefined}>
                  {(id) => <Input id={id} type="password" autoComplete="off" value={oauthSecret} placeholder={c.oauthSecretSet ? "••••••••" : ""} onChange={(e) => setOauthSecret(e.target.value)} />}
                </Field>
              </div>
            )}
          </div>
        </Card>
        <Card title={t("mail.sender")}>
          <div className="grid gap-4 md:grid-cols-3">
            <Field label={t("mail.fromName")}>{(id) => <Input id={id} value={c.fromName} onChange={(e) => setC({ ...c, fromName: e.target.value })} placeholder="FleetWarden" />}</Field>
            <Field label={t("mail.fromAddress")}>
              {(id) => <Input id={id} type="email" value={c.fromAddress} onChange={(e) => setC({ ...c, fromAddress: e.target.value })} placeholder="fleetwarden@example.com" />}
            </Field>
            <Field label={t("mail.helo")} hint={t("mail.heloHint")}>{(id) => <Input id={id} value={c.heloName} onChange={(e) => setC({ ...c, heloName: e.target.value })} />}</Field>
          </div>
          <div className="mt-5 flex flex-wrap items-end gap-3 border-t border-line pt-5">
            <Button type="submit" loading={busy === "save"}>
              {t("common.save")}
            </Button>
            <div className="flex flex-1 flex-wrap items-end justify-end gap-2">
              <Field label={t("mail.testTo")} className="w-full sm:w-72">
                {(id) => <Input id={id} type="email" value={to} onChange={(e) => setTo(e.target.value)} placeholder="you@example.com" />}
              </Field>
              <Button variant="secondary" icon={<Send className="size-4" />} loading={busy === "test"} disabled={!to || !c.host} onClick={() => void test()}>
                {t("mail.sendTest")}
              </Button>
            </div>
          </div>
        </Card>
      </form>
      <MailLog key={logKey} />
    </div>
  );
}

function MailLog() {
  const { t, i18n } = useTranslation();
  const [list, setList] = useState<MailLogEntry[] | null>(null);
  const load = useCallback(() => {
    get<MailLogEntry[]>("/mail/log").then(setList).catch(() => setList([]));
  }, []);
  useEffect(load, [load]);
  const tone = { sent: "success", queued: "warning", failed: "danger" } as const;
  return (
    <Card
      title={t("mail.log")}
      description={t("mail.logSubtitle")}
      actions={<Button variant="ghost" size="sm" icon={<RefreshCw className="size-4" />} onClick={load} aria-label={t("common.refresh")} />}
    >
      {!list ? (
        <Spinner />
      ) : list.length === 0 ? (
        <p className="py-6 text-center text-sm text-muted">{t("mail.logEmpty")}</p>
      ) : (
        <div className="-mx-5 -my-5 overflow-x-auto">
          <table className="w-full min-w-[640px] text-sm">
            <thead className="border-b border-line text-left text-xs text-muted uppercase">
              <tr>
                <th className="px-5 py-3 font-medium">{t("mail.when")}</th>
                <th className="px-3 py-3 font-medium">{t("mail.to")}</th>
                <th className="px-3 py-3 font-medium">{t("mail.subject")}</th>
                <th className="px-5 py-3 font-medium">{t("mail.status")}</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-line">
              {list.map((m) => (
                <tr key={m.id}>
                  <td className="px-5 py-3 whitespace-nowrap text-muted">{formatDate(m.createdAt, i18n.language)}</td>
                  <td className="px-3 py-3">{m.to}</td>
                  <td className="px-3 py-3">
                    {m.subject}
                    {m.lastError && <p className="mt-0.5 text-xs break-all text-danger">{m.lastError}</p>}
                  </td>
                  <td className="px-5 py-3">
                    <div className="flex items-center gap-2">
                      <Badge tone={tone[m.status]}>{t(`mail.statuses.${m.status}`)}</Badge>
                      {m.status === "failed" && (
                        <Button variant="ghost" size="sm" aria-label={t("mail.retry")} title={t("mail.retry")} icon={<RotateCcw className="size-3.5" />} onClick={() => void post(`/mail/log/${m.id}/retry`).then(load)} />
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
  );
}
