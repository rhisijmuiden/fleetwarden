import { useTranslation } from "react-i18next";
import type { PowerAction, PowerState } from "../../api";
import { Badge } from "../../components/ui";

const stateTone: Record<PowerState, "neutral" | "primary" | "success" | "warning" | "danger"> = {
  sent: "primary",
  "waiting-user": "warning",
  postponed: "warning",
  skipped: "neutral",
  completed: "success",
  failed: "danger",
};

export function PowerStateBadge({ action }: { action: Pick<PowerAction, "state" | "message" | "action"> }) {
  const { t } = useTranslation();
  const label = action.state === "sent" && action.action === "wake" ? t("power.state.waking") : t(`power.state.${action.state}`);
  return (
    <span title={action.message || undefined}>
      <Badge tone={stateTone[action.state] ?? "neutral"}>{label}</Badge>
    </span>
  );
}
