import type { MetadataRoute } from "next";
import { i18n } from "@/lib/i18n";
import { absoluteUrl } from "@/lib/site";
import { source } from "@/lib/source";

/** Locale-independent path of a page URL: "/zh/docs/x" → "/docs/x". */
const strip = (url: string) => url.replace(/^\/zh(?=\/|$)/, "");

const languages = (path: string) => ({
  ...Object.fromEntries(i18n.languages.map((lang) => [lang, absoluteUrl(lang, path)])),
  "x-default": absoluteUrl(i18n.defaultLanguage, path),
});

export default function sitemap(): MetadataRoute.Sitemap {
  const home = i18n.languages.map((lang) => ({
    url: absoluteUrl(lang),
    alternates: { languages: languages("") },
  }));
  const pages = i18n.languages.flatMap((lang) =>
    source.getPages(lang).map((page) => {
      const path = strip(page.url);
      return {
        url: absoluteUrl(lang, path),
        // Git commit time when known; never the build time.
        lastModified: page.data.lastModified,
        alternates: { languages: languages(path) },
      };
    }),
  );
  return [...home, ...pages];
}
