import { useState } from "react";
import { Navigate, Route, Routes, useLocation } from "react-router";
import { useTranslation } from "react-i18next";
import { useAuth } from "./auth";
import { Sidebar } from "./components/Sidebar";
import { Topbar } from "./components/Topbar";
import { InfoDialog } from "./components/InfoDialog";
import { Spinner } from "./components/ui";
import { Dashboard } from "./pages/Dashboard";
import { Placeholder } from "./pages/Placeholder";
import { Login } from "./pages/Login";
import { ForcePasswordChange } from "./pages/ForcePasswordChange";
import { Profile } from "./pages/Profile";
import { Users } from "./pages/admin/Users";
import { Authentication } from "./pages/admin/Authentication";
import { Mail } from "./pages/admin/Mail";
import { Branding } from "./pages/admin/Branding";
import { Settings } from "./pages/admin/Settings";
import { ReverseProxy } from "./pages/admin/ReverseProxy";
import { Audit } from "./pages/admin/Audit";
import { Backup } from "./pages/admin/backup/Backup";
import { Devices } from "./pages/devices/Devices";
import { DeviceDetail } from "./pages/devices/DeviceDetail";
import { Groups } from "./pages/devices/Groups";
import { AgentUpdates } from "./pages/admin/AgentUpdates";
import { Patches } from "./pages/patches/Patches";
import { Software } from "./pages/software/Software";
import { DeploymentDetail, Deployments } from "./pages/software/Deployments";
import { Remote } from "./pages/remote/Remote";
import { RemoteViewer } from "./pages/remote/Viewer";
import { Reports } from "./pages/reports/Reports";
import { navigation } from "./nav";
import { useTheme } from "./useTheme";
import type { Permission } from "./api";

const adminPages: { path: string; perm: Permission; element: React.ReactNode }[] = [
  { path: "/devices", perm: "devices.view", element: <Devices /> },
  { path: "/devices/:id", perm: "devices.view", element: <DeviceDetail /> },
  { path: "/groups", perm: "devices.view", element: <Groups /> },
  { path: "/patches", perm: "devices.view", element: <Patches /> },
  { path: "/software", perm: "devices.view", element: <Software /> },
  { path: "/deployments", perm: "devices.view", element: <Deployments /> },
  { path: "/deployments/:id", perm: "devices.view", element: <DeploymentDetail /> },
  { path: "/remote", perm: "remote.control", element: <Remote /> },
  { path: "/reports", perm: "devices.view", element: <Reports /> },
  { path: "/admin/users", perm: "users.manage", element: <Users /> },
  { path: "/admin/authentication", perm: "settings.manage", element: <Authentication /> },
  { path: "/admin/mail", perm: "settings.manage", element: <Mail /> },
  { path: "/admin/backup", perm: "backup.manage", element: <Backup /> },
  { path: "/admin/agent-updates", perm: "agents.manage", element: <AgentUpdates /> },
  { path: "/admin/branding", perm: "settings.manage", element: <Branding /> },
  { path: "/admin/audit", perm: "audit.view", element: <Audit /> },
  { path: "/admin/settings", perm: "settings.manage", element: <Settings /> },
  { path: "/admin/reverse-proxy", perm: "settings.manage", element: <ReverseProxy /> },
];

export function App() {
  const { me, loading, can } = useAuth();
  const location = useLocation();
  const { t } = useTranslation();
  const { theme, toggle } = useTheme();
  // Start with the narrow menu on phones so the page keeps its width.
  const [collapsed, setCollapsed] = useState(() => window.matchMedia("(max-width: 767px)").matches);
  const [infoOpen, setInfoOpen] = useState(false);

  if (loading) return <Spinner />;
  if (!me) {
    return location.pathname === "/login" ? <Login /> : <Navigate to="/login" replace state={{ from: location.pathname }} />;
  }
  if (me.mustChangePassword) return <ForcePasswordChange />;
  if (location.pathname === "/login") return <Navigate to="/" replace />;
  // The remote control viewer fills its own tab, without the menu.
  if (location.pathname.startsWith("/remote/connect/") && can("remote.control")) {
    return (
      <Routes>
        <Route path="/remote/connect/:deviceId" element={<RemoteViewer />} />
      </Routes>
    );
  }

  const planned = navigation.flatMap((g) => g.items).filter((i) => i.phase !== undefined && (!i.perm || can(i.perm)));
  return (
    <div className="flex h-full">
      <a
        href="#main"
        className="sr-only focus:not-sr-only focus:absolute focus:top-2 focus:left-2 focus:z-50 focus:rounded-md focus:bg-surface focus:px-3 focus:py-2"
      >
        {t("app.skipToContent")}
      </a>
      <Sidebar collapsed={collapsed} onToggle={() => setCollapsed((c) => !c)} onInfo={() => setInfoOpen(true)} />
      <div className="flex min-w-0 flex-1 flex-col">
        <Topbar theme={theme} onToggleTheme={toggle} />
        <main id="main" className="flex-1 overflow-y-auto px-6 py-8 lg:px-10">
          <div className="mx-auto max-w-6xl">
            <Routes>
              <Route path="/" element={<Dashboard />} />
              <Route path="/profile" element={<Profile />} />
              {adminPages
                .filter((p) => can(p.perm))
                .map((p) => (
                  <Route key={p.path} path={p.path} element={p.element} />
                ))}
              {planned.map((i) => (
                <Route key={i.path} path={i.path} element={<Placeholder title={i.label} phase={i.phase!} />} />
              ))}
              <Route path="*" element={<p className="text-muted">{t("notFound")}</p>} />
            </Routes>
          </div>
        </main>
      </div>
      <InfoDialog open={infoOpen} onClose={() => setInfoOpen(false)} />
    </div>
  );
}
