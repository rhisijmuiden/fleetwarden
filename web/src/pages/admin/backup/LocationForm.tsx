import { useState } from "react";
import { useTranslation } from "react-i18next";
import { CheckCircle2, KeyRound, PlugZap, ShieldAlert } from "lucide-react";
import { post, type BackupLocation, type LocationTest, type LocationType } from "../../../api";
import { Alert, Button, CopyField, Field, Input, Select, Switch, useErrorText } from "../../../components/ui";
import { locationTypes } from "./util";

// The form state of a location: the stored fields plus write-only secrets.
export type LocationDraft = {
  id: number;
  name: string;
  enabled: boolean;
  type: LocationType;
  host: string;
  port: number;
  username: string;
  password: string;
  passwordSet: boolean;
  implicitTls: boolean;
  tlsFingerprint: string;
  sshAuth: "password" | "key";
  privateKey: string;
  publicKey: string;
  hostKey: string;
  hostKeyFingerprint: string;
  tenantId: string;
  clientId: string;
  clientSecret: string;
  clientSecretSet: boolean;
  driveUser: string;
  path: string;
};

export function emptyDraft(): LocationDraft {
  return {
    id: 0, name: "", enabled: true, type: "sftp", host: "", port: 22, username: "", password: "", passwordSet: false,
    implicitTls: false, tlsFingerprint: "", sshAuth: "password", privateKey: "", publicKey: "", hostKey: "", hostKeyFingerprint: "",
    tenantId: "", clientId: "", clientSecret: "", clientSecretSet: false, driveUser: "", path: "fleetwarden",
  };
}

export function draftOf(l: BackupLocation): LocationDraft {
  return { ...emptyDraft(), ...l, password: "", privateKey: "", clientSecret: "", tlsFingerprint: l.tlsFingerprint.replace(/:/g, "") };
}

// What the API expects for a location form.
export function draftBody(d: LocationDraft) {
  return {
    id: d.id, name: d.name, enabled: d.enabled, type: d.type, host: d.host, port: Number(d.port) || 0, username: d.username,
    password: d.password || undefined, implicitTls: d.implicitTls, tlsFingerprint: d.tlsFingerprint, sshAuth: d.sshAuth,
    privateKey: d.privateKey || undefined, hostKey: d.hostKey, tenantId: d.tenantId, clientId: d.clientId,
    clientSecret: d.clientSecret || undefined, driveUser: d.driveUser, path: d.path,
  };
}

function defaultPort(type: LocationType, implicitTls: boolean): number {
  if (type === "sftp") return 22;
  if (type === "ftps" && implicitTls) return 990;
  if (type === "ftp" || type === "ftps") return 21;
  return 0;
}

type Props = {
  draft: LocationDraft;
  onChange: (d: LocationDraft) => void;
  // Without a name and enabled switch: used to find backups on a new server.
  bare?: boolean;
};

export function LocationForm({ draft: d, onChange, bare }: Props) {
  const { t } = useTranslation();
  const errorText = useErrorText();
  const [test, setTest] = useState<LocationTest | null>(null);
  const [testError, setTestError] = useState<string | null>(null);
  const [testing, setTesting] = useState(false);
  const set = (patch: Partial<LocationDraft>) => onChange({ ...d, ...patch });

  const setType = (type: LocationType) => {
    const port = d.port === defaultPort(d.type, d.implicitTls) || !d.port ? defaultPort(type, d.implicitTls) : d.port;
    set({ type, port });
    setTest(null);
  };

  const runTest = async (next = d) => {
    setTesting(true);
    setTest(null);
    setTestError(null);
    try {
      setTest(await post<LocationTest>("/backup/locations/test", draftBody(next)));
    } catch (e) {
      setTestError(errorText(e));
    } finally {
      setTesting(false);
    }
  };

  const trust = () => {
    if (!test?.trust) return;
    const next =
      test.trust.kind === "ssh"
        ? { ...d, hostKey: test.trust.value, hostKeyFingerprint: test.trust.fingerprint }
        : { ...d, tlsFingerprint: test.trust.value };
    onChange(next);
    void runTest(next);
  };

  const generateKey = async () => {
    try {
      const k = await post<{ privateKey: string; publicKey: string }>("/backup/ssh-key");
      set({ privateKey: k.privateKey, publicKey: k.publicKey, sshAuth: "key" });
    } catch (e) {
      setTestError(errorText(e));
    }
  };

  const remote = d.type === "ftp" || d.type === "ftps" || d.type === "sftp";
  return (
    <div className="space-y-4">
      {!bare && (
        <div className="grid gap-4 sm:grid-cols-2">
          <Field label={t("backup.loc.name")}>{(id) => <Input id={id} value={d.name} required maxLength={80} onChange={(e) => set({ name: e.target.value })} placeholder={t("backup.loc.namePlaceholder")} />}</Field>
          <Field label={t("backup.loc.type")}>
            {(id) => (
              <Select id={id} value={d.type} onChange={(e) => setType(e.target.value as LocationType)}>
                {locationTypes.map((lt) => (
                  <option key={lt} value={lt}>
                    {t(`backup.types.${lt}`)}
                  </option>
                ))}
              </Select>
            )}
          </Field>
        </div>
      )}
      {bare && (
        <Field label={t("backup.loc.type")}>
          {(id) => (
            <Select id={id} value={d.type} onChange={(e) => setType(e.target.value as LocationType)}>
              {locationTypes.map((lt) => (
                <option key={lt} value={lt}>
                  {t(`backup.types.${lt}`)}
                </option>
              ))}
            </Select>
          )}
        </Field>
      )}
      <p className="text-xs text-muted">{t(`backup.typeHints.${d.type}`)}</p>
      {d.type === "ftp" && <Alert tone="warning">{t("backup.loc.ftpWarning")}</Alert>}

      {remote && (
        <>
          <div className="grid gap-4 sm:grid-cols-[1fr_110px]">
            <Field label={t("backup.loc.host")}>{(id) => <Input id={id} value={d.host} onChange={(e) => set({ host: e.target.value })} placeholder="nas.example.com" />}</Field>
            <Field label={t("backup.loc.port")}>{(id) => <Input id={id} type="number" min={1} max={65535} value={d.port} onChange={(e) => set({ port: Number(e.target.value) })} />}</Field>
          </div>
          {d.type === "ftps" && (
            <Switch
              checked={d.implicitTls}
              onChange={(v) => set({ implicitTls: v, port: d.port === defaultPort("ftps", d.implicitTls) ? defaultPort("ftps", v) : d.port })}
              label={t("backup.loc.implicitTls")}
              description={t("backup.loc.implicitTlsHint")}
            />
          )}
          <div className="grid gap-4 sm:grid-cols-2">
            <Field label={t("backup.loc.username")}>{(id) => <Input id={id} value={d.username} autoComplete="off" onChange={(e) => set({ username: e.target.value })} />}</Field>
            {d.type === "sftp" && (
              <Field label={t("backup.loc.sshAuth")}>
                {(id) => (
                  <Select id={id} value={d.sshAuth} onChange={(e) => set({ sshAuth: e.target.value as "password" | "key" })}>
                    <option value="password">{t("backup.loc.authPassword")}</option>
                    <option value="key">{t("backup.loc.authKey")}</option>
                  </Select>
                )}
              </Field>
            )}
            {(d.type !== "sftp" || d.sshAuth === "password") && (
              <Field label={t("backup.loc.password")} hint={d.passwordSet ? t("common.secretKept") : undefined}>
                {(id) => <Input id={id} type="password" autoComplete="new-password" value={d.password} placeholder={d.passwordSet ? "••••••••" : ""} onChange={(e) => set({ password: e.target.value })} />}
              </Field>
            )}
          </div>
          {d.type === "sftp" && d.sshAuth === "key" && (
            <div className="space-y-3 rounded-md border border-line bg-surface-muted/50 p-4">
              <p className="text-sm text-muted">{t("backup.loc.keyHint")}</p>
              {d.publicKey ? (
                <Field label={t("backup.loc.publicKey")} hint={t("backup.loc.publicKeyHint")}>
                  {() => <CopyField value={d.publicKey} label={t("backup.loc.publicKey")} />}
                </Field>
              ) : null}
              <div className="flex flex-wrap gap-2">
                <Button variant="secondary" size="sm" icon={<KeyRound className="size-4" />} onClick={() => void generateKey()}>
                  {d.publicKey ? t("backup.loc.newKey") : t("backup.loc.generateKey")}
                </Button>
              </div>
              <Field label={t("backup.loc.ownKey")}>
                {(id) => (
                  <textarea
                    id={id}
                    rows={3}
                    value={d.privateKey}
                    onChange={(e) => set({ privateKey: e.target.value, publicKey: e.target.value ? "" : d.publicKey })}
                    placeholder="-----BEGIN OPENSSH PRIVATE KEY-----"
                    className="w-full rounded-md border border-line bg-surface px-3 py-2 font-mono text-xs text-fg outline-none focus-visible:ring-2 focus-visible:ring-primary"
                  />
                )}
              </Field>
            </div>
          )}
          {d.type === "sftp" && d.hostKeyFingerprint && <p className="text-xs text-muted">{t("backup.loc.trustedKey", { fp: d.hostKeyFingerprint })}</p>}
          {d.type === "ftps" && d.tlsFingerprint && <p className="text-xs break-all text-muted">{t("backup.loc.trustedCert", { fp: d.tlsFingerprint })}</p>}
        </>
      )}

      {d.type === "onedrive" && (
        <div className="space-y-4">
          <Alert tone="info">{t("backup.loc.onedriveHint")}</Alert>
          <div className="grid gap-4 sm:grid-cols-2">
            <Field label={t("auth.tenantId")}>{(id) => <Input id={id} className="font-mono" value={d.tenantId} onChange={(e) => set({ tenantId: e.target.value })} />}</Field>
            <Field label={t("auth.clientId")}>{(id) => <Input id={id} className="font-mono" value={d.clientId} onChange={(e) => set({ clientId: e.target.value })} />}</Field>
          </div>
          <div className="grid gap-4 sm:grid-cols-2">
            <Field label={t("auth.clientSecret")} hint={d.clientSecretSet ? t("common.secretKept") : undefined}>
              {(id) => <Input id={id} type="password" autoComplete="off" value={d.clientSecret} placeholder={d.clientSecretSet ? "••••••••" : ""} onChange={(e) => set({ clientSecret: e.target.value })} />}
            </Field>
            <Field label={t("backup.loc.driveUser")} hint={t("backup.loc.driveUserHint")}>
              {(id) => <Input id={id} type="email" value={d.driveUser} onChange={(e) => set({ driveUser: e.target.value })} placeholder="backup@example.com" />}
            </Field>
          </div>
        </div>
      )}

      <Field label={t("backup.loc.path")} hint={d.type === "local" ? t("backup.loc.localPathHint") : t("backup.loc.pathHint")}>
        {(id) => <Input id={id} value={d.path} onChange={(e) => set({ path: e.target.value })} placeholder="fleetwarden" />}
      </Field>

      {!bare && <Switch checked={d.enabled} onChange={(v) => set({ enabled: v })} label={t("backup.loc.enabled")} description={t("backup.loc.enabledHint")} />}

      {!bare && (
        <div className="space-y-3 border-t border-line pt-4">
          <Button variant="secondary" icon={<PlugZap className="size-4" />} loading={testing} onClick={() => void runTest()}>
            {t("backup.loc.test")}
          </Button>
          {testError && <Alert tone="danger">{testError}</Alert>}
          {test?.trust && (
            <Alert tone="warning" title={test.trust.mismatch ? t("backup.trust.changedTitle") : t("backup.trust.newTitle")}>
              <p>{t(test.trust.kind === "ssh" ? "backup.trust.sshBody" : "backup.trust.tlsBody")}</p>
              <p className="mt-1 font-mono text-xs break-all">{test.trust.fingerprint}</p>
              <Button size="sm" className="mt-2" icon={<ShieldAlert className="size-4" />} onClick={trust}>
                {t("backup.trust.accept")}
              </Button>
            </Alert>
          )}
          {test && !test.trust && !test.ok && <Alert tone="danger">{test.error}</Alert>}
          {test?.ok && (
            <Alert tone={test.repository === "wrong_passphrase" ? "warning" : "success"} title={t("backup.loc.testOk")}>
              <span className="inline-flex items-center gap-1">
                <CheckCircle2 className="size-3.5" />
                {t(`backup.repo.${test.repository}`, { count: test.snapshots })}
              </span>
            </Alert>
          )}
        </div>
      )}
    </div>
  );
}
