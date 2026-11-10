import { useCallback, useEffect, useRef, useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { MessageSquare, Send, X } from "lucide-react";
import { useAuth } from "../../auth";
import { get, post, type ChatView, type Device } from "../../api";
import { Alert, Badge, Button, useErrorText } from "../../components/ui";

const maxText = 2000;

// ChatButton opens the chat with the user signed on to the device. Hidden
// without selfservice.handle. The panel itself tells when the device is
// offline, so a previous chat can still be read.
export function ChatButton({ onOpen, size = "md", variant = "secondary" }: { onOpen: () => void; size?: "sm" | "md"; variant?: "primary" | "secondary" }) {
  const { t } = useTranslation();
  const { can } = useAuth();
  if (!can("selfservice.handle")) return null;
  return (
    <Button size={size} variant={variant} icon={<MessageSquare className="size-4" />} title={t("chat.hint")} onClick={onOpen}>
      {t("chat.open")}
    </Button>
  );
}

// ChatPanel is the chat with the user signed on to a device, docked at the
// bottom right. The technician starts the chat with the first message; the
// user answers in the tray. It polls every three seconds.
export function ChatPanel({ device, helpRequestId, onClose }: { device: Device; helpRequestId?: number; onClose: () => void }) {
  const { t, i18n } = useTranslation();
  const errorText = useErrorText();
  const [view, setView] = useState<ChatView | null>(null);
  const [text, setText] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const scroller = useRef<HTMLDivElement>(null);
  const input = useRef<HTMLTextAreaElement>(null);

  const load = useCallback(
    () =>
      get<ChatView>(`/devices/${device.id}/chat`)
        .then(setView)
        .catch(() => {}),
    [device.id],
  );
  useEffect(() => {
    void load();
    const id = window.setInterval(load, 3000);
    return () => window.clearInterval(id);
  }, [load]);

  const chat = view?.chat ?? null;
  const open = chat != null && chat.endedAt == null;
  const count = chat?.messages.length ?? 0;
  useEffect(() => {
    scroller.current?.scrollTo({ top: scroller.current.scrollHeight });
  }, [count, chat?.id]);
  useEffect(() => input.current?.focus(), []);

  const send = async (e?: FormEvent) => {
    e?.preventDefault();
    const body = text.trim();
    if (!body || busy) return;
    setBusy(true);
    setError(null);
    try {
      setView(await post<ChatView>(`/devices/${device.id}/chat`, { text: body, helpRequestId: open ? undefined : helpRequestId }));
      setText("");
    } catch (err) {
      setError(errorText(err));
    } finally {
      setBusy(false);
      input.current?.focus();
    }
  };
  const end = async () => {
    setBusy(true);
    try {
      setView(await post<ChatView>(`/devices/${device.id}/chat/end`));
    } catch (err) {
      setError(errorText(err));
    } finally {
      setBusy(false);
    }
  };

  const time = (iso: string) => new Date(iso).toLocaleTimeString(i18n.language === "nl" ? "nl-NL" : "en-GB", { hour: "2-digit", minute: "2-digit" });
  const online = view?.online ?? device.online;
  const status = !chat
    ? null
    : !open
      ? t(chat.endedBy === "timeout" ? "chat.endedIdle" : "chat.ended")
      : chat.trays > 0
        ? t("chat.delivered")
        : chat.deliveredAt
          ? t("chat.trayGone")
          : t("chat.notDelivered");

  return (
    <section
      role="dialog"
      aria-label={t("chat.title", { host: device.hostname })}
      className="fixed right-4 bottom-4 z-40 flex h-[min(560px,calc(100vh-32px))] w-[min(400px,calc(100vw-32px))] flex-col overflow-hidden rounded-xl border border-line bg-surface shadow-2xl"
    >
      <header className="flex items-start gap-3 bg-primary px-4 py-3 text-primary-contrast">
        <MessageSquare className="mt-0.5 size-5 shrink-0" />
        <div className="min-w-0 flex-1">
          <h2 className="truncate text-sm font-semibold">{t("chat.title", { host: device.hostname })}</h2>
          <p className="truncate text-xs opacity-85">
            {chat?.username ? chat.username : online ? t("chat.online") : t("chat.offline")}
            {helpRequestId ? ` · ${t("chat.linked", { id: helpRequestId })}` : ""}
          </p>
        </div>
        <button type="button" onClick={onClose} aria-label={t("common.close")} className="grid size-7 place-items-center rounded-md hover:bg-white/15">
          <X className="size-4" />
        </button>
      </header>

      <div ref={scroller} className="flex-1 space-y-3 overflow-y-auto bg-surface-muted/60 px-4 py-4">
        {!chat && <p className="py-8 text-center text-sm text-muted">{online ? t("chat.empty") : t("chat.emptyOffline")}</p>}
        {chat && !open && chat.messages.length > 0 && <p className="text-center text-xs text-muted">{t("chat.previous")}</p>}
        {chat?.messages.map((m) => {
          const mine = m.sender === "tech";
          return (
            <div key={m.id} className={`flex flex-col ${mine ? "items-end pl-10" : "items-start pr-10"}`}>
              <span className="mb-0.5 px-1 text-[11px] text-muted">
                {m.name} · {time(m.createdAt)}
              </span>
              <div
                className={`rounded-2xl px-3 py-2 text-sm whitespace-pre-wrap break-words ${
                  mine ? "rounded-br-sm bg-primary text-primary-contrast" : "rounded-bl-sm border border-line bg-surface"
                }`}
              >
                {m.body}
              </div>
            </div>
          );
        })}
      </div>

      <footer className="space-y-2 border-t border-line px-3 py-3">
        {error && <Alert tone="danger">{error}</Alert>}
        {status && (
          <div className="flex items-center justify-between gap-2 text-xs text-muted">
            <span className="flex items-center gap-1.5">
              {open && <Badge tone={chat!.trays > 0 ? "success" : "warning"}>{chat!.trays > 0 ? t("chat.seen") : t("chat.waiting")}</Badge>}
              {status}
            </span>
            {open && (
              <button type="button" disabled={busy} onClick={end} className="font-medium text-danger hover:underline disabled:opacity-50">
                {t("chat.end")}
              </button>
            )}
          </div>
        )}
        <form onSubmit={send} className="flex items-end gap-2">
          <textarea
            ref={input}
            rows={2}
            value={text}
            maxLength={maxText}
            disabled={!open && !online}
            placeholder={open ? t("chat.placeholder") : online ? t("chat.startPlaceholder") : t("chat.offlinePlaceholder")}
            aria-label={t("chat.placeholder")}
            onChange={(e) => setText(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter" && !e.shiftKey) {
                e.preventDefault();
                void send();
              }
            }}
            className="max-h-32 min-h-10 flex-1 resize-none rounded-md border border-line bg-surface px-3 py-2 text-sm focus:border-primary focus:outline-none disabled:opacity-60"
          />
          <Button type="submit" loading={busy} disabled={!text.trim() || (!open && !online)} icon={<Send className="size-4" />} aria-label={t("chat.send")} title={t("chat.send")} />
        </form>
      </footer>
    </section>
  );
}
