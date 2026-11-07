import { useCallback, useEffect, useMemo, useState } from "react";
import { Link, useParams } from "react-router";
import { useTranslation } from "react-i18next";
import { ArrowLeft, Ban, CalendarClock, RotateCcw, Rocket, Search } from "lucide-react";
import { get, post, type Deployment, type DeploymentView, type JobState, type SoftwareJob } from "../../api";
import { useAuth } from "../../auth";
import { Alert, Badge, Button, Card, Input, Modal, PageHeader, Select, Spinner, formatDate, useErrorText } from "../../components/ui";
import { PlatformBadge } from "../devices/util";
import { JobStateBadge, Progress, SourceBadge } from "./util";

const finished = (d: Deployment) => !!d.cancelledAt || (d.counts.pending === 0 && d.counts.running === 0 && !d.ongoing);

function ActionBadge({ d }: { d: Deployment }) {
  const { t } = useTranslation();
  return <Badge tone={d.action === "uninstall" ? "danger" : "primary"}>{t(`software.actions.${d.action}`)}</Badge>;
}

function DeploymentState({ d }: { d: Deployment }) {
  const { t, i18n } = useTranslation();
  if (d.cancelledAt) return <Badge>{t("deployments.state.cancelled")}</Badge>;
  if (new Date(d.startAt).getTime() > Date.now())
    return (
      <span className="inline-flex items-center gap-1 text-xs text-muted">
        <CalendarClock className="size-3.5" /> {formatDate(d.startAt, i18n.language)}
      </span>
    );
  if (d.counts.pending > 0 || d.counts.running > 0) return <Badge tone="primary">{t("deployments.state.running")}</Badge>;
  if (d.ongoing) return <Badge tone="primary">{t("deployments.state.ongoing")}</Badge>;
  if (d.counts.failed > 0) return <Badge tone="danger">{t("deployments.state.failed")}</Badge>;
  return <Badge tone="success">{t("deployments.state.done")}</Badge>;
}

export function Deployments() {
  const { t, i18n } = useTranslation();
  const errorText = useErrorText();
  const [list, setList] = useState<Deployment[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const load = useCallback(() => {
    get<Deployment[]>("/deployments?limit=200")
      .then(setList)
      .catch((e) => setError(errorText(e)));
  }, []);
  useEffect(() => {
    load();
    const id = setInterval(load, 10000);
    return () => clearInterval(id);
  }, [load]);

  return (
    <div className="space-y-6">
      <PageHeader
        title={t("deployments.title")}
        description={t("deployments.subtitle")}
        actions={
          <Link to="/software">
            <Button icon={<Rocket className="size-4" />}>{t("deployments.new")}</Button>
          </Link>
        }
      />
      {error && <Alert tone="danger">{error}</Alert>}
      <Card>
        {!list ? (
          <Spinner />
        ) : list.length === 0 ? (
          <div className="flex flex-col items-center gap-3 py-10 text-center">
            <span className="grid size-12 place-items-center rounded-full bg-primary/10 text-primary">
              <Rocket className="size-6" />
            </span>
            <p className="font-medium">{t("deployments.emptyTitle")}</p>
            <p className="max-w-md text-sm text-muted">{t("deployments.emptyText")}</p>
          </div>
        ) : (
          <div className="-mx-5 -my-5 overflow-x-auto">
            <table className="w-full min-w-[820px] text-sm">
              <thead className="border-b border-line text-left text-xs text-muted uppercase">
                <tr>
                  <th className="px-5 py-3 font-medium">{t("deployments.cols.name")}</th>
                  <th className="px-3 py-3 font-medium">{t("deployments.cols.targets")}</th>
                  <th className="px-3 py-3 font-medium">{t("deployments.cols.state")}</th>
                  <th className="px-3 py-3 font-medium">{t("deployments.cols.progress")}</th>
                  <th className="px-5 py-3 font-medium">{t("deployments.cols.created")}</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-line">
                {list.map((d) => (
                  <tr key={d.id} className="hover:bg-surface-muted/50">
                    <td className="px-5 py-3">
                      <Link to={`/deployments/${d.id}`} className="font-medium hover:text-primary">
                        {d.name}
                      </Link>
                      <div className="mt-1 flex items-center gap-2">
                        <ActionBadge d={d} />
                        <PlatformBadge platform={d.platform} />
                      </div>
                    </td>
                    <td className="max-w-[220px] px-3 py-3 text-muted">
                      <div className="truncate">{d.groups.length ? d.groups.map((g) => g.name).join(", ") : t("deployments.devicesOnly")}</div>
                      {d.ongoing && !d.cancelledAt && <div className="text-xs">{t("deployments.ongoing")}</div>}
                    </td>
                    <td className="px-3 py-3">
                      <DeploymentState d={d} />
                    </td>
                    <td className="px-3 py-3">
                      <Progress counts={d.counts} />
                    </td>
                    <td className="px-5 py-3 whitespace-nowrap text-muted">
                      <div>{formatDate(d.createdAt, i18n.language)}</div>
                      <div className="text-xs">{d.requestedBy}</div>
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

const stateFilters: ("" | "running" | JobState)[] = ["", "running", "pending", "completed", "skipped", "failed", "cancelled", "expired"];
const runningStates: JobState[] = ["sent", "downloading", "installing", "uninstalling"];

export function DeploymentDetail() {
  const { id } = useParams();
  const { t, i18n } = useTranslation();
  const errorText = useErrorText();
  const { can } = useAuth();
  const deploy = can("software.deploy");
  const [view, setView] = useState<DeploymentView | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [filter, setFilter] = useState("");
  const [state, setState] = useState<(typeof stateFilters)[number]>("");
  const [job, setJob] = useState<SoftwareJob | null>(null);
  const [confirmCancel, setConfirmCancel] = useState(false);
  const [busy, setBusy] = useState(false);

  const load = useCallback(() => {
    get<DeploymentView>(`/deployments/${id}`)
      .then(setView)
      .catch((e) => setError(errorText(e)));
  }, [id]);
  useEffect(() => {
    load();
    const t = setInterval(load, 5000);
    return () => clearInterval(t);
  }, [load]);

  const jobs = useMemo(() => {
    const f = filter.trim().toLowerCase();
    return (view?.jobs ?? []).filter(
      (j) => (!f || j.hostname.toLowerCase().includes(f)) && (!state || (state === "running" ? runningStates.includes(j.state) : j.state === state)),
    );
  }, [view, filter, state]);

  const act = async (what: "cancel" | "retry") => {
    setBusy(true);
    setError(null);
    try {
      const r = await post<{ jobs: number }>(`/deployments/${id}/${what}`);
      setNotice(what === "retry" ? t("deployments.retried", { count: r?.jobs ?? 0 }) : t("deployments.cancelled"));
      setConfirmCancel(false);
      load();
    } catch (e) {
      setError(errorText(e));
    } finally {
      setBusy(false);
    }
  };

  if (error && !view) return <Alert tone="danger">{error}</Alert>;
  if (!view) return <Spinner />;
  const d = view.deployment;
  const p = view.package;
  const failed = d.counts.failed + view.jobs.filter((j) => j.state === "expired").length;

  return (
    <div className="space-y-6">
      <Link to="/deployments" className="inline-flex items-center gap-1.5 text-sm text-muted hover:text-fg">
        <ArrowLeft className="size-4" /> {t("deployments.title")}
      </Link>
      <div className="flex flex-wrap items-start justify-between gap-4">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight">{d.name}</h1>
          <div className="mt-2 flex flex-wrap items-center gap-2 text-sm text-muted">
            <ActionBadge d={d} />
            <PlatformBadge platform={d.platform} />
            <DeploymentState d={d} />
            <span>
              {t("deployments.by", {
                user: d.requestedBy,
                date: formatDate(d.createdAt, i18n.language),
              })}
            </span>
          </div>
        </div>
        {deploy && (
          <div className="flex gap-2">
            {failed > 0 && !d.cancelledAt && (
              <Button variant="secondary" icon={<RotateCcw className="size-4" />} loading={busy} onClick={() => act("retry")}>
                {t("deployments.retry", { count: failed })}
              </Button>
            )}
            {!finished(d) && (
              <Button variant="danger" icon={<Ban className="size-4" />} onClick={() => setConfirmCancel(true)}>
                {t("deployments.cancel")}
              </Button>
            )}
          </div>
        )}
      </div>
      {error && <Alert tone="danger">{error}</Alert>}
      {notice && <Alert tone="success">{notice}</Alert>}

      <div className="grid items-start gap-6 lg:grid-cols-3">
        <Card title={t("deployments.progress")} className="lg:col-span-2">
          <Progress counts={d.counts} />
          {d.counts.reboot > 0 && <p className="mt-3 text-sm text-warning">{t("deployments.rebootNeeded", { count: d.counts.reboot })}</p>}
        </Card>
        <Card title={t("deployments.details")}>
          <dl className="space-y-2 text-sm">
            {p && (
              <div className="flex justify-between gap-3">
                <dt className="text-muted">{t("deployments.package")}</dt>
                <dd className="flex items-center gap-2 text-right">
                  <SourceBadge source={p.source} /> {p.version || t("software.latest")}
                </dd>
              </div>
            )}
            {d.action === "uninstall" && (
              <>
                {d.uninstallVersion && (
                  <div className="flex justify-between gap-3">
                    <dt className="text-muted">{t("software.deploy.version")}</dt>
                    <dd className="font-mono text-xs">{d.uninstallVersion}</dd>
                  </div>
                )}
                {d.uninstallArgs && (
                  <div className="flex justify-between gap-3">
                    <dt className="text-muted">{t("software.deploy.uninstallArgs")}</dt>
                    <dd className="font-mono text-xs">{d.uninstallArgs}</dd>
                  </div>
                )}
                {d.uninstallPurge && (
                  <div className="flex justify-between gap-3">
                    <dt className="text-muted">{t("software.deploy.purge")}</dt>
                    <dd>✓</dd>
                  </div>
                )}
              </>
            )}
            <div className="flex justify-between gap-3">
              <dt className="text-muted">{t("deployments.cols.targets")}</dt>
              <dd className="text-right">{d.groups.length ? d.groups.map((g) => g.name).join(", ") : t("deployments.devicesOnly")}</dd>
            </div>
            <div className="flex justify-between gap-3">
              <dt className="text-muted">{t("deployments.start")}</dt>
              <dd>{formatDate(d.startAt, i18n.language)}</dd>
            </div>
            <div className="flex justify-between gap-3">
              <dt className="text-muted">{t("software.deploy.expire")}</dt>
              <dd>{t("deployments.days", { count: d.expireDays })}</dd>
            </div>
            {d.ongoing && <p className="text-xs text-muted">{t("software.deploy.ongoingHint")}</p>}
          </dl>
        </Card>
      </div>

      <Card
        title={t("deployments.jobs", { count: view.jobs.length })}
        actions={
          <div className="flex flex-wrap gap-2">
            <div className="relative w-52 max-w-full">
              <Search className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted" />
              <Input
                value={filter}
                onChange={(e) => setFilter(e.target.value)}
                placeholder={t("deployments.findDevice")}
                className="pl-9"
                aria-label={t("deployments.findDevice")}
              />
            </div>
            <div className="w-44">
              <Select value={state} onChange={(e) => setState(e.target.value as typeof state)} aria-label={t("deployments.cols.state")}>
                {stateFilters.map((s) => (
                  <option key={s} value={s}>
                    {s === "" ? t("deployments.allStates") : s === "running" ? t("deployments.state.running") : t(`software.state.${s}`)}
                  </option>
                ))}
              </Select>
            </div>
          </div>
        }
      >
        <JobsTable jobs={jobs} onOpen={setJob} />
      </Card>

      <JobDialog job={job} onClose={() => setJob(null)} />
      <Modal
        open={confirmCancel}
        onClose={() => setConfirmCancel(false)}
        title={t("deployments.cancelTitle")}
        footer={
          <>
            <Button variant="secondary" onClick={() => setConfirmCancel(false)}>
              {t("common.cancel")}
            </Button>
            <Button variant="danger" loading={busy} onClick={() => act("cancel")}>
              {t("deployments.cancel")}
            </Button>
          </>
        }
      >
        <p className="text-sm">{t("deployments.cancelText")}</p>
      </Modal>
    </div>
  );
}

// JobsTable lists jobs; with showDeployment it names the deployment instead of the device.
export function JobsTable({ jobs, onOpen, showDeployment }: { jobs: SoftwareJob[]; onOpen: (j: SoftwareJob) => void; showDeployment?: boolean }) {
  const { t, i18n } = useTranslation();
  if (jobs.length === 0) return <p className="text-sm text-muted">{t("deployments.noJobs")}</p>;
  return (
    <div className="-mx-5 -my-5 max-h-[640px] overflow-auto">
      <table className="w-full min-w-[680px] text-sm">
        <thead className="sticky top-0 border-b border-line bg-surface text-left text-xs text-muted uppercase">
          <tr>
            <th className="px-5 py-3 font-medium">{showDeployment ? t("deployments.cols.name") : t("deployments.cols.device")}</th>
            <th className="px-3 py-3 font-medium">{t("deployments.cols.state")}</th>
            <th className="px-3 py-3 font-medium">{t("deployments.cols.result")}</th>
            <th className="px-5 py-3 font-medium">{t("deployments.cols.updated")}</th>
          </tr>
        </thead>
        <tbody className="divide-y divide-line">
          {jobs.map((j) => (
            <tr key={j.id} className="cursor-pointer hover:bg-surface-muted/50" onClick={() => onOpen(j)}>
              <td className="px-5 py-2">
                {showDeployment ? (
                  <Link to={`/deployments/${j.deploymentId}`} onClick={(e) => e.stopPropagation()} className="hover:text-primary">
                    {j.deploymentName}
                  </Link>
                ) : (
                  <Link to={`/devices/${j.deviceId}`} onClick={(e) => e.stopPropagation()} className="hover:text-primary">
                    {j.hostname}
                  </Link>
                )}
              </td>
              <td className="px-3 py-2">
                <JobStateBadge state={j.state} message={j.message} />
                {j.rebootRequired && (
                  <span className="ml-2">
                    <Badge tone="warning">{t("deployments.reboot")}</Badge>
                  </span>
                )}
              </td>
              <td className="max-w-[320px] truncate px-3 py-2 text-muted" title={j.message}>
                {j.message}
                {j.exitCode !== null && <span className="ml-2 font-mono text-xs">({t("deployments.exitCode", { code: j.exitCode })})</span>}
              </td>
              <td className="px-5 py-2 whitespace-nowrap text-muted">{formatDate(j.finishedAt ?? j.updatedAt, i18n.language)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

export function JobDialog({ job, onClose }: { job: SoftwareJob | null; onClose: () => void }) {
  const { t, i18n } = useTranslation();
  if (!job) return null;
  const rows: [string, React.ReactNode][] = [
    [t("deployments.cols.device"), job.hostname],
    [t("deployments.cols.state"), <JobStateBadge key="s" state={job.state} />],
    [t("deployments.cols.result"), job.message || "—"],
    [t("deployments.exitCodeLabel"), job.exitCode ?? "—"],
    [t("deployments.version"), job.version || "—"],
    [t("deployments.attempts"), job.attempts],
    [t("deployments.sentAt"), job.sentAt ? formatDate(job.sentAt, i18n.language) : "—"],
    [t("deployments.finishedAt"), job.finishedAt ? formatDate(job.finishedAt, i18n.language) : "—"],
  ];
  return (
    <Modal open wide onClose={onClose} title={job.deploymentName}>
      <div className="space-y-4">
        <dl className="grid gap-x-6 gap-y-2 text-sm sm:grid-cols-2">
          {rows.map(([k, v]) => (
            <div key={k} className="flex justify-between gap-3">
              <dt className="text-muted">{k}</dt>
              <dd className="text-right">{v}</dd>
            </div>
          ))}
        </dl>
        {job.rebootRequired && <Alert tone="warning">{t("deployments.rebootText")}</Alert>}
        <div>
          <div className="mb-1 text-sm font-medium">{t("deployments.output")}</div>
          {job.output ? (
            <pre className="max-h-80 overflow-auto rounded-md border border-line bg-surface-muted p-3 font-mono text-xs whitespace-pre-wrap">{job.output}</pre>
          ) : (
            <p className="text-sm text-muted">{t("deployments.noOutput")}</p>
          )}
        </div>
      </div>
    </Modal>
  );
}
