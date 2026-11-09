import { useEffect, useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import QRCode from "qrcode";
import { post, type Me } from "../api";
import { Alert, Button, CopyField, Field, Input, Spinner, useErrorText } from "./ui";

// Enrols an authenticator app: shows the QR code and confirms one code.
export function TotpSetup({ onDone }: { onDone: (me: Me) => void }) {
  const { t } = useTranslation();
  const errorText = useErrorText();
  const [secret, setSecret] = useState<string | null>(null);
  const [qr, setQr] = useState<string | null>(null);
  const [code, setCode] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    post<{ secret: string; uri: string }>("/me/totp/setup")
      .then(async (r) => {
        setSecret(r.secret);
        setQr(await QRCode.toDataURL(r.uri, { margin: 1, width: 200, errorCorrectionLevel: "M" }));
      })
      .catch((e) => setError(errorText(e)));
  }, []);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      onDone(await post<Me>("/me/totp/enable", { code }));
    } catch (err) {
      setError(errorText(err));
    } finally {
      setBusy(false);
    }
  };

  if (!secret && !error) return <Spinner />;
  return (
    <form onSubmit={submit} className="space-y-4">
      {error && <Alert tone="danger">{error}</Alert>}
      {secret && (
        <>
          <ol className="list-decimal space-y-1 pl-5 text-sm text-muted">
            <li>{t("totp.step1")}</li>
            <li>{t("totp.step2")}</li>
          </ol>
          {qr && (
            <div className="flex justify-center">
              <img src={qr} alt={t("totp.qrAlt")} className="size-[200px] rounded-md border border-line bg-white p-2" />
            </div>
          )}
          <Field label={t("totp.manual")}>{() => <CopyField value={secret} label={t("totp.manual")} />}</Field>
          <Field label={t("totp.code")}>
            {(id) => (
              <Input
                id={id}
                inputMode="numeric"
                autoComplete="one-time-code"
                required
                maxLength={7}
                value={code}
                onChange={(e) => setCode(e.target.value)}
                className="font-mono tracking-widest"
              />
            )}
          </Field>
          <Button type="submit" loading={busy}>
            {t("totp.enable")}
          </Button>
        </>
      )}
    </form>
  );
}
