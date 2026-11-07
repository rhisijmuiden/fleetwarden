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
  | "agents.manage"
  | "patches.manage"
  | "patches.approve"
  | "software.manage"
  | "software.deploy"
  | "remote.control";

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
  patchPolicyId: number | null;
  remoteConsent: RemoteConsentSetting;
  createdAt: string;
  deviceCount: number;
  onlineCount: number;
};

export type Platform = "windows" | "linux";

export type RemoteConsentSetting = "auto" | "direct" | "notify" | "ask";
export type RemoteState = "pending" | "consent" | "connecting" | "active" | "ended" | "denied" | "failed";

export type RemoteSession = {
  id: string;
  deviceId: string;
  hostname: string;
  groupId: number;
  userId: number | null;
  operator: string;
  kind: "desktop" | "terminal";
  consent: "direct" | "notify" | "ask";
  state: RemoteState;
  reason: string;
  remoteIp: string;
  createdAt: string;
  connectedAt: string | null;
  endedAt: string | null;
  bytesDown: number;
  bytesUp: number;
};

export type RemoteSessionList = { items: RemoteSession[]; total: number };

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
  patchScanAt: string | null;
  patchScanError: string;
  patchSource: string;
  patchCluster: string;
  patchRebootPending: boolean;
  patchMissing: number;
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

export type PatchCategory = "security" | "critical" | "cumulative" | "definition" | "driver" | "feature" | "other";
export type ApprovalMode = "auto" | "manual" | "never";
export type Approval = { mode: ApprovalMode; delayDays: number };
export type RebootPolicy = "never" | "automatic" | "prompt";

export type PatchPolicy = {
  id: number;
  name: string;
  description: string;
  enabled: boolean;
  windowDays: number; // bit 0 = Sunday
  windowStart: number; // minutes after midnight
  windowMinutes: number;
  timeZone: string;
  approvals: Record<PatchCategory, Approval>;
  reboot: RebootPolicy;
  notifyUsers: boolean;
  maxPostpones: number;
  postponeMinutes: number;
  countdownSeconds: number;
  takeOver: boolean;
  scanHours: number;
  createdAt: string;
  updatedAt: string;
  groups: { id: number; name: string }[];
  nextWindow: string | null;
  windowOpen: boolean;
};

export type PatchPolicies = { policies: PatchPolicy[]; defaults: PatchPolicy; categories: PatchCategory[]; serverTimeZone: string };

export type PatchRow = {
  updateId: string;
  title: string;
  kb: string;
  category: PatchCategory;
  severity: string;
  sizeBytes: number;
  releasedAt: string | null;
  currentVersion: string;
  newVersion: string;
  reboot: boolean;
  firstSeenAt: string;
  decision: "" | "approved" | "declined";
  approved: boolean;
  approvedFrom: string | null;
};

export type PatchState = "sent" | "waiting-user" | "postponed" | "installing" | "rebooting" | "completed" | "failed";

export type PatchRun = {
  id: number;
  deviceId: string;
  hostname: string;
  policyId: number | null;
  trigger: "window" | "manual";
  state: PatchState;
  message: string;
  updates: string[];
  results: { id: string; ok: boolean; error?: string }[];
  installed: number;
  failed: number;
  rebootRequired: boolean;
  postpones: number;
  postponedUntil: string | null;
  requestedBy: string;
  startedAt: string;
  updatedAt: string;
  finishedAt: string | null;
};

export type DevicePatches = {
  policy: PatchPolicy | null;
  serverTimeZone: string;
  scanAt: string | null;
  scanError: string;
  source: string;
  cluster: string;
  rebootPending: boolean;
  updates: PatchRow[];
  runs: PatchRun[];
};

export type UpdateSummary = {
  updateId: string;
  title: string;
  kb: string;
  category: PatchCategory;
  severity: string;
  platform: Platform;
  releasedAt: string | null;
  firstSeenAt: string;
  devices: number;
  decision: "" | "approved" | "declined";
  decidedBy: string;
};

export type PatchStats = {
  devices: number;
  managed: number;
  scanned: number;
  upToDate: number;
  missing: number;
  security: number;
  rebootPending: number;
  scanErrors: number;
  runsOpen: number;
  runsFailed: number;
};

export type PatchSummary = { stats: PatchStats; runs: PatchRun[]; policies: number };

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

export type LocationProgress = {
  name: string;
  state: "waiting" | "running" | "succeeded" | "failed";
  done: number;
  total: number;
  kind: string;
  error?: string;
};

export type BackupProgress = {
  running: boolean;
  trigger: string;
  startedAt: string;
  phase: "dump" | "upload" | "done" | "";
  locations: Record<string, LocationProgress> | null;
};

export type Snapshot = { chain: string; id: string; kind: "full" | "incremental"; time: string; instance: string };

export type SnapshotDetails = Snapshot & { host: string; version: string; size: number; chunks: number; newChunks: number; restorable: boolean };

export type RestoreStatus = {
  id: string;
  state: "applying" | "completed" | "failed";
  message: string;
  snapshot: string;
  requestedBy: string;
  startedAt: string;
  updatedAt: string;
};

export type RestoreView = {
  state: "" | "preparing" | "requested" | "failed";
  message: string;
  snapshot: string;
  done: number;
  total: number;
  pending: boolean;
  last: RestoreStatus | null;
};

// ---- software deployment ----

export type PackageSource = "winget" | "upload" | "apt";
export type InstallerKind = "msi" | "exe" | "inno" | "nullsoft" | "burn" | "apt" | "deb";

export type SoftwarePackage = {
  id: number;
  name: string;
  publisher: string;
  description: string;
  platform: Platform;
  source: PackageSource;
  wingetId: string;
  version: string;
  fileSha256: string;
  fileName: string;
  fileSize: number;
  installerKind: InstallerKind | "";
  installArgs: string;
  aptPackage: string;
  detectName: string;
  productCode: string;
  anyHash: boolean;
  timeoutMinutes: number;
  createdBy: string;
  createdAt: string;
  updatedAt: string;
  installed: number;
};

export type CatalogStatus = { packages: number; updatedAt: string | null; checkedAt: string | null; error: string };

export type PackagesView = {
  packages: SoftwarePackage[];
  kinds: Record<Platform, InstallerKind[]>;
  defaultArgs: Record<string, string>;
  catalog: CatalogStatus;
  maxUploadMiB: number;
};

export type CatalogHit = { id: string; name: string; moniker?: string; version: string };
export type CatalogSearch = { status: CatalogStatus; results: CatalogHit[] };

export type CatalogPackage = {
  id: string;
  name: string;
  publisher: string;
  description: string;
  license: string;
  homepage: string;
  version: string;
  versions: string[];
  choice: {
    installer: { kind: InstallerKind; url: string; sha256: string; args?: string; fileName?: string };
    detect: { name: string; productCode?: string; version?: string };
    type: string;
    architecture: string;
    scope: string;
  } | null;
  choiceError: string;
  installers: { architecture: string; type: string; scope: string; locale: string }[];
};

export type UploadResult = { sha256: string; size: number; name: string; kind: InstallerKind };

export type JobCounts = {
  total: number;
  pending: number;
  running: number;
  completed: number;
  skipped: number;
  failed: number;
  cancelled: number;
  reboot: number;
};

export type Deployment = {
  id: number;
  name: string;
  action: "install" | "uninstall" | "browsers";
  packageId: number | null;
  platform: Platform;
  uninstallName: string;
  uninstallVersion: string;
  uninstallArgs: string;
  uninstallPurge: boolean;
  browsers: BrowserKey[];
  groupIds: number[];
  ongoing: boolean;
  startAt: string;
  expireDays: number;
  requestedBy: string;
  createdAt: string;
  cancelledAt: string | null;
  groups: { id: number; name: string }[];
  counts: JobCounts;
};

export type JobState =
  | "pending"
  | "sent"
  | "downloading"
  | "installing"
  | "uninstalling"
  | "waiting"
  | "completed"
  | "skipped"
  | "failed"
  | "cancelled"
  | "expired";

export type SoftwareJob = {
  id: number;
  deploymentId: number;
  deploymentName: string;
  action: "install" | "uninstall" | "browsers";
  deviceId: string;
  hostname: string;
  groupId: number;
  state: JobState;
  message: string;
  exitCode: number | null;
  output: string;
  rebootRequired: boolean;
  version: string;
  attempts: number;
  createdAt: string;
  sentAt: string | null;
  updatedAt: string;
  finishedAt: string | null;
};

export type DeploymentView = { deployment: Deployment; package: SoftwarePackage | null; jobs: SoftwareJob[] };

export type BrowserKey = "chrome" | "edge" | "firefox";
export type BrowserReport = {
  catalog: CatalogStatus;
  browsers: { key: BrowserKey; name: string; latest: string; esr?: string; devices: number; upToDate: number }[];
  devices: {
    deviceId: string;
    hostname: string;
    groupId: number;
    groupName: string;
    lastSeen: string | null;
    online: boolean;
    outdated: boolean;
    browsers: Partial<Record<BrowserKey, { version: string; edition?: string; upToDate: boolean }>>;
  }[];
};

export type InstalledSoftware = { name: string; publisher: string; platform: Platform; source: string; versions: string[]; devices: number };
export type InstalledDevice = { deviceId: string; hostname: string; groupId: number; version: string };

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

// uploadFile sends a file as the raw request body (name in X-File-Name) and
// reports the progress from 0 to 1.
export function uploadFile<T>(path: string, file: File, onProgress?: (p: number) => void): Promise<T> {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    xhr.open("POST", `/api/v1${path}`);
    xhr.withCredentials = true;
    xhr.setRequestHeader("X-Requested-With", "fleetwarden");
    xhr.setRequestHeader("Content-Type", "application/octet-stream");
    xhr.setRequestHeader("X-File-Name", file.name);
    xhr.upload.onprogress = (e) => {
      if (e.lengthComputable) onProgress?.(e.loaded / e.total);
    };
    xhr.onerror = () => reject(new ApiError(0, "network"));
    xhr.onload = () => {
      let data: { error?: string; detail?: string } = {};
      try {
        data = JSON.parse(xhr.responseText);
      } catch {
        /* empty body */
      }
      if (xhr.status >= 200 && xhr.status < 300) resolve(data as T);
      else if (xhr.status === 413) reject(new ApiError(413, "installer_too_large"));
      else reject(new ApiError(xhr.status, data.error ?? `http_${xhr.status}`, data.detail));
    };
    xhr.send(file);
  });
}
