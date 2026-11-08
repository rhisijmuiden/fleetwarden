import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { Plus, Trash2 } from "lucide-react";
import type {
  BrowserKey,
  BrowserSettings,
  ConfigSettings,
  ConfigType,
  Platform,
  RegistrySettings,
  RegistryType,
  RegistryValue,
  ScriptSettings,
  ScriptShell,
  ScriptTrigger,
  StartupMode,
} from "../../api";
import { Alert, Button, Field, Input, Select, Switch } from "../../components/ui";

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
