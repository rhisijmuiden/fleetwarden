import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { Power, PowerOff, RotateCcw, Sunrise } from "lucide-react";
import { post, type BulkPowerResult, type PowerActionKind, type PowerRequest } from "../../api";
import { Alert, Button, Field, Input, Modal, Switch, useErrorText } from "../../components/ui";

export const actionIcon: Record<PowerActionKind, typeof Power> = { wake: Sunrise, restart: RotateCcw, shutdown: PowerOff };

// PowerDialog asks how a restart or shutdown is carried out (a wake needs no
// options) and sends it to one device or to several.
export function PowerDialog({
  action,
  deviceIds,
  label,
  onClose,
  onDone,
}: {
  action: PowerActionKind | null;
  deviceIds: string[];
  label: string; // host name, or "3 devices"
  onClose: () => void;
  onDone: (message: string) => void;
}) {
  const { t } = useTranslation();
  const errorText = useErrorText();
  const [notify, setNotify] = useState(true);
  const [countdown, setCountdown] = useState(300);
  const [postpones, setPostpones] = useState(1);
  const [postponeMinutes, setPostponeMinutes] = useState(30);
  const [skipIfUser, setSkipIfUser] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    setError(null);
    setSkipIfUser(false);
  }, [action]);

  const send = async () => {
    if (!action) return;
    setBusy(true);
    setError(null);
    const req: PowerRequest = { action };
    if (action !== "wake") {
      Object.assign(req, { notify, countdownSeconds: countdown, maxPostpones: notify ? postpones : 0, postponeMinutes, skipIfUserLoggedOn: skipIfUser });
    }
    try {
      if (deviceIds.length === 1) {
        await post(`/devices/${deviceIds[0]}/power`, req);
        onDone(t(`power.sentOne.${action}`, { name: label }));
      } else {
        const res = await post<BulkPowerResult>("/power/actions", { ...req, deviceIds });
        onDone(t("power.sentMany", { sent: res.sent, skipped: res.skipped }));
      }
    } catch (e) {
      setError(errorText(e));
    } finally {
      setBusy(false);
    }
  };

  const Icon = action ? actionIcon[action] : Power;
  return (
    <Modal
      open={!!action}
      onClose={onClose}
      title={action ? t(`power.dialogTitle.${action}`, { name: label }) : ""}
      footer={
        <>
          <Button variant="secondary" onClick={onClose}>
            {t("common.cancel")}
          </Button>
          <Button onClick={send} loading={busy} icon={<Icon className="size-4" />}>
            {action ? t(`power.actions.${action}`) : ""}
          </Button>
        </>
      }
    >
      <div className="space-y-4">
        {error && <Alert tone="danger">{error}</Alert>}
        {action === "wake" && <p className="text-sm text-muted">{t("power.wakeExplain")}</p>}
        {action && action !== "wake" && (
          <>
            <Switch checked={notify} onChange={setNotify} label={t("power.notifyUsers")} description={t("power.notifyUsersHint")} />
            {notify && (
              <div className="grid gap-4 sm:grid-cols-3">
                <Field label={t("patches.countdown")}>
                  {(id) => <Input id={id} type="number" min={30} max={3600} value={countdown} onChange={(e) => setCountdown(Number(e.target.value) || 30)} />}
                </Field>
                <Field label={t("patches.maxPostpones")}>
                  {(id) => <Input id={id} type="number" min={0} max={20} value={postpones} onChange={(e) => setPostpones(Number(e.target.value) || 0)} />}
                </Field>
                <Field label={t("patches.postponeMinutes")}>
                  {(id) => <Input id={id} type="number" min={5} max={1440} value={postponeMinutes} onChange={(e) => setPostponeMinutes(Number(e.target.value) || 5)} />}
                </Field>
              </div>
            )}
            <Switch checked={skipIfUser} onChange={setSkipIfUser} label={t("power.skipIfUser")} description={t("power.skipIfUserHint")} />
          </>
        )}
      </div>
    </Modal>
  );
}
