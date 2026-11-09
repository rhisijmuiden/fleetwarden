import { useCallback, useEffect, useMemo, useState } from "react";
import { Link, useSearchParams } from "react-router";
import { useTranslation } from "react-i18next";
import { Boxes, CalendarClock, FileSpreadsheet, HelpCircle, Pencil, Search, ShieldCheck, ShieldOff, Upload } from "lucide-react";
import { get, post, put, type AssetField, type AssetList, type AssetRow, type ImportResult } from "../../api";
import { useAuth } from "../../auth";
import { useGroups } from "../../components/GroupScope";
import { Alert, Badge, Button, Card, Input, Modal, PageHeader, Select, Spinner, useErrorText } from "../../components/ui";
import { AssetForm, AssetInput, WarrantyBadge, assetFields, emptyAsset, formatDay } from "./util";
import { Licenses } from "./Licenses";

type Tab = "devices" | "licenses";

export function Assets() {
  const { t } = useTranslation();
  const [params, setParams] = useSearchParams();
  const tab: Tab = params.get("tab") === "licenses" ? "licenses" : "devices";
  const setTab = (k: Tab) => {
    const next = new URLSearchParams();
    if (k !== "devices") next.set("tab", k);
    setParams(next);
  };
  return (
    <div className="space-y-6">
      <PageHeader title={t("assets.title")} description={t("assets.subtitle")} />
      <div className="flex gap-1 overflow-x-auto border-b border-line" role="tablist">
        {(["devices", "licenses"] as Tab[]).map((k) => (
          <button
            key={k}
            role="tab"
            aria-selected={tab === k}
            onClick={() => setTab(k)}
            className={`-mb-px border-b-2 px-4 py-2 text-sm font-medium whitespace-nowrap ${tab === k ? "border-primary text-fg" : "border-transparent text-muted hover:text-fg"}`}
          >
            {t(`assets.tabs.${k}`)}
          </button>
        ))}
      </div>
      {tab === "devices" ? <Register /> : <Licenses />}
    </div>
  );
}

function Tile({
  icon,
  label,
  value,
  sub,
  tone,
  active,
  onClick,
}: {
  icon: React.ReactNode;
  label: string;
  value: number;
  sub?: string;
  tone: "danger" | "warning" | "success" | "neutral";
  active: boolean;
  onClick: () => void;
}) {
  const tones = {
    danger: "bg-danger/10 text-danger",
    warning: "bg-warning/10 text-warning",
    success: "bg-success/10 text-success",
    neutral: "bg-surface-muted text-muted",
  };
  return (
    <button
      type="button"
      onClick={onClick}
      aria-pressed={active}
      className={`flex items-center gap-4 rounded-lg border bg-surface p-4 text-left transition hover:border-primary/50 ${active ? "border-primary ring-2 ring-primary/20" : "border-line"}`}
    >
      <span className={`grid size-10 shrink-0 place-items-center rounded-md ${tones[tone]}`}>{icon}</span>
      <span className="min-w-0">
        <span className="block text-2xl font-semibold tabular-nums">{value}</span>
        <span className="block truncate text-sm text-muted">{label}</span>
        {sub && <span className="block truncate text-xs text-muted">{sub}</span>}
      </span>
    </button>
  );
}

function Register() {
  const { t, i18n } = useTranslation();
  const { can } = useAuth();
  const errorText = useErrorText();
  const manage = can("assets.manage");
  const [params, setParams] = useSearchParams();
  const groups = useGroups();
  const search = params.get("search") ?? "";
  const group = params.get("group") ?? "";
  const warranty = params.get("warranty") ?? "";
  const [searchInput, setSearchInput] = useState(search);
  const [data, setData] = useState<AssetList | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [editing, setEditing] = useState<AssetRow | null>(null);
  const [bulk, setBulk] = useState(false);
  const [importing, setImporting] = useState(false);
  const [reload, setReload] = useState(0);

  const setParam = (k: string, v: string) => {
    const next = new URLSearchParams(params);
    if (v) next.set(k, v);
    else next.delete(k);
    setParams(next, { replace: true });
  };
  useEffect(() => {
    const id = setTimeout(() => searchInput !== search && setParam("search", searchInput.trim()), 300);
    return () => clearTimeout(id);
  }, [searchInput]);

  const query = useMemo(() => {
    const q = new URLSearchParams();
    if (search) q.set("search", search);
    if (group) q.set("group", group);
    if (warranty) q.set("warranty", warranty);
    return q.toString();
  }, [search, group, warranty]);
  const load = useCallback(() => {
    get<AssetList>(`/assets?${query}`)
      .then((d) => {
        setData(d);
        setError(null);
      })
      .catch((e) => setError(errorText(e)));
  }, [query]);
  useEffect(load, [load, reload]);

  const items = data?.items ?? [];
  const sum = data?.summary;
  const days = sum?.expiringDays ?? 90;
  const allSelected = items.length > 0 && items.every((d) => selected.has(d.id));
  const toggleAll = () => setSelected(allSelected ? new Set() : new Set(items.map((d) => d.id)));
  const toggle = (id: string) => {
    const next = new Set(selected);
    if (next.has(id)) next.delete(id);
    else next.add(id);
    setSelected(next);
  };
  const filter = (w: string) => setParam("warranty", warranty === w ? "" : w);

  return (
    <div className="space-y-6">
      {error && <Alert tone="danger">{error}</Alert>}
      {notice && <Alert tone="success">{notice}</Alert>}
      {sum && (
        <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
          <Tile
            icon={<ShieldOff className="size-5" />}
            label={t("assets.summary.expired")}
            value={sum.expired}
            tone={sum.expired ? "danger" : "neutral"}
            active={warranty === "expired"}
            onClick={() => filter("expired")}
          />
          <Tile
            icon={<CalendarClock className="size-5" />}
            label={t("assets.summary.expiring")}
            sub={t("assets.summary.withinDays", { count: days })}
            value={sum.expiring}
            tone={sum.expiring ? "warning" : "neutral"}
            active={warranty === "expiring"}
            onClick={() => filter("expiring")}
          />
          <Tile
            icon={<ShieldCheck className="size-5" />}
            label={t("assets.summary.valid")}
            value={sum.valid}
            tone="success"
            active={warranty === "valid"}
            onClick={() => filter("valid")}
          />
          <Tile
            icon={<HelpCircle className="size-5" />}
            label={t("assets.summary.unknown")}
            value={sum.unknown}
            tone="neutral"
            active={warranty === "unknown"}
            onClick={() => filter("unknown")}
          />
        </div>
      )}

      <div className="flex flex-wrap items-center gap-2">
        <div className="relative min-w-[220px] flex-1">
          <Search className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted" />
          <Input
            value={searchInput}
            onChange={(e) => setSearchInput(e.target.value)}
            placeholder={t("assets.searchPlaceholder")}
            className="pl-9"
            aria-label={t("assets.search")}
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
        <a
          href="/api/v1/assets/export"
          download
          className="inline-flex h-9 items-center gap-2 rounded-md border border-line bg-surface px-3 text-sm font-medium hover:bg-surface-muted"
        >
          <FileSpreadsheet className="size-4" /> {t("assets.export")}
        </a>
        {manage && (
          <Button variant="secondary" icon={<Upload className="size-4" />} onClick={() => setImporting(true)}>
            {t("assets.import.button")}
          </Button>
        )}
      </div>

      {manage && selected.size > 0 && (
        <div className="flex flex-wrap items-center gap-3 rounded-md border border-primary/30 bg-primary/5 px-4 py-2.5 text-sm">
          <span className="font-medium">{t("devices.selected", { count: selected.size })}</span>
          <Button size="sm" variant="secondary" icon={<Pencil className="size-4" />} onClick={() => setBulk(true)}>
            {t("assets.bulk.button")}
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
            <Boxes className="size-10 text-muted" strokeWidth={1.5} />
            <p className="mt-3 font-medium">{search || group || warranty ? t("devices.noMatch") : t("assets.empty")}</p>
          </div>
        ) : (
          <div className="-mx-5 -my-5 overflow-x-auto">
            <table className="w-full min-w-[960px] text-sm">
              <thead className="border-b border-line text-left text-xs text-muted uppercase">
                <tr>
                  {manage && (
                    <th className="w-10 py-3 pl-5">
                      <input type="checkbox" className="size-4 accent-primary" checked={allSelected} onChange={toggleAll} aria-label={t("devices.selectAll")} />
                    </th>
                  )}
                  <th className={`${manage ? "px-3" : "px-5"} py-3 font-medium`}>{t("devices.device")}</th>
                  <th className="px-3 py-3 font-medium">{t("assets.cols.hardware")}</th>
                  <th className="px-3 py-3 font-medium">{t("assets.fields.owner")}</th>
                  <th className="px-3 py-3 font-medium">{t("assets.fields.location")}</th>
                  <th className="px-3 py-3 font-medium">{t("assets.fields.purchaseDate")}</th>
                  <th className="px-3 py-3 font-medium">{t("assets.fields.warrantyEnd")}</th>
                  {manage && <th className="w-12 px-5 py-3" />}
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
                      <Link to={`/devices/${d.id}`} className="inline-flex items-center gap-2 font-medium hover:underline">
                        <span className={`size-2 shrink-0 rounded-full ${d.online ? "bg-success" : "bg-muted/40"}`} title={d.online ? t("devices.online") : t("devices.offline")} />
                        {d.hostname}
                      </Link>
                      <div className="text-xs text-muted">
                        {d.groupName}
                        {d.assetTag && <> · {d.assetTag}</>}
                      </div>
                    </td>
                    <td className="px-3 py-3">
                      <div className="truncate">{[d.manufacturer, d.model].filter(Boolean).join(" ") || "–"}</div>
                      {d.serial && <div className="font-mono text-xs text-muted">{d.serial}</div>}
                    </td>
                    <td className="px-3 py-3">
                      {d.owner || (d.loggedInUser ? <span className="text-muted italic">{d.loggedInUser}</span> : "–")}
                      {d.department && <div className="text-xs text-muted">{d.department}</div>}
                    </td>
                    <td className="px-3 py-3">{d.location || "–"}</td>
                    <td className="px-3 py-3 whitespace-nowrap">
                      {formatDay(d.purchaseDate, i18n.language)}
                      {d.supplier && <div className="text-xs text-muted">{d.supplier}</div>}
                    </td>
                    <td className="px-3 py-3 whitespace-nowrap">
                      <div>{formatDay(d.warrantyEnd, i18n.language)}</div>
                      <WarrantyBadge end={d.warrantyEnd} expiringDays={days} />
                    </td>
                    {manage && (
                      <td className="px-5 py-3 text-right">
                        <Button size="sm" variant="ghost" icon={<Pencil className="size-4" />} aria-label={t("common.edit")} onClick={() => setEditing(d)} />
                      </td>
                    )}
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Card>

      {editing && (
        <AssetForm
          deviceId={editing.id}
          initial={editing}
          loggedInUser={editing.loggedInUser}
          onClose={() => setEditing(null)}
          onSaved={() => setReload((r) => r + 1)}
        />
      )}
      {bulk && (
        <BulkDialog
          ids={[...selected]}
          onClose={() => setBulk(false)}
          onSaved={(n) => {
            setNotice(t("assets.bulk.done", { count: n }));
            setSelected(new Set());
            setReload((r) => r + 1);
          }}
        />
      )}
      {importing && (
        <ImportDialog
          onClose={() => setImporting(false)}
          onApplied={(n) => {
            setNotice(t("assets.import.done", { count: n }));
            setReload((r) => r + 1);
          }}
        />
      )}
    </div>
  );
}

function BulkDialog({ ids, onClose, onSaved }: { ids: string[]; onClose: () => void; onSaved: (n: number) => void }) {
  const { t } = useTranslation();
  const errorText = useErrorText();
  const [on, setOn] = useState<Set<AssetField>>(new Set());
  const [d, setD] = useState({ ...emptyAsset });
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const save = () => {
    const fields: Partial<Record<AssetField, string>> = {};
    on.forEach((f) => (fields[f] = d[f]));
    setBusy(true);
    setError(null);
    put<{ updated: number }>("/assets", { ids, fields })
      .then((r) => {
        onSaved(r.updated);
        onClose();
      })
      .catch((e) => setError(errorText(e)))
      .finally(() => setBusy(false));
  };

  return (
    <Modal
      open
      wide
      onClose={onClose}
      title={t("assets.bulk.title", { count: ids.length })}
      footer={
        <>
          <Button variant="secondary" onClick={onClose}>
            {t("common.cancel")}
          </Button>
          <Button onClick={save} loading={busy} disabled={on.size === 0}>
            {t("assets.bulk.apply")}
          </Button>
        </>
      }
    >
      <p className="text-sm text-muted">{t("assets.bulk.hint")}</p>
      {error && <Alert tone="danger">{error}</Alert>}
      <div className="space-y-3">
        {assetFields.map((f) => (
          <div key={f} className="flex items-end gap-3">
            <input
              type="checkbox"
              className="mb-2.5 size-4 accent-primary"
              checked={on.has(f)}
              aria-label={t("assets.bulk.change", { field: t(`assets.fields.${f}`) })}
              onChange={(e) => {
                const next = new Set(on);
                if (e.target.checked) next.add(f);
                else next.delete(f);
                setOn(next);
              }}
            />
            <div className={`flex-1 ${on.has(f) ? "" : "opacity-50"}`}>
              <AssetInput
                field={f}
                d={d}
                set={(p) => {
                  setD({ ...d, ...p });
                  setOn(new Set(on).add(f));
                }}
              />
            </div>
          </div>
        ))}
      </div>
    </Modal>
  );
}

const statusTone = { update: "primary", unchanged: "neutral", not_found: "warning", ambiguous: "warning", invalid: "danger" } as const;

function ImportDialog({ onClose, onApplied }: { onClose: () => void; onApplied: (n: number) => void }) {
  const { t } = useTranslation();
  const errorText = useErrorText();
  const [csv, setCsv] = useState("");
  const [fileName, setFileName] = useState("");
  const [check, setCheck] = useState<ImportResult | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const run = (text: string, apply: boolean) => {
    setBusy(true);
    setError(null);
    post<ImportResult>("/assets/import", { csv: text, apply })
      .then((r) => {
        if (apply) {
          onApplied(r.updated);
          onClose();
        } else setCheck(r);
      })
      .catch((e) => setError(errorText(e)))
      .finally(() => setBusy(false));
  };
  const onFile = (f: File | undefined) => {
    if (!f) return;
    setFileName(f.name);
    setCheck(null);
    f.text().then((text) => {
      setCsv(text);
      run(text, false);
    });
  };
  const shown = check?.rows.filter((r) => r.status !== "unchanged") ?? [];

  return (
    <Modal
      open
      wide
      onClose={onClose}
      title={t("assets.import.title")}
      footer={
        <>
          <Button variant="secondary" onClick={onClose}>
            {t("common.cancel")}
          </Button>
          <Button onClick={() => run(csv, true)} loading={busy} disabled={!check || check.updated === 0}>
            {t("assets.import.apply", { count: check?.updated ?? 0 })}
          </Button>
        </>
      }
    >
      <p className="text-sm text-muted">{t("assets.import.hint")}</p>
      <label className="flex cursor-pointer items-center justify-center gap-2 rounded-md border border-dashed border-line px-4 py-6 text-sm hover:border-primary/50 hover:bg-surface-muted/50">
        <Upload className="size-4" />
        <span>{fileName || t("assets.import.choose")}</span>
        <input type="file" accept=".csv,text/csv,text/plain" className="sr-only" onChange={(e) => onFile(e.target.files?.[0])} />
      </label>
      {error && <Alert tone="danger">{error}</Alert>}
      {busy && !check && <Spinner />}
      {check && (
        <div className="space-y-3 text-sm">
          <p>
            {t("assets.import.columns")}: {check.columns.map((c) => (c === "hostname" || c === "serial" ? t(`assets.import.key.${c}`) : t(`assets.fields.${c}`))).join(", ")}
            {check.ignored.length > 0 && <span className="text-muted"> · {t("assets.import.ignored", { list: check.ignored.join(", ") })}</span>}
          </p>
          <p className="font-medium">
            {t("assets.import.summary", { update: check.updated, total: check.rows.length })}
          </p>
          {shown.length > 0 && (
            <div className="max-h-72 overflow-y-auto rounded-md border border-line">
              <table className="w-full text-xs">
                <thead className="sticky top-0 bg-surface text-left text-muted">
                  <tr>
                    <th className="px-3 py-2 font-medium">{t("assets.import.line")}</th>
                    <th className="px-3 py-2 font-medium">{t("devices.device")}</th>
                    <th className="px-3 py-2 font-medium">{t("assets.import.result")}</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-line">
                  {shown.map((r) => (
                    <tr key={r.line}>
                      <td className="px-3 py-2 tabular-nums">{r.line}</td>
                      <td className="px-3 py-2">
                        {r.hostname ?? r.key}
                        {r.changes && (
                          <div className="text-muted">
                            {Object.entries(r.changes)
                              .map(([k, v]) => `${t(`assets.fields.${k}`)}: ${v || "–"}`)
                              .join(" · ")}
                          </div>
                        )}
                      </td>
                      <td className="px-3 py-2">
                        <Badge tone={statusTone[r.status]}>{t(`assets.import.status.${r.status}`)}</Badge>
                        {r.error && <div className="mt-1 text-muted">{t(`assets.import.errors.${r.error.split(":")[0]}`)}</div>}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </div>
      )}
    </Modal>
  );
}
