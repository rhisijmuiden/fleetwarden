import { useCallback, useEffect, useMemo, useState } from "react";
import { Link, useSearchParams } from "react-router";
import { useTranslation } from "react-i18next";
import { FolderInput, MonitorSmartphone, Plus, RefreshCw, Search } from "lucide-react";
import { get, post, type DeviceList } from "../../api";
import { useAuth } from "../../auth";
import { Alert, Button, Card, Input, Modal, PageHeader, Select, Spinner, useErrorText } from "../../components/ui";
import { useGroups } from "../../components/GroupScope";
import { AddClient } from "./AddClient";
import { KindIcon, PlatformBadge, StatusDot, formatBytes, timeAgo } from "./util";

const pageSize = 100;

export function Devices() {
  const { t, i18n } = useTranslation();
  const { can } = useAuth();
  const errorText = useErrorText();
  const [params, setParams] = useSearchParams();
  const [data, setData] = useState<DeviceList | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [moving, setMoving] = useState(false);
  const [adding, setAdding] = useState(false);
  const [reload, setReload] = useState(0);
  const groups = useGroups(reload);
  const manage = can("devices.manage");

  const search = params.get("search") ?? "";
  const group = params.get("group") ?? "";
  const platform = params.get("platform") ?? "";
  const status = params.get("status") ?? "";
  const software = params.get("software") ?? "";
  const page = Number(params.get("page") ?? "0");
  const [searchInput, setSearchInput] = useState(search);

  const setParam = (k: string, v: string) => {
    const next = new URLSearchParams(params);
    if (v) next.set(k, v);
    else next.delete(k);
    if (k !== "page") next.delete("page");
    setParams(next, { replace: true });
  };

  // Debounce the search box into the URL.
  useEffect(() => {
    const id = setTimeout(() => searchInput !== search && setParam("search", searchInput.trim()), 300);
    return () => clearTimeout(id);
  }, [searchInput]);

  const query = useMemo(() => {
    const q = new URLSearchParams({ limit: String(pageSize), offset: String(page * pageSize) });
    if (search) q.set("search", search);
    if (group) q.set("group", group);
    if (platform) q.set("platform", platform);
    if (status) q.set("status", status);
    if (software) q.set("software", software);
    return q.toString();
  }, [search, group, platform, status, software, page]);

  const load = useCallback(() => {
    get<DeviceList>(`/devices?${query}`)
      .then((d) => {
        setData(d);
        setError(null);
      })
      .catch((e) => setError(errorText(e)));
  }, [query]);
  useEffect(load, [load, reload]);
  // Online status changes all the time; refresh quietly.
  useEffect(() => {
    const id = setInterval(load, 15000);
    return () => clearInterval(id);
  }, [load]);

  const items = data?.items ?? [];
  const allSelected = items.length > 0 && items.every((d) => selected.has(d.id));
  const toggleAll = () => setSelected(allSelected ? new Set() : new Set(items.map((d) => d.id)));
  const toggle = (id: string) => {
    const next = new Set(selected);
    if (next.has(id)) next.delete(id);
    else next.add(id);
    setSelected(next);
  };
  const filtersActive = !!(search || group || platform || status || software);

  return (
    <div className="space-y-6">
      <PageHeader
        title={t("devices.title")}
        description={t("devices.subtitle")}
        actions={
          <>
            <Button variant="secondary" icon={<RefreshCw className="size-4" />} onClick={() => setReload((r) => r + 1)}>
              {t("common.refresh")}
            </Button>
            {manage && (
              <Button icon={<Plus className="size-4" />} onClick={() => setAdding(true)}>
                {t("addClient.button")}
              </Button>
            )}
          </>
        }
      />
      {error && <Alert tone="danger">{error}</Alert>}

      <div className="flex flex-wrap items-center gap-2">
        <div className="relative min-w-[220px] flex-1">
          <Search className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted" />
          <Input
            value={searchInput}
            onChange={(e) => setSearchInput(e.target.value)}
            placeholder={t("devices.searchPlaceholder")}
            className="pl-9"
            aria-label={t("devices.search")}
          />
        </div>
        <div className="w-full sm:w-44">
          <Select value={group} onChange={(e) => setParam("group", e.target.value)} aria-label={t("devices.group")}>
            <option value="">{t("devices.allGroups")}</option>
            {(groups ?? []).map((g) => (
              <option key={g.id} value={g.id}>
                {g.name}
              </option>
            ))}
          </Select>
        </div>
        <div className="w-[calc(50%-4px)] sm:w-36">
          <Select value={platform} onChange={(e) => setParam("platform", e.target.value)} aria-label={t("devices.platform")}>
            <option value="">{t("devices.allPlatforms")}</option>
            <option value="windows">Windows</option>
            <option value="linux">Linux</option>
          </Select>
        </div>
        <div className="w-[calc(50%-4px)] sm:w-36">
          <Select value={status} onChange={(e) => setParam("status", e.target.value)} aria-label={t("devices.status")}>
            <option value="">{t("devices.anyStatus")}</option>
            <option value="online">{t("devices.online")}</option>
            <option value="offline">{t("devices.offline")}</option>
          </Select>
        </div>
        {software && (
          <Button variant="secondary" size="sm" onClick={() => setParam("software", "")}>
            {t("devices.withSoftware", { name: software })} ✕
          </Button>
        )}
      </div>

      {manage && selected.size > 0 && (
        <div className="flex flex-wrap items-center gap-3 rounded-md border border-primary/30 bg-primary/5 px-4 py-2.5 text-sm">
          <span className="font-medium">{t("devices.selected", { count: selected.size })}</span>
          <Button size="sm" variant="secondary" icon={<FolderInput className="size-4" />} onClick={() => setMoving(true)}>
            {t("devices.moveTo")}
          </Button>
          <Button size="sm" variant="ghost" onClick={() => setSelected(new Set())}>
            {t("devices.clearSelection")}
          </Button>
        </div>
      )}

      <Card>
        {!data ? (
          <Spinner />
        ) : items.length === 0 ? (
          <div className="grid place-items-center py-12 text-center">
            <MonitorSmartphone className="size-10 text-muted" strokeWidth={1.5} />
            <p className="mt-3 font-medium">{filtersActive ? t("devices.noMatch") : t("devices.empty")}</p>
            {!filtersActive && <p className="mt-1 max-w-sm text-sm text-muted">{t("devices.emptyHint")}</p>}
            {!filtersActive && manage && (
              <Button className="mt-4" icon={<Plus className="size-4" />} onClick={() => setAdding(true)}>
                {t("addClient.button")}
              </Button>
            )}
          </div>
        ) : (
          <div className="-mx-5 -my-5 overflow-x-auto">
            <table className="w-full min-w-[900px] text-sm">
              <thead className="border-b border-line text-left text-xs text-muted uppercase">
                <tr>
                  {manage && (
                    <th className="w-10 py-3 pl-5">
                      <input type="checkbox" className="size-4 accent-primary" checked={allSelected} onChange={toggleAll} aria-label={t("devices.selectAll")} />
                    </th>
                  )}
                  <th className={`${manage ? "px-3" : "px-5"} py-3 font-medium`}>{t("devices.device")}</th>
                  <th className="px-3 py-3 font-medium">{t("devices.status")}</th>
                  <th className="px-3 py-3 font-medium">{t("devices.os")}</th>
                  <th className="px-3 py-3 font-medium">{t("devices.group")}</th>
                  <th className="px-3 py-3 font-medium">{t("devices.user")}</th>
                  <th className="px-3 py-3 font-medium">{t("devices.hardware")}</th>
                  <th className="px-5 py-3 font-medium">{t("devices.lastSeen")}</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-line">
                {items.map((d) => (
                  <tr key={d.id} className="hover:bg-surface-muted/50">
                    {manage && (
                      <td className="py-3 pl-5">
                        <input
                          type="checkbox"
                          className="size-4 accent-primary"
                          checked={selected.has(d.id)}
                          onChange={() => toggle(d.id)}
                          aria-label={d.hostname}
                        />
                      </td>
                    )}
                    <td className={`${manage ? "px-3" : "px-5"} py-3`}>
                      <Link to={`/devices/${d.id}`} className="group flex items-center gap-2.5">
                        <span className="grid size-8 shrink-0 place-items-center rounded-md bg-surface-muted text-muted group-hover:text-primary">
                          <KindIcon device={d} />
                        </span>
                        <span className="min-w-0">
                          <span className="block truncate font-medium group-hover:text-primary">{d.hostname}</span>
                          <span className="block truncate text-xs text-muted">{d.localIps?.[0] ?? d.remoteIp}</span>
                        </span>
                      </Link>
                    </td>
                    <td className="px-3 py-3">
                      <StatusDot online={d.online} />
                    </td>
                    <td className="px-3 py-3">
                      <div className="flex items-center gap-2">
                        <PlatformBadge platform={d.platform} />
                        <span className="truncate">{d.osName || "–"}</span>
                      </div>
                      {d.domainJoined && <div className="mt-0.5 truncate text-xs text-muted">{d.domain}</div>}
                    </td>
                    <td className="px-3 py-3">{d.groupName}</td>
                    <td className="max-w-[160px] truncate px-3 py-3 text-muted">{d.loggedInUser || "–"}</td>
                    <td className="px-3 py-3 text-xs text-muted">
                      <div className="truncate">{[d.manufacturer, d.model].filter(Boolean).join(" ") || "–"}</div>
                      {d.memoryBytes > 0 && <div>{formatBytes(d.memoryBytes, i18n.language)} RAM</div>}
                    </td>
                    <td className="px-5 py-3 whitespace-nowrap text-muted">{d.online ? t("devices.now") : timeAgo(d.lastSeenAt, i18n.language)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Card>
      {data && data.total > pageSize && (
        <div className="flex items-center justify-between text-sm text-muted">
          <span>{t("devices.pageInfo", { from: page * pageSize + 1, to: Math.min(data.total, (page + 1) * pageSize), total: data.total })}</span>
          <div className="flex gap-2">
            <Button variant="secondary" size="sm" disabled={page === 0} onClick={() => setParam("page", String(page - 1))}>
              {t("devices.previous")}
            </Button>
            <Button variant="secondary" size="sm" disabled={(page + 1) * pageSize >= data.total} onClick={() => setParam("page", String(page + 1))}>
              {t("devices.next")}
            </Button>
          </div>
        </div>
      )}

      <MoveDialog
        open={moving}
        ids={[...selected]}
        onClose={() => setMoving(false)}
        onDone={() => {
          setMoving(false);
          setSelected(new Set());
          setReload((r) => r + 1);
        }}
      />
      <AddClient
        open={adding}
        onClose={() => {
          setAdding(false);
          setReload((r) => r + 1);
        }}
      />
    </div>
  );
}

export function MoveDialog({ open, ids, onClose, onDone }: { open: boolean; ids: string[]; onClose: () => void; onDone: () => void }) {
  const { t } = useTranslation();
  const errorText = useErrorText();
  const groups = useGroups(open ? 1 : 0);
  const [target, setTarget] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => {
    setError(null);
    setTarget("");
  }, [open]);
  return (
    <Modal
      open={open}
      onClose={onClose}
      title={t("devices.moveTitle", { count: ids.length })}
      footer={
        <>
          <Button variant="secondary" onClick={onClose}>
            {t("common.cancel")}
          </Button>
          <Button
            loading={busy}
            disabled={!target}
            onClick={() => {
              setBusy(true);
              post("/devices/move", { ids, groupId: Number(target) })
                .then(onDone)
                .catch((e) => setError(errorText(e)))
                .finally(() => setBusy(false));
            }}
          >
            {t("devices.move")}
          </Button>
        </>
      }
    >
      {error && <Alert tone="danger">{error}</Alert>}
      <Select value={target} onChange={(e) => setTarget(e.target.value)} aria-label={t("devices.group")}>
        <option value="">{t("devices.chooseGroup")}</option>
        {(groups ?? []).map((g) => (
          <option key={g.id} value={g.id}>
            {g.name}
          </option>
        ))}
      </Select>
    </Modal>
  );
}
