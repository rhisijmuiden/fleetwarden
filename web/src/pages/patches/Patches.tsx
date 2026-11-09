import { useCallback, useEffect, useState } from "react";
import { Link, useSearchParams } from "react-router";
import { useTranslation } from "react-i18next";
import { Check, Clock, Pencil, Plus, Search, ShieldAlert, ShieldCheck, Trash2, X } from "lucide-react";
import { del, get, put, type PatchCategory, type PatchPolicies, type PatchPolicy, type PatchSummary, type UpdateSummary } from "../../api";
import { useAuth } from "../../auth";
import { Alert, Badge, Button, Card, Input, Modal, PageHeader, Select, Spinner, formatDate, useErrorText } from "../../components/ui";
import { PlatformBadge, timeAgo } from "../devices/util";
import { PolicyForm } from "./PolicyForm";
import { CategoryBadge, RunState, formatInZone, windowText } from "./util";

type Tab = "overview" | "updates" | "policies";
const tabs: Tab[] = ["overview", "updates", "policies"];

export function Patches() {
  const { t } = useTranslation();
  const [params, setParams] = useSearchParams();
  const tab = (tabs.includes(params.get("tab") as Tab) ? params.get("tab") : "overview") as Tab;
  return (
    <div className="space-y-6">
      <PageHeader title={t("patches.title")} description={t("patches.subtitle")} />
      <div className="flex gap-1 border-b border-line" role="tablist">
        {tabs.map((k) => (
          <button
            key={k}
            role="tab"
            aria-selected={tab === k}
            onClick={() => setParams(k === "overview" ? {} : { tab: k })}
            className={`-mb-px border-b-2 px-4 py-2 text-sm font-medium ${tab === k ? "border-primary text-fg" : "border-transparent text-muted hover:text-fg"}`}
          >
            {t(`patches.tabs.${k}`)}
          </button>
        ))}
      </div>
      {tab === "overview" && <Overview />}
      {tab === "updates" && <Updates />}
      {tab === "policies" && <Policies />}
    </div>
  );
}

function Stat({ label, value, tone, icon }: { label: string; value: number | string; tone?: string; icon?: React.ReactNode }) {
  return (
    <div className="rounded-lg border border-line bg-surface p-4">
      <div className="flex items-center gap-2 text-xs font-medium text-muted uppercase">
        {icon}
        {label}
      </div>
      <div className={`mt-2 text-2xl font-semibold tabular-nums ${tone ?? ""}`}>{value}</div>
    </div>
  );
}

function Overview() {
  const { t, i18n } = useTranslation();
  const errorText = useErrorText();
  const [sum, setSum] = useState<PatchSummary | null>(null);
  const [error, setError] = useState<string | null>(null);
  const load = useCallback(() => {
    get<PatchSummary>("/patches/summary")
      .then(setSum)
      .catch((e) => setError(errorText(e)));
  }, []);
  useEffect(() => {
    load();
    const id = setInterval(load, 15000);
    return () => clearInterval(id);
  }, [load]);
  if (error) return <Alert tone="danger">{error}</Alert>;
  if (!sum) return <Spinner />;
  const s = sum.stats;
  const pct = s.scanned ? Math.round((s.upToDate / s.scanned) * 100) : 0;
  return (
    <div className="space-y-6">
      {sum.policies === 0 && (
        <Alert tone="info" title={t("patches.noPolicyTitle")}>
          {t("patches.noPolicyText")}{" "}
          <Link to="/patches?tab=policies" className="font-medium text-primary hover:underline">
            {t("patches.createPolicy")}
          </Link>
        </Alert>
      )}
      <div className="grid grid-cols-2 gap-4 md:grid-cols-4">
        <Stat label={t("patches.stats.compliance")} value={s.scanned ? `${pct}%` : "–"} icon={<ShieldCheck className="size-4" />} />
        <Stat label={t("patches.stats.missing")} value={s.missing} tone={s.missing ? "text-warning" : ""} />
        <Stat label={t("patches.stats.security")} value={s.security} tone={s.security ? "text-danger" : ""} icon={<ShieldAlert className="size-4" />} />
        <Stat label={t("patches.stats.reboot")} value={s.rebootPending} />
        <Stat label={t("patches.stats.managed")} value={`${s.managed} / ${s.devices}`} />
        <Stat label={t("patches.stats.scanned")} value={s.scanned} />
        <Stat label={t("patches.stats.running")} value={s.runsOpen} />
        <Stat label={t("patches.stats.failed")} value={s.runsFailed} tone={s.runsFailed ? "text-danger" : ""} />
      </div>
      {s.scanErrors > 0 && <Alert tone="warning">{t("patches.scanErrors", { count: s.scanErrors })}</Alert>}
      <Card title={t("patches.recentRuns")}>
        {sum.runs.length === 0 ? (
          <p className="rounded-md border border-dashed border-line px-4 py-6 text-center text-sm text-muted">{t("patches.noRuns")}</p>
        ) : (
          <RunsTable runs={sum.runs} lang={i18n.language} showDevice />
        )}
      </Card>
    </div>
  );
}

export function RunsTable({ runs, lang, showDevice }: { runs: PatchSummary["runs"]; lang: string; showDevice?: boolean }) {
  const { t } = useTranslation();
  return (
    <div className="-mx-5 -my-5 overflow-x-auto">
      <table className="w-full min-w-[720px] text-sm">
        <thead className="border-b border-line text-left text-xs text-muted uppercase">
          <tr>
            <th className="px-5 py-3 font-medium">{t("patches.started")}</th>
            {showDevice && <th className="px-3 py-3 font-medium">{t("patches.device")}</th>}
            <th className="px-3 py-3 font-medium">{t("patches.trigger")}</th>
            <th className="px-3 py-3 font-medium">{t("patches.status")}</th>
            <th className="px-3 py-3 font-medium">{t("patches.result")}</th>
            <th className="px-5 py-3 font-medium">{t("patches.message")}</th>
          </tr>
        </thead>
        <tbody className="divide-y divide-line">
          {runs.map((r) => (
            <tr key={r.id}>
              <td className="px-5 py-2 whitespace-nowrap">{formatDate(r.startedAt, lang)}</td>
              {showDevice && (
                <td className="px-3 py-2">
                  <Link to={`/devices/${r.deviceId}`} className="font-medium hover:text-primary">
                    {r.hostname}
                  </Link>
                </td>
              )}
              <td className="px-3 py-2 text-muted">
                {t(`patches.triggers.${r.trigger}`)}
                {r.requestedBy && ` · ${r.requestedBy}`}
              </td>
              <td className="px-3 py-2">
                <RunState run={r} />
                {r.state === "postponed" && r.postponedUntil && (
                  <span className="ml-2 inline-flex items-center gap-1 text-xs text-muted">
                    <Clock className="size-3" /> {formatDate(r.postponedUntil, lang)}
                  </span>
                )}
              </td>
              <td className="px-3 py-2 whitespace-nowrap text-muted">
                {t("patches.resultText", { installed: r.installed, total: r.updates.length })}
                {r.failed > 0 && <span className="text-danger"> · {t("patches.failedCount", { count: r.failed })}</span>}
                {r.postpones > 0 && <span> · {t("patches.postponedCount", { count: r.postpones })}</span>}
                {r.rebootRequired && <span> · {t("patches.rebootNeeded")}</span>}
              </td>
              <td className="max-w-[280px] truncate px-5 py-2 text-muted" title={r.message}>
                {r.message}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

const categories: PatchCategory[] = ["security", "critical", "cumulative", "definition", "feature", "other"];

function Updates() {
  const { t, i18n } = useTranslation();
  const { can } = useAuth();
  const errorText = useErrorText();
  const [list, setList] = useState<UpdateSummary[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [search, setSearch] = useState("");
  const [category, setCategory] = useState("");
  const [platform, setPlatform] = useState("");
  const [decision, setDecision] = useState("");
  const [n, setN] = useState(0);
  const approve = can("patches.approve");

  useEffect(() => {
    const q = new URLSearchParams();
    if (search.trim()) q.set("search", search.trim());
    if (category) q.set("category", category);
    if (platform) q.set("platform", platform);
    if (decision) q.set("decision", decision);
    const ctl = new AbortController();
    const timer = setTimeout(() => {
      get<UpdateSummary[]>(`/patches/updates?${q}`, ctl.signal)
        .then(setList)
        .catch((e) => !ctl.signal.aborted && setError(errorText(e)));
    }, 250);
    return () => {
      clearTimeout(timer);
      ctl.abort();
    };
  }, [search, category, platform, decision, n]);

  const decide = (u: UpdateSummary, d: "" | "approved" | "declined") =>
    put("/patches/decision", { updateId: u.updateId, decision: d })
      .then(() => setN((x) => x + 1))
      .catch((e) => setError(errorText(e)));

  return (
    <Card
      title={t("patches.missingUpdates")}
      description={t("patches.missingUpdatesHint")}
      actions={
        <div className="flex flex-wrap gap-2">
          <div className="relative w-56 max-w-full">
            <Search className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted" />
            <Input value={search} onChange={(e) => setSearch(e.target.value)} placeholder={t("patches.searchUpdates")} className="pl-9" aria-label={t("patches.searchUpdates")} />
          </div>
          <Select value={category} onChange={(e) => setCategory(e.target.value)} className="w-40" aria-label={t("patches.category")}>
            <option value="">{t("patches.allCategories")}</option>
            {categories.map((c) => (
              <option key={c} value={c}>
                {t(`patches.categories.${c}`)}
              </option>
            ))}
          </Select>
          <Select value={platform} onChange={(e) => setPlatform(e.target.value)} className="w-32" aria-label={t("devices.platform")}>
            <option value="">{t("devices.allPlatforms")}</option>
            <option value="windows">Windows</option>
            <option value="linux">Linux</option>
          </Select>
          <Select value={decision} onChange={(e) => setDecision(e.target.value)} className="w-40" aria-label={t("patches.decision")}>
            <option value="">{t("patches.allDecisions")}</option>
            <option value="none">{t("patches.decisions.none")}</option>
            <option value="approved">{t("patches.decisions.approved")}</option>
            <option value="declined">{t("patches.decisions.declined")}</option>
          </Select>
        </div>
      }
    >
      {error && <Alert tone="danger">{error}</Alert>}
      {!list ? (
        <Spinner />
      ) : list.length === 0 ? (
        <p className="rounded-md border border-dashed border-line px-4 py-6 text-center text-sm text-muted">{t("patches.noMissing")}</p>
      ) : (
        <div className="-mx-5 -my-5 max-h-[640px] overflow-auto">
          <table className="w-full min-w-[820px] text-sm">
            <thead className="sticky top-0 border-b border-line bg-surface text-left text-xs text-muted uppercase">
              <tr>
                <th className="px-5 py-3 font-medium">{t("patches.update")}</th>
                <th className="px-3 py-3 font-medium">{t("patches.category")}</th>
                <th className="px-3 py-3 font-medium">{t("devices.platform")}</th>
                <th className="px-3 py-3 text-right font-medium">{t("patches.devices")}</th>
                <th className="px-3 py-3 font-medium">{t("patches.firstSeen")}</th>
                <th className="px-5 py-3 text-right font-medium">{t("patches.decision")}</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-line">
              {list.map((u) => (
                <tr key={u.updateId}>
                  <td className="px-5 py-2">
                    <div className="font-medium">{u.title}</div>
                    <div className="text-xs text-muted">
                      {[u.kb, u.severity].filter(Boolean).join(" · ")}
                      {u.releasedAt && ` · ${t("patches.released", { when: formatDate(u.releasedAt, i18n.language) })}`}
                    </div>
                  </td>
                  <td className="px-3 py-2">
                    <CategoryBadge category={u.category} />
                  </td>
                  <td className="px-3 py-2">
                    <PlatformBadge platform={u.platform} />
                  </td>
                  <td className="px-3 py-2 text-right tabular-nums">{u.devices}</td>
                  <td className="px-3 py-2 whitespace-nowrap text-muted">{timeAgo(u.firstSeenAt, i18n.language)}</td>
                  <td className="px-5 py-2">
                    <div className="flex items-center justify-end gap-1">
                      {u.decision && (
                        <span title={u.decidedBy}>
                          <Badge tone={u.decision === "approved" ? "success" : "danger"}>{t(`patches.decisions.${u.decision}`)}</Badge>
                        </span>
                      )}
                      {approve && (
                        <>
                          <Button
                            variant="ghost"
                            size="sm"
                            title={t("patches.approve")}
                            aria-label={t("patches.approve")}
                            disabled={u.decision === "approved"}
                            icon={<Check className="size-4" />}
                            onClick={() => decide(u, "approved")}
                          />
                          <Button
                            variant="ghost"
                            size="sm"
                            title={t("patches.decline")}
                            aria-label={t("patches.decline")}
                            disabled={u.decision === "declined"}
                            icon={<X className="size-4" />}
                            onClick={() => decide(u, "declined")}
                          />
                          {u.decision && (
                            <Button variant="ghost" size="sm" onClick={() => decide(u, "")}>
                              {t("patches.followPolicy")}
                            </Button>
                          )}
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
  );
}

function Policies() {
  const { t, i18n } = useTranslation();
  const { can } = useAuth();
  const errorText = useErrorText();
  const [data, setData] = useState<PatchPolicies | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [editing, setEditing] = useState<PatchPolicy | "new" | null>(null);
  const [deleting, setDeleting] = useState<PatchPolicy | null>(null);
  const [n, setN] = useState(0);
  const manage = can("patches.manage");
  useEffect(() => {
    get<PatchPolicies>("/patches/policies")
      .then(setData)
      .catch((e) => setError(errorText(e)));
  }, [n]);

  return (
    <div className="space-y-6">
      {error && <Alert tone="danger">{error}</Alert>}
      <Card
        title={t("patches.policies")}
        description={t("patches.policiesHint")}
        actions={
          manage && (
            <Button icon={<Plus className="size-4" />} onClick={() => setEditing("new")}>
              {t("patches.addPolicy")}
            </Button>
          )
        }
      >
        {!data ? (
          <Spinner />
        ) : data.policies.length === 0 ? (
          <p className="rounded-md border border-dashed border-line px-4 py-6 text-center text-sm text-muted">{t("patches.noPolicies")}</p>
        ) : (
          <div className="-mx-5 -my-5 overflow-x-auto">
            <table className="w-full min-w-[820px] text-sm">
              <thead className="border-b border-line text-left text-xs text-muted uppercase">
                <tr>
                  <th className="px-5 py-3 font-medium">{t("patches.policyName")}</th>
                  <th className="px-3 py-3 font-medium">{t("patches.window")}</th>
                  <th className="px-3 py-3 font-medium">{t("patches.rebootPolicy")}</th>
                  <th className="px-3 py-3 font-medium">{t("patches.groups")}</th>
                  <th className="px-5 py-3 text-right font-medium">
                    <span className="sr-only">{t("common.actions")}</span>
                  </th>
                </tr>
              </thead>
              <tbody className="divide-y divide-line">
                {data.policies.map((p) => (
                  <tr key={p.id}>
                    <td className="px-5 py-3">
                      <div className="flex items-center gap-2 font-medium">
                        {p.name}
                        {!p.enabled && <Badge>{t("patches.disabled")}</Badge>}
                        {p.enabled && p.windowOpen && <Badge tone="success">{t("patches.windowOpenNow")}</Badge>}
                      </div>
                      {p.description && <div className="mt-0.5 text-xs text-muted">{p.description}</div>}
                    </td>
                    <td className="px-3 py-3">
                      <div>{windowText(p, i18n.language, t)}</div>
                      <div className="text-xs text-muted">
                        {p.timeZone || t("patches.serverTime")}
                        {p.enabled && p.nextWindow && !p.windowOpen && ` · ${t("patches.nextWindow", { when: formatInZone(p.nextWindow, i18n.language, p.timeZone || data.serverTimeZone) })}`}
                      </div>
                    </td>
                    <td className="px-3 py-3">
                      {t(`patches.reboot.${p.reboot}`)}
                      {p.notifyUsers && <div className="text-xs text-muted">{t("patches.postponeSummary", { count: p.maxPostpones, minutes: p.postponeMinutes })}</div>}
                    </td>
                    <td className="px-3 py-3">
                      {p.groups.length === 0 ? (
                        <span className="text-muted">{t("patches.noGroups")}</span>
                      ) : (
                        <div className="flex flex-wrap gap-1">
                          {p.groups.map((g) => (
                            <Badge key={g.id}>{g.name}</Badge>
                          ))}
                        </div>
                      )}
                    </td>
                    <td className="px-5 py-3">
                      {manage && (
                        <div className="flex justify-end gap-1">
                          <Button variant="ghost" size="sm" aria-label={t("common.edit")} title={t("common.edit")} icon={<Pencil className="size-4" />} onClick={() => setEditing(p)} />
                          <Button variant="ghost" size="sm" aria-label={t("common.delete")} title={t("common.delete")} icon={<Trash2 className="size-4" />} onClick={() => setDeleting(p)} />
                        </div>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Card>
      {data && (
        <PolicyForm
          editing={editing}
          defaults={data.defaults}
          serverTimeZone={data.serverTimeZone}
          onClose={() => setEditing(null)}
          onSaved={() => {
            setEditing(null);
            setN((x) => x + 1);
          }}
        />
      )}
      <Modal
        open={!!deleting}
        onClose={() => setDeleting(null)}
        title={t("patches.deletePolicy")}
        footer={
          <>
            <Button variant="secondary" onClick={() => setDeleting(null)}>
              {t("common.cancel")}
            </Button>
            <Button
              variant="danger"
              onClick={() =>
                deleting &&
                del(`/patches/policies/${deleting.id}`)
                  .then(() => {
                    setDeleting(null);
                    setN((x) => x + 1);
                  })
                  .catch((e) => {
                    setDeleting(null);
                    setError(errorText(e));
                  })
              }
            >
              {t("common.delete")}
            </Button>
          </>
        }
      >
        <p className="text-sm">{deleting && t("patches.deletePolicyConfirm", { name: deleting.name, count: deleting.groups.length })}</p>
      </Modal>
    </div>
  );
}
