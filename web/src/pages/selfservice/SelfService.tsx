import { useCallback, useEffect, useMemo, useState } from "react";
import { Link, useSearchParams } from "react-router";
import { useTranslation } from "react-i18next";
import { Check, CheckCircle2, Inbox, LifeBuoy, MessageSquare, MonitorPlay, Pencil, Plus, Store, Trash2, X } from "lucide-react";
import { del, get, post, put, type JobState, type PackagesView, type SoftwarePackage } from "../../api";
import { useAuth } from "../../auth";
import { useGroups } from "../../components/GroupScope";
import { Alert, Badge, Button, Card, Field, Input, Modal, PageHeader, Select, Spinner, Switch, formatDate, useErrorText } from "../../components/ui";
import { PlatformBadge } from "../devices/util";
import { timeAgo } from "../devices/util";
import { JobStateBadge, SourceBadge } from "../software/util";
import type { HelpRequest, HelpState, SelfServiceItem, SelfServiceRequest, SelfServiceSettings } from "./types";

type Tab = "requests" | "help" | "catalog" | "settings";

// notifyCounts lets the menu badge refresh after a decision.
const notifyCounts = () => window.dispatchEvent(new Event("fw:selfservice"));

export function SelfService() {
  const { t } = useTranslation();
  const { can, me } = useAuth();
  const [params, setParams] = useSearchParams();
  const tabs: Tab[] = ["requests", "help", "catalog", ...(can("selfservice.manage") && !me?.scoped ? (["settings"] as Tab[]) : [])];
  const tab = (tabs.includes(params.get("tab") as Tab) ? params.get("tab") : "requests") as Tab;
  return (
    <div className="space-y-6">
      <PageHeader title={t("selfservice.title")} description={t("selfservice.subtitle")} />
      <div className="flex gap-1 overflow-x-auto border-b border-line" role="tablist">
        {tabs.map((k) => (
          <button
            key={k}
            role="tab"
            aria-selected={tab === k}
            onClick={() => setParams(k === "requests" ? {} : { tab: k })}
            className={`-mb-px border-b-2 px-4 py-2 text-sm font-medium whitespace-nowrap ${tab === k ? "border-primary text-fg" : "border-transparent text-muted hover:text-fg"}`}
          >
            {t(`selfservice.tabs.${k}`)}
          </button>
        ))}
      </div>
      {tab === "requests" && <Requests />}
      {tab === "help" && <HelpRequests />}
      {tab === "catalog" && <Catalog />}
      {tab === "settings" && <SettingsTab />}
    </div>
  );
}

function Empty({ icon, title, text }: { icon: React.ReactNode; title: string; text?: string }) {
  return (
    <div className="grid place-items-center rounded-md border border-dashed border-line px-6 py-10 text-center">
      <span className="grid size-12 place-items-center rounded-full bg-primary/10 text-primary">{icon}</span>
      <p className="mt-3 font-medium">{title}</p>
      {text && <p className="mt-1 max-w-md text-sm text-muted">{text}</p>}
    </div>
  );
}

// ---- install requests ----

const requestTone: Record<SelfServiceRequest["state"], "neutral" | "primary" | "success" | "warning" | "danger"> = {
  pending: "warning",
  approved: "success",
  rejected: "danger",
  cancelled: "neutral",
};

function Requests() {
  const { t, i18n } = useTranslation();
  const { can } = useAuth();
  const errorText = useErrorText();
  const [list, setList] = useState<SelfServiceRequest[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [deciding, setDeciding] = useState<{ r: SelfServiceRequest; approve: boolean } | null>(null);
  const [note, setNote] = useState("");
  const [busy, setBusy] = useState(false);
  const handle = can("selfservice.handle");

  const load = useCallback(() => {
    get<{ items: SelfServiceRequest[] }>("/selfservice/requests")
      .then((r) => {
        setList(r.items);
        setError(null);
      })
      .catch((e) => setError(errorText(e)));
  }, []);
  useEffect(() => {
    load();
    const id = setInterval(load, 30_000);
    return () => clearInterval(id);
  }, [load]);

  const decide = async () => {
    if (!deciding) return;
    setBusy(true);
    try {
      await post(`/selfservice/requests/${deciding.r.id}/${deciding.approve ? "approve" : "reject"}`, { note });
      setDeciding(null);
      notifyCounts();
      load();
    } catch (e) {
      setError(errorText(e));
      setDeciding(null);
    } finally {
      setBusy(false);
    }
  };

  if (!list) return error ? <Alert tone="danger">{error}</Alert> : <Spinner />;
  const pending = list.filter((r) => r.state === "pending");
  const done = list.filter((r) => r.state !== "pending");
  const row = (r: SelfServiceRequest) => (
    <li key={r.id} className="flex flex-wrap items-start gap-4 px-5 py-4">
      <span className={`grid size-9 shrink-0 place-items-center rounded-md ${r.state === "pending" ? "bg-warning/10 text-warning" : "bg-primary/10 text-primary"}`}>
        <Store className="size-4" />
      </span>
      <div className="min-w-0 flex-1 space-y-1">
        <div className="flex flex-wrap items-center gap-2">
          <span className="font-medium">{r.name}</span>
          <Badge tone={requestTone[r.state]}>{t(`selfservice.requestStates.${r.state}`)}</Badge>
          {r.jobState && <JobStateBadge state={r.jobState as JobState} message={r.jobMessage} />}
        </div>
        <p className="text-sm">
          {t("selfservice.requestedBy", { user: r.username || t("selfservice.unknownUser") })}{" "}
          <Link to={`/devices/${r.deviceId}`} className="font-medium hover:text-primary">
            {r.hostname}
          </Link>{" "}
          <span className="text-muted">({r.groupName})</span>
        </p>
        {r.reason && <p className="rounded-md bg-surface-muted px-3 py-2 text-sm">“{r.reason}”</p>}
        <p className="text-xs text-muted">
          {t("selfservice.requestedAt", { when: timeAgo(r.createdAt, i18n.language), date: formatDate(r.createdAt, i18n.language) })}
          {r.decidedAt &&
            ` · ${t(r.decidedBy === "automatic" ? "selfservice.decidedAuto" : `selfservice.decided.${r.state}`, { user: r.decidedBy, date: formatDate(r.decidedAt, i18n.language) })}`}
          {r.note && ` · “${r.note}”`}
          {r.deploymentId && (
            <>
              {" · "}
              <Link to={`/deployments/${r.deploymentId}`} className="hover:text-primary">
                {t("selfservice.viewDeployment")}
              </Link>
            </>
          )}
        </p>
      </div>
      {r.state === "pending" && handle && (
        <div className="flex gap-2">
          <Button size="sm" variant="secondary" icon={<X className="size-4" />} onClick={() => { setNote(""); setDeciding({ r, approve: false }); }}>
            {t("selfservice.reject")}
          </Button>
          <Button size="sm" icon={<Check className="size-4" />} onClick={() => { setNote(""); setDeciding({ r, approve: true }); }}>
            {t("selfservice.approve")}
          </Button>
        </div>
      )}
    </li>
  );
  return (
    <div className="space-y-6">
      {error && <Alert tone="danger">{error}</Alert>}
      <Card title={t("selfservice.pendingTitle", { count: pending.length })} description={t("selfservice.pendingHint")}>
        {pending.length === 0 ? (
          <Empty icon={<CheckCircle2 className="size-6" />} title={t("selfservice.noPending")} />
        ) : (
          <ul className="-mx-5 -my-5 divide-y divide-line">{pending.map(row)}</ul>
        )}
      </Card>
      <Card title={t("selfservice.historyTitle")}>
        {done.length === 0 ? (
          <Empty icon={<Inbox className="size-6" />} title={t("selfservice.noHistory")} text={t("selfservice.noHistoryText")} />
        ) : (
          <ul className="-mx-5 -my-5 divide-y divide-line">{done.map(row)}</ul>
        )}
      </Card>
      <Modal
        open={!!deciding}
        onClose={() => setDeciding(null)}
        title={deciding ? t(deciding.approve ? "selfservice.approveTitle" : "selfservice.rejectTitle", { name: deciding.r.name }) : ""}
        footer={
          <>
            <Button variant="secondary" onClick={() => setDeciding(null)}>
              {t("common.cancel")}
            </Button>
            <Button variant={deciding?.approve ? "primary" : "danger"} loading={busy} onClick={decide}>
              {deciding?.approve ? t("selfservice.approve") : t("selfservice.reject")}
            </Button>
          </>
        }
      >
        {deciding && (
          <>
            <p className="text-sm">
              {t(deciding.approve ? "selfservice.approveText" : "selfservice.rejectText", {
                name: deciding.r.name,
                user: deciding.r.username || t("selfservice.unknownUser"),
                device: deciding.r.hostname,
              })}
            </p>
            <Field label={t("selfservice.note")} hint={t("selfservice.noteHint")}>
              {(id) => <Input id={id} value={note} maxLength={500} onChange={(e) => setNote(e.target.value)} />}
            </Field>
          </>
        )}
      </Modal>
    </div>
  );
}

// ---- help requests ----

const helpTone: Record<HelpState, "neutral" | "primary" | "warning"> = { open: "warning", in_progress: "primary", closed: "neutral" };

function HelpRequests() {
  const { t, i18n } = useTranslation();
  const { can } = useAuth();
  const errorText = useErrorText();
  const [filter, setFilter] = useState<"active" | "">("active");
  const [list, setList] = useState<HelpRequest[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [open, setOpen] = useState<HelpRequest | null>(null);

  const load = useCallback(() => {
    get<{ items: HelpRequest[] }>(`/selfservice/help${filter ? `?state=${filter}` : ""}`)
      .then((r) => {
        setList(r.items);
        setError(null);
      })
      .catch((e) => setError(errorText(e)));
  }, [filter]);
  useEffect(() => {
    setList(null);
    load();
    const id = setInterval(load, 30_000);
    return () => clearInterval(id);
  }, [load]);

  return (
    <div className="space-y-4">
      {error && <Alert tone="danger">{error}</Alert>}
      <Card
        title={t("selfservice.helpTitle")}
        description={t("selfservice.helpHint")}
        actions={
          <Select value={filter} onChange={(e) => setFilter(e.target.value as "active" | "")} aria-label={t("selfservice.filter")}>
            <option value="active">{t("selfservice.helpActive")}</option>
            <option value="">{t("selfservice.helpAll")}</option>
          </Select>
        }
      >
        {!list ? (
          <Spinner />
        ) : list.length === 0 ? (
          <Empty icon={<LifeBuoy className="size-6" />} title={t("selfservice.noHelp")} text={t("selfservice.noHelpText")} />
        ) : (
          <ul className="-mx-5 -my-5 divide-y divide-line">
            {list.map((h) => (
              <li key={h.id}>
                <button type="button" onClick={() => setOpen(h)} className="flex w-full flex-wrap items-start gap-4 px-5 py-4 text-left hover:bg-surface-muted/60">
                  <span className={`grid size-9 shrink-0 place-items-center rounded-md ${h.state === "open" ? "bg-warning/10 text-warning" : "bg-primary/10 text-primary"}`}>
                    <MessageSquare className="size-4" />
                  </span>
                  <div className="min-w-0 flex-1 space-y-1">
                    <div className="flex flex-wrap items-center gap-2">
                      <span className="text-sm text-muted">#{h.id}</span>
                      <span className="font-medium">{h.subject}</span>
                      <Badge tone={helpTone[h.state]}>{t(`selfservice.helpStates.${h.state}`)}</Badge>
                    </div>
                    {h.message && <p className="line-clamp-2 text-sm text-muted">{h.message}</p>}
                    <p className="text-xs text-muted">
                      {[h.username || t("selfservice.unknownUser"), `${h.hostname} (${h.groupName})`, timeAgo(h.createdAt, i18n.language)].join(" · ")}
                      {h.assignedTo && ` · ${t("selfservice.assignedTo", { user: h.assignedTo })}`}
                    </p>
                  </div>
                </button>
              </li>
            ))}
          </ul>
        )}
      </Card>
      {open && (
        <HelpDialog
          help={open}
          canHandle={can("selfservice.handle")}
          canRemote={can("remote.control")}
          onClose={() => setOpen(null)}
          onSaved={() => {
            setOpen(null);
            notifyCounts();
            load();
          }}
        />
      )}
    </div>
  );
}

function HelpDialog({ help, canHandle, canRemote, onClose, onSaved }: { help: HelpRequest; canHandle: boolean; canRemote: boolean; onClose: () => void; onSaved: () => void }) {
  const { t, i18n } = useTranslation();
  const errorText = useErrorText();
  const [state, setState] = useState<HelpState>(help.state === "open" && canHandle ? "in_progress" : help.state);
  const [reply, setReply] = useState(help.reply);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const save = async () => {
    setBusy(true);
    try {
      await put(`/selfservice/help/${help.id}`, { state, assignedTo: help.assignedTo, reply });
      onSaved();
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
      title={`#${help.id} ${help.subject}`}
      footer={
        canHandle ? (
          <>
            <Button variant="secondary" onClick={onClose}>
              {t("common.cancel")}
            </Button>
            <Button loading={busy} onClick={save}>
              {t("common.save")}
            </Button>
          </>
        ) : undefined
      }
    >
      {error && <Alert tone="danger">{error}</Alert>}
      <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1.5 text-sm">
        <dt className="text-muted">{t("selfservice.user")}</dt>
        <dd>{help.username || t("selfservice.unknownUser")}</dd>
        <dt className="text-muted">{t("selfservice.device")}</dt>
        <dd className="flex flex-wrap items-center gap-2">
          <Link to={`/devices/${help.deviceId}`} className="font-medium hover:text-primary">
            {help.hostname}
          </Link>
          <span className="text-muted">({help.groupName})</span>
          <Badge tone={help.online ? "success" : "neutral"}>{help.online ? t("selfservice.online") : t("selfservice.offline")}</Badge>
        </dd>
        <dt className="text-muted">{t("selfservice.contact")}</dt>
        <dd>{help.contact || "–"}</dd>
        <dt className="text-muted">{t("selfservice.sent")}</dt>
        <dd>{formatDate(help.createdAt, i18n.language)}</dd>
        {help.assignedTo && (
          <>
            <dt className="text-muted">{t("selfservice.assigned")}</dt>
            <dd>{help.assignedTo}</dd>
          </>
        )}
      </dl>
      <div className="rounded-md bg-surface-muted px-4 py-3 text-sm whitespace-pre-wrap">{help.message || t("selfservice.noMessage")}</div>
      {canRemote && help.online && (
        <a href={`/remote/connect/${help.deviceId}`} target="_blank" rel="noreferrer" className="inline-flex items-center gap-2 text-sm font-medium text-primary hover:underline">
          <MonitorPlay className="size-4" />
          {t("selfservice.remote")}
        </a>
      )}
      {canHandle ? (
        <>
          <Field label={t("selfservice.state")}>
            {(id) => (
              <Select id={id} value={state} onChange={(e) => setState(e.target.value as HelpState)}>
                {(["open", "in_progress", "closed"] as HelpState[]).map((s) => (
                  <option key={s} value={s}>
                    {t(`selfservice.helpStates.${s}`)}
                  </option>
                ))}
              </Select>
            )}
          </Field>
          <Field label={t("selfservice.reply")} hint={t("selfservice.replyHint")}>
            {(id) => (
              <textarea
                id={id}
                value={reply}
                maxLength={2000}
                rows={4}
                onChange={(e) => setReply(e.target.value)}
                className="w-full rounded-md border border-line bg-surface px-3 py-2 text-sm outline-none focus-visible:ring-2 focus-visible:ring-primary"
              />
            )}
          </Field>
        </>
      ) : (
        help.reply && <p className="text-sm">{help.reply}</p>
      )}
    </Modal>
  );
}

// ---- catalog ----

type Draft = { id?: number; packageId: number; displayName: string; description: string; category: string; groupIds: number[]; approval: boolean; enabled: boolean };

function Catalog() {
  const { t } = useTranslation();
  const { can } = useAuth();
  const errorText = useErrorText();
  const manage = can("selfservice.manage");
  const [items, setItems] = useState<SelfServiceItem[] | null>(null);
  const [packages, setPackages] = useState<SoftwarePackage[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [draft, setDraft] = useState<Draft | null>(null);
  const [removing, setRemoving] = useState<SelfServiceItem | null>(null);
  const [busy, setBusy] = useState(false);

  const load = useCallback(() => {
    get<{ items: SelfServiceItem[] }>("/selfservice/items")
      .then((r) => setItems(r.items))
      .catch((e) => setError(errorText(e)));
  }, []);
  useEffect(() => {
    load();
    get<PackagesView>("/software/packages")
      .then((v) => setPackages(v.packages))
      .catch(() => {});
  }, [load]);

  const available = useMemo(() => packages.filter((p) => !(items ?? []).some((i) => i.packageId === p.id)), [packages, items]);

  const remove = async () => {
    if (!removing) return;
    setBusy(true);
    try {
      await del(`/selfservice/items/${removing.id}`);
      setRemoving(null);
      load();
    } catch (e) {
      setError(errorText(e));
      setRemoving(null);
    } finally {
      setBusy(false);
    }
  };

  if (!items) return error ? <Alert tone="danger">{error}</Alert> : <Spinner />;
  return (
    <div className="space-y-4">
      {error && <Alert tone="danger">{error}</Alert>}
      <Card
        title={t("selfservice.catalogTitle", { count: items.length })}
        description={t("selfservice.catalogHint")}
        actions={
          manage && (
            <Button
              icon={<Plus className="size-4" />}
              disabled={available.length === 0}
              title={available.length === 0 ? t("selfservice.allAdded") : undefined}
              onClick={() => setDraft({ packageId: available[0]?.id ?? 0, displayName: "", description: "", category: "", groupIds: [], approval: false, enabled: true })}
            >
              {t("selfservice.addItem")}
            </Button>
          )
        }
      >
        {items.length === 0 ? (
          <Empty
            icon={<Store className="size-6" />}
            title={t("selfservice.emptyCatalog")}
            text={packages.length === 0 ? t("selfservice.emptyLibrary") : t("selfservice.emptyCatalogText")}
          />
        ) : (
          <div className="-mx-5 -my-5 overflow-x-auto">
            <table className="w-full min-w-[760px] text-sm">
              <thead className="border-b border-line text-left text-xs text-muted uppercase">
                <tr>
                  <th className="px-5 py-3 font-medium">{t("selfservice.cols.name")}</th>
                  <th className="px-3 py-3 font-medium">{t("selfservice.cols.category")}</th>
                  <th className="px-3 py-3 font-medium">{t("selfservice.cols.visibleTo")}</th>
                  <th className="px-3 py-3 font-medium">{t("selfservice.cols.approval")}</th>
                  <th className="px-3 py-3 text-right font-medium">{t("selfservice.cols.requests")}</th>
                  {manage && <th className="px-5 py-3 text-right font-medium">{t("common.actions")}</th>}
                </tr>
              </thead>
              <tbody className="divide-y divide-line">
                {items.map((i) => (
                  <tr key={i.id} className={i.enabled ? "" : "opacity-60"}>
                    <td className="px-5 py-3">
                      <div className="flex flex-wrap items-center gap-2 font-medium">
                        {i.displayName || i.packageName}
                        {!i.enabled && <Badge>{t("selfservice.hidden")}</Badge>}
                      </div>
                      <div className="mt-1 flex flex-wrap items-center gap-2 text-xs text-muted">
                        <SourceBadge source={i.source} />
                        <PlatformBadge platform={i.platform} />
                        {[i.publisher, i.version, i.displayName ? i.packageName : ""].filter(Boolean).join(" · ")}
                      </div>
                    </td>
                    <td className="px-3 py-3">{i.category || <span className="text-muted">–</span>}</td>
                    <td className="px-3 py-3">{i.groups.length === 0 ? t("selfservice.allGroups") : i.groups.map((g) => g.name).join(", ")}</td>
                    <td className="px-3 py-3">
                      {i.approval ? <Badge tone="warning">{t("selfservice.needsApproval")}</Badge> : <Badge tone="success">{t("selfservice.direct")}</Badge>}
                    </td>
                    <td className="px-3 py-3 text-right tabular-nums" title={t("selfservice.requestsHint")}>
                      {i.requests} / {i.installs}
                    </td>
                    {manage && (
                      <td className="px-5 py-3 text-right whitespace-nowrap">
                        <Button
                          size="sm"
                          variant="ghost"
                          icon={<Pencil className="size-4" />}
                          aria-label={t("common.edit")}
                          onClick={() =>
                            setDraft({
                              id: i.id,
                              packageId: i.packageId,
                              displayName: i.displayName,
                              description: i.description,
                              category: i.category,
                              groupIds: i.groupIds,
                              approval: i.approval,
                              enabled: i.enabled,
                            })
                          }
                        />
                        <Button size="sm" variant="ghost" icon={<Trash2 className="size-4" />} aria-label={t("common.delete")} onClick={() => setRemoving(i)} />
                      </td>
                    )}
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Card>
      {draft && (
        <ItemDialog
          draft={draft}
          packages={draft.id ? packages : available}
          categories={[...new Set(items.map((i) => i.category).filter(Boolean))]}
          onClose={() => setDraft(null)}
          onSaved={() => {
            setDraft(null);
            load();
          }}
        />
      )}
      <Modal
        open={!!removing}
        onClose={() => setRemoving(null)}
        title={t("selfservice.removeTitle")}
        footer={
          <>
            <Button variant="secondary" onClick={() => setRemoving(null)}>
              {t("common.cancel")}
            </Button>
            <Button variant="danger" loading={busy} onClick={remove}>
              {t("selfservice.remove")}
            </Button>
          </>
        }
      >
        <p className="text-sm">{t("selfservice.removeText", { name: removing?.displayName || removing?.packageName })}</p>
      </Modal>
    </div>
  );
}

function ItemDialog({ draft, packages, categories, onClose, onSaved }: { draft: Draft; packages: SoftwarePackage[]; categories: string[]; onClose: () => void; onSaved: () => void }) {
  const { t } = useTranslation();
  const errorText = useErrorText();
  const groups = useGroups();
  const [d, setD] = useState<Draft>(draft);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const pkg = packages.find((p) => p.id === d.packageId);
  const save = async () => {
    setBusy(true);
    try {
      if (d.id) await put(`/selfservice/items/${d.id}`, d);
      else await post("/selfservice/items", d);
      onSaved();
    } catch (e) {
      setError(errorText(e));
    } finally {
      setBusy(false);
    }
  };
  const toggleGroup = (id: number) => setD({ ...d, groupIds: d.groupIds.includes(id) ? d.groupIds.filter((g) => g !== id) : [...d.groupIds, id] });
  return (
    <Modal
      open
      wide
      onClose={onClose}
      title={d.id ? t("selfservice.editItem") : t("selfservice.addItem")}
      footer={
        <>
          <Button variant="secondary" onClick={onClose}>
            {t("common.cancel")}
          </Button>
          <Button loading={busy} disabled={!d.packageId} onClick={save}>
            {t("common.save")}
          </Button>
        </>
      }
    >
      {error && <Alert tone="danger">{error}</Alert>}
      <Field label={t("selfservice.package")} hint={t("selfservice.packageHint")}>
        {(id) => (
          <Select id={id} value={d.packageId} disabled={!!d.id} onChange={(e) => setD({ ...d, packageId: Number(e.target.value) })}>
            {packages.map((p) => (
              <option key={p.id} value={p.id}>
                {p.name} ({p.platform === "windows" ? "Windows" : "Linux"})
              </option>
            ))}
          </Select>
        )}
      </Field>
      <div className="grid gap-4 sm:grid-cols-2">
        <Field label={t("selfservice.displayName")} hint={t("selfservice.displayNameHint")}>
          {(id) => <Input id={id} value={d.displayName} maxLength={120} placeholder={pkg?.name} onChange={(e) => setD({ ...d, displayName: e.target.value })} />}
        </Field>
        <Field label={t("selfservice.category")} hint={t("selfservice.categoryHint")}>
          {(id) => (
            <>
              <Input id={id} value={d.category} maxLength={60} list="ss-categories" onChange={(e) => setD({ ...d, category: e.target.value })} />
              <datalist id="ss-categories">
                {categories.map((c) => (
                  <option key={c} value={c} />
                ))}
              </datalist>
            </>
          )}
        </Field>
      </div>
      <Field label={t("selfservice.description")} hint={t("selfservice.descriptionHint")}>
        {(id) => (
          <textarea
            id={id}
            value={d.description}
            maxLength={1000}
            rows={3}
            placeholder={pkg?.description}
            onChange={(e) => setD({ ...d, description: e.target.value })}
            className="w-full rounded-md border border-line bg-surface px-3 py-2 text-sm outline-none focus-visible:ring-2 focus-visible:ring-primary"
          />
        )}
      </Field>
      <Switch checked={d.approval} onChange={(v) => setD({ ...d, approval: v })} label={t("selfservice.approvalSwitch")} description={t("selfservice.approvalHint")} />
      <Switch checked={d.enabled} onChange={(v) => setD({ ...d, enabled: v })} label={t("selfservice.enabledSwitch")} description={t("selfservice.enabledHint")} />
      <div className="space-y-2">
        <p className="text-sm font-medium">{t("selfservice.visibleTo")}</p>
        <p className="text-xs text-muted">{t("selfservice.visibleToHint")}</p>
        <div className="grid max-h-48 gap-1 overflow-y-auto rounded-md border border-line p-2 sm:grid-cols-2">
          {(groups ?? []).map((g) => (
            <label key={g.id} className="flex cursor-pointer items-center gap-2 rounded-md px-2 py-1.5 text-sm hover:bg-surface-muted">
              <input type="checkbox" checked={d.groupIds.includes(g.id)} onChange={() => toggleGroup(g.id)} className="accent-primary" />
              {g.name}
            </label>
          ))}
        </div>
      </div>
    </Modal>
  );
}

// ---- settings ----

function SettingsTab() {
  const { t } = useTranslation();
  const errorText = useErrorText();
  const [s, setS] = useState<SelfServiceSettings | null>(null);
  const [recipients, setRecipients] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [saved, setSaved] = useState(false);
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    get<SelfServiceSettings>("/selfservice/settings")
      .then((v) => {
        setS(v);
        setRecipients(v.recipients.join(", "));
      })
      .catch((e) => setError(errorText(e)));
  }, []);
  if (!s) return error ? <Alert tone="danger">{error}</Alert> : <Spinner />;
  const save = async () => {
    setBusy(true);
    setSaved(false);
    try {
      const out = await put<SelfServiceSettings>("/selfservice/settings", {
        ...s,
        recipients: recipients
          .split(/[,;\s]+/)
          .map((r) => r.trim())
          .filter(Boolean),
      });
      setS(out);
      setRecipients(out.recipients.join(", "));
      setSaved(true);
      setError(null);
    } catch (e) {
      setError(errorText(e));
    } finally {
      setBusy(false);
    }
  };
  return (
    <div className="space-y-6">
      {error && <Alert tone="danger">{error}</Alert>}
      {saved && <Alert tone="success">{t("common.saved")}</Alert>}
      <Card title={t("selfservice.settings.trayTitle")} description={t("selfservice.settings.trayHint")}>
        <div className="space-y-4">
          <Switch checked={s.software} onChange={(v) => setS({ ...s, software: v })} label={t("selfservice.settings.software")} description={t("selfservice.settings.softwareHint")} />
          <Switch checked={s.updates} onChange={(v) => setS({ ...s, updates: v })} label={t("selfservice.settings.updates")} description={t("selfservice.settings.updatesHint")} />
          <Switch checked={s.help} onChange={(v) => setS({ ...s, help: v })} label={t("selfservice.settings.help")} description={t("selfservice.settings.helpHint")} />
          <Field label={t("selfservice.settings.helpText")} hint={t("selfservice.settings.helpTextHint")}>
            {(id) => (
              <textarea
                id={id}
                value={s.helpText}
                maxLength={500}
                rows={2}
                onChange={(e) => setS({ ...s, helpText: e.target.value })}
                className="w-full rounded-md border border-line bg-surface px-3 py-2 text-sm outline-none focus-visible:ring-2 focus-visible:ring-primary"
              />
            )}
          </Field>
        </div>
      </Card>
      <Card title={t("selfservice.settings.mailTitle")} description={t("selfservice.settings.mailHint")}>
        <div className="grid gap-4 sm:grid-cols-[1fr_200px]">
          <Field label={t("selfservice.settings.recipients")} hint={t("selfservice.settings.recipientsHint")}>
            {(id) => <Input id={id} value={recipients} onChange={(e) => setRecipients(e.target.value)} placeholder="servicedesk@example.com" />}
          </Field>
          <Field label={t("selfservice.settings.language")}>
            {(id) => (
              <Select id={id} value={s.language} onChange={(e) => setS({ ...s, language: e.target.value as "en" | "nl" })}>
                <option value="en">English</option>
                <option value="nl">Nederlands</option>
              </Select>
            )}
          </Field>
        </div>
      </Card>
      <div className="flex justify-end">
        <Button loading={busy} onClick={save}>
          {t("common.save")}
        </Button>
      </div>
    </div>
  );
}
