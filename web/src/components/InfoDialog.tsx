import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { fetchVersion, type VersionInfo } from "../api";
import { useAuth } from "../auth";
import { useBranding } from "../branding";
import { Updates } from "./Updates";
import { Modal } from "./ui";

type Props = { open: boolean; onClose: () => void };

export function InfoDialog({ open, onClose }: Props) {
  const { t } = useTranslation();
  const { can } = useAuth();
  const { branding, logoUrl } = useBranding();
  const [info, setInfo] = useState<VersionInfo | null>(null);
  const [error, setError] = useState(false);

  useEffect(() => {
    if (!open) return;
    const ctrl = new AbortController();
    setError(false);
    fetchVersion(ctrl.signal)
      .then(setInfo)
      .catch((e) => {
        if (e.name !== "AbortError") setError(true);
      });
    return () => ctrl.abort();
  }, [open]);

  const rows: [string, string][] = info
    ? [
        [t("info.version"), info.version],
        [t("info.channel"), t(`info.channels.${info.channel}`, { defaultValue: info.channel })],
        [t("info.commit"), info.commit.slice(0, 12)],
        [t("info.built"), info.buildDate],
        [t("info.platform"), info.platform],
      ]
    : [];

  return (
    <Modal open={open} onClose={onClose} title={t("info.title", { product: branding.productName })} wide={can("system.update")}>
      <div className="flex items-start gap-4">
        <img src={logoUrl} alt="" className="size-10 shrink-0 object-contain" />
        <div className="min-w-0 flex-1">
          {error && <p className="text-sm text-danger">{t("info.unavailable")}</p>}
          {info && (
            <dl className="grid grid-cols-[auto_1fr] gap-x-6 gap-y-2 text-sm">
              {rows.map(([k, v]) => (
                <div key={k} className="contents">
                  <dt className="text-muted">{k}</dt>
                  <dd className="font-medium break-all">{v}</dd>
                </div>
              ))}
            </dl>
          )}
        </div>
      </div>
      {can("system.update") ? (
        <div className="border-t border-line pt-4">
          <h3 className="mb-3 text-sm font-semibold">{t("updates.title")}</h3>
          <Updates compact />
        </div>
      ) : (
        <p className="text-sm text-muted">{t("info.adminsUpdate")}</p>
      )}
      <p className="border-t border-line pt-3 text-xs text-muted">
        {branding.productName !== "FleetWarden" && `${t("info.poweredBy")} `}
        {t("info.license")}
      </p>
    </Modal>
  );
}
