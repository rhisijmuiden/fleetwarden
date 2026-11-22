import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { useSearchParams } from "react-router";
import { Bot, Check, ShieldCheck, X } from "lucide-react";
import { get, post, type McpConsent, type McpLevel } from "../api";
import { useAuth } from "../auth";
import { useBranding } from "../branding";
import { LevelBadge, mcpLevels } from "../components/McpTokens";
import { Alert, Button, Spinner, useErrorText } from "../components/ui";

// AuthorizeAssistant is the OAuth consent page: the signed-in user lets an AI
// assistant act on their behalf with the access level they pick.
export function AuthorizeAssistant() {
  const { t } = useTranslation();
  const { me } = useAuth();
  const { branding } = useBranding();
  const errorText = useErrorText();
  const [params] = useSearchParams();
  const [c, setC] = useState<McpConsent | null>(null);
  const [access, setAccess] = useState<McpLevel>("read");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState<"allow" | "deny" | null>(null);
  const clientId = params.get("client_id") ?? "";
  const redirectUri = params.get("redirect_uri") ?? "";

  useEffect(() => {
    const q = new URLSearchParams({ client_id: clientId, redirect_uri: redirectUri, scope: params.get("scope") ?? "" });
    get<McpConsent>(`/mcp/oauth/request?${q}`)
      .then((r) => {
        setC(r);
        setAccess(r.requested);
      })
      .catch((e) => setError(errorText(e)));
  }, [clientId, redirectUri]);

  const answer = async (deny: boolean) => {
    setBusy(deny ? "deny" : "allow");
    setError(null);
    try {
      const r = await post<{ redirect: string }>("/mcp/oauth/approve", {
        clientId,
        redirectUri,
        codeChallenge: params.get("code_challenge") ?? "",
        state: params.get("state") ?? "",
        access,
        deny,
      });
      window.location.assign(r.redirect);
    } catch (e) {
      setError(errorText(e));
      setBusy(null);
    }
  };

  const allowed = c ? mcpLevels.slice(0, mcpLevels.indexOf(c.maxLevel) + 1) : [];
  return (
    <div className="mx-auto max-w-lg py-6">
      <section className="rounded-lg border border-line bg-surface shadow-card">
        <div className="flex flex-col items-center gap-3 border-b border-line px-6 py-6 text-center">
          <div className="flex items-center gap-3">
            <span className="grid size-12 place-items-center rounded-full bg-primary/10 text-primary">
              <Bot className="size-6" />
            </span>
            <span className="h-px w-8 bg-line" />
            <span className="grid size-12 place-items-center rounded-full bg-primary/10 text-primary">
              <ShieldCheck className="size-6" />
            </span>
          </div>
          <h1 className="text-xl font-semibold tracking-tight">{c ? t("mcp.consentTitle", { client: c.clientName, product: branding.productName }) : t("mcp.consentLoading")}</h1>
          {c && <p className="text-sm text-muted">{t("mcp.consentSubtitle", { client: c.clientName, user: me?.displayName || me?.username, host: c.redirectHost })}</p>}
        </div>
        <div className="space-y-5 px-6 py-5">
          {error && <Alert tone="danger">{error}</Alert>}
          {!c ? (
            !error && <Spinner />
          ) : (
            <>
              <fieldset className="space-y-2">
                <legend className="mb-1.5 text-sm font-medium">{t("mcp.consentAccess")}</legend>
                {allowed.map((l) => (
                  <label key={l} className={`flex cursor-pointer gap-3 rounded-md border px-3 py-2.5 transition-colors ${access === l ? "border-primary bg-primary/5" : "border-line hover:bg-surface-muted"}`}>
                    <input type="radio" name="access" className="mt-1 accent-primary" checked={access === l} onChange={() => setAccess(l)} />
                    <span>
                      <LevelBadge level={l} />
                      <span className="mt-1 block text-xs text-muted">{t(`mcp.levelHint.${l}`)}</span>
                    </span>
                  </label>
                ))}
              </fieldset>
              <ul className="space-y-1.5 text-xs text-muted">
                <li>• {t("mcp.consentRole")}</li>
                <li>• {t("mcp.consentAudit")}</li>
                <li>• {t("mcp.consentRevoke")}</li>
              </ul>
              <div className="flex flex-wrap justify-end gap-2">
                <Button variant="secondary" icon={<X className="size-4" />} loading={busy === "deny"} disabled={!!busy} onClick={() => answer(true)}>
                  {t("mcp.deny")}
                </Button>
                <Button icon={<Check className="size-4" />} loading={busy === "allow"} disabled={!!busy} onClick={() => answer(false)}>
                  {t("mcp.allow")}
                </Button>
              </div>
            </>
          )}
        </div>
      </section>
    </div>
  );
}
