import type { LucideIcon } from "lucide-react";
import {
  Bell,
  DatabaseBackup,
  FileBarChart,
  FolderTree,
  KeyRound,
  LayoutDashboard,
  Mail,
  Monitor,
  MonitorPlay,
  Package,
  Palette,
  Rocket,
  Settings,
  ShieldCheck,
  Users,
} from "lucide-react";

export type NavItem = {
  path: string;
  label: string; // i18n key
  icon: LucideIcon;
  phase?: number; // planned phase while not built yet
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
      { path: "/devices", label: "nav.devices", icon: Monitor, phase: 3 },
      { path: "/groups", label: "nav.groups", icon: FolderTree, phase: 3 },
    ],
  },
  {
    label: "nav.management",
    items: [
      { path: "/patches", label: "nav.patches", icon: ShieldCheck, phase: 4 },
      { path: "/software", label: "nav.software", icon: Package, phase: 5 },
      { path: "/deployments", label: "nav.deployments", icon: Rocket, phase: 5 },
      { path: "/remote", label: "nav.remote", icon: MonitorPlay, phase: 6 },
    ],
  },
  {
    label: "nav.insights",
    items: [
      { path: "/reports", label: "nav.reports", icon: FileBarChart, phase: 7 },
      { path: "/alerts", label: "nav.alerts", icon: Bell, phase: 7 },
    ],
  },
  {
    label: "nav.administration",
    items: [
      { path: "/admin/users", label: "nav.users", icon: Users, phase: 1 },
      { path: "/admin/authentication", label: "nav.authentication", icon: KeyRound, phase: 1 },
      { path: "/admin/mail", label: "nav.mail", icon: Mail, phase: 1 },
      { path: "/admin/backup", label: "nav.backup", icon: DatabaseBackup, phase: 2 },
      { path: "/admin/branding", label: "nav.branding", icon: Palette, phase: 1 },
      { path: "/admin/settings", label: "nav.settings", icon: Settings, phase: 1 },
    ],
  },
];
