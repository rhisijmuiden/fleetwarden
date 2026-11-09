import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";
import { ChevronDown, LogOut, Moon, Sun, UserRound } from "lucide-react";
import { useAuth } from "../auth";
import { languages } from "../i18n";
import type { Theme } from "../useTheme";

type Props = { theme: Theme; onToggleTheme: () => void };

export function Topbar({ theme, onToggleTheme }: Props) {
  const { t, i18n } = useTranslation();
  const { me, logout, setLanguage } = useAuth();
  const [open, setOpen] = useState(false);
  const menu = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!open) return;
    const close = (e: MouseEvent) => {
      if (!menu.current?.contains(e.target as Node)) setOpen(false);
    };
    const esc = (e: KeyboardEvent) => e.key === "Escape" && setOpen(false);
    document.addEventListener("mousedown", close);
    document.addEventListener("keydown", esc);
    return () => {
      document.removeEventListener("mousedown", close);
      document.removeEventListener("keydown", esc);
    };
  }, [open]);

  const name = me?.displayName || me?.username || "";
  const initials = name
    .split(/[\s@.]+/)
    .filter(Boolean)
    .slice(0, 2)
    .map((p) => p[0]?.toUpperCase())
    .join("");

  return (
    <header className="flex h-16 shrink-0 items-center justify-end gap-2 border-b border-line bg-surface px-6">
      <label className="sr-only" htmlFor="lang">
        {t("app.language")}
      </label>
      <select
        id="lang"
        value={i18n.resolvedLanguage}
        onChange={(e) => setLanguage(e.target.value as "en" | "nl")}
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
      {me && (
        <div className="relative" ref={menu}>
          <button
            type="button"
            onClick={() => setOpen((o) => !o)}
            aria-haspopup="menu"
            aria-expanded={open}
            className="flex h-9 items-center gap-2 rounded-md pr-1.5 pl-1 hover:bg-surface-muted focus-visible:ring-2 focus-visible:ring-primary outline-none"
          >
            <span className="grid size-7 place-items-center rounded-full bg-primary text-xs font-semibold text-primary-contrast">{initials}</span>
            <span className="hidden max-w-[160px] truncate text-sm font-medium sm:block">{name}</span>
            <ChevronDown className="size-4 text-muted" />
          </button>
          {open && (
            <div role="menu" className="absolute right-0 z-30 mt-2 w-60 overflow-hidden rounded-md border border-line bg-surface shadow-xl">
              <div className="border-b border-line px-4 py-3">
                <p className="truncate text-sm font-medium">{name}</p>
                <p className="truncate text-xs text-muted">
                  {me.username} · {t(`roles.${me.role}`)}
                </p>
              </div>
              <Link role="menuitem" to="/profile" onClick={() => setOpen(false)} className="flex items-center gap-2 px-4 py-2.5 text-sm hover:bg-surface-muted">
                <UserRound className="size-4 text-muted" />
                {t("app.profile")}
              </Link>
              <button role="menuitem" type="button" onClick={() => void logout()} className="flex w-full items-center gap-2 px-4 py-2.5 text-left text-sm hover:bg-surface-muted">
                <LogOut className="size-4 text-muted" />
                {t("app.signOut")}
              </button>
            </div>
          )}
        </div>
      )}
    </header>
  );
}
