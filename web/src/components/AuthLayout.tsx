import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { useBranding } from "../branding";
import { languages } from "../i18n";

// Centered card for the sign-in, password change and TOTP screens.
export function AuthLayout({ title, subtitle, children }: { title: string; subtitle?: string; children: ReactNode }) {
  const { t, i18n } = useTranslation();
  const { branding, logoUrl } = useBranding();
  return (
    <div className="grid min-h-full place-items-center bg-bg px-4 py-10">
      <div className="w-full max-w-[400px]">
        <div className="mb-8 flex flex-col items-center gap-3 text-center">
          <img src={logoUrl} alt="" className="size-12" />
          <p className="text-lg font-semibold tracking-tight">{branding.productName}</p>
        </div>
        <div className="rounded-lg border border-line bg-surface p-6 shadow-sm sm:p-8">
          <h1 className="text-xl font-semibold tracking-tight">{title}</h1>
          {subtitle && <p className="mt-1 text-sm text-muted">{subtitle}</p>}
          <div className="mt-6">{children}</div>
        </div>
        <div className="mt-6 flex justify-center">
          <label className="sr-only" htmlFor="auth-lang">
            {t("app.language")}
          </label>
          <select
            id="auth-lang"
            value={i18n.resolvedLanguage}
            onChange={(e) => void i18n.changeLanguage(e.target.value)}
            className="rounded-md bg-transparent px-2 py-1 text-sm text-muted outline-none hover:text-fg focus-visible:ring-2 focus-visible:ring-primary"
          >
            {languages.map((l) => (
              <option key={l.code} value={l.code}>
                {l.label}
              </option>
            ))}
          </select>
        </div>
      </div>
    </div>
  );
}
