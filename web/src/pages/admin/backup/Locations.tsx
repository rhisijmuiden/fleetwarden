import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { Cloud, FolderOpen, HardDrive, Pencil, Play, Plus, Server, Trash2 } from "lucide-react";
import { del, post, put, type BackupLocation, type LocationType } from "../../../api";
import { Alert, Badge, Button, Card, Modal, formatDate, useErrorText } from "../../../components/ui";
import { LocationForm, draftBody, draftOf, emptyDraft, type LocationDraft } from "./LocationForm";
import { describeLocation } from "./util";

export const typeIcon: Record<LocationType, typeof Server> = { sftp: Server, ftps: Server, ftp: Server, onedrive: Cloud, local: HardDrive };

export function RunBadge({ status }: { status: "running" | "succeeded" | "failed" }) {
  const { t } = useTranslation();
  const tone = { running: "primary", succeeded: "success", failed: "danger" } as const;
  return <Badge tone={tone[status]}>{t(`backup.status.${status}`)}</Badge>;
}

export function Locations({ locations, onChanged, onRun, running }: { locations: BackupLocation[]; onChanged: () => void; onRun: (id: number) => void; running: boolean }) {
  const { t, i18n } = useTranslation();
  const errorText = useErrorText();
  const [draft, setDraft] = useState<LocationDraft | null>(null);
  const [deleting, setDeleting] = useState<BackupLocation | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);

  const save = async (e: FormEvent) => {
    e.preventDefault();
    if (!draft) return;
    setSaving(true);
    setError(null);
    try {
      if (draft.id) await put(`/backup/locations/${draft.id}`, draftBody(draft));
      else await post("/backup/locations", draftBody(draft));
      setDraft(null);
      onChanged();
    } catch (err) {
      setError(errorText(err));
    } finally {
      setSaving(false);
    }
  };

  const remove = async () => {
    if (!deleting) return;
    try {
      await del(`/backup/locations/${deleting.id}`);
      setDeleting(null);
      onChanged();
    } catch (err) {
      setError(errorText(err));
    }
  };

  return (
    <Card
      title={t("backup.locations")}
      description={t("backup.locationsHint")}
      actions={
        <Button
          icon={<Plus className="size-4" />}
          onClick={() => {
            setError(null);
            setDraft(emptyDraft());
          }}
        >
          {t("backup.addLocation")}
        </Button>
      }
    >
      {locations.length === 0 ? (
        <div className="grid place-items-center gap-2 py-10 text-center">
          <FolderOpen className="size-8 text-muted" />
          <p className="font-medium">{t("backup.noLocations")}</p>
          <p className="max-w-md text-sm text-muted">{t("backup.noLocationsHint")}</p>
        </div>
      ) : (
        <ul className="-mx-5 -my-5 divide-y divide-line">
          {locations.map((l) => {
            const Icon = typeIcon[l.type];
            return (
              <li key={l.id} className={`flex flex-wrap items-center gap-4 px-5 py-4 ${l.enabled ? "" : "opacity-60"}`}>
                <span className="grid size-10 shrink-0 place-items-center rounded-md bg-primary/10 text-primary">
                  <Icon className="size-5" />
                </span>
                <div className="min-w-0 flex-1">
                  <div className="flex flex-wrap items-center gap-2">
                    <span className="font-medium">{l.name}</span>
                    <Badge>{t(`backup.types.${l.type}`)}</Badge>
                    {!l.enabled && <Badge tone="warning">{t("backup.disabled")}</Badge>}
                  </div>
                  <p className="truncate font-mono text-xs text-muted">{describeLocation(l)}</p>
                  <p className="mt-1 text-xs text-muted">
                    {l.lastRun ? (
                      <span className="inline-flex flex-wrap items-center gap-1.5">
                        {t("backup.lastRun")}: {formatDate(l.lastRun.startedAt, i18n.language)} <RunBadge status={l.lastRun.status} />
                        {l.lastRun.error && <span className="text-danger">{l.lastRun.error}</span>}
                      </span>
                    ) : (
                      t("backup.neverRun")
                    )}
                  </p>
                </div>
                <div className="flex gap-1">
                  <Button variant="ghost" size="sm" icon={<Play className="size-4" />} disabled={running} onClick={() => onRun(l.id)} title={t("backup.runHere")} aria-label={t("backup.runHere")} />
                  <Button
                    variant="ghost"
                    size="sm"
                    icon={<Pencil className="size-4" />}
                    onClick={() => {
                      setError(null);
                      setDraft(draftOf(l));
                    }}
                    title={t("common.edit")}
                    aria-label={t("common.edit")}
                  />
                  <Button variant="ghost" size="sm" icon={<Trash2 className="size-4" />} onClick={() => setDeleting(l)} title={t("common.delete")} aria-label={t("common.delete")} />
                </div>
              </li>
            );
          })}
        </ul>
      )}

      <Modal
        open={!!draft}
        onClose={() => setDraft(null)}
        wide
        title={draft?.id ? t("backup.editLocation") : t("backup.addLocation")}
        footer={
          <>
            <Button variant="secondary" onClick={() => setDraft(null)}>
              {t("common.cancel")}
            </Button>
            <Button type="submit" form="location-form" loading={saving}>
              {t("common.save")}
            </Button>
          </>
        }
      >
        {draft && (
          <form id="location-form" onSubmit={save} className="space-y-4">
            {error && <Alert tone="danger">{error}</Alert>}
            <LocationForm draft={draft} onChange={setDraft} />
          </form>
        )}
      </Modal>

      <Modal
        open={!!deleting}
        onClose={() => setDeleting(null)}
        title={t("backup.deleteLocation")}
        footer={
          <>
            <Button variant="secondary" onClick={() => setDeleting(null)}>
              {t("common.cancel")}
            </Button>
            <Button variant="danger" onClick={() => void remove()}>
              {t("common.delete")}
            </Button>
          </>
        }
      >
        <p className="text-sm">{t("backup.deleteLocationBody", { name: deleting?.name })}</p>
      </Modal>
    </Card>
  );
}
