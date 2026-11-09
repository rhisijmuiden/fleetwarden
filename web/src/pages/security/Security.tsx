import { useCallback, useEffect, useState, type ReactNode } from "react";
import { Link, useSearchParams } from "react-router";
import { useTranslation } from "react-i18next";
import { Bug, BrickWall, Eye, KeyRound, Lock, Search, ShieldAlert, ShieldCheck, ShieldQuestion } from "lucide-react";
import { get, type RecoveryKey, type SecurityFilter, type SecurityRow, type SecurityStats } from "../../api";
import { useAuth } from "../../auth";
import { useGroups } from "../../components/GroupScope";
import { Alert, Badge, Card, Input, PageHeader, Select, Spinner, formatDate, useErrorText } from "../../components/ui";
import { PlatformBadge, timeAgo } from "../devices/util";
import { AntivirusBadge, EncryptionBadge, FirewallBadge, RevealDialog, RotateDialog, YesNo } from "./util";

type Tab = "overview" | "keys";

export function Security() {
  const { t } = useTranslation();
  const { can } = useAuth();
  const [params, setParams] = useSearchParams();
  const tab: Tab = params.get("tab") === "keys" && can("security.keys") ? "keys" : "overview";
  return (
    <div className="space-y-6">
      <PageHeader title={t("security.title")} description={t("security.subtitle")} />
      {can("security.keys") && (
        <div className="flex gap-1 border-b border-line" role="tablist">
          {(["overview", "keys"] as Tab[]).map((k) => (
            <button
              key={k}
              role="tab"
              aria-selected={tab === k}
              onClick={() => setParams(k === "overview" ? {} : { tab: k })}
              className={`-mb-px border-b-2 px-4 py-2 text-sm font-medium ${tab === k ? "border-primary text-fg" : "border-transparent text-muted hover:text-fg"}`}
            >
              {t(`security.tabs.${k}`)}
            </button>
          ))}
        </div>
      )}
      {tab === "keys" ? <RecoveryKeys /> : <Overview />}
    </div>
  );
}

function Tile({
  label,
  value,
  sub,
  tone,
  icon,
  active,
  onClick,
}: {
  label: string;
  value: ReactNode;
  sub?: string;
  tone: "success" | "warning" | "danger" | "neutral";
  icon: ReactNode;
  active: boolean;
  onClick: () => void;
}) {
  const tones = { success: "text-success", warning: "text-warning", danger: "text-danger", neutral: "text-fg" };
  return (
    <button
      type="button"
      onClick={onClick}
      aria-pressed={active}
      className={`rounded-lg border bg-surface p-4 text-left transition-colors hover:border-primary/50 ${active ? "border-primary ring-1 ring-primary" : "border-line"}`}
    >
      <div className="flex items-center gap-2 text-xs font-medium text-muted uppercase">
        {icon}
        {label}
      </div>
      <div className={`mt-2 text-2xl font-semibold tabular-nums ${tones[tone]}`}>{value}</div>
      {sub && <div className="mt-0.5 text-xs text-muted">{sub}</div>}
    </button>
  );
}

function Overview() {
  const { t, i18n } = useTranslation();
  const errorText = useErrorText();
  const groups = useGroups();
  const [params, setParams] = useSearchParams();
  const filter = (params.get("filter") ?? "") as SecurityFilter;
  const [search, setSearch] = useState("");
  const [group, setGroup] = useState("");
  const [stats, setStats] = useState<SecurityStats | null>(null);
  const [rows, setRows] = useState<SecurityRow[] | null>(null);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(() => {
    get<SecurityStats>("/security/summary")
      .then(setStats)
      .catch((e) => setError(errorText(e)));
    const q = new URLSearchParams();
    if (filter) q.set("filter", filter);
    if (search.trim()) q.set("search", search.trim());
    if (group) q.set("group", group);
    get<SecurityRow[]>(`/security/devices?${q}`)
      .then((r) => {
        setRows(r);
        setError(null);
      })
      .catch((e) => setError(errorText(e)));
  }, [filter, search, group]);
  useEffect(() => {
    const id = setTimeout(load, 200);
    const iv = setInterval(load, 30000);
    return () => {
      clearTimeout(id);
      clearInterval(iv);
    };
  }, [load]);

  const setFilter = (f: SecurityFilter) => setParams(f === filter || !f ? {} : { filter: f });
  const s = stats;
  const unencrypted = s ? (s.encryption.none ?? 0) + (s.encryption.partial ?? 0) + (s.encryption.suspended ?? 0) : 0;
  const avBad = s ? (s.antivirus.outdated ?? 0) + (s.antivirus.disabled ?? 0) : 0;
  const fwBad = s ? (s.firewall.off ?? 0) + (s.firewall.partial ?? 0) : 0;
  const unreported = s ? s.devices - s.reported : 0;

  return (
    <div className="space-y-6">
      {error && <Alert tone="danger">{error}</Alert>}
      {s && (
        <div className="grid grid-cols-2 gap-3 md:grid-cols-3 xl:grid-cols-6">
          <Tile
            label={t("security.tiles.encrypted")}
            value={`${s.encryption.encrypted ?? 0}/${s.reported}`}
            sub={t("security.tiles.encryptedSub", { count: unencrypted })}
            tone={unencrypted ? "warning" : "success"}
            icon={<Lock className="size-3.5" />}
            active={filter === "unencrypted"}
            onClick={() => setFilter("unencrypted")}
          />
          <Tile
            label={t("security.tiles.antivirus")}
            value={`${s.antivirus.ok ?? 0}/${s.reported}`}
            sub={t("security.tiles.antivirusSub", { count: avBad })}
            tone={avBad ? "danger" : "success"}
            icon={<ShieldCheck className="size-3.5" />}
            active={filter === "antivirus"}
            onClick={() => setFilter("antivirus")}
          />
          <Tile
            label={t("security.tiles.firewall")}
            value={`${s.firewall.on ?? 0}/${s.reported}`}
            sub={t("security.tiles.firewallSub", { count: fwBad })}
            tone={fwBad ? "warning" : "success"}
            icon={<BrickWall className="size-3.5" />}
            active={filter === "firewall"}
            onClick={() => setFilter("firewall")}
          />
          <Tile
            label={t("security.tiles.threats")}
            value={s.threats}
            sub={t("security.tiles.threatsSub")}
            tone={s.threats ? "danger" : "success"}
            icon={<Bug className="size-3.5" />}
            active={filter === "threats"}
            onClick={() => setFilter("threats")}
          />
          <Tile
            label={t("security.tiles.secureBoot")}
            value={s.secureBootOff}
            sub={t("security.tiles.secureBootSub")}
            tone={s.secureBootOff ? "warning" : "success"}
            icon={<ShieldAlert className="size-3.5" />}
            active={filter === "secureboot"}
            onClick={() => setFilter("secureboot")}
          />
          <Tile
            label={t("security.tiles.unreported")}
            value={unreported}
            sub={t("security.tiles.unreportedSub")}
            tone="neutral"
            icon={<ShieldQuestion className="size-3.5" />}
            active={filter === "unreported"}
            onClick={() => setFilter("unreported")}
          />
        </div>
      )}

      <Card
        title={filter ? t(`security.filters.${filter}`) : t("security.allDevices")}
        description={filter ? undefined : t("security.allDevicesHint")}
        actions={
          <div className="flex flex-wrap gap-2">
            <div className="relative">
              <Search className="pointer-events-none absolute top-2.5 left-2.5 size-4 text-muted" />
              <Input value={search} onChange={(e) => setSearch(e.target.value)} placeholder={t("security.search")} className="w-56 pl-8" aria-label={t("security.search")} />
            </div>
<div className="w-44">
            <Select value={group} onChange={(e) => setGroup(e.target.value)} aria-label={t("devices.group")}>
              <option value="">{t("reports.allGroups")}</option>
              {groups?.map((g) => (
                <option key={g.id} value={g.id}>
                  {g.name}
                </option>
              ))}
            </Select>
            </div>
          </div>
        }
      >
        {!rows ? (
          <Spinner />
        ) : rows.length === 0 ? (
          <p className="py-6 text-center text-sm text-muted">{filter ? t("security.noneMatch") : t("security.noDevices")}</p>
        ) : (
          <div className="-mx-5 -my-5 overflow-x-auto">
            <table className="w-full min-w-[860px] text-sm">
              <thead className="border-b border-line text-left text-xs text-muted">
                <tr>
                  <th className="px-5 py-2.5 font-medium">{t("devices.device")}</th>
                  <th className="px-3 py-2.5 font-medium">{t("security.col.encryption")}</th>
                  <th className="px-3 py-2.5 font-medium">{t("security.col.antivirus")}</th>
                  <th className="px-3 py-2.5 font-medium">{t("security.col.firewall")}</th>
                  <th className="px-3 py-2.5 font-medium">{t("security.col.threats")}</th>
                  <th className="px-3 py-2.5 font-medium">{t("security.col.secureBoot")}</th>
                  <th className="px-3 py-2.5 font-medium">{t("security.col.admins")}</th>
                  <th className="px-5 py-2.5 font-medium">{t("security.col.reported")}</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-line">
                {rows.map((r) => (
                  <tr key={r.deviceId} className="hover:bg-surface-muted/50">
                    <td className="px-5 py-2.5">
                      <Link to={`/devices/${r.deviceId}?tab=security`} className="font-medium hover:text-primary">
                        {r.hostname}
                      </Link>
                      <div className="mt-0.5 flex items-center gap-2 text-xs text-muted">
                        <PlatformBadge platform={r.platform} />
                        <span>{r.groupName}</span>
                      </div>
                    </td>
                    <td className="px-3 py-2.5">
                      <EncryptionBadge state={r.encryption} />
                      {r.keys > 0 && (
                        <span className="ml-1.5 inline-flex items-center gap-0.5 text-xs text-muted" title={t("security.keys.stored", { count: r.keys })}>
                          <KeyRound className="size-3" />
                          {r.keys}
                        </span>
                      )}
                    </td>
                    <td className="px-3 py-2.5">
                      <AntivirusBadge state={r.antivirus} platform={r.platform} />
                    </td>
                    <td className="px-3 py-2.5">
                      <FirewallBadge state={r.firewall} />
                    </td>
                    <td className="px-3 py-2.5">{r.collectedAt ? r.threats > 0 ? <Badge tone="danger">{r.threats}</Badge> : <span className="text-muted">0</span> : "–"}</td>
                    <td className="px-3 py-2.5">
                      <YesNo value={r.collectedAt ? r.secureBoot : null} />
                    </td>
                    <td className="px-3 py-2.5 tabular-nums">{r.collectedAt ? r.admins : "–"}</td>
                    <td className="px-5 py-2.5 text-xs text-muted" title={formatDate(r.collectedAt, i18n.language)}>
                      {r.collectedAt ? timeAgo(r.collectedAt, i18n.language) : t("security.notReported")}
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

function RecoveryKeys() {
  const { t, i18n } = useTranslation();
  const errorText = useErrorText();
  const { can } = useAuth();
  const [search, setSearch] = useState("");
  const [list, setList] = useState<RecoveryKey[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [open, setOpen] = useState<RecoveryKey | null>(null);
  const [rotate, setRotate] = useState<RecoveryKey | null>(null);

  const load = useCallback(() => {
    get<RecoveryKey[]>(`/security/keys?search=${encodeURIComponent(search.trim())}`)
      .then((l) => {
        setList(l);
        setError(null);
      })
      .catch((e) => setError(errorText(e)));
  }, [search]);
  useEffect(() => {
    const id = setTimeout(load, 250);
    return () => clearTimeout(id);
  }, [load]);

  return (
    <div className="space-y-4">
      {error && <Alert tone="danger">{error}</Alert>}
      {notice && <Alert tone="success">{notice}</Alert>}
      <Card title={t("security.keys.title")} description={t("security.keys.subtitle")}>
        <div className="relative mb-5 max-w-xl">
          <Search className="pointer-events-none absolute top-2.5 left-2.5 size-4 text-muted" />
          <Input
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            placeholder={t("security.keys.search")}
            className="pl-8"
            aria-label={t("security.keys.search")}
            autoFocus
          />
        </div>
        {!list ? (
          <Spinner />
        ) : list.length === 0 ? (
          <p className="py-6 text-center text-sm text-muted">{search ? t("security.keys.noMatch") : t("security.keys.none")}</p>
        ) : (
          <div className="-mx-5 -mb-5 overflow-x-auto border-t border-line">
            <table className="w-full min-w-[820px] text-sm">
              <thead className="border-b border-line text-left text-xs text-muted">
                <tr>
                  <th className="px-5 py-2.5 font-medium">{t("security.keys.keyId")}</th>
                  <th className="px-3 py-2.5 font-medium">{t("devices.device")}</th>
                  <th className="px-3 py-2.5 font-medium">{t("security.keys.volume")}</th>
                  <th className="px-3 py-2.5 font-medium">{t("security.keys.status")}</th>
                  <th className="px-3 py-2.5 font-medium">{t("security.keys.lastShown")}</th>
                  <th className="px-5 py-2.5" />
                </tr>
              </thead>
              <tbody className="divide-y divide-line">
                {list.map((k) => (
                  <tr key={k.id}>
                    <td className="px-5 py-2.5 font-mono text-xs">
                      <span className="font-semibold">{k.keyId.slice(1, 9)}</span>
                      {k.keyId.slice(9)}
                    </td>
                    <td className="px-3 py-2.5">
                      {k.deviceId ? (
                        <Link to={`/devices/${k.deviceId}?tab=security`} className="font-medium hover:text-primary">
                          {k.hostname}
                        </Link>
                      ) : (
                        <span className="font-medium">{k.hostname}</span>
                      )}
                      <div className="text-xs text-muted">
                        {k.serial && `${t("security.keys.serial")} ${k.serial}`}
                        {k.serial && k.groupName && " · "}
                        {k.groupName}
                      </div>
                    </td>
                    <td className="px-3 py-2.5">{k.volume}</td>
                    <td className="px-3 py-2.5">
                      {!k.deviceId ? (
                        <Badge>{t("security.keys.deviceRemoved")}</Badge>
                      ) : k.removedAt ? (
                        <Badge tone="warning">{t("security.keys.replaced", { when: formatDate(k.removedAt, i18n.language) })}</Badge>
                      ) : (
                        <Badge tone="success">{t("security.keys.inUse")}</Badge>
                      )}
                    </td>
                    <td className="px-3 py-2.5 text-xs text-muted">{k.revealedAt ? `${formatDate(k.revealedAt, i18n.language)} · ${k.revealedBy}` : t("security.keys.neverShown")}</td>
                    <td className="px-5 py-2.5 text-right">
                      <button
                        type="button"
                        onClick={() => setOpen(k)}
                        className="inline-flex items-center gap-1.5 rounded-md border border-line px-2.5 py-1 text-xs font-medium hover:bg-surface-muted"
                      >
                        <Eye className="size-3.5" />
                        {t("security.keys.show")}
                      </button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Card>
      {open && (
        <RevealDialog
          recoveryKey={open}
          onClose={() => {
            setOpen(null);
            load();
          }}
          onRotate={
            open.deviceId && can("devices.manage")
              ? () => {
                  setRotate(open);
                  setOpen(null);
                }
              : undefined
          }
        />
      )}
      {rotate && rotate.deviceId && (
        <RotateDialog
          deviceId={rotate.deviceId}
          volume={rotate.volume}
          onClose={() => setRotate(null)}
          onDone={() => setNotice(t("security.keys.rotateStarted", { host: rotate.hostname }))}
        />
      )}
    </div>
  );
}
