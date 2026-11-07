import { useCallback, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { CalendarClock, DatabaseBackup, Layers, LockKeyhole, Play } from "lucide-react";
import { get, post, type BackupConfig, type BackupLocation, type BackupProgress, type BackupRun, type RestoreView } from "../../../api";
import { Alert, Badge, Button, Card, PageHeader, Spinner, formatDate, useErrorText } from "../../../components/ui";
import { History } from "./History";
import { Locations, RunBadge } from "./Locations";
import { Restore } from "./Restore";
import { Passphrase, Schedule } from "./Schedule";
import { formatBytes } from "./util";

const tabs = ["overview", "locations", "schedule", "restore", "history"] as const;
type Tab = (typeof tabs)[number];

function initialTab(): Tab {
  const h = window.location.hash.slice(1) as Tab;
  return tabs.includes(h) ? h : "overview";
}

export function Backup() {
  const { t } = useTranslation();
  const errorText = useErrorText();
  const [tab, setTabState] = useState<Tab>(initialTab);
  const [config, setConfig] = useState<BackupConfig | null>(null);
  const [locations, setLocations] = useState<BackupLocation[] | null>(null);
  const [runs, setRuns] = useState<BackupRun[] | null>(null);
  const [progress, setProgress] = useState<BackupProgress | null>(null);
  const [restore, setRestore] = useState<RestoreView | null>(null);
  const [error, setError] = useState<string | null>(null);

  const setTab = (next: Tab) => {
    setTabState(next);
    history.replaceState(null, "", `#${next}`);
  };
  const loadLocations = useCallback(() => get<BackupLocation[]>("/backup/locations").then(setLocations), []);
  const loadRuns = useCallback(() => get<BackupRun[]>("/backup/runs").then(setRuns), []);
  useEffect(() => {
    Promise.all([
      get<BackupConfig>("/backup/config").then(setConfig),
      loadLocations(),
      loadRuns(),
      get<BackupProgress>("/backup/progress").then(setProgress),
      get<RestoreView>("/backup/restore").then(setRestore),
    ]).catch((e) => setError(errorText(e)));
  }, []);

  // Follow a running backup; refresh the lists when it ends.
  useEffect(() => {
    if (!progress?.running) return;
    const id = setInterval(() => {
      get<BackupProgress>("/backup/progress")
        .then((p) => {
          setProgress(p);
          if (!p.running) {
            void loadLocations();
            void loadRuns();
          }
        })
        .catch(() => undefined);
    }, 1500);
    return () => clearInterval(id);
  }, [progress?.running]);

  const run = async (kind: "auto" | "full", locationId?: number) => {
    setError(null);
    try {
      setProgress(await post<BackupProgress>("/backup/run", { kind, locationId: locationId ?? 0 }));
      setTab("overview");
    } catch (e) {
      setError(errorText(e));
    }
  };

  if (!config || !locations) return error ? <Alert tone="danger">{error}</Alert> : <Spinner />;
  const running = !!progress?.running;

  return (
    <div className="space-y-6">
      <PageHeader
        title={t("backup.title")}
        description={t("backup.subtitle")}
        actions={
          <>
            <Button variant="secondary" icon={<Layers className="size-4" />} disabled={running || !config.passphraseSet} onClick={() => void run("full")}>
              {t("backup.runFull")}
            </Button>
            <Button icon={<Play className="size-4" />} loading={running} disabled={!config.passphraseSet} onClick={() => void run("auto")}>
              {t("backup.runNow")}
            </Button>
          </>
        }
      />
      <div role="tablist" aria-label={t("backup.title")} className="flex gap-1 overflow-x-auto border-b border-line">
        {tabs.map((k) => (
          <button
            key={k}
            role="tab"
            type="button"
            aria-selected={tab === k}
            onClick={() => setTab(k)}
            className={`-mb-px border-b-2 px-3 py-2 text-sm font-medium whitespace-nowrap transition-colors ${
              tab === k ? "border-primary text-fg" : "border-transparent text-muted hover:text-fg"
            }`}
          >
            {t(`backup.tabs.${k}`)}
          </button>
        ))}
      </div>
      {error && <Alert tone="danger">{error}</Alert>}

      {tab === "overview" && (
        <Overview config={config} locations={locations} runs={runs} progress={progress} onGo={setTab} />
      )}
      {tab === "locations" && <Locations locations={locations} onChanged={() => void loadLocations()} onRun={(id) => void run("auto", id)} running={running} />}
      {tab === "schedule" && (
        <div className="space-y-6">
          <Passphrase config={config} onSaved={setConfig} />
          <Schedule config={config} onSaved={setConfig} />
        </div>
      )}
      {tab === "restore" && <Restore locations={locations} restore={restore} onRestoreChange={setRestore} />}
      {tab === "history" && <History runs={runs} onRefresh={() => void loadRuns()} />}
    </div>
  );
}

function Overview({ config, locations, runs, progress, onGo }: { config: BackupConfig; locations: BackupLocation[]; runs: BackupRun[] | null; progress: BackupProgress | null; onGo: (t: Tab) => void }) {
  const { t, i18n } = useTranslation();
  const lastOk = runs?.find((r) => r.status === "succeeded");
  const enabled = locations.filter((l) => l.enabled);
  return (
    <div className="space-y-6">
      {!config.passphraseSet && (
        <Alert tone="warning" title={t("backup.setup.passTitle")}>
          <p>{t("backup.setup.passBody")}</p>
          <Button size="sm" className="mt-2" icon={<LockKeyhole className="size-4" />} onClick={() => onGo("schedule")}>
            {t("backup.pass.set")}
          </Button>
        </Alert>
      )}
      {config.passphraseSet && enabled.length === 0 && (
        <Alert tone="warning" title={t("backup.setup.locTitle")}>
          <p>{t("backup.setup.locBody")}</p>
          <Button size="sm" className="mt-2" onClick={() => onGo("locations")}>
            {t("backup.addLocation")}
          </Button>
        </Alert>
      )}
      {progress?.running && <ProgressCard progress={progress} />}
      <div className="grid gap-4 md:grid-cols-3">
        <Stat icon={<DatabaseBackup className="size-5" />} label={t("backup.stat.last")} value={lastOk ? formatDate(lastOk.startedAt, i18n.language) : t("backup.neverRun")} sub={lastOk ? `${lastOk.locationName} · ${t(`backup.kinds.${lastOk.kind || "full"}`)}` : undefined} />
        <Stat
          icon={<CalendarClock className="size-5" />}
          label={t("backup.stat.next")}
          value={config.schedule.enabled && config.nextRun ? formatDate(config.nextRun, i18n.language) : t("backup.stat.notScheduled")}
          sub={config.schedule.enabled ? (config.schedule.intervalHours === 24 ? t("backup.sched.daily") : t("backup.sched.hours", { count: config.schedule.intervalHours })) : undefined}
          action={!config.schedule.enabled ? () => onGo("schedule") : undefined}
          actionLabel={t("backup.stat.setSchedule")}
        />
        <Stat icon={<Layers className="size-5" />} label={t("backup.stat.locations")} value={String(enabled.length)} sub={t("backup.stat.retention", { count: config.schedule.keepFull })} />
      </div>
      {!progress?.running && progress?.phase === "done" && progress.locations && (
        <Card title={t("backup.lastResult")}>
          <ul className="space-y-2 text-sm">
            {Object.entries(progress.locations).map(([id, l]) => (
              <li key={id} className="flex flex-wrap items-center gap-2">
                <RunBadge status={l.state === "succeeded" ? "succeeded" : l.state === "failed" ? "failed" : "running"} />
                <span className="font-medium">{l.name}</span>
                {l.kind && <Badge>{t(`backup.kinds.${l.kind}`)}</Badge>}
                {l.error && <span className="text-danger">{l.error}</span>}
              </li>
            ))}
          </ul>
        </Card>
      )}
    </div>
  );
}

function Stat({ icon, label, value, sub, action, actionLabel }: { icon: React.ReactNode; label: string; value: string; sub?: string; action?: () => void; actionLabel?: string }) {
  return (
    <div className="rounded-lg border border-line bg-surface p-5">
      <div className="flex items-center gap-2 text-muted">
        {icon}
        <span className="text-sm">{label}</span>
      </div>
      <p className="mt-2 text-lg font-semibold">{value}</p>
      {sub && <p className="text-xs text-muted">{sub}</p>}
      {action && (
        <button type="button" onClick={action} className="mt-1 text-xs font-medium text-primary hover:underline">
          {actionLabel}
        </button>
      )}
    </div>
  );
}

function ProgressCard({ progress }: { progress: BackupProgress }) {
  const { t, i18n } = useTranslation();
  return (
    <Card title={t("backup.running")} description={progress.phase === "dump" ? t("backup.phase.dump") : t("backup.phase.upload")}>
      <ul className="space-y-4">
        {Object.entries(progress.locations ?? {}).map(([id, l]) => {
          const pct = l.total ? Math.min(100, Math.round((l.done / l.total) * 100)) : 0;
          return (
            <li key={id}>
              <div className="flex flex-wrap items-center justify-between gap-2 text-sm">
                <span className="font-medium">{l.name}</span>
                <span className="text-xs text-muted">
                  {l.state === "running" ? `${formatBytes(l.done, i18n.language)} · ${pct}%` : t(`backup.lstate.${l.state}`)}
                </span>
              </div>
              <div className="mt-1.5 h-2 overflow-hidden rounded-full bg-line">
                <div
                  className={`h-full transition-all ${l.state === "failed" ? "bg-danger" : l.state === "succeeded" ? "bg-success" : "bg-primary"}`}
                  style={{ width: `${l.state === "succeeded" || l.state === "failed" ? 100 : pct}%` }}
                />
              </div>
              {l.error && <p className="mt-1 text-xs text-danger">{l.error}</p>}
            </li>
          );
        })}
      </ul>
    </Card>
  );
}
