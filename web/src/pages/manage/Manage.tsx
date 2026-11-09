import { useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Activity, Cog, FileText, FolderOpen, PowerOff } from "lucide-react";
import type { Device } from "../../api";
import { Alert } from "../../components/ui";
import { ProcessesPanel } from "./Processes";
import { ServicesPanel } from "./Services";
import { LogsPanel } from "./Logs";
import { FilesPanel } from "./Files";

type Section = "processes" | "services" | "logs" | "files";

const sections: { key: Section; icon: ReactNode }[] = [
  { key: "processes", icon: <Activity className="size-4" /> },
  { key: "services", icon: <Cog className="size-4" /> },
  { key: "logs", icon: <FileText className="size-4" /> },
  { key: "files", icon: <FolderOpen className="size-4" /> },
];

// DeviceManageTab manages a device without taking over its screen: the user
// keeps working while processes, services, logs and files are handled.
export function DeviceManageTab({ device }: { device: Device }) {
  const { t } = useTranslation();
  const [section, setSection] = useState<Section>("processes");

  return (
    <div className="grid gap-6 lg:grid-cols-[200px_minmax(0,1fr)]">
      <nav aria-label={t("manage.title")} className="flex gap-1 overflow-x-auto lg:flex-col">
        {sections.map((s) => (
          <button
            key={s.key}
            type="button"
            onClick={() => setSection(s.key)}
            aria-current={section === s.key ? "page" : undefined}
            className={`flex shrink-0 items-center gap-2.5 rounded-md px-3 py-2 text-left text-sm font-medium transition-colors ${
              section === s.key ? "bg-primary/10 text-primary" : "text-muted hover:bg-surface-muted hover:text-fg"
            }`}
          >
            {s.icon}
            {t(`manage.sections.${s.key}`)}
          </button>
        ))}
        <p className="mt-3 hidden px-3 text-xs text-muted lg:block">{t("manage.noTakeover")}</p>
      </nav>
      <div className="min-w-0 space-y-4">
        {!device.online && (
          <Alert tone="warning" title={t("manage.offlineTitle")}>
            <span className="inline-flex items-center gap-1.5">
              <PowerOff className="size-4" /> {t("manage.offline")}
            </span>
          </Alert>
        )}
        {device.online && section === "processes" && <ProcessesPanel device={device} />}
        {device.online && section === "services" && <ServicesPanel device={device} />}
        {device.online && section === "logs" && <LogsPanel device={device} />}
        {device.online && section === "files" && <FilesPanel device={device} />}
      </div>
    </div>
  );
}

// SortHeader is a clickable table header.
export function SortHeader<K extends string>({
  k,
  sort,
  setSort,
  children,
  className = "",
}: {
  k: K;
  sort: { key: K; desc: boolean };
  setSort: (s: { key: K; desc: boolean }) => void;
  children: ReactNode;
  className?: string;
}) {
  const active = sort.key === k;
  return (
    <th className={`py-3 font-medium ${className}`} aria-sort={active ? (sort.desc ? "descending" : "ascending") : undefined}>
      <button
        type="button"
        className={`inline-flex items-center gap-1 uppercase hover:text-fg ${active ? "text-fg" : ""}`}
        onClick={() => setSort({ key: k, desc: active ? !sort.desc : k !== "name" })}
      >
        {children}
        {active && <span aria-hidden>{sort.desc ? "↓" : "↑"}</span>}
      </button>
    </th>
  );
}
