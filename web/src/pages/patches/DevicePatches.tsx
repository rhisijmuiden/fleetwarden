import { useCallback, useEffect, useMemo, useState } from "react";
import { Link } from "react-router";
import { useTranslation } from "react-i18next";
import { Download, RefreshCw, RotateCcw, XCircle } from "lucide-react";
import { del, get, post, type Device, type DevicePatches } from "../../api";
import { useAuth } from "../../auth";
import { Alert, Badge, Button, Card, Spinner, formatDate, useErrorText } from "../../components/ui";
import { formatBytes, timeAgo } from "../devices/util";
import { RunsTable } from "./Patches";
import { CategoryBadge, RunState, formatInZone, windowText } from "./util";

export function DevicePatchesTab({ device }: { device: Device }) {
  const { t, i18n } = useTranslation();
  const { can } = useAuth();
  const errorText = useErrorText();
  const [data, setData] = useState<DevicePatches | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [busy, setBusy] = useState<"" | "scan" | "install">("");
  const manage = can("devices.manage");

  const load = useCallback(() => {
    get<DevicePatches>(`/devices/${device.id}/patches`)
      .then(setData)
      .catch((e) => setError(errorText(e)));
  }, [device.id]);
  useEffect(() => {
    load();
    const id = setInterval(load, 10000);
    return () => clearInterval(id);
  }, [load]);

  const open = data?.runs.find((r) => !r.finishedAt);
  const approvedCount = useMemo(() => data?.updates.filter((u) => u.approved).length ?? 0, [data]);

  if (error && !data) return <Alert tone="danger">{error}</Alert>;
  if (!data) return <Spinner />;

  const toggle = (id: string) =>
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
    post(`/devices/${device.id}/patches/install`, { updateIds: [...selected] })
      .then(() => {
        setNotice(t("patches.installStarted"));
        setSelected(new Set());
        load();
      })
      .catch((e) => setError(errorText(e)))
      .finally(() => setBusy(""));
  };

  return (
    <div className="space-y-6">
      {error && <Alert tone="danger">{error}</Alert>}
      {notice && <Alert tone="success">{notice}</Alert>}
      <div className="grid gap-6 lg:grid-cols-3">
        <Card title={t("patches.status")} className="lg:col-span-2">
          <dl className="grid grid-cols-1 gap-x-6 gap-y-3 text-sm sm:grid-cols-2">
            <div>
              <dt className="text-xs text-muted">{t("patches.lastScan")}</dt>
              <dd>{data.scanAt ? `${formatDate(data.scanAt, i18n.language)} (${timeAgo(data.scanAt, i18n.language)})` : t("patches.neverScanned")}</dd>
            </div>
            <div>
              <dt className="text-xs text-muted">{t("patches.missingCount")}</dt>
              <dd>
                {data.updates.length} · {t("patches.approvedCount", { count: approvedCount })}
              </dd>
            </div>
            <div>
              <dt className="text-xs text-muted">{t("patches.restart")}</dt>
              <dd>{data.rebootPending ? <Badge tone="warning">{t("patches.rebootPending")}</Badge> : t("patches.noRestart")}</dd>
            </div>
            <div>
              <dt className="text-xs text-muted">{t("patches.source")}</dt>
              <dd>
                {data.source ? t(`patches.sources.${data.source}`, { defaultValue: data.source }) : "–"}
                {data.cluster && <span className="text-muted"> · {t("patches.cluster", { name: data.cluster })}</span>}
              </dd>
            </div>
            {open && (
              <div className="sm:col-span-2">
                <dt className="text-xs text-muted">{t("patches.currentJob")}</dt>
                <dd className="flex flex-wrap items-center gap-2">
                  <RunState run={open} />
                  <span className="text-muted">{open.message}</span>
                  {manage && (
                    <Button
                      variant="ghost"
                      size="sm"
                      icon={<XCircle className="size-4" />}
                      onClick={() =>
                        del(`/patches/runs/${open.id}`)
                          .then(load)
                          .catch((e) => setError(errorText(e)))
                      }
                    >
                      {t("patches.closeJob")}
                    </Button>
                  )}
                </dd>
              </div>
            )}
          </dl>
          {data.scanError && (
            <div className="mt-4">
              <Alert tone="warning" title={t("patches.scanFailed")}>
                <span className="font-mono text-xs break-all">{data.scanError}</span>
              </Alert>
            </div>
          )}
        </Card>
        <Card title={t("patches.policy")}>
          {data.policy ? (
            <div className="space-y-1 text-sm">
              <Link to="/patches?tab=policies" className="font-medium hover:text-primary">
                {data.policy.name}
              </Link>
              {!data.policy.enabled && <Badge>{t("patches.disabled")}</Badge>}
              <div className="text-muted">{windowText(data.policy, i18n.language, t)}</div>
              <div className="text-muted">{t(`patches.reboot.${data.policy.reboot}`)}</div>
              <div className="text-xs text-muted">{data.policy.timeZone || t("patches.serverTimeZone", { zone: data.serverTimeZone })}</div>
              {data.policy.enabled && data.policy.nextWindow && (
                <div className="text-muted">
                  {data.policy.windowOpen ? t("patches.windowOpenNow") : t("patches.nextWindow", { when: formatInZone(data.policy.nextWindow, i18n.language, data.policy.timeZone || data.serverTimeZone) })}
                </div>
              )}
            </div>
          ) : (
            <p className="text-sm text-muted">
              {t("patches.deviceNoPolicy")}{" "}
              <Link to="/groups" className="text-primary hover:underline">
                {t("nav.groups")}
              </Link>
            </p>
          )}
        </Card>
      </div>

      <Card
        title={t("patches.missingUpdates")}
        actions={
          manage && (
            <div className="flex flex-wrap gap-2">
              <Button variant="secondary" size="sm" icon={<RefreshCw className="size-4" />} loading={busy === "scan"} disabled={!device.online} title={!device.online ? t("devices.offlineHint") : undefined} onClick={scan}>
                {t("patches.scanNow")}
              </Button>
              <Button
                size="sm"
                icon={data.updates.length === 0 && data.rebootPending ? <RotateCcw className="size-4" /> : <Download className="size-4" />}
                loading={busy === "install"}
                disabled={!device.online || !!open || (selected.size === 0 && approvedCount === 0 && !data.rebootPending)}
                title={!device.online ? t("devices.offlineHint") : undefined}
                onClick={install}
              >
                {selected.size > 0 ? t("patches.installSelected", { count: selected.size }) : data.updates.length === 0 && data.rebootPending ? t("patches.restartNow") : t("patches.installApproved")}
              </Button>
            </div>
          )
        }
      >
        {data.updates.length === 0 ? (
          <p className="rounded-md border border-dashed border-line px-4 py-6 text-center text-sm text-muted">{data.scanAt ? t("patches.upToDate") : t("patches.neverScanned")}</p>
        ) : (
          <div className="-mx-5 -my-5 max-h-[560px] overflow-auto">
            <table className="w-full min-w-[760px] text-sm">
              <thead className="sticky top-0 border-b border-line bg-surface text-left text-xs text-muted uppercase">
                <tr>
                  {manage && (
                    <th className="w-10 px-5 py-3">
                      <span className="sr-only">{t("patches.select")}</span>
                    </th>
                  )}
                  <th className={`${manage ? "px-3" : "px-5"} py-3 font-medium`}>{t("patches.update")}</th>
                  <th className="px-3 py-3 font-medium">{t("patches.category")}</th>
                  <th className="px-3 py-3 font-medium">{t("patches.size")}</th>
                  <th className="px-5 py-3 font-medium">{t("patches.approval")}</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-line">
                {data.updates.map((u) => (
                  <tr key={u.updateId}>
                    {manage && (
                      <td className="px-5 py-2">
                        <input type="checkbox" className="size-4 accent-primary" checked={selected.has(u.updateId)} onChange={() => toggle(u.updateId)} aria-label={u.title} />
                      </td>
                    )}
                    <td className={`${manage ? "px-3" : "px-5"} py-2`}>
                      <div className="font-medium">{u.title}</div>
                      <div className="text-xs text-muted">
                        {[u.kb, u.severity, u.currentVersion && u.newVersion ? `${u.currentVersion} → ${u.newVersion}` : "", u.reboot ? t("patches.needsRestart") : ""]
                          .filter(Boolean)
                          .join(" · ")}
                      </div>
                    </td>
                    <td className="px-3 py-2">
                      <CategoryBadge category={u.category} />
                    </td>
                    <td className="px-3 py-2 whitespace-nowrap text-muted">{u.sizeBytes ? formatBytes(u.sizeBytes, i18n.language) : ""}</td>
                    <td className="px-5 py-2">
                      {u.decision === "declined" ? (
                        <Badge tone="danger">{t("patches.decisions.declined")}</Badge>
                      ) : u.approved ? (
                        <Badge tone="success">{u.decision === "approved" ? t("patches.decisions.approved") : t("patches.autoApproved")}</Badge>
                      ) : u.approvedFrom ? (
                        <span className="text-xs text-muted">{t("patches.approvedFrom", { when: formatDate(u.approvedFrom, i18n.language) })}</span>
                      ) : (
                        <span className="text-xs text-muted">{t("patches.awaitingApproval")}</span>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Card>

      <Card title={t("patches.history")}>
        {data.runs.length === 0 ? <p className="rounded-md border border-dashed border-line px-4 py-6 text-center text-sm text-muted">{t("patches.noRuns")}</p> : <RunsTable runs={data.runs} lang={i18n.language} />}
      </Card>
    </div>
  );
}
