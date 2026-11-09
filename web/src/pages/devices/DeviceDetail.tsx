import { useCallback, useEffect, useMemo, useState, type ReactNode } from "react";
import { Link, useNavigate, useParams } from "react-router";
import { useTranslation } from "react-i18next";
import { ArrowLeft, FolderInput, PackagePlus, RefreshCw, Search, Trash2 } from "lucide-react";
import { del, get, patch, post, type Device, type PackagesView, type SoftwareJob, type SoftwareRow } from "../../api";
import { useAuth } from "../../auth";
import { Alert, Badge, Button, Card, Input, Modal, Spinner, formatDate, useErrorText } from "../../components/ui";
import { MoveDialog } from "./Devices";
import { DevicePatchesTab } from "../patches/DevicePatches";
import { DeviceDriversTab } from "../drivers/DeviceDrivers";
import { DeployDialog, type DeployTarget } from "../software/DeployDialog";
import { JobDialog, JobsTable } from "../software/Deployments";
import { SourceBadge } from "../software/util";
import { RemoteButton } from "../remote/Remote";
import { DeviceConfigsTab } from "../configurations/DeviceConfigs";
import { PowerButtons } from "../power/DevicePower";
import { KindIcon, PlatformBadge, StatusDot, formatBytes, timeAgo } from "./util";

type Tab = "overview" | "software" | "updates" | "drivers" | "configurations" | "notes";

export function DeviceDetail() {
  const { id = "" } = useParams();
  const { t, i18n } = useTranslation();
  const { can } = useAuth();
  const navigate = useNavigate();
  const errorText = useErrorText();
  const [device, setDevice] = useState<Device | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [tab, setTab] = useState<Tab>("overview");
  const [moving, setMoving] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const manage = can("devices.manage");

  const load = useCallback(() => {
    get<Device>(`/devices/${id}`)
      .then(setDevice)
      .catch((e) => setError(errorText(e)));
  }, [id]);
  useEffect(load, [load]);
  useEffect(() => {
    const t = setInterval(load, 20000);
    return () => clearInterval(t);
  }, [load]);

  if (error && !device) return <Alert tone="danger">{error}</Alert>;
  if (!device) return <Spinner />;
  const hw = device.hardware;

  return (
    <div className="space-y-6">
      <Link to="/devices" className="inline-flex items-center gap-1.5 text-sm text-muted hover:text-fg">
        <ArrowLeft className="size-4" /> {t("devices.title")}
      </Link>
      <div className="flex flex-wrap items-start justify-between gap-4">
        <div className="flex items-center gap-4">
          <span className="grid size-12 place-items-center rounded-lg bg-primary/10 text-primary">
            <KindIcon device={device} className="size-6" />
          </span>
          <div>
            <h1 className="text-2xl font-semibold tracking-tight">{device.hostname}</h1>
            <div className="mt-1 flex flex-wrap items-center gap-3 text-sm text-muted">
              <StatusDot online={device.online} />
              <PlatformBadge platform={device.platform} />
              <span>{t(`devices.kinds.${device.kind}`)}</span>
              <span>·</span>
              <span>{device.groupName}</span>
              {!device.online && <span>· {t("devices.lastSeenAgo", { when: timeAgo(device.lastSeenAt, i18n.language) })}</span>}
              {device.online && device.connectedVia && <span>· {t("devices.viaGateway", { name: device.connectedVia })}</span>}
            </div>
          </div>
        </div>
        {(manage || can("remote.control") || can("power.control")) && (
          <div className="flex flex-wrap gap-2">
            {can("remote.control") && <RemoteButton device={device} />}
            {can("power.control") && (
              <PowerButtons
                device={device}
                onDone={(m) => {
                  setNotice(m);
                  setTimeout(load, 5000);
                }}
              />
            )}
            {manage && (
              <>
                <Button
                  variant="secondary"
                  icon={<RefreshCw className="size-4" />}
                  disabled={!device.online}
                  title={!device.online ? t("devices.offlineHint") : undefined}
                  onClick={() =>
                    post(`/devices/${device.id}/inventory`)
                      .then(() => {
                        setNotice(t("devices.inventoryRequested"));
                        setTimeout(load, 5000);
                      })
                      .catch((e) => setError(errorText(e)))
                  }
                >
                  {t("devices.refreshInventory")}
                </Button>
                <Button variant="secondary" icon={<FolderInput className="size-4" />} onClick={() => setMoving(true)}>
                  {t("devices.moveTo")}
                </Button>
                <Button variant="ghost" icon={<Trash2 className="size-4" />} onClick={() => setDeleting(true)}>
                  {t("common.delete")}
                </Button>
              </>
            )}
          </div>
        )}
      </div>
      {error && <Alert tone="danger">{error}</Alert>}
      {notice && <Alert tone="success">{notice}</Alert>}

      <div className="flex gap-1 border-b border-line" role="tablist">
        {(["overview", "software", "updates", "drivers", "configurations", "notes"] as Tab[]).map((k) => (
          <button
            key={k}
            role="tab"
            aria-selected={tab === k}
            onClick={() => setTab(k)}
            className={`-mb-px border-b-2 px-4 py-2 text-sm font-medium ${tab === k ? "border-primary text-fg" : "border-transparent text-muted hover:text-fg"}`}
          >
            {t(`devices.tabs.${k}`)}
          </button>
        ))}
      </div>

      {tab === "overview" && (
        <div className="grid gap-6 lg:grid-cols-2">
          <Card title={t("devices.system")}>
            <Facts
              rows={[
                [t("devices.os"), device.osName],
                [t("devices.osVersion"), [device.osVersion, device.osBuild && `(${device.osBuild})`].filter(Boolean).join(" ")],
                [t("devices.domain"), device.domainJoined ? device.domain : t("devices.notJoined")],
                [t("devices.ou"), device.ou],
                [t("devices.user"), device.loggedInUser || t("devices.nobody")],
                [t("devices.localIps"), (device.localIps ?? []).join(", ")],
                [t("devices.remoteIp"), device.remoteIp],
                [t("devices.bootTime"), device.bootTime ? `${formatDate(device.bootTime, i18n.language)} (${timeAgo(device.bootTime, i18n.language)})` : ""],
                [t("devices.agentVersion"), device.agentVersion],
                [
                  t("devices.agentUpdate"),
                  device.updateVersion && device.updateState && device.updateState !== "completed" ? (
                    <span title={device.updateMessage}>
                      <Badge tone={device.updateState === "failed" || device.updateState === "rolled-back" ? "danger" : "primary"}>
                        {t("devices.agentUpdateTo", { state: t(`agentUpdates.state.${device.updateState}`), version: device.updateVersion })}
                      </Badge>
                      {device.updateMessage && <span className="mt-1 block text-xs text-muted">{device.updateMessage}</span>}
                    </span>
                  ) : (
                    ""
                  ),
                ],
                [t("devices.enrolled"), formatDate(device.enrolledAt, i18n.language)],
                [t("devices.inventoryAt"), device.inventoryAt ? timeAgo(device.inventoryAt, i18n.language) : t("devices.noInventory")],
              ]}
            />
          </Card>
          <Card title={t("devices.hardware")}>
            <Facts
              rows={[
                [t("devices.manufacturer"), device.manufacturer],
                [
                  t("devices.model"),
                  <>
                    {device.model} {hw?.virtual && <Badge>{t("devices.virtual")}</Badge>}
                  </>,
                ],
                [t("devices.serial"), device.serial],
                [t("devices.bios"), hw?.bios],
                [t("devices.cpu"), device.cpu],
                [t("devices.cores"), hw ? t("devices.coresValue", { cores: hw.cpuCores, threads: hw.cpuThreads }) : ""],
                [t("devices.memory"), device.memoryBytes ? formatBytes(device.memoryBytes, i18n.language) : ""],
                [t("devices.gpus"), (hw?.gpus ?? []).join(", ")],
              ]}
            />
          </Card>
          <Card title={t("devices.storage")}>
            <div className="space-y-4">
              {(hw?.volumes ?? []).map((v) => {
                const used = v.sizeBytes ? 1 - v.freeBytes / v.sizeBytes : 0;
                return (
                  <div key={v.mount}>
                    <div className="flex justify-between text-sm">
                      <span className="font-medium">
                        {v.mount} <span className="text-xs text-muted">{v.fileSystem}</span>
                      </span>
                      <span className="text-muted">
                        {t("devices.free", { free: formatBytes(v.freeBytes, i18n.language), size: formatBytes(v.sizeBytes, i18n.language) })}
                      </span>
                    </div>
                    <div className="mt-1.5 h-2 overflow-hidden rounded-full bg-surface-muted">
                      <div
                        className={`h-full rounded-full ${used > 0.9 ? "bg-danger" : used > 0.75 ? "bg-warning" : "bg-primary"}`}
                        style={{ width: `${Math.round(used * 100)}%` }}
                      />
                    </div>
                  </div>
                );
              })}
              {(hw?.disks ?? []).length > 0 && (
                <ul className="divide-y divide-line rounded-md border border-line text-sm">
                  {(hw?.disks ?? []).map((d) => (
                    <li key={d.name} className="flex items-center justify-between gap-3 px-3 py-2">
                      <span className="truncate">{d.model || d.name}</span>
                      <span className="flex shrink-0 items-center gap-2 text-muted">
                        {d.media && <Badge>{d.media.toUpperCase()}</Badge>}
                        {formatBytes(d.sizeBytes, i18n.language)}
                      </span>
                    </li>
                  ))}
                </ul>
              )}
              {!hw?.volumes?.length && !hw?.disks?.length && <p className="text-sm text-muted">{t("devices.noInventory")}</p>}
            </div>
          </Card>
          <Card title={t("devices.network")}>
            <ul className="divide-y divide-line text-sm">
              {(hw?.network ?? []).map((n) => (
                <li key={n.name + n.mac} className="py-2 first:pt-0 last:pb-0">
                  <div className="flex justify-between gap-3">
                    <span className="truncate font-medium">{n.name}</span>
                    <code className="shrink-0 font-mono text-xs text-muted">{n.mac}</code>
                  </div>
                  <div className="text-xs text-muted">{(n.ips ?? []).join(", ")}</div>
                </li>
              ))}
              {!hw?.network?.length && <li className="text-muted">{t("devices.noInventory")}</li>}
            </ul>
          </Card>
        </div>
      )}
      {tab === "software" && <SoftwareTab device={device} />}
      {tab === "updates" && <DevicePatchesTab device={device} />}
      {tab === "drivers" && <DeviceDriversTab device={device} />}
      {tab === "configurations" && <DeviceConfigsTab device={device} />}
      {tab === "notes" && <NotesTab device={device} editable={manage} onSaved={load} />}

      <MoveDialog
        open={moving}
        ids={[device.id]}
        onClose={() => setMoving(false)}
        onDone={() => {
          setMoving(false);
          load();
        }}
      />
      <Modal
        open={deleting}
        onClose={() => setDeleting(false)}
        title={t("devices.deleteTitle")}
        footer={
          <>
            <Button variant="secondary" onClick={() => setDeleting(false)}>
              {t("common.cancel")}
            </Button>
            <Button
              variant="danger"
              onClick={() =>
                del(`/devices/${device.id}`)
                  .then(() => navigate("/devices"))
                  .catch((e) => {
                    setDeleting(false);
                    setError(errorText(e));
                  })
              }
            >
              {t("common.delete")}
            </Button>
          </>
        }
      >
        <p className="text-sm">{t("devices.deleteConfirm", { host: device.hostname })}</p>
      </Modal>
    </div>
  );
}

function Facts({ rows }: { rows: [string, ReactNode][] }) {
  return (
    <dl className="grid grid-cols-[minmax(110px,40%)_1fr] gap-x-4 gap-y-2.5 text-sm">
      {rows.map(([k, v]) => (
        <div key={k} className="contents">
          <dt className="text-muted">{k}</dt>
          <dd className="min-w-0 break-words">{v || "–"}</dd>
        </div>
      ))}
    </dl>
  );
}

function SoftwareTab({ device }: { device: Device }) {
  const { t, i18n } = useTranslation();
  const { can } = useAuth();
  const deploy = can("software.deploy");
  const id = device.id;
  const [list, setList] = useState<SoftwareRow[] | null>(null);
  const [jobs, setJobs] = useState<SoftwareJob[]>([]);
  const [filter, setFilter] = useState("");
  const [target, setTarget] = useState<DeployTarget | null>(null);
  const [picking, setPicking] = useState(false);
  const [job, setJob] = useState<SoftwareJob | null>(null);
  useEffect(() => {
    get<SoftwareRow[]>(`/devices/${id}/software`)
      .then(setList)
      .catch(() => setList([]));
  }, [id]);
  useEffect(() => {
    const load = () =>
      get<SoftwareJob[]>(`/devices/${id}/software-jobs`)
        .then(setJobs)
        .catch(() => setJobs([]));
    load();
    const tm = setInterval(load, 10000);
    return () => clearInterval(tm);
  }, [id]);
  const shown = useMemo(() => {
    const f = filter.trim().toLowerCase();
    return (list ?? []).filter((s) => !f || s.name.toLowerCase().includes(f) || s.publisher.toLowerCase().includes(f));
  }, [list, filter]);
  const agentRow = (name: string) => name.toLowerCase().startsWith("fleetwarden");
  return (
    <div className="space-y-6">
      {(deploy || jobs.length > 0) && (
        <Card
          title={t("devices.softwareJobs")}
          actions={
            deploy && (
              <Button size="sm" icon={<PackagePlus className="size-4" />} onClick={() => setPicking(true)}>
                {t("devices.installSoftware")}
              </Button>
            )
          }
        >
          <JobsTable jobs={jobs.slice(0, 20)} onOpen={setJob} showDeployment />
        </Card>
      )}
      <Card
        title={t("devices.softwareCount", { count: list?.length ?? 0 })}
        actions={
          <div className="relative w-64 max-w-full">
            <Search className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted" />
            <Input
              value={filter}
              onChange={(e) => setFilter(e.target.value)}
              placeholder={t("devices.filterSoftware")}
              className="pl-9"
              aria-label={t("devices.filterSoftware")}
            />
          </div>
        }
      >
        {!list ? (
          <Spinner />
        ) : (
          <div className="-mx-5 -my-5 max-h-[600px] overflow-auto">
            <table className="w-full min-w-[640px] text-sm">
              <thead className="sticky top-0 border-b border-line bg-surface text-left text-xs text-muted uppercase">
                <tr>
                  <th className="px-5 py-3 font-medium">{t("devices.program")}</th>
                  <th className="px-3 py-3 font-medium">{t("devices.version")}</th>
                  <th className="px-3 py-3 font-medium">{t("devices.publisher")}</th>
                  <th className="px-3 py-3 font-medium">{t("devices.installedOn")}</th>
                  <th className="px-3 py-3 text-right font-medium">{t("devices.size")}</th>
                  {deploy && <th className="px-5 py-3" />}
                </tr>
              </thead>
              <tbody className="divide-y divide-line">
                {shown.map((s, i) => (
                  <tr key={`${s.name}-${s.version}-${i}`} className="group">
                    <td className="px-5 py-2">
                      <Link to={`/devices?software=${encodeURIComponent(s.name)}`} className="hover:text-primary" title={t("devices.findOthers")}>
                        {s.name}
                      </Link>
                    </td>
                    <td className="px-3 py-2 font-mono text-xs">{s.version}</td>
                    <td className="max-w-[220px] truncate px-3 py-2 text-muted">{s.publisher}</td>
                    <td className="px-3 py-2 whitespace-nowrap text-muted">{s.installedOn ?? ""}</td>
                    <td className="px-3 py-2 text-right whitespace-nowrap text-muted">{s.sizeBytes ? formatBytes(s.sizeBytes, i18n.language) : ""}</td>
                    {deploy && (
                      <td className="px-5 py-1 text-right">
                        {!agentRow(s.name) && (
                          <Button
                            size="sm"
                            variant="ghost"
                            className="opacity-60 group-hover:opacity-100 focus-visible:opacity-100"
                            title={t("software.uninstall")}
                            aria-label={`${t("software.uninstall")}: ${s.name}`}
                            onClick={() => setTarget({ action: "uninstall", name: s.name, platform: device.platform, versions: [s.version], deviceIds: [id] })}
                          >
                            <Trash2 className="size-4" />
                          </Button>
                        )}
                      </td>
                    )}
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Card>
      <PackagePicker
        open={picking}
        device={device}
        onClose={() => setPicking(false)}
        onPick={(p) => {
          setPicking(false);
          setTarget({ action: "install", packageId: p.id, name: p.name, platform: p.platform, deviceIds: [id] });
        }}
      />
      <DeployDialog target={target} onClose={() => setTarget(null)} />
      <JobDialog job={job} onClose={() => setJob(null)} />
    </div>
  );
}

function PackagePicker({
  open,
  device,
  onClose,
  onPick,
}: {
  open: boolean;
  device: Device;
  onClose: () => void;
  onPick: (p: PackagesView["packages"][number]) => void;
}) {
  const { t } = useTranslation();
  const [view, setView] = useState<PackagesView | null>(null);
  useEffect(() => {
    if (open)
      get<PackagesView>("/software/packages")
        .then(setView)
        .catch(() => setView(null));
  }, [open]);
  const pkgs = (view?.packages ?? []).filter((p) => p.platform === device.platform);
  return (
    <Modal open={open} onClose={onClose} title={t("devices.installSoftware")}>
      {!view ? (
        <Spinner />
      ) : pkgs.length === 0 ? (
        <p className="text-sm text-muted">
          {t("devices.noPackages")}{" "}
          <Link to="/software" className="font-medium text-primary hover:underline">
            {t("nav.software")}
          </Link>
        </p>
      ) : (
        <ul className="max-h-96 divide-y divide-line overflow-auto">
          {pkgs.map((p) => (
            <li key={p.id}>
              <button className="flex w-full items-center gap-3 px-2 py-2 text-left text-sm hover:bg-surface-muted" onClick={() => onPick(p)}>
                <span className="flex-1">
                  <span className="font-medium">{p.name}</span>
                  {p.publisher && <span className="block text-xs text-muted">{p.publisher}</span>}
                </span>
                <SourceBadge source={p.source} />
              </button>
            </li>
          ))}
        </ul>
      )}
    </Modal>
  );
}

function NotesTab({ device, editable, onSaved }: { device: Device; editable: boolean; onSaved: () => void }) {
  const { t } = useTranslation();
  const errorText = useErrorText();
  const [notes, setNotes] = useState(device.notes);
  const [state, setState] = useState<{ busy?: boolean; ok?: boolean; error?: string }>({});
  return (
    <Card title={t("devices.tabs.notes")} description={t("devices.notesHint")}>
      <div className="space-y-3">
        {state.error && <Alert tone="danger">{state.error}</Alert>}
        {state.ok && <Alert tone="success">{t("common.saved")}</Alert>}
        <textarea
          value={notes}
          onChange={(e) => setNotes(e.target.value)}
          disabled={!editable}
          rows={8}
          maxLength={4000}
          aria-label={t("devices.tabs.notes")}
          className="w-full rounded-md border border-line bg-surface px-3 py-2 text-sm outline-none focus-visible:ring-2 focus-visible:ring-primary"
        />
        {editable && (
          <Button
            loading={state.busy}
            onClick={() => {
              setState({ busy: true });
              patch(`/devices/${device.id}`, { notes })
                .then(() => {
                  setState({ ok: true });
                  onSaved();
                })
                .catch((e) => setState({ error: errorText(e) }));
            }}
          >
            {t("common.save")}
          </Button>
        )}
      </div>
    </Card>
  );
}
