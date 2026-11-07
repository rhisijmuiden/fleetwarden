import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { ShieldCheck } from "lucide-react";
import { post, type Me } from "../api";
import { useAuth } from "../auth";
import { languages } from "../i18n";
import { PasswordForm } from "../components/PasswordForm";
import { TotpSetup } from "../components/TotpSetup";
import { Alert, Badge, Button, Card, Field, Input, Modal, PageHeader, Select, useErrorText } from "../components/ui";

export function Profile() {
  const { t } = useTranslation();
  const { me, setMe, setLanguage } = useAuth();
  const errorText = useErrorText();
  const [saved, setSaved] = useState(false);
  const [totpOpen, setTotpOpen] = useState(false);
  const [disableOpen, setDisableOpen] = useState(false);
  const [pw, setPw] = useState("");
  const [error, setError] = useState<string | null>(null);
  if (!me) return null;
  const local = me.source === "local";

  const disable = async (e: FormEvent) => {
    e.preventDefault();
    setError(null);
    try {
      setMe(await post<Me>("/me/totp/disable", { password: pw }));
      setDisableOpen(false);
      setPw("");
    } catch (err) {
      setError(errorText(err));
    }
  };

  return (
    <div className="space-y-6">
      <PageHeader title={t("profile.title")} description={t("profile.subtitle")} />
      <div className="grid gap-6 lg:grid-cols-2">
        <Card title={t("profile.account")}>
          <dl className="grid grid-cols-[auto_1fr] gap-x-6 gap-y-3 text-sm">
            <dt className="text-muted">{t("users.username")}</dt>
            <dd className="font-medium break-all">{me.username}</dd>
            <dt className="text-muted">{t("users.name")}</dt>
            <dd>{me.displayName || "–"}</dd>
            <dt className="text-muted">{t("users.role")}</dt>
            <dd>{t(`roles.${me.role}`)}</dd>
            <dt className="text-muted">{t("users.source")}</dt>
            <dd>
              <Badge tone={local ? "neutral" : "primary"}>{t(`users.sources.${me.source}`)}</Badge>
            </dd>
          </dl>
          <div className="mt-5 max-w-xs">
            <Field label={t("app.language")}>
              {(id) => (
                <Select id={id} value={me.language} onChange={(e) => setLanguage(e.target.value as "en" | "nl")}>
                  {languages.map((l) => (
                    <option key={l.code} value={l.code}>
                      {l.label}
                    </option>
                  ))}
                </Select>
              )}
            </Field>
          </div>
        </Card>

        <Card title={t("profile.twoFactor")} description={local ? t("profile.twoFactorHint") : t("profile.twoFactorEntra")}>
          {local ? (
            me.totpEnabled ? (
              <div className="flex flex-wrap items-center gap-3">
                <Badge tone="success">
                  <ShieldCheck className="mr-1 size-3.5" />
                  {t("profile.totpOn")}
                </Badge>
                <Button variant="secondary" size="sm" onClick={() => setDisableOpen(true)}>
                  {t("profile.totpDisable")}
                </Button>
              </div>
            ) : (
              <div className="flex flex-wrap items-center gap-3">
                <Badge tone="warning">{t("profile.totpOff")}</Badge>
                <Button size="sm" onClick={() => setTotpOpen(true)}>
                  {t("profile.totpSetup")}
                </Button>
              </div>
            )
          ) : (
            <p className="text-sm text-muted">{t("profile.managedByEntra")}</p>
          )}
        </Card>

        {local && (
          <Card title={t("profile.password")} className="lg:col-span-2">
            <div className="max-w-md space-y-4">
              {saved && <Alert tone="success">{t("profile.passwordSaved")}</Alert>}
              <PasswordForm
                onDone={(next) => {
                  setMe(next);
                  setSaved(true);
                }}
              />
            </div>
          </Card>
        )}
      </div>

      <Modal open={totpOpen} onClose={() => setTotpOpen(false)} title={t("profile.totpSetup")}>
        <TotpSetup
          onDone={(next) => {
            setMe(next);
            setTotpOpen(false);
          }}
        />
      </Modal>
      <Modal open={disableOpen} onClose={() => setDisableOpen(false)} title={t("profile.totpDisable")}>
        <form onSubmit={disable} className="space-y-4">
          {error && <Alert tone="danger">{error}</Alert>}
          <p className="text-sm text-muted">{t("profile.totpDisableHint")}</p>
          <Field label={t("password.current")}>
            {(id) => <Input id={id} type="password" required value={pw} onChange={(e) => setPw(e.target.value)} />}
          </Field>
          <Button type="submit" variant="danger">
            {t("profile.totpDisable")}
          </Button>
        </form>
      </Modal>
    </div>
  );
}
