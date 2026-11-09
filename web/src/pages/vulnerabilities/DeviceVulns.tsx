import { useEffect, useState } from "react";
import { Link } from "react-router";
import { useTranslation } from "react-i18next";
import { Rocket, ShieldOff } from "lucide-react";
import { get, type Device, type DeviceVulns } from "../../api";
import { useAuth } from "../../auth";
import {
  Alert,
  Badge,
  Button,
  Card,
  Spinner,
  useErrorText,
} from "../../components/ui";
import { timeAgo } from "../devices/util";
import { AcceptDialog } from "./VulnDetail";
import {
  FixBadge,
  FixDialog,
  KEVBadge,
  SeverityBadge,
  kindLabel,
  severities,
} from "./util";
import { SeverityCounts } from "./Vulnerabilities";

export function DeviceVulnsTab({ device }: { device: Device }) {
  const { t, i18n } = useTranslation();
  const { can } = useAuth();
  const errorText = useErrorText();
  const [data, setData] = useState<DeviceVulns | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [fixing, setFixing] = useState<string[] | null>(null);
  const [accept, setAccept] = useState<string | null>(null);
  const [showAccepted, setShowAccepted] = useState(false);
  const [n, setN] = useState(0);
  useEffect(() => {
    get<DeviceVulns>(`/devices/${device.id}/vulnerabilities`)
      .then(setData)
      .catch((e) => setError(errorText(e)));
  }, [device.id, n]);
  if (error) return <Alert tone="danger">{error}</Alert>;
  if (!data) return <Spinner />;
  const manage = can("devices.manage");
  const rows = data.vulnerabilities.filter((v) => showAccepted || !v.accepted);
  const open = data.vulnerabilities.filter((v) => !v.accepted);
  const counts: Record<string, number> = {};
  for (const s of severities)
    counts[s] = new Set(
      open.filter((v) => v.severity === s).map((v) => v.cve),
    ).size;
  const fixable = rows.filter(
    (v) =>
      selected.has(v.cve + v.product) &&
      (v.fix === "patch" || v.fix === "software"),
  );
  const toggle = (k: string) =>
    setSelected((s) => {
      const x = new Set(s);
      if (x.has(k)) x.delete(k);
      else x.add(k);
      return x;
    });
  return (
    <Card
      title={t("vulns.deviceTitle")}
      description={
        data.checkedAt
          ? t("vulns.deviceChecked", {
              when: timeAgo(data.checkedAt, i18n.language),
            })
          : t("vulns.deviceNotChecked")
      }
      actions={
        manage &&
        selected.size > 0 && (
          <Button
            size="sm"
            icon={<Rocket className="size-4" />}
            disabled={fixable.length === 0}
            onClick={() => setFixing([...new Set(fixable.map((v) => v.cve))])}
          >
            {t("vulns.fixSelected", {
              count: new Set(fixable.map((v) => v.cve)).size,
            })}
          </Button>
        )
      }
    >
      <div className="space-y-4">
        {!data.supported && <Alert tone="info">{t("vulns.unsupported")}</Alert>}
        {data.pending && data.checkedAt && (
          <p className="text-xs text-muted">{t("vulns.pending")}</p>
        )}
        <div className="flex flex-wrap items-center justify-between gap-3 text-sm">
          <SeverityCounts counts={counts} />
          {data.vulnerabilities.some((v) => v.accepted) && (
            <label className="inline-flex items-center gap-2">
              <input
                type="checkbox"
                className="size-4 accent-primary"
                checked={showAccepted}
                onChange={(e) => setShowAccepted(e.target.checked)}
              />
              {t("vulns.showAccepted")}
            </label>
          )}
        </div>
        {rows.length === 0 ? (
          <p className="rounded-md border border-dashed border-line px-4 py-6 text-center text-sm text-muted">
            {data.checkedAt
              ? t("vulns.deviceClean")
              : t("vulns.deviceNotChecked")}
          </p>
        ) : (
          <div className="-mx-5 -mb-5 overflow-x-auto">
            <table className="w-full min-w-[820px] text-sm">
              <thead className="border-y border-line text-left text-xs text-muted uppercase">
                <tr>
                  {manage && <th className="w-10 px-5 py-3" />}
                  <th
                    className={`${manage ? "px-3" : "px-5"} py-3 font-medium`}
                  >
                    {t("vulns.cveCol")}
                  </th>
                  <th className="px-3 py-3 font-medium">
                    {t("vulns.severityCol")}
                  </th>
                  <th className="px-3 py-3 font-medium">
                    {t("vulns.productCol")}
                  </th>
                  <th className="px-3 py-3 font-medium">
                    {t("vulns.fixedIn")}
                  </th>
                  <th className="px-3 py-3 font-medium">{t("vulns.fixCol")}</th>
                  <th className="px-5 py-3" />
                </tr>
              </thead>
              <tbody className="divide-y divide-line">
                {rows.map((v) => {
                  const k = v.cve + v.product;
                  return (
                    <tr
                      key={k}
                      className={`align-top hover:bg-surface-muted/50 ${v.accepted ? "opacity-60" : ""}`}
                    >
                      {manage && (
                        <td className="px-5 py-2.5">
                          <input
                            type="checkbox"
                            className="size-4 accent-primary"
                            checked={selected.has(k)}
                            disabled={v.accepted}
                            onChange={() => toggle(k)}
                            aria-label={v.cve}
                          />
                        </td>
                      )}
                      <td
                        className={`${manage ? "px-3" : "px-5"} max-w-sm py-2.5`}
                      >
                        <div className="flex flex-wrap items-center gap-2">
                          <Link
                            to={`/vulnerabilities/${v.cve}`}
                            className="font-mono font-medium hover:text-primary"
                          >
                            {v.cve}
                          </Link>
                          {v.kev && <KEVBadge />}
                          {v.accepted && (
                            <Badge>{t("vulns.acceptedShort")}</Badge>
                          )}
                        </div>
                        <p className="mt-0.5 line-clamp-2 text-xs text-muted">
                          {v.title || v.summary}
                        </p>
                      </td>
                      <td className="px-3 py-2.5">
                        <SeverityBadge severity={v.severity} score={v.score} />
                      </td>
                      <td className="px-3 py-2.5">
                        {v.product}
                        <div className="text-xs text-muted">
                          {kindLabel(v.kind, t)}
                          {v.installedVersion && ` · ${v.installedVersion}`}
                        </div>
                      </td>
                      <td className="px-3 py-2.5 font-mono text-xs">
                        {v.fixedVersion || "–"}
                      </td>
                      <td className="px-3 py-2.5">
                        <FixBadge fix={v.fix} />
                      </td>
                      <td className="px-5 py-2.5 text-right">
                        {can("vulns.manage") && !v.accepted && (
                          <button
                            className="text-muted hover:text-fg"
                            title={t("vulns.accept")}
                            aria-label={t("vulns.accept")}
                            onClick={() => setAccept(v.cve)}
                          >
                            <ShieldOff className="size-4" />
                          </button>
                        )}
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        )}
      </div>
      <FixDialog
        open={fixing !== null}
        cves={fixing ?? []}
        deviceIds={[device.id]}
        onClose={() => setFixing(null)}
        onDone={() => {
          setSelected(new Set());
          setN((x) => x + 1);
        }}
      />
      <AcceptDialog
        open={accept !== null}
        cve={accept ?? ""}
        devices={[{ id: device.id, name: device.hostname }]}
        fixedDevice={device.id}
        onClose={() => setAccept(null)}
        onDone={() => {
          setAccept(null);
          setN((x) => x + 1);
        }}
      />
    </Card>
  );
}
