import { useCallback, useEffect, useState } from "react";
import { Link } from "react-router";
import { useTranslation } from "react-i18next";
import { Eye, KeyRound, Pencil, Plus, ScrollText, Trash2 } from "lucide-react";
import { del, get, post, put, type LicenseInstall, type SoftwareLicense } from "../../api";
import { useAuth } from "../../auth";
import { Alert, Badge, Button, Card, CopyField, Field, Input, Modal, Spinner, useErrorText } from "../../components/ui";
import { timeAgo } from "../devices/util";
import { daysLeft, formatDay } from "./util";

type LicenseState = "over" | "expired" | "expiring" | "ok";

function licenseState(l: SoftwareLicense, days: number): LicenseState {
  if (l.inUse > l.seats) return "over";
  const left = daysLeft(l.expiresOn);
  if (left !== null && left < 0) return "expired";
  if (left !== null && left <= days) return "expiring";
  return "ok";
}

const stateTone = { over: "danger", expired: "danger", expiring: "warning", ok: "success" } as const;

export function Licenses() {
  const { t, i18n } = useTranslation();
  const { can } = useAuth();
  const errorText = useErrorText();
  const manage = can("assets.manage");
  const [list, setList] = useState<SoftwareLicense[] | null>(null);
  const [days, setDays] = useState(90);
  const [error, setError] = useState<string | null>(null);
  const [editing, setEditing] = useState<SoftwareLicense | "new" | null>(null);
  const [showing, setShowing] = useState<SoftwareLicense | null>(null);
  const [deleting, setDeleting] = useState<SoftwareLicense | null>(null);

  const load = useCallback(() => {
    get<{ licenses: SoftwareLicense[]; expiringDays: number }>("/licenses")
      .then((r) => {
        setList(r.licenses);
        setDays(r.expiringDays);
        setError(null);
      })
      .catch((e) => setError(errorText(e)));
  }, []);
  useEffect(load, [load]);

  const totals = (list ?? []).reduce((a, l) => ({ seats: a.seats + l.seats, inUse: a.inUse + l.inUse }), { seats: 0, inUse: 0 });

  return (
    <div className="space-y-6">
      {error && <Alert tone="danger">{error}</Alert>}
      <Card
        title={t("assets.licenses.title")}
        description={t("assets.licenses.subtitle")}
        actions={
          manage && (
            <Button icon={<Plus className="size-4" />} onClick={() => setEditing("new")}>
              {t("assets.licenses.new")}
            </Button>
          )
        }
      >
        {!list ? (
          <Spinner />
        ) : list.length === 0 ? (
          <div className="grid place-items-center py-10 text-center">
            <ScrollText className="size-10 text-muted" strokeWidth={1.5} />
            <p className="mt-3 font-medium">{t("assets.licenses.empty")}</p>
            <p className="mt-1 max-w-md text-sm text-muted">{t("assets.licenses.emptyHint")}</p>
          </div>
        ) : (
          <div className="-mx-5 -my-5 overflow-x-auto">
            <table className="w-full min-w-[820px] text-sm">
              <thead className="border-b border-line text-left text-xs text-muted uppercase">
                <tr>
                  <th className="px-5 py-3 font-medium">{t("assets.licenses.name")}</th>
                  <th className="px-3 py-3 font-medium">{t("assets.licenses.usage")}</th>
                  <th className="px-3 py-3 text-right font-medium">{t("assets.licenses.free")}</th>
                  <th className="px-3 py-3 font-medium">{t("assets.licenses.expiresOn")}</th>
                  <th className="px-3 py-3 font-medium">{t("assets.licenses.state")}</th>
                  <th className="w-28 px-5 py-3" />
                </tr>
              </thead>
              <tbody className="divide-y divide-line">
                {list.map((l) => {
                  const st = licenseState(l, days);
                  const pct = l.seats ? Math.min(100, Math.round((l.inUse / l.seats) * 100)) : l.inUse ? 100 : 0;
                  return (
                    <tr key={l.id} className="hover:bg-surface-muted/50">
                      <td className="px-5 py-3">
                        <button type="button" className="text-left font-medium hover:underline" onClick={() => setShowing(l)}>
                          {l.name}
                        </button>
                        <div className="text-xs text-muted">
                          {[l.vendor, l.matchName].filter(Boolean).join(" · ")}
                          {l.hasKey && <KeyRound className="ml-1.5 inline size-3" aria-label={t("assets.licenses.hasKey")} />}
                        </div>
                      </td>
                      <td className="px-3 py-3">
                        <div className="flex items-center gap-3">
                          <div className="h-2 w-32 overflow-hidden rounded-full bg-surface-muted">
                            <div
                              className={`h-full rounded-full ${st === "over" ? "bg-danger" : pct >= 90 ? "bg-warning" : "bg-primary"}`}
                              style={{ width: `${pct}%` }}
                            />
                          </div>
                          <span className="tabular-nums">{t("assets.licenses.ofSeats", { inUse: l.inUse, seats: l.seats })}</span>
                        </div>
                      </td>
                      <td className={`px-3 py-3 text-right tabular-nums ${l.seats - l.inUse < 0 ? "font-medium text-danger" : ""}`}>{l.seats - l.inUse}</td>
                      <td className="px-3 py-3 whitespace-nowrap">{l.expiresOn ? formatDay(l.expiresOn, i18n.language) : t("assets.licenses.perpetual")}</td>
                      <td className="px-3 py-3">
                        <Badge tone={stateTone[st]}>{t(`assets.licenses.states.${st}`, { count: l.inUse - l.seats })}</Badge>
                      </td>
                      <td className="px-5 py-3 text-right whitespace-nowrap">
                        <Button size="sm" variant="ghost" icon={<Eye className="size-4" />} aria-label={t("assets.licenses.devices")} onClick={() => setShowing(l)} />
                        {manage && (
                          <>
                            <Button size="sm" variant="ghost" icon={<Pencil className="size-4" />} aria-label={t("common.edit")} onClick={() => setEditing(l)} />
                            <Button size="sm" variant="ghost" icon={<Trash2 className="size-4" />} aria-label={t("common.delete")} onClick={() => setDeleting(l)} />
                          </>
                        )}
                      </td>
                    </tr>
                  );
                })}
              </tbody>
              <tfoot className="border-t border-line text-xs text-muted">
                <tr>
                  <td className="px-5 py-3">{t("assets.licenses.total")}</td>
                  <td className="px-3 py-3 tabular-nums">{t("assets.licenses.ofSeats", { inUse: totals.inUse, seats: totals.seats })}</td>
                  <td colSpan={4} />
                </tr>
              </tfoot>
            </table>
          </div>
        )}
      </Card>

      {editing && <LicenseForm initial={editing === "new" ? null : editing} onClose={() => setEditing(null)} onSaved={load} />}
      {showing && <LicenseDetail license={showing} manage={manage} onClose={() => setShowing(null)} />}
      {deleting && (
        <Modal
          open
          onClose={() => setDeleting(null)}
          title={t("assets.licenses.delete")}
          footer={
            <>
              <Button variant="secondary" onClick={() => setDeleting(null)}>
                {t("common.cancel")}
              </Button>
              <Button
                variant="danger"
                onClick={() =>
                  del(`/licenses/${deleting.id}`)
                    .then(() => {
                      setDeleting(null);
                      load();
                    })
                    .catch((e) => setError(errorText(e)))
                }
              >
                {t("common.delete")}
              </Button>
            </>
          }
        >
          <p className="text-sm">{t("assets.licenses.confirmDelete", { name: deleting.name })}</p>
        </Modal>
      )}
    </div>
  );
}

type Draft = {
  name: string;
  vendor: string;
  matchName: string;
  matchPublisher: string;
  seats: number;
  expiresOn: string;
  cost: string;
  notes: string;
  key: string;
};

function LicenseForm({ initial, onClose, onSaved }: { initial: SoftwareLicense | null; onClose: () => void; onSaved: () => void }) {
  const { t } = useTranslation();
  const errorText = useErrorText();
  const [d, setD] = useState<Draft>({
    name: initial?.name ?? "",
    vendor: initial?.vendor ?? "",
    matchName: initial?.matchName ?? "",
    matchPublisher: initial?.matchPublisher ?? "",
    seats: initial?.seats ?? 1,
    expiresOn: initial?.expiresOn ?? "",
    cost: initial?.cost != null ? String(initial.cost) : "",
    notes: initial?.notes ?? "",
    key: "",
  });
  const [keyChanged, setKeyChanged] = useState(false);
  const [preview, setPreview] = useState<{ devices: number; programs: string[] } | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const set = (p: Partial<Draft>) => setD({ ...d, ...p });

  // Show which programs the match counts while typing.
  useEffect(() => {
    if (d.matchName.replace(/[*\s]/g, "") === "") {
      setPreview(null);
      return;
    }
    const id = setTimeout(() => {
      post<{ devices: number; programs: string[] }>("/licenses/preview", { matchName: d.matchName, matchPublisher: d.matchPublisher })
        .then(setPreview)
        .catch(() => setPreview(null));
    }, 400);
    return () => clearTimeout(id);
  }, [d.matchName, d.matchPublisher]);

  const save = () => {
    setBusy(true);
    setError(null);
    const body = {
      name: d.name,
      vendor: d.vendor,
      matchName: d.matchName,
      matchPublisher: d.matchPublisher,
      seats: Number(d.seats),
      expiresOn: d.expiresOn,
      cost: d.cost.trim() === "" ? null : Number(d.cost.replace(",", ".")),
      notes: d.notes,
      ...(keyChanged || !initial ? { key: d.key } : {}),
    };
    (initial ? put(`/licenses/${initial.id}`, body) : post("/licenses", body))
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
      title={initial ? t("assets.licenses.edit") : t("assets.licenses.new")}
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
      <div className="grid gap-4 sm:grid-cols-2">
        <Field label={t("assets.licenses.name")}>
          {(id) => <Input id={id} value={d.name} onChange={(e) => set({ name: e.target.value })} placeholder="Microsoft 365 Business Standard" />}
        </Field>
        <Field label={t("assets.licenses.vendor")}>{(id) => <Input id={id} value={d.vendor} onChange={(e) => set({ vendor: e.target.value })} />}</Field>
        <Field label={t("assets.licenses.matchName")} hint={t("assets.licenses.matchNameHint")}>
          {(id) => <Input id={id} value={d.matchName} onChange={(e) => set({ matchName: e.target.value })} placeholder="Microsoft 365 Apps*" />}
        </Field>
        <Field label={t("assets.licenses.matchPublisher")} hint={t("assets.licenses.matchPublisherHint")}>
          {(id) => <Input id={id} value={d.matchPublisher} onChange={(e) => set({ matchPublisher: e.target.value })} placeholder="Microsoft*" />}
        </Field>
      </div>
      {preview && (
        <Alert tone={preview.devices ? "info" : "warning"} title={t("assets.licenses.previewTitle", { count: preview.devices })}>
          {preview.programs.length > 0 && <span className="text-xs">{preview.programs.join(" · ")}</span>}
        </Alert>
      )}
      <div className="grid gap-4 sm:grid-cols-3">
        <Field label={t("assets.licenses.seats")}>
          {(id) => <Input id={id} type="number" min={0} value={d.seats} onChange={(e) => set({ seats: Number(e.target.value) })} />}
        </Field>
        <Field label={t("assets.licenses.expiresOn")} hint={t("assets.licenses.expiresHint")}>
          {(id) => <Input id={id} type="date" value={d.expiresOn} onChange={(e) => set({ expiresOn: e.target.value })} />}
        </Field>
        <Field label={t("assets.licenses.cost")}>
          {(id) => <Input id={id} inputMode="decimal" value={d.cost} onChange={(e) => set({ cost: e.target.value })} />}
        </Field>
      </div>
      <Field label={t("assets.licenses.key")} hint={initial?.hasKey && !keyChanged ? t("assets.licenses.keyKept") : t("assets.licenses.keyHint")}>
        {(id) => (
          <Input
            id={id}
            type="password"
            autoComplete="off"
            value={d.key}
            placeholder={initial?.hasKey && !keyChanged ? "••••••••" : ""}
            onChange={(e) => {
              setKeyChanged(true);
              set({ key: e.target.value });
            }}
          />
        )}
      </Field>
      <Field label={t("assets.licenses.notes")}>
        {(id) => (
          <textarea
            id={id}
            rows={3}
            value={d.notes}
            onChange={(e) => set({ notes: e.target.value })}
            className="w-full rounded-md border border-line bg-surface px-3 py-2 text-sm focus:border-primary focus:ring-2 focus:ring-primary/20 focus:outline-none"
          />
        )}
      </Field>
    </Modal>
  );
}

function LicenseDetail({ license, manage, onClose }: { license: SoftwareLicense; manage: boolean; onClose: () => void }) {
  const { t, i18n } = useTranslation();
  const errorText = useErrorText();
  const [devices, setDevices] = useState<LicenseInstall[] | null>(null);
  const [key, setKey] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => {
    get<{ devices: LicenseInstall[] }>(`/licenses/${license.id}/devices`)
      .then((r) => setDevices(r.devices))
      .catch((e) => setError(errorText(e)));
  }, [license.id]);

  return (
    <Modal open wide onClose={onClose} title={license.name}>
      {error && <Alert tone="danger">{error}</Alert>}
      <p className="text-sm text-muted">
        {t("assets.licenses.ofSeats", { inUse: license.inUse, seats: license.seats })}
        {license.cost != null && <> · {t("assets.licenses.costValue", { cost: license.cost.toLocaleString(i18n.language === "nl" ? "nl-NL" : "en-GB", { minimumFractionDigits: 2 }) })}</>}
      </p>
      {license.notes && <p className="text-sm whitespace-pre-wrap">{license.notes}</p>}
      {manage && license.hasKey && (
        <div>
          {key === null ? (
            <Button
              size="sm"
              variant="secondary"
              icon={<KeyRound className="size-4" />}
              onClick={() =>
                post<{ key: string }>(`/licenses/${license.id}/key`)
                  .then((r) => setKey(r.key))
                  .catch((e) => setError(errorText(e)))
              }
            >
              {t("assets.licenses.showKey")}
            </Button>
          ) : (
            <CopyField value={key} label={t("assets.licenses.key")} />
          )}
        </div>
      )}
      {!devices ? (
        <Spinner />
      ) : devices.length === 0 ? (
        <p className="text-sm text-muted">{t("assets.licenses.noDevices")}</p>
      ) : (
        <div className="max-h-96 overflow-y-auto rounded-md border border-line">
          <table className="w-full text-sm">
            <thead className="sticky top-0 bg-surface text-left text-xs text-muted uppercase">
              <tr>
                <th className="px-3 py-2 font-medium">{t("devices.device")}</th>
                <th className="px-3 py-2 font-medium">{t("assets.fields.owner")}</th>
                <th className="px-3 py-2 font-medium">{t("assets.licenses.program")}</th>
                <th className="px-3 py-2 font-medium">{t("devices.lastSeen")}</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-line">
              {devices.map((d) => (
                <tr key={d.deviceId}>
                  <td className="px-3 py-2">
                    <Link to={`/devices/${d.deviceId}`} className="font-medium hover:underline">
                      {d.hostname}
                    </Link>
                    <div className="text-xs text-muted">{d.groupName}</div>
                  </td>
                  <td className="px-3 py-2">{d.owner || "–"}</td>
                  <td className="px-3 py-2">
                    {d.program}
                    <div className="text-xs text-muted">{d.version}</div>
                  </td>
                  <td className="px-3 py-2 text-xs text-muted">{timeAgo(d.lastSeenAt, i18n.language)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {devices && devices.length < license.inUse && <p className="text-xs text-muted">{t("assets.licenses.outsideScope", { count: license.inUse - devices.length })}</p>}
    </Modal>
  );
}
