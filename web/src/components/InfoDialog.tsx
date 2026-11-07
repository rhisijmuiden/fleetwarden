import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { RefreshCw, X } from "lucide-react";
import logo from "../../../branding/logo.svg";
import { fetchVersion, type VersionInfo } from "../api";

type Props = { open: boolean; onClose: () => void };

export function InfoDialog({ open, onClose }: Props) {
  const { t } = useTranslation();
  const ref = useRef<HTMLDialogElement>(null);
  const [info, setInfo] = useState<VersionInfo | null>(null);
  const [error, setError] = useState(false);
  const [checked, setChecked] = useState(false);

  useEffect(() => {
    const d = ref.current;
    if (!d) return;
    if (open && !d.open) d.showModal();
    if (!open && d.open) d.close();
  }, [open]);

  useEffect(() => {
    if (!open) return;
    const ctrl = new AbortController();
    setError(false);
    setChecked(false);
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
    <dialog
      ref={ref}
      onClose={onClose}
      className="m-auto w-[min(440px,calc(100vw-32px))] rounded-lg border border-line bg-surface p-0 text-fg shadow-2xl backdrop:bg-black/40 backdrop:backdrop-blur-sm"
    >
      <div className="flex items-center gap-3 border-b border-line px-5 py-4">
        <img src={logo} alt="" className="size-8" />
        <h2 className="flex-1 text-base font-semibold">{t("info.title")}</h2>
        <button
          type="button"
          onClick={onClose}
          aria-label={t("info.close")}
          className="grid size-8 place-items-center rounded-md text-muted hover:bg-surface-muted hover:text-fg"
        >
          <X className="size-4" />
        </button>
      </div>
      <div className="space-y-4 px-5 py-4">
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
        <button
          type="button"
          onClick={() => setChecked(true)}
          className="inline-flex h-9 items-center gap-2 rounded-md bg-primary px-4 text-sm font-medium text-primary-contrast hover:bg-primary-hover focus-visible:ring-2 focus-visible:ring-primary focus-visible:ring-offset-2 outline-none"
        >
          <RefreshCw className="size-4" />
          {t("info.checkUpdates")}
        </button>
        {checked && <p className="text-sm text-muted">{t("info.updatesSoon")}</p>}
        <p className="border-t border-line pt-3 text-xs text-muted">{t("info.license")}</p>
      </div>
    </dialog>
  );
}
