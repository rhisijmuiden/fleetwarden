import { NavLink } from "react-router";
import { useTranslation } from "react-i18next";
import { Info, PanelLeft, PanelLeftClose } from "lucide-react";
import { useAuth } from "../auth";
import { useBranding } from "../branding";
import { navigation } from "../nav";

type Props = { collapsed: boolean; onToggle: () => void; onInfo: () => void };

export function Sidebar({ collapsed, onToggle, onInfo }: Props) {
  const { t } = useTranslation();
  const { can } = useAuth();
  const { branding, logoUrl } = useBranding();
  const groups = navigation
    .map((g) => ({ ...g, items: g.items.filter((i) => !i.perm || can(i.perm)) }))
    .filter((g) => g.items.length > 0);
  const itemBase =
    "group flex items-center gap-3 rounded-md px-3 py-2 text-sm font-medium transition-colors outline-none focus-visible:ring-2 focus-visible:ring-primary";

  return (
    <aside
      className={`flex h-full shrink-0 flex-col bg-sidebar text-sidebar-fg transition-[width] duration-200 ${
        collapsed ? "w-[68px]" : "w-64"
      }`}
    >
      <div className="flex h-16 items-center gap-3 px-4">
        <img src={logoUrl} alt="" className="size-8 shrink-0 object-contain" />
        {!collapsed && <span className="truncate text-base font-semibold tracking-tight text-white">{branding.productName}</span>}
      </div>

      <nav className="flex-1 space-y-6 overflow-y-auto px-3 py-4" aria-label="Main">
        {groups.map((group) => (
          <div key={group.label}>
            {!collapsed && (
              <p className="mb-2 px-3 text-[11px] font-semibold tracking-wider text-sidebar-fg/60 uppercase">
                {t(group.label)}
              </p>
            )}
            <ul className="space-y-1">
              {group.items.map(({ path, label, icon: Icon, phase }) => (
                <li key={path}>
                  <NavLink
                    to={path}
                    end={path === "/"}
                    title={collapsed ? t(label) : undefined}
                    className={({ isActive }) =>
                      `${itemBase} ${
                        isActive ? "bg-sidebar-active text-white" : "hover:bg-sidebar-active/60 hover:text-white"
                      }`
                    }
                  >
                    {({ isActive }) => (
                      <>
                        <Icon className={`size-[18px] shrink-0 ${isActive ? "text-primary" : ""}`} strokeWidth={1.8} />
                        {!collapsed && <span className="flex-1 truncate">{t(label)}</span>}
                        {!collapsed && phase !== undefined && (
                          <span className="rounded-sm bg-white/5 px-1.5 py-0.5 text-[10px] text-sidebar-fg/60">
                            F{phase}
                          </span>
                        )}
                      </>
                    )}
                  </NavLink>
                </li>
              ))}
            </ul>
          </div>
        ))}
      </nav>

      <div className="space-y-1 border-t border-white/5 p-3">
        <button type="button" onClick={onInfo} className={`${itemBase} w-full hover:bg-sidebar-active/60 hover:text-white`}>
          <Info className="size-[18px] shrink-0" strokeWidth={1.8} />
          {!collapsed && <span>{t("nav.info")}</span>}
        </button>
        <button
          type="button"
          onClick={onToggle}
          className={`${itemBase} w-full hover:bg-sidebar-active/60 hover:text-white`}
          aria-label={collapsed ? t("app.expandMenu") : t("app.collapseMenu")}
        >
          {collapsed ? (
            <PanelLeft className="size-[18px] shrink-0" strokeWidth={1.8} />
          ) : (
            <PanelLeftClose className="size-[18px] shrink-0" strokeWidth={1.8} />
          )}
          {!collapsed && <span>{t("app.collapseMenu")}</span>}
        </button>
      </div>
    </aside>
  );
}
