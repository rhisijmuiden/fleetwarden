import { useTranslation } from "react-i18next";
import { Hammer } from "lucide-react";

type Props = { title: string; phase: number };

export function Placeholder({ title, phase }: Props) {
  const { t } = useTranslation();
  return (
    <div className="space-y-6">
      <h1 className="text-2xl font-semibold tracking-tight">{t(title)}</h1>
      <div className="grid place-items-center rounded-lg border border-dashed border-line bg-surface px-6 py-16 text-center">
        <Hammer className="size-10 text-muted" strokeWidth={1.5} />
        <p className="mt-3 font-medium">{t("placeholder.comingIn", { phase })}</p>
        <p className="mt-1 max-w-sm text-sm text-muted">{t("placeholder.body")}</p>
      </div>
    </div>
  );
}
