import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { post, type Me } from "../api";
import { Alert, Button, Field, Input, useErrorText } from "./ui";

// Changes the signed-in user's password; used for the forced change and in the profile.
export function PasswordForm({ onDone, submitLabel }: { onDone: (me: Me) => void; submitLabel?: string }) {
  const { t } = useTranslation();
  const errorText = useErrorText();
  const [current, setCurrent] = useState("");
  const [next, setNext] = useState("");
  const [repeat, setRepeat] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setError(null);
    if (next !== repeat) {
      setError(t("password.mismatch"));
      return;
    }
    setBusy(true);
    try {
      const me = await post<Me>("/me/password", { current, new: next });
      setCurrent("");
      setNext("");
      setRepeat("");
      onDone(me);
    } catch (err) {
      setError(errorText(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <form onSubmit={submit} className="space-y-4">
      {error && <Alert tone="danger">{error}</Alert>}
      <Field label={t("password.current")}>
        {(id) => <Input id={id} type="password" autoComplete="current-password" required value={current} onChange={(e) => setCurrent(e.target.value)} />}
      </Field>
      <Field label={t("password.new")} hint={t("password.rules")}>
        {(id) => <Input id={id} type="password" autoComplete="new-password" required minLength={12} value={next} onChange={(e) => setNext(e.target.value)} />}
      </Field>
      <Field label={t("password.repeat")}>
        {(id) => <Input id={id} type="password" autoComplete="new-password" required value={repeat} onChange={(e) => setRepeat(e.target.value)} />}
      </Field>
      <Button type="submit" loading={busy} className="w-full sm:w-auto">
        {submitLabel ?? t("password.save")}
      </Button>
    </form>
  );
}
