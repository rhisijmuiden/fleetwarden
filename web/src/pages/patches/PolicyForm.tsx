import { useEffect, useMemo, useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { post, put, type Approval, type ApprovalMode, type PatchCategory, type PatchPolicy, type RebootPolicy } from "../../api";
import { Alert, Button, Field, Input, Modal, Select, Switch, useErrorText } from "../../components/ui";
import { minutesToTime, timeToMinutes, weekOrder, weekdayNames } from "./util";

const categories: PatchCategory[] = ["security", "critical", "cumulative", "definition", "driver", "feature", "other"];
const durations = [60, 120, 180, 240, 360, 480, 720, 1440];

type Draft = Omit<PatchPolicy, "id" | "createdAt" | "updatedAt" | "groups" | "nextWindow" | "windowOpen">;

function toDraft(p: PatchPolicy): Draft {
  const { id: _id, createdAt: _c, updatedAt: _u, groups: _g, nextWindow: _n, windowOpen: _w, ...rest } = p;
  return { ...rest, approvals: { ...p.approvals } };
}

export function PolicyForm({
  editing,
  defaults,
  serverTimeZone,
  onClose,
  onSaved,
}: {
  editing: PatchPolicy | "new" | null;
  defaults: PatchPolicy;
  serverTimeZone: string;
  onClose: () => void;
  onSaved: () => void;
}) {
  const { t, i18n } = useTranslation();
  const errorText = useErrorText();
  const policy = editing && editing !== "new" ? editing : null;
  const [d, setD] = useState<Draft>(() => toDraft(defaults));
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const zones = useMemo(() => {
    try {
      return (Intl as unknown as { supportedValuesOf(k: string): string[] }).supportedValuesOf("timeZone");
    } catch {
      return [] as string[];
    }
  }, []);
  const days = weekdayNames(i18n.language);

  useEffect(() => {
    if (!editing) return;
    setD(toDraft(policy ?? { ...defaults, name: "" }));
    setError(null);
  }, [editing]);

  const set = <K extends keyof Draft>(k: K, v: Draft[K]) => setD((x) => ({ ...x, [k]: v }));
  const setApproval = (c: PatchCategory, a: Partial<Approval>) =>
    setD((x) => ({ ...x, approvals: { ...x.approvals, [c]: { ...(x.approvals[c] ?? { mode: "manual", delayDays: 0 }), ...a } } }));

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      if (policy) await put(`/patches/policies/${policy.id}`, d);
      else await post("/patches/policies", d);
      onSaved();
    } catch (err) {
      setError(errorText(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Modal
      open={!!editing}
      onClose={onClose}
      wide
      title={policy ? t("patches.editPolicy") : t("patches.addPolicy")}
      footer={
        <>
          <Button variant="secondary" onClick={onClose}>
            {t("common.cancel")}
          </Button>
          <Button type="submit" form="policy-form" loading={busy}>
            {t("common.save")}
          </Button>
        </>
      }
    >
      <form id="policy-form" onSubmit={submit} className="space-y-6">
        {error && <Alert tone="danger">{error}</Alert>}
        <div className="grid gap-4 sm:grid-cols-2">
          <Field label={t("patches.policyName")}>
            {(id) => <Input id={id} required maxLength={100} value={d.name} onChange={(e) => set("name", e.target.value)} />}
          </Field>
          <Field label={t("groups.description")}>
            {(id) => <Input id={id} maxLength={500} value={d.description} onChange={(e) => set("description", e.target.value)} />}
          </Field>
        </div>
        <Switch checked={d.enabled} onChange={(v) => set("enabled", v)} label={t("patches.enabled")} description={t("patches.enabledHint")} />

        <section className="space-y-3">
          <h3 className="text-sm font-semibold">{t("patches.window")}</h3>
          <div className="flex flex-wrap gap-2" role="group" aria-label={t("patches.windowDays")}>
            {weekOrder.map((day) => {
              const on = (d.windowDays & (1 << day)) !== 0;
              return (
                <button
                  key={day}
                  type="button"
                  aria-pressed={on}
                  onClick={() => set("windowDays", d.windowDays ^ (1 << day))}
                  className={`h-8 min-w-12 rounded-md border px-3 text-xs font-medium capitalize transition-colors ${on ? "border-primary bg-primary text-primary-contrast" : "border-line bg-surface text-muted hover:text-fg"}`}
                >
                  {days[day]}
                </button>
              );
            })}
          </div>
          <div className="grid gap-4 sm:grid-cols-3">
            <Field label={t("patches.windowStart")}>
              {(id) => <Input id={id} type="time" required value={minutesToTime(d.windowStart)} onChange={(e) => set("windowStart", timeToMinutes(e.target.value))} />}
            </Field>
            <Field label={t("patches.windowLength")}>
              {(id) => (
                <Select id={id} value={d.windowMinutes} onChange={(e) => set("windowMinutes", Number(e.target.value))}>
                  {[...new Set([...durations, d.windowMinutes])]
                    .sort((a, b) => a - b)
                    .map((m) => (
                      <option key={m} value={m}>
                        {t("patches.hours", { count: m / 60 })}
                      </option>
                    ))}
                </Select>
              )}
            </Field>
            <Field label={t("patches.timeZone")}>
              {(id) => (
                <Select id={id} value={d.timeZone} onChange={(e) => set("timeZone", e.target.value)}>
                  <option value="">{t("patches.serverTimeZone", { zone: serverTimeZone })}</option>
                  {zones.map((z) => (
                    <option key={z} value={z}>
                      {z}
                    </option>
                  ))}
                </Select>
              )}
            </Field>
          </div>
          <p className="text-xs text-muted">{t("patches.windowHint")}</p>
        </section>

        <section className="space-y-3">
          <h3 className="text-sm font-semibold">{t("patches.approvals")}</h3>
          <p className="text-xs text-muted">{t("patches.approvalsHint")}</p>
          <div className="divide-y divide-line rounded-md border border-line">
            {categories.map((c) => {
              const a = d.approvals[c] ?? { mode: "manual" as ApprovalMode, delayDays: 0 };
              return (
                <div key={c} className="flex flex-wrap items-center gap-3 px-3 py-2">
                  <div className="min-w-48 flex-1">
                    <div className="text-sm font-medium">{t(`patches.categories.${c}`)}</div>
                    <div className="text-xs text-muted">{t(`patches.categoryHints.${c}`)}</div>
                  </div>
                  <div className="w-60 shrink-0">
                    <Select value={a.mode} onChange={(e) => setApproval(c, { mode: e.target.value as ApprovalMode })} aria-label={t(`patches.categories.${c}`)}>
                      <option value="auto">{t("patches.modes.auto")}</option>
                      <option value="manual">{t("patches.modes.manual")}</option>
                      <option value="never">{t("patches.modes.never")}</option>
                    </Select>
                  </div>
                  <label className={`flex shrink-0 items-center gap-2 text-sm ${a.mode === "auto" ? "" : "invisible"}`}>
                    {t("patches.after")}
                    <span className="w-20">
                      <Input type="number" min={0} max={365} value={a.delayDays} onChange={(e) => setApproval(c, { delayDays: Math.max(0, Number(e.target.value) || 0) })} />
                    </span>
                    {t("patches.days")}
                  </label>
                </div>
              );
            })}
          </div>
        </section>

        <section className="space-y-3">
          <h3 className="text-sm font-semibold">{t("patches.restartAndUsers")}</h3>
          <Field label={t("patches.rebootPolicy")} hint={t(`patches.rebootHints.${d.reboot}`)}>
            {(id) => (
              <Select id={id} value={d.reboot} onChange={(e) => set("reboot", e.target.value as RebootPolicy)}>
                {(["prompt", "automatic", "never"] as RebootPolicy[]).map((r) => (
                  <option key={r} value={r}>
                    {t(`patches.reboot.${r}`)}
                  </option>
                ))}
              </Select>
            )}
          </Field>
          <Switch checked={d.notifyUsers} onChange={(v) => set("notifyUsers", v)} label={t("patches.notifyUsers")} description={t("patches.notifyUsersHint")} />
          {(d.notifyUsers || d.reboot !== "never") && (
            <div className="grid gap-4 sm:grid-cols-3">
              <Field label={t("patches.maxPostpones")}>
                {(id) => <Input id={id} type="number" min={0} max={20} value={d.maxPostpones} onChange={(e) => set("maxPostpones", Number(e.target.value) || 0)} />}
              </Field>
              <Field label={t("patches.postponeMinutes")}>
                {(id) => <Input id={id} type="number" min={5} max={1440} value={d.postponeMinutes} onChange={(e) => set("postponeMinutes", Number(e.target.value) || 5)} />}
              </Field>
              <Field label={t("patches.countdown")} hint={t("patches.countdownHint")}>
                {(id) => <Input id={id} type="number" min={30} max={3600} value={d.countdownSeconds} onChange={(e) => set("countdownSeconds", Number(e.target.value) || 30)} />}
              </Field>
            </div>
          )}
        </section>

        <section className="space-y-3">
          <h3 className="text-sm font-semibold">{t("patches.advanced")}</h3>
          <Switch checked={d.takeOver} onChange={(v) => set("takeOver", v)} label={t("patches.takeOver")} description={t("patches.takeOverHint")} />
          <p className="text-xs text-muted">{t("patches.scanIntervalMoved")}</p>
        </section>
      </form>
    </Modal>
  );
}
