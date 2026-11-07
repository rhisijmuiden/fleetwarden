import { useCallback, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { Search } from "lucide-react";
import { get, type AuditEntry } from "../../api";
import { Alert, Badge, Button, Card, Input, PageHeader, Spinner, formatDate, useErrorText } from "../../components/ui";

const pageSize = 100;

export function Audit() {
  const { t, i18n } = useTranslation();
  const errorText = useErrorText();
  const [q, setQ] = useState("");
  const [query, setQuery] = useState("");
  const [rows, setRows] = useState<AuditEntry[] | null>(null);
  const [more, setMore] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(
    (before?: number) => {
      const p = new URLSearchParams({ limit: String(pageSize) });
      if (query) p.set("q", query);
      if (before) p.set("before", String(before));
      get<AuditEntry[]>(`/audit?${p}`)
        .then((list) => {
          setRows((prev) => (before && prev ? [...prev, ...list] : list));
          setMore(list.length === pageSize);
        })
        .catch((e) => setError(errorText(e)));
    },
    [query],
  );
  useEffect(() => {
    setRows(null);
    load();
  }, [load]);

  const label = (action: string) => t(`audit.actions.${action.replace(/\./g, "_")}`, { defaultValue: action });
  return (
    <div className="space-y-6">
      <PageHeader title={t("audit.title")} description={t("audit.subtitle")} />
      {error && <Alert tone="danger">{error}</Alert>}
      <form
        onSubmit={(e) => {
          e.preventDefault();
          setQuery(q.trim());
        }}
        className="flex max-w-md gap-2"
      >
        <Input value={q} onChange={(e) => setQ(e.target.value)} placeholder={t("audit.search")} aria-label={t("audit.search")} />
        <Button type="submit" variant="secondary" icon={<Search className="size-4" />} aria-label={t("audit.search")} />
      </form>
      <Card>
        {!rows ? (
          <Spinner />
        ) : rows.length === 0 ? (
          <p className="py-6 text-center text-sm text-muted">{t("audit.empty")}</p>
        ) : (
          <div className="-mx-5 -my-5 overflow-x-auto">
            <table className="w-full min-w-[760px] text-sm">
              <thead className="border-b border-line text-left text-xs text-muted uppercase">
                <tr>
                  <th className="px-5 py-3 font-medium">{t("audit.when")}</th>
                  <th className="px-3 py-3 font-medium">{t("audit.who")}</th>
                  <th className="px-3 py-3 font-medium">{t("audit.action")}</th>
                  <th className="px-3 py-3 font-medium">{t("audit.target")}</th>
                  <th className="px-5 py-3 font-medium">{t("audit.ip")}</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-line">
                {rows.map((r) => (
                  <tr key={r.id} className="align-top">
                    <td className="px-5 py-3 whitespace-nowrap text-muted">{formatDate(r.at, i18n.language)}</td>
                    <td className="px-3 py-3 font-medium">{r.actor || "–"}</td>
                    <td className="px-3 py-3">
                      <div className="flex items-center gap-2">
                        {label(r.action)}
                        {!r.success && <Badge tone="danger">{t("audit.failed")}</Badge>}
                      </div>
                      {Object.keys(r.details ?? {}).length > 0 && (
                        <p className="mt-0.5 font-mono text-[11px] break-all text-muted">
                          {Object.entries(r.details)
                            .map(([k, v]) => `${k}=${typeof v === "string" ? v : JSON.stringify(v)}`)
                            .join("  ")}
                        </p>
                      )}
                    </td>
                    <td className="px-3 py-3 break-all">{r.target}</td>
                    <td className="px-5 py-3 font-mono text-xs text-muted">{r.ip}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Card>
      {more && rows && (
        <Button variant="secondary" onClick={() => load(rows[rows.length - 1].id)}>
          {t("audit.more")}
        </Button>
      )}
    </div>
  );
}
