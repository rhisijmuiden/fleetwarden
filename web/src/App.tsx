import { useState } from "react";
import { Route, Routes } from "react-router";
import { useTranslation } from "react-i18next";
import { Sidebar } from "./components/Sidebar";
import { Topbar } from "./components/Topbar";
import { InfoDialog } from "./components/InfoDialog";
import { Dashboard } from "./pages/Dashboard";
import { Placeholder } from "./pages/Placeholder";
import { navigation } from "./nav";
import { useTheme } from "./useTheme";

export function App() {
  const { t } = useTranslation();
  const { theme, toggle } = useTheme();
  const [collapsed, setCollapsed] = useState(false);
  const [infoOpen, setInfoOpen] = useState(false);
  const planned = navigation.flatMap((g) => g.items).filter((i) => i.phase !== undefined);

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
