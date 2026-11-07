import { useTranslation } from "react-i18next";
import { Monitor } from "lucide-react";

export function Dashboard() {
  const { t } = useTranslation();
  const stats = [
    { label: t("dashboard.devices"), value: "0" },
    { label: t("dashboard.online"), value: "0" },
    { label: t("dashboard.missingPatches"), value: "0" },
    { label: t("dashboard.outdatedSoftware"), value: "0" },
  ];
  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">{t("dashboard.title")}</h1>
        <p className="mt-1 text-sm text-muted">{t("dashboard.subtitle")}</p>
      </div>
      <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
        {stats.map((s) => (
          <div key={s.label} className="rounded-lg border border-line bg-surface p-5">
            <p className="text-sm text-muted">{s.label}</p>
            <p className="mt-2 text-3xl font-semibold tabular-nums">{s.value}</p>
          </div>
        ))}
      </div>
      <div className="grid place-items-center rounded-lg border border-dashed border-line bg-surface px-6 py-16 text-center">
        <Monitor className="size-10 text-muted" strokeWidth={1.5} />
        <p className="mt-3 max-w-sm text-sm text-muted">{t("dashboard.empty")}</p>
      </div>
    </div>
  );
}
