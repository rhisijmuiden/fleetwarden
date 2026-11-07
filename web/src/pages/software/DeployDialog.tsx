import { useEffect, useMemo, useState } from "react";
import { useNavigate } from "react-router";
import { useTranslation } from "react-i18next";
import { Search } from "lucide-react";
import { get, post, type Deployment, type DeviceGroup, type DeviceList, type Platform } from "../../api";
import { Alert, Button, Field, Input, Modal, Switch, useErrorText } from "../../components/ui";
import { PlatformBadge } from "../devices/util";

export type DeployTarget =
  | { action: "install"; packageId: number; name: string; platform: Platform; deviceIds?: string[] }
  | { action: "uninstall"; name: string; platform: Platform; versions: string[]; deviceIds?: string[] };

// DeployDialog picks devices and groups, the start time and the expiry for
// an install or uninstall.
export function DeployDialog({ target, onClose }: { target: DeployTarget | null; onClose: () => void }) {
  const { t } = useTranslation();
  const errorText = useErrorText();
  const navigate = useNavigate();
  const [groups, setGroups] = useState<DeviceGroup[]>([]);
  const [devices, setDevices] = useState<DeviceList["items"]>([]);
  const [groupIds, setGroupIds] = useState<number[]>([]);
  const [deviceIds, setDeviceIds] = useState<string[]>([]);
  const [filter, setFilter] = useState("");
  const [ongoing, setOngoing] = useState(true);
  const [later, setLater] = useState(false);
  const [startAt, setStartAt] = useState("");
  const [expireDays, setExpireDays] = useState(7);
  const [version, setVersion] = useState("");
  const [args, setArgs] = useState("");
  const [purge, setPurge] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!target) return;
    setGroupIds([]);
    setDeviceIds(target.deviceIds ?? []);
    setFilter("");
    setOngoing(target.action === "install");
    setLater(false);
    setStartAt("");
    setExpireDays(7);
    setVersion("");
    setArgs("");
    setPurge(false);
    setError(null);
    get<DeviceGroup[]>("/groups")
      .then(setGroups)
      .catch(() => setGroups([]));
    get<DeviceList>(`/devices?platform=${target.platform}&limit=1000`)
      .then((l) => setDevices(l.items))
      .catch(() => setDevices([]));
  }, [target]);

  const shown = useMemo(() => {
    const f = filter.trim().toLowerCase();
    const list = f ? devices.filter((d) => d.hostname.toLowerCase().includes(f) || d.groupName.toLowerCase().includes(f)) : devices;
    return list.slice(0, 200);
  }, [devices, filter]);

  // Only groups with devices of this platform (or already picked) are offered.
  const shownGroups = groups.filter((g) => groupIds.includes(g.id) || devices.some((d) => d.groupId === g.id));

  if (!target) return null;
  const toggle = <T,>(list: T[], v: T) => (list.includes(v) ? list.filter((x) => x !== v) : [...list, v]);
  const groupDevices = devices.filter((d) => groupIds.includes(d.groupId)).length;
  const extra = deviceIds.filter((id) => !devices.some((d) => d.id === id && groupIds.includes(d.groupId))).length;

  const submit = async () => {
    setBusy(true);
    setError(null);
    try {
      const body: Record<string, unknown> = {
        action: target.action,
        groupIds,
        deviceIds,
        ongoing: groupIds.length > 0 && ongoing,
        expireDays,
        startAt: later && startAt ? new Date(startAt).toISOString() : "",
      };
      if (target.action === "install") body.packageId = target.packageId;
      else body.uninstall = { name: target.name, version, platform: target.platform, args, purge };
      const d = await post<Deployment>("/deployments", body);
      onClose();
      navigate(`/deployments/${d.id}`);
    } catch (e) {
      setError(errorText(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Modal
      open
      wide
      onClose={onClose}
      title={target.action === "install" ? t("software.deploy.titleInstall", { name: target.name }) : t("software.deploy.titleUninstall", { name: target.name })}
      footer={
        <>
          <Button variant="secondary" onClick={onClose}>
            {t("common.cancel")}
          </Button>
          <Button variant={target.action === "uninstall" ? "danger" : "primary"} loading={busy} disabled={groupIds.length === 0 && deviceIds.length === 0} onClick={submit}>
            {target.action === "install" ? t("software.deploy.start") : t("software.deploy.startUninstall")}
          </Button>
        </>
      }
    >
      <div className="space-y-5">
        {error && <Alert tone="danger">{error}</Alert>}
        <p className="flex items-center gap-2 text-sm text-muted">
          <PlatformBadge platform={target.platform} /> {t("software.deploy.platformHint")}
        </p>

        <div className="grid gap-5 md:grid-cols-2">
          <div className="space-y-2">
            <div className="text-sm font-medium">{t("software.deploy.groups")}</div>
            <div className="max-h-56 space-y-1 overflow-auto rounded-md border border-line p-2">
              {shownGroups.length === 0 && <p className="px-2 py-1 text-sm text-muted">{t("software.deploy.noGroups")}</p>}
              {shownGroups.map((g) => (
                <label key={g.id} className="flex cursor-pointer items-center gap-2 rounded px-2 py-1 text-sm hover:bg-surface-muted">
                  <input type="checkbox" checked={groupIds.includes(g.id)} onChange={() => setGroupIds((l) => toggle(l, g.id))} className="accent-primary" />
                  <span className="flex-1">{g.name}</span>
                  <span className="text-xs text-muted">{t("software.deploy.groupDevices", { count: devices.filter((d) => d.groupId === g.id).length })}</span>
                </label>
              ))}
            </div>
            {groupIds.length > 0 && <Switch checked={ongoing} onChange={setOngoing} label={t("software.deploy.ongoing")} description={t("software.deploy.ongoingHint")} />}
          </div>
          <div className="space-y-2">
            <div className="text-sm font-medium">{t("software.deploy.devices")}</div>
            <div className="relative">
              <Search className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted" />
              <Input
                value={filter}
                onChange={(e) => setFilter(e.target.value)}
                placeholder={t("software.deploy.findDevice")}
                className="pl-9"
                aria-label={t("software.deploy.findDevice")}
              />
            </div>
            <div className="max-h-48 space-y-1 overflow-auto rounded-md border border-line p-2">
              {shown.length === 0 && <p className="px-2 py-1 text-sm text-muted">{t("software.deploy.noDevices")}</p>}
              {shown.map((d) => (
                <label key={d.id} className="flex cursor-pointer items-center gap-2 rounded px-2 py-1 text-sm hover:bg-surface-muted">
                  <input type="checkbox" checked={deviceIds.includes(d.id)} onChange={() => setDeviceIds((l) => toggle(l, d.id))} className="accent-primary" />
                  <span className="flex-1 truncate">{d.hostname}</span>
                  <span className="truncate text-xs text-muted">{d.groupName}</span>
                </label>
              ))}
            </div>
          </div>
        </div>
        <p className="text-sm text-muted">{t("software.deploy.summary", { count: groupDevices + extra })}</p>

        {target.action === "uninstall" && (
          <div className="grid gap-4 md:grid-cols-2">
            {target.versions.length > 1 && (
              <Field label={t("software.deploy.version")}>
                {(id) => (
                  <select id={id} value={version} onChange={(e) => setVersion(e.target.value)} className="h-9 w-full rounded-md border border-line bg-surface px-3 text-sm">
                    <option value="">{t("software.deploy.anyVersion")}</option>
                    {target.versions.map((v) => (
                      <option key={v} value={v}>
                        {v}
                      </option>
                    ))}
                  </select>
                )}
              </Field>
            )}
            <Field label={t("software.deploy.uninstallArgs")} hint={target.platform === "windows" ? t("software.deploy.uninstallArgsHint") : t("software.deploy.aptArgsHint")}>
              {(id) => <Input id={id} value={args} onChange={(e) => setArgs(e.target.value)} placeholder={target.platform === "windows" ? "/S" : "--auto-remove"} />}
            </Field>
            {target.platform === "linux" && <Switch checked={purge} onChange={setPurge} label={t("software.deploy.purge")} description={t("software.deploy.purgeHint")} />}
          </div>
        )}

        <div className="grid gap-4 md:grid-cols-2">
          <div className="space-y-2">
            <Switch checked={later} onChange={setLater} label={t("software.deploy.later")} description={t("software.deploy.laterHint")} />
            {later && <Input type="datetime-local" value={startAt} onChange={(e) => setStartAt(e.target.value)} aria-label={t("software.deploy.startAt")} />}
          </div>
          <Field label={t("software.deploy.expire")} hint={t("software.deploy.expireHint")}>
            {(id) => <Input id={id} type="number" min={1} max={90} value={expireDays} onChange={(e) => setExpireDays(Number(e.target.value) || 7)} />}
          </Field>
        </div>
      </div>
    </Modal>
  );
}
