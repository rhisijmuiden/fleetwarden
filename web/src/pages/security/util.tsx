import { useState } from "react";
import { useTranslation } from "react-i18next";
import { Eye, KeyRound, RotateCcw, TriangleAlert } from "lucide-react";
import { post, type AntivirusState, type EncryptionState, type FirewallSummary, type RecoveryKey } from "../../api";
import { Alert, Badge, Button, CopyField, Field, Input, Modal, formatDate, useErrorText } from "../../components/ui";

type Tone = "neutral" | "primary" | "success" | "warning" | "danger";

const encryptionTone: Record<string, Tone> = {
  encrypted: "success",
  partial: "warning",
  suspended: "warning",
  encrypting: "primary",
  none: "danger",
  unknown: "neutral",
};

export function EncryptionBadge({ state }: { state: EncryptionState }) {
  const { t } = useTranslation();
  if (!state) return <span className="text-muted">–</span>;
  return <Badge tone={encryptionTone[state] ?? "neutral"}>{t(`security.encryption.${state}`)}</Badge>;
}

export function AntivirusBadge({ state, platform }: { state: AntivirusState; platform: string }) {
  const { t } = useTranslation();
  if (!state) return <span className="text-muted">–</span>;
  // Most Linux servers run without antivirus; that is no alarm.
  const tone: Tone =
    state === "ok" ? "success" : state === "none" && platform === "linux" ? "neutral" : state === "outdated" ? "warning" : state === "unknown" ? "neutral" : "danger";
  return <Badge tone={tone}>{t(`security.antivirus.${state}`)}</Badge>;
}

export function FirewallBadge({ state }: { state: FirewallSummary }) {
  const { t } = useTranslation();
  if (!state) return <span className="text-muted">–</span>;
  const tone: Tone = state === "on" ? "success" : state === "partial" ? "warning" : state === "off" ? "danger" : "neutral";
  return <Badge tone={tone}>{t(`security.firewall.${state}`)}</Badge>;
}

export function YesNo({ value, good = true }: { value: boolean | null | undefined; good?: boolean }) {
  const { t } = useTranslation();
  if (value === null || value === undefined) return <span className="text-muted">–</span>;
  return <Badge tone={value === good ? "success" : "warning"}>{value ? t("common.yes") : t("common.no")}</Badge>;
}

// formatKey splits the 48 digits in groups for reading aloud.
function KeyDigits({ value }: { value: string }) {
  return (
    <div className="grid grid-cols-4 gap-2 rounded-md border border-line bg-surface-muted p-3 font-mono text-base tracking-wider sm:text-lg">
      {value.split("-").map((g, i) => (
        <span key={i} className="text-center tabular-nums">
          {g}
        </span>
      ))}
    </div>
  );
}

// RevealDialog shows one recovery key after asking for a reason, and offers
// to rotate it afterwards.
export function RevealDialog({
  recoveryKey,
  onClose,
  onRotate,
}: {
  recoveryKey: RecoveryKey;
  onClose: () => void;
  onRotate?: () => void;
}) {
  const { t, i18n } = useTranslation();
  const errorText = useErrorText();
  const [reason, setReason] = useState("");
  const [password, setPassword] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const reveal = () => {
    setBusy(true);
    setError(null);
    post<{ password: string }>(`/security/keys/${recoveryKey.id}/reveal`, { reason })
      .then((r) => setPassword(r.password))
      .catch((e) => setError(errorText(e)))
      .finally(() => setBusy(false));
  };
  return (
    <Modal
      open
      wide
      onClose={onClose}
      title={t("security.keys.revealTitle", { host: recoveryKey.hostname, volume: recoveryKey.volume })}
      footer={
        password ? (
          <>
            {onRotate && !recoveryKey.removedAt && (
              <Button variant="secondary" icon={<RotateCcw className="size-4" />} onClick={onRotate}>
                {t("security.keys.rotate")}
              </Button>
            )}
            <Button onClick={onClose}>{t("common.close")}</Button>
          </>
        ) : (
          <>
            <Button variant="secondary" onClick={onClose}>
              {t("common.cancel")}
            </Button>
            <Button icon={<Eye className="size-4" />} loading={busy} onClick={reveal}>
              {t("security.keys.reveal")}
            </Button>
          </>
        )
      }
    >
      {error && <Alert tone="danger">{error}</Alert>}
      <dl className="grid grid-cols-1 gap-x-6 gap-y-2 text-sm sm:grid-cols-2">
        <div>
          <dt className="text-xs text-muted">{t("security.keys.keyId")}</dt>
          <dd className="font-mono text-xs break-all">{recoveryKey.keyId}</dd>
        </div>
        <div>
          <dt className="text-xs text-muted">{t("security.keys.lastSeen")}</dt>
          <dd>{formatDate(recoveryKey.lastSeenAt, i18n.language)}</dd>
        </div>
      </dl>
      {recoveryKey.removedAt && (
        <Alert tone="warning">{t("security.keys.replacedHint", { when: formatDate(recoveryKey.removedAt, i18n.language) })}</Alert>
      )}
      {password ? (
        <div className="space-y-3">
          <KeyDigits value={password} />
          <CopyField value={password} label={t("security.keys.password")} />
          <Alert tone="warning" title={t("security.keys.afterUseTitle")}>
            {t("security.keys.afterUse")}
          </Alert>
        </div>
      ) : (
        <>
          <Alert tone="info">{t("security.keys.auditHint")}</Alert>
          <Field label={t("security.keys.reason")} hint={t("security.keys.reasonHint")}>
            {(id) => <Input id={id} value={reason} maxLength={300} onChange={(e) => setReason(e.target.value)} autoFocus />}
          </Field>
        </>
      )}
    </Modal>
  );
}

// RotateDialog confirms a key rotation and starts it.
export function RotateDialog({ deviceId, volume, onClose, onDone }: { deviceId: string; volume: string; onClose: () => void; onDone: () => void }) {
  const { t } = useTranslation();
  const errorText = useErrorText();
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const rotate = () => {
    setBusy(true);
    setError(null);
    post(`/devices/${deviceId}/security/actions`, { action: "bitlocker.rotate", mount: volume })
      .then(() => {
        onDone();
        onClose();
      })
      .catch((e) => setError(errorText(e)))
      .finally(() => setBusy(false));
  };
  return (
    <Modal
      open
      onClose={onClose}
      title={t("security.keys.rotateTitle", { volume })}
      footer={
        <>
          <Button variant="secondary" onClick={onClose}>
            {t("common.cancel")}
          </Button>
          <Button icon={<KeyRound className="size-4" />} loading={busy} onClick={rotate}>
            {t("security.keys.rotate")}
          </Button>
        </>
      }
    >
      {error && <Alert tone="danger">{error}</Alert>}
      <p className="flex gap-2 text-sm">
        <TriangleAlert className="mt-0.5 size-4 shrink-0 text-warning" />
        {t("security.keys.rotateText")}
      </p>
    </Modal>
  );
}
