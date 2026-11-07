import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { Box, Check, FileUp, Globe, Search, Terminal } from "lucide-react";
import {
  get,
  post,
  put,
  uploadFile,
  type CatalogPackage,
  type CatalogSearch,
  type InstallerKind,
  type PackageSource,
  type PackagesView,
  type SoftwarePackage,
  type UploadResult,
} from "../../api";
import { Alert, Badge, Button, Field, Input, Modal, Select, Spinner, Switch, useErrorText } from "../../components/ui";
import { formatSize } from "./util";

type Draft = Partial<SoftwarePackage> & { source: PackageSource };

const empty = (source: PackageSource): Draft => ({
  source,
  name: "",
  publisher: "",
  description: "",
  version: "",
  installArgs: "",
  detectName: "",
  productCode: "",
  anyHash: false,
  timeoutMinutes: 60,
  installerKind: source === "apt" ? "apt" : "",
  platform: source === "apt" ? "linux" : "windows",
});

// PackageForm adds a package from the winget catalog, an uploaded installer
// or an apt package, or edits one.
export function PackageForm({ view, pkg, onClose, onSaved }: { view: PackagesView; pkg: SoftwarePackage | null; onClose: () => void; onSaved: () => void }) {
  const { t } = useTranslation();
  const errorText = useErrorText();
  const [draft, setDraft] = useState<Draft | null>(pkg ? { ...pkg } : null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const set = (p: Partial<Draft>) => setDraft((d) => (d ? { ...d, ...p } : d));

  const save = async () => {
    if (!draft) return;
    setBusy(true);
    setError(null);
    try {
      if (pkg) await put(`/software/packages/${pkg.id}`, draft);
      else await post("/software/packages", draft);
      onSaved();
    } catch (e) {
      setError(errorText(e));
    } finally {
      setBusy(false);
    }
  };

  const ready =
    draft && draft.name && (draft.source !== "winget" || draft.wingetId) && (draft.source !== "upload" || draft.fileSha256) && (draft.source !== "apt" || draft.aptPackage);
  return (
    <Modal
      open
      wide
      onClose={onClose}
      title={pkg ? t("software.form.editTitle") : t("software.form.addTitle")}
      footer={
        draft && (
          <>
            <Button variant="secondary" onClick={onClose}>
              {t("common.cancel")}
            </Button>
            <Button loading={busy} disabled={!ready} onClick={save}>
              {t("common.save")}
            </Button>
          </>
        )
      }
    >
      {!draft ? (
        <SourcePicker onPick={(s) => setDraft(empty(s))} />
      ) : (
        <div className="space-y-5">
          {error && <Alert tone="danger">{error}</Alert>}
          {draft.source === "winget" && <WingetPart draft={draft} set={set} catalogEmpty={view.catalog.packages === 0} editing={!!pkg} />}
          {draft.source === "upload" && <UploadPart draft={draft} set={set} view={view} />}
          {draft.source === "apt" && (
            <Field label={t("software.form.aptPackage")} hint={t("software.form.aptPackageHint")}>
              {(id) => (
                <Input
                  id={id}
                  value={draft.aptPackage ?? ""}
                  onChange={(e) => set({ aptPackage: e.target.value.trim().toLowerCase(), name: draft.name || e.target.value.trim() })}
                  placeholder="htop"
                  className="font-mono"
                />
              )}
            </Field>
          )}
          {(draft.source !== "winget" || draft.wingetId) && <CommonFields draft={draft} set={set} view={view} />}
        </div>
      )}
    </Modal>
  );
}

function SourcePicker({ onPick }: { onPick: (s: PackageSource) => void }) {
  const { t } = useTranslation();
  const options: { s: PackageSource; icon: React.ReactNode }[] = [
    { s: "winget", icon: <Globe className="size-5" /> },
    { s: "upload", icon: <FileUp className="size-5" /> },
    { s: "apt", icon: <Terminal className="size-5" /> },
  ];
  return (
    <div className="grid gap-3 md:grid-cols-3">
      {options.map((o) => (
        <button
          key={o.s}
          type="button"
          onClick={() => onPick(o.s)}
          className="flex flex-col items-start gap-2 rounded-lg border border-line bg-surface p-4 text-left transition-colors hover:border-primary hover:bg-surface-muted focus-visible:ring-2 focus-visible:ring-primary focus-visible:outline-none"
        >
          <span className="rounded-md bg-primary/10 p-2 text-primary">{o.icon}</span>
          <span className="font-medium">{t(`software.form.source.${o.s}`)}</span>
          <span className="text-sm text-muted">{t(`software.form.source.${o.s}Hint`)}</span>
        </button>
      ))}
    </div>
  );
}

function WingetPart({ draft, set, catalogEmpty, editing }: { draft: Draft; set: (p: Partial<Draft>) => void; catalogEmpty: boolean; editing: boolean }) {
  const { t } = useTranslation();
  const errorText = useErrorText();
  const [q, setQ] = useState("");
  const [hits, setHits] = useState<CatalogSearch["results"] | null>(null);
  const [detail, setDetail] = useState<CatalogPackage | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!draft.wingetId) return;
    get<CatalogPackage>(`/software/catalog/package?id=${encodeURIComponent(draft.wingetId)}`)
      .then((d) => {
        setDetail(d);
        if (!draft.publisher && d.publisher) set({ publisher: d.publisher });
      })
      .catch((e) => setError(errorText(e)));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [draft.wingetId]);

  useEffect(() => {
    const term = q.trim();
    if (term.length < 2) {
      setHits(null);
      return;
    }
    const ctl = new AbortController();
    const timer = setTimeout(() => {
      setLoading(true);
      get<CatalogSearch>(`/software/catalog?q=${encodeURIComponent(term)}`, ctl.signal)
        .then((r) => setHits(r.results))
        .catch(() => undefined)
        .finally(() => setLoading(false));
    }, 250);
    return () => {
      clearTimeout(timer);
      ctl.abort();
    };
  }, [q]);

  if (draft.wingetId) {
    return (
      <div className="space-y-3">
        {error && <Alert tone="warning">{error}</Alert>}
        <div className="flex flex-wrap items-start justify-between gap-3 rounded-lg border border-line bg-surface-muted/50 p-4">
          <div className="min-w-0 space-y-1">
            <div className="flex items-center gap-2 font-medium">
              <Box className="size-4 text-primary" />
              {detail?.name ?? draft.name}
            </div>
            <div className="font-mono text-xs text-muted">{draft.wingetId}</div>
            {detail && <p className="text-sm text-muted">{detail.description}</p>}
            {detail && (
              <div className="flex flex-wrap gap-2 pt-1 text-xs">
                <Badge>{detail.publisher}</Badge>
                {detail.license && <Badge>{detail.license}</Badge>}
                {detail.choice && <Badge tone="primary">{`${detail.choice.type} · ${detail.choice.architecture}`}</Badge>}
              </div>
            )}
          </div>
          {!editing && (
            <Button variant="ghost" size="sm" onClick={() => set({ wingetId: "", name: "" })}>
              {t("software.form.otherPackage")}
            </Button>
          )}
        </div>
        {detail && !detail.choice && <Alert tone="danger">{t("software.form.noInstaller")}</Alert>}
        {detail && (
          <Field label={t("software.form.version")} hint={t("software.form.versionHint")}>
            {(id) => (
              <Select id={id} value={draft.version ?? ""} onChange={(e) => set({ version: e.target.value })}>
                <option value="">{t("software.form.latest", { version: detail.versions[0] ?? detail.version })}</option>
                {detail.versions.map((v) => (
                  <option key={v} value={v}>
                    {v}
                  </option>
                ))}
              </Select>
            )}
          </Field>
        )}
      </div>
    );
  }
  return (
    <div className="space-y-3">
      {catalogEmpty && <Alert tone="info">{t("software.form.catalogEmpty")}</Alert>}
      <div className="relative">
        <Search className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted" />
        <Input
          autoFocus
          value={q}
          onChange={(e) => setQ(e.target.value)}
          placeholder={t("software.form.searchCatalog")}
          className="pl-9"
          aria-label={t("software.form.searchCatalog")}
        />
      </div>
      {loading && <Spinner />}
      {hits && hits.length === 0 && !loading && <p className="text-sm text-muted">{t("software.form.noHits")}</p>}
      {hits && hits.length > 0 && (
        <ul className="max-h-80 divide-y divide-line overflow-auto rounded-md border border-line">
          {hits.map((h) => (
            <li key={h.id}>
              <button
                type="button"
                onClick={() => set({ wingetId: h.id, name: h.name })}
                className="flex w-full items-center justify-between gap-3 px-4 py-2.5 text-left hover:bg-surface-muted"
              >
                <span className="min-w-0">
                  <span className="block truncate font-medium">{h.name}</span>
                  <span className="block truncate font-mono text-xs text-muted">{h.id}</span>
                </span>
                <span className="shrink-0 text-xs text-muted tabular-nums">{h.version}</span>
              </button>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

function UploadPart({ draft, set, view }: { draft: Draft; set: (p: Partial<Draft>) => void; view: PackagesView }) {
  const { t, i18n } = useTranslation();
  const errorText = useErrorText();
  const input = useRef<HTMLInputElement>(null);
  const [progress, setProgress] = useState<number | null>(null);
  const [error, setError] = useState<string | null>(null);

  const pick = async (f: File | undefined) => {
    if (!f) return;
    setError(null);
    if (f.size > view.maxUploadMiB * 1024 * 1024) {
      setError(t("errors.installer_too_large"));
      return;
    }
    setProgress(0);
    try {
      const r = await uploadFile<UploadResult>("/software/uploads", f, setProgress);
      const base = r.name.replace(/\.(msi|exe|deb)$/i, "");
      set({
        fileSha256: r.sha256,
        fileName: r.name,
        fileSize: r.size,
        installerKind: r.kind,
        platform: r.kind === "deb" ? "linux" : "windows",
        name: draft.name || base,
      });
    } catch (e) {
      setError(errorText(e));
    } finally {
      setProgress(null);
    }
  };

  return (
    <div className="space-y-3">
      {error && <Alert tone="danger">{error}</Alert>}
      <div
        onDragOver={(e) => e.preventDefault()}
        onDrop={(e) => {
          e.preventDefault();
          pick(e.dataTransfer.files[0]);
        }}
        className="flex flex-col items-center gap-2 rounded-lg border-2 border-dashed border-line px-4 py-6 text-center"
      >
        {draft.fileSha256 ? (
          <>
            <Check className="size-6 text-success" />
            <div className="font-medium">{draft.fileName}</div>
            <div className="font-mono text-xs break-all text-muted">
              {formatSize(draft.fileSize ?? 0, i18n.language)} · SHA-256 {draft.fileSha256}
            </div>
          </>
        ) : (
          <>
            <FileUp className="size-6 text-muted" />
            <div className="text-sm text-muted">{t("software.form.dropFile", { size: Math.round(view.maxUploadMiB / 1024) })}</div>
          </>
        )}
        {progress !== null ? (
          <div className="mt-2 h-2 w-full max-w-sm overflow-hidden rounded-full bg-surface-muted">
            <div className="h-full bg-primary transition-all" style={{ width: `${Math.round(progress * 100)}%` }} />
          </div>
        ) : (
          <Button variant="secondary" size="sm" onClick={() => input.current?.click()}>
            {draft.fileSha256 ? t("software.form.replaceFile") : t("software.form.chooseFile")}
          </Button>
        )}
        <input ref={input} type="file" accept=".msi,.exe,.deb" className="hidden" onChange={(e) => pick(e.target.files?.[0])} />
      </div>
    </div>
  );
}

function CommonFields({ draft, set, view }: { draft: Draft; set: (p: Partial<Draft>) => void; view: PackagesView }) {
  const { t } = useTranslation();
  const kinds = (view.kinds[draft.platform ?? "windows"] ?? []).filter((k) => k !== "apt") as InstallerKind[];
  const kind = draft.installerKind || "";
  const defaultArgs = kind ? view.defaultArgs[kind] : "";
  const argsHint =
    draft.source === "winget"
      ? t("software.form.argsWinget")
      : kind === "msi"
        ? t("software.form.argsMsi")
        : kind === "deb" || draft.source === "apt"
          ? t("software.form.argsApt")
          : defaultArgs
            ? t("software.form.argsDefault", { args: defaultArgs })
            : t("software.form.argsRequired");
  return (
    <div className="space-y-4">
      <div className="grid gap-4 md:grid-cols-2">
        <Field label={t("software.form.name")}>{(id) => <Input id={id} value={draft.name ?? ""} onChange={(e) => set({ name: e.target.value })} />}</Field>
        <Field label={t("software.form.publisher")}>{(id) => <Input id={id} value={draft.publisher ?? ""} onChange={(e) => set({ publisher: e.target.value })} />}</Field>
        {draft.source === "upload" && (
          <Field label={t("software.form.kind")} hint={t(`software.kinds.${kind || "exe"}Hint`)}>
            {(id) => (
              <Select id={id} value={kind} onChange={(e) => set({ installerKind: e.target.value as InstallerKind })}>
                {kinds.map((k) => (
                  <option key={k} value={k}>
                    {t(`software.kinds.${k}`)}
                  </option>
                ))}
              </Select>
            )}
          </Field>
        )}
        {draft.source !== "winget" && (
          <Field label={t("software.form.fileVersion")} hint={t("software.form.fileVersionHint")}>
            {(id) => <Input id={id} value={draft.version ?? ""} onChange={(e) => set({ version: e.target.value })} placeholder="1.0.0" />}
          </Field>
        )}
      </div>
      <Field label={draft.source === "upload" && kind !== "msi" && kind !== "deb" ? t("software.form.silentArgs") : t("software.form.extraArgs")} hint={argsHint}>
        {(id) => <Input id={id} value={draft.installArgs ?? ""} onChange={(e) => set({ installArgs: e.target.value })} placeholder={defaultArgs || ""} className="font-mono" />}
      </Field>
      <details className="rounded-md border border-line px-4 py-3 text-sm">
        <summary className="cursor-pointer font-medium">{t("software.form.advanced")}</summary>
        <div className="mt-4 grid gap-4 md:grid-cols-2">
          {draft.source !== "apt" && (
            <Field label={t("software.form.detectName")} hint={t("software.form.detectNameHint")}>
              {(id) => <Input id={id} value={draft.detectName ?? ""} onChange={(e) => set({ detectName: e.target.value })} />}
            </Field>
          )}
          {draft.platform === "windows" && (
            <Field label={t("software.form.productCode")} hint={t("software.form.productCodeHint")}>
              {(id) => (
                <Input
                  id={id}
                  value={draft.productCode ?? ""}
                  onChange={(e) => set({ productCode: e.target.value })}
                  placeholder="{00000000-0000-0000-0000-000000000000}"
                  className="font-mono"
                />
              )}
            </Field>
          )}
          <Field label={t("software.form.timeout")}>
            {(id) => <Input id={id} type="number" min={5} max={600} value={draft.timeoutMinutes ?? 60} onChange={(e) => set({ timeoutMinutes: Number(e.target.value) || 60 })} />}
          </Field>
          {draft.source === "winget" && (
            <div className="md:col-span-2">
              <Switch checked={!!draft.anyHash} onChange={(v) => set({ anyHash: v })} label={t("software.form.anyHash")} description={t("software.form.anyHashHint")} />
            </div>
          )}
        </div>
      </details>
    </div>
  );
}
