import { useEffect, useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { Bot, KeyRound, Plus, Trash2 } from "lucide-react";
import { del, get, post, type McpLevel, type McpToken, type McpTokenCreated } from "../api";
import { Alert, Badge, Button, Card, CopyField, Field, Input, Modal, Select, Spinner, formatDate, useErrorText } from "./ui";

export const mcpLevels: McpLevel[] = ["read", "write", "full"];

export function LevelBadge({ level }: { level: McpLevel }) {
  const { t } = useTranslation();
  const tone = level === "read" ? "success" : level === "write" ? "warning" : "danger";
  return <Badge tone={tone}>{t(`mcp.level.${level}`)}</Badge>;
}

// Snippets that connect an assistant with a personal token.
export function connectSnippets(url: string, token: string) {
  return {
    claudeCode: `claude mcp add --transport http fleetwarden ${url} --header "Authorization: Bearer ${token}"`,
    json: JSON.stringify({ mcpServers: { fleetwarden: { type: "http", url, headers: { Authorization: `Bearer ${token}` } } } }, null, 2),
  };
}

// McpTokens lists the user's tokens and connections (everyone's with all) and
// makes new personal tokens.
export function McpTokens({ all, url, maxLevel, enabled }: { all?: boolean; url: string; maxLevel: McpLevel; enabled: boolean }) {
  const { t, i18n } = useTranslation();
  const errorText = useErrorText();
  const [list, setList] = useState<McpToken[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [open, setOpen] = useState(false);
  const [created, setCreated] = useState<McpTokenCreated | null>(null);
  const [revoke, setRevoke] = useState<McpToken | null>(null);

  const load = () =>
    get<{ tokens: McpToken[] }>(`/mcp/tokens${all ? "?all=1" : ""}`)
      .then((r) => setList(r.tokens))
      .catch((e) => setError(errorText(e)));
  useEffect(() => {
    void load();
  }, [all]);

  const doRevoke = async () => {
    if (!revoke) return;
    try {
      await del(`/mcp/tokens/${revoke.id}`);
      setRevoke(null);
      void load();
    } catch (e) {
      setError(errorText(e));
    }
  };

  return (
    <Card
      title={all ? t("mcp.tokensAll") : t("mcp.tokensMine")}
      description={t("mcp.tokensHint")}
      actions={
        <Button icon={<Plus className="size-4" />} disabled={!enabled} onClick={() => setOpen(true)}>
          {t("mcp.newToken")}
        </Button>
      }
    >
      {error && <Alert tone="danger">{error}</Alert>}
      {!enabled && <Alert tone="info">{t("mcp.disabledHint")}</Alert>}
      {!list ? (
        !error && <Spinner />
      ) : list.length === 0 ? (
        <div className="grid place-items-center gap-2 py-10 text-center text-sm text-muted">
          <Bot className="size-8 text-muted/60" />
          <p>{t("mcp.noTokens")}</p>
        </div>
      ) : (
        <div className="-mx-5 -my-5 overflow-x-auto">
          <table className="w-full min-w-[760px] text-sm">
            <thead className="border-b border-line text-left text-xs text-muted uppercase">
              <tr>
                <th className="px-5 py-3 font-medium">{t("mcp.tokenName")}</th>
                {all && <th className="px-3 py-3 font-medium">{t("mcp.user")}</th>}
                <th className="px-3 py-3 font-medium">{t("mcp.access")}</th>
                <th className="px-3 py-3 font-medium">{t("mcp.lastUsed")}</th>
                <th className="px-3 py-3 font-medium">{t("mcp.expires")}</th>
                <th className="px-5 py-3 text-right font-medium">
                  <span className="sr-only">{t("common.actions")}</span>
                </th>
              </tr>
            </thead>
            <tbody className="divide-y divide-line">
              {list.map((tk) => (
                <tr key={tk.id}>
                  <td className="px-5 py-3">
                    <div className="flex items-center gap-2 font-medium">
                      {tk.kind === "oauth" ? <Bot className="size-4 text-primary" /> : <KeyRound className="size-4 text-muted" />}
                      {tk.name}
                    </div>
                    <div className="mt-0.5 text-xs text-muted">
                      {tk.kind === "oauth" ? t("mcp.kindOauth") : <span className="font-mono">{tk.prefix}…</span>} · {t("mcp.created", { date: formatDate(tk.createdAt, i18n.language) })}
                    </div>
                  </td>
                  {all && <td className="px-3 py-3">{tk.username}</td>}
                  <td className="px-3 py-3">
                    <LevelBadge level={tk.access} />
                  </td>
                  <td className="px-3 py-3 text-muted">
                    {tk.lastUsedAt ? (
                      <>
                        {formatDate(tk.lastUsedAt, i18n.language)}
                        <div className="font-mono text-xs">{tk.lastIp}</div>
                      </>
                    ) : (
                      t("mcp.never")
                    )}
                  </td>
                  <td className="px-3 py-3 text-muted">
                    {tk.kind === "oauth" ? t("mcp.oauthExpiry", { date: formatDate(tk.refreshExpiresAt, i18n.language) }) : tk.expiresAt ? formatDate(tk.expiresAt, i18n.language) : t("mcp.noExpiry")}
                  </td>
                  <td className="px-5 py-3 text-right">
                    <Button variant="ghost" size="sm" icon={<Trash2 className="size-4" />} onClick={() => setRevoke(tk)}>
                      {t("mcp.revoke")}
                    </Button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      <NewToken
        open={open}
        maxLevel={maxLevel}
        onClose={() => setOpen(false)}
        onCreated={(c) => {
          setOpen(false);
          setCreated(c);
          void load();
        }}
      />
      <Modal open={!!created} onClose={() => setCreated(null)} title={t("mcp.tokenCreated")} wide footer={<Button onClick={() => setCreated(null)}>{t("common.done")}</Button>}>
        {created && <CreatedToken url={url} token={created} />}
      </Modal>
      <Modal
        open={!!revoke}
        onClose={() => setRevoke(null)}
        title={t("mcp.revokeTitle")}
        footer={
          <>
            <Button variant="secondary" onClick={() => setRevoke(null)}>
              {t("common.cancel")}
            </Button>
            <Button variant="danger" onClick={doRevoke}>
              {t("mcp.revoke")}
            </Button>
          </>
        }
      >
        <p className="text-sm">{t("mcp.revokeText", { name: revoke?.name ?? "" })}</p>
      </Modal>
    </Card>
  );
}

function NewToken({ open, maxLevel, onClose, onCreated }: { open: boolean; maxLevel: McpLevel; onClose: () => void; onCreated: (t: McpTokenCreated) => void }) {
  const { t } = useTranslation();
  const errorText = useErrorText();
  const [name, setName] = useState("");
  const [access, setAccess] = useState<McpLevel>("read");
  const [days, setDays] = useState(90);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const allowed = mcpLevels.slice(0, mcpLevels.indexOf(maxLevel) + 1);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      onCreated(await post<McpTokenCreated>("/mcp/tokens", { name, access, expiresDays: days }));
      setName("");
    } catch (err) {
      setError(errorText(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Modal open={open} onClose={onClose} title={t("mcp.newToken")}>
      <form id="mcp-new-token" onSubmit={submit} className="space-y-4">
        {error && <Alert tone="danger">{error}</Alert>}
        <Field label={t("mcp.tokenName")} hint={t("mcp.tokenNameHint")}>
          {(id) => <Input id={id} value={name} onChange={(e) => setName(e.target.value)} placeholder="Claude Desktop" required maxLength={100} autoFocus />}
        </Field>
        <fieldset className="space-y-2">
          <legend className="mb-1.5 text-sm font-medium">{t("mcp.access")}</legend>
          {allowed.map((l) => (
            <label key={l} className={`flex cursor-pointer gap-3 rounded-md border px-3 py-2.5 ${access === l ? "border-primary bg-primary/5" : "border-line"}`}>
              <input type="radio" name="access" className="mt-1 accent-primary" checked={access === l} onChange={() => setAccess(l)} />
              <span>
                <span className="flex items-center gap-2 text-sm font-medium">
                  <LevelBadge level={l} />
                </span>
                <span className="mt-0.5 block text-xs text-muted">{t(`mcp.levelHint.${l}`)}</span>
              </span>
            </label>
          ))}
        </fieldset>
        <Field label={t("mcp.expires")}>
          {(id) => (
            <Select id={id} value={days} onChange={(e) => setDays(Number(e.target.value))}>
              {[7, 30, 90, 180, 365].map((d) => (
                <option key={d} value={d}>
                  {t("mcp.days", { count: d })}
                </option>
              ))}
              <option value={0}>{t("mcp.noExpiry")}</option>
            </Select>
          )}
        </Field>
      </form>
      <div className="flex justify-end gap-2">
        <Button variant="secondary" onClick={onClose}>
          {t("common.cancel")}
        </Button>
        <Button type="submit" form="mcp-new-token" loading={busy}>
          {t("mcp.create")}
        </Button>
      </div>
    </Modal>
  );
}

function CreatedToken({ url, token }: { url: string; token: McpTokenCreated }) {
  const { t } = useTranslation();
  const s = connectSnippets(url, token.secret);
  return (
    <div className="space-y-4">
      <Alert tone="warning">{t("mcp.copyNow")}</Alert>
      <Field label={t("mcp.token")}>{() => <CopyField value={token.secret} label={t("mcp.token")} />}</Field>
      <Field label="Claude Code">{() => <CopyField value={s.claudeCode} label="Claude Code" />}</Field>
      <div className="space-y-1.5">
        <p className="text-sm font-medium">{t("mcp.jsonConfig")}</p>
        <pre className="overflow-x-auto rounded-md border border-line bg-surface-muted p-3 font-mono text-xs">{s.json}</pre>
        <p className="text-xs text-muted">{t("mcp.jsonHint")}</p>
      </div>
    </div>
  );
}
