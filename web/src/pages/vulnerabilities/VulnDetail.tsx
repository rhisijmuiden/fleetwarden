import { useCallback, useEffect, useState } from "react";
import { Link, useParams } from "react-router";
import { useTranslation } from "react-i18next";
import {
  ArrowLeft,
  ExternalLink,
  Rocket,
  ShieldOff,
  Trash2,
} from "lucide-react";
import { del, get, post, type VulnDetail as Detail } from "../../api";
import { useAuth } from "../../auth";
import {
  Alert,
  Button,
  Card,
  Field,
  Input,
  Modal,
  Select,
  Spinner,
  formatDate,
  useErrorText,
} from "../../components/ui";
import { PlatformBadge, StatusDot } from "../devices/util";
import {
  FixBadge,
  FixDialog,
  KEVBadge,
  SeverityBadge,
  kindLabel,
} from "./util";

function rangeText(r: Detail["ranges"][number], t: (k: string) => string) {
  if (r.version) return r.version;
  const parts: string[] = [];
  if (r.startIncl) parts.push(`≥ ${r.startIncl}`);
  if (r.startExcl) parts.push(`> ${r.startExcl}`);
  if (r.endIncl) parts.push(`≤ ${r.endIncl}`);
  if (r.endExcl) parts.push(`< ${r.endExcl}`);
  return parts.join(" ") || t("vulns.allVersions");
}

export function VulnDetail() {
  const { cve = "" } = useParams();
  const { t, i18n } = useTranslation();
  const { can } = useAuth();
  const errorText = useErrorText();
  const [d, setD] = useState<Detail | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [fixing, setFixing] = useState<string[] | null>(null);
  const [accepting, setAccepting] = useState(false);
  const load = useCallback(() => {
    get<Detail>(`/vulnerabilities/cve/${encodeURIComponent(cve)}`)
      .then(setD)
      .catch((e) => setError(errorText(e)));
  }, [cve]);
  useEffect(load, [load]);
  if (error)
    return (
      <div className="space-y-4">
        <BackLink />
        <Alert tone="danger">{error}</Alert>
      </div>
    );
  if (!d) return <Spinner />;
  const open = d.devices.filter((x) => !x.accepted);
  const fixable = open.filter((x) => x.fix === "patch" || x.fix === "software");
  const removeException = (id: number) =>
    del(`/vulnerabilities/exceptions/${id}`)
      .then(load)
      .catch((e) => setError(errorText(e)));
  return (
    <div className="space-y-6">
      <BackLink />
      <div className="flex flex-wrap items-start justify-between gap-4">
        <div className="min-w-0">
          <div className="flex flex-wrap items-center gap-3">
            <h1 className="font-mono text-2xl font-semibold tracking-tight">
              {d.cve}
            </h1>
            <SeverityBadge severity={d.severity} score={d.score} />
            {d.kev && <KEVBadge ransomware={d.ransomware} />}
          </div>
          {d.title && <p className="mt-1 text-sm font-medium">{d.title}</p>}
        </div>
        <div className="flex flex-wrap gap-2">
          {can("vulns.manage") && (
            <Button
              variant="secondary"
              icon={<ShieldOff className="size-4" />}
              onClick={() => setAccepting(true)}
            >
              {t("vulns.accept")}
            </Button>
          )}
          {can("devices.manage") && (
            <Button
              icon={<Rocket className="size-4" />}
              disabled={fixable.length === 0}
              onClick={() => setFixing([d.cve])}
            >
              {t("vulns.fixOn", { count: deviceCount(fixable) })}
            </Button>
          )}
        </div>
      </div>
      {d.kev && (
        <Alert tone="danger" title={t("vulns.kevTitle")}>
          {t("vulns.kevText", {
            name: d.kevName,
            added: formatDay(d.kevAdded, i18n.language),
            due: formatDay(d.kevDue, i18n.language),
          })}
          {d.ransomware && ` ${t("vulns.ransomwareText")}`}
        </Alert>
      )}
      {!d.kev && d.exploited && (
        <Alert tone="warning">{t("vulns.exploitedText")}</Alert>
      )}
      <Card title={t("vulns.about")}>
        <div className="space-y-4 text-sm">
          <p className="leading-relaxed">
            {d.summary || t("vulns.noDescription")}
          </p>
          <dl className="grid gap-x-6 gap-y-3 sm:grid-cols-2 lg:grid-cols-4">
            <div>
              <dt className="text-xs text-muted">{t("vulns.score")}</dt>
              <dd className="font-medium tabular-nums">
                {d.score ? d.score.toFixed(1) : "–"}
              </dd>
            </div>
            <div>
              <dt className="text-xs text-muted">{t("vulns.published")}</dt>
              <dd>{formatDate(d.published, i18n.language)}</dd>
            </div>
            <div>
              <dt className="text-xs text-muted">{t("vulns.msrcRating")}</dt>
              <dd>
                {d.msrcSeverity ? t(`vulns.severity.${d.msrcSeverity}`) : "–"}
              </dd>
            </div>
            <div>
              <dt className="text-xs text-muted">{t("vulns.updates")}</dt>
              <dd className="break-words">
                {d.updates.length
                  ? d.updates.map((k) => `KB${k}`).join(", ")
                  : "–"}
              </dd>
            </div>
          </dl>
          {d.vector && (
            <p className="font-mono text-xs break-all text-muted">{d.vector}</p>
          )}
          {d.ranges.length > 0 && (
            <div>
              <p className="text-xs text-muted">
                {t("vulns.affectedVersions")}
              </p>
              <ul className="mt-1 space-y-0.5 text-xs">
                {d.ranges.map((r, i) => (
                  <li key={i}>
                    <span className="font-mono">{r.product}</span>{" "}
                    {rangeText(r, t)}
                  </li>
                ))}
              </ul>
            </div>
          )}
          <a
            href={d.nvdUrl}
            target="_blank"
            rel="noreferrer noopener"
            className="inline-flex items-center gap-1 text-primary hover:underline"
          >
            {t("vulns.nvdLink")} <ExternalLink className="size-3.5" />
          </a>
        </div>
      </Card>
      <Card
        title={t("vulns.affectedTitle", { count: deviceCount(open) })}
        description={t("vulns.affectedHint")}
      >
        {d.devices.length === 0 ? (
          <p className="rounded-md border border-dashed border-line px-4 py-6 text-center text-sm text-muted">
            {t("vulns.noAffected")}
          </p>
        ) : (
          <div className="-mx-5 -mb-5 overflow-x-auto">
            <table className="w-full min-w-[820px] text-sm">
              <thead className="border-y border-line text-left text-xs text-muted uppercase">
                <tr>
                  <th className="px-5 py-3 font-medium">
                    {t("vulns.deviceCol")}
                  </th>
                  <th className="px-3 py-3 font-medium">
                    {t("vulns.productCol")}
                  </th>
                  <th className="px-3 py-3 font-medium">
                    {t("vulns.installed")}
                  </th>
                  <th className="px-3 py-3 font-medium">
                    {t("vulns.fixedIn")}
                  </th>
                  <th className="px-5 py-3 font-medium">{t("vulns.fixCol")}</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-line">
                {d.devices.map((x) => (
                  <tr
                    key={x.deviceId + x.product}
                    className={`align-top hover:bg-surface-muted/50 ${x.accepted ? "opacity-60" : ""}`}
                  >
                    <td className="px-5 py-2.5">
                      <Link
                        to={`/devices/${x.deviceId}?tab=vulnerabilities`}
                        className="font-medium hover:text-primary"
                      >
                        {x.hostname}
                      </Link>
                      <div className="mt-0.5 flex flex-wrap items-center gap-2 text-xs text-muted">
                        <StatusDot online={x.online} />
                        <PlatformBadge platform={x.platform} />
                        {x.groupName}
                      </div>
                    </td>
                    <td className="px-3 py-2.5">
                      {x.product}
                      <div className="text-xs text-muted">
                        {kindLabel(x.kind, t)}
                      </div>
                    </td>
                    <td className="px-3 py-2.5 font-mono text-xs">
                      {x.installedVersion || "–"}
                    </td>
                    <td className="px-3 py-2.5 font-mono text-xs">
                      {x.fixedVersion || "–"}
                    </td>
                    <td className="px-5 py-2.5">
                      {x.accepted ? (
                        <span className="text-xs text-muted">
                          {t("vulns.acceptedShort")}
                        </span>
                      ) : (
                        <FixBadge fix={x.fix} />
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Card>
      {d.exceptions.length > 0 && (
        <Card title={t("vulns.acceptedTitle")}>
          <ul className="divide-y divide-line text-sm">
            {d.exceptions.map((x) => (
              <li
                key={x.id}
                className="flex items-start justify-between gap-3 py-2"
              >
                <div>
                  <p className="font-medium">
                    {x.deviceId ? x.hostname : t("vulns.allDevices")}
                  </p>
                  <p className="text-muted">{x.reason}</p>
                  <p className="text-xs text-muted">
                    {t("vulns.acceptedBy", {
                      user: x.createdBy,
                      when: formatDate(x.createdAt, i18n.language),
                    })}
                    {x.expiresAt &&
                      ` · ${t("vulns.until", { date: formatDate(x.expiresAt, i18n.language) })}`}
                  </p>
                </div>
                {can("vulns.manage") && (
                  <Button
                    variant="ghost"
                    size="sm"
                    icon={<Trash2 className="size-4" />}
                    onClick={() => removeException(x.id)}
                  >
                    {t("vulns.removeAccept")}
                  </Button>
                )}
              </li>
            ))}
          </ul>
        </Card>
      )}
      <FixDialog
        open={fixing !== null}
        cves={fixing ?? []}
        onClose={() => setFixing(null)}
        onDone={load}
      />
      <AcceptDialog
        open={accepting}
        cve={d.cve}
        devices={open.map((x) => ({ id: x.deviceId, name: x.hostname }))}
        onClose={() => setAccepting(false)}
        onDone={() => {
          setAccepting(false);
          load();
        }}
      />
    </div>
  );
}

function BackLink() {
  const { t } = useTranslation();
  return (
    <Link
      to="/vulnerabilities"
      className="inline-flex items-center gap-1 text-sm text-muted hover:text-fg"
    >
      <ArrowLeft className="size-4" /> {t("vulns.back")}
    </Link>
  );
}

export function AcceptDialog({
  open,
  cve,
  devices,
  fixedDevice,
  onClose,
  onDone,
}: {
  open: boolean;
  cve: string;
  devices: { id: string; name: string }[];
  fixedDevice?: string;
  onClose: () => void;
  onDone: () => void;
}) {
  const { t } = useTranslation();
  const { me } = useAuth();
  const errorText = useErrorText();
  const unique = devices.filter(
    (d, i) => devices.findIndex((x) => x.id === d.id) === i,
  );
  const [device, setDevice] = useState(fixedDevice ?? "");
  const [reason, setReason] = useState("");
  const [expires, setExpires] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const scoped = !!me?.scoped;
  useEffect(() => {
    if (open) {
      setDevice(fixedDevice ?? (scoped && unique[0] ? unique[0].id : ""));
      setReason("");
      setExpires("");
      setError(null);
    }
  }, [open]);
  const save = () => {
    setBusy(true);
    setError(null);
    post("/vulnerabilities/exceptions", {
      cve,
      deviceId: device,
      reason,
      expiresAt: expires || null,
    })
      .then(onDone)
      .catch((e) => setError(errorText(e)))
      .finally(() => setBusy(false));
  };
  return (
    <Modal
      open={open}
      onClose={onClose}
      title={t("vulns.acceptTitle", { cve })}
      footer={
        <>
          <Button variant="secondary" onClick={onClose}>
            {t("common.cancel")}
          </Button>
          <Button loading={busy} disabled={!reason.trim()} onClick={save}>
            {t("vulns.accept")}
          </Button>
        </>
      }
    >
      {error && <Alert tone="danger">{error}</Alert>}
      <p className="text-sm text-muted">{t("vulns.acceptIntro")}</p>
      {!fixedDevice && (
        <Field label={t("vulns.appliesTo")}>
          {(id) => (
            <Select
              id={id}
              value={device}
              onChange={(e) => setDevice(e.target.value)}
            >
              {!scoped && <option value="">{t("vulns.allDevices")}</option>}
              {unique.map((d) => (
                <option key={d.id} value={d.id}>
                  {d.name}
                </option>
              ))}
            </Select>
          )}
        </Field>
      )}
      <Field label={t("vulns.reason")} hint={t("vulns.reasonHint")}>
        {(id) => (
          <Input
            id={id}
            value={reason}
            onChange={(e) => setReason(e.target.value)}
            maxLength={1000}
          />
        )}
      </Field>
      <Field label={t("vulns.expires")} hint={t("vulns.expiresHint")}>
        {(id) => (
          <Input
            id={id}
            type="date"
            value={expires}
            onChange={(e) => setExpires(e.target.value)}
            className="w-48"
          />
        )}
      </Field>
    </Modal>
  );
}

// A device can have the same CVE in more than one product (Chrome and Edge).
function deviceCount(rows: { deviceId: string }[]): number {
  return new Set(rows.map((x) => x.deviceId)).size;
}

// KEV dates are calendar days without a time.
function formatDay(iso: string | null | undefined, lang: string): string {
  if (!iso) return "–";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "–";
  return d.toLocaleDateString(lang === "nl" ? "nl-NL" : "en-GB", {
    dateStyle: "medium",
    timeZone: "UTC",
  });
}
