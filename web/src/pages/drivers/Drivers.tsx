import { useCallback, useEffect, useState } from "react";
import { Link, useSearchParams } from "react-router";
import { useTranslation } from "react-i18next";
import {
  Archive,
  Ban,
  Cpu,
  Download,
  HardDrive,
  PackageCheck,
  RotateCcw,
  Search,
  Trash2,
  TriangleAlert,
} from "lucide-react";
import {
  get,
  post,
  type DriverDetail,
  type DriverSummary,
  type DriverUpdate,
} from "../../api";
import { useAuth } from "../../auth";
import {
  Alert,
  Badge,
  Button,
  Card,
  Input,
  Modal,
  PageHeader,
  Select,
  Spinner,
  formatDate,
  useErrorText,
} from "../../components/ui";
import { PlatformBadge, StatusDot, formatBytes } from "../devices/util";
import { DriverStateBadge, KindBadge, driverMeta, sourceName } from "./util";
import { UploadList, VendorCatalogs } from "./Vendors";

type Tab = "available" | "repository" | "uploads" | "vendors" | "rejected";
const tabs: Tab[] = [
  "available",
  "repository",
  "uploads",
  "vendors",
  "rejected",
];
type ListTab = "available" | "repository" | "rejected";

export function Drivers() {
  const { t } = useTranslation();
  const [params, setParams] = useSearchParams();
  const tab = (
    tabs.includes(params.get("tab") as Tab) ? params.get("tab") : "available"
  ) as Tab;
  const [n, setN] = useState(0);
  return (
    <div className="space-y-6">
      <PageHeader
        title={t("drivers.title")}
        description={t("drivers.subtitle")}
      />
      <Summary refresh={n} />
      <div className="flex gap-1 border-b border-line" role="tablist">
        {tabs.map((k) => (
          <button
            key={k}
            role="tab"
            aria-selected={tab === k}
            onClick={() => setParams(k === "available" ? {} : { tab: k })}
            className={`-mb-px border-b-2 px-4 py-2 text-sm font-medium ${tab === k ? "border-primary text-fg" : "border-transparent text-muted hover:text-fg"}`}
          >
            {t(`drivers.tabs.${k}`)}
          </button>
        ))}
      </div>
      {tab === "uploads" ? (
        <UploadList onChanged={() => setN((x) => x + 1)} />
      ) : tab === "vendors" ? (
        <VendorCatalogs onChanged={() => setN((x) => x + 1)} />
      ) : (
        <DriverList key={tab} tab={tab} onChanged={() => setN((x) => x + 1)} />
      )}
    </div>
  );
}

function Stat({
  label,
  value,
  tone,
  icon,
}: {
  label: string;
  value: number | string;
  tone?: string;
  icon: React.ReactNode;
}) {
  return (
    <div className="rounded-lg border border-line bg-surface p-4">
      <div className="flex items-center gap-2 text-xs font-medium text-muted uppercase">
        {icon}
        {label}
      </div>
      <div className={`mt-2 text-2xl font-semibold tabular-nums ${tone ?? ""}`}>
        {value}
      </div>
    </div>
  );
}

function Summary({ refresh }: { refresh: number }) {
  const { t, i18n } = useTranslation();
  const [sum, setSum] = useState<DriverSummary | null>(null);
  const load = useCallback(() => {
    get<DriverSummary>("/drivers/summary")
      .then(setSum)
      .catch(() => {});
  }, []);
  useEffect(() => {
    load();
    const id = setInterval(load, 10000);
    return () => clearInterval(id);
  }, [load, refresh]);
  if (!sum) return null;
  const s = sum.stats;
  return (
    <div className="space-y-3">
      <div className="grid grid-cols-2 gap-4 md:grid-cols-4">
        <Stat
          label={t("drivers.stats.available")}
          value={s.available}
          tone={s.available ? "text-warning" : ""}
          icon={<Cpu className="size-4" />}
        />
        <Stat
          label={t("drivers.stats.ready")}
          value={s.ready}
          icon={<PackageCheck className="size-4" />}
        />
        <Stat
          label={t("drivers.stats.queued")}
          value={s.queued}
          icon={<Download className="size-4" />}
        />
        <Stat
          label={t("drivers.stats.size")}
          value={formatBytes(s.storedBytes, i18n.language)}
          icon={<HardDrive className="size-4" />}
        />
      </div>
      <p className="text-xs text-muted">
        {t("drivers.stats.devices", { devices: s.devices, scanned: s.scanned })}
        {sum.freeBytes != null &&
          ` · ${t("drivers.stats.free", { size: formatBytes(sum.freeBytes, i18n.language) })}`}
      </p>
      {s.failed > 0 && (
        <Alert tone="warning">
          {t("drivers.failedHint", { count: s.failed })}
        </Alert>
      )}
    </div>
  );
}

function DriverList({
  tab,
  onChanged,
}: {
  tab: ListTab;
  onChanged: () => void;
}) {
  const { t, i18n } = useTranslation();
  const { can } = useAuth();
  const errorText = useErrorText();
  const [list, setList] = useState<DriverUpdate[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [search, setSearch] = useState("");
  const [kind, setKind] = useState("");
  const [selected, setSelected] = useState<Set<number>>(new Set());
  const [busy, setBusy] = useState("");
  const [open, setOpen] = useState<number | null>(null);
  const [n, setN] = useState(0);
  const manage = can("drivers.manage");

  useEffect(() => {
    const q = new URLSearchParams();
    if (search.trim()) q.set("search", search.trim());
    if (kind) q.set("kind", kind);
    q.set("state", tab === "available" ? "" : tab);
    if (tab === "available") q.delete("state");
    const ctl = new AbortController();
    const timer = setTimeout(() => {
      get<DriverUpdate[]>(`/drivers?${q}`, ctl.signal)
        .then((l) =>
          setList(
            tab === "available"
              ? l.filter(
                  (u) =>
                    u.state === "available" ||
                    (u.state === "failed" && u.source !== "upload"),
                )
              : l.filter((u) => u.source !== "upload"),
          ),
        )
        .catch((e) => !ctl.signal.aborted && setError(errorText(e)));
    }, 250);
    return () => {
      clearTimeout(timer);
      ctl.abort();
    };
  }, [search, kind, tab, n]);

  // Downloads in progress: keep the list fresh.
  useEffect(() => {
    if (!list?.some((u) => u.state === "queued" || u.state === "downloading"))
      return;
    const id = setInterval(() => setN((x) => x + 1), 5000);
    return () => clearInterval(id);
  }, [list]);

  const act = (action: "add" | "reject" | "remove", ids: number[]) => {
    setBusy(action);
    setError(null);
    post("/drivers/state", { ids, action })
      .then(() => {
        setSelected(new Set());
        setN((x) => x + 1);
        onChanged();
      })
      .catch((e) => setError(errorText(e)))
      .finally(() => setBusy(""));
  };
  const toggle = (id: number) =>
    setSelected((s) => {
      const x = new Set(s);
      if (x.has(id)) x.delete(id);
      else x.add(id);
      return x;
    });
  const ids = [...selected];

  const actions = manage && selected.size > 0 && (
    <div className="flex flex-wrap gap-2">
      {tab === "available" && (
        <>
          <Button
            size="sm"
            icon={<Archive className="size-4" />}
            loading={busy === "add"}
            onClick={() => act("add", ids)}
          >
            {t("drivers.addSelected", { count: selected.size })}
          </Button>
          <Button
            variant="secondary"
            size="sm"
            icon={<Ban className="size-4" />}
            loading={busy === "reject"}
            onClick={() => act("reject", ids)}
          >
            {t("drivers.reject")}
          </Button>
        </>
      )}
      {tab === "repository" && (
        <Button
          variant="danger"
          size="sm"
          icon={<Trash2 className="size-4" />}
          loading={busy === "remove"}
          onClick={() => act("remove", ids)}
        >
          {t("drivers.removeSelected", { count: selected.size })}
        </Button>
      )}
      {tab === "rejected" && (
        <Button
          variant="secondary"
          size="sm"
          icon={<RotateCcw className="size-4" />}
          loading={busy === "remove"}
          onClick={() => act("remove", ids)}
        >
          {t("drivers.unreject")}
        </Button>
      )}
    </div>
  );

  return (
    <Card
      title={t(`drivers.lists.${tab}`)}
      description={t(`drivers.listHints.${tab}`)}
      actions={actions}
    >
      <div className="space-y-4">
        {error && <Alert tone="danger">{error}</Alert>}
        <div className="flex flex-wrap gap-3">
          <div className="relative min-w-56 flex-1">
            <Search className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted" />
            <Input
              className="pl-9"
              placeholder={t("drivers.search")}
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              aria-label={t("drivers.search")}
            />
          </div>
          <div className="w-44">
            <Select
              value={kind}
              onChange={(e) => setKind(e.target.value)}
              aria-label={t("drivers.kind")}
            >
              <option value="">{t("drivers.allKinds")}</option>
              <option value="driver">{t("drivers.kinds.driver")}</option>
              <option value="firmware">{t("drivers.kinds.firmware")}</option>
            </Select>
          </div>
        </div>
        {!list ? (
          <Spinner />
        ) : list.length === 0 ? (
          <p className="rounded-md border border-dashed border-line px-4 py-6 text-center text-sm text-muted">
            {t(`drivers.empty.${tab}`)}
          </p>
        ) : (
          <div className="-mx-5 -mb-5 overflow-x-auto">
            <table className="w-full min-w-[820px] text-sm">
              <thead className="border-y border-line text-left text-xs text-muted uppercase">
                <tr>
                  {manage && (
                    <th className="w-10 px-5 py-3">
                      <input
                        type="checkbox"
                        className="size-4 accent-primary"
                        checked={
                          selected.size > 0 && selected.size === list.length
                        }
                        onChange={(e) =>
                          setSelected(
                            e.target.checked
                              ? new Set(list.map((u) => u.id))
                              : new Set(),
                          )
                        }
                        aria-label={t("drivers.selectAll")}
                      />
                    </th>
                  )}
                  <th
                    className={`${manage ? "px-3" : "px-5"} py-3 font-medium`}
                  >
                    {t("drivers.update")}
                  </th>
                  <th className="px-3 py-3 font-medium">{t("drivers.kind")}</th>
                  <th className="px-3 py-3 font-medium">
                    {t("drivers.source")}
                  </th>
                  <th className="px-3 py-3 font-medium">
                    {t("drivers.devicesCol")}
                  </th>
                  <th className="px-3 py-3 font-medium">{t("drivers.size")}</th>
                  <th className="px-5 py-3 font-medium">
                    {t("drivers.status")}
                  </th>
                </tr>
              </thead>
              <tbody className="divide-y divide-line">
                {list.map((u) => (
                  <tr key={u.id} className="hover:bg-surface-muted/50">
                    {manage && (
                      <td className="px-5 py-2">
                        <input
                          type="checkbox"
                          className="size-4 accent-primary"
                          checked={selected.has(u.id)}
                          onChange={() => toggle(u.id)}
                          aria-label={u.title}
                        />
                      </td>
                    )}
                    <td className={`${manage ? "px-3" : "px-5"} py-2`}>
                      <button
                        className="text-left font-medium hover:text-primary"
                        onClick={() => setOpen(u.id)}
                      >
                        {u.title}
                      </button>
                      <div className="text-xs text-muted">
                        {driverMeta(u)}
                        {u.critical && (
                          <span className="ml-2">
                            <Badge tone="danger">{t("drivers.critical")}</Badge>
                          </span>
                        )}
                      </div>
                    </td>
                    <td className="px-3 py-2">
                      <KindBadge kind={u.kind} />
                    </td>
                    <td className="px-3 py-2 whitespace-nowrap text-muted">
                      <span className="mr-1.5">{sourceName(u, t)}</span>
                      <PlatformBadge platform={u.platform} />
                    </td>
                    <td className="px-3 py-2 tabular-nums">{u.devices}</td>
                    <td className="px-3 py-2 whitespace-nowrap text-muted">
                      {u.state === "ready"
                        ? formatBytes(u.storedBytes, i18n.language)
                        : u.sizeBytes
                          ? formatBytes(u.sizeBytes, i18n.language)
                          : "–"}
                    </td>
                    <td className="px-5 py-2">
                      <DriverStateBadge state={u.state} />
                      {u.state === "failed" && (
                        <div
                          className="mt-1 max-w-[240px] truncate text-xs text-danger"
                          title={u.error}
                        >
                          {u.error}
                        </div>
                      )}
                      {u.state === "ready" && u.storedAt && (
                        <div className="mt-1 text-xs text-muted">
                          {formatDate(u.storedAt, i18n.language)}
                        </div>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>
      {open != null && (
        <DriverDialog
          id={open}
          onClose={() => setOpen(null)}
          onAction={(action) => {
            act(action, [open]);
            setOpen(null);
          }}
        />
      )}
    </Card>
  );
}

function DriverDialog({
  id,
  onClose,
  onAction,
}: {
  id: number;
  onClose: () => void;
  onAction: (a: "add" | "reject" | "remove") => void;
}) {
  const { t, i18n } = useTranslation();
  const { can } = useAuth();
  const errorText = useErrorText();
  const [d, setD] = useState<DriverDetail | null>(null);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => {
    get<DriverDetail>(`/drivers/${id}`)
      .then(setD)
      .catch((e) => setError(errorText(e)));
  }, [id]);
  const manage = can("drivers.manage");
  const footer =
    d && manage ? (
      <>
        {(d.state === "available" ||
          d.state === "failed" ||
          d.state === "rejected") && (
          <Button
            icon={<Archive className="size-4" />}
            onClick={() => onAction("add")}
          >
            {d.state === "failed" ? t("drivers.retry") : t("drivers.add")}
          </Button>
        )}
        {(d.state === "available" || d.state === "failed") && (
          <Button
            variant="secondary"
            icon={<Ban className="size-4" />}
            onClick={() => onAction("reject")}
          >
            {t("drivers.reject")}
          </Button>
        )}
        {(d.state === "ready" ||
          d.state === "queued" ||
          d.state === "downloading") && (
          <Button
            variant="danger"
            icon={<Trash2 className="size-4" />}
            onClick={() => onAction("remove")}
          >
            {t("drivers.remove")}
          </Button>
        )}
      </>
    ) : undefined;
  return (
    <Modal
      open
      onClose={onClose}
      title={d?.title ?? t("drivers.update")}
      footer={footer}
      wide
    >
      {error && <Alert tone="danger">{error}</Alert>}
      {!d ? (
        !error && <Spinner />
      ) : (
        <div className="space-y-5 text-sm">
          <div className="flex flex-wrap items-center gap-2">
            <KindBadge kind={d.kind} />
            <DriverStateBadge state={d.state} />
            {d.critical && <Badge tone="danger">{t("drivers.critical")}</Badge>}
            {d.reboot && (
              <span className="text-xs text-muted">
                {t("drivers.needsRestart")}
              </span>
            )}
          </div>
          {d.kind === "firmware" && (
            <Alert tone="warning" title={t("drivers.firmwareWarningTitle")}>
              {t("drivers.firmwareWarning")}
            </Alert>
          )}
          {d.state === "failed" && d.error && (
            <Alert tone="danger" title={t("drivers.downloadFailed")}>
              <span className="font-mono text-xs break-all">{d.error}</span>
            </Alert>
          )}
          <dl className="grid grid-cols-1 gap-x-6 gap-y-3 sm:grid-cols-2">
            <Item label={t("drivers.vendor")} value={d.vendor} />
            <Item label={t("drivers.class")} value={d.class} />
            <Item label={t("drivers.version")} value={d.version} />
            <Item
              label={t("drivers.released")}
              value={
                d.releasedAt
                  ? formatDate(d.releasedAt, i18n.language).split(",")[0]
                  : ""
              }
            />
            <Item label={t("drivers.source")} value={sourceName(d, t)} />
            {d.hardwareId && (
              <Item label={t("drivers.hardwareId")} value={d.hardwareId} mono />
            )}
            <Item
              label={t("drivers.installer")}
              value={t(
                `drivers.installers.${d.installer || (d.platform === "linux" ? "fwupd" : "inf")}`,
              )}
            />
            {d.command && (
              <Item label={t("drivers.command")} value={d.command} mono />
            )}
            <Item
              label={t("drivers.firstSeen")}
              value={formatDate(d.firstSeenAt, i18n.language)}
            />
            {d.decidedBy && (
              <Item
                label={t("drivers.decidedBy")}
                value={`${d.decidedBy} · ${formatDate(d.decidedAt, i18n.language)}`}
              />
            )}
          </dl>
          {d.description && (
            <p className="rounded-md bg-surface-muted px-3 py-2 text-xs whitespace-pre-line text-muted">
              {d.description}
            </p>
          )}
          <div>
            <h3 className="mb-2 text-xs font-semibold text-muted uppercase">
              {t("drivers.files")}
            </h3>
            <ul className="space-y-1">
              {d.files.map((f, i) => (
                <li
                  key={i}
                  className="flex flex-wrap items-baseline gap-x-2 text-xs"
                >
                  <span className="font-medium">{f.name}</span>
                  {f.size ? (
                    <span className="text-muted">
                      {formatBytes(f.size, i18n.language)}
                    </span>
                  ) : null}
                  {f.stored ? (
                    <span className="font-mono text-success" title="SHA-256">
                      {f.stored.slice(0, 16)}…
                    </span>
                  ) : (
                    <span className="truncate text-muted" title={f.url}>
                      {f.url ? new URL(f.url).host : ""}
                    </span>
                  )}
                </li>
              ))}
            </ul>
          </div>
          <div>
            <h3 className="mb-2 text-xs font-semibold text-muted uppercase">
              {t("drivers.offeredTo", { count: d.deviceList.length })}
            </h3>
            {d.deviceList.length === 0 ? (
              <p className="text-xs text-muted">{t("drivers.noDevices")}</p>
            ) : (
              <div className="max-h-64 overflow-auto rounded-md border border-line">
                <table className="w-full text-xs">
                  <tbody className="divide-y divide-line">
                    {d.deviceList.map((x) => (
                      <tr key={x.deviceId}>
                        <td className="px-3 py-1.5">
                          <Link
                            to={`/devices/${x.deviceId}`}
                            className="font-medium hover:text-primary"
                          >
                            {x.hostname}
                          </Link>
                          <div className="text-muted">{x.groupName}</div>
                        </td>
                        <td className="px-3 py-1.5 text-muted">
                          {x.deviceName}
                          {x.currentVersion &&
                            ` · ${t("drivers.currentVersion", { version: x.currentVersion })}`}
                        </td>
                        <td className="px-3 py-1.5">
                          <StatusDot online={x.online} />
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </div>
          {d.state !== "ready" && (
            <p className="flex items-start gap-2 text-xs text-muted">
              <TriangleAlert className="mt-0.5 size-3.5 shrink-0" />
              {t("drivers.notDownloadedHint")}
            </p>
          )}
        </div>
      )}
    </Modal>
  );
}

function Item({
  label,
  value,
  mono,
}: {
  label: string;
  value: string;
  mono?: boolean;
}) {
  return (
    <div className="min-w-0">
      <dt className="text-xs text-muted">{label}</dt>
      <dd className={`break-words ${mono ? "font-mono text-xs" : ""}`}>
        {value || "–"}
      </dd>
    </div>
  );
}
