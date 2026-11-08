import { useCallback, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { Play, SlidersHorizontal } from "lucide-react";
import { get, post, type Device, type DeviceConfigState, type DeviceConfigsView } from "../../api";
import { useAuth } from "../../auth";
import { Alert, Button, Card, Spinner, useErrorText } from "../../components/ui";
import { DeviceStateTable, RunsCard, StateDialog } from "./Configurations";

// The configurations tab of a device.
export function DeviceConfigsTab({ device }: { device: Device }) {
  const { t, i18n } = useTranslation();
  const errorText = useErrorText();
  const { can } = useAuth();
  const [view, setView] = useState<DeviceConfigsView | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [shown, setShown] = useState<DeviceConfigState | null>(null);
  const load = useCallback(() => {
    get<DeviceConfigsView>(`/devices/${device.id}/configurations`)
      .then(setView)
      .catch((e) => setError(errorText(e)));
  }, [device.id]);
  useEffect(() => {
    load();
    const id = setInterval(load, 10000);
    return () => clearInterval(id);
  }, [load]);
  const apply = async () => {
    setBusy(true);
    setError(null);
    try {
      await post(`/devices/${device.id}/configurations/apply`);
      setNotice(t("configs.applySent", { count: 1 }));
      setTimeout(load, 3000);
    } catch (e) {
      setError(errorText(e));
    } finally {
      setBusy(false);
    }
  };
  if (!view) return error ? <Alert tone="danger">{error}</Alert> : <Spinner />;
  const rows = view.items.map((r) => ({ ...r, online: device.online }));
  const names = new Map(view.items.map((r) => [r.configurationId, r.configurationName]));
  return (
    <div className="space-y-6">
      {error && <Alert tone="danger">{error}</Alert>}
      {notice && <Alert tone="success">{notice}</Alert>}
      <Card
        title={t("configs.deviceTitle")}
        description={t("configs.deviceHint")}
        actions={
          can("devices.manage") &&
          rows.length > 0 && (
            <Button variant="secondary" size="sm" icon={<Play className="size-4" />} loading={busy} disabled={!device.online} onClick={apply}>
              {t("configs.applyNow")}
            </Button>
          )
        }
      >
        {rows.length === 0 ? (
          <div className="flex flex-col items-center gap-2 py-6 text-center text-sm text-muted">
            <SlidersHorizontal className="size-6" />
            {t("configs.deviceEmpty")}
          </div>
        ) : (
          <div className="-mx-5 -my-5 overflow-x-auto">
            <DeviceStateTable rows={rows} onShow={setShown} lang={i18n.language} by="configuration" />
          </div>
        )}
      </Card>
      {view.runs.length > 0 && <RunsCard runs={view.runs} configName={(id) => names.get(id) ?? `#${id}`} />}
      <StateDialog row={shown} onClose={() => setShown(null)} />
    </div>
  );
}
