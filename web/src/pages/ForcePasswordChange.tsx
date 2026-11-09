import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useAuth } from "../auth";
import { AuthLayout } from "../components/AuthLayout";
import { PasswordForm } from "../components/PasswordForm";
import { TotpSetup } from "../components/TotpSetup";
import { Button } from "../components/ui";
import type { Me } from "../api";

// First sign-in: a new password is required; setting up TOTP is offered after that.
export function ForcePasswordChange() {
  const { t } = useTranslation();
  const { me, setMe, logout } = useAuth();
  const [changed, setChanged] = useState<Me | null>(null);
  const [setupTotp, setSetupTotp] = useState(false);

  if (changed) {
    return (
      <AuthLayout title={t("firstLogin.totpTitle")} subtitle={t("firstLogin.totpSubtitle")}>
        {setupTotp ? (
          <TotpSetup onDone={setMe} />
        ) : (
          <div className="flex flex-col gap-2 sm:flex-row">
            <Button onClick={() => setSetupTotp(true)}>{t("firstLogin.setupTotp")}</Button>
            <Button variant="secondary" onClick={() => setMe(changed)}>
              {t("firstLogin.skip")}
            </Button>
          </div>
        )}
      </AuthLayout>
    );
  }

  return (
    <AuthLayout title={t("firstLogin.title")} subtitle={t("firstLogin.subtitle", { user: me?.username })}>
      <PasswordForm
        submitLabel={t("firstLogin.submit")}
        onDone={(next) => (next.source === "local" && !next.totpEnabled ? setChanged(next) : setMe(next))}
      />
      <button type="button" onClick={() => void logout()} className="mt-4 text-sm text-muted hover:text-fg">
        {t("app.signOut")}
      </button>
    </AuthLayout>
  );
}
