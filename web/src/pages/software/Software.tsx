import { useCallback, useEffect, useMemo, useState } from "react";
import { Link, useSearchParams } from "react-router";
import { useTranslation } from "react-i18next";
import { Package, Pencil, Plus, RefreshCw, Rocket, Search, Trash2 } from "lucide-react";
import { del, get, post, type CatalogStatus, type InstalledDevice, type InstalledSoftware, type PackagesView, type Platform, type SoftwarePackage } from "../../api";
import { useAuth } from "../../auth";
import { Alert, Button, Card, Input, Modal, PageHeader, Select, Spinner, formatDate, useErrorText } from "../../components/ui";
import { PlatformBadge } from "../devices/util";
import { DeployDialog, type DeployTarget } from "./DeployDialog";
import { PackageForm } from "./PackageForm";
import { Browsers } from "./Browsers";
import { SourceBadge, formatSize } from "./util";

type Tab = "library" | "installed" | "browsers";
const tabs: Tab[] = ["library", "installed", "browsers"];

export function Software() {
  const { t } = useTranslation();
  const [params, setParams] = useSearchParams();
  const tab = (tabs.includes(params.get("tab") as Tab) ? params.get("tab") : "library") as Tab;
  return (
    <div className="space-y-6">
      <PageHeader title={t("software.title")} description={t("software.subtitle")} />
      <div className="flex gap-1 border-b border-line" role="tablist">
        {tabs.map((k) => (
          <button
            key={k}
            role="tab"
            aria-selected={tab === k}
            onClick={() => setParams(k === "library" ? {} : { tab: k })}
            className={`-mb-px border-b-2 px-4 py-2 text-sm font-medium ${tab === k ? "border-primary text-fg" : "border-transparent text-muted hover:text-fg"}`}
          >
            {t(`software.tabs.${k}`)}
          </button>
        ))}
      </div>
      {tab === "library" && <Library />}
      {tab === "installed" && <Installed />}
      {tab === "browsers" && <Browsers />}
    </div>
  );
}

function Library() {
  const { t, i18n } = useTranslation();
  const errorText = useErrorText();
  const { can } = useAuth();
  const manage = can("software.manage");
  const deploy = can("software.deploy");
  const [view, setView] = useState<PackagesView | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [filter, setFilter] = useState("");
  const [editing, setEditing] = useState<SoftwarePackage | null | "new">(null);
  const [deleting, setDeleting] = useState<SoftwarePackage | null>(null);
  const [target, setTarget] = useState<DeployTarget | null>(null);
  const [busy, setBusy] = useState(false);

  const load = useCallback(() => {
    get<PackagesView>("/software/packages")
      .then(setView)
      .catch((e) => setError(errorText(e)));
  }, []);
  useEffect(load, [load]);

  const shown = useMemo(() => {
    const f = filter.trim().toLowerCase();
    return (view?.packages ?? []).filter((p) => !f || p.name.toLowerCase().includes(f) || p.publisher.toLowerCase().includes(f) || p.wingetId.toLowerCase().includes(f));
  }, [view, filter]);

  const remove = async () => {
    if (!deleting) return;
    setBusy(true);
    try {
      await del(`/software/packages/${deleting.id}`);
      setDeleting(null);
      load();
    } catch (e) {
      setError(errorText(e));
      setDeleting(null);
    } finally {
      setBusy(false);
    }
  };

  if (error && !view) return <Alert tone="danger">{error}</Alert>;
  if (!view) return <Spinner />;
  return (
    <div className="space-y-6">
      {error && <Alert tone="danger">{error}</Alert>}
      {manage && <CatalogCard status={view.catalog} onRefreshed={load} />}
      <Card
        title={t("software.library", { count: view.packages.length })}
        description={t("software.libraryHint")}
        actions={
          <div className="flex flex-wrap items-center gap-2">
            <div className="relative w-56 max-w-full">
              <Search className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted" />
              <Input value={filter} onChange={(e) => setFilter(e.target.value)} placeholder={t("software.filter")} className="pl-9" aria-label={t("software.filter")} />
            </div>
            {manage && (
              <Button icon={<Plus className="size-4" />} onClick={() => setEditing("new")}>
                {t("software.add")}
              </Button>
            )}
          </div>
        }
      >
        {view.packages.length === 0 ? (
          <div className="flex flex-col items-center gap-3 py-10 text-center">
            <span className="grid size-12 place-items-center rounded-full bg-primary/10 text-primary">
              <Package className="size-6" />
            </span>
            <p className="font-medium">{t("software.emptyTitle")}</p>
            <p className="max-w-md text-sm text-muted">{t("software.emptyText")}</p>
          </div>
        ) : (
          <div className="-mx-5 -my-5 overflow-x-auto">
            <table className="w-full min-w-[760px] text-sm">
              <thead className="border-b border-line text-left text-xs text-muted uppercase">
                <tr>
                  <th className="px-5 py-3 font-medium">{t("software.cols.name")}</th>
                  <th className="px-3 py-3 font-medium">{t("software.cols.source")}</th>
                  <th className="px-3 py-3 font-medium">{t("software.cols.version")}</th>
                  <th className="px-3 py-3 font-medium">{t("software.cols.platform")}</th>
                  <th className="px-3 py-3 text-right font-medium">{t("software.cols.installed")}</th>
                  <th className="px-5 py-3 text-right font-medium">{t("common.actions")}</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-line">
                {shown.map((p) => (
                  <tr key={p.id}>
                    <td className="px-5 py-3">
                      <div className="font-medium">{p.name}</div>
                      <div className="text-xs text-muted">
                        {[p.publisher, p.wingetId || p.aptPackage || p.fileName, p.fileSize ? formatSize(p.fileSize, i18n.language) : ""].filter(Boolean).join(" · ")}
                      </div>
                    </td>
                    <td className="px-3 py-3">
                      <SourceBadge source={p.source} />
                    </td>
                    <td className="px-3 py-3">{p.version ? <span className="font-mono text-xs">{p.version}</span> : <span className="text-muted">{t("software.latest")}</span>}</td>
                    <td className="px-3 py-3">
                      <PlatformBadge platform={p.platform} />
                    </td>
                    <td className="px-3 py-3 text-right tabular-nums">{p.installed}</td>
                    <td className="px-5 py-3">
                      <div className="flex justify-end gap-1">
                        {deploy && (
                          <Button
                            size="sm"
                            icon={<Rocket className="size-4" />}
                            onClick={() =>
                              setTarget({
                                action: "install",
                                packageId: p.id,
                                name: p.name,
                                platform: p.platform,
                              })
                            }
                          >
                            {t("software.deployAction")}
                          </Button>
                        )}
                        {manage && (
                          <>
                            <Button size="sm" variant="ghost" onClick={() => setEditing(p)} aria-label={t("common.edit")} title={t("common.edit")}>
                              <Pencil className="size-4" />
                            </Button>
                            <Button size="sm" variant="ghost" onClick={() => setDeleting(p)} aria-label={t("common.delete")} title={t("common.delete")}>
                              <Trash2 className="size-4" />
                            </Button>
                          </>
                        )}
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Card>
      <p className="text-sm text-muted">
        <Link to="/deployments" className="font-medium text-primary hover:underline">
          {t("software.toDeployments")}
        </Link>
      </p>

      {editing && (
        <PackageForm
          view={view}
          pkg={editing === "new" ? null : editing}
          onClose={() => setEditing(null)}
          onSaved={() => {
            setEditing(null);
            load();
          }}
        />
      )}
      <Modal
        open={!!deleting}
        onClose={() => setDeleting(null)}
        title={t("software.deleteTitle")}
        footer={
          <>
            <Button variant="secondary" onClick={() => setDeleting(null)}>
              {t("common.cancel")}
            </Button>
            <Button variant="danger" loading={busy} onClick={remove}>
              {t("common.delete")}
            </Button>
          </>
        }
      >
        <p className="text-sm">{t("software.deleteText", { name: deleting?.name ?? "" })}</p>
      </Modal>
      <DeployDialog target={target} onClose={() => setTarget(null)} />
    </div>
  );
}

function CatalogCard({ status, onRefreshed }: { status: CatalogStatus; onRefreshed: () => void }) {
  const { t, i18n } = useTranslation();
  const errorText = useErrorText();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const refresh = async () => {
    setBusy(true);
    setError(null);
    try {
      await post("/software/catalog/refresh");
      onRefreshed();
    } catch (e) {
      setError(errorText(e));
    } finally {
      setBusy(false);
    }
  };
  return (
    <Card
      title={t("software.catalog.title")}
      description={
        status.packages
          ? t("software.catalog.loaded", {
              count: status.packages,
              date: formatDate(status.updatedAt, i18n.language),
            })
          : t("software.catalog.notLoaded")
      }
      actions={
        <Button variant="secondary" loading={busy} icon={<RefreshCw className="size-4" />} onClick={refresh}>
          {t("software.catalog.refresh")}
        </Button>
      }
    >
      <div className="space-y-2 text-sm text-muted">
        <p>{t("software.catalog.hint")}</p>
        {(error || status.error) && <Alert tone="warning">{error || status.error}</Alert>}
      </div>
    </Card>
  );
}

function Installed() {
  const { t } = useTranslation();
  const errorText = useErrorText();
  const { can } = useAuth();
  const deploy = can("software.deploy");
  const [search, setSearch] = useState("");
  const [platform, setPlatform] = useState<"" | Platform>("");
  const [list, setList] = useState<InstalledSoftware[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [target, setTarget] = useState<DeployTarget | null>(null);
  const [holders, setHolders] = useState<{
    row: InstalledSoftware;
    devices: InstalledDevice[];
  } | null>(null);

  useEffect(() => {
    const ctl = new AbortController();
    const id = setTimeout(() => {
      const q = new URLSearchParams();
      if (search.trim()) q.set("search", search.trim());
      if (platform) q.set("platform", platform);
      get<InstalledSoftware[]>(`/software/installed?${q}`, ctl.signal)
        .then((l) => {
          setList(l);
          setError(null);
        })
        .catch((e) => {
          if (!ctl.signal.aborted) setError(errorText(e));
        });
    }, 250);
    return () => {
      clearTimeout(id);
      ctl.abort();
    };
  }, [search, platform]);

  const devicesFor = async (row: InstalledSoftware) => {
    const q = new URLSearchParams({ name: row.name, platform: row.platform });
    return get<InstalledDevice[]>(`/software/installed/devices?${q}`);
  };
  const showDevices = async (row: InstalledSoftware) => {
    try {
      setHolders({ row, devices: await devicesFor(row) });
    } catch (e) {
      setError(errorText(e));
    }
  };
  const uninstall = async (row: InstalledSoftware) => {
    try {
      const devices = await devicesFor(row);
      setTarget({
        action: "uninstall",
        name: row.name,
        platform: row.platform,
        versions: row.versions,
        deviceIds: devices.map((d) => d.deviceId),
      });
    } catch (e) {
      setError(errorText(e));
    }
  };

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap gap-2">
        <div className="relative w-72 max-w-full">
          <Search className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted" />
          <Input
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            placeholder={t("software.installedSearch")}
            className="pl-9"
            aria-label={t("software.installedSearch")}
          />
        </div>
        <div className="w-44">
          <Select value={platform} onChange={(e) => setPlatform(e.target.value as "" | Platform)} aria-label={t("software.cols.platform")}>
            <option value="">{t("software.allPlatforms")}</option>
            <option value="windows">Windows</option>
            <option value="linux">Linux</option>
          </Select>
        </div>
      </div>
      {error && <Alert tone="danger">{error}</Alert>}
      <Card title={t("software.installedTitle", { count: list?.length ?? 0 })} description={t("software.installedHint")}>
        {!list ? (
          <Spinner />
        ) : list.length === 0 ? (
          <p className="text-sm text-muted">{t("software.installedEmpty")}</p>
        ) : (
          <div className="-mx-5 -my-5 max-h-[640px] overflow-auto">
            <table className="w-full min-w-[720px] text-sm">
              <thead className="sticky top-0 border-b border-line bg-surface text-left text-xs text-muted uppercase">
                <tr>
                  <th className="px-5 py-3 font-medium">{t("software.cols.name")}</th>
                  <th className="px-3 py-3 font-medium">{t("software.cols.versions")}</th>
                  <th className="px-3 py-3 font-medium">{t("software.cols.platform")}</th>
                  <th className="px-3 py-3 text-right font-medium">{t("software.cols.devices")}</th>
                  <th className="px-5 py-3 text-right font-medium">{t("common.actions")}</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-line">
                {list.map((s) => (
                  <tr key={`${s.platform}-${s.name}`}>
                    <td className="px-5 py-2">
                      <div className="font-medium">{s.name}</div>
                      {s.publisher && <div className="text-xs text-muted">{s.publisher}</div>}
                    </td>
                    <td className="max-w-[240px] truncate px-3 py-2 font-mono text-xs" title={s.versions.join(", ")}>
                      {s.versions.slice(0, 3).join(", ")}
                      {s.versions.length > 3 && ` +${s.versions.length - 3}`}
                    </td>
                    <td className="px-3 py-2">
                      <PlatformBadge platform={s.platform} />
                    </td>
                    <td className="px-3 py-2 text-right">
                      <button className="tabular-nums text-primary hover:underline" onClick={() => showDevices(s)}>
                        {s.devices}
                      </button>
                    </td>
                    <td className="px-5 py-2 text-right">
                      {deploy && (
                        <Button size="sm" variant="ghost" icon={<Trash2 className="size-4" />} onClick={() => uninstall(s)}>
                          {t("software.uninstall")}
                        </Button>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Card>
      <Modal open={!!holders} onClose={() => setHolders(null)} title={t("software.holdersTitle", { name: holders?.row.name ?? "" })}>
        <ul className="max-h-96 divide-y divide-line overflow-auto text-sm">
          {holders?.devices.map((d) => (
            <li key={`${d.deviceId}-${d.version}`} className="flex items-center justify-between gap-3 py-2">
              <Link to={`/devices/${d.deviceId}`} className="hover:text-primary">
                {d.hostname}
              </Link>
              <span className="font-mono text-xs text-muted">{d.version}</span>
            </li>
          ))}
        </ul>
      </Modal>
      <DeployDialog target={target} onClose={() => setTarget(null)} />
    </div>
  );
}
