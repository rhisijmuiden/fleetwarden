// Fails when the English and Dutch translation files do not have the same keys.
import { readFileSync } from "node:fs";

const load = (lang) => JSON.parse(readFileSync(new URL(`../src/locales/${lang}.json`, import.meta.url)));
const keys = (obj, prefix = "") =>
  Object.entries(obj).flatMap(([k, v]) =>
    v && typeof v === "object" ? keys(v, `${prefix}${k}.`) : [`${prefix}${k}`],
  );

const en = new Set(keys(load("en")));
const nl = new Set(keys(load("nl")));
const missingNl = [...en].filter((k) => !nl.has(k));
const missingEn = [...nl].filter((k) => !en.has(k));
if (missingNl.length || missingEn.length) {
  if (missingNl.length) console.error("Missing in nl.json:", missingNl.join(", "));
  if (missingEn.length) console.error("Missing in en.json:", missingEn.join(", "));
  process.exit(1);
}
console.log(`i18n OK: ${en.size} keys in en and nl`);
