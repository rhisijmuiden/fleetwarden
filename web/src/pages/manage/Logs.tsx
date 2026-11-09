import { useCallback, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { AlertOctagon, AlertTriangle, Download, Info, Search, XCircle } from "lucide-react";
import { get, type Device } from "../../api";
import { Alert, Button, Card, Field, Input, Select, Spinner, formatDate, useErrorText } from "../../components/ui";
import type { LogEvent, LogSource } from "./types";

const levelIcon = (l: number) => {
  switch (l) {
    case 1:
      return <AlertOctagon className="size-4 text-danger" />;
    case 2:
      return <XCircle className="size-4 text-danger" />;
    case 3:
      return <AlertTriangle className="size-4 text-warning" />;
  }
  return <Info className="size-4 text-muted" />;
};

const periods = [1, 24, 24 * 7, 24 * 30, 0];

function csvCell(v: string | number | undefined) {
  const s = String(v ?? "");
  return /[",\n;]/.test(s) ? `"${s.replace(/"/g, '""')}"` : s;
}

export function LogsPanel({ device }: { device: Device }) {
  const { t, i18n } = useTranslation();
  const errorText = useErrorText();
  const [sources, setSources] = useState<LogSource[] | null>(null);
  const [source, setSource] = useState("");
  const [level, setLevel] = useState(device.platform === "windows" ? 3 : 4);
  const [hours, setHours] = useState(24);
  const [search, setSearch] = useState("");
  const [provider, setProvider] = useState("");
  const [max, setMax] = useState(500);
  const [events, setEvents] = useState<LogEvent[] | null>(null);
  const [truncated, setTruncated] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [open, setOpen] = useState<number | null>(null);

  useEffect(() => {
    get<{ sources: LogSource[] }>(`/devices/${device.id}/manage/logs`)
      .then((r) => {
        setSources(r.sources);
        setSource((s) => s || (r.sources.find((x) => x.name === "System") ?? r.sources[0])?.name || "");
      })
      .catch((e) => setError(errorText(e)));
  }, [device.id]);

  const load = useCallback(() => {
    if (!source) return;
    setLoading(true);
    setError(null);
    setOpen(null);
    const q = new URLSearchParams({ source, level: String(level), hours: String(hours), max: String(max) });
    if (search.trim()) q.set("search", search.trim());
    if (provider.trim()) q.set("provider", provider.trim());
    get<{ events: LogEvent[]; truncated: boolean }>(`/devices/${device.id}/manage/logs/events?${q}`)
      .then((r) => {
        setEvents(r.events);
        setTruncated(r.truncated);
      })
      .catch((e) => setError(errorText(e)))
      .finally(() => setLoading(false));
  }, [device.id, source, level, hours, max, search, provider]);
  // Reload when the log or a list filter changes; text filters wait for Enter.
  useEffect(load, [device.id, source, level, hours, max]);

  const exportCsv = () => {
    if (!events) return;
    const rows = [["time", "level", "source", "eventId", "message"].join(",")];
    for (const e of events) rows.push([e.time, t(`manage.logs.levels.${e.level}`), e.provider, e.eventId ?? "", e.message].map(csvCell).join(","));
    const blob = new Blob(["﻿" + rows.join("\r\n")], { type: "text/csv;charset=utf-8" });
    const a = document.createElement("a");
    a.href = URL.createObjectURL(blob);
    a.download = `${device.hostname}-${source.replace(/[^A-Za-z0-9]+/g, "-")}.csv`;
    a.click();
    URL.revokeObjectURL(a.href);
  };

  return (
    <Card
      title={t("manage.sections.logs")}
      description={device.platform === "windows" ? t("manage.logs.hintWindows") : t("manage.logs.hintLinux")}
      actions={
        <Button variant="secondary" size="sm" icon={<Download className="size-4" />} disabled={!events?.length} onClick={exportCsv}>
          {t("manage.logs.export")}
        </Button>
      }
    >
      <div className="space-y-4">
        {error && <Alert tone="danger">{error}</Alert>}
        <form
          className="grid gap-3 sm:grid-cols-2 xl:grid-cols-3"
          onSubmit={(e) => {
            e.preventDefault();
            load();
          }}
        >
          <Field label={t("manage.logs.log")}>
            {(id) => (
              <Select id={id} value={source} onChange={(e) => setSource(e.target.value)} disabled={!sources}>
                {(sources ?? []).map((s) => (
                  <option key={s.name} value={s.name}>
                    {t(`manage.logs.sources.${s.name}`, { defaultValue: s.name })}
                  </option>
                ))}
              </Select>
            )}
          </Field>
          <Field label={t("manage.logs.level")}>
            {(id) => (
              <Select id={id} value={level} onChange={(e) => setLevel(Number(e.target.value))}>
                {[1, 2, 3, 4, 0].map((l) => (
                  <option key={l} value={l}>
                    {t(`manage.logs.minLevel.${l}`)}
                  </option>
                ))}
              </Select>
            )}
          </Field>
          <Field label={t("manage.logs.period")}>
            {(id) => (
              <Select id={id} value={hours} onChange={(e) => setHours(Number(e.target.value))}>
                {periods.map((h) => (
                  <option key={h} value={h}>
                    {t(`manage.logs.periods.${h}`)}
                  </option>
                ))}
              </Select>
            )}
          </Field>
          <Field label={device.platform === "windows" ? t("manage.logs.provider") : t("manage.logs.unit")}>
            {(id) => <Input id={id} value={provider} placeholder={device.platform === "windows" ? "Service Control Manager" : "ssh.service"} onChange={(e) => setProvider(e.target.value)} />}
          </Field>
          <Field label={t("manage.logs.search")}>
            {(id) => <Input id={id} value={search} onChange={(e) => setSearch(e.target.value)} />}
          </Field>
          <Field label={t("manage.logs.max")}>
            {(id) => (
              <div className="flex gap-2">
                <Select id={id} value={max} onChange={(e) => setMax(Number(e.target.value))}>
                  {[100, 500, 2000].map((n) => (
                    <option key={n} value={n}>
                      {n}
                    </option>
                  ))}
                </Select>
                <Button type="submit" icon={<Search className="size-4" />} loading={loading} aria-label={t("manage.logs.apply")} />
              </div>
            )}
          </Field>
        </form>
        {loading && !events ? (
          <Spinner />
        ) : events && events.length === 0 ? (
          <p className="rounded-md border border-dashed border-line px-4 py-6 text-center text-sm text-muted">{t("manage.logs.none")}</p>
        ) : events ? (
          <>
            <div className="-mx-5 overflow-x-auto">
              <table className="w-full min-w-[760px] text-sm">
                <thead className="border-y border-line text-left text-xs text-muted uppercase">
                  <tr>
                    <th className="w-10 px-5 py-3">
                      <span className="sr-only">{t("manage.logs.level")}</span>
                    </th>
                    <th className="px-3 py-3 font-medium">{t("manage.logs.time")}</th>
                    <th className="px-3 py-3 font-medium">{t("manage.logs.source")}</th>
                    {device.platform === "windows" && <th className="px-3 py-3 font-medium">{t("manage.logs.eventId")}</th>}
                    <th className="px-5 py-3 font-medium">{t("manage.logs.message")}</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-line">
                  {events.map((e, i) => (
                    <tr key={i} className="cursor-pointer align-top hover:bg-surface-muted/50" onClick={() => setOpen(open === i ? null : i)} aria-expanded={open === i}>
                      <td className="px-5 py-2" title={t(`manage.logs.levels.${e.level}`)}>
                        {levelIcon(e.level)}
                      </td>
                      <td className="px-3 py-2 text-xs whitespace-nowrap text-muted">{formatDate(e.time, i18n.language)}</td>
                      <td className="max-w-[220px] truncate px-3 py-2 text-xs" title={e.provider}>
                        {e.provider || "–"}
                      </td>
                      {device.platform === "windows" && <td className="px-3 py-2 font-mono text-xs text-muted">{e.eventId || "–"}</td>}
                      <td className="px-5 py-2">
                        <div className={open === i ? "font-mono text-xs break-words whitespace-pre-wrap" : "line-clamp-2 break-words"}>{e.message || "–"}</div>
                        {open === i && (e.pid || e.user) && (
                          <div className="mt-1 text-xs text-muted">{[e.pid ? `PID ${e.pid}` : "", e.user ?? ""].filter(Boolean).join(" · ")}</div>
                        )}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
            <p className="text-xs text-muted">{truncated ? t("manage.logs.truncated", { count: events.length }) : t("manage.logs.count", { count: events.length })}</p>
          </>
        ) : null}
      </div>
    </Card>
  );
}
