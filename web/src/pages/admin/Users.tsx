import { useCallback, useEffect, useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { KeyRound, Pencil, Plus, ShieldCheck, Trash2 } from "lucide-react";
import { del, get, patch, post, type Role, type User } from "../../api";
import { useAuth } from "../../auth";
import { Alert, Badge, Button, Card, CopyField, Field, Input, Modal, PageHeader, Select, Spinner, Switch, formatDate, useErrorText } from "../../components/ui";

const roles: Role[] = ["administrator", "technician", "readonly"];

type Editing = { mode: "create" } | { mode: "edit"; user: User } | null;

export function Users() {
  const { t, i18n } = useTranslation();
  const { me } = useAuth();
  const errorText = useErrorText();
  const [users, setUsers] = useState<User[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [editing, setEditing] = useState<Editing>(null);
  const [oneTime, setOneTime] = useState<{ user: string; password: string } | null>(null);
  const [resetting, setResetting] = useState<User | null>(null);
  const [deleting, setDeleting] = useState<User | null>(null);

  const load = useCallback(() => {
    get<User[]>("/users").then(setUsers).catch((e) => setError(errorText(e)));
  }, []);
  useEffect(load, [load]);

  return (
    <div className="space-y-6">
      <PageHeader
        title={t("users.title")}
        description={t("users.subtitle")}
        actions={
          <Button icon={<Plus className="size-4" />} onClick={() => setEditing({ mode: "create" })}>
            {t("users.add")}
          </Button>
        }
      />
      {error && <Alert tone="danger">{error}</Alert>}
      <Card>
        {!users ? (
          <Spinner />
        ) : (
          <div className="-mx-5 -my-5 overflow-x-auto">
            <table className="w-full min-w-[720px] text-sm">
              <thead className="border-b border-line text-left text-xs text-muted uppercase">
                <tr>
                  <th className="px-5 py-3 font-medium">{t("users.user")}</th>
                  <th className="px-3 py-3 font-medium">{t("users.role")}</th>
                  <th className="px-3 py-3 font-medium">{t("users.source")}</th>
                  <th className="px-3 py-3 font-medium">{t("users.lastLogin")}</th>
                  <th className="px-5 py-3 text-right font-medium">
                    <span className="sr-only">{t("common.actions")}</span>
                  </th>
                </tr>
              </thead>
              <tbody className="divide-y divide-line">
                {users.map((u) => (
                  <tr key={u.id} className={u.disabled ? "opacity-60" : ""}>
                    <td className="px-5 py-3">
                      <div className="flex items-center gap-2 font-medium">
                        {u.displayName || u.username}
                        {u.breakGlass && <Badge tone="warning">{t("users.breakGlass")}</Badge>}
                        {u.disabled && <Badge tone="danger">{t("users.disabled")}</Badge>}
                        {u.totpEnabled && <ShieldCheck className="size-4 text-success" aria-label={t("profile.totpOn")} />}
                      </div>
                      <div className="text-xs text-muted">
                        {u.username}
                        {u.email && u.email !== u.username ? ` · ${u.email}` : ""}
                      </div>
                    </td>
                    <td className="px-3 py-3">{t(`roles.${u.role}`)}</td>
                    <td className="px-3 py-3">
                      <Badge tone={u.source === "entra" ? "primary" : "neutral"}>{t(`users.sources.${u.source}`)}</Badge>
                    </td>
                    <td className="px-3 py-3 text-muted">{formatDate(u.lastLoginAt, i18n.language)}</td>
                    <td className="px-5 py-3">
                      <div className="flex justify-end gap-1">
                        <Button variant="ghost" size="sm" aria-label={t("common.edit")} title={t("common.edit")} icon={<Pencil className="size-4" />} onClick={() => setEditing({ mode: "edit", user: u })} />
                        {u.source === "local" && (
                          <Button variant="ghost" size="sm" aria-label={t("users.resetPassword")} title={t("users.resetPassword")} icon={<KeyRound className="size-4" />} onClick={() => setResetting(u)} />
                        )}
                        {!u.breakGlass && u.id !== me?.id && (
                          <Button variant="ghost" size="sm" aria-label={t("common.delete")} title={t("common.delete")} icon={<Trash2 className="size-4" />} onClick={() => setDeleting(u)} />
                        )}
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Card>
      <RolesCard />

      <UserForm
        editing={editing}
        onClose={() => setEditing(null)}
        onSaved={(pw, username) => {
          setEditing(null);
          load();
          if (pw) setOneTime({ user: username, password: pw });
        }}
      />
      <ResetPassword
        user={resetting}
        onClose={() => setResetting(null)}
        onDone={(pw) => {
          if (resetting) setOneTime({ user: resetting.username, password: pw });
          setResetting(null);
          load();
        }}
      />
      <Modal
        open={!!deleting}
        onClose={() => setDeleting(null)}
        title={t("users.deleteTitle")}
        footer={
          <>
            <Button variant="secondary" onClick={() => setDeleting(null)}>
              {t("common.cancel")}
            </Button>
            <Button
              variant="danger"
              onClick={() =>
                deleting &&
                del(`/users/${deleting.id}`)
                  .then(() => {
                    setDeleting(null);
                    load();
                  })
                  .catch((e) => {
                    setDeleting(null);
                    setError(errorText(e));
                  })
              }
            >
              {t("common.delete")}
            </Button>
          </>
        }
      >
        <p className="text-sm">{t("users.deleteConfirm", { user: deleting?.username })}</p>
      </Modal>
      <Modal open={!!oneTime} onClose={() => setOneTime(null)} title={t("users.oneTimeTitle")} footer={<Button onClick={() => setOneTime(null)}>{t("common.done")}</Button>}>
        <Alert tone="warning">{t("users.oneTimeHint", { user: oneTime?.user })}</Alert>
        {oneTime && <CopyField value={oneTime.password} label={t("login.password")} />}
      </Modal>
    </div>
  );
}

function UserForm({ editing, onClose, onSaved }: { editing: Editing; onClose: () => void; onSaved: (oneTime: string | undefined, username: string) => void }) {
  const { t } = useTranslation();
  const { me } = useAuth();
  const errorText = useErrorText();
  const user = editing?.mode === "edit" ? editing.user : null;
  const [username, setUsername] = useState("");
  const [displayName, setDisplayName] = useState("");
  const [email, setEmail] = useState("");
  const [role, setRole] = useState<Role>("technician");
  const [disabled, setDisabled] = useState(false);
  const [password, setPassword] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    setUsername(user?.username ?? "");
    setDisplayName(user?.displayName ?? "");
    setEmail(user?.email ?? "");
    setRole(user?.role ?? "technician");
    setDisabled(user?.disabled ?? false);
    setPassword("");
    setError(null);
  }, [editing, user]);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      if (user) {
        await patch(`/users/${user.id}`, { displayName, email, role: user.source === "local" ? role : undefined, disabled });
        onSaved(undefined, user.username);
      } else {
        const r = await post<{ user: User; oneTimePassword?: string }>("/users", { username, displayName, email, role, password });
        onSaved(r.oneTimePassword, r.user.username);
      }
    } catch (err) {
      setError(errorText(err));
    } finally {
      setBusy(false);
    }
  };

  const entra = user?.source === "entra";
  return (
    <Modal
      open={!!editing}
      onClose={onClose}
      title={user ? t("users.editTitle") : t("users.add")}
      footer={
        <>
          <Button variant="secondary" onClick={onClose}>
            {t("common.cancel")}
          </Button>
          <Button type="submit" form="user-form" loading={busy}>
            {t("common.save")}
          </Button>
        </>
      }
    >
      <form id="user-form" onSubmit={submit} className="space-y-4">
        {error && <Alert tone="danger">{error}</Alert>}
        <Field label={t("users.username")} hint={!user ? t("users.usernameHint") : undefined}>
          {(id) => <Input id={id} required disabled={!!user} value={username} onChange={(e) => setUsername(e.target.value)} autoComplete="off" />}
        </Field>
        <div className="grid gap-4 sm:grid-cols-2">
          <Field label={t("users.name")}>{(id) => <Input id={id} value={displayName} onChange={(e) => setDisplayName(e.target.value)} />}</Field>
          <Field label={t("users.email")}>{(id) => <Input id={id} type="email" value={email} onChange={(e) => setEmail(e.target.value)} />}</Field>
        </div>
        <Field label={t("users.role")} hint={entra ? t("users.roleFromEntra") : user?.breakGlass ? t("users.breakGlassRole") : undefined}>
          {(id) => (
            <Select id={id} value={role} disabled={entra || user?.breakGlass} onChange={(e) => setRole(e.target.value as Role)}>
              {roles.map((r) => (
                <option key={r} value={r}>
                  {t(`roles.${r}`)}
                </option>
              ))}
            </Select>
          )}
        </Field>
        {!user && (
          <Field label={t("users.initialPassword")} hint={t("users.initialPasswordHint")}>
            {(id) => <Input id={id} type="password" autoComplete="new-password" value={password} onChange={(e) => setPassword(e.target.value)} />}
          </Field>
        )}
        {user && user.id !== me?.id && <Switch checked={disabled} onChange={setDisabled} label={t("users.disableAccount")} description={t("users.disableHint")} />}
      </form>
    </Modal>
  );
}

function ResetPassword({ user, onClose, onDone }: { user: User | null; onClose: () => void; onDone: (pw: string) => void }) {
  const { t } = useTranslation();
  const errorText = useErrorText();
  const [resetTotp, setResetTotp] = useState(false);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => {
    setResetTotp(false);
    setError(null);
  }, [user]);
  return (
    <Modal
      open={!!user}
      onClose={onClose}
      title={t("users.resetPassword")}
      footer={
        <>
          <Button variant="secondary" onClick={onClose}>
            {t("common.cancel")}
          </Button>
          <Button
            onClick={() =>
              user &&
              post<{ oneTimePassword: string }>(`/users/${user.id}/reset-password`, { resetTotp })
                .then((r) => onDone(r.oneTimePassword))
                .catch((e) => setError(errorText(e)))
            }
          >
            {t("users.resetPassword")}
          </Button>
        </>
      }
    >
      {error && <Alert tone="danger">{error}</Alert>}
      <p className="text-sm">{t("users.resetHint", { user: user?.username })}</p>
      {user?.totpEnabled && <Switch checked={resetTotp} onChange={setResetTotp} label={t("users.resetTotp")} />}
    </Modal>
  );
}

function RolesCard() {
  const { t } = useTranslation();
  const [list, setList] = useState<{ role: Role; permissions: string[] }[]>([]);
  useEffect(() => {
    get<{ role: Role; permissions: string[] }[]>("/roles").then(setList).catch(() => undefined);
  }, []);
  return (
    <Card title={t("users.rolesTitle")} description={t("users.rolesSubtitle")}>
      <div className="grid gap-4 md:grid-cols-3">
        {list.map((r) => (
          <div key={r.role} className="rounded-md border border-line p-4">
            <p className="font-medium">{t(`roles.${r.role}`)}</p>
            <p className="mt-0.5 text-xs text-muted">{t(`roles.${r.role}Desc`)}</p>
            <ul className="mt-3 space-y-1 text-xs">
              {r.permissions.map((p) => (
                <li key={p} className="flex items-center gap-1.5">
                  <span className="size-1.5 rounded-full bg-primary" />
                  {t(`permissions.${p.replace(".", "_")}`)}
                </li>
              ))}
            </ul>
          </div>
        ))}
      </div>
    </Card>
  );
}
