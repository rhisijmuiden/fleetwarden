// Types of the management API (processes, services, event logs, files).
import { ApiError } from "../../api";

export type Process = {
  pid: number;
  ppid: number;
  name: string;
  path?: string;
  command?: string;
  user?: string;
  session: number;
  cpu: number;
  memory: number;
  threads: number;
  startedAt?: string;
  protected?: boolean;
  agent?: boolean;
};

export type Service = {
  name: string;
  displayName: string;
  description?: string;
  state: "running" | "stopped" | "starting" | "stopping" | "paused" | "failed" | "exited";
  startType: "automatic" | "automatic-delayed" | "manual" | "disabled" | "static";
  account?: string;
  pid?: number;
  path?: string;
  canStop: boolean;
  protected?: boolean;
};

export type ServiceAction = "start" | "stop" | "restart" | "automatic" | "manual" | "disabled";

export type LogSource = { name: string; records?: number };

export type LogEvent = {
  time: string;
  level: number;
  provider: string;
  eventId?: number;
  message: string;
  user?: string;
  pid?: number;
};

export type FileEntry = {
  name: string;
  path: string;
  dir: boolean;
  size: number;
  modified?: string;
  mode?: string;
  link?: boolean;
  hidden?: boolean;
  drive?: "fixed" | "removable" | "network" | "cdrom" | "other";
  free?: number;
};

export type FileListing = { path: string; files: FileEntry[]; truncated: boolean };

export type Transfer = {
  id: string;
  deviceId: string;
  direction: "download" | "upload";
  path: string;
  name: string;
  state: "waiting" | "running" | "ready" | "failed";
  error?: string;
  message?: string;
  size: number;
  done: number;
  zip?: boolean;
  createdAt: string;
  updatedAt: string;
};

// uploadRaw sends a file as the request body and reports progress from 0
// to 1. The name travels in the query string, so any file name works.
export function uploadRaw<T>(path: string, file: File, onProgress?: (p: number) => void): Promise<T> {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    xhr.open("POST", `/api/v1${path}`);
    xhr.withCredentials = true;
    xhr.setRequestHeader("X-Requested-With", "fleetwarden");
    xhr.setRequestHeader("Content-Type", "application/octet-stream");
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
      else if (xhr.status === 413) reject(new ApiError(413, "transfer_too_large"));
      else reject(new ApiError(xhr.status, data.error ?? `http_${xhr.status}`, data.detail));
    };
    xhr.send(file);
  });
}
