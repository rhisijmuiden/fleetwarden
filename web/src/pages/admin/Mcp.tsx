import { useEffect, useMemo, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Bot, ShieldAlert } from "lucide-react";
import { get, put, type McpLevel, type McpSettingsView } from "../../api";
import { LevelBadge, McpTokens, connectSnippets, mcpLevels } from "../../components/McpTokens";
import { Alert, Button, Card, CopyField, Field, PageHeader, Spinner, Switch, useErrorText } from "../../components/ui";

const groups = ["overview", "devices", "software", "patches", "vulnerabilities", "security", "alerts", "assets", "power", "manage", "actions", "destructive"];

export function Mcp() {
  const { t } = useTranslation();
  const errorText = useErrorText();
  const [v, setV] = useState<McpSettingsView | null>(null);
  const [saved, setSaved] = useState<McpSettingsView | null>(null);
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<{ tone: "success" | "danger"; text: string } | null>(null);
  useEffect(() => {
    get<McpSettingsView>("/mcp/settings")
      .then((s) => {
        setV(s);
        setSaved(s);
      })
      .catch((e) => setMsg({ tone: "danger", text: errorText(e) }));
  }, []);

  const save = async () => {
    if (!v) return;
    setBusy(true);
    setMsg(null);
    try {
      const s = await put<McpSettingsView>("/mcp/settings", { enabled: v.enabled, maxLevel: v.maxLevel, oauth: v.oauth, disabledTools: v.disabledTools });
      setV(s);
      setSaved(s);
      setMsg({ tone: "success", text: t("common.saved") });
    } catch (e) {
      setMsg({ tone: "danger", text: errorText(e) });
    } finally {
      setBusy(false);
    }
  };

  const byGroup = useMemo(() => {
    const m = new Map<string, McpSettingsView["tools"]>();
    for (const tool of v?.tools ?? []) m.set(tool.group, [...(m.get(tool.group) ?? []), tool]);
    return groups.filter((g) => m.has(g)).map((g) => ({ group: g, tools: m.get(g)! }));
  }, [v?.tools]);

  if (!v) return msg ? <Alert tone={msg.tone}>{msg.text}</Alert> : <Spinner />;
  const maxIdx = mcpLevels.indexOf(v.maxLevel);
  const active = v.tools.filter((x) => mcpLevels.indexOf(x.level) <= maxIdx && !v.disabledTools.includes(x.name)).length;
  const dirty = JSON.stringify(saved && { ...saved, tools: 0, url: 0 }) !== JSON.stringify({ ...v, tools: 0, url: 0 });

  return (
    <div className="space-y-6">
      <PageHeader
        title={t("mcp.title")}
        description={t("mcp.subtitle")}
        actions={
          <Button onClick={save} loading={busy} disabled={!dirty}>
            {t("common.save")}
          </Button>
        }
      />
      {msg && <Alert tone={msg.tone}>{msg.text}</Alert>}

      <div className="grid gap-6 lg:grid-cols-2">
        <Card title={t("mcp.server")} description={t("mcp.serverHint")}>
          <div className="space-y-5">
            <Switch checked={v.enabled} onChange={(enabled) => setV({ ...v, enabled })} label={t("mcp.enable")} description={t("mcp.enableHint")} />
            <Switch checked={v.oauth} onChange={(oauth) => setV({ ...v, oauth })} label={t("mcp.oauth")} description={t("mcp.oauthHint")} />
            <fieldset className="space-y-2">
              <legend className="mb-1.5 text-sm font-medium">{t("mcp.maxLevel")}</legend>
              {mcpLevels.map((l) => (
                <label key={l} className={`flex cursor-pointer gap-3 rounded-md border px-3 py-2.5 transition-colors ${v.maxLevel === l ? "border-primary bg-primary/5" : "border-line hover:bg-surface-muted"}`}>
                  <input type="radio" name="maxLevel" className="mt-1 accent-primary" checked={v.maxLevel === l} onChange={() => setV({ ...v, maxLevel: l as McpLevel })} />
                  <span>
                    <LevelBadge level={l} />
                    <span className="mt-1 block text-xs text-muted">{t(`mcp.levelHint.${l}`)}</span>
                  </span>
                </label>
              ))}
            </fieldset>
            {v.maxLevel === "full" && (
              <Alert tone="warning" title={t("mcp.fullWarningTitle")}>
                {t("mcp.fullWarning")}
              </Alert>
            )}
          </div>
        </Card>

        <Card title={t("mcp.connect")} description={t("mcp.connectHint")}>
          <div className="space-y-5">
            <Field label={t("mcp.endpoint")} hint={t("mcp.endpointHint")}>
              {() => <CopyField value={v.url} label={t("mcp.endpoint")} />}
            </Field>
            <ol className="space-y-3 text-sm">
              <Step n={1} title={t("mcp.stepClaudeTitle")}>
                {v.oauth ? t("mcp.stepClaude", { url: v.url }) : t("mcp.stepClaudeNoOauth")}
              </Step>
              <Step n={2} title={t("mcp.stepCodeTitle")}>
                {t("mcp.stepCode")}
                <div className="mt-2">
                  <CopyField value={v.oauth ? `claude mcp add --transport http fleetwarden ${v.url}` : connectSnippets(v.url, "<token>").claudeCode} label="Claude Code" />
                </div>
              </Step>
              <Step n={3} title={t("mcp.stepOtherTitle")}>
                {t("mcp.stepOther")}
              </Step>
            </ol>
            <div className="flex items-start gap-3 rounded-md border border-line bg-surface-muted px-3.5 py-3 text-xs text-muted">
              <ShieldAlert className="mt-0.5 size-4 shrink-0 text-primary" />
              <p>{t("mcp.securityNote")}</p>
            </div>
          </div>
        </Card>
      </div>

      <McpTokens all url={v.url} maxLevel={saved?.maxLevel ?? "read"} enabled={!!saved?.enabled} />

      <Card title={t("mcp.tools")} description={t("mcp.toolsHint", { active, total: v.tools.length })}>
        <div className="grid gap-6 md:grid-cols-2 xl:grid-cols-3">
          {byGroup.map(({ group, tools }) => (
            <div key={group} className="space-y-3">
              <h3 className="text-xs font-semibold tracking-wide text-muted uppercase">{t(`mcp.group.${group}`)}</h3>
              <ul className="space-y-3">
                {tools.map((tool) => {
                  const over = mcpLevels.indexOf(tool.level) > maxIdx;
                  const on = !v.disabledTools.includes(tool.name);
                  return (
                    <li key={tool.name} className={`rounded-md border border-line p-3 ${over ? "opacity-55" : ""}`}>
                      <div className="flex items-start justify-between gap-3">
                        <div className="min-w-0">
                          <p className="flex flex-wrap items-center gap-2 text-sm font-medium">
                            <Bot className="size-3.5 text-muted" />
                            <span className="font-mono text-xs">{tool.name}</span>
                            {tool.level !== "read" && <LevelBadge level={tool.level} />}
                          </p>
                          <p className="mt-1 text-xs text-muted">{t(`mcp.tool.${tool.name}`, { defaultValue: tool.description })}</p>
                          {over && <p className="mt-1 text-xs text-warning">{t("mcp.aboveMax")}</p>}
                        </div>
                        <ToolSwitch
                          checked={on}
                          label={tool.name}
                          onChange={(c) => setV({ ...v, disabledTools: c ? v.disabledTools.filter((n) => n !== tool.name) : [...v.disabledTools, tool.name] })}
                        />
                      </div>
                    </li>
                  );
                })}
              </ul>
            </div>
          ))}
        </div>
      </Card>
    </div>
  );
}

function Step({ n, title, children }: { n: number; title: string; children: ReactNode }) {
  return (
    <li className="flex gap-3">
      <span className="grid size-6 shrink-0 place-items-center rounded-full bg-primary/10 text-xs font-semibold text-primary">{n}</span>
      <div className="min-w-0 flex-1">
        <p className="font-medium">{title}</p>
        <div className="mt-0.5 text-muted">{children}</div>
      </div>
    </li>
  );
}

function ToolSwitch({ checked, onChange, label }: { checked: boolean; onChange: (v: boolean) => void; label: string }) {
  return (
    <button
      type="button"
      role="switch"
      aria-checked={checked}
      aria-label={label}
      onClick={() => onChange(!checked)}
      className={`relative mt-0.5 inline-flex h-5 w-9 shrink-0 rounded-full transition-colors focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary ${checked ? "bg-primary" : "bg-line"}`}
    >
      <span className={`absolute top-0.5 size-4 rounded-full bg-white shadow transition-transform ${checked ? "translate-x-4" : "translate-x-0.5"}`} />
    </button>
  );
}
