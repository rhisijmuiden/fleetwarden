import { useEffect, useMemo, useState, type FormEvent, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Check, CheckCircle2, CircleSlash, Copy, Download, FlaskConical, ShieldPlus, TriangleAlert, XCircle } from "lucide-react";
import { ApiError, get, post, put } from "../../api";
import { Alert, Badge, Button, Card, CopyField, Field, Input, PageHeader, Select, Spinner, Switch, useErrorText } from "../../components/ui";
import { configFileName, exampleConfig, type ProxyKind, type TLSMode } from "./proxyConfigs";

type RequestView = {
  peer: string;
  peerTrusted: boolean;
  clientIp: string;
  scheme: string;
  host: string;
  forwardedFor: string;
  forwardedProto: string;
  forwardedHost: string;
  nonce?: string;
};

type ProxyView = {
  trustedProxies: string[];
  hsts: boolean;
  kind: ProxyKind;
  upstream: string;
  tlsMode: TLSMode;
  publicUrl: string;
  detectedUrl: string;
  envTrustedProxies: string[];
  listen: string;
  listenLocalOnly: boolean;
  serverAddresses: string[];
  envFile: string;
  request: RequestView;
  maxUploadMiB: number;
};

type CheckStatus = "pass" | "warn" | "fail" | "skip";
type Check = { id: string; status: CheckStatus; detail?: string; side: "server" | "browser" };
type TestResult = { publicUrl: string; nonce: string; checks: Omit<Check, "side">[] };

const kinds: { id: ProxyKind; label: string }[] = [
  { id: "zoraxy", label: "Zoraxy" },
  { id: "nginx", label: "nginx" },
  { id: "caddy", label: "Caddy" },
  { id: "haproxy", label: "HAProxy" },
];

const isLoopback = (ip: string) => ip === "::1" || ip.startsWith("127.");
const listenPort = (listen: string) => listen.slice(listen.lastIndexOf(":") + 1) || "8080";

function defaultUpstream(v: ProxyView) {
  const port = listenPort(v.listen);
  if (v.listenLocalOnly) return `127.0.0.1:${port}`;
  const ip = v.serverAddresses.find((a) => !a.includes(":")) ?? v.serverAddresses[0];
  return ip ? `${ip.includes(":") ? `[${ip}]` : ip}:${port}` : `127.0.0.1:${port}`;
}

function hostOf(url: string) {
  try {
    return new URL(url).hostname;
  } catch {
    return "";
  }
}

export function ReverseProxy() {
  const { t } = useTranslation();
  const errorText = useErrorText();
  const [v, setV] = useState<ProxyView | null>(null);
  const [trustedText, setTrustedText] = useState("");
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<{ tone: "success" | "danger"; text: string } | null>(null);

  const load = (next: ProxyView) => {
    setV({ ...next, upstream: next.upstream || defaultUpstream(next) });
    setTrustedText(next.trustedProxies.join("\n"));
  };
  useEffect(() => {
    get<ProxyView>("/settings/proxy")
      .then(load)
      .catch((e) => setMsg({ tone: "danger", text: errorText(e) }));
  }, []);

  const save = async (e?: FormEvent) => {
    e?.preventDefault();
    if (!v) return;
    setBusy(true);
    setMsg(null);
    try {
      const trustedProxies = trustedText.split(/[\s,]+/).filter(Boolean);
      load(
        await put<ProxyView>("/settings/proxy", {
          trustedProxies,
          hsts: v.hsts,
          kind: v.kind,
          upstream: v.upstream,
          tlsMode: v.tlsMode,
          publicUrl: v.publicUrl,
        }),
      );
      setMsg({ tone: "success", text: t("common.saved") });
    } catch (err) {
      setMsg({ tone: "danger", text: errorText(err) });
    } finally {
      setBusy(false);
    }
  };

  if (!v) {
    return (
      <div className="space-y-6">
        <PageHeader title={t("proxy.title")} description={t("proxy.subtitle")} />
        {msg ? <Alert tone={msg.tone}>{msg.text}</Alert> : <Spinner />}
      </div>
    );
  }

  const rq = v.request;
  const viaProxy = !isLoopback(rq.peer) || rq.forwardedFor !== "";
  const untrustedProxy = !rq.peerTrusted && rq.forwardedFor !== "";
  const listedPeer = trustedText.split(/[\s,]+/).includes(rq.peer);
  const trustPeer = () => setTrustedText((cur) => (cur.trim() ? `${cur.trim()}\n${rq.peer}` : rq.peer));
  const domain = hostOf(v.publicUrl) || hostOf(v.detectedUrl) || "fleet.example.com";

  return (
    <div className="space-y-6">
      <PageHeader title={t("proxy.title")} description={t("proxy.subtitle")} />

      <Card title={t("proxy.statusTitle")} description={t("proxy.statusSubtitle")}>
        <div className="space-y-4">
          <dl className="grid gap-x-6 gap-y-3 text-sm sm:grid-cols-2 lg:grid-cols-3">
            <Info label={t("proxy.peer")}>
              <span className="font-mono">{rq.peer}</span>{" "}
              {rq.peerTrusted ? (
                <Badge tone="success">{t("proxy.trusted")}</Badge>
              ) : (
                <Badge tone={viaProxy ? "warning" : "neutral"}>{t("proxy.notTrusted")}</Badge>
              )}
            </Info>
            <Info label={t("proxy.clientIp")}>
              <span className="font-mono">{rq.clientIp}</span>
            </Info>
            <Info label={t("proxy.scheme")}>
              <Badge tone={rq.scheme === "https" ? "success" : "warning"}>{rq.scheme}</Badge>
            </Info>
            <Info label={t("proxy.host")}>
              <span className="font-mono break-all">{rq.host}</span>
            </Info>
            <Info label="X-Forwarded-For">
              <span className="font-mono break-all">{rq.forwardedFor || "–"}</span>
            </Info>
            <Info label="X-Forwarded-Proto">
              <span className="font-mono">{rq.forwardedProto || "–"}</span>
            </Info>
            <Info label={t("proxy.listen")}>
              <span className="font-mono">{v.listen}</span>
            </Info>
            <Info label={t("proxy.serverAddresses")}>
              <span className="font-mono break-all">{v.serverAddresses.join(", ") || "–"}</span>
            </Info>
          </dl>
          {untrustedProxy && (
            <Alert tone="warning" title={t("proxy.untrustedTitle", { peer: rq.peer })}>
              <p>{t("proxy.untrustedText")}</p>
              {!listedPeer && (
                <Button size="sm" variant="secondary" className="mt-2" icon={<ShieldPlus className="size-4" />} onClick={trustPeer}>
                  {t("proxy.trustPeer", { peer: rq.peer })}
                </Button>
              )}
            </Alert>
          )}
          {v.listenLocalOnly ? (
            <Alert tone="info" title={t("proxy.localOnlyTitle", { listen: v.listen })}>
              <p>{t("proxy.localOnlyText", { file: v.envFile })}</p>
              <div className="mt-2">
                <CopyField
                  value={`sudo sed -i 's/^FW_LISTEN=.*/FW_LISTEN=0.0.0.0:${listenPort(v.listen)}/' ${v.envFile} && sudo systemctl restart fleetwarden-server`}
                />
              </div>
            </Alert>
          ) : (
            <Alert tone="info" title={t("proxy.openTitle", { listen: v.listen })}>
              <p>{t("proxy.openText")}</p>
            </Alert>
          )}
        </div>
      </Card>

      <Card title={t("proxy.settingsTitle")} description={t("proxy.settingsSubtitle")}>
        <form onSubmit={save} className="max-w-2xl space-y-5">
          {msg && <Alert tone={msg.tone}>{msg.text}</Alert>}
          <Field label={t("proxy.publicUrl")} hint={t("proxy.publicUrlHint", { url: v.detectedUrl })}>
            {(id) => (
              <Input id={id} type="url" value={v.publicUrl} placeholder={`https://${domain}`} onChange={(e) => setV({ ...v, publicUrl: e.target.value })} />
            )}
          </Field>
          <Field label={t("proxy.trustedProxies")} hint={t("proxy.trustedHint", { env: v.envTrustedProxies.join(", ") || "–", file: v.envFile })}>
            {(id) => (
              <textarea
                id={id}
                rows={3}
                value={trustedText}
                onChange={(e) => setTrustedText(e.target.value)}
                placeholder={"192.168.1.10\n10.0.0.0/24"}
                className="w-full rounded-md border border-line bg-surface px-3 py-2 font-mono text-xs text-fg outline-none focus-visible:ring-2 focus-visible:ring-primary"
              />
            )}
          </Field>
          <Switch checked={v.hsts} onChange={(hsts) => setV({ ...v, hsts })} label={t("proxy.hsts")} description={t("proxy.hstsHint")} />
          <Button type="submit" loading={busy}>
            {t("common.save")}
          </Button>
        </form>
      </Card>

      <Examples v={v} setV={setV} domain={domain} onSave={() => void save()} busy={busy} />

      <ProxyTest publicUrl={v.publicUrl} />
    </div>
  );
}

function Info({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="min-w-0">
      <dt className="text-xs text-muted">{label}</dt>
      <dd className="mt-0.5">{children}</dd>
    </div>
  );
}

function CodeBlock({ code, fileName }: { code: string; fileName: string }) {
  const { t } = useTranslation();
  const [copied, setCopied] = useState(false);
  const download = () => {
    const url = URL.createObjectURL(new Blob([code], { type: "text/plain" }));
    const a = document.createElement("a");
    a.href = url;
    a.download = fileName;
    a.click();
    URL.revokeObjectURL(url);
  };
  return (
    <div className="overflow-hidden rounded-md border border-line">
      <div className="flex items-center justify-between gap-2 border-b border-line bg-surface-muted px-3 py-1.5">
        <span className="font-mono text-xs text-muted">{fileName}</span>
        <div className="flex gap-1">
          <Button
            size="sm"
            variant="ghost"
            icon={copied ? <Check className="size-4 text-success" /> : <Copy className="size-4" />}
            onClick={() =>
              void navigator.clipboard?.writeText(code).then(() => {
                setCopied(true);
                setTimeout(() => setCopied(false), 1500);
              })
            }
          >
            {copied ? t("proxy.copied") : t("common.copy")}
          </Button>
          <Button size="sm" variant="ghost" icon={<Download className="size-4" />} onClick={download}>
            {t("proxy.download")}
          </Button>
        </div>
      </div>
      <pre className="max-h-[32rem] overflow-auto bg-surface p-3 font-mono text-xs leading-relaxed">{code}</pre>
    </div>
  );
}

function Examples({ v, setV, domain, onSave, busy }: { v: ProxyView; setV: (v: ProxyView) => void; domain: string; onSave: () => void; busy: boolean }) {
  const { t } = useTranslation();
  const input = { domain, upstream: v.upstream || defaultUpstream(v), tlsMode: v.tlsMode, maxUploadMiB: v.maxUploadMiB };
  const code = useMemo(() => exampleConfig(v.kind, input), [v.kind, input.domain, input.upstream, input.tlsMode, input.maxUploadMiB]);
  const gib = v.maxUploadMiB / 1024;
  const reqs = ["web", "agents", "websocket", "headers", "tls", "uploads", "timeouts"] as const;

  return (
    <Card
      title={t("proxy.examplesTitle")}
      description={t("proxy.examplesSubtitle")}
      actions={
        <Button size="sm" variant="secondary" loading={busy} onClick={onSave}>
          {t("proxy.rememberChoices")}
        </Button>
      }
    >
      <div className="space-y-5">
        <div role="tablist" aria-label={t("proxy.proxyType")} className="inline-flex flex-wrap gap-1 rounded-md border border-line bg-surface-muted p-1">
          {kinds.map((k) => (
            <button
              key={k.id}
              role="tab"
              type="button"
              aria-selected={v.kind === k.id}
              onClick={() => setV({ ...v, kind: k.id })}
              className={`rounded px-3 py-1.5 text-sm font-medium transition-colors ${v.kind === k.id ? "bg-surface text-fg shadow-sm" : "text-muted hover:text-fg"}`}
            >
              {k.label}
            </button>
          ))}
        </div>

        <div className="grid gap-4 sm:grid-cols-3">
          <Field label={t("proxy.domain")} hint={t("proxy.domainHint")}>
            {(id) => <Input id={id} value={domain} readOnly className="font-mono" />}
          </Field>
          <Field label={t("proxy.upstream")} hint={t("proxy.upstreamHint")}>
            {(id) => (
              <>
                <Input id={id} list={`${id}-list`} value={v.upstream} className="font-mono" onChange={(e) => setV({ ...v, upstream: e.target.value })} />
                <datalist id={`${id}-list`}>
                  {[`127.0.0.1:${listenPort(v.listen)}`, ...v.serverAddresses.map((a) => `${a.includes(":") ? `[${a}]` : a}:${listenPort(v.listen)}`)].map(
                    (o) => (
                      <option key={o} value={o} />
                    ),
                  )}
                </datalist>
              </>
            )}
          </Field>
          <Field label={t("proxy.tlsMode")}>
            {(id) => (
              <Select id={id} value={v.tlsMode} onChange={(e) => setV({ ...v, tlsMode: e.target.value as TLSMode })}>
                <option value="acme">{t("proxy.tlsAcme")}</option>
                <option value="own">{t("proxy.tlsOwn")}</option>
              </Select>
            )}
          </Field>
        </div>

        {v.listenLocalOnly && !input.upstream.startsWith("127.") && !input.upstream.startsWith("[::1]") && (
          <Alert tone="warning">{t("proxy.upstreamLocalOnly", { listen: v.listen })}</Alert>
        )}

        {v.kind === "zoraxy" ? (
          <ZoraxySteps domain={domain} upstream={input.upstream} tlsMode={v.tlsMode} />
        ) : (
          <CodeBlock code={code} fileName={configFileName[v.kind]} />
        )}

        <div>
          <h3 className="mb-2 text-sm font-semibold">{t("proxy.reqTitle")}</h3>
          <ul className="grid gap-2 sm:grid-cols-2">
            {reqs.map((r) => (
              <li key={r} className="flex gap-2 rounded-md border border-line px-3 py-2 text-sm">
                <CheckCircle2 className="mt-0.5 size-4 shrink-0 text-primary" />
                <span>
                  <span className="font-medium">{t(`proxy.req.${r}.title`)}</span>
                  <span className="block text-xs text-muted">{t(`proxy.req.${r}.text`, { gib })}</span>
                </span>
              </li>
            ))}
          </ul>
        </div>
      </div>
    </Card>
  );
}

function ZoraxySteps({ domain, upstream, tlsMode }: { domain: string; upstream: string; tlsMode: TLSMode }) {
  const { t } = useTranslation();
  const steps: { title: string; items: ReactNode[] }[] = [
    {
      title: t("proxy.zoraxy.s1"),
      items: [t("proxy.zoraxy.s1a"), t("proxy.zoraxy.s1b"), t("proxy.zoraxy.s1c")],
    },
    {
      title: t("proxy.zoraxy.s2"),
      items: [
        <ValueRow key="d" label={t("proxy.zoraxy.matching")} value={domain} />,
        <ValueRow key="u" label={t("proxy.zoraxy.target")} value={upstream} />,
        t("proxy.zoraxy.s2c"),
      ],
    },
    {
      title: t("proxy.zoraxy.s3"),
      items: [t("proxy.zoraxy.s3a"), <ValueRow key="w" label={t("proxy.zoraxy.wsTimeout")} value="3600" />, t("proxy.zoraxy.s3c"), t("proxy.zoraxy.s3d")],
    },
    {
      title: t(tlsMode === "acme" ? "proxy.zoraxy.s4acme" : "proxy.zoraxy.s4own"),
      items: tlsMode === "acme" ? [t("proxy.zoraxy.s4a", { domain }), t("proxy.zoraxy.s4b")] : [t("proxy.zoraxy.s4ownA", { domain })],
    },
    { title: t("proxy.zoraxy.s5"), items: [t("proxy.zoraxy.s5a"), t("proxy.zoraxy.s5b")] },
  ];
  return (
    <ol className="space-y-3">
      {steps.map((s, i) => (
        <li key={i} className="flex gap-3 rounded-md border border-line p-3">
          <span className="grid size-6 shrink-0 place-items-center rounded-full bg-primary text-xs font-semibold text-primary-contrast">{i + 1}</span>
          <div className="min-w-0 flex-1 space-y-1.5">
            <p className="text-sm font-medium">{s.title}</p>
            <ul className="space-y-1.5 text-sm text-fg/90">
              {s.items.map((it, j) => (
                <li key={j} className="ml-4 list-disc">
                  {it}
                </li>
              ))}
            </ul>
          </div>
        </li>
      ))}
    </ol>
  );
}

function ValueRow({ label, value }: { label: string; value: string }) {
  return (
    <div className="space-y-1">
      <span>{label}</span>
      <div className="max-w-md">
        <CopyField value={value} label={label} />
      </div>
    </div>
  );
}

// ---- end-to-end test ----

const uploadTestBytes = 16 << 20;

async function browserChecks(publicUrl: string, nonce: string): Promise<Check[]> {
  const out: Check[] = [];
  const add = (id: string, status: CheckStatus, detail = "") => out.push({ id, status, detail, side: "browser" });

  add("browser_https", location.protocol === "https:" ? "pass" : "warn", location.protocol.replace(":", ""));
  if (publicUrl) add("browser_origin", location.origin === publicUrl.replace(/\/$/, "") ? "pass" : "warn", location.origin);

  try {
    const pv = await get<RequestView>(`/proxy-probe/${nonce}`);
    if (!pv.forwardedFor && isLoopback(pv.peer)) add("browser_headers", "warn", pv.peer);
    else if (!pv.peerTrusted) add("browser_headers", "fail", pv.peer);
    else if (location.protocol === "https:" && pv.scheme !== "https") add("browser_headers", "fail", `${pv.peer}, X-Forwarded-Proto: ${pv.forwardedProto || "–"}`);
    else add("browser_headers", "pass", `${pv.peer} → ${pv.clientIp}`);
  } catch (e) {
    add("browser_headers", "fail", e instanceof ApiError ? e.code : String(e));
  }

  const wsStatus = await new Promise<[CheckStatus, string]>((resolve) => {
    const proto = location.protocol === "https:" ? "wss" : "ws";
    let done = false;
    const finish = (r: [CheckStatus, string]) => {
      if (done) return;
      done = true;
      resolve(r);
    };
    try {
      const ws = new WebSocket(`${proto}://${location.host}/api/v1/proxy-probe/${nonce}/ws`);
      const timer = setTimeout(() => {
        ws.close();
        finish(["fail", "timeout"]);
      }, 10000);
      ws.onopen = () => ws.send(`fleetwarden-${nonce}`);
      ws.onmessage = (ev) => {
        clearTimeout(timer);
        ws.close();
        finish(ev.data === `fleetwarden-${nonce}` ? ["pass", ""] : ["fail", "echo"]);
      };
      ws.onerror = () => {
        clearTimeout(timer);
        finish(["fail", ""]);
      };
      ws.onclose = (ev) => {
        clearTimeout(timer);
        finish(["fail", ev.code ? String(ev.code) : ""]);
      };
    } catch (e) {
      finish(["fail", String(e)]);
    }
  });
  add("browser_websocket", ...wsStatus);

  try {
    const started = performance.now();
    const res = await fetch(`/api/v1/proxy-probe/${nonce}/upload`, {
      method: "POST",
      headers: { "X-Requested-With": "fleetwarden", "Content-Type": "application/octet-stream" },
      body: new Blob([new Uint8Array(uploadTestBytes)]),
      credentials: "same-origin",
    });
    const secs = (performance.now() - started) / 1000;
    if (res.ok) add("browser_upload", "pass", `${(uploadTestBytes / (1 << 20) / Math.max(secs, 0.001)).toFixed(1)} MB/s`);
    else add("browser_upload", "fail", String(res.status));
  } catch (e) {
    add("browser_upload", "fail", String(e));
  }
  return out;
}

function ProxyTest({ publicUrl }: { publicUrl: string }) {
  const { t } = useTranslation();
  const errorText = useErrorText();
  const [checks, setChecks] = useState<Check[] | null>(null);
  const [running, setRunning] = useState(false);
  const [err, setErr] = useState("");

  const run = async () => {
    setRunning(true);
    setErr("");
    setChecks(null);
    try {
      const res = await post<TestResult>("/settings/proxy/test", { publicUrl });
      const server: Check[] = res.checks.map((c) => ({ ...c, side: "server" }));
      setChecks(server);
      const browser = await browserChecks(res.publicUrl, res.nonce);
      setChecks([...server, ...browser]);
    } catch (e) {
      setErr(errorText(e));
    } finally {
      setRunning(false);
    }
  };

  const count = (s: CheckStatus) => checks?.filter((c) => c.status === s).length ?? 0;

  return (
    <Card
      title={t("proxy.testTitle")}
      description={t("proxy.testSubtitle")}
      actions={
        <Button onClick={() => void run()} loading={running} icon={<FlaskConical className="size-4" />}>
          {t("proxy.runTest")}
        </Button>
      }
    >
      {err && <Alert tone="danger">{err}</Alert>}
      {!checks && !err && !running && <p className="text-sm text-muted">{t("proxy.testIntro")}</p>}
      {running && !checks && <Spinner />}
      {checks && (
        <div className="space-y-4">
          {!running && (
            <Alert tone={count("fail") > 0 ? "danger" : count("warn") > 0 ? "warning" : "success"}>
              {count("fail") > 0
                ? t("proxy.summaryFail", { n: count("fail") })
                : count("warn") > 0
                  ? t("proxy.summaryWarn", { n: count("warn") })
                  : t("proxy.summaryPass")}
            </Alert>
          )}
          {(["server", "browser"] as const).map((side) => {
            const list = checks.filter((c) => c.side === side);
            if (list.length === 0) return running && side === "browser" ? <Spinner key={side} /> : null;
            return (
              <div key={side}>
                <h3 className="mb-2 text-sm font-semibold">{t(`proxy.side.${side}`)}</h3>
                <ul className="divide-y divide-line rounded-md border border-line">
                  {list.map((c) => (
                    <CheckRow key={c.id} c={c} />
                  ))}
                </ul>
              </div>
            );
          })}
        </div>
      )}
    </Card>
  );
}

function CheckRow({ c }: { c: Check }) {
  const { t } = useTranslation();
  const icon = {
    pass: <CheckCircle2 className="size-4 text-success" />,
    warn: <TriangleAlert className="size-4 text-warning" />,
    fail: <XCircle className="size-4 text-danger" />,
    skip: <CircleSlash className="size-4 text-muted" />,
  }[c.status];
  const text = t(`proxy.check.${c.id}.${c.status}`, { detail: c.detail, defaultValue: "" }) || (c.status === "skip" ? t("proxy.skipped") : "");
  return (
    <li className="flex gap-3 px-3 py-2.5">
      <span className="mt-0.5 shrink-0" aria-label={t(`proxy.status.${c.status}`)}>
        {icon}
      </span>
      <div className="min-w-0 flex-1">
        <p className="text-sm font-medium">{t(`proxy.check.${c.id}.title`)}</p>
        {text && <p className="text-xs text-muted">{text}</p>}
        {c.detail && c.status !== "pass" && !text.includes(c.detail) && <p className="mt-0.5 font-mono text-xs break-all text-muted">{c.detail}</p>}
      </div>
    </li>
  );
}
