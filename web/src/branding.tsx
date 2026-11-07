import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from "react";
import defaultLogo from "../../branding/logo.svg";
import { get, type Branding } from "./api";

type BrandingState = {
  branding: Branding;
  logoUrl: string;
  reload: () => void;
};

const fallback: Branding = {
  productName: "FleetWarden",
  primaryColor: "",
  primaryColorDark: "",
  sidebarColor: "",
  hasLogo: false,
  hasFavicon: false,
  revision: 0,
};

const BrandingContext = createContext<BrandingState>({ branding: fallback, logoUrl: defaultLogo, reload: () => {} });

// Mixes a hex colour with black (amount > 0) or white (amount < 0).
export function shade(hex: string, amount: number): string {
  const n = parseInt(hex.slice(1), 16);
  const target = amount > 0 ? 0 : 255;
  const a = Math.abs(amount);
  const ch = (v: number) => Math.round(v + (target - v) * a);
  const r = ch((n >> 16) & 255);
  const g = ch((n >> 8) & 255);
  const b = ch(n & 255);
  return `#${((1 << 24) | (r << 16) | (g << 8) | b).toString(16).slice(1)}`;
}

// Picks black or white text for a background colour (WCAG relative luminance).
export function contrastText(hex: string): string {
  const n = parseInt(hex.slice(1), 16);
  const lin = (c: number) => {
    const s = c / 255;
    return s <= 0.03928 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4;
  };
  const l = 0.2126 * lin((n >> 16) & 255) + 0.7152 * lin((n >> 8) & 255) + 0.0722 * lin(n & 255);
  return l > 0.4 ? "#0f1729" : "#ffffff";
}

function applyBranding(b: Branding) {
  // White-label colours override the default theme tokens from branding/theme.css.
  let css = "";
  if (b.primaryColor) {
    css += `:root{--fw-primary:${b.primaryColor};--fw-primary-hover:${shade(b.primaryColor, 0.15)};--fw-primary-contrast:${contrastText(b.primaryColor)}}`;
  }
  const dark = b.primaryColorDark || b.primaryColor;
  if (dark) {
    css += `:root[data-theme="dark"]{--fw-primary:${dark};--fw-primary-hover:${shade(dark, -0.15)};--fw-primary-contrast:${contrastText(dark)}}`;
  }
  if (b.sidebarColor) {
    css += `:root,:root[data-theme="dark"]{--fw-sidebar:${b.sidebarColor};--fw-sidebar-active:${shade(b.sidebarColor, -0.12)}}`;
  }
  let style = document.getElementById("fw-branding") as HTMLStyleElement | null;
  if (!style) {
    style = document.createElement("style");
    style.id = "fw-branding";
    document.head.appendChild(style);
  }
  style.textContent = css;
  document.title = b.productName;
  const icon = b.hasFavicon
    ? `/api/v1/branding/favicon?v=${b.revision}`
    : b.hasLogo
      ? `/api/v1/branding/logo?v=${b.revision}`
      : defaultLogo;
  document.querySelector<HTMLLinkElement>("#favicon")?.setAttribute("href", icon);
}

export function BrandingProvider({ children }: { children: ReactNode }) {
  const [branding, setBranding] = useState<Branding>(fallback);
  const reload = useCallback(() => {
    get<Branding>("/branding")
      .then((b) => {
        setBranding(b);
        applyBranding(b);
      })
      .catch(() => applyBranding(fallback));
  }, []);
  useEffect(reload, [reload]);
  const value = useMemo(
    () => ({ branding, reload, logoUrl: branding.hasLogo ? `/api/v1/branding/logo?v=${branding.revision}` : defaultLogo }),
    [branding, reload],
  );
  return <BrandingContext.Provider value={value}>{children}</BrandingContext.Provider>;
}

export const useBranding = () => useContext(BrandingContext);
