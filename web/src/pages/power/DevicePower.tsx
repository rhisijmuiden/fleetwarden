import { useState } from "react";
import { useTranslation } from "react-i18next";
import type { Device, PowerActionKind } from "../../api";
import { Button } from "../../components/ui";
import { PowerDialog, actionIcon } from "./PowerDialog";

// PowerButtons wakes a switched-off device, or restarts or shuts down an
// online one.
export function PowerButtons({ device, onDone }: { device: Device; onDone: (message: string) => void }) {
  const { t } = useTranslation();
  const [action, setAction] = useState<PowerActionKind | null>(null);
  const actions: PowerActionKind[] = device.online ? ["restart", "shutdown"] : ["wake"];
  return (
    <>
      {actions.map((a) => {
        const Icon = actionIcon[a];
        return (
          <Button key={a} variant="secondary" icon={<Icon className="size-4" />} onClick={() => setAction(a)}>
            {t(`power.actions.${a}`)}
          </Button>
        );
      })}
      <PowerDialog
        action={action}
        deviceIds={[device.id]}
        label={device.hostname}
        onClose={() => setAction(null)}
        onDone={(m) => {
          setAction(null);
          onDone(m);
        }}
      />
    </>
  );
}
