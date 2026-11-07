import i18n from "i18next";
import { initReactI18next } from "react-i18next";
import en from "./locales/en.json";
import nl from "./locales/nl.json";

export const languages = [
  { code: "en", label: "English" },
  { code: "nl", label: "Nederlands" },
] as const;

function initialLanguage(): string {
  try {
    const saved = localStorage.getItem("fw.lang");
    if (saved === "en" || saved === "nl") return saved;
  } catch {
    /* storage unavailable */
  }
  return "en"; // English is the default; Dutch is opt-in per user.
}

void i18n.use(initReactI18next).init({
  resources: { en: { translation: en }, nl: { translation: nl } },
  lng: initialLanguage(),
  fallbackLng: "en",
  interpolation: { escapeValue: false },
});

i18n.on("languageChanged", (lng) => {
  document.documentElement.lang = lng;
  try {
    localStorage.setItem("fw.lang", lng);
  } catch {
    /* storage unavailable */
  }
});

export default i18n;
