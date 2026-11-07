import { useEffect, useRef, useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { ImageUp, RotateCcw, Trash2 } from "lucide-react";
import tokens from "../../../../branding/tokens.json";
import { api, del, put, type Branding as BrandingT } from "../../api";
import { useBranding } from "../../branding";
import { Alert, Button, Card, Field, Input, PageHeader, useErrorText } from "../../components/ui";

const defaults = {
  primaryColor: tokens.color.light.primary,
  primaryColorDark: tokens.color.dark.primary,
  sidebarColor: tokens.color.light.sidebar,
};

function ColorField({ label, value, fallback, onChange }: { label: string; value: string; fallback: string; onChange: (v: string) => void }) {
  const { t } = useTranslation();
  return (
    <Field label={label}>
      {(id) => (
        <div className="flex gap-2">
          <input
            type="color"
            aria-label={label}
            value={(value || fallback).toLowerCase()}
            onChange={(e) => onChange(e.target.value)}
            className="h-9 w-12 shrink-0 cursor-pointer rounded-md border border-line bg-surface p-1"
          />
          <Input id={id} value={value} placeholder={fallback} onChange={(e) => onChange(e.target.value)} className="font-mono" maxLength={7} />
          {value && <Button variant="ghost" aria-label={t("branding.reset")} title={t("branding.reset")} icon={<RotateCcw className="size-4" />} onClick={() => onChange("")} />}
        </div>
      )}
    </Field>
  );
}

function AssetPicker({ name, has, revision, onChange }: { name: "logo" | "favicon"; has: boolean; revision: number; onChange: (b: BrandingT) => void }) {
  const { t } = useTranslation();
  const errorText = useErrorText();
  const input = useRef<HTMLInputElement>(null);
  const [error, setError] = useState<string | null>(null);
  const upload = async (file: File) => {
    setError(null);
    try {
      onChange(await api<BrandingT>("PUT", `/settings/branding/${name}`, file));
    } catch (e) {
      setError(errorText(e));
    }
  };
  return (
    <div className="space-y-3">
      <div className="flex items-center gap-4">
        <div className="grid size-16 shrink-0 place-items-center rounded-lg border border-line bg-surface-muted">
          {has ? <img src={`/api/v1/branding/${name}?v=${revision}`} alt="" className="max-h-12 max-w-12" /> : <span className="text-xs text-muted">{t("branding.default")}</span>}
        </div>
        <div className="flex flex-wrap gap-2">
          <Button variant="secondary" size="sm" icon={<ImageUp className="size-4" />} onClick={() => input.current?.click()}>
            {t("branding.upload")}
          </Button>
          {has && (
            <Button variant="ghost" size="sm" icon={<Trash2 className="size-4" />} onClick={() => void del<BrandingT>(`/settings/branding/${name}`).then(onChange)}>
              {t("branding.remove")}
            </Button>
          )}
        </div>
        <input
          ref={input}
          type="file"
          hidden
          accept="image/png,image/svg+xml,image/jpeg,image/webp,image/x-icon"
          onChange={(e) => {
            const f = e.target.files?.[0];
            if (f) void upload(f);
            e.target.value = "";
          }}
        />
      </div>
      {error && <Alert tone="danger">{error}</Alert>}
    </div>
  );
}

export function Branding() {
  const { t } = useTranslation();
  const errorText = useErrorText();
  const { branding, reload } = useBranding();
  const [form, setForm] = useState({ productName: "", primaryColor: "", primaryColorDark: "", sidebarColor: "" });
  const [msg, setMsg] = useState<{ tone: "success" | "danger"; text: string } | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    setForm({
      productName: branding.productName === "FleetWarden" ? "" : branding.productName,
      primaryColor: branding.primaryColor,
      primaryColorDark: branding.primaryColorDark,
      sidebarColor: branding.sidebarColor,
    });
  }, [branding.productName, branding.primaryColor, branding.primaryColorDark, branding.sidebarColor]);

  const save = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setMsg(null);
    try {
      await put("/settings/branding", form);
      reload();
      setMsg({ tone: "success", text: t("common.saved") });
    } catch (err) {
      setMsg({ tone: "danger", text: errorText(err) });
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="space-y-6">
      <PageHeader title={t("branding.title")} description={t("branding.subtitle")} />
      {msg && <Alert tone={msg.tone}>{msg.text}</Alert>}
      <div className="grid gap-6 lg:grid-cols-2">
        <Card title={t("branding.identity")}>
          <form onSubmit={save} className="space-y-5">
            <Field label={t("branding.productName")} hint={t("branding.productNameHint")}>
              {(id) => <Input id={id} maxLength={40} value={form.productName} placeholder="FleetWarden" onChange={(e) => setForm({ ...form, productName: e.target.value })} />}
            </Field>
            <ColorField label={t("branding.primary")} value={form.primaryColor} fallback={defaults.primaryColor} onChange={(v) => setForm({ ...form, primaryColor: v })} />
            <ColorField label={t("branding.primaryDark")} value={form.primaryColorDark} fallback={defaults.primaryColorDark} onChange={(v) => setForm({ ...form, primaryColorDark: v })} />
            <ColorField label={t("branding.sidebar")} value={form.sidebarColor} fallback={defaults.sidebarColor} onChange={(v) => setForm({ ...form, sidebarColor: v })} />
            <Button type="submit" loading={busy}>
              {t("common.save")}
            </Button>
          </form>
        </Card>
        <div className="space-y-6">
          <Card title={t("branding.logo")} description={t("branding.logoHint")}>
            <AssetPicker name="logo" has={branding.hasLogo} revision={branding.revision} onChange={reload} />
          </Card>
          <Card title={t("branding.favicon")} description={t("branding.faviconHint")}>
            <AssetPicker name="favicon" has={branding.hasFavicon} revision={branding.revision} onChange={reload} />
          </Card>
          <Card title={t("branding.preview")}>
            <div className="flex flex-wrap items-center gap-3">
              <Button>{t("branding.previewButton")}</Button>
              <Button variant="secondary">{t("branding.previewSecondary")}</Button>
              <span className="text-sm font-medium text-primary">{t("branding.previewLink")}</span>
            </div>
          </Card>
        </div>
      </div>
    </div>
  );
}
