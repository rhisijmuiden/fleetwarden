import { Link } from "react-router";
import { useTranslation } from "react-i18next";
import { CalendarClock, HelpCircle, ScrollText, ShieldOff } from "lucide-react";
import type { AssetsReport } from "../../api";
import { Alert, Badge, Card, Spinner } from "../../components/ui";
import { formatDay } from "../assets/util";
import { Generated, Tile, useReport } from "./Reports";

const licenseTone = { over: "danger", expired: "danger", expiring: "warning", ok: "success" } as const;

export function AssetsReportView({ query }: { query: string }) {
  const { t, i18n } = useTranslation();
  const { data: r, error } = useReport<AssetsReport>("assets", query);
  if (error && !r) return <Alert tone="danger">{error}</Alert>;
  if (!r) return <Spinner />;
  return (
    <div className="space-y-6">
      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <Tile
          icon={<ShieldOff className="size-3.5" />}
          label={t("assets.summary.expired")}
          value={r.expired}
          tone={r.expired ? "text-danger" : ""}
          sub={t("reports.ofDevices", { compliant: r.expired, count: r.devices })}
          to="/asset-management?warranty=expired"
        />
        <Tile
          icon={<CalendarClock className="size-3.5" />}
          label={t("assets.summary.expiring")}
          value={r.expiring}
          tone={r.expiring ? "text-warning" : ""}
          sub={t("assets.summary.withinDays", { count: r.expiringDays })}
          to="/asset-management?warranty=expiring"
        />
        <Tile icon={<HelpCircle className="size-3.5" />} label={t("assets.summary.unknown")} value={r.unknown} to="/asset-management?warranty=unknown" />
        <Tile
          icon={<ScrollText className="size-3.5" />}
          label={t("assets.report.licensesOver")}
          value={r.licensesOver}
          tone={r.licensesOver ? "text-danger" : ""}
          sub={t("assets.licenses.ofSeats", { inUse: r.inUse, seats: r.seats })}
          to="/asset-management?tab=licenses"
        />
      </div>

      <Card title={t("assets.licenses.title")}>
        {r.licenses.length === 0 ? (
          <p className="text-sm text-muted">{t("assets.licenses.empty")}</p>
        ) : (
          <div className="-mx-5 -my-5 overflow-x-auto">
            <table className="w-full min-w-[640px] text-sm">
              <thead className="border-b border-line text-left text-xs text-muted uppercase">
                <tr>
                  <th className="px-5 py-3 font-medium">{t("assets.licenses.name")}</th>
                  <th className="px-3 py-3 text-right font-medium">{t("assets.licenses.seats")}</th>
                  <th className="px-3 py-3 text-right font-medium">{t("assets.licenses.inUse")}</th>
                  <th className="px-3 py-3 text-right font-medium">{t("assets.licenses.free")}</th>
                  <th className="px-3 py-3 font-medium">{t("assets.licenses.expiresOn")}</th>
                  <th className="px-5 py-3 font-medium">{t("assets.licenses.state")}</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-line">
                {r.licenses.map((l) => (
                  <tr key={l.id}>
                    <td className="px-5 py-3">
                      <span className="font-medium">{l.name}</span>
                      {l.vendor && <div className="text-xs text-muted">{l.vendor}</div>}
                    </td>
                    <td className="px-3 py-3 text-right tabular-nums">{l.seats}</td>
                    <td className="px-3 py-3 text-right tabular-nums">{l.inUse}</td>
                    <td className={`px-3 py-3 text-right tabular-nums ${l.free < 0 ? "font-medium text-danger" : ""}`}>{l.free}</td>
                    <td className="px-3 py-3 whitespace-nowrap">{l.expiresOn ? formatDay(l.expiresOn, i18n.language) : t("assets.licenses.perpetual")}</td>
                    <td className="px-5 py-3">
                      <Badge tone={licenseTone[l.state]}>{t(`assets.licenses.states.${l.state}`, { count: -l.free })}</Badge>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Card>

      <Card title={t("assets.report.warranty")}>
        {r.warranty.length === 0 ? (
          <p className="text-sm text-muted">{t("assets.report.noWarranty")}</p>
        ) : (
          <div className="-mx-5 -my-5 overflow-x-auto">
            <table className="w-full min-w-[720px] text-sm">
              <thead className="border-b border-line text-left text-xs text-muted uppercase">
                <tr>
                  <th className="px-5 py-3 font-medium">{t("devices.device")}</th>
                  <th className="px-3 py-3 font-medium">{t("assets.cols.hardware")}</th>
                  <th className="px-3 py-3 font-medium">{t("assets.fields.owner")}</th>
                  <th className="px-3 py-3 font-medium">{t("assets.fields.location")}</th>
                  <th className="px-5 py-3 font-medium">{t("assets.fields.warrantyEnd")}</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-line">
                {r.warranty.map((d) => (
                  <tr key={d.id}>
                    <td className="px-5 py-3">
                      <Link to={`/devices/${d.id}`} className="font-medium hover:underline">
                        {d.hostname}
                      </Link>
                      <div className="text-xs text-muted">{d.groupName}</div>
                    </td>
                    <td className="px-3 py-3">
                      {[d.manufacturer, d.model].filter(Boolean).join(" ") || "–"}
                      {d.serial && <div className="font-mono text-xs text-muted">{d.serial}</div>}
                    </td>
                    <td className="px-3 py-3">{d.owner || "–"}</td>
                    <td className="px-3 py-3">{d.location || "–"}</td>
                    <td className="px-5 py-3 whitespace-nowrap">
                      {formatDay(d.warrantyEnd, i18n.language)}{" "}
                      <Badge tone={d.state === "expired" ? "danger" : "warning"}>
                        {d.state === "expired" ? t("assets.warranty.expired") : t("assets.warranty.daysLeft", { count: d.daysLeft ?? 0 })}
                      </Badge>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Card>
      <Generated at={r.generated} />
    </div>
  );
}
