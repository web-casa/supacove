import { defineI18n } from "fumadocs-core/i18n";

// English is the default locale and is served without a prefix (/, /docs/…)
// from app/(en); Chinese lives under /zh in app/zh. Both render the shared
// implementations in routes/.
// scripts/check-parity.mjs enforces identical slug sets in CI/build.
export const i18n = defineI18n({
  languages: ["en", "zh"] as const,
  defaultLanguage: "en",
  hideLocale: "default-locale",
  // content lives in per-locale DIRECTORIES (content/docs/<locale>/<slug>.mdx)
  parser: "dir",
});

export type Lang = (typeof i18n.languages)[number];
