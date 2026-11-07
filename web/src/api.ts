// Typed client for the FleetWarden API. Every state-changing request carries
// the X-Requested-With header the server requires (CSRF protection).

export type Permission =
  | "dashboard.view"
  | "devices.view"
  | "devices.manage"
  | "users.manage"
  | "settings.manage"
  | "audit.view"
  | "system.update"
  | "backup.manage"
  | "groups.manage"
  | "agents.manage";

export type Role = "administrator" | "technician" | "readonly";

export type User = {
  id: number;
  username: string;
  displayName: string;
  email: string;
  source: "local" | "entra";
  mustChangePassword: boolean;
  totpEnabled: boolean;
  role: Role;
  language: "en" | "nl";
  disabled: boolean;
  breakGlass: boolean;
  scoped: boolean;
  scopeGroups: number[];
  createdAt: string;
  lastLoginAt: string | null;
};

export type Me = User & { permissions: Permission[] };

export type VersionInfo = {
  version: string;
  commit: string;
  channel: "production" | "test" | string;
  buildDate: string;
  goVersion: string;
  platform: string;
};

export type Branding = {
  productName: string;
  primaryColor: string;
  primaryColorDark: string;
  sidebarColor: string;
  hasLogo: boolean;
  hasFavicon: boolean;
  revision: number;
};

export type AuthConfig = { ssoEnabled: boolean; localLoginAllowed: boolean; productName: string };

export type AuthSettings = {
  ssoEnabled: boolean;
  tenantId: string;
  clientId: string;
  clientSecret?: string;
  clientSecretSet: boolean;
  redirectUri: string;
  breakGlassNetworks: string[];
  clientIp: string;
};

export type RoleMapping = { id: number; claim: "group" | "role"; value: string; role: Role; createdAt: string; scoped: boolean; groups: number[] };

export type Channel = "production" | "test";

export type DeviceGroup = {
  id: number;
  name: string;
  description: string;
  updateRing: number;
  agentChannel: Channel | null;
  windowStart: number | null;
  windowEnd: number | null;
  createdAt: string;
  deviceCount: number;
  onlineCount: number;
};

export type Platform = "windows" | "linux";

export type Disk = { name: string; model: string; sizeBytes: number; media: string };
export type Volume = { mount: string; fileSystem: string; sizeBytes: number; freeBytes: number };
export type NetIface = { name: string; mac: string; ips: string[] | null };
export type Hardware = {
  manufacturer: string;
  model: string;
  serial: string;
  bios: string;
  cpu: string;
  cpuCores: number;
  cpuThreads: number;
  memoryBytes: number;
  disks: Disk[] | null;
  volumes: Volume[] | null;
  network: NetIface[] | null;
  gpus?: string[] | null;
  virtual: boolean;
};

export type Device = {
  id: string;
  groupId: number;
  groupName: string;
  hostname: string;
  platform: Platform;
  arch: string;
  agentVersion: string;
  osName: string;
  osVersion: string;
  osBuild: string;
  kind: "workstation" | "server" | "hypervisor";
  domain: string;
  domainJoined: boolean;
  ou: string;
  manufacturer: string;
  model: string;
  serial: string;
  cpu: string;
  cpuCores: number;
  memoryBytes: number;
  loggedInUser: string;
  remoteIp: string;
  localIps: string[];
  bootTime: string | null;
  hardware?: Hardware;
  enrolledAt: string;
  lastSeenAt: string;
  inventoryAt: string | null;
  notes: string;
  online: boolean;
  updateVersion: string;
  updateState: UpdateState | "";
  updateMessage: string;
  updateAt: string | null;
};

export type DeviceList = { items: Device[]; total: number };
export type DeviceStats = { total: number; online: number; platform: Record<string, number> };

export type SoftwareRow = { name: string; version: string; publisher: string; installedOn: string | null; source: string; sizeBytes: number };

export type EnrollmentToken = {
  id: number;
  prefix: string;
  description: string;
  groupId: number;
  groupName: string;
  createdBy: string;
  createdAt: string;
  expiresAt: string | null;
  revokedAt: string | null;
  maxUses: number | null;
  uses: number;
  lastUsedAt: string | null;
};

export type InstallPackage = { platform: Platform; arch: string; kind: "msi" | "deb"; version: string; url: string; name: string };

export type UpdateState = "sent" | "downloading" | "installing" | "completed" | "failed" | "rolled-back";

export type AgentPackage = {
  version: string;
  platform: Platform;
  arch: string;
  kind: "msi" | "deb";
  name: string;
  sha256: string;
  size: number;
  source: "bundled" | "github" | "upload" | "release";
  added: string;
};

export type AgentRelease = {
  version: string;
  prerelease: boolean;
  source: AgentPackage["source"];
  added: string;
  packages: AgentPackage[];
  deletable: boolean;
  devices: number;
};

export type RolloutStatus = "active" | "paused" | "completed" | "cancelled" | "superseded";

export type RingStats = { ring: number; groups: string[]; devices: number; updated: number; inProgress: number; failed: number; pending: number };

export type Rollout = {
  id: number;
  version: string;
  channel: Channel;
  status: RolloutStatus;
  ring: number;
  ringStartedAt: string;
  soakHours: number;
  maxFailures: number;
  automatic: boolean;
  message: string;
  createdBy: string;
  createdAt: string;
  rings: RingStats[];
  soakEndsAt: string | null;
};

export type AgentUpdateSettings = {
  autoDownload: boolean;
  autoRollout: boolean;
  soakHours: number;
  maxFailures: number;
  keepReleases: number;
  lastCheck: string;
  lastError: string;
};

export type AgentUpdates = {
  settings: AgentUpdateSettings;
  channel: Channel;
  releases: AgentRelease[];
  rollouts: Rollout[];
  problems: { deviceId: string; hostname: string; from: string; version: string; state: UpdateState; message: string; at: string | null }[];
  versions: Record<string, number>;
};

export type InstallInfo = {
  token: string;
  serverUrl: string;
  usable: boolean;
  windowsCommand: string;
  linuxCommand: string;
  msiexecCommand: string;
  downloads: InstallPackage[];
};

export type MailConfig = {
  enabled: boolean;
  host: string;
  port: number;
  security: "starttls" | "tls" | "none";
  auth: "none" | "plain" | "login" | "xoauth2";
  username: string;
  password?: string;
  oauthTenant: string;
  oauthClientId: string;
  oauthSecret?: string;
  fromName: string;
  fromAddress: string;
  heloName: string;
  passwordSet?: boolean;
  oauthSecretSet?: boolean;
};

export type MailLogEntry = {
  id: number;
  createdAt: string;
  kind: string;
  to: string;
  subject: string;
  status: "queued" | "sent" | "failed";
  attempts: number;
  nextAttemptAt: string;
  lastError: string;
  sentAt: string | null;
};

export type AuditEntry = {
  id: number;
  at: string;
  actorId: number | null;
  actor: string;
  ip: string;
  action: string;
  target: string;
  success: boolean;
  details: Record<string, unknown>;
};

export type Release = { version: string; channel: string; publishedAt: string; url: string; notes: string };

export type UpdateStatus = {
  id: string;
  state: "" | "downloading" | "backup" | "installing" | "verifying" | "rolling-back" | "completed" | "rolled-back" | "failed";
  fromVersion: string;
  toVersion: string;
  message: string;
  startedAt: string;
  updatedAt: string;
};

export type UpdateView = {
  current: VersionInfo;
  channel: "production" | "test";
  latest: Release | null;
  updateAvailable: boolean;
  checkError?: string;
  status: UpdateStatus;
  pending: boolean;
  checkedAt: string;
};

export type LocationType = "local" | "ftp" | "ftps" | "sftp" | "onedrive";

export type BackupRun = {
  id: number;
  locationId: number | null;
  locationName: string;
  trigger: "schedule" | "manual";
  actor: string;
  kind: "" | "auto" | "full" | "incremental";
  chain: string;
  snapshot: string;
  status: "running" | "succeeded" | "failed";
  startedAt: string;
  finishedAt: string | null;
  sizeBytes: number;
  uploadedBytes: number;
  error: string;
};

// A location as the server returns it: secrets are never sent back.
export type BackupLocation = {
  id: number;
  name: string;
  enabled: boolean;
  type: LocationType;
  host: string;
  port: number;
  username: string;
  implicitTls: boolean;
  tlsFingerprint: string;
  hostKey: string;
  hostKeyFingerprint: string;
  sshAuth: "password" | "key";
  publicKey: string;
  tenantId: string;
  clientId: string;
  driveUser: string;
  path: string;
  passwordSet: boolean;
  clientSecretSet: boolean;
  lastRun: BackupRun | null;
  createdAt: string;
};

export type LocationTest = {
  ok: boolean;
  error?: string;
  trust?: { kind: "ssh" | "tls"; fingerprint: string; value: string; mismatch: boolean };
  repository: "none" | "ok" | "wrong_passphrase" | "unknown";
  snapshots: number;
};

export type BackupSchedule = { enabled: boolean; intervalHours: number; startTime: string; fullEveryDays: number; keepFull: number };

export type BackupConfig = { passphraseSet: boolean; schedule: BackupSchedule; nextRun: string | null; timeZone: string; instanceId: string };

export type LocationProgress = { name: string; state: "waiting" | "running" | "succeeded" | "failed"; done: number; total: number; kind: string; error?: string };

export type BackupProgress = { running: boolean; trigger: string; startedAt: string; phase: "dump" | "upload" | "done" | ""; locations: Record<string, LocationProgress> | null };

export type Snapshot = { chain: string; id: string; kind: "full" | "incremental"; time: string; instance: string };

export type SnapshotDetails = Snapshot & { host: string; version: string; size: number; chunks: number; newChunks: number; restorable: boolean };

export type RestoreStatus = { id: string; state: "applying" | "completed" | "failed"; message: string; snapshot: string; requestedBy: string; startedAt: string; updatedAt: string };

export type RestoreView = {
  state: "" | "preparing" | "requested" | "failed";
  message: string;
  snapshot: string;
  done: number;
  total: number;
  pending: boolean;
  last: RestoreStatus | null;
};

export class ApiError extends Error {
  constructor(
    public status: number,
    public code: string,
    public detail?: string,
  ) {
    super(detail ? `${code}: ${detail}` : code);
  }
}

// Called when any request finds the session gone, so the app can show the login page.
let onUnauthenticated: (() => void) | null = null;
export function setUnauthenticatedHandler(fn: (() => void) | null) {
  onUnauthenticated = fn;
}

export async function api<T = unknown>(method: string, path: string, body?: unknown, signal?: AbortSignal): Promise<T> {
  const headers: Record<string, string> = { "X-Requested-With": "fleetwarden" };
  let payload: BodyInit | undefined;
  if (body instanceof FormData) {
    payload = body; // the browser sets the multipart boundary
  } else if (body instanceof Blob) {
    payload = body;
    headers["Content-Type"] = body.type || "application/octet-stream";
  } else if (body !== undefined) {
    payload = JSON.stringify(body);
    headers["Content-Type"] = "application/json";
  }
  const res = await fetch(`/api/v1${path}`, { method, headers, body: payload, signal, credentials: "same-origin" });
  if (res.status === 204) return undefined as T;
  const data = await res.json().catch(() => ({}));
  if (!res.ok) {
    const err = new ApiError(res.status, data.error ?? `http_${res.status}`, data.detail);
    if (res.status === 401 && err.code === "unauthenticated" && !path.startsWith("/auth/")) onUnauthenticated?.();
    throw err;
  }
  return data as T;
}

export const get = <T>(path: string, signal?: AbortSignal) => api<T>("GET", path, undefined, signal);
export const post = <T>(path: string, body?: unknown) => api<T>("POST", path, body ?? {});
export const put = <T>(path: string, body?: unknown) => api<T>("PUT", path, body ?? {});
export const patch = <T>(path: string, body?: unknown) => api<T>("PATCH", path, body ?? {});
export const del = <T>(path: string) => api<T>("DELETE", path);

export async function fetchVersion(signal?: AbortSignal): Promise<VersionInfo> {
  return get<VersionInfo>("/version", signal);
}
