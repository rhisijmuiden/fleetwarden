import { useCallback, useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { RefreshCw, Search, Shield, XCircle } from "lucide-react";
import { get, post, type Device } from "../../api";
import { Alert, Badge, Button, Card, Input, Modal, Spinner, Switch, formatDate, useErrorText } from "../../components/ui";
import { formatBytes } from "../devices/util";
import { SortHeader } from "./Manage";
import type { Process } from "./types";

type SortKey = "name" | "pid" | "cpu" | "memory" | "user";

export function ProcessesPanel({ device }: { device: Device }) {
  const { t, i18n } = useTranslation();
  const errorText = useErrorText();
  const [list, setList] = useState<Process[] | null>(null);
  const [at, setAt] = useState<string>("");
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [auto, setAuto] = useState(false);
  const [search, setSearch] = useState("");
  const [sort, setSort] = useState<{ key: SortKey; desc: boolean }>({ key: "cpu", desc: true });
  const [stopping, setStopping] = useState<Process | null>(null);
  const [busy, setBusy] = useState(false);

  const load = useCallback(() => {
    setLoading(true);
    get<{ processes: Process[]; at: string }>(`/devices/${device.id}/manage/processes`)
      .then((r) => {
        setList(r.processes);
        setAt(r.at);
        setError(null);
      })
      .catch((e) => setError(errorText(e)))
      .finally(() => setLoading(false));
  }, [device.id]);
  useEffect(load, [load]);
  useEffect(() => {
    if (!auto) return;
    const id = setInterval(load, 5000);
    return () => clearInterval(id);
  }, [auto, load]);

  const shown = useMemo(() => {
    const q = search.trim().toLowerCase();
    const rows = (list ?? []).filter(
      (p) => !q || p.name.toLowerCase().includes(q) || String(p.pid) === q || (p.user ?? "").toLowerCase().includes(q) || (p.command ?? "").toLowerCase().includes(q),
    );
    const dir = sort.desc ? -1 : 1;
    rows.sort((a, b) => {
      switch (sort.key) {
        case "pid":
          return (a.pid - b.pid) * dir;
        case "cpu":
          return (a.cpu - b.cpu) * dir || b.memory - a.memory;
        case "memory":
          return (a.memory - b.memory) * dir;
        case "user":
          return (a.user ?? "").localeCompare(b.user ?? "") * dir;
      }
      return a.name.localeCompare(b.name) * dir;
    });
    return rows;
  }, [list, search, sort]);

  const totals = useMemo(() => {
    const ps = list ?? [];
    return { cpu: ps.reduce((s, p) => s + p.cpu, 0), memory: ps.reduce((s, p) => s + p.memory, 0) };
  }, [list]);

  const stop = () => {
    if (!stopping) return;
    setBusy(true);
    post(`/devices/${device.id}/manage/processes/${stopping.pid}/kill`, { name: stopping.name })
      .then(() => {
        setNotice(t("manage.processes.stopped", { name: stopping.name }));
        setStopping(null);
        load();
      })
      .catch((e) => {
        setError(errorText(e));
        setStopping(null);
      })
      .finally(() => setBusy(false));
  };

  return (
    <Card
      title={t("manage.sections.processes")}
      description={t("manage.processes.hint")}
      actions={
        <>
          <Switch checked={auto} onChange={setAuto} label={t("manage.autoRefresh")} />
          <Button variant="secondary" size="sm" icon={<RefreshCw className="size-4" />} loading={loading} onClick={load}>
            {t("manage.refresh")}
          </Button>
        </>
      }
    >
      <div className="space-y-4">
        {error && <Alert tone="danger">{error}</Alert>}
        {notice && <Alert tone="success">{notice}</Alert>}
        <div className="flex flex-wrap items-center gap-3">
          <div className="relative min-w-[220px] flex-1">
            <Search className="pointer-events-none absolute top-2.5 left-3 size-4 text-muted" />
            <Input className="pl-9" placeholder={t("manage.processes.search")} value={search} onChange={(e) => setSearch(e.target.value)} />
          </div>
          {list && (
            <p className="text-xs text-muted">
              {t("manage.processes.summary", {
                count: list.length,
                cpu: totals.cpu.toFixed(0),
                memory: formatBytes(totals.memory, i18n.language),
              })}
              {at && ` · ${formatDate(at, i18n.language)}`}
            </p>
          )}
        </div>
        {!list ? (
          error ? null : <Spinner />
        ) : (
          <div className="-mx-5 overflow-x-auto">
            <table className="w-full min-w-[760px] text-sm">
              <thead className="border-y border-line text-left text-xs text-muted uppercase">
                <tr>
                  <SortHeader k="name" sort={sort} setSort={setSort} className="px-5">
                    {t("manage.processes.name")}
                  </SortHeader>
                  <SortHeader k="pid" sort={sort} setSort={setSort} className="px-3">
                    PID
                  </SortHeader>
                  <SortHeader k="user" sort={sort} setSort={setSort} className="px-3">
                    {t("manage.processes.user")}
                  </SortHeader>
                  <SortHeader k="cpu" sort={sort} setSort={setSort} className="px-3 text-right">
                    CPU
                  </SortHeader>
                  <SortHeader k="memory" sort={sort} setSort={setSort} className="px-3 text-right">
                    {t("manage.processes.memory")}
                  </SortHeader>
                  <th className="px-3 py-3 font-medium">{t("manage.processes.started")}</th>
                  <th className="px-5 py-3">
                    <span className="sr-only">{t("common.actions")}</span>
                  </th>
                </tr>
              </thead>
              <tbody className="divide-y divide-line">
                {shown.slice(0, 500).map((p) => (
                  <tr key={p.pid} className="hover:bg-surface-muted/50">
                    <td className="max-w-[300px] px-5 py-2">
                      <div className="flex items-center gap-2 font-medium">
                        <span className="truncate" title={p.path || p.name}>
                          {p.name}
                        </span>
                        {p.agent && <Badge tone="primary">{t("manage.processes.agent")}</Badge>}
                      </div>
                      {p.command && (
                        <div className="truncate font-mono text-xs text-muted" title={p.command}>
                          {p.command}
                        </div>
                      )}
                    </td>
                    <td className="px-3 py-2 font-mono text-xs text-muted">{p.pid}</td>
                    <td className="max-w-[180px] truncate px-3 py-2 text-xs text-muted" title={p.user}>
                      {p.user || "–"}
                      {device.platform === "windows" && p.session > 0 && <span className="ml-1">({t("manage.processes.session", { n: p.session })})</span>}
                    </td>
                    <td className="px-3 py-2 text-right whitespace-nowrap tabular-nums">
                      <span className={p.cpu >= 50 ? "font-semibold text-danger" : p.cpu >= 10 ? "font-medium text-warning" : ""}>{p.cpu.toFixed(1)}%</span>
                    </td>
                    <td className="px-3 py-2 text-right whitespace-nowrap tabular-nums">{p.memory ? formatBytes(p.memory, i18n.language) : "–"}</td>
                    <td className="px-3 py-2 text-xs whitespace-nowrap text-muted">{formatDate(p.startedAt, i18n.language)}</td>
                    <td className="px-5 py-2 text-right">
                      {p.protected ? (
                        <span className="inline-flex items-center gap-1 text-xs text-muted" title={t("manage.processes.protected")}>
                          <Shield className="size-3.5" />
                        </span>
                      ) : (
                        <Button variant="ghost" size="sm" icon={<XCircle className="size-4 text-danger" />} title={t("manage.processes.stop")} onClick={() => setStopping(p)}>
                          <span className="sr-only">{t("manage.processes.stop")}</span>
                        </Button>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
            {shown.length > 500 && <p className="px-5 pt-3 text-xs text-muted">{t("manage.processes.more", { count: shown.length - 500 })}</p>}
            {shown.length === 0 && <p className="px-5 py-6 text-center text-sm text-muted">{t("manage.nothingFound")}</p>}
          </div>
        )}
      </div>
      <Modal
        open={stopping !== null}
        onClose={() => setStopping(null)}
        title={t("manage.processes.stopTitle")}
        footer={
          <>
            <Button variant="secondary" onClick={() => setStopping(null)}>
              {t("common.cancel")}
            </Button>
            <Button variant="danger" loading={busy} onClick={stop}>
              {t("manage.processes.stop")}
            </Button>
          </>
        }
      >
        <p className="text-sm">{t("manage.processes.stopBody", { name: stopping?.name, pid: stopping?.pid })}</p>
      </Modal>
    </Card>
  );
}
