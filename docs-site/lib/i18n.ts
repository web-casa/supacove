import { defineI18n } from "fumadocs-core/i18n";

// zh is the default locale so a bare path lands on Chinese; `en` is the full
// mirror. scripts/check-parity.mjs enforces identical slug sets in CI/build.
export const i18n = defineI18n({
  languages: ["zh", "en"] as const,
  defaultLanguage: "zh",
  // keep the locale prefix on every URL (incl. default) so links are
  // unambiguous and the parity script's slug sets map 1:1 to routes
  hideLocale: "never",
  // content lives in per-locale DIRECTORIES (content/docs/<locale>/<slug>.mdx)
  parser: "dir",
});
