import { useEffect, useState } from "react";
import { NavLink } from "react-router";
import { useTranslation } from "react-i18next";
import { Info, PanelLeft, PanelLeftClose } from "lucide-react";
import { get } from "../api";
import { useAuth } from "../auth";
import { useBranding } from "../branding";
import { navigation } from "../nav";

type Props = { collapsed: boolean; onToggle: () => void; onInfo: () => void };

// useOpenAlerts polls the number of open, unacknowledged alerts.
function useOpenAlerts(enabled: boolean) {
  const [n, setN] = useState(0);
  useEffect(() => {
    if (!enabled) return;
    const load = () =>
      get<{ open: number }>("/alerts/count")
        .then((r) => setN(r.open))
        .catch(() => {});
    load();
    const id = setInterval(load, 60_000);
    window.addEventListener("fw:alerts", load);
    return () => {
      clearInterval(id);
      window.removeEventListener("fw:alerts", load);
    };
  }, [enabled]);
  return n;
}

// useSelfServiceCount polls what waits for a technician: install requests
// to approve and new help requests.
function useSelfServiceCount(enabled: boolean) {
  const [n, setN] = useState(0);
  useEffect(() => {
    if (!enabled) return;
    const load = () =>
      get<{ pendingRequests: number; openHelp: number }>("/selfservice/count")
        .then((r) => setN(r.pendingRequests + r.openHelp))
        .catch(() => {});
    load();
    const id = setInterval(load, 60_000);
    window.addEventListener("fw:selfservice", load);
    return () => {
      clearInterval(id);
      window.removeEventListener("fw:selfservice", load);
    };
  }, [enabled]);
  return n;
}

export function Sidebar({ collapsed, onToggle, onInfo }: Props) {
  const { t } = useTranslation();
  const { can } = useAuth();
  const { branding, logoUrl } = useBranding();
  const openAlerts = useOpenAlerts(can("devices.view"));
  const selfService = useSelfServiceCount(can("devices.view"));
  const groups = navigation
    .map((g) => ({ ...g, items: g.items.filter((i) => !i.perm || can(i.perm)) }))
    .filter((g) => g.items.length > 0);
  const itemBase =
    "group relative flex items-center gap-3 rounded-md px-3 py-2 text-sm font-medium transition-colors outline-none focus-visible:ring-2 focus-visible:ring-primary";

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
              {group.items.map(({ path, label, icon: Icon, phase, badge }) => (
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
                        {badge === "alerts" && openAlerts > 0 && (
                          <span
                            className={`rounded-full bg-danger px-1.5 text-[11px] leading-[18px] font-semibold text-white tabular-nums ${collapsed ? "absolute top-1 right-1" : ""}`}
                          >
                            {openAlerts > 99 ? "99+" : openAlerts}
                          </span>
                        )}
                        {badge === "selfservice" && selfService > 0 && (
                          <span
                            className={`rounded-full bg-warning px-1.5 text-[11px] leading-[18px] font-semibold text-white tabular-nums ${collapsed ? "absolute top-1 right-1" : ""}`}
                          >
                            {selfService > 99 ? "99+" : selfService}
                          </span>
                        )}
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
