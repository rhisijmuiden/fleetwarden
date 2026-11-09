import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { get, type DeviceGroup } from "../api";
import { Switch } from "./ui";

// Loads the device groups the signed-in user can see.
export function useGroups(reloadKey = 0): DeviceGroup[] | null {
  const [groups, setGroups] = useState<DeviceGroup[] | null>(null);
  useEffect(() => {
    get<DeviceGroup[]>("/groups")
      .then(setGroups)
      .catch(() => setGroups([]));
  }, [reloadKey]);
  return groups;
}

type Props = {
  scoped: boolean;
  groups: number[];
  onChange: (scoped: boolean, groups: number[]) => void;
  disabled?: boolean;
};

// Limits a role to device groups: off means all groups.
export function GroupScope({ scoped, groups, onChange, disabled }: Props) {
  const { t } = useTranslation();
  const all = useGroups();
  return (
    <div className="space-y-3 rounded-md border border-line p-3">
      <Switch
        checked={scoped}
        onChange={(v) => !disabled && onChange(v, groups)}
        label={t("scope.limit")}
        description={disabled ? t("scope.adminAll") : scoped ? t("scope.limitedHint") : t("scope.allHint")}
      />
      {scoped && !disabled && (
        <div className="grid max-h-48 gap-1 overflow-y-auto sm:grid-cols-2">
          {(all ?? []).map((g) => (
            <label key={g.id} className="flex cursor-pointer items-center gap-2 rounded-md px-2 py-1.5 text-sm hover:bg-surface-muted">
              <input
                type="checkbox"
                className="size-4 accent-primary"
                checked={groups.includes(g.id)}
                onChange={(e) => onChange(true, e.target.checked ? [...groups, g.id] : groups.filter((x) => x !== g.id))}
              />
              <span className="truncate">{g.name}</span>
            </label>
          ))}
          {scoped && groups.length === 0 && <p className="text-xs text-warning sm:col-span-2">{t("scope.noneSelected")}</p>}
        </div>
      )}
    </div>
  );
}
