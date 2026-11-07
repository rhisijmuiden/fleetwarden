// Small set of UI primitives in the FleetWarden style. Colours always come
// from the theme tokens so white-label settings apply everywhere.
import {
  forwardRef,
  useEffect,
  useId,
  useRef,
  useState,
  type ButtonHTMLAttributes,
  type InputHTMLAttributes,
  type ReactNode,
  type SelectHTMLAttributes,
} from "react";
import { useTranslation } from "react-i18next";
import { AlertTriangle, Check, CheckCircle2, Copy, Info, Loader2, X, XCircle } from "lucide-react";
import { ApiError } from "../api";

const focus = "outline-none focus-visible:ring-2 focus-visible:ring-primary focus-visible:ring-offset-2 focus-visible:ring-offset-surface";

type ButtonProps = ButtonHTMLAttributes<HTMLButtonElement> & {
  variant?: "primary" | "secondary" | "ghost" | "danger";
  size?: "sm" | "md";
  loading?: boolean;
  icon?: ReactNode;
};

export function Button({ variant = "primary", size = "md", loading, icon, children, className = "", disabled, ...rest }: ButtonProps) {
  const variants = {
    primary: "bg-primary text-primary-contrast hover:bg-primary-hover",
    secondary: "border border-line bg-surface text-fg hover:bg-surface-muted",
    ghost: "text-muted hover:bg-surface-muted hover:text-fg",
    danger: "bg-danger text-white hover:opacity-90",
  };
  const sizes = { sm: "h-8 px-3 text-xs", md: "h-9 px-4 text-sm" };
  return (
    <button
      type="button"
      disabled={disabled || loading}
      className={`inline-flex shrink-0 items-center justify-center gap-2 rounded-md font-medium transition-colors disabled:cursor-not-allowed disabled:opacity-50 ${variants[variant]} ${sizes[size]} ${focus} ${className}`}
      {...rest}
    >
      {loading ? <Loader2 className="size-4 animate-spin" /> : icon}
      {children}
    </button>
  );
}

const fieldBase = `h-9 w-full rounded-md border border-line bg-surface px-3 text-sm text-fg placeholder:text-muted/70 disabled:opacity-60 ${focus}`;

export const Input = forwardRef<HTMLInputElement, InputHTMLAttributes<HTMLInputElement>>(function Input(
  { className = "", ...rest },
  ref,
) {
  return <input ref={ref} className={`${fieldBase} ${className}`} {...rest} />;
});

export function Select({ className = "", children, ...rest }: SelectHTMLAttributes<HTMLSelectElement>) {
  return (
    <select className={`${fieldBase} pr-8 ${className}`} {...rest}>
      {children}
    </select>
  );
}

type FieldProps = { label: string; hint?: ReactNode; children: (id: string) => ReactNode; className?: string };

export function Field({ label, hint, children, className = "" }: FieldProps) {
  const id = useId();
  return (
    <div className={`space-y-1.5 ${className}`}>
      <label htmlFor={id} className="block text-sm font-medium">
        {label}
      </label>
      {children(id)}
      {hint && <p className="text-xs text-muted">{hint}</p>}
    </div>
  );
}

export function Switch({ checked, onChange, label, description }: { checked: boolean; onChange: (v: boolean) => void; label: string; description?: string }) {
  const id = useId();
  return (
    <div className="flex items-start gap-3">
      <button
        id={id}
        type="button"
        role="switch"
        aria-checked={checked}
        onClick={() => onChange(!checked)}
        className={`relative mt-0.5 inline-flex h-5 w-9 shrink-0 rounded-full transition-colors ${checked ? "bg-primary" : "bg-line"} ${focus}`}
      >
        <span className={`absolute top-0.5 size-4 rounded-full bg-white shadow transition-transform ${checked ? "translate-x-4" : "translate-x-0.5"}`} />
      </button>
      <label htmlFor={id} className="cursor-pointer">
        <span className="block text-sm font-medium">{label}</span>
        {description && <span className="block text-xs text-muted">{description}</span>}
      </label>
    </div>
  );
}

export function Card({ title, description, actions, children, className = "" }: { title?: ReactNode; description?: ReactNode; actions?: ReactNode; children: ReactNode; className?: string }) {
  return (
    <section className={`rounded-lg border border-line bg-surface ${className}`}>
      {(title || actions) && (
        <header className="flex flex-wrap items-start justify-between gap-3 border-b border-line px-5 py-4">
          <div className="min-w-0">
            {title && <h2 className="text-base font-semibold">{title}</h2>}
            {description && <p className="mt-0.5 text-sm text-muted">{description}</p>}
          </div>
          {actions && <div className="flex flex-wrap gap-2">{actions}</div>}
        </header>
      )}
      <div className="px-5 py-5">{children}</div>
    </section>
  );
}

export function PageHeader({ title, description, actions }: { title: string; description?: string; actions?: ReactNode }) {
  return (
    <div className="mb-6 flex flex-wrap items-end justify-between gap-4">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">{title}</h1>
        {description && <p className="mt-1 max-w-2xl text-sm text-muted">{description}</p>}
      </div>
      {actions && <div className="flex flex-wrap gap-2">{actions}</div>}
    </div>
  );
}

export function Alert({ tone = "info", children, title }: { tone?: "info" | "success" | "warning" | "danger"; title?: string; children?: ReactNode }) {
  const tones = {
    info: { cls: "border-primary/30 bg-primary/5", icon: <Info className="size-4 text-primary" /> },
    success: { cls: "border-success/30 bg-success/5", icon: <CheckCircle2 className="size-4 text-success" /> },
    warning: { cls: "border-warning/40 bg-warning/5", icon: <AlertTriangle className="size-4 text-warning" /> },
    danger: { cls: "border-danger/30 bg-danger/5", icon: <XCircle className="size-4 text-danger" /> },
  }[tone];
  return (
    <div role={tone === "danger" ? "alert" : "status"} className={`flex gap-3 rounded-md border px-3.5 py-3 text-sm ${tones.cls}`}>
      <span className="mt-0.5 shrink-0">{tones.icon}</span>
      <div className="min-w-0 space-y-1 break-words">
        {title && <p className="font-medium">{title}</p>}
        {children && <div className="text-fg/90">{children}</div>}
      </div>
    </div>
  );
}

export function Badge({ tone = "neutral", children }: { tone?: "neutral" | "primary" | "success" | "warning" | "danger"; children: ReactNode }) {
  const tones = {
    neutral: "bg-surface-muted text-muted",
    primary: "bg-primary/10 text-primary",
    success: "bg-success/10 text-success",
    warning: "bg-warning/10 text-warning",
    danger: "bg-danger/10 text-danger",
  };
  return <span className={`inline-flex items-center rounded-sm px-1.5 py-0.5 text-xs font-medium ${tones[tone]}`}>{children}</span>;
}

export function Spinner() {
  const { t } = useTranslation();
  return (
    <div className="grid place-items-center py-16 text-muted" role="status">
      <Loader2 className="size-6 animate-spin" />
      <span className="sr-only">{t("common.loading")}</span>
    </div>
  );
}

export function Modal({ open, onClose, title, children, footer, wide }: { open: boolean; onClose: () => void; title: string; children: ReactNode; footer?: ReactNode; wide?: boolean }) {
  const { t } = useTranslation();
  const ref = useRef<HTMLDialogElement>(null);
  useEffect(() => {
    const d = ref.current;
    if (!d) return;
    if (open && !d.open) d.showModal();
    if (!open && d.open) d.close();
  }, [open]);
  return (
    <dialog
      ref={ref}
      onClose={onClose}
      className={`m-auto ${wide ? "w-[min(640px,calc(100vw-32px))]" : "w-[min(460px,calc(100vw-32px))]"} rounded-lg border border-line bg-surface p-0 text-fg shadow-2xl backdrop:bg-black/40 backdrop:backdrop-blur-sm`}
    >
      {open && (
        <>
          <div className="flex items-center gap-3 border-b border-line px-5 py-4">
            <h2 className="flex-1 text-base font-semibold">{title}</h2>
            <button type="button" onClick={onClose} aria-label={t("common.close")} className={`grid size-8 place-items-center rounded-md text-muted hover:bg-surface-muted hover:text-fg ${focus}`}>
              <X className="size-4" />
            </button>
          </div>
          <div className="space-y-4 px-5 py-4">{children}</div>
          {footer && <div className="flex flex-wrap justify-end gap-2 border-t border-line px-5 py-3">{footer}</div>}
        </>
      )}
    </dialog>
  );
}

export function CopyField({ value, label }: { value: string; label?: string }) {
  const { t } = useTranslation();
  const [copied, setCopied] = useState(false);
  return (
    <div className="flex gap-2">
      <Input readOnly value={value} aria-label={label} className="font-mono text-xs" onFocus={(e) => e.currentTarget.select()} />
      <Button
        variant="secondary"
        aria-label={t("common.copy")}
        title={t("common.copy")}
        icon={copied ? <Check className="size-4 text-success" /> : <Copy className="size-4" />}
        onClick={() => {
          void navigator.clipboard?.writeText(value).then(() => {
            setCopied(true);
            setTimeout(() => setCopied(false), 1500);
          });
        }}
      />
    </div>
  );
}

// Translates an API error code; unknown codes fall back to a generic message plus detail.
export function useErrorText() {
  const { t } = useTranslation();
  return (e: unknown): string => {
    if (e instanceof ApiError) {
      const msg = t(`errors.${e.code}`, { defaultValue: "" }) || t("errors.generic");
      return e.detail ? `${msg} (${e.detail})` : msg;
    }
    return t("errors.network");
  };
}

export function formatDate(iso: string | null | undefined, lang: string): string {
  if (!iso) return "–";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime()) || d.getFullYear() < 2000) return "–";
  return d.toLocaleString(lang === "nl" ? "nl-NL" : "en-GB", { dateStyle: "medium", timeStyle: "short" });
}
