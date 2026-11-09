import { useCallback, useEffect, useMemo, useState } from "react";
import { Link, useNavigate, useParams } from "react-router";
import { useTranslation } from "react-i18next";
import { ArrowLeft, Layers, Plus, Search, Trash2 } from "lucide-react";
import { del, get, post, put, type ConfigList, type ConfigProfile } from "../../api";
import { useAuth } from "../../auth";
import { useGroups } from "../../components/GroupScope";
import { Alert, Badge, Button, Card, Field, Input, Modal, PageHeader, Spinner, Switch, formatDate, useErrorText } from "../../components/ui";
import { PlatformBadge } from "../devices/util";
import { ConfigTabs, TypeBadge } from "./util";

// Configuration profiles bundle configurations and assign them to device
// groups: a device in a group gets every configuration of the group's
// profiles.

export function ConfigProfiles() {
  const { t, i18n } = useTranslation();
  const errorText = useErrorText();
  const { can } = useAuth();
  const manage = can("config.manage");
  const groups = useGroups();
  const [profiles, setProfiles] = useState<ConfigProfile[] | null>(null);
  const [configs, setConfigs] = useState<ConfigList | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [filter, setFilter] = useState("");
  const load = useCallback(() => {
    get<ConfigProfile[]>("/config-profiles")
      .then(setProfiles)
      .catch((e) => setError(errorText(e)));
    get<ConfigList>("/configurations")
      .then(setConfigs)
      .catch(() => {});
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);
  useEffect(load, [load]);
  const groupName = (id: number) => groups?.find((g) => g.id === id)?.name ?? `#${id}`;
  const items = useMemo(() => {
    const f = filter.trim().toLowerCase();
    return (profiles ?? []).filter((p) => !f || p.name.toLowerCase().includes(f) || p.description.toLowerCase().includes(f));
  }, [profiles, filter]);

  return (
    <div className="space-y-6">
      <PageHeader
        title={t("configs.title")}
        description={t("configs.profiles.subtitle")}
        actions={
          manage && (
            <Link to="/configurations/profiles/new">
              <Button icon={<Plus className="size-4" />}>{t("configs.profiles.new")}</Button>
            </Link>
          )
        }
      />
      <ConfigTabs />
      {error && <Alert tone="danger">{error}</Alert>}
      <Card>
        {!profiles ? (
          <Spinner />
        ) : profiles.length === 0 ? (
          <div className="flex flex-col items-center gap-3 py-10 text-center">
            <span className="grid size-12 place-items-center rounded-full bg-primary/10 text-primary">
              <Layers className="size-6" />
            </span>
            <p className="font-medium">{t("configs.profiles.emptyTitle")}</p>
            <p className="max-w-md text-sm text-muted">{t("configs.profiles.emptyText")}</p>
            {manage && (
              <Link to="/configurations/profiles/new">
                <Button icon={<Plus className="size-4" />}>{t("configs.profiles.new")}</Button>
              </Link>
            )}
          </div>
        ) : (
          <div className="space-y-4">
            <div className="relative max-w-sm">
              <Search className="pointer-events-none absolute top-2.5 left-3 size-4 text-muted" />
              <Input className="pl-9" placeholder={t("configs.profiles.search")} value={filter} onChange={(e) => setFilter(e.target.value)} aria-label={t("configs.profiles.search")} />
            </div>
            <div className="-mx-5 -mb-5 overflow-x-auto">
              <table className="w-full min-w-[820px] text-sm">
                <thead className="border-y border-line text-left text-xs text-muted uppercase">
                  <tr>
                    <th className="px-5 py-3 font-medium">{t("configs.profiles.colName")}</th>
                    <th className="px-3 py-3 font-medium">{t("configs.profiles.items")}</th>
                    <th className="px-3 py-3 font-medium">{t("configs.cols.groups")}</th>
                    <th className="px-3 py-3 font-medium">{t("configs.profiles.devicesCol")}</th>
                    <th className="px-5 py-3 font-medium">{t("configs.cols.changed")}</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-line">
                  {items.map((p) => {
                    const members = (configs?.items ?? []).filter((c) => p.configIds.includes(c.id));
                    return (
                      <tr key={p.id} className="hover:bg-surface-muted/50">
                        <td className="px-5 py-3">
                          <Link to={`/configurations/profiles/${p.id}`} className="font-medium hover:text-primary">
                            {p.name}
                          </Link>
                          {!p.enabled && (
                            <span className="ml-2">
                              <Badge>{t("configs.disabled")}</Badge>
                            </span>
                          )}
                          {p.description && <p className="mt-0.5 max-w-sm truncate text-xs text-muted">{p.description}</p>}
                        </td>
                        <td className="px-3 py-3">
                          {members.length === 0 ? (
                            <span className="text-xs text-muted">{t("configs.profiles.noItems")}</span>
                          ) : (
                            <div className="flex max-w-[320px] flex-wrap gap-1.5">
                              {members.map((c) => (
                                <Link key={c.id} to={`/configurations/${c.id}`} title={c.name} className="hover:opacity-80">
                                  <TypeBadge type={c.type} label={c.name} />
                                </Link>
                              ))}
                            </div>
                          )}
                        </td>
                        <td className="max-w-[220px] px-3 py-3 text-muted">
                          <div className="truncate">{p.groupIds.length ? p.groupIds.map(groupName).join(", ") : t("configs.noGroups")}</div>
                        </td>
                        <td className="px-3 py-3 text-muted tabular-nums">{p.devices}</td>
                        <td className="px-5 py-3 whitespace-nowrap text-muted">
                          <div>{formatDate(p.updatedAt, i18n.language)}</div>
                          <div className="text-xs">{p.updatedBy || p.createdBy}</div>
                        </td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>
          </div>
        )}
      </Card>
    </div>
  );
}

function CheckRow({ checked, onChange, disabled, children }: { checked: boolean; onChange: (v: boolean) => void; disabled?: boolean; children: React.ReactNode }) {
  return (
    <label className={`flex items-center gap-3 rounded-md px-2 py-2 text-sm ${disabled ? "" : "cursor-pointer hover:bg-surface-muted"}`}>
      <input type="checkbox" className="size-4 accent-primary" checked={checked} disabled={disabled} onChange={(e) => onChange(e.target.checked)} />
      {children}
    </label>
  );
}

export function ProfileEditor() {
  const { id } = useParams();
  const { t } = useTranslation();
  const navigate = useNavigate();
  const errorText = useErrorText();
  const { can } = useAuth();
  const manage = can("config.manage");
  const groups = useGroups();
  const [p, setP] = useState<ConfigProfile | null>(null);
  const [configs, setConfigs] = useState<ConfigList | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [confirm, setConfirm] = useState(false);
  const [filter, setFilter] = useState("");

  useEffect(() => {
    get<ConfigList>("/configurations")
      .then(setConfigs)
      .catch((e) => setError(errorText(e)));
    if (id) {
      get<ConfigProfile>(`/config-profiles/${id}`)
        .then(setP)
        .catch((e) => setError(errorText(e)));
    } else {
      setP({ id: 0, name: "", description: "", enabled: true, groupIds: [], configIds: [], createdBy: "", updatedBy: "", createdAt: "", updatedAt: "", devices: 0 });
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [id]);

  const shown = useMemo(() => {
    const f = filter.trim().toLowerCase();
    return (configs?.items ?? []).filter((c) => !f || c.name.toLowerCase().includes(f));
  }, [configs, filter]);

  if (error && !p) return <Alert tone="danger">{error}</Alert>;
  if (!p || !configs) return <Spinner />;
  const set = (patch: Partial<ConfigProfile>) => setP({ ...p, ...patch });
  const toggle = (list: number[], v: number, on: boolean) => (on ? [...list, v] : list.filter((x) => x !== v));

  const save = async () => {
    setBusy(true);
    setError(null);
    const body = { name: p.name, description: p.description, enabled: p.enabled, groupIds: p.groupIds, configIds: p.configIds };
    try {
      if (id) await put<ConfigProfile>(`/config-profiles/${id}`, body);
      else await post<ConfigProfile>("/config-profiles", body);
      navigate("/configurations/profiles");
    } catch (e) {
      setError(errorText(e));
      window.scrollTo({ top: 0, behavior: "smooth" });
    } finally {
      setBusy(false);
    }
  };
  const remove = async () => {
    setBusy(true);
    try {
      await del(`/config-profiles/${id}`);
      navigate("/configurations/profiles");
    } catch (e) {
      setError(errorText(e));
      setBusy(false);
    }
  };

  return (
    <div className="space-y-6">
      <Link to="/configurations/profiles" className="inline-flex items-center gap-1.5 text-sm text-muted hover:text-fg">
        <ArrowLeft className="size-4" /> {t("configs.tabs.profiles")}
      </Link>
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h1 className="flex items-center gap-2 text-2xl font-semibold tracking-tight">
          <Layers className="size-6 text-primary" /> {id ? p.name : t("configs.profiles.new")}
        </h1>
        {id && manage && (
          <Button variant="ghost" icon={<Trash2 className="size-4" />} onClick={() => setConfirm(true)}>
            {t("common.delete")}
          </Button>
        )}
      </div>
      {error && <Alert tone="danger">{error}</Alert>}
      <div className="grid gap-6 xl:grid-cols-[1fr_360px]">
        <Card title={t("configs.profiles.items")} description={t("configs.profiles.itemsHint")}>
          {configs.items.length === 0 ? (
            <p className="text-sm text-muted">{t("configs.profiles.noConfigs")}</p>
          ) : (
            <div className="space-y-3">
              {configs.items.length > 8 && (
                <div className="relative max-w-sm">
                  <Search className="pointer-events-none absolute top-2.5 left-3 size-4 text-muted" />
                  <Input className="pl-9" placeholder={t("configs.search")} value={filter} onChange={(e) => setFilter(e.target.value)} aria-label={t("configs.search")} />
                </div>
              )}
              <div className="grid gap-0.5">
                {shown.map((c) => (
                  <CheckRow key={c.id} checked={p.configIds.includes(c.id)} disabled={!manage} onChange={(on) => set({ configIds: toggle(p.configIds, c.id, on) })}>
                    <span className="min-w-0 flex-1">
                      <span className="block truncate font-medium">{c.name}</span>
                      {c.description && <span className="block truncate text-xs text-muted">{c.description}</span>}
                    </span>
                    <TypeBadge type={c.type} />
                    <PlatformBadge platform={c.platform} />
                    {!c.enabled && <Badge>{t("configs.disabled")}</Badge>}
                  </CheckRow>
                ))}
              </div>
            </div>
          )}
          {manage && (
            <Link to="/configurations" className="mt-3 inline-block text-sm text-primary hover:underline">
              {t("configs.profiles.makeConfig")}
            </Link>
          )}
        </Card>
        <div className="space-y-6">
          <Card title={t("configs.general")}>
            <div className="space-y-4">
              <Field label={t("configs.name")}>{(fid) => <Input id={fid} value={p.name} disabled={!manage} onChange={(e) => set({ name: e.target.value })} maxLength={200} />}</Field>
              <Field label={t("configs.description")}>
                {(fid) => <Input id={fid} value={p.description} disabled={!manage} onChange={(e) => set({ description: e.target.value })} maxLength={1000} />}
              </Field>
              <Switch checked={p.enabled} onChange={(enabled) => manage && set({ enabled })} label={t("configs.enabled")} description={t("configs.profiles.enabledHint")} />
            </div>
          </Card>
          <Card title={t("configs.assign")} description={t("configs.profiles.assignHint")}>
            <div className="grid max-h-64 gap-0.5 overflow-y-auto">
              {(groups ?? []).map((g) => (
                <CheckRow key={g.id} checked={p.groupIds.includes(g.id)} disabled={!manage} onChange={(on) => set({ groupIds: toggle(p.groupIds, g.id, on) })}>
                  <span className="flex-1 truncate">{g.name}</span>
                  <span className="text-xs text-muted tabular-nums">{g.deviceCount}</span>
                </CheckRow>
              ))}
            </div>
            {p.groupIds.length === 0 && <p className="mt-2 text-xs text-warning">{t("configs.profiles.noGroupsHint")}</p>}
          </Card>
          {manage && (
            <div className="flex justify-end gap-2">
              <Button variant="secondary" onClick={() => navigate("/configurations/profiles")}>
                {t("common.cancel")}
              </Button>
              <Button loading={busy} onClick={save}>
                {t("common.save")}
              </Button>
            </div>
          )}
        </div>
      </div>
      <Modal
        open={confirm}
        onClose={() => setConfirm(false)}
        title={t("configs.profiles.deleteTitle")}
        footer={
          <>
            <Button variant="secondary" onClick={() => setConfirm(false)}>
              {t("common.cancel")}
            </Button>
            <Button variant="danger" loading={busy} onClick={remove}>
              {t("common.delete")}
            </Button>
          </>
        }
      >
        <p className="text-sm">{t("configs.profiles.deleteText", { name: p.name })}</p>
      </Modal>
    </div>
  );
}
