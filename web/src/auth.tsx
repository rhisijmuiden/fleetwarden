import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from "react";
import i18n from "./i18n";
import { ApiError, get, post, put, setUnauthenticatedHandler, type Me, type Permission } from "./api";

type AuthState = {
  me: Me | null;
  loading: boolean;
  setMe: (me: Me | null) => void;
  refresh: () => Promise<void>;
  logout: () => Promise<void>;
  can: (p: Permission) => boolean;
  setLanguage: (lang: "en" | "nl") => void;
};

const AuthContext = createContext<AuthState | null>(null);

export function AuthProvider({ children }: { children: ReactNode }) {
  const [me, setMeState] = useState<Me | null>(null);
  const [loading, setLoading] = useState(true);

  const setMe = useCallback((next: Me | null) => {
    setMeState(next);
    // The interface follows the language saved for the user.
    if (next && next.language !== i18n.resolvedLanguage) void i18n.changeLanguage(next.language);
  }, []);

  const refresh = useCallback(async () => {
    try {
      setMe(await get<Me>("/auth/me"));
    } catch (e) {
      if (e instanceof ApiError && e.status === 401) setMe(null);
      else throw e;
    } finally {
      setLoading(false);
    }
  }, [setMe]);

  useEffect(() => {
    setUnauthenticatedHandler(() => setMeState(null));
    refresh().catch(() => setLoading(false));
    return () => setUnauthenticatedHandler(null);
  }, [refresh]);

  const logout = useCallback(async () => {
    await post("/auth/logout").catch(() => undefined);
    setMeState(null);
  }, []);

  const setLanguage = useCallback(
    (lang: "en" | "nl") => {
      void i18n.changeLanguage(lang);
      if (me) {
        setMeState({ ...me, language: lang });
        put("/me/language", { language: lang }).catch(() => undefined);
      }
    },
    [me],
  );

  const value = useMemo<AuthState>(
    () => ({ me, loading, setMe, refresh, logout, setLanguage, can: (p) => !!me?.permissions.includes(p) }),
    [me, loading, setMe, refresh, logout, setLanguage],
  );
  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}

export function useAuth(): AuthState {
  const ctx = useContext(AuthContext);
  if (!ctx) throw new Error("useAuth outside AuthProvider");
  return ctx;
}
