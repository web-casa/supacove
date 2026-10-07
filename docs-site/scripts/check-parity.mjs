// Both locales must ship the identical slug set; a one-sided page is a
// silent documentation regression (product parity standard, docs site).
import { readdirSync, statSync } from "node:fs";
import { join } from "node:path";

const root = new URL("../content/docs/", import.meta.url).pathname;

function slugs(dir) {
  const out = [];
  for (const entry of readdirSync(dir)) {
    const full = join(dir, entry);
    if (statSync(full).isDirectory()) out.push(...slugs(full).map((s) => `${entry}/${s}`));
    else if (entry.endsWith(".mdx")) out.push(entry.replace(/\.mdx$/, ""));
  }
  return out.sort();
}

const zh = slugs(join(root, "zh"));
const en = slugs(join(root, "en"));
const missingEn = zh.filter((s) => !en.includes(s));
const missingZh = en.filter((s) => !zh.includes(s));
if (missingEn.length || missingZh.length) {
  console.error("locale parity broken:");
  for (const s of missingEn) console.error(`  en missing: ${s}`);
  for (const s of missingZh) console.error(`  zh missing: ${s}`);
  process.exit(1);
}
console.log(`parity ok: ${zh.length} slugs in both locales`);
