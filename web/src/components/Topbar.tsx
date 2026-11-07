import { useTranslation } from "react-i18next";
import { Moon, Sun } from "lucide-react";
import { languages } from "../i18n";
import type { Theme } from "../useTheme";

type Props = { theme: Theme; onToggleTheme: () => void };

export function Topbar({ theme, onToggleTheme }: Props) {
  const { t, i18n } = useTranslation();
  return (
    <header className="flex h-16 shrink-0 items-center justify-end gap-2 border-b border-line bg-surface px-6">
      <label className="sr-only" htmlFor="lang">
        {t("app.language")}
      </label>
      <select
        id="lang"
        value={i18n.resolvedLanguage}
        onChange={(e) => void i18n.changeLanguage(e.target.value)}
        className="h-9 rounded-md border border-line bg-surface px-2 text-sm text-fg outline-none focus-visible:ring-2 focus-visible:ring-primary"
      >
        {languages.map((l) => (
          <option key={l.code} value={l.code}>
            {l.label}
          </option>
        ))}
      </select>
      <button
        type="button"
        onClick={onToggleTheme}
        aria-label={theme === "dark" ? t("app.themeLight") : t("app.themeDark")}
        title={theme === "dark" ? t("app.themeLight") : t("app.themeDark")}
        className="grid size-9 place-items-center rounded-md border border-line text-muted hover:text-fg focus-visible:ring-2 focus-visible:ring-primary outline-none"
      >
        {theme === "dark" ? <Sun className="size-4" /> : <Moon className="size-4" />}
      </button>
    </header>
  );
}
