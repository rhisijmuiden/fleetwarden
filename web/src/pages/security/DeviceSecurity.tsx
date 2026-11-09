import { useCallback, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { Eye, RefreshCw, RotateCcw, ScanSearch, ShieldCheck, DownloadCloud } from "lucide-react";
import { get, post, type Device, type DeviceSecurity, type RecoveryKey, type SecurityActionName } from "../../api";
import { useAuth } from "../../auth";
import { Alert, Badge, Button, Card, Spinner, formatDate, useErrorText } from "../../components/ui";
import { timeAgo } from "../devices/util";
import { AntivirusBadge, EncryptionBadge, FirewallBadge, RevealDialog, RotateDialog, YesNo } from "./util";

const stateTone = { queued: "neutral", running: "primary", completed: "success", failed: "danger" } as const;

export function DeviceSecurityTab({ device }: { device: Device }) {
  const { t, i18n } = useTranslation();
  const { can } = useAuth();
  const errorText = useErrorText();
  const [data, setData] = useState<DeviceSecurity | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [busy, setBusy] = useState("");
  const [reveal, setReveal] = useState<RecoveryKey | null>(null);
  const [rotate, setRotate] = useState<string | null>(null);
  const manage = can("devices.manage");
  const windows = device.platform === "windows";

  const load = useCallback(() => {
    get<DeviceSecurity>(`/devices/${device.id}/security`)
      .then(setData)
      .catch((e) => setError(errorText(e)));
  }, [device.id]);
  useEffect(() => {
    load();
    const id = setInterval(load, 10000);
    return () => clearInterval(id);
  }, [load]);

  if (error && !data) return <Alert tone="danger">{error}</Alert>;
  if (!data) return <Spinner />;

  const refresh = () => {
    setBusy("refresh");
    setError(null);
    post(`/devices/${device.id}/security/refresh`)
      .then(() => {
        setNotice(t("security.device.refreshRequested"));
        setTimeout(load, 4000);
      })
      .catch((e) => setError(errorText(e)))
      .finally(() => setBusy(""));
  };
  const run = (action: SecurityActionName) => {
    setBusy(action);
    setError(null);
    post(`/devices/${device.id}/security/actions`, { action })
      .then(() => {
        setNotice(t(device.online ? "security.device.actionStarted" : "security.device.actionQueued", { action: t(`security.actions.${action}`) }));
        load();
      })
      .catch((e) => setError(errorText(e)))
      .finally(() => setBusy(""));
  };

  const rep = data.report;
  const sm = data.summary;
  const def = rep?.defender;
  const open = (a: SecurityActionName) => data.actions.some((x) => x.action === a && (x.state === "queued" || x.state === "running"));
  const threats = def?.threats ?? [];

  return (
    <div className="space-y-6">
      {error && <Alert tone="danger">{error}</Alert>}
      {notice && <Alert tone="success">{notice}</Alert>}

      <Card
        title={t("security.device.title")}
        description={
          data.collectedAt
            ? t("security.device.collected", { when: `${formatDate(data.collectedAt, i18n.language)} (${timeAgo(data.collectedAt, i18n.language)})` })
            : t("security.device.never")
        }
        actions={
          manage && (
            <Button variant="secondary" size="sm" icon={<RefreshCw className="size-4" />} disabled={!device.online} loading={busy === "refresh"} onClick={refresh}>
              {t("security.device.refresh")}
            </Button>
          )
        }
      >
        {!sm ? (
          <p className="text-sm text-muted">{t("security.device.neverHint")}</p>
        ) : (
          <dl className="grid grid-cols-2 gap-x-6 gap-y-4 text-sm sm:grid-cols-3 lg:grid-cols-6">
            <div>
              <dt className="text-xs text-muted">{t("security.col.encryption")}</dt>
              <dd className="mt-1">
                <EncryptionBadge state={sm.encryption} />
              </dd>
            </div>
            <div>
              <dt className="text-xs text-muted">{t("security.col.antivirus")}</dt>
              <dd className="mt-1">
                <AntivirusBadge state={sm.antivirus} platform={device.platform} />
              </dd>
            </div>
            <div>
              <dt className="text-xs text-muted">{t("security.col.firewall")}</dt>
              <dd className="mt-1">
                <FirewallBadge state={sm.firewall} />
              </dd>
            </div>
            <div>
              <dt className="text-xs text-muted">{t("security.col.threats")}</dt>
              <dd className="mt-1">{sm.threats > 0 ? <Badge tone="danger">{sm.threats}</Badge> : <Badge tone="success">0</Badge>}</dd>
            </div>
            <div>
              <dt className="text-xs text-muted">{t("security.col.secureBoot")}</dt>
              <dd className="mt-1">
                <YesNo value={sm.secureBoot} />
              </dd>
            </div>
            <div>
              <dt className="text-xs text-muted">TPM</dt>
              <dd className="mt-1">
                {rep?.tpm ? (
                  rep.tpm.present ? (
                    <Badge tone={rep.tpm.ready ? "success" : "warning"}>
                      {rep.tpm.version ? `${rep.tpm.version} · ` : ""}
                      {rep.tpm.ready ? t("security.device.tpmReady") : t("security.device.tpmNotReady")}
                    </Badge>
                  ) : (
                    <Badge tone="warning">{t("security.device.tpmNone")}</Badge>
                  )
                ) : (
                  <span className="text-muted">–</span>
                )}
              </dd>
            </div>
          </dl>
        )}
        {rep?.errors && rep.errors.length > 0 && (
          <div className="mt-4">
            <Alert tone="warning" title={t("security.device.partial")}>
              <ul className="space-y-0.5 font-mono text-xs break-all">
                {rep.errors.map((e, i) => (
                  <li key={i}>{e}</li>
                ))}
              </ul>
            </Alert>
          </div>
        )}
      </Card>

      {rep && (
        <>
          <Card title={t("security.device.encryption")} description={windows ? t("security.device.encryptionHintWin") : t("security.device.encryptionHintLinux")}>
            {rep.volumes.length === 0 ? (
              <p className="text-sm text-muted">{t("security.device.noVolumes")}</p>
            ) : (
              <div className="-mx-5 -my-5 overflow-x-auto">
                <table className="w-full min-w-[640px] text-sm">
                  <thead className="border-b border-line text-left text-xs text-muted">
                    <tr>
                      <th className="px-5 py-2.5 font-medium">{t("security.device.volume")}</th>
                      <th className="px-3 py-2.5 font-medium">{t("security.device.method")}</th>
                      <th className="px-3 py-2.5 font-medium">{t("security.device.status")}</th>
                      <th className="px-3 py-2.5 font-medium">{t("security.device.protection")}</th>
                      <th className="px-5 py-2.5 font-medium">{t("security.device.protectors")}</th>
                    </tr>
                  </thead>
                  <tbody className="divide-y divide-line">
                    {rep.volumes.map((v) => (
                      <tr key={v.mount}>
                        <td className="px-5 py-2.5 font-medium">
                          {v.mount} {v.system && <Badge tone="primary">{t("security.device.systemVolume")}</Badge>}
                        </td>
                        <td className="px-3 py-2.5">
                          {v.method ? (v.method === "bitlocker" ? "BitLocker" : "LUKS") : <span className="text-muted">{t("security.device.notEncrypted")}</span>}
                          {v.cipher && <div className="text-xs text-muted">{v.cipher}</div>}
                        </td>
                        <td className="px-3 py-2.5">
                          {t(`security.volume.${v.status}`)}
                          {(v.status === "encrypting" || v.status === "decrypting" || v.status === "paused") && ` (${v.percent}%)`}
                        </td>
                        <td className="px-3 py-2.5">
                          <Badge tone={v.protection === "on" ? "success" : v.protection === "suspended" ? "warning" : v.method ? "warning" : "neutral"}>
                            {t(`security.protection.${v.protection}`)}
                          </Badge>
                        </td>
                        <td className="px-5 py-2.5 text-xs text-muted">{v.protectors?.join(", ") || "–"}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </Card>

          {windows && data.canKeys && (
            <Card title={t("security.keys.deviceTitle")} description={t("security.keys.deviceHint")}>
              {data.keys.length === 0 ? (
                <p className="text-sm text-muted">{t("security.keys.deviceNone")}</p>
              ) : (
                <ul className="-mx-5 -my-5 divide-y divide-line">
                  {data.keys.map((k) => (
                    <li key={k.id} className="flex flex-wrap items-center gap-3 px-5 py-3 text-sm">
                      <span className="w-10 font-medium">{k.volume}</span>
                      <span className="min-w-0 flex-1 font-mono text-xs break-all">{k.keyId}</span>
                      {k.removedAt ? (
                        <Badge tone="warning">{t("security.keys.replaced", { when: formatDate(k.removedAt, i18n.language) })}</Badge>
                      ) : (
                        <Badge tone="success">{t("security.keys.inUse")}</Badge>
                      )}
                      <Button size="sm" variant="secondary" icon={<Eye className="size-3.5" />} onClick={() => setReveal(k)}>
                        {t("security.keys.show")}
                      </Button>
                      {manage && !k.removedAt && (
                        <Button
                          size="sm"
                          variant="ghost"
                          icon={<RotateCcw className="size-3.5" />}
                          disabled={open("bitlocker.rotate")}
                          onClick={() => setRotate(k.volume)}
                        >
                          {t("security.keys.rotate")}
                        </Button>
                      )}
                    </li>
                  ))}
                </ul>
              )}
            </Card>
          )}

          <div className="grid gap-6 lg:grid-cols-2">
            <Card
              title={t("security.device.antivirus")}
              actions={
                manage &&
                windows &&
                def && (
                  <>
                    <Button size="sm" variant="secondary" icon={<ScanSearch className="size-4" />} loading={busy === "defender.quickscan"} disabled={open("defender.quickscan")} onClick={() => run("defender.quickscan")}>
                      {t("security.actions.defender.quickscan")}
                    </Button>
                    <Button size="sm" variant="secondary" icon={<ShieldCheck className="size-4" />} loading={busy === "defender.fullscan"} disabled={open("defender.fullscan")} onClick={() => run("defender.fullscan")}>
                      {t("security.actions.defender.fullscan")}
                    </Button>
                    <Button size="sm" variant="secondary" icon={<DownloadCloud className="size-4" />} loading={busy === "defender.signatures"} disabled={open("defender.signatures")} onClick={() => run("defender.signatures")}>
                      {t("security.actions.defender.signatures")}
                    </Button>
                  </>
                )
              }
            >
              {rep.antivirus.length === 0 ? (
                <p className="text-sm text-muted">{t("security.device.noAntivirus")}</p>
              ) : (
                <ul className="space-y-2 text-sm">
                  {rep.antivirus.map((a) => (
                    <li key={a.name} className="flex flex-wrap items-center gap-2">
                      <span className="font-medium">{a.name}</span>
                      <Badge tone={a.enabled ? "success" : "danger"}>{a.enabled ? t("security.device.on") : t("security.device.off")}</Badge>
                      {a.enabled && <Badge tone={a.upToDate ? "success" : "warning"}>{a.upToDate ? t("security.device.upToDate") : t("security.device.outdated")}</Badge>}
                    </li>
                  ))}
                </ul>
              )}
              {def && (
                <dl className="mt-5 grid grid-cols-1 gap-x-6 gap-y-3 border-t border-line pt-4 text-sm sm:grid-cols-2">
                  <div>
                    <dt className="text-xs text-muted">{t("security.device.defenderMode")}</dt>
                    <dd>{def.mode}</dd>
                  </div>
                  <div>
                    <dt className="text-xs text-muted">{t("security.device.realTime")}</dt>
                    <dd>
                      <YesNo value={def.realTime} />
                    </dd>
                  </div>
                  <div>
                    <dt className="text-xs text-muted">{t("security.device.tamper")}</dt>
                    <dd>
                      <YesNo value={def.tamperProtected} />
                    </dd>
                  </div>
                  <div>
                    <dt className="text-xs text-muted">{t("security.device.signatures")}</dt>
                    <dd>
                      {def.signatureVersion || "–"}
                      {def.signatureUpdatedAt && <span className="text-xs text-muted"> · {timeAgo(def.signatureUpdatedAt, i18n.language)}</span>}
                    </dd>
                  </div>
                  <div>
                    <dt className="text-xs text-muted">{t("security.device.quickScan")}</dt>
                    <dd>{def.quickScanAt ? formatDate(def.quickScanAt, i18n.language) : t("security.device.neverScanned")}</dd>
                  </div>
                  <div>
                    <dt className="text-xs text-muted">{t("security.device.fullScan")}</dt>
                    <dd>{def.fullScanAt ? formatDate(def.fullScanAt, i18n.language) : t("security.device.neverScanned")}</dd>
                  </div>
                </dl>
              )}
            </Card>

            <Card title={t("security.device.firewallTitle")}>
              {rep.firewall.length === 0 ? (
                <p className="text-sm text-muted">–</p>
              ) : (
                <ul className="-mx-5 -my-5 divide-y divide-line">
                  {rep.firewall.map((f) => (
                    <li key={f.name} className="flex flex-wrap items-center gap-2 px-5 py-3 text-sm">
                      <span className="flex-1 font-medium">
                        {f.name === "none" ? t("security.device.noFirewall") : f.name}
                        {f.active && (
                          <span className="ml-2">
                            <Badge tone="primary">{t("security.device.activeProfile")}</Badge>
                          </span>
                        )}
                      </span>
                      {f.inbound && <span className="text-xs text-muted">{t(`security.device.inbound.${f.inbound}`)}</span>}
                      <Badge tone={f.enabled ? "success" : "danger"}>{f.enabled ? t("security.device.on") : t("security.device.off")}</Badge>
                    </li>
                  ))}
                </ul>
              )}
            </Card>
          </div>

          {threats.length > 0 && (
            <Card title={t("security.device.threats")} description={t("security.device.threatsHint")}>
              <div className="-mx-5 -my-5 overflow-x-auto">
                <table className="w-full min-w-[640px] text-sm">
                  <thead className="border-b border-line text-left text-xs text-muted">
                    <tr>
                      <th className="px-5 py-2.5 font-medium">{t("security.device.threat")}</th>
                      <th className="px-3 py-2.5 font-medium">{t("security.device.severity")}</th>
                      <th className="px-3 py-2.5 font-medium">{t("security.device.detected")}</th>
                      <th className="px-5 py-2.5 font-medium">{t("security.device.state")}</th>
                    </tr>
                  </thead>
                  <tbody className="divide-y divide-line">
                    {threats.map((th, i) => (
                      <tr key={i}>
                        <td className="px-5 py-2.5">
                          <div className="font-medium break-all">{th.name}</div>
                          {th.resources?.map((r) => (
                            <div key={r} className="font-mono text-xs break-all text-muted">
                              {r}
                            </div>
                          ))}
                        </td>
                        <td className="px-3 py-2.5">
                          <Badge tone={th.severity === "Severe" || th.severity === "High" ? "danger" : th.severity === "Moderate" ? "warning" : "neutral"}>{th.severity}</Badge>
                        </td>
                        <td className="px-3 py-2.5 text-xs">{formatDate(th.detectedAt, i18n.language)}</td>
                        <td className="px-5 py-2.5">
                          <Badge tone={th.active ? "danger" : "success"}>{th.active ? t("security.device.active") : th.action || t("security.device.resolved")}</Badge>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </Card>
          )}

          <Card title={t("security.device.admins")} description={windows ? t("security.device.adminsHintWin") : t("security.device.adminsHintLinux")}>
            {rep.admins.length === 0 ? (
              <p className="text-sm text-muted">–</p>
            ) : (
              <ul className="flex flex-wrap gap-2">
                {rep.admins.map((a) => (
                  <li key={a.name} title={a.sid} className="inline-flex items-center gap-1.5 rounded-md border border-line px-2.5 py-1 text-sm">
                    <span className="font-medium">{a.name}</span>
                    {a.kind === "group" && <Badge>{t("security.device.group")}</Badge>}
                    {a.kind === "unknown" && <Badge tone="warning">{t("security.device.unknownAccount")}</Badge>}
                    {a.builtin && <Badge tone="primary">{t("security.device.builtin")}</Badge>}
                  </li>
                ))}
              </ul>
            )}
          </Card>
        </>
      )}

      {data.actions.length > 0 && (
        <Card title={t("security.device.actionsTitle")}>
          <ul className="-mx-5 -my-5 divide-y divide-line">
            {data.actions.map((a) => (
              <li key={a.id} className="flex flex-wrap items-center gap-3 px-5 py-3 text-sm">
                <span className="flex-1 font-medium">
                  {t(`security.actions.${a.action}`)}
                  {a.mount && ` ${a.mount}`}
                </span>
                {a.message && <span className="text-xs break-all text-muted">{a.message}</span>}
                <span className="text-xs text-muted">
                  {formatDate(a.createdAt, i18n.language)} · {a.requestedBy}
                </span>
                <Badge tone={stateTone[a.state]}>{t(`security.actionState.${a.state}`)}</Badge>
              </li>
            ))}
          </ul>
        </Card>
      )}

      {reveal && (
        <RevealDialog
          recoveryKey={reveal}
          onClose={() => {
            setReveal(null);
            load();
          }}
          onRotate={
            manage
              ? () => {
                  setRotate(reveal.volume);
                  setReveal(null);
                }
              : undefined
          }
        />
      )}
      {rotate && (
        <RotateDialog
          deviceId={device.id}
          volume={rotate}
          onClose={() => setRotate(null)}
          onDone={() => {
            setNotice(t("security.keys.rotateStarted", { host: device.hostname }));
            load();
          }}
        />
      )}
    </div>
  );
}
