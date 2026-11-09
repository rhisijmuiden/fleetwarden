import { useEffect, useState, type ReactNode } from "react";
import { Link } from "react-router";
import { useTranslation } from "react-i18next";
import { Pencil } from "lucide-react";
import { get, type Device, type DeviceAsset, type ReportSettings } from "../../api";
import { useAuth } from "../../auth";
import { Button, Card } from "../../components/ui";
import { AssetForm, WarrantyBadge, emptyAsset, formatDay } from "./util";

// DeviceAssetCard shows owner, location and warranty on the device page.
export function DeviceAssetCard({ device }: { device: Device }) {
  const { t, i18n } = useTranslation();
  const { can } = useAuth();
  const [asset, setAsset] = useState<DeviceAsset | null>(null);
  const [days, setDays] = useState(90);
  const [editing, setEditing] = useState(false);

  useEffect(() => {
    get<DeviceAsset>(`/devices/${device.id}/asset`)
      .then(setAsset)
      .catch(() => setAsset(emptyAsset));
    get<ReportSettings>("/reports/settings")
      .then((s) => setDays(s.expiringDays || 90))
      .catch(() => {});
  }, [device.id]);

  const a = asset ?? emptyAsset;
  const rows: [string, ReactNode][] = [
    [t("assets.fields.owner"), a.owner],
    [t("assets.fields.assetTag"), a.assetTag],
    [t("assets.fields.location"), a.location],
    [t("assets.fields.department"), a.department],
    [t("assets.fields.supplier"), [a.supplier, a.orderNumber && `(${a.orderNumber})`].filter(Boolean).join(" ")],
    [t("assets.fields.purchaseDate"), a.purchaseDate && formatDay(a.purchaseDate, i18n.language)],
    [
      t("assets.fields.warrantyEnd"),
      a.warrantyEnd ? (
        <span className="inline-flex flex-wrap items-center gap-2">
          {formatDay(a.warrantyEnd, i18n.language)} <WarrantyBadge end={a.warrantyEnd} expiringDays={days} />
        </span>
      ) : (
        ""
      ),
    ],
  ];

  return (
    <Card
      title={
        <Link to={`/asset-management?search=${encodeURIComponent(device.hostname)}`} className="hover:underline">
          {t("assets.card")}
        </Link>
      }
      actions={
        can("assets.manage") && (
          <Button size="sm" variant="secondary" icon={<Pencil className="size-4" />} onClick={() => setEditing(true)} disabled={!asset}>
            {t("common.edit")}
          </Button>
        )
      }
    >
      <dl className="grid grid-cols-[minmax(110px,40%)_1fr] gap-x-4 gap-y-2.5 text-sm">
        {rows.map(([k, v]) => (
          <div key={k} className="contents">
            <dt className="text-muted">{k}</dt>
            <dd className="min-w-0 break-words">{v || "–"}</dd>
          </div>
        ))}
      </dl>
      {editing && (
        <AssetForm deviceId={device.id} initial={a} loggedInUser={device.loggedInUser} onClose={() => setEditing(false)} onSaved={setAsset} />
      )}
    </Card>
  );
}
