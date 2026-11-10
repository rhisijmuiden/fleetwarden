import { useCallback, useEffect, useState, type FormEvent } from "react";
import { Link } from "react-router";
import { useTranslation } from "react-i18next";
import { Ban, FolderTree, KeyRound, Pencil, Plus, Trash2 } from "lucide-react";
import {
  del,
  get,
  post,
  put,
  type Channel,
  type DeviceGroup,
  type EnrollmentToken,
  type PatchPolicies,
  type RemoteConsentSetting,
  type ScanSettings,
} from "../../api";
import { useAuth } from "../../auth";
import { Alert, Badge, Button, Card, Field, Input, Modal, PageHeader, Select, Spinner, formatDate, useErrorText, Switch } from "../../components/ui";
import { IntervalInput, formatInterval } from "../../components/IntervalInput";
import { AddClient } from "./AddClient";

export function Groups() {
  const { t } = useTranslation();
  const { can } = useAuth();
  const errorText = useErrorText();
  const [groups, setGroups] = useState<DeviceGroup[] | null>(null);
  const [policies, setPolicies] = useState<PatchPolicies["policies"]>([]);
  const [error, setError] = useState<string | null>(null);
  const [editing, setEditing] = useState<DeviceGroup | "new" | null>(null);
  const [deleting, setDeleting] = useState<DeviceGroup | null>(null);
  const [adding, setAdding] = useState<number | null>(null);
  const [reload, setReload] = useState(0);
  const manageGroups = can("groups.manage");
  const manageDevices = can("devices.manage");

  const load = useCallback(() => {
    get<DeviceGroup[]>("/groups")
      .then(setGroups)
      .catch((e) => setError(errorText(e)));
    get<PatchPolicies>("/patches/policies")
      .then((p) => setPolicies(p.policies))
      .catch(() => setPolicies([]));
  }, []);
  useEffect(load, [load, reload]);
  const policyName = (id: number | null) => policies.find((p) => p.id === id)?.name;

  return (
    <div className="space-y-6">
      <PageHeader
        title={t("groups.title")}
        description={t("groups.subtitle")}
        actions={
          manageGroups && (
            <Button icon={<Plus className="size-4" />} onClick={() => setEditing("new")}>
              {t("groups.add")}
            </Button>
          )
        }
      />
      {error && <Alert tone="danger">{error}</Alert>}
      <Card>
        {!groups ? (
          <Spinner />
        ) : (
          <div className="-mx-5 -my-5 overflow-x-auto">
            <table className="w-full min-w-[760px] text-sm">
              <thead className="border-b border-line text-left text-xs text-muted uppercase">
                <tr>
                  <th className="px-5 py-3 font-medium">{t("groups.name")}</th>
                  <th className="px-3 py-3 font-medium">{t("groups.devices")}</th>
                  <th className="px-3 py-3 font-medium">{t("groups.ring")}</th>
                  <th className="px-3 py-3 font-medium">{t("groups.channel")}</th>
                  <th className="px-3 py-3 font-medium">{t("groups.window")}</th>
                  <th className="px-3 py-3 font-medium">{t("groups.patchPolicy")}</th>
                  <th className="px-3 py-3 font-medium">{t("groups.remoteConsent")}</th>
                  <th className="px-5 py-3 text-right font-medium">
                    <span className="sr-only">{t("common.actions")}</span>
                  </th>
                </tr>
              </thead>
              <tbody className="divide-y divide-line">
                {groups.map((g) => (
                  <tr key={g.id}>
                    <td className="px-5 py-3">
                      <Link to={`/devices?group=${g.id}`} className="flex items-center gap-2 font-medium hover:text-primary">
                        <FolderTree className="size-4 text-muted" /> {g.name}
                      </Link>
                      {g.description && <div className="mt-0.5 text-xs text-muted">{g.description}</div>}
                    </td>
                    <td className="px-3 py-3">
                      {g.deviceCount} <span className="text-xs text-muted">({t("groups.onlineCount", { count: g.onlineCount })})</span>
                    </td>
                    <td className="px-3 py-3">
                      <Badge tone={g.updateRing === 0 ? "warning" : "neutral"}>{t(`groups.rings.${g.updateRing}`)}</Badge>
                    </td>
                    <td className="px-3 py-3">{g.agentChannel ? t(`info.channels.${g.agentChannel}`) : t("groups.followServer")}</td>
                    <td className="px-3 py-3 text-muted">
                      {g.windowStart != null && g.windowEnd != null ? `${pad(g.windowStart)}:00 – ${pad(g.windowEnd)}:00` : t("groups.anyTime")}
                    </td>
                    <td className="px-3 py-3">
                      {policyName(g.patchPolicyId) ? (
                        <Link to="/patches?tab=policies" className="hover:text-primary">
                          {policyName(g.patchPolicyId)}
                        </Link>
                      ) : (
                        <span className="text-muted">{t("groups.noPatchPolicy")}</span>
                      )}
                    </td>
                    <td className="px-3 py-3 text-muted">{t(`groups.consent.${g.remoteConsent ?? "auto"}`)}</td>
                    <td className="px-5 py-3">
                      <div className="flex justify-end gap-1">
                        {manageDevices && (
                          <Button variant="ghost" size="sm" icon={<Plus className="size-4" />} onClick={() => setAdding(g.id)}>
                            {t("addClient.button")}
                          </Button>
                        )}
                        {manageGroups && (
                          <>
                            <Button
                              variant="ghost"
                              size="sm"
                              aria-label={t("common.edit")}
                              title={t("common.edit")}
                              icon={<Pencil className="size-4" />}
                              onClick={() => setEditing(g)}
                            />
                            <Button
                              variant="ghost"
                              size="sm"
                              aria-label={t("common.delete")}
                              title={g.deviceCount > 0 ? t("groups.notEmpty") : t("common.delete")}
                              disabled={g.deviceCount > 0}
                              icon={<Trash2 className="size-4" />}
                              onClick={() => setDeleting(g)}
                            />
                          </>
                        )}
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Card>
      {manageDevices && <Tokens reload={reload} />}

      <GroupForm
        editing={editing}
        policies={policies}
        onClose={() => setEditing(null)}
        onSaved={() => {
          setEditing(null);
          setReload((r) => r + 1);
        }}
      />
      <Modal
        open={!!deleting}
        onClose={() => setDeleting(null)}
        title={t("groups.deleteTitle")}
        footer={
          <>
            <Button variant="secondary" onClick={() => setDeleting(null)}>
              {t("common.cancel")}
            </Button>
            <Button
              variant="danger"
              onClick={() =>
                deleting &&
                del(`/groups/${deleting.id}`)
                  .then(() => {
                    setDeleting(null);
                    setReload((r) => r + 1);
                  })
                  .catch((e) => {
                    setDeleting(null);
                    setError(errorText(e));
                  })
              }
            >
              {t("common.delete")}
            </Button>
          </>
        }
      >
        <p className="text-sm">{t("groups.deleteConfirm", { name: deleting?.name })}</p>
      </Modal>
      <AddClient
        open={adding !== null}
        defaultGroup={adding ?? undefined}
        onClose={() => {
          setAdding(null);
          setReload((r) => r + 1);
        }}
      />
    </div>
  );
}

const pad = (n: number) => String(n).padStart(2, "0");

function GroupForm({
  editing,
  policies,
  onClose,
  onSaved,
}: {
  editing: DeviceGroup | "new" | null;
  policies: PatchPolicies["policies"];
  onClose: () => void;
  onSaved: () => void;
}) {
  const { t } = useTranslation();
  const errorText = useErrorText();
  const group = editing && editing !== "new" ? editing : null;
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [ring, setRing] = useState(1);
  const [channel, setChannel] = useState<"" | Channel>("");
  const [windowOn, setWindowOn] = useState(false);
  const [start, setStart] = useState(22);
  const [end, setEnd] = useState(6);
  const [policy, setPolicy] = useState(0);
  const [consent, setConsent] = useState<RemoteConsentSetting>("auto");
  const [unattended, setUnattended] = useState(false);
  const [inventory, setInventory] = useState<number | null>(null);
  const [patchScan, setPatchScan] = useState<number | null>(null);
  const [defaults, setDefaults] = useState<ScanSettings | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    setName(group?.name ?? "");
    setDescription(group?.description ?? "");
    setRing(group?.updateRing ?? 1);
    setChannel(group?.agentChannel ?? "");
    setWindowOn(group?.windowStart != null);
    setStart(group?.windowStart ?? 22);
    setEnd(group?.windowEnd ?? 6);
    setPolicy(group?.patchPolicyId ?? 0);
    setConsent(group?.remoteConsent ?? "auto");
    setUnattended(group?.remoteUnattended ?? false);
    setInventory(group?.inventoryMinutes ?? null);
    setPatchScan(group?.patchScanMinutes ?? null);
    setError(null);
    // Only shown as a hint, so a user without access to Settings just misses it.
    if (editing)
      get<ScanSettings>("/settings/scans")
        .then(setDefaults)
        .catch(() => setDefaults(null));
  }, [editing]);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(null);
    const body = {
      name,
      description,
      updateRing: ring,
      agentChannel: channel || null,
      windowStart: windowOn ? start : null,
      windowEnd: windowOn ? end : null,
      patchPolicyId: policy || null,
      remoteConsent: consent,
      remoteUnattended: unattended,
      inventoryMinutes: inventory,
      patchScanMinutes: patchScan,
    };
    try {
      if (group) await put(`/groups/${group.id}`, body);
      else await post("/groups", body);
      onSaved();
    } catch (err) {
      setError(errorText(err));
    } finally {
      setBusy(false);
    }
  };

  const hours = Array.from({ length: 24 }, (_, i) => i);
  return (
    <Modal
      open={!!editing}
      onClose={onClose}
      title={group ? t("groups.editTitle") : t("groups.add")}
      footer={
        <>
          <Button variant="secondary" onClick={onClose}>
            {t("common.cancel")}
          </Button>
          <Button type="submit" form="group-form" loading={busy}>
            {t("common.save")}
          </Button>
        </>
      }
    >
      <form id="group-form" onSubmit={submit} className="space-y-4">
        {error && <Alert tone="danger">{error}</Alert>}
        <Field label={t("groups.name")}>{(id) => <Input id={id} required maxLength={100} value={name} onChange={(e) => setName(e.target.value)} />}</Field>
        <Field label={t("groups.description")}>
          {(id) => <Input id={id} maxLength={500} value={description} onChange={(e) => setDescription(e.target.value)} />}
        </Field>
        <div className="grid gap-4 sm:grid-cols-2">
          <Field label={t("groups.ring")} hint={t("groups.ringHint")}>
            {(id) => (
              <Select id={id} value={ring} onChange={(e) => setRing(Number(e.target.value))}>
                {[0, 1, 2, 3].map((r) => (
                  <option key={r} value={r}>
                    {t(`groups.rings.${r}`)}
                  </option>
                ))}
              </Select>
            )}
          </Field>
          <Field label={t("groups.channel")} hint={t("groups.channelHint")}>
            {(id) => (
              <Select id={id} value={channel} onChange={(e) => setChannel(e.target.value as "" | Channel)}>
                <option value="">{t("groups.followServer")}</option>
                <option value="production">{t("info.channels.production")}</option>
                <option value="test">{t("info.channels.test")}</option>
              </Select>
            )}
          </Field>
        </div>
        <div className="space-y-2">
          <label className="flex items-center gap-2 text-sm font-medium">
            <input type="checkbox" className="size-4 accent-primary" checked={windowOn} onChange={(e) => setWindowOn(e.target.checked)} />
            {t("groups.windowLimit")}
          </label>
          {windowOn && (
            <div className="flex items-center gap-2 text-sm">
              <Select value={start} onChange={(e) => setStart(Number(e.target.value))} className="w-24" aria-label={t("groups.windowStart")}>
                {hours.map((h) => (
                  <option key={h} value={h}>
                    {pad(h)}:00
                  </option>
                ))}
              </Select>
              <span>–</span>
              <Select value={end} onChange={(e) => setEnd(Number(e.target.value))} className="w-24" aria-label={t("groups.windowEnd")}>
                {hours.map((h) => (
                  <option key={h} value={h}>
                    {pad(h)}:00
                  </option>
                ))}
              </Select>
            </div>
          )}
          <p className="text-xs text-muted">{t("groups.windowHint")}</p>
        </div>
        <Field label={t("groups.patchPolicy")} hint={t("groups.patchPolicyHint")}>
          {(id) => (
            <Select id={id} value={policy} onChange={(e) => setPolicy(Number(e.target.value))}>
              <option value={0}>{t("groups.noPatchPolicy")}</option>
              {policies.map((p) => (
                <option key={p.id} value={p.id}>
                  {p.name}
                </option>
              ))}
            </Select>
          )}
        </Field>
        <section className="space-y-3">
          <h3 className="text-sm font-semibold">{t("groups.scans")}</h3>
          <IntervalOverride
            label={t("groups.overrideInventory")}
            value={inventory}
            fallback={defaults?.inventoryMinutes ?? 360}
            hint={
              defaults &&
              t("groups.scanDefault", {
                value: formatInterval(defaults.inventoryMinutes, t),
              })
            }
            onChange={setInventory}
          />
          <IntervalOverride
            label={t("groups.overridePatchScan")}
            value={patchScan}
            fallback={defaults?.patchScanMinutes ?? 360}
            hint={
              defaults &&
              t("groups.scanDefault", {
                value: formatInterval(defaults.patchScanMinutes, t),
              })
            }
            onChange={setPatchScan}
          />
        </section>
        <Field label={t("groups.remoteConsent")} hint={t(`groups.consentHint.${consent}`)}>
          {(id) => (
            <Select id={id} value={consent} onChange={(e) => setConsent(e.target.value as RemoteConsentSetting)}>
              {(["auto", "ask", "notify", "direct"] as RemoteConsentSetting[]).map((c) => (
                <option key={c} value={c}>
                  {t(`groups.consent.${c}`)}
                </option>
              ))}
            </Select>
          )}
        </Field>
        {consent !== "direct" && consent !== "notify" && (
          <Switch checked={unattended} onChange={setUnattended} label={t("groups.unattended")} description={t("groups.unattendedHint")} />
        )}
      </form>
    </Modal>
  );
}

function IntervalOverride({
  label,
  value,
  fallback,
  hint,
  onChange,
}: {
  label: string;
  value: number | null;
  fallback: number;
  hint?: string | null;
  onChange: (v: number | null) => void;
}) {
  return (
    <div className="space-y-2">
      <label className="flex items-center gap-2 text-sm font-medium">
        <input type="checkbox" className="size-4 accent-primary" checked={value != null} onChange={(e) => onChange(e.target.checked ? fallback : null)} />
        {label}
      </label>
      {value != null && <IntervalInput value={value} onChange={onChange} />}
      {hint && <p className="text-xs text-muted">{hint}</p>}
    </div>
  );
}

function Tokens({ reload }: { reload: number }) {
  const { t, i18n } = useTranslation();
  const errorText = useErrorText();
  const [tokens, setTokens] = useState<EnrollmentToken[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [showing, setShowing] = useState<EnrollmentToken | null>(null);
  const [revoking, setRevoking] = useState<EnrollmentToken | null>(null);
  const [n, setN] = useState(0);
  useEffect(() => {
    get<EnrollmentToken[]>("/enrollment-tokens")
      .then(setTokens)
      .catch((e) => setError(errorText(e)));
  }, [reload, n]);

  const state = (tk: EnrollmentToken) => {
    if (tk.revokedAt) return <Badge tone="danger">{t("tokens.revoked")}</Badge>;
    if (tk.expiresAt && new Date(tk.expiresAt) < new Date()) return <Badge>{t("tokens.expired")}</Badge>;
    if (tk.maxUses != null && tk.uses >= tk.maxUses) return <Badge>{t("tokens.usedUp")}</Badge>;
    return <Badge tone="success">{t("tokens.active")}</Badge>;
  };

  return (
    <Card title={t("tokens.title")} description={t("tokens.subtitle")}>
      {error && <Alert tone="danger">{error}</Alert>}
      {!tokens ? (
        <Spinner />
      ) : tokens.length === 0 ? (
        <p className="rounded-md border border-dashed border-line px-4 py-6 text-center text-sm text-muted">{t("tokens.empty")}</p>
      ) : (
        <div className="-mx-5 -my-5 overflow-x-auto">
          <table className="w-full min-w-[760px] text-sm">
            <thead className="border-b border-line text-left text-xs text-muted uppercase">
              <tr>
                <th className="px-5 py-3 font-medium">{t("tokens.token")}</th>
                <th className="px-3 py-3 font-medium">{t("devices.group")}</th>
                <th className="px-3 py-3 font-medium">{t("tokens.state")}</th>
                <th className="px-3 py-3 font-medium">{t("tokens.uses")}</th>
                <th className="px-3 py-3 font-medium">{t("tokens.expires")}</th>
                <th className="px-5 py-3 text-right font-medium">
                  <span className="sr-only">{t("common.actions")}</span>
                </th>
              </tr>
            </thead>
            <tbody className="divide-y divide-line">
              {tokens.map((tk) => (
                <tr key={tk.id} className={tk.revokedAt ? "opacity-60" : ""}>
                  <td className="px-5 py-3">
                    <code className="font-mono text-xs">{tk.prefix}…</code>
                    <div className="text-xs text-muted">
                      {tk.description} · {tk.createdBy}, {formatDate(tk.createdAt, i18n.language)}
                    </div>
                  </td>
                  <td className="px-3 py-3">{tk.groupName}</td>
                  <td className="px-3 py-3">{state(tk)}</td>
                  <td className="px-3 py-3">
                    {tk.uses}
                    {tk.maxUses != null && ` / ${tk.maxUses}`}
                  </td>
                  <td className="px-3 py-3 text-muted">{tk.expiresAt ? formatDate(tk.expiresAt, i18n.language) : t("tokens.never")}</td>
                  <td className="px-5 py-3">
                    <div className="flex justify-end gap-1">
                      {!tk.revokedAt && (
                        <>
                          <Button variant="ghost" size="sm" icon={<KeyRound className="size-4" />} onClick={() => setShowing(tk)}>
                            {t("tokens.show")}
                          </Button>
                          <Button
                            variant="ghost"
                            size="sm"
                            aria-label={t("tokens.revoke")}
                            title={t("tokens.revoke")}
                            icon={<Ban className="size-4" />}
                            onClick={() => setRevoking(tk)}
                          />
                        </>
                      )}
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      <AddClient open={!!showing} token={showing} onClose={() => setShowing(null)} />
      <Modal
        open={!!revoking}
        onClose={() => setRevoking(null)}
        title={t("tokens.revokeTitle")}
        footer={
          <>
            <Button variant="secondary" onClick={() => setRevoking(null)}>
              {t("common.cancel")}
            </Button>
            <Button
              variant="danger"
              onClick={() =>
                revoking &&
                del(`/enrollment-tokens/${revoking.id}`)
                  .then(() => {
                    setRevoking(null);
                    setN((x) => x + 1);
                  })
                  .catch((e) => setError(errorText(e)))
              }
            >
              {t("tokens.revoke")}
            </Button>
          </>
        }
      >
        <p className="text-sm">{t("tokens.revokeConfirm")}</p>
      </Modal>
    </Card>
  );
}
