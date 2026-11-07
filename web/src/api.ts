// Typed client for the FleetWarden API. Every state-changing request carries
// the X-Requested-With header the server requires (CSRF protection).

export type Permission =
  | "dashboard.view"
  | "devices.view"
  | "devices.manage"
  | "users.manage"
  | "settings.manage"
  | "audit.view"
  | "system.update";

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

export type RoleMapping = { id: number; claim: "group" | "role"; value: string; role: Role; createdAt: string };

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
  if (body instanceof Blob) {
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
