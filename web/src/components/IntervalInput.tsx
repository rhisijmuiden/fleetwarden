import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import { Input, Select } from "./ui";

const units = [
  { key: "days", minutes: 1440 },
  { key: "hours", minutes: 60 },
  { key: "minutes", minutes: 1 },
] as const;

type Unit = (typeof units)[number];

function bestUnit(minutes: number): Unit {
  return units.find((u) => minutes > 0 && minutes % u.minutes === 0) ?? units[2];
}

/** formatInterval renders a number of minutes as "6 hours" or "1 day". */
export function formatInterval(minutes: number, t: TFunction): string {
  const u = bestUnit(minutes);
  return t(`interval.${u.key}`, { count: minutes / u.minutes });
}

/** IntervalInput edits a number of minutes as a number and a unit. */
export function IntervalInput({ id, value, onChange, disabled }: { id?: string; value: number; onChange: (minutes: number) => void; disabled?: boolean }) {
  const { t } = useTranslation();
  const [unit, setUnit] = useState<Unit>(() => bestUnit(value));
  const [text, setText] = useState(() => String(value / bestUnit(value).minutes));
  // Follow outside changes, such as a form that loads or resets.
  useEffect(() => {
    if (Math.round(Number(text) * unit.minutes) !== value) {
      const u = bestUnit(value);
      setUnit(u);
      setText(String(value / u.minutes));
    }
  }, [value]);

  return (
    <div className="flex max-w-xs gap-2">
      <div className="w-24 shrink-0">
        <Input
          id={id}
          type="number"
          min={1}
          step={1}
          disabled={disabled}
          value={text}
          onChange={(e) => {
            setText(e.target.value);
            const n = Number(e.target.value);
            if (n > 0) onChange(Math.round(n * unit.minutes));
          }}
        />
      </div>
      <Select
        aria-label={t("interval.unit")}
        disabled={disabled}
        value={unit.key}
        onChange={(e) => {
          const u = units.find((x) => x.key === e.target.value) ?? units[1];
          setUnit(u);
          const n = Number(text);
          if (n > 0) onChange(Math.round(n * u.minutes));
        }}
      >
        {units.map((u) => (
          <option key={u.key} value={u.key}>
            {t(`interval.unitNames.${u.key}`)}
          </option>
        ))}
      </Select>
    </div>
  );
}
