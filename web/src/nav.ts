import type { LucideIcon } from "lucide-react";
import type { Permission } from "./api";
import {
  Bell,
  Cpu,
  ScrollText,
  DatabaseBackup,
  FileBarChart,
  FolderTree,
  KeyRound,
  LayoutDashboard,
  Mail,
  Network,
  Monitor,
  MonitorPlay,
  Package,
  Palette,
  RefreshCw,
  Rocket,
  Settings,
  SlidersHorizontal,
  ShieldCheck,
  ShieldEllipsis,
  ShieldHalf,
  Users,
} from "lucide-react";

export type NavItem = {
  path: string;
  label: string; // i18n key
  icon: LucideIcon;
  phase?: number; // planned phase while not built yet
  perm?: Permission; // hidden for users without this permission
  badge?: "alerts"; // shows a live count
};

export type NavGroup = { label: string; items: NavItem[] };

// Menu grouped by task: what you look at, what you manage, what you learn, how you configure.
export const navigation: NavGroup[] = [
  {
    label: "nav.overview",
    items: [{ path: "/", label: "nav.dashboard", icon: LayoutDashboard }],
  },
  {
    label: "nav.endpoints",
    items: [
      { path: "/devices", label: "nav.devices", icon: Monitor, perm: "devices.view" },
      { path: "/groups", label: "nav.groups", icon: FolderTree, perm: "devices.view" },
    ],
  },
  {
    label: "nav.management",
    items: [
      { path: "/patches", label: "nav.patches", icon: ShieldCheck, perm: "devices.view" },
      { path: "/drivers", label: "nav.drivers", icon: Cpu, perm: "devices.view" },
      { path: "/software", label: "nav.software", icon: Package, perm: "devices.view" },
      { path: "/deployments", label: "nav.deployments", icon: Rocket, perm: "devices.view" },
      { path: "/configurations", label: "nav.configurations", icon: SlidersHorizontal, perm: "devices.view" },
      { path: "/remote", label: "nav.remote", icon: MonitorPlay, perm: "remote.control" },
    ],
  },
  {
    label: "nav.insights",
    items: [
      { path: "/reports", label: "nav.reports", icon: FileBarChart, perm: "devices.view" },
      { path: "/alerts", label: "nav.alerts", icon: Bell, perm: "devices.view", badge: "alerts" },
      { path: "/security", label: "nav.security", icon: ShieldEllipsis, perm: "devices.view" },
    ],
  },
  {
    label: "nav.administration",
    items: [
      { path: "/admin/users", label: "nav.users", icon: Users, perm: "users.manage" },
      { path: "/admin/authentication", label: "nav.authentication", icon: KeyRound, perm: "settings.manage" },
      { path: "/admin/mail", label: "nav.mail", icon: Mail, perm: "settings.manage" },
      { path: "/admin/backup", label: "nav.backup", icon: DatabaseBackup, perm: "backup.manage" },
      { path: "/admin/agent-updates", label: "nav.agentUpdates", icon: RefreshCw, perm: "agents.manage" },
      { path: "/admin/gateways", label: "nav.gateways", icon: ShieldHalf, perm: "settings.manage" },
      { path: "/admin/reverse-proxy", label: "nav.reverseProxy", icon: Network, perm: "settings.manage" },
      { path: "/admin/branding", label: "nav.branding", icon: Palette, perm: "settings.manage" },
      { path: "/admin/audit", label: "nav.audit", icon: ScrollText, perm: "audit.view" },
      { path: "/admin/settings", label: "nav.settings", icon: Settings, perm: "settings.manage" },
    ],
  },
];
