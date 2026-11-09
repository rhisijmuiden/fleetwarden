import { useCallback, useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { Play, RefreshCw, RotateCw, Search, Shield, Square } from "lucide-react";
import { get, post, type Device } from "../../api";
import { Alert, Badge, Button, Card, Input, Modal, Select, Spinner, useErrorText } from "../../components/ui";
import type { Service, ServiceAction } from "./types";

type Filter = "all" | "running" | "stopped" | "automatic-stopped";

const stateTone = (s: Service["state"]) =>
  s === "running" ? "success" : s === "failed" ? "danger" : s === "starting" || s === "stopping" ? "warning" : "neutral";

export function ServicesPanel({ device }: { device: Device }) {
  const { t } = useTranslation();
  const errorText = useErrorText();
  const [list, setList] = useState<Service[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [search, setSearch] = useState("");
  const [filter, setFilter] = useState<Filter>("all");
  const [busy, setBusy] = useState<string>("");
  const [confirm, setConfirm] = useState<{ svc: Service; action: ServiceAction } | null>(null);

  const load = useCallback(() => {
    setLoading(true);
    get<{ services: Service[] }>(`/devices/${device.id}/manage/services`)
      .then((r) => {
        setList(r.services);
        setError(null);
      })
      .catch((e) => setError(errorText(e)))
      .finally(() => setLoading(false));
  }, [device.id]);
  useEffect(load, [load]);

  const shown = useMemo(() => {
    const q = search.trim().toLowerCase();
    return (list ?? []).filter((s) => {
      if (q && !s.name.toLowerCase().includes(q) && !s.displayName.toLowerCase().includes(q) && !(s.description ?? "").toLowerCase().includes(q)) return false;
      switch (filter) {
        case "running":
          return s.state === "running";
        case "stopped":
          return s.state !== "running";
        case "automatic-stopped":
          return s.state !== "running" && (s.startType === "automatic" || s.startType === "automatic-delayed");
      }
      return true;
    });
  }, [list, search, filter]);

  const run = (svc: Service, action: ServiceAction) => {
    setBusy(svc.name + action);
    setError(null);
    setNotice(null);
    post(`/devices/${device.id}/manage/services/action`, { name: svc.name, action })
      .then(() => {
        setNotice(t(`manage.services.done.${action}`, { name: svc.displayName }));
        load();
      })
      .catch((e) => setError(errorText(e)))
      .finally(() => {
        setBusy("");
        setConfirm(null);
      });
  };
  // Stopping or disabling asks first; starting does not.
  const act = (svc: Service, action: ServiceAction) => {
    if (action === "stop" || action === "disabled" || action === "restart") setConfirm({ svc, action });
    else run(svc, action);
  };

  return (
    <Card
      title={t("manage.sections.services")}
      description={device.platform === "windows" ? t("manage.services.hintWindows") : t("manage.services.hintLinux")}
      actions={
        <Button variant="secondary" size="sm" icon={<RefreshCw className="size-4" />} loading={loading} onClick={load}>
          {t("manage.refresh")}
        </Button>
      }
    >
      <div className="space-y-4">
        {error && <Alert tone="danger">{error}</Alert>}
        {notice && <Alert tone="success">{notice}</Alert>}
        <div className="flex flex-wrap items-center gap-3">
          <div className="relative min-w-[220px] flex-1">
            <Search className="pointer-events-none absolute top-2.5 left-3 size-4 text-muted" />
            <Input className="pl-9" placeholder={t("manage.services.search")} value={search} onChange={(e) => setSearch(e.target.value)} />
          </div>
          <div className="w-60">
            <Select value={filter} onChange={(e) => setFilter(e.target.value as Filter)} aria-label={t("manage.services.filter")}>
            {(["all", "running", "stopped", "automatic-stopped"] as Filter[]).map((f) => (
              <option key={f} value={f}>
                {t(`manage.services.filters.${f}`)}
              </option>
            ))}
            </Select>
          </div>
          {list && <p className="text-xs text-muted">{t("manage.services.count", { count: shown.length, total: list.length })}</p>}
        </div>
        {!list ? (
          error ? null : <Spinner />
        ) : (
          <div className="-mx-5 overflow-x-auto">
            <table className="w-full min-w-[860px] text-sm">
              <thead className="border-y border-line text-left text-xs text-muted uppercase">
                <tr>
                  <th className="px-5 py-3 font-medium">{t("manage.services.service")}</th>
                  <th className="px-3 py-3 font-medium">{t("manage.services.state")}</th>
                  <th className="px-3 py-3 font-medium">{t("manage.services.startType")}</th>
                  <th className="px-3 py-3 font-medium">{t("manage.services.account")}</th>
                  <th className="px-5 py-3 text-right font-medium">{t("common.actions")}</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-line">
                {shown.map((s) => (
                  <tr key={s.name} className="hover:bg-surface-muted/50">
                    <td className="max-w-[380px] px-5 py-2">
                      <div className="truncate font-medium" title={s.description || s.displayName}>
                        {s.displayName}
                      </div>
                      <div className="truncate font-mono text-xs text-muted" title={s.path}>
                        {s.name}
                      </div>
                    </td>
                    <td className="px-3 py-2">
                      <Badge tone={stateTone(s.state)}>{t(`manage.services.states.${s.state}`)}</Badge>
                    </td>
                    <td className="px-3 py-2">
                      {s.protected || s.startType === "static" ? (
                        <span className="text-xs text-muted">{t(`manage.services.startTypes.${s.startType}`)}</span>
                      ) : (
                        <Select
                          className="h-8 w-44 text-xs"
                          value={s.startType === "automatic-delayed" ? "automatic" : s.startType}
                          disabled={busy !== ""}
                          aria-label={t("manage.services.startType")}
                          onChange={(e) => act(s, e.target.value as ServiceAction)}
                        >
                          {(["automatic", "manual", "disabled"] as const).map((v) => (
                            <option key={v} value={v}>
                              {v === "automatic" && s.startType === "automatic-delayed" ? t("manage.services.startTypes.automatic-delayed") : t(`manage.services.startTypes.${v}`)}
                            </option>
                          ))}
                        </Select>
                      )}
                    </td>
                    <td className="max-w-[160px] truncate px-3 py-2 text-xs text-muted" title={s.account}>
                      {s.account || "–"}
                    </td>
                    <td className="px-5 py-2">
                      {s.protected ? (
                        <span className="flex justify-end text-xs text-muted" title={t("manage.services.protected")}>
                          <Shield className="size-3.5" />
                        </span>
                      ) : (
                        <div className="flex justify-end gap-1">
                          {s.state !== "running" && s.state !== "starting" ? (
                            <Button variant="ghost" size="sm" icon={<Play className="size-3.5" />} loading={busy === s.name + "start"} disabled={busy !== "" || s.startType === "disabled"} onClick={() => act(s, "start")}>
                              {t("manage.services.start")}
                            </Button>
                          ) : (
                            <>
                              <Button variant="ghost" size="sm" icon={<RotateCw className="size-3.5" />} loading={busy === s.name + "restart"} disabled={busy !== "" || !s.canStop} onClick={() => act(s, "restart")}>
                                {t("manage.services.restart")}
                              </Button>
                              <Button variant="ghost" size="sm" icon={<Square className="size-3.5" />} loading={busy === s.name + "stop"} disabled={busy !== "" || !s.canStop} onClick={() => act(s, "stop")}>
                                {t("manage.services.stop")}
                              </Button>
                            </>
                          )}
                        </div>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
            {shown.length === 0 && <p className="px-5 py-6 text-center text-sm text-muted">{t("manage.nothingFound")}</p>}
          </div>
        )}
      </div>
      <Modal
        open={confirm !== null}
        onClose={() => setConfirm(null)}
        title={confirm ? t(`manage.services.confirm.${confirm.action}`, { name: confirm.svc.displayName }) : ""}
        footer={
          <>
            <Button variant="secondary" onClick={() => setConfirm(null)}>
              {t("common.cancel")}
            </Button>
            <Button variant={confirm?.action === "restart" ? "primary" : "danger"} loading={busy !== ""} onClick={() => confirm && run(confirm.svc, confirm.action)}>
              {confirm && t(`manage.services.actions.${confirm.action}`)}
            </Button>
          </>
        }
      >
        <p className="text-sm">{t("manage.services.confirmBody")}</p>
      </Modal>
    </Card>
  );
}
