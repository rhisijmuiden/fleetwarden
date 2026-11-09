import { useCallback, useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  ArrowUp,
  CheckCircle2,
  Download,
  File as FileIcon,
  Folder,
  FolderPlus,
  HardDrive,
  Loader2,
  Pencil,
  RefreshCw,
  Trash2,
  Upload,
  X,
  XCircle,
} from "lucide-react";
import { ApiError, del, get, post, type Device } from "../../api";
import { Alert, Button, Card, Input, Modal, Spinner, Switch, formatDate, useErrorText } from "../../components/ui";
import { formatBytes } from "../devices/util";
import { uploadRaw, type FileEntry, type FileListing, type Transfer } from "./types";

// parentOf returns the folder above a path, or "" at a root (the drive list).
function parentOf(path: string, windows: boolean): string {
  if (!path) return "";
  if (windows) {
    const p = path.replace(/\\+$/, "");
    if (/^[A-Za-z]:$/.test(p)) return "";
    const i = p.lastIndexOf("\\");
    if (i <= 2) return p.slice(0, 2) + "\\";
    return p.slice(0, i);
  }
  if (path === "/") return "";
  const p = path.replace(/\/+$/, "");
  const i = p.lastIndexOf("/");
  return i <= 0 ? "/" : p.slice(0, i);
}

function crumbs(path: string, windows: boolean): { name: string; path: string }[] {
  if (!path) return [];
  const sep = windows ? "\\" : "/";
  const parts = path.split(sep).filter(Boolean);
  const out: { name: string; path: string }[] = [];
  if (!windows) out.push({ name: "/", path: "/" });
  let acc = "";
  parts.forEach((p, i) => {
    acc = windows ? (i === 0 ? p + "\\" : acc.replace(/\\$/, "") + "\\" + p) : acc + "/" + p;
    out.push({ name: p, path: acc });
  });
  return out;
}

export function FilesPanel({ device }: { device: Device }) {
  const { t, i18n } = useTranslation();
  const errorText = useErrorText();
  const windows = device.platform === "windows";
  const [path, setPath] = useState("");
  const [typed, setTyped] = useState("");
  const [list, setList] = useState<FileListing | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [hidden, setHidden] = useState(false);
  const [transfers, setTransfers] = useState<Transfer[]>([]);
  const [uploads, setUploads] = useState<{ name: string; progress: number }[]>([]);
  const [dialog, setDialog] = useState<
    { kind: "mkdir" } | { kind: "rename"; entry: FileEntry } | { kind: "delete"; entry: FileEntry } | { kind: "overwrite"; files: File[] } | null
  >(null);
  const [name, setName] = useState("");
  const [busy, setBusy] = useState(false);
  const fileInput = useRef<HTMLInputElement>(null);

  const load = useCallback(
    (p: string) => {
      setLoading(true);
      setError(null);
      get<FileListing>(`/devices/${device.id}/manage/files?path=${encodeURIComponent(p)}`)
        .then((r) => {
          setList(r);
          setPath(r.path || p);
          setTyped(r.path || p);
        })
        .catch((e) => setError(errorText(e)))
        .finally(() => setLoading(false));
    },
    [device.id],
  );
  useEffect(() => load(""), [load]);

  const loadTransfers = useCallback(() => {
    get<{ transfers: Transfer[] }>(`/transfers?device=${device.id}`)
      .then((r) => setTransfers(r.transfers))
      .catch(() => {});
  }, [device.id]);
  useEffect(loadTransfers, [loadTransfers]);
  const active = transfers.some((x) => x.state === "waiting" || x.state === "running");
  useEffect(() => {
    if (!active) return;
    const id = setInterval(loadTransfers, 1000);
    return () => clearInterval(id);
  }, [active, loadTransfers]);
  // Show uploaded files once they arrive.
  const finished = transfers.filter((x) => x.direction === "upload" && x.state === "ready").length;
  useEffect(() => {
    if (finished > 0 && path) load(path);
  }, [finished]);

  const open = (e: FileEntry) => {
    if (e.dir) load(e.path);
  };
  const download = (e: FileEntry) => {
    setError(null);
    post<Transfer>(`/devices/${device.id}/manage/files/download`, { path: e.path })
      .then(() => loadTransfers())
      .catch((err) => setError(errorText(err)));
  };
  const startUploads = (files: File[], overwrite: boolean) => {
    setDialog(null);
    for (const f of files) {
      setUploads((u) => [...u, { name: f.name, progress: 0 }]);
      const q = new URLSearchParams({ dir: path, name: f.name });
      if (overwrite) q.set("overwrite", "1");
      uploadRaw<Transfer>(`/devices/${device.id}/manage/files/upload?${q}`, f, (p) =>
        setUploads((u) => u.map((x) => (x.name === f.name ? { ...x, progress: p } : x))),
      )
        .then(() => loadTransfers())
        .catch((err) => setError(errorText(err)))
        .finally(() => setUploads((u) => u.filter((x) => x.name !== f.name)));
    }
  };
  const chooseFiles = (fl: FileList | null) => {
    const files = Array.from(fl ?? []);
    if (files.length === 0) return;
    const existing = new Set((list?.files ?? []).map((f) => f.name.toLowerCase()));
    if (files.some((f) => existing.has(f.name.toLowerCase()))) setDialog({ kind: "overwrite", files });
    else startUploads(files, false);
  };

  const sep = windows ? "\\" : "/";
  const join = (dir: string, n: string) => (dir.endsWith(sep) ? dir + n : dir + sep + n);
  const submitDialog = () => {
    if (!dialog) return;
    setBusy(true);
    setError(null);
    let req: Promise<unknown>;
    let msg = "";
    if (dialog.kind === "mkdir") {
      req = post(`/devices/${device.id}/manage/files/mkdir`, { path: join(path, name.trim()) });
      msg = t("manage.files.created", { name: name.trim() });
    } else if (dialog.kind === "rename") {
      req = post(`/devices/${device.id}/manage/files/rename`, { path: dialog.entry.path, newPath: join(path, name.trim()) });
      msg = t("manage.files.renamed", { name: name.trim() });
    } else if (dialog.kind === "delete") {
      req = post(`/devices/${device.id}/manage/files/delete`, { path: dialog.entry.path, recursive: dialog.entry.dir });
      msg = t("manage.files.deleted", { name: dialog.entry.name });
    } else {
      return;
    }
    req
      .then(() => {
        setNotice(msg);
        setDialog(null);
        load(path);
      })
      .catch((e) => setError(errorText(e)))
      .finally(() => setBusy(false));
  };

  const entries = (list?.files ?? []).filter((f) => hidden || !f.hidden);
  const atRoots = path === "";
  const validName = name.trim() !== "" && !/[<>:"/\\|?*]/.test(name) && name.trim() !== "." && name.trim() !== "..";

  return (
    <div className="space-y-4">
      <Card
        title={t("manage.sections.files")}
        description={t("manage.files.hint")}
        actions={
          <>
            <Switch checked={hidden} onChange={setHidden} label={t("manage.files.showHidden")} />
            <Button variant="secondary" size="sm" icon={<RefreshCw className="size-4" />} loading={loading} onClick={() => load(path)}>
              {t("manage.refresh")}
            </Button>
          </>
        }
      >
        <div className="space-y-4">
          {error && <Alert tone="danger">{error}</Alert>}
          {notice && <Alert tone="success">{notice}</Alert>}
          <div className="flex flex-wrap items-center gap-2">
            <Button variant="secondary" size="sm" icon={<ArrowUp className="size-4" />} disabled={atRoots} onClick={() => load(parentOf(path, windows))} aria-label={t("manage.files.up")} />
            <form
              className="flex min-w-[260px] flex-1 gap-2"
              onSubmit={(e) => {
                e.preventDefault();
                load(typed.trim());
              }}
            >
              <Input className="font-mono text-xs" value={typed} placeholder={windows ? "C:\\Windows\\Logs" : "/var/log"} onChange={(e) => setTyped(e.target.value)} aria-label={t("manage.files.path")} />
              <Button type="submit" variant="secondary" size="sm" className="h-9">
                {t("manage.files.go")}
              </Button>
            </form>
            <Button
              variant="secondary"
              size="sm"
              icon={<FolderPlus className="size-4" />}
              disabled={atRoots}
              onClick={() => {
                setName("");
                setDialog({ kind: "mkdir" });
              }}
            >
              {t("manage.files.newFolder")}
            </Button>
            <Button size="sm" icon={<Upload className="size-4" />} disabled={atRoots} onClick={() => fileInput.current?.click()}>
              {t("manage.files.upload")}
            </Button>
            <input
              ref={fileInput}
              type="file"
              multiple
              className="hidden"
              onChange={(e) => {
                chooseFiles(e.target.files);
                e.target.value = "";
              }}
            />
          </div>
          {!atRoots && (
            <nav className="flex flex-wrap items-center gap-1 text-xs text-muted" aria-label={t("manage.files.path")}>
              <button type="button" className="hover:text-fg" onClick={() => load("")}>
                {t("manage.files.drives")}
              </button>
              {crumbs(path, windows).map((c) => (
                <span key={c.path} className="inline-flex items-center gap-1">
                  <span aria-hidden>›</span>
                  <button type="button" className="font-mono hover:text-fg" onClick={() => load(c.path)}>
                    {c.name}
                  </button>
                </span>
              ))}
            </nav>
          )}
          {!list ? (
            error ? null : <Spinner />
          ) : (
            <div
              className="-mx-5 overflow-x-auto"
              onDragOver={(e) => {
                if (!atRoots) e.preventDefault();
              }}
              onDrop={(e) => {
                if (atRoots) return;
                e.preventDefault();
                chooseFiles(e.dataTransfer.files);
              }}
            >
              <table className="w-full min-w-[640px] text-sm">
                <thead className="border-y border-line text-left text-xs text-muted uppercase">
                  <tr>
                    <th className="px-5 py-3 font-medium">{t("manage.files.name")}</th>
                    <th className="px-3 py-3 text-right font-medium">{atRoots ? t("manage.files.free") : t("manage.files.size")}</th>
                    <th className="px-3 py-3 font-medium">{atRoots ? t("manage.files.driveType") : t("manage.files.modified")}</th>
                    <th className="px-5 py-3">
                      <span className="sr-only">{t("common.actions")}</span>
                    </th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-line">
                  {entries.map((f) => (
                    <tr key={f.path} className={`hover:bg-surface-muted/50 ${f.hidden ? "opacity-70" : ""}`}>
                      <td className="max-w-[420px] px-5 py-2">
                        <button
                          type="button"
                          className={`flex max-w-full items-center gap-2 text-left ${f.dir ? "font-medium hover:text-primary" : "cursor-default"}`}
                          onClick={() => open(f)}
                          onDoubleClick={() => !f.dir && download(f)}
                        >
                          {f.drive ? (
                            <HardDrive className="size-4 shrink-0 text-primary" />
                          ) : f.dir ? (
                            <Folder className="size-4 shrink-0 text-primary" />
                          ) : (
                            <FileIcon className="size-4 shrink-0 text-muted" />
                          )}
                          <span className="truncate">{f.drive && windows ? f.path : f.name}</span>
                        </button>
                      </td>
                      <td className="px-3 py-2 text-right whitespace-nowrap text-muted tabular-nums">
                        {f.drive
                          ? f.size
                            ? t("manage.files.freeOf", { free: formatBytes(f.free ?? 0, i18n.language), size: formatBytes(f.size, i18n.language) })
                            : "–"
                          : f.dir
                            ? "–"
                            : formatBytes(f.size, i18n.language)}
                      </td>
                      <td className="px-3 py-2 text-xs whitespace-nowrap text-muted">
                        {f.drive ? t(`manage.files.driveTypes.${f.drive}`) : formatDate(f.modified, i18n.language)}
                      </td>
                      <td className="px-5 py-2">
                        {!f.drive && (
                          <div className="flex justify-end gap-1">
                            <Button variant="ghost" size="sm" icon={<Download className="size-3.5" />} onClick={() => download(f)} title={f.dir ? t("manage.files.downloadZip") : t("manage.files.download")}>
                              <span className="sr-only">{t("manage.files.download")}</span>
                            </Button>
                            <Button
                              variant="ghost"
                              size="sm"
                              icon={<Pencil className="size-3.5" />}
                              title={t("manage.files.rename")}
                              onClick={() => {
                                setName(f.name);
                                setDialog({ kind: "rename", entry: f });
                              }}
                            >
                              <span className="sr-only">{t("manage.files.rename")}</span>
                            </Button>
                            <Button variant="ghost" size="sm" icon={<Trash2 className="size-3.5" />} title={t("common.delete")} onClick={() => setDialog({ kind: "delete", entry: f })}>
                              <span className="sr-only">{t("common.delete")}</span>
                            </Button>
                          </div>
                        )}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
              {entries.length === 0 && <p className="px-5 py-6 text-center text-sm text-muted">{t("manage.files.empty")}</p>}
              {list.truncated && <p className="px-5 pt-3 text-xs text-muted">{t("manage.files.truncated")}</p>}
              {!atRoots && <p className="px-5 pt-3 text-xs text-muted">{t("manage.files.dropHint")}</p>}
            </div>
          )}
        </div>
      </Card>

      {(transfers.length > 0 || uploads.length > 0) && (
        <Card title={t("manage.files.transfers")} description={t("manage.files.transfersHint")}>
          <ul className="divide-y divide-line text-sm">
            {uploads.map((u) => (
              <li key={"u" + u.name} className="flex items-center gap-3 py-2">
                <Loader2 className="size-4 animate-spin text-primary" />
                <span className="flex-1 truncate">{t("manage.files.sendingToServer", { name: u.name })}</span>
                <Progress value={u.progress} />
              </li>
            ))}
            {transfers.map((x) => (
              <li key={x.id} className="flex flex-wrap items-center gap-3 py-2">
                {x.state === "ready" ? (
                  <CheckCircle2 className="size-4 text-success" />
                ) : x.state === "failed" ? (
                  <XCircle className="size-4 text-danger" />
                ) : (
                  <Loader2 className="size-4 animate-spin text-primary" />
                )}
                <div className="min-w-0 flex-1">
                  <div className="truncate font-medium" title={x.path}>
                    {x.direction === "download" ? <Download className="mr-1 inline size-3.5" /> : <Upload className="mr-1 inline size-3.5" />}
                    {x.name}
                  </div>
                  <div className="truncate text-xs text-muted" title={x.path}>
                    {x.state === "failed"
                      ? errorText(new ApiError(0, x.error || "generic", x.message))
                      : x.state === "ready"
                        ? x.direction === "download"
                          ? t("manage.files.readyDownload", { size: formatBytes(x.size, i18n.language) })
                          : t("manage.files.readyUpload", { path: x.path })
                        : x.state === "waiting"
                          ? t("manage.files.waiting")
                          : `${formatBytes(x.done, i18n.language)}${x.size ? ` / ${formatBytes(x.size, i18n.language)}` : ""}`}
                  </div>
                </div>
                {(x.state === "running" || x.state === "waiting") && x.size > 0 && <Progress value={x.done / x.size} />}
                {x.state === "ready" && x.direction === "download" && (
                  <a
                    href={`/api/v1/transfers/${x.id}/file`}
                    className="inline-flex h-8 items-center gap-2 rounded-md bg-primary px-3 text-xs font-medium text-primary-contrast hover:bg-primary-hover"
                  >
                    <Download className="size-3.5" /> {t("manage.files.save")}
                  </a>
                )}
                <Button
                  variant="ghost"
                  size="sm"
                  icon={<X className="size-3.5" />}
                  title={t("manage.files.removeTransfer")}
                  onClick={() => del(`/transfers/${x.id}`).then(loadTransfers).catch(() => {})}
                >
                  <span className="sr-only">{t("manage.files.removeTransfer")}</span>
                </Button>
              </li>
            ))}
          </ul>
        </Card>
      )}

      <Modal
        open={dialog?.kind === "mkdir" || dialog?.kind === "rename"}
        onClose={() => setDialog(null)}
        title={dialog?.kind === "rename" ? t("manage.files.rename") : t("manage.files.newFolder")}
        footer={
          <>
            <Button variant="secondary" onClick={() => setDialog(null)}>
              {t("common.cancel")}
            </Button>
            <Button loading={busy} disabled={!validName} onClick={submitDialog}>
              {t("common.save")}
            </Button>
          </>
        }
      >
        <Input
          autoFocus
          value={name}
          onChange={(e) => setName(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter" && validName) submitDialog();
          }}
          aria-label={t("manage.files.name")}
        />
      </Modal>
      <Modal
        open={dialog?.kind === "delete"}
        onClose={() => setDialog(null)}
        title={t("manage.files.deleteTitle")}
        footer={
          <>
            <Button variant="secondary" onClick={() => setDialog(null)}>
              {t("common.cancel")}
            </Button>
            <Button variant="danger" loading={busy} onClick={submitDialog}>
              {t("common.delete")}
            </Button>
          </>
        }
      >
        {dialog?.kind === "delete" && (
          <p className="text-sm">
            {dialog.entry.dir ? t("manage.files.deleteFolderBody", { path: dialog.entry.path }) : t("manage.files.deleteBody", { path: dialog.entry.path })}
          </p>
        )}
      </Modal>
      <Modal
        open={dialog?.kind === "overwrite"}
        onClose={() => setDialog(null)}
        title={t("manage.files.overwriteTitle")}
        footer={
          <>
            <Button variant="secondary" onClick={() => setDialog(null)}>
              {t("common.cancel")}
            </Button>
            <Button variant="danger" onClick={() => dialog?.kind === "overwrite" && startUploads(dialog.files, true)}>
              {t("manage.files.overwrite")}
            </Button>
          </>
        }
      >
        <p className="text-sm">{t("manage.files.overwriteBody")}</p>
      </Modal>
    </div>
  );
}

function Progress({ value }: { value: number }) {
  const pct = Math.max(0, Math.min(100, Math.round(value * 100)));
  return (
    <div className="flex w-36 items-center gap-2" role="progressbar" aria-valuenow={pct} aria-valuemin={0} aria-valuemax={100}>
      <div className="h-1.5 flex-1 overflow-hidden rounded-full bg-surface-muted">
        <div className="h-full rounded-full bg-primary transition-all" style={{ width: `${pct}%` }} />
      </div>
      <span className="w-9 text-right text-xs text-muted tabular-nums">{pct}%</span>
    </div>
  );
}
