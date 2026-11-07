import type { TFunction } from "i18next";
import { useTranslation } from "react-i18next";
import type { PatchCategory, PatchPolicy, PatchRun, PatchState } from "../../api";
import { Badge } from "../../components/ui";

const stateTone: Record<PatchState, "neutral" | "primary" | "success" | "warning" | "danger"> = {
  sent: "neutral",
  "waiting-user": "warning",
  postponed: "warning",
  installing: "primary",
  rebooting: "primary",
  completed: "success",
  failed: "danger",
};

export function RunState({ run }: { run: Pick<PatchRun, "state" | "message"> }) {
  const { t } = useTranslation();
  return (
    <span title={run.message || undefined}>
      <Badge tone={stateTone[run.state] ?? "neutral"}>{t(`patches.state.${run.state}`)}</Badge>
    </span>
  );
}

const categoryTone: Partial<Record<PatchCategory, "danger" | "warning" | "primary" | "neutral">> = {
  security: "danger",
  critical: "warning",
  cumulative: "primary",
};

export function CategoryBadge({ category }: { category: PatchCategory }) {
  const { t } = useTranslation();
  return <Badge tone={categoryTone[category] ?? "neutral"}>{t(`patches.categories.${category}`)}</Badge>;
}

export const pad = (n: number) => String(n).padStart(2, "0");

export const minutesToTime = (m: number) => `${pad(Math.floor(m / 60))}:${pad(m % 60)}`;

export function timeToMinutes(v: string): number {
  const [h, m] = v.split(":").map(Number);
  return (h || 0) * 60 + (m || 0);
}

// Sunday first, matching bit 0 of windowDays.
export function weekdayNames(lang: string): string[] {
  const fmt = new Intl.DateTimeFormat(lang === "nl" ? "nl-NL" : "en-GB", { weekday: "short", timeZone: "UTC" });
  return Array.from({ length: 7 }, (_, i) => fmt.format(new Date(Date.UTC(2026, 10, 1 + i)))); // 1 Nov 2026 is a Sunday
}

// Monday-first order for display.
export const weekOrder = [1, 2, 3, 4, 5, 6, 0];

export function windowText(p: Pick<PatchPolicy, "windowDays" | "windowStart" | "windowMinutes">, lang: string, t: TFunction) {
  const names = weekdayNames(lang);
  const days = weekOrder.filter((d) => p.windowDays & (1 << d));
  const dayText = days.length === 7 ? t("patches.everyDay") : days.length === 0 ? t("patches.noDays") : days.map((d) => names[d]).join(", ");
  const end = (p.windowStart + p.windowMinutes) % 1440;
  return `${dayText} · ${minutesToTime(p.windowStart)}–${minutesToTime(end)}`;
}

// Formats a time in the policy's time zone (the window is defined there).
export function formatInZone(iso: string | null | undefined, lang: string, zone: string): string {
  if (!iso) return "–";
  try {
    return new Date(iso).toLocaleString(lang === "nl" ? "nl-NL" : "en-GB", { dateStyle: "medium", timeStyle: "short", timeZone: zone || undefined });
  } catch {
    return new Date(iso).toLocaleString(lang === "nl" ? "nl-NL" : "en-GB", { dateStyle: "medium", timeStyle: "short" });
  }
}
