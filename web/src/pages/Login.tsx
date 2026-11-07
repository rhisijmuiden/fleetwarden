import { useEffect, useRef, useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { useSearchParams } from "react-router";
import { KeyRound, LogIn } from "lucide-react";
import { get, post, type AuthConfig, type Me } from "../api";
import { useAuth } from "../auth";
import { AuthLayout } from "../components/AuthLayout";
import { Alert, Button, Field, Input, useErrorText } from "../components/ui";

function MicrosoftLogo() {
  return (
    <svg viewBox="0 0 21 21" className="size-4" aria-hidden="true">
      <rect x="1" y="1" width="9" height="9" fill="#f25022" />
      <rect x="11" y="1" width="9" height="9" fill="#7fba00" />
      <rect x="1" y="11" width="9" height="9" fill="#00a4ef" />
      <rect x="11" y="11" width="9" height="9" fill="#ffb900" />
    </svg>
  );
}

export function Login() {
  const { t } = useTranslation();
  const { setMe } = useAuth();
  const errorText = useErrorText();
  const [params] = useSearchParams();
  const [cfg, setCfg] = useState<AuthConfig | null>(null);
  const [showLocal, setShowLocal] = useState(false);
  const [step, setStep] = useState<"password" | "totp">("password");
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [code, setCode] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const codeRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    get<AuthConfig>("/auth/config").then(setCfg).catch(() => setCfg({ ssoEnabled: false, localLoginAllowed: true, productName: "" }));
  }, []);
  useEffect(() => {
    const e = params.get("error");
    if (e) setError(t(`errors.${e}`, { defaultValue: t("errors.generic") }));
  }, [params, t]);
  useEffect(() => {
    if (step === "totp") codeRef.current?.focus();
  }, [step]);

  const submitPassword = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      const r = await post<{ mfaRequired: boolean; user?: Me }>("/auth/login", { username, password });
      if (r.mfaRequired) setStep("totp");
      else if (r.user) setMe(r.user);
    } catch (err) {
      setError(errorText(err));
    } finally {
      setBusy(false);
    }
  };

  const submitCode = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      const r = await post<{ user: Me }>("/auth/totp", { code });
      setMe(r.user);
    } catch (err) {
      setError(errorText(err));
      setCode("");
    } finally {
      setBusy(false);
    }
  };

  if (step === "totp") {
    return (
      <AuthLayout title={t("login.totpTitle")} subtitle={t("login.totpSubtitle")}>
        <form onSubmit={submitCode} className="space-y-4">
          {error && <Alert tone="danger">{error}</Alert>}
          <Field label={t("login.code")}>
            {(id) => (
              <Input
                id={id}
                ref={codeRef}
                inputMode="numeric"
                autoComplete="one-time-code"
                pattern="[0-9 ]{6,7}"
                maxLength={7}
                required
                value={code}
                onChange={(e) => setCode(e.target.value)}
                className="text-center font-mono text-lg tracking-[0.4em]"
              />
            )}
          </Field>
          <Button type="submit" loading={busy} className="w-full">
            {t("login.verify")}
          </Button>
          <button type="button" className="w-full text-center text-sm text-muted hover:text-fg" onClick={() => { setStep("password"); setError(null); }}>
            {t("login.back")}
          </button>
        </form>
      </AuthLayout>
    );
  }

  const sso = cfg?.ssoEnabled;
  const localVisible = !sso || showLocal;
  return (
    <AuthLayout title={t("login.title")} subtitle={t("login.subtitle")}>
      <div className="space-y-4">
        {error && <Alert tone="danger">{error}</Alert>}
        {sso && (
          <a
            href="/api/v1/auth/sso/start"
            className="flex h-10 w-full items-center justify-center gap-2.5 rounded-md border border-line bg-surface text-sm font-medium hover:bg-surface-muted focus-visible:ring-2 focus-visible:ring-primary outline-none"
          >
            <MicrosoftLogo />
            {t("login.microsoft")}
          </a>
        )}
        {sso && !showLocal && (
          <button type="button" onClick={() => setShowLocal(true)} className="flex w-full items-center justify-center gap-2 text-sm text-muted hover:text-fg">
            <KeyRound className="size-4" />
            {t("login.useLocal")}
          </button>
        )}
        {sso && showLocal && (
          <div className="flex items-center gap-3 text-xs text-muted">
            <span className="h-px flex-1 bg-line" />
            {t("login.localAccount")}
            <span className="h-px flex-1 bg-line" />
          </div>
        )}
        {localVisible && (
          <form onSubmit={submitPassword} className="space-y-4">
            <Field label={t("login.username")}>
              {(id) => <Input id={id} autoComplete="username" autoFocus required value={username} onChange={(e) => setUsername(e.target.value)} />}
            </Field>
            <Field label={t("login.password")}>
              {(id) => <Input id={id} type="password" autoComplete="current-password" required value={password} onChange={(e) => setPassword(e.target.value)} />}
            </Field>
            <Button type="submit" loading={busy} className="w-full" icon={<LogIn className="size-4" />}>
              {t("login.submit")}
            </Button>
          </form>
        )}
      </div>
    </AuthLayout>
  );
}
