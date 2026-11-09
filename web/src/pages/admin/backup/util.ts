import type { BackupLocation, LocationType } from "../../../api";

export function formatBytes(n: number, lang: string): string {
  const units = ["B", "KB", "MB", "GB", "TB"];
  let i = 0;
  let v = n;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  const fmt = new Intl.NumberFormat(lang === "nl" ? "nl-NL" : "en-GB", { maximumFractionDigits: i === 0 ? 0 : 1 });
  return `${fmt.format(v)} ${units[i]}`;
}

export const locationTypes: LocationType[] = ["sftp", "ftps", "ftp", "onedrive", "local"];

// Where a location points to, in one line.
export function describeLocation(l: Pick<BackupLocation, "type" | "host" | "port" | "username" | "driveUser" | "path">): string {
  const folder = l.path ? `/${l.path}` : "/";
  switch (l.type) {
    case "local":
      return `/var/lib/fleetwarden/backups${folder}`;
    case "onedrive":
      return `${l.driveUser} · OneDrive${folder}`;
    default:
      return `${l.username}@${l.host}:${l.port}${folder}`;
  }
}
