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

// Every literal key passed to t("...") in the source must exist.
import { readdirSync, statSync } from "node:fs";
import { join } from "node:path";
const srcDir = new URL("../src", import.meta.url).pathname;
const files = [];
const walk = (d) => {
  for (const f of readdirSync(d)) {
    const p = join(d, f);
    if (statSync(p).isDirectory()) walk(p);
    else if (/\.tsx?$/.test(f)) files.push(p);
  }
};
walk(srcDir);
const used = new Set();
for (const f of files) {
  for (const m of readFileSync(f, "utf8").matchAll(/\bt\(\s*"([a-zA-Z0-9_.-]+)"/g)) used.add(m[1]);
}
// Plural keys exist as key_one / key_other (i18next picks one by count).
const missing = [...used].filter((k) => !en.has(k) && !(en.has(`${k}_one`) && en.has(`${k}_other`)));
if (missing.length) {
  console.error("Used in the source but missing in en.json:", missing.join(", "));
  process.exit(1);
}
console.log(`i18n OK: ${used.size} literal keys used in the source all exist`);
