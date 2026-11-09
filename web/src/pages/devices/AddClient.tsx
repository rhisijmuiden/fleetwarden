import { useEffect, useState } from "react";
import { Link } from "react-router";
import { useTranslation } from "react-i18next";
import { CheckCircle2, Download, Loader2, Terminal } from "lucide-react";
import { get, post, type Device, type EnrollmentToken, type GatewayOption, type InstallInfo, type Platform } from "../../api";
import { Alert, Button, CopyField, Field, Input, Modal, Select, useErrorText } from "../../components/ui";
import { useGroups } from "../../components/GroupScope";
import { KindIcon } from "./util";

type Props = {
  open: boolean;
  onClose: () => void;
  // Show the install commands of an existing token instead of creating one.
  token?: EnrollmentToken | null;
  defaultGroup?: number;
};

// The Add client wizard: choose platform and group, get a download and a
// ready-made command, and watch the new device connect.
export function AddClient({ open, onClose, token, defaultGroup }: Props) {
  const { t } = useTranslation();
  const errorText = useErrorText();
  const groups = useGroups(open ? 1 : 0);
  const [platform, setPlatform] = useState<Platform>("windows");
  const [group, setGroup] = useState("");
  const [expires, setExpires] = useState("30");
  const [description, setDescription] = useState("");
  const [created, setCreated] = useState<EnrollmentToken | null>(null);
  const [info, setInfo] = useState<InstallInfo | null>(null);
  const [gateways, setGateways] = useState<GatewayOption[]>([]);
  const [via, setVia] = useState("");
  const [since, setSince] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!open) return;
    setError(null);
    setCreated(token ?? null);
    setInfo(null);
    setDescription("");
    setSince(new Date(Date.now() - 60_000).toISOString());
    setVia("");
    get<GatewayOption[]>("/gateways/options")
      .then(setGateways)
      .catch(() => setGateways([]));
    if (token) {
      get<InstallInfo>(`/enrollment-tokens/${token.id}/install`)
        .then(setInfo)
        .catch((e) => setError(errorText(e)));
    }
  }, [open, token]);

  useEffect(() => {
    if (groups && !group) setGroup(String(defaultGroup ?? groups[0]?.id ?? ""));
  }, [groups]);

  // Install commands for devices outside the network point at a gateway.
  const changeVia = async (v: string) => {
    setVia(v);
    if (!created) return;
    try {
      setInfo(await get<InstallInfo>(`/enrollment-tokens/${created.id}/install${v ? `?via=${v}` : ""}`));
    } catch (e) {
      setError(errorText(e));
    }
  };

  const create = async () => {
    setBusy(true);
    setError(null);
    try {
      const tok = await post<EnrollmentToken>("/enrollment-tokens", {
        groupId: Number(group),
        description: description || t("addClient.defaultDescription", { platform: platform === "windows" ? "Windows" : "Linux" }),
        expiresInDays: Number(expires),
      });
      setSince(tok.createdAt);
      setCreated(tok);
      setInfo(await get<InstallInfo>(`/enrollment-tokens/${tok.id}/install${via ? `?via=${via}` : ""}`));
    } catch (e) {
      setError(errorText(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Modal
      open={open}
      onClose={onClose}
      wide
      title={created ? t("addClient.installTitle", { group: created.groupName }) : t("addClient.title")}
      footer={
        created ? (
          <Button onClick={onClose}>{t("common.done")}</Button>
        ) : (
          <>
            <Button variant="secondary" onClick={onClose}>
              {t("common.cancel")}
            </Button>
            <Button loading={busy} disabled={!group} onClick={create}>
              {t("addClient.create")}
            </Button>
          </>
        )
      }
    >
      {error && <Alert tone="danger">{error}</Alert>}
      {!created ? (
        <div className="space-y-5">
          <div>
            <p className="mb-2 text-sm font-medium">{t("addClient.platform")}</p>
            <div className="grid gap-3 sm:grid-cols-2">
              {(["windows", "linux"] as Platform[]).map((p) => (
                <button
                  key={p}
                  type="button"
                  onClick={() => setPlatform(p)}
                  className={`rounded-lg border p-4 text-left transition-colors ${platform === p ? "border-primary bg-primary/5 ring-1 ring-primary" : "border-line hover:bg-surface-muted"}`}
                >
                  <span className="block font-medium">{t(`addClient.platforms.${p}`)}</span>
                  <span className="mt-0.5 block text-xs text-muted">{t(`addClient.platformHints.${p}`)}</span>
                </button>
              ))}
            </div>
          </div>
          <div className="grid gap-4 sm:grid-cols-2">
            <Field label={t("devices.group")} hint={t("addClient.groupHint")}>
              {(id) => (
                <Select id={id} value={group} onChange={(e) => setGroup(e.target.value)}>
                  {(groups ?? []).map((g) => (
                    <option key={g.id} value={g.id}>
                      {g.name}
                    </option>
                  ))}
                </Select>
              )}
            </Field>
            <Field label={t("tokens.expires")}>
              {(id) => (
                <Select id={id} value={expires} onChange={(e) => setExpires(e.target.value)}>
                  {["1", "7", "30", "90", "365", "0"].map((d) => (
                    <option key={d} value={d}>
                      {d === "0" ? t("tokens.never") : t("tokens.days", { count: Number(d) })}
                    </option>
                  ))}
                </Select>
              )}
            </Field>
          </div>
          <Field label={t("tokens.description")} hint={t("tokens.descriptionHint")}>
            {(id) => <Input id={id} value={description} onChange={(e) => setDescription(e.target.value)} maxLength={200} />}
          </Field>
        </div>
      ) : !info ? (
        <div className="grid place-items-center py-8">
          <Loader2 className="size-6 animate-spin text-muted" />
        </div>
      ) : (
        <div className="space-y-5">
          {gateways.length > 0 && <ViaSelect gateways={gateways} value={via} onChange={changeVia} />}
          <InstallSteps info={info} platform={token ? null : platform} tokenId={created.id} since={since} />
        </div>
      )}
    </Modal>
  );
}

function ViaSelect({ gateways, value, onChange }: { gateways: GatewayOption[]; value: string; onChange: (v: string) => void }) {
  const { t } = useTranslation();
  return (
    <Field label={t("addClient.via")} hint={t("addClient.viaHint")}>
      {(id) => (
        <Select id={id} value={value} onChange={(e) => onChange(e.target.value)}>
          <option value="">{t("addClient.viaDirect")}</option>
          {gateways.map((g) => (
            <option key={g.id} value={g.id}>
              {t("addClient.viaGateway", { name: g.name, url: g.publicUrl })}
            </option>
          ))}
        </Select>
      )}
    </Field>
  );
}

function InstallSteps({ info, platform, tokenId, since }: { info: InstallInfo; platform: Platform | null; tokenId: number; since: string }) {
  const { t } = useTranslation();
  const [devices, setDevices] = useState<Device[]>([]);
  const show = (p: Platform) => platform === null || platform === p;
  const downloads = info.downloads.filter((d) => show(d.platform));

  useEffect(() => {
    const poll = () =>
      get<Device[]>(`/enrollment-tokens/${tokenId}/devices?since=${encodeURIComponent(since)}`)
        .then(setDevices)
        .catch(() => undefined);
    poll();
    const id = setInterval(poll, 3000);
    return () => clearInterval(id);
  }, [tokenId, since]);

  return (
    <div className="space-y-5">
      {!info.usable && <Alert tone="warning">{t("addClient.tokenUnusable")}</Alert>}
      {show("windows") && (
        <section className="space-y-2">
          <h3 className="flex items-center gap-2 text-sm font-semibold">
            <Terminal className="size-4 text-primary" /> {t("addClient.windowsCommand")}
          </h3>
          <CopyField value={info.windowsCommand} label={t("addClient.windowsCommand")} />
          <p className="text-xs text-muted">{t("addClient.windowsCommandHint")}</p>
          <details className="text-xs text-muted">
            <summary className="cursor-pointer select-none">{t("addClient.msiexec")}</summary>
            <div className="mt-2">
              <CopyField value={info.msiexecCommand} label={t("addClient.msiexec")} />
            </div>
          </details>
        </section>
      )}
      {show("linux") && (
        <section className="space-y-2">
          <h3 className="flex items-center gap-2 text-sm font-semibold">
            <Terminal className="size-4 text-primary" /> {t("addClient.linuxCommand")}
          </h3>
          <CopyField value={info.linuxCommand} label={t("addClient.linuxCommand")} />
          <p className="text-xs text-muted">{t("addClient.linuxCommandHint")}</p>
        </section>
      )}
      <section className="space-y-2">
        <h3 className="text-sm font-semibold">{t("addClient.download")}</h3>
        {downloads.length === 0 ? (
          <Alert tone="warning">{t("addClient.noPackage")}</Alert>
        ) : (
          <div className="flex flex-wrap gap-2">
            {downloads.map((d) => (
              <a
                key={d.url}
                href={d.url}
                className="inline-flex h-9 items-center gap-2 rounded-md border border-line bg-surface px-4 text-sm font-medium hover:bg-surface-muted"
              >
                <Download className="size-4" />
                {d.kind === "msi" ? t("addClient.msi") : t("addClient.deb", { arch: d.arch })}
                <span className="text-xs text-muted">{d.version}</span>
              </a>
            ))}
          </div>
        )}
        <p className="text-xs text-muted">{t("addClient.downloadHint")}</p>
      </section>
      <details className="text-xs text-muted">
        <summary className="cursor-pointer select-none">{t("addClient.manual")}</summary>
        <div className="mt-2 space-y-2">
          <p>{t("addClient.manualHint")}</p>
          <CopyField value={info.serverUrl} label={t("addClient.server")} />
          <CopyField value={info.token} label={t("addClient.token")} />
        </div>
      </details>
      <section className="rounded-lg border border-line bg-surface-muted/50 p-4" aria-live="polite">
        {devices.length === 0 ? (
          <p className="flex items-center gap-2 text-sm text-muted">
            <Loader2 className="size-4 animate-spin" /> {t("addClient.waiting")}
          </p>
        ) : (
          <ul className="space-y-2">
            {devices.map((d) => (
              <li key={d.id} className="flex items-center gap-2 text-sm">
                <CheckCircle2 className="size-4 text-success" />
                <KindIcon device={d} />
                <Link to={`/devices/${d.id}`} className="font-medium hover:text-primary">
                  {d.hostname}
                </Link>
                <span className="text-muted">{d.online ? t("addClient.connected") : t("addClient.enrolled")}</span>
              </li>
            ))}
          </ul>
        )}
      </section>
    </div>
  );
}
