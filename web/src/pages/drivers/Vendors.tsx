import { useCallback, useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { Check, FileUp, Pencil, Plus, RefreshCw, Trash2 } from "lucide-react";
import {
  del,
  get,
  post,
  put,
  uploadFile,
  type DriverKind,
  type DriverUpdate,
  type DriverUploadReq,
  type Platform,
  type VendorStatus,
} from "../../api";
import { useAuth } from "../../auth";
import {
  Alert,
  Badge,
  Button,
  Card,
  Field,
  Input,
  Modal,
  Select,
  Spinner,
  Switch,
  formatDate,
  useErrorText,
} from "../../components/ui";
import { PlatformBadge, formatBytes, timeAgo } from "../devices/util";
import { KindBadge } from "./util";

const vendorNames: Record<string, string> = {
  dell: "Dell",
  hp: "HP",
  lenovo: "Lenovo",
};

// VendorCatalogs shows which vendor BIOS catalogs the server reads and lets
// an administrator switch them off or read them again.
export function VendorCatalogs({ onChanged }: { onChanged: () => void }) {
  const { t, i18n } = useTranslation();
  const { can } = useAuth();
  const errorText = useErrorText();
  const [st, setSt] = useState<VendorStatus | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const manage = can("drivers.manage");

  const load = useCallback(() => {
    get<VendorStatus>("/drivers/vendors")
      .then(setSt)
      .catch((e) => setError(errorText(e)));
  }, []);
  useEffect(() => {
    load();
  }, [load]);
  useEffect(() => {
    if (!st?.running) return;
    const id = setInterval(load, 3000);
    return () => clearInterval(id);
  }, [st?.running, load]);

  if (!st) return error ? <Alert tone="danger">{error}</Alert> : <Spinner />;

  const toggle = (vendor: "dell" | "hp" | "lenovo", on: boolean) => {
    setError(null);
    put<VendorStatus>("/drivers/vendors", { ...st.settings, [vendor]: on })
      .then((v) => {
        setSt(v);
        onChanged();
      })
      .catch((e) => setError(errorText(e)));
  };
  const sync = () => {
    setBusy(true);
    setError(null);
    post<VendorStatus>("/drivers/vendors/sync")
      .then((v) => {
        setSt(v);
        onChanged();
      })
      .catch((e) => setError(errorText(e)))
      .finally(() => setBusy(false));
  };
  const unmatched = Object.entries(st.unmatched).filter(([, n]) => n > 0);

  return (
    <div className="space-y-6">
      {error && <Alert tone="danger">{error}</Alert>}
      <div className="grid gap-6 lg:grid-cols-3">
        <Card
          title={t("drivers.vendors.title")}
          description={t("drivers.vendors.hint")}
          className="lg:col-span-2"
        >
          <div className="space-y-4">
            {(["dell", "hp", "lenovo"] as const).map((v) => (
              <div
                key={v}
                className={manage ? "" : "pointer-events-none opacity-70"}
              >
                <Switch
                  checked={st.settings[v]}
                  onChange={(on) => toggle(v, on)}
                  label={vendorNames[v]}
                  description={t(`drivers.vendors.about.${v}`)}
                />
              </div>
            ))}
          </div>
        </Card>
        <Card
          title={t("drivers.vendors.status")}
          actions={
            manage && (
              <Button
                size="sm"
                variant="secondary"
                icon={<RefreshCw className="size-4" />}
                loading={busy || st.running}
                onClick={sync}
              >
                {t("drivers.vendors.syncNow")}
              </Button>
            )
          }
        >
          <dl className="space-y-3 text-sm">
            <div>
              <dt className="text-xs text-muted">
                {t("drivers.vendors.lastRun")}
              </dt>
              <dd>
                {st.lastRun
                  ? `${formatDate(st.lastRun, i18n.language)} (${timeAgo(st.lastRun, i18n.language)})`
                  : t("drivers.vendors.notYet")}
              </dd>
            </div>
            <div>
              <dt className="text-xs text-muted">
                {t("drivers.vendors.models")}
              </dt>
              <dd>{st.catalogs.length}</dd>
            </div>
          </dl>
          {st.lastError && (
            <div className="mt-3">
              <Alert tone="warning">
                <span className="text-xs break-all">{st.lastError}</span>
              </Alert>
            </div>
          )}
          <p className="mt-3 text-xs text-muted">
            {t("drivers.vendors.daily")}
          </p>
        </Card>
      </div>

      {unmatched.length > 0 && (
        <Alert tone="info">
          {t("drivers.vendors.unmatched", {
            list: unmatched
              .map(([v, n]) => `${vendorNames[v] ?? v}: ${n}`)
              .join(", "),
          })}
        </Alert>
      )}

      <Card
        title={t("drivers.vendors.catalogs")}
        description={t("drivers.vendors.catalogsHint")}
      >
        {st.catalogs.length === 0 ? (
          <p className="rounded-md border border-dashed border-line px-4 py-6 text-center text-sm text-muted">
            {t("drivers.vendors.none")}
          </p>
        ) : (
          <div className="-mx-5 -my-5 overflow-x-auto">
            <table className="w-full min-w-[640px] text-sm">
              <thead className="border-b border-line text-left text-xs text-muted uppercase">
                <tr>
                  <th className="px-5 py-3 font-medium">
                    {t("drivers.vendor")}
                  </th>
                  <th className="px-3 py-3 font-medium">
                    {t("drivers.vendors.model")}
                  </th>
                  <th className="px-3 py-3 font-medium">
                    {t("drivers.devicesCol")}
                  </th>
                  <th className="px-3 py-3 font-medium">
                    {t("drivers.vendors.biosReleases")}
                  </th>
                  <th className="px-5 py-3 font-medium">
                    {t("drivers.vendors.fetched")}
                  </th>
                </tr>
              </thead>
              <tbody className="divide-y divide-line">
                {st.catalogs.map((c) => (
                  <tr key={c.vendor + c.key}>
                    <td className="px-5 py-2 font-medium">
                      {vendorNames[c.vendor]}
                    </td>
                    <td className="px-3 py-2 font-mono text-xs">{c.key}</td>
                    <td className="px-3 py-2 tabular-nums">{c.devices}</td>
                    <td className="px-3 py-2 tabular-nums">{c.items}</td>
                    <td className="px-5 py-2 text-xs">
                      {c.fetchedAt ? timeAgo(c.fetchedAt, i18n.language) : "–"}
                      {c.error && (
                        <div
                          className="mt-0.5 max-w-[320px] truncate text-warning"
                          title={c.error}
                        >
                          {c.error}
                        </div>
                      )}
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

// UploadList shows the packages administrators uploaded themselves.
export function UploadList({ onChanged }: { onChanged: () => void }) {
  const { t, i18n } = useTranslation();
  const { can } = useAuth();
  const errorText = useErrorText();
  const [list, setList] = useState<DriverUpdate[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [edit, setEdit] = useState<DriverUpdate | "new" | null>(null);
  const [n, setN] = useState(0);
  const manage = can("drivers.manage");

  useEffect(() => {
    get<DriverUpdate[]>("/drivers?source=upload")
      .then(setList)
      .catch((e) => setError(errorText(e)));
  }, [n]);

  const saved = () => {
    setEdit(null);
    setN((x) => x + 1);
    onChanged();
  };

  return (
    <Card
      title={t("drivers.uploads.title")}
      description={t("drivers.uploads.hint")}
      actions={
        manage && (
          <Button
            size="sm"
            icon={<Plus className="size-4" />}
            onClick={() => setEdit("new")}
          >
            {t("drivers.uploads.add")}
          </Button>
        )
      }
    >
      {error && <Alert tone="danger">{error}</Alert>}
      {!list ? (
        <Spinner />
      ) : list.length === 0 ? (
        <p className="rounded-md border border-dashed border-line px-4 py-6 text-center text-sm text-muted">
          {t("drivers.uploads.empty")}
        </p>
      ) : (
        <div className="-mx-5 -my-5 overflow-x-auto">
          <table className="w-full min-w-[760px] text-sm">
            <thead className="border-b border-line text-left text-xs text-muted uppercase">
              <tr>
                <th className="px-5 py-3 font-medium">{t("drivers.update")}</th>
                <th className="px-3 py-3 font-medium">{t("drivers.kind")}</th>
                <th className="px-3 py-3 font-medium">
                  {t("drivers.uploads.target")}
                </th>
                <th className="px-3 py-3 font-medium">
                  {t("drivers.devicesCol")}
                </th>
                <th className="px-3 py-3 font-medium">{t("drivers.size")}</th>
                <th className="px-5 py-3 font-medium">
                  <span className="sr-only">{t("drivers.uploads.edit")}</span>
                </th>
              </tr>
            </thead>
            <tbody className="divide-y divide-line">
              {list.map((u) => (
                <tr key={u.id}>
                  <td className="px-5 py-2">
                    <div className="font-medium">{u.title}</div>
                    <div className="text-xs text-muted">
                      {[
                        u.version,
                        u.files[0]?.name,
                        t(`drivers.installers.${u.installer || "inf"}`),
                      ]
                        .filter(Boolean)
                        .join(" · ")}
                    </div>
                    {u.state === "failed" && (
                      <div className="text-xs text-danger">{u.error}</div>
                    )}
                  </td>
                  <td className="px-3 py-2">
                    <KindBadge kind={u.kind} />
                  </td>
                  <td className="px-3 py-2 text-xs">
                    <PlatformBadge platform={u.platform} />
                    <span className="ml-1.5 text-muted">
                      {targetText(u, t)}
                    </span>
                  </td>
                  <td className="px-3 py-2 tabular-nums">{u.devices}</td>
                  <td className="px-3 py-2 whitespace-nowrap text-muted">
                    {formatBytes(u.sizeBytes, i18n.language)}
                  </td>
                  <td className="px-5 py-2 text-right">
                    {manage && (
                      <Button
                        size="sm"
                        variant="ghost"
                        icon={<Pencil className="size-4" />}
                        onClick={() => setEdit(u)}
                      >
                        {t("drivers.uploads.edit")}
                      </Button>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {edit && (
        <UploadDialog
          upload={edit === "new" ? null : edit}
          onClose={() => setEdit(null)}
          onSaved={saved}
        />
      )}
    </Card>
  );
}

function targetText(u: DriverUpdate, t: (k: string) => string) {
  const parts = [u.target.manufacturer, u.target.model].filter(Boolean);
  return parts.length ? parts.join(" · ") : t("drivers.uploads.allDevices");
}

const blank: DriverUploadReq = {
  platform: "windows",
  kind: "driver",
  title: "",
  version: "",
  vendor: "",
  description: "",
  reboot: false,
  command: "",
  manufacturer: "",
  model: "",
};

function UploadDialog({
  upload,
  onClose,
  onSaved,
}: {
  upload: DriverUpdate | null;
  onClose: () => void;
  onSaved: () => void;
}) {
  const { t, i18n } = useTranslation();
  const errorText = useErrorText();
  const input = useRef<HTMLInputElement>(null);
  const [f, setF] = useState<DriverUploadReq & { size?: number }>(
    upload
      ? {
          platform: upload.platform,
          kind: upload.kind,
          title: upload.title,
          version: upload.version,
          vendor: upload.vendor,
          description: upload.description,
          reboot: upload.reboot,
          command: upload.command,
          manufacturer: upload.target.manufacturer,
          model: upload.target.model,
          name: upload.files[0]?.name,
          sha256: upload.files[0]?.stored,
          size: upload.sizeBytes,
        }
      : blank,
  );
  const [progress, setProgress] = useState<number | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const set = (p: Partial<typeof f>) => setF((x) => ({ ...x, ...p }));
  const ext = (f.name ?? "").toLowerCase().split(".").pop();
  const isExe = ext === "exe";

  const pick = async (file: File | undefined) => {
    if (!file) return;
    setError(null);
    setProgress(0);
    try {
      const r = await uploadFile<{
        sha256: string;
        size: number;
        name: string;
      }>("/drivers/uploads/file", file, setProgress);
      const e = r.name.toLowerCase().split(".").pop();
      set({
        sha256: r.sha256,
        name: r.name,
        size: r.size,
        title: f.title || r.name.replace(/\.(zip|cab|exe)$/i, ""),
        platform: e === "zip" || e === "exe" ? "windows" : f.platform,
      });
    } catch (err) {
      setError(errorText(err));
    } finally {
      setProgress(null);
    }
  };
  const save = () => {
    setBusy(true);
    setError(null);
    const body = { ...f };
    delete body.size;
    (upload
      ? put(`/drivers/uploads/${upload.id}`, body)
      : post("/drivers/uploads", body)
    )
      .then(onSaved)
      .catch((e) => setError(errorText(e)))
      .finally(() => setBusy(false));
  };
  const remove = () => {
    if (
      !upload ||
      !confirm(t("drivers.uploads.confirmDelete", { title: upload.title }))
    )
      return;
    setBusy(true);
    del(`/drivers/uploads/${upload.id}`)
      .then(onSaved)
      .catch((e) => setError(errorText(e)))
      .finally(() => setBusy(false));
  };

  return (
    <Modal
      open
      onClose={onClose}
      wide
      title={upload ? upload.title : t("drivers.uploads.add")}
      footer={
        <>
          {upload && (
            <Button
              variant="danger"
              icon={<Trash2 className="size-4" />}
              onClick={remove}
              disabled={busy}
            >
              {t("drivers.uploads.delete")}
            </Button>
          )}
          <Button variant="secondary" onClick={onClose}>
            {t("common.cancel")}
          </Button>
          <Button
            onClick={save}
            loading={busy}
            disabled={!f.sha256 || !f.title.trim()}
          >
            {t("common.save")}
          </Button>
        </>
      }
    >
      <div className="space-y-4">
        {error && <Alert tone="danger">{error}</Alert>}
        {!upload && (
          <div
            onDragOver={(e) => e.preventDefault()}
            onDrop={(e) => {
              e.preventDefault();
              pick(e.dataTransfer.files[0]);
            }}
            className="flex flex-col items-center gap-2 rounded-lg border-2 border-dashed border-line px-4 py-6 text-center"
          >
            {f.sha256 ? (
              <>
                <Check className="size-6 text-success" />
                <div className="font-medium">{f.name}</div>
                <div className="font-mono text-xs break-all text-muted">
                  {formatBytes(f.size ?? 0, i18n.language)} · SHA-256 {f.sha256}
                </div>
              </>
            ) : (
              <>
                <FileUp className="size-6 text-muted" />
                <div className="text-sm text-muted">
                  {t("drivers.uploads.drop")}
                </div>
              </>
            )}
            {progress != null ? (
              <div className="h-1.5 w-64 overflow-hidden rounded-full bg-surface-muted">
                <div
                  className="h-full bg-primary"
                  style={{ width: `${Math.round(progress * 100)}%` }}
                />
              </div>
            ) : (
              <Button
                size="sm"
                variant="secondary"
                onClick={() => input.current?.click()}
              >
                {f.sha256
                  ? t("drivers.uploads.other")
                  : t("drivers.uploads.choose")}
              </Button>
            )}
            <input
              ref={input}
              type="file"
              accept=".zip,.cab,.exe"
              className="hidden"
              onChange={(e) => pick(e.target.files?.[0])}
            />
          </div>
        )}
        <div className="grid gap-4 sm:grid-cols-2">
          <Field label={t("drivers.uploads.name")}>
            {(id) => (
              <Input
                id={id}
                value={f.title}
                onChange={(e) => set({ title: e.target.value })}
              />
            )}
          </Field>
          <Field label={t("drivers.version")}>
            {(id) => (
              <Input
                id={id}
                value={f.version}
                onChange={(e) => set({ version: e.target.value })}
              />
            )}
          </Field>
          <Field
            label={t("drivers.uploads.platform")}
            hint={t("drivers.uploads.platformHint")}
          >
            {(id) => (
              <Select
                id={id}
                value={f.platform}
                disabled={!!upload || ext === "zip" || isExe}
                onChange={(e) => set({ platform: e.target.value as Platform })}
              >
                <option value="windows">Windows</option>
                <option value="linux">Linux (fwupd)</option>
              </Select>
            )}
          </Field>
          <Field label={t("drivers.kind")}>
            {(id) => (
              <Select
                id={id}
                value={f.kind}
                onChange={(e) => set({ kind: e.target.value as DriverKind })}
              >
                <option value="driver">{t("drivers.kinds.driver")}</option>
                <option value="firmware">{t("drivers.kinds.firmware")}</option>
              </Select>
            )}
          </Field>
          <Field
            label={t("drivers.uploads.manufacturer")}
            hint={t("drivers.uploads.manufacturerHint")}
          >
            {(id) => (
              <Input
                id={id}
                value={f.manufacturer}
                placeholder="Dell"
                onChange={(e) => set({ manufacturer: e.target.value })}
              />
            )}
          </Field>
          <Field
            label={t("drivers.uploads.model")}
            hint={t("drivers.uploads.modelHint")}
          >
            {(id) => (
              <Input
                id={id}
                value={f.model}
                placeholder="Latitude 7450"
                onChange={(e) => set({ model: e.target.value })}
              />
            )}
          </Field>
          {isExe && (
            <Field
              label={t("drivers.uploads.arguments")}
              hint={t("drivers.uploads.argumentsHint")}
              className="sm:col-span-2"
            >
              {(id) => (
                <Input
                  id={id}
                  value={f.command}
                  placeholder="/s"
                  className="font-mono"
                  onChange={(e) => set({ command: e.target.value })}
                />
              )}
            </Field>
          )}
          <Field
            label={t("drivers.uploads.description")}
            className="sm:col-span-2"
          >
            {(id) => (
              <Input
                id={id}
                value={f.description}
                onChange={(e) => set({ description: e.target.value })}
              />
            )}
          </Field>
        </div>
        {f.kind === "driver" && (
          <Switch
            checked={f.reboot}
            onChange={(v) => set({ reboot: v })}
            label={t("drivers.uploads.reboot")}
          />
        )}
        <p className="text-xs text-muted">
          {t(
            `drivers.uploads.how.${isExe ? "exe" : f.platform === "linux" ? "fwupd" : "inf"}`,
          )}
        </p>
        {upload && (
          <p className="text-xs text-muted">
            <Badge tone="success">{t("drivers.states.ready")}</Badge>{" "}
            {t("drivers.uploads.uploadedBy", {
              user: upload.decidedBy,
              date: formatDate(upload.decidedAt, i18n.language),
            })}
          </p>
        )}
      </div>
    </Modal>
  );
}
