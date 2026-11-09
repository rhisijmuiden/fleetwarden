import { useCallback, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import { CalendarClock, Mail, Pencil, Plus, Send, Trash2 } from "lucide-react";
import { del, get, post, put, type ReportKind, type ReportSchedule } from "../../api";
import { useGroups } from "../../components/GroupScope";
import { Alert, Badge, Button, Card, Field, Input, Modal, Select, Spinner, Switch, formatDate, useErrorText } from "../../components/ui";
import { minutesToTime, timeToMinutes } from "../patches/util";

const kinds: ReportKind[] = ["compliance", "patches", "software", "assets"];
type Draft = Omit<ReportSchedule, "id" | "nextRunAt" | "lastRunAt" | "lastError" | "createdBy"> & { id?: number };

const emptyDraft = (lang: string): Draft => ({
  name: "",
  enabled: true,
  reports: ["compliance", "patches", "software"],
  groupIds: [],
  frequency: "weekly",
  weekday: 1,
  monthDay: 1,
  sendAt: 8 * 60,
  timeZone: "",
  recipients: [],
  language: lang === "nl" ? "nl" : "en",
  formats: ["pdf"],
});

function useWeekdays(lang: string) {
  // 2023-01-01 was a Sunday.
  return Array.from({ length: 7 }, (_, i) => new Date(2023, 0, 1 + i).toLocaleDateString(lang === "nl" ? "nl-NL" : "en-GB", { weekday: "long" }));
}

export function scheduleText(s: Pick<ReportSchedule, "frequency" | "weekday" | "monthDay" | "sendAt">, t: TFunction, days: string[]) {
  const time = minutesToTime(s.sendAt);
  if (s.frequency === "daily") return t("reports.schedules.everyDay", { time });
  if (s.frequency === "weekly") return t("reports.schedules.everyWeek", { day: days[s.weekday], time });
  return t("reports.schedules.everyMonth", { day: s.monthDay, time });
}

export function Schedules() {
  const { t, i18n } = useTranslation();
  const errorText = useErrorText();
  const days = useWeekdays(i18n.language);
  const [list, setList] = useState<ReportSchedule[] | null>(null);
  const [zone, setZone] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [draft, setDraft] = useState<Draft | null>(null);
  const [sending, setSending] = useState<number | null>(null);

  const load = useCallback(() => {
    get<{ schedules: ReportSchedule[]; serverTimeZone: string }>("/reports/schedules")
      .then((r) => {
        setList(r.schedules);
        setZone(r.serverTimeZone);
      })
      .catch((e) => setError(errorText(e)));
  }, []);
  useEffect(load, [load]);

  const send = (s: ReportSchedule) => {
    setSending(s.id);
    setNotice(null);
    setError(null);
    post<{ queued: number }>(`/reports/schedules/${s.id}/send`)
      .then((r) => {
        setNotice(t("reports.schedules.sent", { count: r.queued }));
        load();
      })
      .catch((e) => setError(errorText(e)))
      .finally(() => setSending(null));
  };
  const remove = (s: ReportSchedule) => {
    if (!confirm(t("reports.schedules.confirmDelete", { name: s.name }))) return;
    del(`/reports/schedules/${s.id}`)
      .then(load)
      .catch((e) => setError(errorText(e)));
  };

  if (!list) return error ? <Alert tone="danger">{error}</Alert> : <Spinner />;
  return (
    <div className="space-y-4">
      {error && <Alert tone="danger">{error}</Alert>}
      {notice && <Alert tone="success">{notice}</Alert>}
      <Card
        title={t("reports.schedules.title")}
        description={t("reports.schedules.subtitle")}
        actions={
          <Button icon={<Plus className="size-4" />} onClick={() => setDraft(emptyDraft(i18n.language))}>
            {t("reports.schedules.new")}
          </Button>
        }
      >
        {list.length === 0 ? (
          <div className="grid place-items-center rounded-md border border-dashed border-line px-6 py-10 text-center">
            <CalendarClock className="size-8 text-muted" strokeWidth={1.5} />
            <p className="mt-3 max-w-sm text-sm text-muted">{t("reports.schedules.empty")}</p>
          </div>
        ) : (
          <ul className="-mx-5 -my-5 divide-y divide-line">
            {list.map((s) => (
              <li key={s.id} className="flex flex-wrap items-start gap-4 px-5 py-4">
                <span className="grid size-9 shrink-0 place-items-center rounded-md bg-primary/10 text-primary">
                  <Mail className="size-4" />
                </span>
                <div className="min-w-0 flex-1 space-y-1">
                  <div className="flex flex-wrap items-center gap-2">
                    <span className="font-medium">{s.name}</span>
                    {!s.enabled && <Badge>{t("reports.schedules.paused")}</Badge>}
                    {s.reports.map((k) => (
                      <Badge key={k} tone="primary">
                        {t(`reports.tabs.${k}`)}
                      </Badge>
                    ))}
                    {s.formats.map((f) => (
                      <Badge key={f}>{f.toUpperCase()}</Badge>
                    ))}
                  </div>
                  <p className="text-sm text-muted">
                    {scheduleText(s, t, days)} · {s.recipients.join(", ")}
                  </p>
                  <p className="text-xs text-muted">
                    {s.enabled &&
                      s.nextRunAt &&
                      t("reports.schedules.next", {
                        date: formatDate(s.nextRunAt, i18n.language),
                      })}
                    {s.lastRunAt && ` · ${t("reports.schedules.last", { date: formatDate(s.lastRunAt, i18n.language) })}`}
                  </p>
                  {s.lastError && <p className="text-xs text-danger">{s.lastError}</p>}
                </div>
                <div className="flex gap-1">
                  <Button size="sm" variant="secondary" icon={<Send className="size-4" />} loading={sending === s.id} onClick={() => send(s)}>
                    {t("reports.schedules.sendNow")}
                  </Button>
                  <Button
                    size="sm"
                    variant="ghost"
                    icon={<Pencil className="size-4" />}
                    aria-label={t("common.edit")}
                    title={t("common.edit")}
                    onClick={() => setDraft({ ...s })}
                  />
                  <Button
                    size="sm"
                    variant="ghost"
                    icon={<Trash2 className="size-4" />}
                    aria-label={t("common.delete")}
                    title={t("common.delete")}
                    onClick={() => remove(s)}
                  />
                </div>
              </li>
            ))}
          </ul>
        )}
      </Card>
      <ScheduleForm draft={draft} zone={zone} days={days} onClose={() => setDraft(null)} onSaved={load} />
    </div>
  );
}

function ScheduleForm({
  draft: initial,
  zone,
  days,
  onClose,
  onSaved,
}: {
  draft: Draft | null;
  zone: string;
  days: string[];
  onClose: () => void;
  onSaved: () => void;
}) {
  const { t } = useTranslation();
  const errorText = useErrorText();
  const groups = useGroups();
  const [d, setD] = useState<Draft | null>(initial);
  const [rcpt, setRcpt] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => {
    setD(initial);
    setRcpt(initial?.recipients.join(", ") ?? "");
    setError(null);
  }, [initial]);
  if (!d) return null;
  const set = (p: Partial<Draft>) => setD({ ...d, ...p });
  const toggle = <T,>(list: T[], v: T, on: boolean) => (on ? [...list.filter((x) => x !== v), v] : list.filter((x) => x !== v));

  const save = () => {
    setBusy(true);
    setError(null);
    const body = { ...d, recipients: rcpt.split(/[,;\s]+/).filter(Boolean) };
    (d.id ? put(`/reports/schedules/${d.id}`, body) : post("/reports/schedules", body))
      .then(() => {
        onSaved();
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
      title={d.id ? t("reports.schedules.edit") : t("reports.schedules.new")}
      footer={
        <>
          <Button variant="secondary" onClick={onClose}>
            {t("common.cancel")}
          </Button>
          <Button onClick={save} loading={busy}>
            {t("common.save")}
          </Button>
        </>
      }
    >
      {error && <Alert tone="danger">{error}</Alert>}
      <Field label={t("reports.schedules.name")}>
        {(id) => <Input id={id} value={d.name} onChange={(e) => set({ name: e.target.value })} placeholder={t("reports.schedules.namePlaceholder")} />}
      </Field>
      <Field label={t("reports.schedules.reports")}>
        {() => (
          <div className="flex flex-wrap gap-4">
            {kinds.map((k) => (
              <label key={k} className="flex items-center gap-2 text-sm">
                <input
                  type="checkbox"
                  className="size-4 accent-primary"
                  checked={d.reports.includes(k)}
                  onChange={(e) => set({ reports: toggle(d.reports, k, e.target.checked) })}
                />
                {t(`reports.tabs.${k}`)}
              </label>
            ))}
          </div>
        )}
      </Field>
      <div className="grid gap-4 sm:grid-cols-3">
        <Field label={t("reports.schedules.frequency")}>
          {(id) => (
            <Select id={id} value={d.frequency} onChange={(e) => set({ frequency: e.target.value as Draft["frequency"] })}>
              <option value="daily">{t("reports.schedules.daily")}</option>
              <option value="weekly">{t("reports.schedules.weekly")}</option>
              <option value="monthly">{t("reports.schedules.monthly")}</option>
            </Select>
          )}
        </Field>
        {d.frequency === "weekly" && (
          <Field label={t("reports.schedules.weekday")}>
            {(id) => (
              <Select id={id} value={d.weekday} onChange={(e) => set({ weekday: Number(e.target.value) })}>
                {days.map((name, i) => (
                  <option key={i} value={i}>
                    {name}
                  </option>
                ))}
              </Select>
            )}
          </Field>
        )}
        {d.frequency === "monthly" && (
          <Field label={t("reports.schedules.monthDay")}>
            {(id) => (
              <Select id={id} value={d.monthDay} onChange={(e) => set({ monthDay: Number(e.target.value) })}>
                {Array.from({ length: 28 }, (_, i) => (
                  <option key={i + 1} value={i + 1}>
                    {i + 1}
                  </option>
                ))}
              </Select>
            )}
          </Field>
        )}
        <Field label={t("reports.schedules.time")} hint={t("reports.schedules.zoneHint", { zone: d.timeZone || zone })}>
          {(id) => <Input id={id} type="time" value={minutesToTime(d.sendAt)} onChange={(e) => set({ sendAt: timeToMinutes(e.target.value) })} />}
        </Field>
      </div>
      <Field label={t("reports.schedules.recipients")} hint={t("reports.schedules.recipientsHint")}>
        {(id) => <Input id={id} value={rcpt} onChange={(e) => setRcpt(e.target.value)} placeholder="it@example.com, manager@example.com" />}
      </Field>
      <div className="grid gap-4 sm:grid-cols-2">
        <Field label={t("reports.schedules.language")}>
          {(id) => (
            <Select id={id} value={d.language} onChange={(e) => set({ language: e.target.value as Draft["language"] })}>
              <option value="en">English</option>
              <option value="nl">Nederlands</option>
            </Select>
          )}
        </Field>
        <Field label={t("reports.schedules.formats")}>
          {() => (
            <div className="flex h-9 items-center gap-4">
              {(["pdf", "csv"] as const).map((f) => (
                <label key={f} className="flex items-center gap-2 text-sm">
                  <input
                    type="checkbox"
                    className="size-4 accent-primary"
                    checked={d.formats.includes(f)}
                    onChange={(e) => set({ formats: toggle(d.formats, f, e.target.checked) })}
                  />
                  {f.toUpperCase()}
                </label>
              ))}
            </div>
          )}
        </Field>
      </div>
      <Field label={t("reports.schedules.groups")} hint={d.groupIds.length === 0 ? t("reports.allGroups") : undefined}>
        {() => (
          <div className="grid max-h-40 gap-1 overflow-y-auto rounded-md border border-line p-2 sm:grid-cols-2">
            {(groups ?? []).map((g) => (
              <label key={g.id} className="flex cursor-pointer items-center gap-2 rounded-md px-2 py-1 text-sm hover:bg-surface-muted">
                <input
                  type="checkbox"
                  className="size-4 accent-primary"
                  checked={d.groupIds.includes(g.id)}
                  onChange={(e) =>
                    set({
                      groupIds: toggle(d.groupIds, g.id, e.target.checked),
                    })
                  }
                />
                <span className="truncate">{g.name}</span>
              </label>
            ))}
          </div>
        )}
      </Field>
      <Switch checked={d.enabled} onChange={(v) => set({ enabled: v })} label={t("reports.schedules.enabled")} />
    </Modal>
  );
}
