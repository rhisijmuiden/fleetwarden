export type VersionInfo = {
  version: string;
  commit: string;
  channel: "production" | "test" | string;
  buildDate: string;
  goVersion: string;
  platform: string;
};

export async function fetchVersion(signal?: AbortSignal): Promise<VersionInfo> {
  const res = await fetch("/api/v1/version", { signal });
  if (!res.ok) throw new Error(`HTTP ${res.status}`);
  return res.json();
}
