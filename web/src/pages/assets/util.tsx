import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { UserRound } from "lucide-react";
import { assetFields, put, type AssetField, type DeviceAsset, type WarrantyState } from "../../api";
import { Alert, Badge, Button, Field, Input, Modal, useErrorText } from "../../components/ui";

export const emptyAsset: DeviceAsset = {
  assetTag: "",
  owner: "",
  location: "",
  department: "",
  supplier: "",
  orderNumber: "",
  purchaseDate: "",
  warrantyEnd: "",
};

// formatDay shows a YYYY-MM-DD date in the user's language.
export function formatDay(day: string | null | undefined, lang: string): string {
  if (!day) return "–";
  const d = new Date(`${day}T00:00:00Z`);
  if (Number.isNaN(d.getTime())) return day;
  return d.toLocaleDateString(lang === "nl" ? "nl-NL" : "en-GB", { dateStyle: "medium", timeZone: "UTC" });
}

// daysLeft counts calendar days from today to a YYYY-MM-DD date.
export function daysLeft(day: string): number | null {
  if (!day) return null;
  const end = Date.parse(`${day}T00:00:00Z`);
  if (Number.isNaN(end)) return null;
  const now = new Date();
  const today = Date.UTC(now.getFullYear(), now.getMonth(), now.getDate());
  return Math.round((end - today) / 86400000);
}

export function warrantyState(day: string, expiringDays: number): WarrantyState {
  const left = daysLeft(day);
  if (left === null) return "unknown";
  if (left < 0) return "expired";
  if (left <= expiringDays) return "expiring";
  return "valid";
}

const tones: Record<WarrantyState, "danger" | "warning" | "success" | "neutral"> = {
  expired: "danger",
  expiring: "warning",
  valid: "success",
  unknown: "neutral",
};

export function WarrantyBadge({ end, expiringDays }: { end: string; expiringDays: number }) {
  const { t } = useTranslation();
  const state = warrantyState(end, expiringDays);
  const left = daysLeft(end);
  const label =
    state === "unknown"
      ? t("assets.warranty.unknown")
      : state === "expired"
        ? t("assets.warranty.expired")
        : state === "expiring"
          ? t("assets.warranty.daysLeft", { count: left ?? 0 })
          : t("assets.warranty.valid");
  return <Badge tone={tones[state]}>{label}</Badge>;
}

// AssetForm edits the asset details of one device. With a logged-on user
// the owner can be filled in from it.
export function AssetForm({
  deviceId,
  initial,
  loggedInUser,
  onClose,
  onSaved,
}: {
  deviceId: string;
  initial: DeviceAsset;
  loggedInUser?: string;
  onClose: () => void;
  onSaved: (a: DeviceAsset) => void;
}) {
  const { t } = useTranslation();
  const errorText = useErrorText();
  const [d, setD] = useState<DeviceAsset>(initial);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => setD(initial), [initial]);
  const set = (p: Partial<DeviceAsset>) => setD({ ...d, ...p });

  const save = () => {
    setBusy(true);
    setError(null);
    put<DeviceAsset>(`/devices/${deviceId}/asset`, d)
      .then((a) => {
        onSaved(a);
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
      title={t("assets.edit")}
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
        <Field label={t("assets.fields.owner")}>
          {(id) => (
            <div className="flex gap-2">
              <Input id={id} value={d.owner} onChange={(e) => set({ owner: e.target.value })} placeholder={t("assets.ownerPlaceholder")} />
              {loggedInUser && loggedInUser !== d.owner && (
                <Button
                  variant="secondary"
                  icon={<UserRound className="size-4" />}
                  title={t("assets.useLoggedIn", { user: loggedInUser })}
                  aria-label={t("assets.useLoggedIn", { user: loggedInUser })}
                  onClick={() => set({ owner: loggedInUser })}
                />
              )}
            </div>
          )}
        </Field>
        <AssetInput field="assetTag" d={d} set={set} />
        <AssetInput field="location" d={d} set={set} />
        <AssetInput field="department" d={d} set={set} />
        <AssetInput field="supplier" d={d} set={set} />
        <AssetInput field="orderNumber" d={d} set={set} />
        <AssetInput field="purchaseDate" d={d} set={set} />
        <AssetInput field="warrantyEnd" d={d} set={set} />
      </div>
    </Modal>
  );
}

export function AssetInput({ field, d, set }: { field: AssetField; d: DeviceAsset; set: (p: Partial<DeviceAsset>) => void }) {
  const { t } = useTranslation();
  const isDate = field === "purchaseDate" || field === "warrantyEnd";
  return (
    <Field label={t(`assets.fields.${field}`)}>
      {(id) => <Input id={id} type={isDate ? "date" : "text"} maxLength={200} value={d[field]} onChange={(e) => set({ [field]: e.target.value })} />}
    </Field>
  );
}

export { assetFields };
