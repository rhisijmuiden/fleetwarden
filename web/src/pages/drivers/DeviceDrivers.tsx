import { useCallback, useEffect, useState } from "react";
import { Link } from "react-router";
import { useTranslation } from "react-i18next";
import { Download, RefreshCw } from "lucide-react";
import { get, post, type Device, type DeviceDrivers } from "../../api";
import { useAuth } from "../../auth";
import {
  Alert,
  Badge,
  Button,
  Card,
  Spinner,
  formatDate,
  useErrorText,
} from "../../components/ui";
import { formatBytes, timeAgo } from "../devices/util";
import { DriverStateBadge, KindBadge, driverMeta, sourceName } from "./util";

export function DeviceDriversTab({ device }: { device: Device }) {
  const { t, i18n } = useTranslation();
  const { can } = useAuth();
  const errorText = useErrorText();
  const [data, setData] = useState<DeviceDrivers | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [selected, setSelected] = useState<Set<number>>(new Set());
  const [busy, setBusy] = useState<"" | "scan" | "install">("");
  const manage = can("devices.manage");

  const load = useCallback(() => {
    get<DeviceDrivers>(`/devices/${device.id}/drivers`)
      .then(setData)
      .catch((e) => setError(errorText(e)));
  }, [device.id]);
  useEffect(() => {
    load();
    const id = setInterval(load, 15000);
    return () => clearInterval(id);
  }, [load]);

  if (error && !data) return <Alert tone="danger">{error}</Alert>;
  if (!data) return <Spinner />;

  const toggle = (id: number) =>
    setSelected((s) => {
      const n = new Set(s);
      if (n.has(id)) n.delete(id);
      else n.add(id);
      return n;
    });
  const scan = () => {
    setBusy("scan");
    setError(null);
    post(`/devices/${device.id}/patches/scan`)
      .then(() => {
        setNotice(t("patches.scanRequested"));
        setTimeout(load, 5000);
      })
      .catch((e) => setError(errorText(e)))
      .finally(() => setBusy(""));
  };
  const install = () => {
    setBusy("install");
    setError(null);
    post(`/devices/${device.id}/drivers/install`, { ids: [...selected] })
      .then(() => {
        setNotice(t("drivers.installStarted"));
        setSelected(new Set());
        load();
      })
      .catch((e) => setError(errorText(e)))
      .finally(() => setBusy(""));
  };
  const pol = data.policy;

  return (
    <div className="space-y-6">
      {error && <Alert tone="danger">{error}</Alert>}
      {notice && <Alert tone="success">{notice}</Alert>}
      <div className="grid gap-6 lg:grid-cols-3">
        <Card title={t("drivers.deviceStatus")} className="lg:col-span-2">
          <dl className="grid grid-cols-1 gap-x-6 gap-y-3 text-sm sm:grid-cols-2">
            <div>
              <dt className="text-xs text-muted">{t("patches.lastScan")}</dt>
              <dd>
                {data.scanAt
                  ? `${formatDate(data.scanAt, i18n.language)} (${timeAgo(data.scanAt, i18n.language)})`
                  : t("patches.neverScanned")}
              </dd>
            </div>
            <div>
              <dt className="text-xs text-muted">{t("drivers.onOffer")}</dt>
              <dd>
                {data.drivers.length} ·{" "}
                {t("drivers.inRepository", {
                  count: data.drivers.filter((d) => d.state === "ready").length,
                })}
              </dd>
            </div>
          </dl>
          {data.scanError && (
            <div className="mt-4">
              <Alert tone="warning" title={t("patches.scanFailed")}>
                <span className="font-mono text-xs break-all">
                  {data.scanError}
                </span>
              </Alert>
            </div>
          )}
        </Card>
        <Card title={t("patches.policy")}>
          {pol ? (
            <div className="space-y-2 text-sm">
              <Link
                to="/patches?tab=policies"
                className="font-medium hover:text-primary"
              >
                {pol.name}
              </Link>
              <div className="flex flex-wrap gap-2">
                <Badge tone={pol.drivers ? "success" : "neutral"}>
                  {pol.drivers
                    ? t("drivers.policyDriversOn")
                    : t("drivers.policyDriversOff")}
                </Badge>
                <Badge tone={pol.firmware ? "warning" : "neutral"}>
                  {pol.firmware
                    ? t("drivers.policyFirmwareOn")
                    : t("drivers.policyFirmwareOff")}
                </Badge>
              </div>
            </div>
          ) : (
            <p className="text-sm text-muted">{t("drivers.deviceNoPolicy")}</p>
          )}
        </Card>
      </div>

      <Card
        title={t("drivers.offered")}
        description={t("drivers.offeredHint")}
        actions={
          manage && (
            <div className="flex flex-wrap gap-2">
              <Button
                variant="secondary"
                size="sm"
                icon={<RefreshCw className="size-4" />}
                loading={busy === "scan"}
                disabled={!device.online}
                title={!device.online ? t("devices.offlineHint") : undefined}
                onClick={scan}
              >
                {t("patches.scanNow")}
              </Button>
              <Button
                size="sm"
                icon={<Download className="size-4" />}
                loading={busy === "install"}
                disabled={!device.online || selected.size === 0}
                onClick={install}
              >
                {t("drivers.installSelected", { count: selected.size })}
              </Button>
            </div>
          )
        }
      >
        {data.drivers.length === 0 ? (
          <p className="rounded-md border border-dashed border-line px-4 py-6 text-center text-sm text-muted">
            {data.scanAt ? t("drivers.upToDate") : t("patches.neverScanned")}
          </p>
        ) : (
          <div className="-mx-5 -my-5 overflow-x-auto">
            <table className="w-full min-w-[760px] text-sm">
              <thead className="border-b border-line text-left text-xs text-muted uppercase">
                <tr>
                  {manage && (
                    <th className="w-10 px-5 py-3">
                      <span className="sr-only">{t("patches.select")}</span>
                    </th>
                  )}
                  <th
                    className={`${manage ? "px-3" : "px-5"} py-3 font-medium`}
                  >
                    {t("drivers.update")}
                  </th>
                  <th className="px-3 py-3 font-medium">{t("drivers.kind")}</th>
                  <th className="px-3 py-3 font-medium">
                    {t("drivers.deviceCol")}
                  </th>
                  <th className="px-3 py-3 font-medium">{t("drivers.size")}</th>
                  <th className="px-5 py-3 font-medium">
                    {t("drivers.status")}
                  </th>
                </tr>
              </thead>
              <tbody className="divide-y divide-line">
                {data.drivers.map((d) => (
                  <tr key={d.id}>
                    {manage && (
                      <td className="px-5 py-2">
                        <input
                          type="checkbox"
                          className="size-4 accent-primary disabled:opacity-40"
                          disabled={d.state !== "ready"}
                          title={
                            d.state !== "ready"
                              ? t("drivers.notInRepository")
                              : undefined
                          }
                          checked={selected.has(d.id)}
                          onChange={() => toggle(d.id)}
                          aria-label={d.title}
                        />
                      </td>
                    )}
                    <td className={`${manage ? "px-3" : "px-5"} py-2`}>
                      <div className="font-medium">{d.title}</div>
                      <div className="text-xs text-muted">
                        {[
                          driverMeta(d),
                          sourceName(d, t),
                          d.reboot ? t("drivers.needsRestart") : "",
                        ]
                          .filter(Boolean)
                          .join(" · ")}
                      </div>
                    </td>
                    <td className="px-3 py-2">
                      <KindBadge kind={d.kind} />
                    </td>
                    <td className="px-3 py-2 text-xs text-muted">
                      {d.deviceName || "–"}
                      {d.currentVersion && (
                        <div>
                          {t("drivers.currentVersion", {
                            version: d.currentVersion,
                          })}
                        </div>
                      )}
                    </td>
                    <td className="px-3 py-2 whitespace-nowrap text-muted">
                      {d.sizeBytes
                        ? formatBytes(d.sizeBytes, i18n.language)
                        : "–"}
                    </td>
                    <td className="px-5 py-2">
                      <DriverStateBadge state={d.state} />
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Card>
    </div>
  );
}
