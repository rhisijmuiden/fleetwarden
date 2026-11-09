// Types of the self-service API (internal/server/selfservice.go).

export type GroupRef = { id: number; name: string };

export type SelfServiceItem = {
  id: number;
  packageId: number;
  displayName: string;
  description: string;
  category: string;
  groupIds: number[];
  approval: boolean;
  enabled: boolean;
  createdBy: string;
  createdAt: string;
  updatedAt: string;
  groups: GroupRef[];
  packageName: string;
  packageDescription: string;
  publisher: string;
  platform: "windows" | "linux";
  source: "winget" | "upload" | "apt";
  version: string;
  requests: number;
  installs: number;
};

export type RequestState = "pending" | "approved" | "rejected" | "cancelled";

export type SelfServiceRequest = {
  id: number;
  itemId: number | null;
  packageId: number | null;
  name: string;
  deviceId: string;
  hostname: string;
  groupId: number;
  groupName: string;
  username: string;
  reason: string;
  state: RequestState;
  deploymentId: number | null;
  decidedBy: string;
  decidedAt: string | null;
  note: string;
  createdAt: string;
  jobState: string;
  jobMessage: string;
};

export type HelpState = "open" | "in_progress" | "closed";

export type HelpRequest = {
  id: number;
  deviceId: string;
  hostname: string;
  groupId: number;
  groupName: string;
  online: boolean;
  username: string;
  subject: string;
  message: string;
  contact: string;
  state: HelpState;
  assignedTo: string;
  reply: string;
  createdAt: string;
  updatedAt: string;
  closedAt: string | null;
};

export type SelfServiceSettings = {
  software: boolean;
  updates: boolean;
  help: boolean;
  helpText: string;
  recipients: string[];
  language: "en" | "nl";
};

export type SelfServiceCounts = { pendingRequests: number; openHelp: number };
