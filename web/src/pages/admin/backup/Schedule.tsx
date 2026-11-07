import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { LockKeyhole } from "lucide-react";
import { put, type BackupConfig, type BackupSchedule } from "../../../api";
import { Alert, Button, Card, Field, Input, Select, Switch, formatDate, useErrorText } from "../../../components/ui";

const intervals = [1, 2, 3, 4, 6, 8, 12, 24];

export function Passphrase({ config, onSaved }: { config: BackupConfig; onSaved: (c: BackupConfig) => void }) {
  const { t } = useTranslation();
  const errorText = useErrorText();
  const [current, setCurrent] = useState("");
  const [next, setNext] = useState("");
  const [repeat, setRepeat] = useState("");
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<{ tone: "success" | "danger"; text: string } | null>(null);

  const save = async (e: FormEvent) => {
    e.preventDefault();
    if (next !== repeat) {
      setMsg({ tone: "danger", text: t("backup.pass.mismatch") });
      return;
    }
    setBusy(true);
    setMsg(null);
    try {
      onSaved(await put<BackupConfig>("/backup/passphrase", { current, new: next }));
      setCurrent("");
      setNext("");
      setRepeat("");
      setMsg({ tone: "success", text: t("backup.pass.saved") });
    } catch (err) {
      setMsg({ tone: "danger", text: errorText(err) });
    } finally {
      setBusy(false);
    }
  };

  return (
    <Card title={t("backup.pass.title")} description={t("backup.pass.subtitle")}>
      <form onSubmit={save} className="max-w-xl space-y-4">
        {msg && <Alert tone={msg.tone}>{msg.text}</Alert>}
        <Alert tone="warning" title={t("backup.pass.warnTitle")}>
          {t("backup.pass.warn")}
        </Alert>
        {config.passphraseSet && (
          <Field label={t("backup.pass.current")}>{(id) => <Input id={id} type="password" autoComplete="current-password" value={current} onChange={(e) => setCurrent(e.target.value)} required />}</Field>
        )}
        <div className="grid gap-4 sm:grid-cols-2">
          <Field label={config.passphraseSet ? t("backup.pass.new") : t("backup.pass.label")} hint={t("backup.pass.hint")}>
            {(id) => <Input id={id} type="password" autoComplete="new-password" minLength={12} value={next} onChange={(e) => setNext(e.target.value)} required />}
          </Field>
          <Field label={t("backup.pass.repeat")}>{(id) => <Input id={id} type="password" autoComplete="new-password" value={repeat} onChange={(e) => setRepeat(e.target.value)} required />}</Field>
        </div>
        {config.passphraseSet && <p className="text-xs text-muted">{t("backup.pass.changeHint")}</p>}
        <Button type="submit" loading={busy} icon={<LockKeyhole className="size-4" />}>
          {config.passphraseSet ? t("backup.pass.change") : t("backup.pass.set")}
        </Button>
      </form>
    </Card>
  );
}

export function Schedule({ config, onSaved }: { config: BackupConfig; onSaved: (c: BackupConfig) => void }) {
  const { t, i18n } = useTranslation();
  const errorText = useErrorText();
  const [s, setS] = useState<BackupSchedule>(config.schedule);
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<{ tone: "success" | "danger"; text: string } | null>(null);

  const save = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setMsg(null);
    try {
      const c = await put<BackupConfig>("/backup/schedule", s);
      onSaved(c);
      setS(c.schedule);
      setMsg({ tone: "success", text: t("common.saved") });
    } catch (err) {
      setMsg({ tone: "danger", text: errorText(err) });
    } finally {
      setBusy(false);
    }
  };
  const num = (v: string, min: number, max: number) => Math.min(max, Math.max(min, Number(v) || min));

  return (
    <Card title={t("backup.sched.title")} description={t("backup.sched.subtitle")}>
      <form onSubmit={save} className="space-y-5">
        {msg && <Alert tone={msg.tone}>{msg.text}</Alert>}
        <Switch checked={s.enabled} onChange={(v) => setS({ ...s, enabled: v })} label={t("backup.sched.enable")} description={config.passphraseSet ? t("backup.sched.enableHint") : t("backup.sched.needsPassphrase")} />
        <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
          <Field label={t("backup.sched.every")}>
            {(id) => (
              <Select id={id} value={s.intervalHours} onChange={(e) => setS({ ...s, intervalHours: Number(e.target.value) })}>
                {intervals.map((h) => (
                  <option key={h} value={h}>
                    {h === 24 ? t("backup.sched.daily") : t("backup.sched.hours", { count: h })}
                  </option>
                ))}
              </Select>
            )}
          </Field>
          <Field label={t("backup.sched.start")} hint={t("backup.sched.tz", { tz: config.timeZone })}>
            {(id) => <Input id={id} type="time" value={s.startTime} onChange={(e) => setS({ ...s, startTime: e.target.value })} required />}
          </Field>
          <Field label={t("backup.sched.fullEvery")} hint={t("backup.sched.fullEveryHint")}>
            {(id) => <Input id={id} type="number" min={1} max={90} value={s.fullEveryDays} onChange={(e) => setS({ ...s, fullEveryDays: num(e.target.value, 1, 90) })} />}
          </Field>
          <Field label={t("backup.sched.keep")} hint={t("backup.sched.keepHint")}>
            {(id) => <Input id={id} type="number" min={1} max={52} value={s.keepFull} onChange={(e) => setS({ ...s, keepFull: num(e.target.value, 1, 52) })} />}
          </Field>
        </div>
        <p className="text-sm text-muted">
          {t("backup.sched.summary", { full: s.fullEveryDays, keep: s.keepFull, days: s.fullEveryDays * s.keepFull })}
        </p>
        {config.schedule.enabled && config.nextRun && <p className="text-sm">{t("backup.nextRun", { when: formatDate(config.nextRun, i18n.language) })}</p>}
        <Button type="submit" loading={busy}>
          {t("common.save")}
        </Button>
      </form>
    </Card>
  );
}
