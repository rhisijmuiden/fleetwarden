import { useCallback, useEffect, useMemo, useState } from "react";
import { Link } from "react-router";
import { useTranslation } from "react-i18next";
import { Globe, RefreshCw, Search } from "lucide-react";
import { get, type BrowserKey, type BrowserReport } from "../../api";
import { useAuth } from "../../auth";
import { Alert, Badge, Button, Card, Input, Spinner, Switch, useErrorText } from "../../components/ui";
import { StatusDot } from "../devices/util";
import { DeployDialog, type DeployTarget } from "./DeployDialog";

const keys: BrowserKey[] = ["chrome", "edge", "firefox"];

// Browsers shows the Chrome, Edge and Firefox versions per device against
// the newest version in the winget catalog, and starts browser updates.
export function Browsers() {
  const { t } = useTranslation();
  const errorText = useErrorText();
  const { can } = useAuth();
  const deploy = can("software.deploy");
  const [rep, setRep] = useState<BrowserReport | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [filter, setFilter] = useState("");
  const [onlyOutdated, setOnlyOutdated] = useState(false);
  const [target, setTarget] = useState<DeployTarget | null>(null);

  const load = useCallback(() => {
    get<BrowserReport>("/software/browsers")
      .then((r) => {
        setRep(r);
        setError(null);
      })
      .catch((e) => setError(errorText(e)));
  }, []);
  useEffect(load, [load]);

  const shown = useMemo(() => {
    const f = filter.trim().toLowerCase();
    return (rep?.devices ?? []).filter((d) => (!onlyOutdated || d.outdated) && (!f || d.hostname.toLowerCase().includes(f) || d.groupName.toLowerCase().includes(f)));
  }, [rep, filter, onlyOutdated]);

  if (error && !rep) return <Alert tone="danger">{error}</Alert>;
  if (!rep) return <Spinner />;
  const outdated = rep.devices.filter((d) => d.outdated);
  const outdatedKeys = keys.filter((k) => rep.devices.some((d) => d.browsers[k] && !d.browsers[k]!.upToDate));

  const update = () =>
    setTarget({
      action: "browsers",
      name: "",
      platform: "windows",
      browsers: outdatedKeys.length ? outdatedKeys : keys,
      deviceIds: outdated.map((d) => d.deviceId),
    });

  return (
    <div className="space-y-6">
      {rep.catalog.packages === 0 && <Alert tone="warning">{t("software.browsers.noCatalog")}</Alert>}
      <div className="grid gap-4 md:grid-cols-3">
        {rep.browsers.map((b) => {
          const pct = b.devices ? Math.round((b.upToDate / b.devices) * 100) : 0;
          return (
            <div key={b.key} className="rounded-lg border border-line bg-surface p-4">
              <div className="flex items-center gap-2">
                <span className="grid size-8 place-items-center rounded-md bg-primary/10 text-primary">
                  <Globe className="size-4" />
                </span>
                <div className="min-w-0">
                  <div className="font-medium">{b.name}</div>
                  <div className="text-xs text-muted">
                    {b.latest ? t("software.browsers.latest", { version: b.latest }) : t("software.browsers.unknownLatest")}
                    {b.esr && ` · ESR ${b.esr}`}
                  </div>
                </div>
              </div>
              <div className="mt-4 flex items-baseline justify-between text-sm">
                <span className="text-2xl font-semibold tabular-nums">{b.devices ? `${pct}%` : "—"}</span>
                <span className="text-muted">{t("software.browsers.upToDate", { count: b.upToDate, total: b.devices })}</span>
              </div>
              <div className="mt-2 h-2 overflow-hidden rounded-full bg-surface-muted">
                <div className={pct === 100 ? "h-full bg-success" : "h-full bg-warning"} style={{ width: `${pct}%` }} />
              </div>
            </div>
          );
        })}
      </div>

      <Card
        title={t("software.browsers.devices", { count: rep.devices.length })}
        description={outdated.length ? t("software.browsers.outdatedCount", { count: outdated.length }) : t("software.browsers.allUpToDate")}
        actions={
          <div className="flex flex-wrap items-center gap-2">
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
            <Button variant="secondary" icon={<RefreshCw className="size-4" />} onClick={load} aria-label={t("common.refresh")} title={t("common.refresh")} />
            {deploy && (
              <Button icon={<RefreshCw className="size-4" />} onClick={update} disabled={rep.catalog.packages === 0}>
                {t("software.browsers.update")}
              </Button>
            )}
          </div>
        }
      >
        <div className="mb-4">
          <Switch checked={onlyOutdated} onChange={setOnlyOutdated} label={t("software.browsers.onlyOutdated")} />
        </div>
        {shown.length === 0 ? (
          <p className="text-sm text-muted">{t("software.browsers.none")}</p>
        ) : (
          <div className="-mx-5 -mb-5 max-h-[640px] overflow-auto border-t border-line">
            <table className="w-full min-w-[720px] text-sm">
              <thead className="sticky top-0 border-b border-line bg-surface text-left text-xs text-muted uppercase">
                <tr>
                  <th className="px-5 py-3 font-medium">{t("deployments.cols.device")}</th>
                  {rep.browsers.map((b) => (
                    <th key={b.key} className="px-3 py-3 font-medium">
                      {b.name}
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody className="divide-y divide-line">
                {shown.map((d) => (
                  <tr key={d.deviceId}>
                    <td className="px-5 py-2">
                      <div className="flex items-center gap-2">
                        <StatusDot online={d.online} />
                        <Link to={`/devices/${d.deviceId}`} className="font-medium hover:text-primary">
                          {d.hostname}
                        </Link>
                      </div>
                      <div className="text-xs text-muted">{d.groupName}</div>
                    </td>
                    {keys.map((k) => {
                      const v = d.browsers[k];
                      return (
                        <td key={k} className="px-3 py-2">
                          {!v ? (
                            <span className="text-muted">—</span>
                          ) : (
                            <span title={v.upToDate ? t("software.browsers.current") : t("software.browsers.outdated")}>
                              <Badge tone={v.upToDate ? "success" : "warning"}>
                                <span className="font-mono">{v.version}</span>
                                {v.edition && <span className="ml-1 font-semibold">{v.edition}</span>}
                              </Badge>
                            </span>
                          )}
                        </td>
                      );
                    })}
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Card>
      <DeployDialog target={target} onClose={() => setTarget(null)} />
    </div>
  );
}
