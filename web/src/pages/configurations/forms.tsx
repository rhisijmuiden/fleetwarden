import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { Plus, Trash2 } from "lucide-react";
import type {
  AdminSettings,
  BrowserKey,
  BrowserSettings,
  ConfigSettings,
  ConfigType,
  DesktopSettings,
  DriveMapping,
  DriveSettings,
  FirewallProfile,
  FirewallProtocol,
  FirewallRule,
  FirewallSettings,
  Platform,
  PowerSettings,
  PrinterConfig,
  PrinterSettings,
  RegistrySettings,
  RegistryType,
  RegistryValue,
  ScriptSettings,
  ScriptShell,
  ScriptTrigger,
  StartupMode,
  WallpaperStyle,
} from "../../api";
import { Alert, Button, Field, Input, Select, Switch } from "../../components/ui";

const newDrive = (letters: string[]): DriveMapping => ({ letter: "HIJKLMNOPQRSTUVWXYZDEFG".split("").find((l) => !letters.includes(l)) ?? "H", path: "", label: "" });
const newPrinter = (): PrinterConfig => ({ kind: "shared", path: "", scope: "device" });
const newRule = (): FirewallRule => ({ name: "", direction: "in", action: "allow", protocol: "tcp", localPorts: "" });

export const browsers: BrowserKey[] = ["chrome", "edge", "firefox"];
export const browserNames: Record<BrowserKey, string> = { chrome: "Google Chrome", edge: "Microsoft Edge", firefox: "Mozilla Firefox" };

export function defaultSettings(type: ConfigType, platform: Platform): ConfigSettings {
  switch (type) {
    case "browser":
      return { browsers: ["chrome", "edge", "firefox"], homepage: "", startupMode: "" } satisfies BrowserSettings;
    case "registry":
      return { values: [{ hive: "HKLM", key: "", name: "", type: "string", value: "" }] } satisfies RegistrySettings;
    case "script":
      return {
        shell: platform === "windows" ? "powershell" : "bash",
        content: "",
        runAs: "system",
        trigger: "once",
        timeoutMinutes: 10,
      } satisfies ScriptSettings;
    case "drives":
      return { drives: [newDrive([])] } satisfies DriveSettings;
    case "printers":
      return { printers: [newPrinter()] } satisfies PrinterSettings;
    case "desktop":
      return { wallpaper: "", wallpaperStyle: "fill", lockMinutes: 10 } satisfies DesktopSettings;
    case "power":
      return { monitorAc: 15, monitorDc: 5, sleepAc: 60, sleepDc: 15 } satisfies PowerSettings;
    case "admins":
      return { members: [] } satisfies AdminSettings;
    case "firewall":
      return { rules: [newRule()] } satisfies FirewallSettings;
  }
}

const textarea =
  "w-full rounded-md border border-line bg-surface px-3 py-2 text-sm text-fg placeholder:text-muted/70 outline-none focus-visible:ring-2 focus-visible:ring-primary";

// ListInput edits a list of strings, one per line.
function ListInput({ id, value, onChange, placeholder, rows = 3 }: { id: string; value?: string[]; onChange: (v: string[]) => void; placeholder?: string; rows?: number }) {
  const [text, setText] = useState((value ?? []).join("\n"));
  useEffect(() => {
    // Keep the text (with an empty last line while typing) unless the list changed elsewhere.
    if (text.split("\n").map((s) => s.trim()).filter(Boolean).join("\n") !== (value ?? []).join("\n")) setText((value ?? []).join("\n"));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [value]);
  return (
    <textarea
      id={id}
      rows={rows}
      className={`${textarea} font-mono text-xs`}
      placeholder={placeholder}
      value={text}
      onChange={(e) => {
        setText(e.target.value);
        onChange(
          e.target.value
            .split("\n")
            .map((s) => s.trim())
            .filter(Boolean),
        );
      }}
    />
  );
}

// TriState picks "not managed", on or off.
function TriState({ id, value, onChange }: { id: string; value?: boolean | null; onChange: (v: boolean | null) => void }) {
  const { t } = useTranslation();
  return (
    <Select id={id} value={value === true ? "on" : value === false ? "off" : ""} onChange={(e) => onChange(e.target.value === "" ? null : e.target.value === "on")}>
      <option value="">{t("configs.notManaged")}</option>
      <option value="on">{t("configs.on")}</option>
      <option value="off">{t("configs.off")}</option>
    </Select>
  );
}

export function BrowserForm({ value, onChange }: { value: BrowserSettings; onChange: (v: BrowserSettings) => void }) {
  const { t } = useTranslation();
  const set = (patch: Partial<BrowserSettings>) => onChange({ ...value, ...patch });
  const [extraText, setExtraText] = useState<Record<string, string>>(() =>
    Object.fromEntries(browsers.map((b) => [b, value.extra?.[b] ? JSON.stringify(value.extra[b], null, 2) : ""])),
  );
  const [extraError, setExtraError] = useState<Record<string, boolean>>({});
  const setExtra = (b: BrowserKey, text: string) => {
    setExtraText((x) => ({ ...x, [b]: text }));
    const extra = { ...(value.extra ?? {}) };
    if (text.trim() === "") {
      delete extra[b];
      setExtraError((x) => ({ ...x, [b]: false }));
    } else {
      try {
        const parsed = JSON.parse(text);
        if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) throw new Error("not an object");
        extra[b] = parsed;
        setExtraError((x) => ({ ...x, [b]: false }));
      } catch {
        setExtraError((x) => ({ ...x, [b]: true }));
        return;
      }
    }
    set({ extra });
  };
  const toggle = (b: BrowserKey) => set({ browsers: value.browsers.includes(b) ? value.browsers.filter((x) => x !== b) : [...value.browsers, b] });
  return (
    <div className="space-y-5">
      <div className="space-y-1.5">
        <p className="text-sm font-medium">{t("configs.browser.browsers")}</p>
        <div className="flex flex-wrap gap-2">
          {browsers.map((b) => (
            <label key={b} className="flex cursor-pointer items-center gap-2 rounded-md border border-line px-3 py-2 text-sm hover:bg-surface-muted">
              <input type="checkbox" className="size-4 accent-primary" checked={value.browsers.includes(b)} onChange={() => toggle(b)} />
              {browserNames[b]}
            </label>
          ))}
        </div>
      </div>
      <div className="grid gap-4 sm:grid-cols-2">
        <Field label={t("configs.browser.homepage")} hint={t("configs.browser.homepageHint")}>
          {(id) => <Input id={id} type="url" placeholder="https://intranet.example.com" value={value.homepage ?? ""} onChange={(e) => set({ homepage: e.target.value })} />}
        </Field>
        <Field label={t("configs.browser.homeButton")} hint={t("configs.browser.homeButtonHint")}>
          {(id) => <TriState id={id} value={value.showHomeButton} onChange={(v) => set({ showHomeButton: v })} />}
        </Field>
        <Field label={t("configs.browser.startup")}>
          {(id) => (
            <Select id={id} value={value.startupMode ?? ""} onChange={(e) => set({ startupMode: e.target.value as StartupMode })}>
              <option value="">{t("configs.notManaged")}</option>
              <option value="newtab">{t("configs.browser.startupNewTab")}</option>
              <option value="restore">{t("configs.browser.startupRestore")}</option>
              <option value="urls">{t("configs.browser.startupUrls")}</option>
            </Select>
          )}
        </Field>
        <Field label={t("configs.browser.passwordManager")} hint={t("configs.browser.passwordManagerHint")}>
          {(id) => <TriState id={id} value={value.passwordManager} onChange={(v) => set({ passwordManager: v })} />}
        </Field>
      </div>
      {value.startupMode === "urls" && (
        <Field label={t("configs.browser.startupPages")} hint={t("configs.onePerLine")}>
          {(id) => <ListInput id={id} value={value.startupUrls} onChange={(v) => set({ startupUrls: v })} placeholder="https://intranet.example.com" />}
        </Field>
      )}
      <div className="space-y-4 rounded-md border border-line p-4">
        <div>
          <p className="text-sm font-medium">{t("configs.browser.extensions")}</p>
          <p className="text-xs text-muted">{t("configs.browser.extensionsHint")}</p>
        </div>
        {value.browsers.includes("chrome") && (
          <Field label={t("configs.browser.chromeExtensions")} hint={t("configs.browser.chromiumExtensionsHint")}>
            {(id) => <ListInput id={id} value={value.chromeExtensions} onChange={(v) => set({ chromeExtensions: v })} placeholder="cjpalhdlnbpafiamejdnhcphjbkeiagm" />}
          </Field>
        )}
        {value.browsers.includes("edge") && (
          <Field label={t("configs.browser.edgeExtensions")} hint={t("configs.browser.chromiumExtensionsHint")}>
            {(id) => <ListInput id={id} value={value.edgeExtensions} onChange={(v) => set({ edgeExtensions: v })} placeholder="odfafepnkmbhccpbejgmiehpchacaeak" />}
          </Field>
        )}
        {value.browsers.includes("firefox") && (
          <Field label={t("configs.browser.firefoxExtensions")} hint={t("configs.browser.firefoxExtensionsHint")}>
            {(id) => (
              <ListInput
                id={id}
                value={value.firefoxExtensions}
                onChange={(v) => set({ firefoxExtensions: v })}
                placeholder="https://addons.mozilla.org/firefox/downloads/latest/ublock-origin/latest.xpi"
              />
            )}
          </Field>
        )}
      </div>
      <details className="rounded-md border border-line p-4">
        <summary className="cursor-pointer text-sm font-medium">{t("configs.browser.advanced")}</summary>
        <p className="mt-2 text-xs text-muted">{t("configs.browser.advancedHint")}</p>
        <div className="mt-3 space-y-3">
          {value.browsers.map((b) => (
            <Field key={b} label={browserNames[b]}>
              {(id) => (
                <>
                  <textarea
                    id={id}
                    rows={4}
                    className={`${textarea} font-mono text-xs ${extraError[b] ? "border-danger" : ""}`}
                    placeholder={b === "firefox" ? '{\n  "DisableTelemetry": true\n}' : '{\n  "BookmarkBarEnabled": true\n}'}
                    value={extraText[b] ?? ""}
                    onChange={(e) => setExtra(b, e.target.value)}
                  />
                  {extraError[b] && <p className="text-xs text-danger">{t("configs.browser.invalidJson")}</p>}
                </>
              )}
            </Field>
          ))}
        </div>
      </details>
    </div>
  );
}

const regTypes: RegistryType[] = ["string", "expandstring", "multistring", "dword", "qword", "delete"];

export function RegistryForm({ value, onChange }: { value: RegistrySettings; onChange: (v: RegistrySettings) => void }) {
  const { t } = useTranslation();
  const setRow = (i: number, patch: Partial<RegistryValue>) => onChange({ ...value, values: value.values.map((v, j) => (j === i ? { ...v, ...patch } : v)) });
  return (
    <div className="space-y-4">
      <div className="space-y-3">
        {value.values.map((v, i) => (
          <div key={i} className="space-y-2 rounded-md border border-line p-3">
            <div className="grid gap-2 sm:grid-cols-[110px_1fr_auto]">
              <Select aria-label={t("configs.registry.hive")} value={v.hive} onChange={(e) => setRow(i, { hive: e.target.value as "HKLM" | "HKCU" })}>
                <option value="HKLM">HKLM</option>
                <option value="HKCU">HKCU</option>
              </Select>
              <Input aria-label={t("configs.registry.key")} placeholder="SOFTWARE\Contoso\App" value={v.key} onChange={(e) => setRow(i, { key: e.target.value })} className="font-mono text-xs" />
              <Button
                variant="ghost"
                aria-label={t("common.delete")}
                icon={<Trash2 className="size-4" />}
                disabled={value.values.length === 1}
                onClick={() => onChange({ ...value, values: value.values.filter((_, j) => j !== i) })}
              />
            </div>
            <div className="grid gap-2 sm:grid-cols-[1fr_260px]">
              <Input aria-label={t("configs.registry.name")} placeholder={t("configs.registry.namePlaceholder")} value={v.name} onChange={(e) => setRow(i, { name: e.target.value })} className="font-mono text-xs" />
              <Select aria-label={t("configs.registry.type")} value={v.type} onChange={(e) => setRow(i, { type: e.target.value as RegistryType })}>
                {regTypes.map((rt) => (
                  <option key={rt} value={rt}>
                    {t(`configs.registry.types.${rt}`)}
                  </option>
                ))}
              </Select>
            </div>
            {v.type !== "delete" &&
              (v.type === "multistring" ? (
                <textarea
                  aria-label={t("configs.registry.value")}
                  rows={3}
                  className={`${textarea} font-mono text-xs`}
                  placeholder={t("configs.onePerLine")}
                  value={v.value ?? ""}
                  onChange={(e) => setRow(i, { value: e.target.value })}
                />
              ) : (
                <Input
                  aria-label={t("configs.registry.value")}
                  placeholder={v.type === "dword" || v.type === "qword" ? "1" : t("configs.registry.value")}
                  value={v.value ?? ""}
                  onChange={(e) => setRow(i, { value: e.target.value })}
                  className="font-mono text-xs"
                />
              ))}
          </div>
        ))}
        <Button variant="secondary" size="sm" icon={<Plus className="size-4" />} onClick={() => onChange({ ...value, values: [...value.values, { hive: "HKLM", key: "", name: "", type: "string", value: "" }] })}>
          {t("configs.registry.add")}
        </Button>
      </div>
      <Alert tone="info">{t("configs.registry.hint")}</Alert>
      <Switch checked={!!value.keep} onChange={(keep) => onChange({ ...value, keep })} label={t("configs.registry.keep")} description={t("configs.registry.keepHint")} />
    </div>
  );
}

export function ScriptForm({ value, onChange, platform, shells }: { value: ScriptSettings; onChange: (v: ScriptSettings) => void; platform: Platform; shells: ScriptShell[] }) {
  const { t } = useTranslation();
  const set = (patch: Partial<ScriptSettings>) => onChange({ ...value, ...patch });
  const triggers: ScriptTrigger[] = platform === "windows" ? ["once", "startup", "logon", "interval"] : ["once", "startup", "interval"];
  return (
    <div className="space-y-4">
      <div className="grid gap-4 sm:grid-cols-2">
        <Field label={t("configs.script.shell")}>
          {(id) => (
            <Select id={id} value={value.shell} onChange={(e) => set({ shell: e.target.value as ScriptShell })}>
              {shells.map((s) => (
                <option key={s} value={s}>
                  {t(`configs.script.shells.${s}`)}
                </option>
              ))}
            </Select>
          )}
        </Field>
        {platform === "windows" && (
          <Field label={t("configs.script.runAs")} hint={value.runAs === "user" ? t("configs.script.runAsUserHint") : t("configs.script.runAsSystemHint")}>
            {(id) => (
              <Select id={id} value={value.runAs} onChange={(e) => set({ runAs: e.target.value as "system" | "user" })}>
                <option value="system">{t("configs.script.system")}</option>
                <option value="user">{t("configs.script.user")}</option>
              </Select>
            )}
          </Field>
        )}
        <Field label={t("configs.script.trigger")} hint={t(`configs.script.triggerHints.${value.trigger}`)}>
          {(id) => (
            <Select id={id} value={value.trigger} onChange={(e) => set({ trigger: e.target.value as ScriptTrigger, intervalMinutes: e.target.value === "interval" ? value.intervalMinutes || 60 : undefined })}>
              {triggers.map((tr) => (
                <option key={tr} value={tr}>
                  {t(`configs.script.triggers.${tr}`)}
                </option>
              ))}
            </Select>
          )}
        </Field>
        {value.trigger === "interval" && (
          <Field label={t("configs.script.interval")} hint={t("configs.script.intervalHint")}>
            {(id) => <Input id={id} type="number" min={5} max={10080} value={value.intervalMinutes ?? 60} onChange={(e) => set({ intervalMinutes: Number(e.target.value) })} />}
          </Field>
        )}
        <Field label={t("configs.script.timeout")}>
          {(id) => <Input id={id} type="number" min={1} max={120} value={value.timeoutMinutes} onChange={(e) => set({ timeoutMinutes: Number(e.target.value) })} />}
        </Field>
      </div>
      <Field label={t("configs.script.content")} hint={t("configs.script.contentHint")}>
        {(id) => (
          <textarea
            id={id}
            rows={14}
            spellCheck={false}
            className={`${textarea} font-mono text-xs`}
            placeholder={value.shell === "powershell" ? 'New-Item -ItemType Directory -Force "C:\\Temp" | Out-Null' : "#!/bin/bash\nset -e\n"}
            value={value.content}
            onChange={(e) => set({ content: e.target.value })}
          />
        )}
      </Field>
    </div>
  );
}

function RemoveButton({ onClick, disabled }: { onClick: () => void; disabled?: boolean }) {
  const { t } = useTranslation();
  return <Button variant="ghost" aria-label={t("common.delete")} icon={<Trash2 className="size-4" />} disabled={disabled} onClick={onClick} />;
}

export function DrivesForm({ value, onChange }: { value: DriveSettings; onChange: (v: DriveSettings) => void }) {
  const { t } = useTranslation();
  const setRow = (i: number, patch: Partial<DriveMapping>) => onChange({ drives: value.drives.map((d, j) => (j === i ? { ...d, ...patch } : d)) });
  const used = value.drives.map((d) => d.letter);
  return (
    <div className="space-y-4">
      <div className="space-y-3">
        {value.drives.map((d, i) => (
          <div key={i} className="grid items-start gap-2 rounded-md border border-line p-3 sm:grid-cols-[90px_1fr_200px_auto]">
            <Select aria-label={t("configs.drives.letter")} value={d.letter} onChange={(e) => setRow(i, { letter: e.target.value })}>
              {"DEFGHIJKLMNOPQRSTUVWXYZ".split("").map((l) => (
                <option key={l} value={l} disabled={l !== d.letter && used.includes(l)}>
                  {l}:
                </option>
              ))}
            </Select>
            <Input aria-label={t("configs.drives.path")} placeholder="\\server\home\%USERNAME%" value={d.path} onChange={(e) => setRow(i, { path: e.target.value })} className="font-mono text-xs" />
            <Input aria-label={t("configs.drives.label")} placeholder={t("configs.drives.labelPlaceholder")} value={d.label ?? ""} onChange={(e) => setRow(i, { label: e.target.value })} maxLength={64} />
            <RemoveButton disabled={value.drives.length === 1} onClick={() => onChange({ drives: value.drives.filter((_, j) => j !== i) })} />
          </div>
        ))}
        <Button variant="secondary" size="sm" icon={<Plus className="size-4" />} disabled={value.drives.length >= 23} onClick={() => onChange({ drives: [...value.drives, newDrive(used)] })}>
          {t("configs.drives.add")}
        </Button>
      </div>
      <Alert tone="info">{t("configs.drives.hint")}</Alert>
    </div>
  );
}

function PrinterRow({ p, onChange, onRemove, canRemove }: { p: PrinterConfig; onChange: (p: PrinterConfig) => void; onRemove: () => void; canRemove: boolean }) {
  const { t } = useTranslation();
  const set = (patch: Partial<PrinterConfig>) => onChange({ ...p, ...patch });
  return (
    <div className="space-y-3 rounded-md border border-line p-3">
      <div className="grid gap-2 sm:grid-cols-[280px_1fr_auto]">
        <Select aria-label={t("configs.printers.kind")} value={p.kind} onChange={(e) => set(e.target.value === "ip" ? { kind: "ip", protocol: p.protocol ?? "raw", port: p.port ?? 9100 } : { kind: "shared", scope: p.scope ?? "device" })}>
          <option value="shared">{t("configs.printers.kinds.shared")}</option>
          <option value="ip">{t("configs.printers.kinds.ip")}</option>
        </Select>
        {p.kind === "shared" ? (
          <Input aria-label={t("configs.printers.path")} placeholder="\\printserver\Reception" value={p.path ?? ""} onChange={(e) => set({ path: e.target.value })} className="font-mono text-xs" />
        ) : (
          <Input aria-label={t("configs.printers.name")} placeholder={t("configs.printers.namePlaceholder")} value={p.name ?? ""} onChange={(e) => set({ name: e.target.value })} maxLength={100} />
        )}
        <RemoveButton disabled={!canRemove} onClick={onRemove} />
      </div>
      {p.kind === "shared" ? (
        <div className="grid gap-3 sm:grid-cols-2">
          <Field label={t("configs.printers.scope")} hint={t(`configs.printers.scopeHints.${p.scope ?? "device"}`)}>
            {(id) => (
              <Select id={id} value={p.scope ?? "device"} onChange={(e) => set({ scope: e.target.value as "device" | "user" })}>
                <option value="device">{t("configs.printers.scopes.device")}</option>
                <option value="user">{t("configs.printers.scopes.user")}</option>
              </Select>
            )}
          </Field>
          <Field label={t("configs.printers.driverOptional")} hint={t("configs.printers.sharedDriverHint")}>
            {(id) => <Input id={id} value={p.driver ?? ""} onChange={(e) => set({ driver: e.target.value })} placeholder="HP Universal Printing PCL 6" />}
          </Field>
        </div>
      ) : (
        <div className="grid gap-3 sm:grid-cols-[1fr_160px_140px]">
          <Field label={t("configs.printers.address")}>
            {(id) => <Input id={id} value={p.address ?? ""} onChange={(e) => set({ address: e.target.value })} placeholder="192.168.1.50" className="font-mono text-xs" />}
          </Field>
          <Field label={t("configs.printers.protocol")}>
            {(id) => (
              <Select id={id} value={p.protocol ?? "raw"} onChange={(e) => set({ protocol: e.target.value as "raw" | "lpr" })}>
                <option value="raw">RAW (9100)</option>
                <option value="lpr">LPR</option>
              </Select>
            )}
          </Field>
          {p.protocol === "lpr" ? (
            <Field label={t("configs.printers.queue")}>{(id) => <Input id={id} value={p.queue ?? ""} onChange={(e) => set({ queue: e.target.value })} placeholder="lp" />}</Field>
          ) : (
            <Field label={t("configs.printers.port")}>
              {(id) => <Input id={id} type="number" min={1} max={65535} value={p.port ?? 9100} onChange={(e) => set({ port: Number(e.target.value) })} />}
            </Field>
          )}
          <div className="sm:col-span-3">
            <Field label={t("configs.printers.driver")} hint={t("configs.printers.driverHint")}>
              {(id) => <Input id={id} value={p.driver ?? ""} onChange={(e) => set({ driver: e.target.value })} placeholder="Microsoft IPP Class Driver" />}
            </Field>
          </div>
        </div>
      )}
      {(p.kind === "ip" || p.driver) && (
        <Field label={t("configs.printers.driverInf")} hint={t("configs.printers.driverInfHint")}>
          {(id) => <Input id={id} value={p.driverInf ?? ""} onChange={(e) => set({ driverInf: e.target.value })} placeholder="\\server\drivers\hp\hpcu270u.inf" className="font-mono text-xs" />}
        </Field>
      )}
      <label className="flex cursor-pointer items-center gap-2 text-sm">
        <input type="checkbox" className="size-4 accent-primary" checked={!!p.default} onChange={(e) => set({ default: e.target.checked })} />
        {t("configs.printers.default")}
      </label>
    </div>
  );
}

export function PrintersForm({ value, onChange }: { value: PrinterSettings; onChange: (v: PrinterSettings) => void }) {
  const { t } = useTranslation();
  const setRow = (i: number, p: PrinterConfig) =>
    onChange({ printers: value.printers.map((x, j) => (j === i ? p : p.default ? { ...x, default: false } : x)) });
  return (
    <div className="space-y-4">
      <div className="space-y-3">
        {value.printers.map((p, i) => (
          <PrinterRow
            key={i}
            p={p}
            onChange={(np) => setRow(i, np)}
            canRemove={value.printers.length > 1}
            onRemove={() => onChange({ printers: value.printers.filter((_, j) => j !== i) })}
          />
        ))}
        <Button variant="secondary" size="sm" icon={<Plus className="size-4" />} disabled={value.printers.length >= 50} onClick={() => onChange({ printers: [...value.printers, newPrinter()] })}>
          {t("configs.printers.add")}
        </Button>
      </div>
      <Alert tone="info">{t("configs.printers.hint")}</Alert>
    </div>
  );
}

const wallpaperStyles: WallpaperStyle[] = ["fill", "fit", "stretch", "center", "tile", "span"];

export function DesktopForm({ value, onChange }: { value: DesktopSettings; onChange: (v: DesktopSettings) => void }) {
  const { t } = useTranslation();
  const set = (patch: Partial<DesktopSettings>) => onChange({ ...value, ...patch });
  return (
    <div className="space-y-5">
      <div className="grid gap-3 sm:grid-cols-[1fr_200px]">
        <Field label={t("configs.desktop.wallpaper")} hint={t("configs.desktop.wallpaperHint")}>
          {(id) => <Input id={id} value={value.wallpaper ?? ""} onChange={(e) => set({ wallpaper: e.target.value })} placeholder="\\server\branding\wallpaper.jpg" className="font-mono text-xs" />}
        </Field>
        <Field label={t("configs.desktop.style")}>
          {(id) => (
            <Select id={id} value={value.wallpaperStyle ?? "fill"} onChange={(e) => set({ wallpaperStyle: e.target.value as WallpaperStyle })} disabled={!value.wallpaper}>
              {wallpaperStyles.map((s) => (
                <option key={s} value={s}>
                  {t(`configs.desktop.styles.${s}`)}
                </option>
              ))}
            </Select>
          )}
        </Field>
      </div>
      <Field label={t("configs.desktop.lock")} hint={t("configs.desktop.lockHint")} className="max-w-xs">
        {(id) => (
          <Input
            id={id}
            type="number"
            min={0}
            max={9999}
            value={value.lockMinutes ?? 0}
            onChange={(e) => set({ lockMinutes: Math.max(0, Math.min(9999, Number(e.target.value) || 0)) })}
          />
        )}
      </Field>
      <Alert tone="info">{t("configs.desktop.hint")}</Alert>
    </div>
  );
}

const powerRows: { key: "monitor" | "sleep" | "hibernate"; ac: keyof PowerSettings; dc: keyof PowerSettings }[] = [
  { key: "monitor", ac: "monitorAc", dc: "monitorDc" },
  { key: "sleep", ac: "sleepAc", dc: "sleepDc" },
  { key: "hibernate", ac: "hibernateAc", dc: "hibernateDc" },
];

// MinutesOrUnmanaged edits a number of minutes; empty is "not managed".
function MinutesOrUnmanaged({ id, value, onChange }: { id: string; value?: number | null; onChange: (v: number | null) => void }) {
  const { t } = useTranslation();
  return (
    <Input
      id={id}
      type="number"
      min={0}
      max={10080}
      placeholder={t("configs.notManaged")}
      value={value ?? ""}
      onChange={(e) => onChange(e.target.value === "" ? null : Math.max(0, Math.min(10080, Number(e.target.value) || 0)))}
    />
  );
}

export function PowerForm({ value, onChange }: { value: PowerSettings; onChange: (v: PowerSettings) => void }) {
  const { t } = useTranslation();
  const set = (k: keyof PowerSettings, v: number | null) => {
    const next = { ...value };
    if (v === null) delete next[k];
    else next[k] = v;
    onChange(next);
  };
  return (
    <div className="space-y-4">
      <div className="overflow-x-auto">
        <table className="w-full min-w-[420px] text-sm">
          <thead>
            <tr className="text-left text-xs uppercase tracking-wide text-muted">
              <th className="py-2 pr-3 font-medium">{t("configs.power.setting")}</th>
              <th className="py-2 pr-3 font-medium">{t("configs.power.ac")}</th>
              <th className="py-2 font-medium">{t("configs.power.dc")}</th>
            </tr>
          </thead>
          <tbody>
            {powerRows.map((r) => (
              <tr key={r.key} className="border-t border-line">
                <td className="py-2 pr-3 font-medium">{t(`configs.power.${r.key}`)}</td>
                <td className="py-2 pr-3">
                  <MinutesOrUnmanaged id={`${r.key}-ac`} value={value[r.ac]} onChange={(v) => set(r.ac, v)} />
                </td>
                <td className="py-2">
                  <MinutesOrUnmanaged id={`${r.key}-dc`} value={value[r.dc]} onChange={(v) => set(r.dc, v)} />
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <Alert tone="info">{t("configs.power.hint")}</Alert>
    </div>
  );
}

export function AdminsForm({ value, onChange }: { value: AdminSettings; onChange: (v: AdminSettings) => void }) {
  const { t } = useTranslation();
  return (
    <div className="space-y-4">
      <Field label={t("configs.admins.members")} hint={t("configs.admins.membersHint")}>
        {(id) => <ListInput id={id} value={value.members} onChange={(members) => onChange({ members })} placeholder={"CONTOSO\\IT Support\nAzureAD\\anna@contoso.com\nbeheer"} rows={5} />}
      </Field>
      <Alert tone="warning">{t("configs.admins.hint")}</Alert>
    </div>
  );
}

const protocols: FirewallProtocol[] = ["any", "tcp", "udp", "icmpv4", "icmpv6"];
const fwProfiles: FirewallProfile[] = ["domain", "private", "public"];

function RuleRow({ r, onChange, onRemove, canRemove }: { r: FirewallRule; onChange: (r: FirewallRule) => void; onRemove: () => void; canRemove: boolean }) {
  const { t } = useTranslation();
  const set = (patch: Partial<FirewallRule>) => onChange({ ...r, ...patch });
  const hasPorts = r.protocol === "tcp" || r.protocol === "udp";
  const profiles = r.profiles ?? [];
  const toggle = (p: FirewallProfile) => set({ profiles: profiles.includes(p) ? profiles.filter((x) => x !== p) : [...profiles, p] });
  return (
    <div className="space-y-3 rounded-md border border-line p-3">
      <div className="grid gap-2 sm:grid-cols-[1fr_150px_150px_150px_auto]">
        <Input aria-label={t("configs.firewall.name")} placeholder={t("configs.firewall.namePlaceholder")} value={r.name} onChange={(e) => set({ name: e.target.value })} maxLength={100} />
        <Select aria-label={t("configs.firewall.direction")} value={r.direction} onChange={(e) => set({ direction: e.target.value as "in" | "out" })}>
          <option value="in">{t("configs.firewall.directions.in")}</option>
          <option value="out">{t("configs.firewall.directions.out")}</option>
        </Select>
        <Select aria-label={t("configs.firewall.action")} value={r.action} onChange={(e) => set({ action: e.target.value as "allow" | "block" })}>
          <option value="allow">{t("configs.firewall.actions.allow")}</option>
          <option value="block">{t("configs.firewall.actions.block")}</option>
        </Select>
        <Select
          aria-label={t("configs.firewall.protocol")}
          value={r.protocol}
          onChange={(e) => {
            const protocol = e.target.value as FirewallProtocol;
            set(protocol === "tcp" || protocol === "udp" ? { protocol } : { protocol, localPorts: "", remotePorts: "" });
          }}
        >
          {protocols.map((p) => (
            <option key={p} value={p}>
              {p === "any" ? t("configs.firewall.anyProtocol") : p.toUpperCase().replace("ICMPV", "ICMPv")}
            </option>
          ))}
        </Select>
        <RemoveButton disabled={!canRemove} onClick={onRemove} />
      </div>
      <div className="grid gap-3 sm:grid-cols-3">
        <Field label={t("configs.firewall.localPorts")}>
          {(id) => <Input id={id} value={r.localPorts ?? ""} onChange={(e) => set({ localPorts: e.target.value })} placeholder={hasPorts ? "80,443,8000-8080" : t("configs.firewall.any")} disabled={!hasPorts} className="font-mono text-xs" />}
        </Field>
        <Field label={t("configs.firewall.remotePorts")}>
          {(id) => <Input id={id} value={r.remotePorts ?? ""} onChange={(e) => set({ remotePorts: e.target.value })} placeholder={t("configs.firewall.any")} disabled={!hasPorts} className="font-mono text-xs" />}
        </Field>
        <Field label={t("configs.firewall.remoteAddresses")}>
          {(id) => <Input id={id} value={r.remoteAddresses ?? ""} onChange={(e) => set({ remoteAddresses: e.target.value })} placeholder="192.168.1.0/24, LocalSubnet" className="font-mono text-xs" />}
        </Field>
      </div>
      <div className="grid gap-3 sm:grid-cols-[1fr_auto]">
        <Field label={t("configs.firewall.program")}>
          {(id) => <Input id={id} value={r.program ?? ""} onChange={(e) => set({ program: e.target.value })} placeholder="C:\Program Files\App\app.exe" className="font-mono text-xs" />}
        </Field>
        <div className="space-y-1.5">
          <p className="text-sm font-medium">{t("configs.firewall.profiles")}</p>
          <div className="flex flex-wrap gap-2">
            {fwProfiles.map((p) => (
              <label key={p} className="flex cursor-pointer items-center gap-2 rounded-md border border-line px-3 py-2 text-sm hover:bg-surface-muted">
                <input type="checkbox" className="size-4 accent-primary" checked={profiles.length === 0 || profiles.includes(p)} onChange={() => toggle(p)} />
                {t(`configs.firewall.profileNames.${p}`)}
              </label>
            ))}
          </div>
        </div>
      </div>
    </div>
  );
}

export function FirewallForm({ value, onChange }: { value: FirewallSettings; onChange: (v: FirewallSettings) => void }) {
  const { t } = useTranslation();
  return (
    <div className="space-y-4">
      <div className="space-y-3">
        {value.rules.map((r, i) => (
          <RuleRow
            key={i}
            r={r}
            onChange={(nr) => onChange({ rules: value.rules.map((x, j) => (j === i ? nr : x)) })}
            canRemove={value.rules.length > 1}
            onRemove={() => onChange({ rules: value.rules.filter((_, j) => j !== i) })}
          />
        ))}
        <Button variant="secondary" size="sm" icon={<Plus className="size-4" />} disabled={value.rules.length >= 50} onClick={() => onChange({ rules: [...value.rules, newRule()] })}>
          {t("configs.firewall.add")}
        </Button>
      </div>
      <Alert tone="info">{t("configs.firewall.hint")}</Alert>
    </div>
  );
}
